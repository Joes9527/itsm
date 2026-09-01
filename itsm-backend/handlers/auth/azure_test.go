package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"itsm-backend/authentication"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type stubAzureProvider struct {
	identity *azureUserInfo
}

func (p stubAzureProvider) Exchange(context.Context, AzureConfig, string) (*azureTokenResponse, error) {
	return &azureTokenResponse{AccessToken: "provider-access-token"}, nil
}

func (p stubAzureProvider) UserInfo(context.Context, string) (*azureUserInfo, error) {
	return p.identity, nil
}

func azureTestClient(t *testing.T) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:azure-session-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func azureTestConfig(tenantCode string) AzureConfig {
	return AzureConfig{TenantID: "azure-tenant", ClientID: "client", ClientSecret: "secret", RedirectURI: "https://itsm.example/api/v1/auth/azure/callback", ITSMTenantCode: tenantCode}
}

func TestAzureLoginStateCookieUsesCanonicalSecurePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/auth/azure/login", AzureLoginHandler(azureTestConfig("azure-default"), zap.NewNop().Sugar()))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/azure/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	state := findCookie(rec.Result().Cookies(), "azure_oauth_state")
	require.NotNil(t, state)
	require.True(t, state.HttpOnly)
	require.True(t, state.Secure)
	require.Equal(t, http.SameSiteLaxMode, state.SameSite)
	bound, err := parseTenantBoundState(state.Value)
	require.NoError(t, err)
	require.Equal(t, "azure-default", bound.TenantCode)
}

func TestAzureLoginFailsClosedWithoutExplicitITSMTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/login", AzureLoginHandler(azureTestConfig(""), zap.NewNop().Sugar()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	require.NotEqual(t, http.StatusTemporaryRedirect, rec.Code)
	require.Nil(t, findCookie(rec.Result().Cookies(), "azure_oauth_state"))
}

func TestAzureLoginBindsExplicitUserSelectedTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/login", AzureLoginHandler(azureTestConfig("configured-default"), zap.NewNop().Sugar()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login?tenantCode=selected-customer", nil))
	require.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	state := findCookie(rec.Result().Cookies(), "azure_oauth_state")
	require.NotNil(t, state)
	bound, err := parseTenantBoundState(state.Value)
	require.NoError(t, err)
	require.Equal(t, "selected-customer", bound.TenantCode)
}

func TestAzureCallbackIssuesCanonicalTenantBoundCookieSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	client := azureTestClient(t)
	tenant := client.Tenant.Create().
		SetName("Azure Tenant").SetCode("azure-" + strconv.FormatInt(now.UnixNano(), 10)).
		SetType("standard").SetStatus("active").SaveX(t.Context())
	user := client.User.Create().
		SetUsername("azure-user").SetEmail("azure@example.test").SetName("Azure User").
		SetPasswordHash("azure_oidc_no_password").SetRole("end_user").SetTenantID(tenant.ID).SetActive(true).SaveX(t.Context())

	secret := "azure-session-secret"
	handler := azureCallbackHandler(azureTestConfig(tenant.Code), client, secret, zap.NewNop().Sugar(), stubAzureProvider{
		identity: &azureUserInfo{OID: "oid", DisplayName: user.Name, Mail: user.Email},
	}, func() time.Time { return now })
	router := gin.New()
	router.GET("/api/v1/auth/azure/callback", handler)
	state, err := generateTenantBoundState(tenant.Code)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/azure/callback?code=ok&state="+state, nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.AddCookie(&http.Cookie{Name: "azure_oauth_state", Value: state})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	access := findCookie(rec.Result().Cookies(), "access_token")
	refresh := findCookie(rec.Result().Cookies(), "refresh_token")
	require.NotNil(t, access)
	require.NotNil(t, refresh)
	require.True(t, access.HttpOnly)
	require.True(t, refresh.HttpOnly)
	require.True(t, access.Secure)
	require.True(t, refresh.Secure)
	require.Equal(t, int(authentication.AccessTokenTTL.Seconds()), access.MaxAge)
	require.Equal(t, int(authentication.RefreshTokenTTL.Seconds()), refresh.MaxAge)

	parsed, err := jwt.ParseWithClaims(access.Value, &authentication.Claims{}, func(*jwt.Token) (any, error) { return []byte(secret), nil })
	require.NoError(t, err)
	claims := parsed.Claims.(*authentication.Claims)
	require.Equal(t, user.ID, claims.UserID)
	require.Equal(t, tenant.ID, claims.TenantID)
	require.Equal(t, "access", claims.TokenType)
	require.WithinDuration(t, now.Add(authentication.AccessTokenTTL), claims.ExpiresAt.Time, 2*time.Second)

	validated, err := authentication.NewRefreshTokenConsumer(secret, nil).Validate(refresh.Value)
	require.NoError(t, err)
	require.Equal(t, tenant.ID, validated.Identity().TenantID)
	require.Equal(t, user.ID, validated.Identity().UserID)
	require.NotContains(t, rec.Body.String(), access.Value)
	require.NotContains(t, rec.Body.String(), refresh.Value)
}

