package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
	"itsm-backend/middleware"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

type memoryNonceStore struct {
	mu        sync.Mutex
	seen      map[string]bool
	unhealthy bool
}

func (s *memoryNonceStore) Claim(_ context.Context, nonce string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unhealthy {
		return false, fmt.Errorf("redis unavailable")
	}
	if s.seen == nil {
		s.seen = make(map[string]bool)
	}
	if s.seen[nonce] {
		return false, nil
	}
	s.seen[nonce] = true
	return true, nil
}

type identityExchangeFixture struct {
	t       *testing.T
	client  *ent.Client
	handler *IdentityExchangeHandler
	nonces  *memoryNonceStore
	now     time.Time
	request IdentityAssertion
	user    *ent.User
	mapping *ent.ExternalIdentity
}

func newIdentityExchangeFixture(t *testing.T, role string) *identityExchangeFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:identity-exchange-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Identity Tenant").SetCode(fmt.Sprintf("IDENTITY-%d", time.Now().UnixNano())).
		SetDomain(fmt.Sprintf("identity-%d.test", time.Now().UnixNano())).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	if role == "identity_exchange_creator" {
		roleEntity, roleErr := client.Role.Create().SetName("Identity Exchange Creator").SetCode(role).SetTenantID(tenant.ID).Save(ctx)
		require.NoError(t, roleErr)
		permissionEntity, permissionErr := client.Permission.Create().SetCode("intake:create").SetName("Intake Create").SetResource("intake").SetAction("create").SetTenantID(tenant.ID).Save(ctx)
		require.NoError(t, permissionErr)
		_, rolePermissionErr := client.RolePermission.Create().SetRoleID(roleEntity.ID).SetPermissionID(permissionEntity.ID).SetTenantID(tenant.ID).Save(ctx)
		require.NoError(t, rolePermissionErr)
		middleware.InvalidateRolePermissionCache(role, tenant.ID)
	}
	userEntity, err := client.User.Create().
		SetUsername(fmt.Sprintf("mapped-%d", time.Now().UnixNano())).SetEmail(fmt.Sprintf("mapped-%d@test.example", time.Now().UnixNano())).
		SetName("Mapped User").SetPasswordHash("x").SetRole(role).SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	mapping, err := client.ExternalIdentity.Create().
		SetTenantID(tenant.ID).SetProvider("microsoft").SetWorkspace("it-support").SetSubject("user-42").SetUserID(userEntity.ID).Save(ctx)
	require.NoError(t, err)
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)
	nonces := &memoryNonceStore{}
	handler := NewIdentityExchangeHandler(client, nonces, "exchange-secret", "jwt-secret", time.Minute, 5*time.Minute)
	handler.now = func() time.Time { return now }
	assertion := IdentityAssertion{
		Provider: "microsoft", Workspace: "it-support", Subject: "user-42", Channel: "teams",
		EventID: "message-42", IssuedAt: now.Unix(), Nonce: "nonce-42",
	}
	assertion.Signature = signIdentityAssertion(assertion, "exchange-secret")
	return &identityExchangeFixture{t: t, client: client, handler: handler, nonces: nonces, now: now, request: assertion, user: userEntity, mapping: mapping}
}

