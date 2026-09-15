package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	enttenant "itsm-backend/ent/tenant"
	entuser "itsm-backend/ent/user"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type atomicRefreshTokenStore struct {
	mu       sync.Mutex
	consumed map[string]struct{}
	err      error
}

func (s *atomicRefreshTokenStore) Consume(_ context.Context, token string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if _, exists := s.consumed[token]; exists {
		return authentication.ErrRefreshTokenConsumed
	}
	s.consumed[token] = struct{}{}
	return nil
}

type refreshServiceFixture struct {
	ctx      context.Context
	client   *ent.Client
	service  *Service
	consumer *authentication.RefreshTokenConsumer
	secret   string
}

func newRefreshServiceFixture(t *testing.T) *refreshServiceFixture {
	t.Helper()
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:common-refresh-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { _ = client.Close() })
	secret := "common-refresh-session-secret"
	consumer := authentication.NewRefreshTokenConsumer(secret, &atomicRefreshTokenStore{consumed: make(map[string]struct{})})
	return &refreshServiceFixture{
		ctx: ctx, client: client, consumer: consumer, secret: secret,
		service: NewService(NewEntRepository(client), secret, zap.NewNop().Sugar(), client, consumer, nil),
	}
}

func (f *refreshServiceFixture) tenant(name string, kind enttenant.Type, status string) *ent.Tenant {
	return f.client.Tenant.Create().
		SetName(name).SetCode(fmt.Sprintf("%s-%d", name, time.Now().UnixNano())).SetType(kind).SetStatus(status).SaveX(f.ctx)
}

func (f *refreshServiceFixture) user(tenantID int, username, role, mspRole string, active bool) *ent.User {
	builder := f.client.User.Create().
		SetUsername(username).SetEmail(username + "@example.test").SetName(username).
		SetPasswordHash("hash").SetTenantID(tenantID).SetRole(role).SetActive(active)
	if mspRole != "" {
		builder.SetMspRole(entuser.MspRole(mspRole))
	}
	return builder.SaveX(f.ctx)
}

func (f *refreshServiceFixture) token(user *ent.User, role string, tenantID int) string {
	token, err := authentication.GenerateRefreshToken(user.ID, user.Username, role, tenantID, f.secret, time.Hour)
	if err != nil {
		panic(err)
	}
	return token
}

func TestAuthResultNeverSerializesJWTs(t *testing.T) {
	payload, err := json.Marshal(&AuthResult{
		AccessToken:  "real-access.jwt.value",
		RefreshToken: "real-refresh.jwt.value",
		User:         &User{ID: 1, Username: "operator"},
	})
	require.NoError(t, err)
	require.NotContains(t, string(payload), "accessToken")
	require.NotContains(t, string(payload), "refreshToken")
	require.NotContains(t, string(payload), "real-access.jwt.value")
	require.NotContains(t, string(payload), "real-refresh.jwt.value")
}

func TestServiceRefreshTokenRotatesNativeTenantSession(t *testing.T) {
	fx := newRefreshServiceFixture(t)
	tenant := fx.tenant("native", "standard", "active")
	actor := fx.user(tenant.ID, "native-refresh", "end_user", "", true)
	original := fx.token(actor, "end_user", tenant.ID)

	first, err := fx.service.RefreshToken(fx.ctx, original)
	require.NoError(t, err)
	require.NotEqual(t, original, first.RefreshToken)
	rotated, err := fx.consumer.Validate(first.RefreshToken)
	require.NoError(t, err)
	require.Equal(t, tenant.ID, rotated.Identity().TenantID)
	require.Equal(t, "end_user", rotated.Identity().Role)

	_, err = fx.service.RefreshToken(fx.ctx, original)
	require.ErrorIs(t, err, authentication.ErrRefreshTokenConsumed)
	_, err = fx.service.RefreshToken(fx.ctx, first.RefreshToken)
	require.NoError(t, err)
}

