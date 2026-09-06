//go:build integration_postgres

package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signIntakeAssertion(a intake.IdentityAssertion) string {
	m := hmac.New(sha256.New, []byte("test-only-exchange"))
	m.Write([]byte(strings.Join([]string{"2", a.Audience, a.Purpose, a.Provider, a.Workspace, a.Subject, a.Channel, a.EventID, strconv.FormatInt(a.IssuedAt, 10), a.Nonce}, "\n")))
	return hex.EncodeToString(m.Sum(nil))
}
func TestPostgresIdentityExchangeRestrictedPoolsMappingAndMSP(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("agent").SetName("Agent").SaveX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("identity-test").SetName("Identity test").SetResource("*").SetAction("*").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	m := f.client.ExternalIdentity.Create().SetTenantID(f.tenant.ID).SetUserID(f.actor.ID).SetProvider("kaf").SetWorkspace("workspace-test").SetSubject("subject-test").SaveX(f.ctx)
	clients, cfg := runtimeClients(t, f)
	sessions := authorization.NewSessionReader(clients.Tenant, clients.IntakeDirectorySnapshot())
	repo := intake.NewIdentityRepository(clients.Tenant, clients.System, sessions)
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:36445", DB: 0, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { redisClient.Close() })
	require.NoError(t, redisClient.Ping(f.ctx).Err())
	ex := intake.NewIdentityExchangeService(intake.IdentityExchangeConfig{Providers: map[string]intake.IdentityProvider{"kaf": {Secret: "test-only-exchange", Channels: []string{"kaf_web"}, Purposes: []string{"create", "read"}}}, MaxAge: time.Minute, FutureSkew: 5 * time.Second, TokenTTL: time.Minute}, intake.NewRedisNonceStore(redisClient), repo, "test-only-jwt")
	a := intake.IdentityAssertion{Version: 2, Audience: "itsm-intake", Purpose: "read", Provider: "kaf", Workspace: "workspace-test", Subject: "subject-test", Channel: "kaf_web", EventID: "submission-test", IssuedAt: time.Now().Unix(), Nonce: uuid.NewString()}
	a.Signature = signIntakeAssertion(a)
	result, err := ex.Exchange(context.Background(), a, "read")
	require.NoError(t, err)
	require.Equal(t, "intake:catalog:read intake:workitem:read", result.Scope)
	claims, err := authentication.ValidateIntakeToken(result.Token, "test-only-jwt")
	require.NoError(t, err)
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	i, err := repo.Validate(ctx, claims)
	require.NoError(t, err)
	require.Equal(t, f.tenant.ID, i.ActorTenantID)
	view, err := intake.NewReadService(sessions, nil, "test-cursor").WorkItem(ctx, i, f.inc.WorkItemID)
	require.NoError(t, err)
	require.Equal(t, "incident", view.RecordClass)
	require.Equal(t, "new", view.Status)
	require.Nil(t, view.AccessResult)
	_, err = ex.Exchange(context.Background(), a, "read")
	require.ErrorIs(t, err, creation.ErrAuthenticationRequired)
	// Same nonce cannot move to a different purpose even after re-signing.
	a.Purpose = "create"
	a.Signature = signIntakeAssertion(a)
	_, err = ex.Exchange(context.Background(), a, "create")
	require.ErrorIs(t, err, creation.ErrAuthenticationRequired)
	_, err = clients.System.ExternalIdentity.UpdateOneID(m.ID).SetActive(false).Save(f.ctx)
	require.ErrorContains(t, err, "permission denied")
	_, err = f.db.ExecContext(f.ctx, "GRANT UPDATE ON external_identities TO "+cfg.SystemRoleUser)
	require.NoError(t, err)
	_, err = database.InitRuntimeDatabases(&cfg, &config.RLSConfig{Mode: "enforce"}, nil)
	require.ErrorContains(t, err, "external_identities UPDATE")
	_, err = f.db.ExecContext(f.ctx, "REVOKE UPDATE ON external_identities FROM "+cfg.SystemRoleUser)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, "REVOKE SELECT ON external_identities FROM "+cfg.SystemRoleUser)
	require.NoError(t, err)
	a.Nonce = uuid.NewString()
	a.Signature = strings.Repeat("0", 64)
	_, err = ex.Exchange(context.Background(), a, "create")
	require.ErrorIs(t, err, creation.ErrAuthenticationRequired, "bad signature must fail before any identity DB lookup")
	a.Signature = signIntakeAssertion(a)
	_, err = ex.Exchange(context.Background(), a, "create")
	require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable, "DB fault is not an unmapped identity")
	_, err = f.db.ExecContext(f.ctx, "GRANT SELECT ON external_identities TO "+cfg.SystemRoleUser)
	require.NoError(t, err)
	manager := intake.NewIdentityMappingService(sessions, map[string]intake.IdentityProvider{"kaf": {}})
	updated, err := manager.Update(ctx, i, m.ID, m.Version, false)
	require.NoError(t, err)
	require.Equal(t, 2, updated.Version)
	_, err = repo.Validate(ctx, claims)
	require.ErrorIs(t, err, creation.ErrAuthenticationRequired)
	_, err = manager.Update(ctx, i, m.ID, 1, true)
	require.ErrorIs(t, err, creation.ErrIdempotencyConflict)
	// A native MSP actor mapped to a selected customer retains native provenance.
	provider := f.client.Tenant.Create().SetCode("identity-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("identity-msp").SetName("MSP").SetEmail("msp@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	mspRole := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(mspRole.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	f.client.ExternalIdentity.Create().SetTenantID(f.tenant.ID).SetUserID(actor.ID).SetProvider("kaf").SetWorkspace("workspace-test").SetSubject("msp-subject").SaveX(f.ctx)
	a.Subject = "msp-subject"
	a.Nonce = uuid.NewString()
	a.Signature = signIntakeAssertion(a)
	result, err = ex.Exchange(context.Background(), a, "create")
	require.NoError(t, err)
	claims, err = authentication.ValidateIntakeToken(result.Token, "test-only-jwt")
	require.NoError(t, err)
	i, err = repo.Validate(ctx, claims)
	require.NoError(t, err)
	require.Equal(t, provider.ID, i.ActorTenantID)
	require.Equal(t, f.tenant.ID, i.TenantID)
	require.Equal(t, "msp_tech", i.Role)
	f.client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).SaveX(f.ctx)
	_, err = repo.Validate(ctx, claims)
	require.ErrorIs(t, err, creation.ErrPermissionDenied)
}