func (f *identityExchangeFixture) exchange(assertion IdentityAssertion) *httptest.ResponseRecorder {
	f.t.Helper()
	body, err := json.Marshal(assertion)
	require.NoError(f.t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/intake/identity-exchange", bytes.NewReader(body))
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = request
	c.Set("request_id", "req-exchange")
	f.handler.Exchange(c)
	return response
}

func responseData(t *testing.T, response *httptest.ResponseRecorder) IdentityExchangeResponse {
	t.Helper()
	var envelope struct {
		Data IdentityExchangeResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	return envelope.Data
}

func TestIdentityExchangeValidAssertionIssuesFiveMinuteScopedToken(t *testing.T) {
	f := newIdentityExchangeFixture(t, "identity_exchange_creator")
	response := f.exchange(f.request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	data := responseData(t, response)
	require.Equal(t, middleware.IntakeTokenType, data.TokenType)
	require.Equal(t, int64(300), data.ExpiresIn)
	require.Equal(t, []string{middleware.IntakeCreateScope}, data.Scope)

	parsed, err := jwt.ParseWithClaims(data.Token, &middleware.Claims{}, func(*jwt.Token) (any, error) {
		return []byte("jwt-secret"), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	claims := parsed.Claims.(*middleware.Claims)
	require.Equal(t, f.user.ID, claims.UserID)
	require.Equal(t, f.mapping.TenantID, claims.TenantID)
	require.Equal(t, "teams", claims.Channel)
	require.Equal(t, "microsoft", claims.Provider)
	require.Equal(t, middleware.IntakeTokenAudience, claims.Audience[0])
	require.Equal(t, 5*time.Minute, claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time))

	audit, err := f.client.AuditLog.Query().Where(auditlog.ActionEQ("intake.identity_exchange.succeeded")).Only(context.Background())
	require.NoError(t, err)
	require.NotNil(t, audit.RequestBody)
	require.Contains(t, *audit.RequestBody, claims.ID)
	require.NotContains(t, *audit.RequestBody, f.request.Signature)
	require.NotContains(t, *audit.RequestBody, f.request.Subject)
}

func TestIdentityExchangeRejectsInvalidOrReplayedAssertions(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*identityExchangeFixture, *IdentityAssertion)
		wantStatus int
		wantCode   string
	}{
		{name: "expired timestamp", mutate: func(f *identityExchangeFixture, a *IdentityAssertion) {
			a.IssuedAt = f.now.Add(-61 * time.Second).Unix()
			a.Signature = signIdentityAssertion(*a, "exchange-secret")
		}, wantStatus: http.StatusUnauthorized, wantCode: "ASSERTION_EXPIRED"},
		{name: "invalid hmac", mutate: func(_ *identityExchangeFixture, a *IdentityAssertion) { a.Signature = strings.Repeat("0", 64) }, wantStatus: http.StatusUnauthorized, wantCode: "INVALID_SIGNATURE"},
		{name: "replay protection unavailable", mutate: func(f *identityExchangeFixture, _ *IdentityAssertion) { f.nonces.unhealthy = true }, wantStatus: http.StatusServiceUnavailable, wantCode: "REPLAY_PROTECTION_UNAVAILABLE"},
		{name: "unknown workspace mapping", mutate: func(_ *identityExchangeFixture, a *IdentityAssertion) {
			a.Workspace = "other-workspace"
			a.Signature = signIdentityAssertion(*a, "exchange-secret")
		}, wantStatus: http.StatusUnauthorized, wantCode: "IDENTITY_NOT_MAPPED"},
		{name: "disabled mapping", mutate: func(f *identityExchangeFixture, _ *IdentityAssertion) {
			_, err := f.mapping.Update().SetActive(false).Save(context.Background())
			require.NoError(f.t, err)
		}, wantStatus: http.StatusUnauthorized, wantCode: "IDENTITY_NOT_MAPPED"},
		{name: "inactive mapped user", mutate: func(f *identityExchangeFixture, _ *IdentityAssertion) {
			_, err := f.user.Update().SetActive(false).Save(context.Background())
			require.NoError(f.t, err)
		}, wantStatus: http.StatusUnauthorized, wantCode: "MAPPED_USER_INACTIVE"},
		{name: "mapping cannot cross tenant to user", mutate: func(f *identityExchangeFixture, _ *IdentityAssertion) {
			ctx := context.Background()
			require.NoError(f.t, f.client.ExternalIdentity.DeleteOneID(f.mapping.ID).Exec(ctx))
			foreignTenant, err := f.client.Tenant.Create().SetName("Foreign Identity Tenant").SetCode(fmt.Sprintf("IDENTITY-FOREIGN-%d", time.Now().UnixNano())).SetDomain(fmt.Sprintf("identity-foreign-%d.test", time.Now().UnixNano())).SetStatus("active").Save(ctx)
			require.NoError(f.t, err)
			foreignUser, err := f.client.User.Create().SetUsername(fmt.Sprintf("foreign-mapped-%d", time.Now().UnixNano())).SetEmail(fmt.Sprintf("foreign-mapped-%d@test", time.Now().UnixNano())).SetName("Foreign Mapped User").SetPasswordHash("x").SetRole("super_admin").SetActive(true).SetTenantID(foreignTenant.ID).Save(ctx)
			require.NoError(f.t, err)
			_, err = f.client.ExternalIdentity.Create().SetTenantID(f.mapping.TenantID).SetProvider("microsoft").SetWorkspace("it-support").SetSubject("user-42").SetUserID(foreignUser.ID).Save(ctx)
			require.NoError(f.t, err)
		}, wantStatus: http.StatusUnauthorized, wantCode: "MAPPED_USER_INACTIVE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newIdentityExchangeFixture(t, "super_admin")
			assertion := f.request
			tc.mutate(f, &assertion)
			response := f.exchange(assertion)
			require.Equal(t, tc.wantStatus, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), tc.wantCode)
		})
	}

	t.Run("same nonce is accepted only once", func(t *testing.T) {
		f := newIdentityExchangeFixture(t, "super_admin")
		require.Equal(t, http.StatusOK, f.exchange(f.request).Code)
		second := f.exchange(f.request)
		require.Equal(t, http.StatusUnauthorized, second.Code)
		require.Contains(t, second.Body.String(), "ASSERTION_REPLAYED")
	})

	t.Run("current role without intake permission is denied", func(t *testing.T) {
		f := newIdentityExchangeFixture(t, "no_intake_permission")
		response := f.exchange(f.request)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "INTAKE_PERMISSION_DENIED")
	})
}