func TestServiceRefreshTokenChecksActorAndTenantBeforeConsumption(t *testing.T) {
	t.Run("disabled user does not consume token", func(t *testing.T) {
		fx := newRefreshServiceFixture(t)
		tenant := fx.tenant("disabled-user", "standard", "active")
		actor := fx.user(tenant.ID, "disabled-refresh", "end_user", "", false)
		token := fx.token(actor, "end_user", tenant.ID)

		_, err := fx.service.RefreshToken(fx.ctx, token)
		require.ErrorContains(t, err, "inactive")
		fx.client.User.UpdateOneID(actor.ID).SetActive(true).ExecX(fx.ctx)
		_, err = fx.service.RefreshToken(fx.ctx, token)
		require.NoError(t, err)
	})

	t.Run("inactive tenant does not consume token", func(t *testing.T) {
		fx := newRefreshServiceFixture(t)
		tenant := fx.tenant("disabled-tenant", "standard", "suspended")
		actor := fx.user(tenant.ID, "tenant-disabled-refresh", "end_user", "", true)
		token := fx.token(actor, "end_user", tenant.ID)

		_, err := fx.service.RefreshToken(fx.ctx, token)
		require.ErrorIs(t, err, authorization.ErrTenantInactive)
		fx.client.Tenant.UpdateOneID(tenant.ID).SetStatus("active").ExecX(fx.ctx)
		_, err = fx.service.RefreshToken(fx.ctx, token)
		require.NoError(t, err)
	})

	t.Run("expired tenant does not consume token", func(t *testing.T) {
		fx := newRefreshServiceFixture(t)
		tenant := fx.tenant("expired-tenant", "standard", "active")
		fx.client.Tenant.UpdateOneID(tenant.ID).SetExpiresAt(time.Now().Add(-time.Minute)).ExecX(fx.ctx)
		actor := fx.user(tenant.ID, "tenant-expired-refresh", "end_user", "", true)
		token := fx.token(actor, "end_user", tenant.ID)

		_, err := fx.service.RefreshToken(fx.ctx, token)
		require.ErrorIs(t, err, authorization.ErrTenantExpired)
		fx.client.Tenant.UpdateOneID(tenant.ID).ClearExpiresAt().ExecX(fx.ctx)
		_, err = fx.service.RefreshToken(fx.ctx, token)
		require.NoError(t, err)
	})
}

func TestServiceRefreshTokenAuthorizesSelectedTenantContext(t *testing.T) {
	t.Run("allocated MSP", func(t *testing.T) {
		fx := newRefreshServiceFixture(t)
		provider := fx.tenant("msp-provider", "msp_provider", "active")
		customer := fx.tenant("msp-customer", "msp_customer", "active")
		actor := fx.user(provider.ID, "msp-refresh", "admin", "provider_agent", true)
		mappedRole := authorization.GetMSPRBACRole("provider_agent")
		token := fx.token(actor, mappedRole, customer.ID)

		_, err := fx.service.RefreshToken(fx.ctx, token)
		require.ErrorIs(t, err, authorization.ErrTenantAccessDenied)
		fx.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(customer.ID).SetRole("primary").SaveX(fx.ctx)
		result, err := fx.service.RefreshToken(fx.ctx, token)
		require.NoError(t, err)
		rotated, err := fx.consumer.Validate(result.RefreshToken)
		require.NoError(t, err)
		require.Equal(t, customer.ID, rotated.Identity().TenantID)
		require.Equal(t, mappedRole, rotated.Identity().Role)
	})

	t.Run("super admin", func(t *testing.T) {
		fx := newRefreshServiceFixture(t)
		origin := fx.tenant("super-origin", "standard", "active")
		target := fx.tenant("super-target", "standard", "active")
		actor := fx.user(origin.ID, "super-refresh", "super_admin", "", true)
		result, err := fx.service.RefreshToken(fx.ctx, fx.token(actor, "super_admin", target.ID))
		require.NoError(t, err)
		rotated, err := fx.consumer.Validate(result.RefreshToken)
		require.NoError(t, err)
		require.Equal(t, target.ID, rotated.Identity().TenantID)
	})
}

func TestServiceRefreshTokenFailsClosedWhenConsumerStoreUnavailable(t *testing.T) {
	fx := newRefreshServiceFixture(t)
	tenant := fx.tenant("store-unavailable", "standard", "active")
	actor := fx.user(tenant.ID, "store-unavailable-refresh", "end_user", "", true)
	consumer := authentication.NewRefreshTokenConsumer(fx.secret, nil)
	svc := NewService(NewEntRepository(fx.client), fx.secret, zap.NewNop().Sugar(), fx.client, consumer, nil)

	result, err := svc.RefreshToken(fx.ctx, fx.token(actor, "end_user", tenant.ID))
	require.Nil(t, result)
	var unavailable *authentication.RefreshTokenStoreUnavailableError
	require.True(t, errors.As(err, &unavailable))
}