func TestAzureCallbackFailsClosedWithoutCookiesForDisabledActorOrTenant(t *testing.T) {
	tests := []struct {
		name         string
		userActive   bool
		tenantStatus string
		expiresAt    time.Time
	}{
		{name: "disabled actor", userActive: false, tenantStatus: "active"},
		{name: "inactive tenant", userActive: true, tenantStatus: "suspended"},
		{name: "expired tenant", userActive: true, tenantStatus: "active", expiresAt: time.Now().Add(-time.Minute)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			client := azureTestClient(t)
			create := client.Tenant.Create().SetName(tt.name).SetCode("azure-" + strconv.FormatInt(time.Now().UnixNano(), 10)).SetType("standard").SetStatus(tt.tenantStatus)
			if !tt.expiresAt.IsZero() {
				create.SetExpiresAt(tt.expiresAt)
			}
			tenant := create.SaveX(t.Context())
			email := "azure-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.test"
			client.User.Create().SetUsername(email).SetEmail(email).SetName(tt.name).SetPasswordHash("oidc").SetRole("end_user").SetTenantID(tenant.ID).SetActive(tt.userActive).SaveX(t.Context())

			handler := azureCallbackHandler(azureTestConfig(tenant.Code), client, "secret", zap.NewNop().Sugar(), stubAzureProvider{identity: &azureUserInfo{Mail: email}}, time.Now)
			router := gin.New()
			router.GET("/callback", handler)
			state, stateErr := generateTenantBoundState(tenant.Code)
			require.NoError(t, stateErr)
			req := httptest.NewRequest(http.MethodGet, "/callback?code=ok&state="+state, nil)
			req.AddCookie(&http.Cookie{Name: "azure_oauth_state", Value: state})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			require.NotEqual(t, http.StatusTemporaryRedirect, rec.Code)
			require.Nil(t, findCookie(rec.Result().Cookies(), "access_token"))
			require.Nil(t, findCookie(rec.Result().Cookies(), "refresh_token"))
		})
	}
}

func TestAzureCallbackDoesNotCrossTenantOrImplicitlyProvision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := azureTestClient(t)
	ctx := t.Context()
	tenantA := client.Tenant.Create().SetName("A").SetCode("azure-a").SetType("standard").SetStatus("active").SaveX(ctx)
	tenantB := client.Tenant.Create().SetName("B").SetCode("azure-b").SetType("standard").SetStatus("active").SaveX(ctx)
	client.User.Create().SetUsername("azure-existing").SetEmail("same@example.test").SetName("Existing").
		SetPasswordHash("oidc").SetRole("end_user").SetTenantID(tenantA.ID).SetActive(true).SaveX(ctx)
	state, err := generateTenantBoundState(tenantB.Code)
	require.NoError(t, err)
	handler := azureCallbackHandler(azureTestConfig(tenantB.Code), client, "secret", zap.NewNop().Sugar(), stubAzureProvider{
		identity: &azureUserInfo{Mail: "same@example.test", DisplayName: "Same"},
	}, time.Now)
	router := gin.New()
	router.GET("/callback", handler)
	req := httptest.NewRequest(http.MethodGet, "/callback?code=ok&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: "azure_oauth_state", Value: state})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusTemporaryRedirect, rec.Code)
	require.Nil(t, findCookie(rec.Result().Cookies(), "access_token"))
	require.Equal(t, 1, client.User.Query().CountX(ctx))
}

func TestAzureCallbackUsesExactActorThenAuthorizesSelectedTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := azureTestClient(t)
	ctx := t.Context()
	actorTenant := client.Tenant.Create().SetName("Actor").SetCode("azure-actor").SetType("standard").SetStatus("active").SaveX(ctx)
	targetTenant := client.Tenant.Create().SetName("Target").SetCode("azure-target").SetType("standard").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetUsername("azure-super").SetEmail("azure-super@example.test").SetName("Azure Super").
		SetPasswordHash("oidc").SetRole("super_admin").SetTenantID(actorTenant.ID).SetActive(true).SaveX(ctx)
	state, err := generateTenantBoundState(targetTenant.Code)
	require.NoError(t, err)
	handler := azureCallbackHandler(azureTestConfig(targetTenant.Code), client, "secret", zap.NewNop().Sugar(), stubAzureProvider{
		identity: &azureUserInfo{Mail: actor.Email, DisplayName: actor.Name},
	}, time.Now)
	router := gin.New()
	router.GET("/callback", handler)
	req := httptest.NewRequest(http.MethodGet, "/callback?code=ok&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: "azure_oauth_state", Value: state})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	refresh := findCookie(rec.Result().Cookies(), "refresh_token")
	require.NotNil(t, refresh)
	claims, err := authentication.NewRefreshTokenConsumer("secret", nil).Validate(refresh.Value)
	require.NoError(t, err)
	require.Equal(t, actor.ID, claims.Identity().UserID)
	require.Equal(t, targetTenant.ID, claims.Identity().TenantID)
}

func TestAzureCallbackProvisionsOnlyWhenExplicitlyEnabledForSelectedTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := azureTestClient(t)
	ctx := t.Context()
	targetTenant := client.Tenant.Create().SetName("Provision target").SetCode("azure-provision").SetType("standard").SetStatus("active").SaveX(ctx)
	state, err := generateTenantBoundState(targetTenant.Code)
	require.NoError(t, err)
	cfg := azureTestConfig(targetTenant.Code)
	cfg.AllowUserProvisioning = true
	handler := azureCallbackHandler(cfg, client, "secret", zap.NewNop().Sugar(), stubAzureProvider{
		identity: &azureUserInfo{Mail: "new-azure@example.test", DisplayName: "New Azure"},
	}, time.Now)
	router := gin.New()
	router.GET("/callback", handler)
	req := httptest.NewRequest(http.MethodGet, "/callback?code=ok&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: "azure_oauth_state", Value: state})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	created := client.User.Query().OnlyX(ctx)
	require.Equal(t, targetTenant.ID, created.TenantID)
	require.Equal(t, "new-azure@example.test", created.Email)
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
