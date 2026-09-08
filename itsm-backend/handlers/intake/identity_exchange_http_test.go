package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent/externalidentity"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/middleware"
	"net/http/httptest"
	"testing"
)

func TestIdentityExchangeRoutesScopeAndLiveMapping(t *testing.T) {
	client, app, identity, command, _, _ := intakeFixture(t)
	cfg, a, n := assertionFixture()
	ctx := context.Background()
	mapping := client.ExternalIdentity.Create().SetTenantID(identity.TenantID).SetUserID(identity.ActorID).SetProvider(a.Provider).SetWorkspace(a.Workspace).SetSubject(a.Subject).SaveX(ctx)
	repo := NewIdentityRepository(client, client, authorization.NewSessionReader(client, sameTransactionDirectory{}))
	s := NewIdentityExchangeService(cfg.config, n, repo, "test-jwt-key")
	s.now = cfg.now
	r := gin.New()
	NewHandler(s, app).RegisterRoutes(r.Group("/api/v1"))
	r.GET("/api/v1/general", middleware.AuthMiddleware("test-jwt-key"), func(c *gin.Context) { c.Status(204) })
	call := func(method, path string, body any, token string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	w := call("POST", "/api/v1/intake/identity-exchange", a, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var out struct {
		Data ExchangeResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, "Bearer", out.Data.TokenType)
	require.Equal(t, "intake:create", out.Data.Scope)
	claims, err := authentication.ValidateIntakeToken(out.Data.Token, "test-jwt-key")
	require.NoError(t, err)
	require.Equal(t, mapping.ID, claims.MappingID)
	token := out.Data.Token
	originalProvider := s.config.Providers["kaf"]
	for _, revoked := range []IdentityProvider{{Secret: "test-only-key", Channels: []string{"other"}, Purposes: []string{"create"}}, {Secret: "test-only-key", Channels: []string{"kaf_web"}, Purposes: []string{"read"}}, {Channels: []string{"kaf_web"}, Purposes: []string{"create"}}} {
		s.config.Providers["kaf"] = revoked
		require.Equal(t, 401, call("POST", "/api/v1/intake/work-items", command, token).Code)
	}
	delete(s.config.Providers, "kaf")
	require.Equal(t, 401, call("POST", "/api/v1/intake/work-items", command, token).Code)
	s.config.Providers["kaf"] = originalProvider

	require.NotEqual(t, 204, call("GET", "/api/v1/general", nil, token).Code)
	require.Equal(t, 403, call("GET", "/api/v1/intake/catalog-items", nil, token).Code)
	w = call("POST", "/api/v1/intake/work-items", command, token)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, 200, call("POST", "/api/v1/intake/work-items", command, token).Code)
	a.Purpose = "read"
	a.Nonce = "read-nonce"
	a.Signature = assertionTestSignature(a)
	w = call("POST", "/api/v1/intake/identity-exchange/read", a, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, "intake:catalog:read intake:workitem:read", out.Data.Scope)
	require.Equal(t, 403, call("POST", "/api/v1/intake/work-items", command, out.Data.Token).Code)
	client.ExternalIdentity.Update().Where(externalidentity.IDEQ(mapping.ID)).SetActive(false).AddVersion(1).SaveX(ctx)
	require.Equal(t, 401, call("POST", "/api/v1/intake/work-items", command, token).Code)
	_, err = repo.Validate(tenantctx.WithTenantID(ctx, identity.TenantID), claims)
	require.Error(t, err)
}
func TestIdentityExchangeRejectsRoleAndUnknownAssertionFields(t *testing.T) {
	s, _, _ := assertionFixture()
	r := gin.New()
	NewHandler(s, nil).RegisterRoutes(r.Group("/api/v1"))
	for _, raw := range []string{`{"role":"admin"}`, `{"scope":["*"]}`, `{"subject":"one","subject":"two"}`, `null`} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/intake/identity-exchange", bytes.NewBufferString(raw)))
		require.Equal(t, 400, w.Code, w.Body.String())
	}
}

func TestIdentityKAFIncidentSourceUsesVerifiedAssertion(t *testing.T) {
	client, app, i, command := graphFixture(t)
	cfg, a, n := assertionFixture()
	client.ExternalIdentity.Create().SetTenantID(i.TenantID).SetUserID(i.ActorID).SetProvider(a.Provider).SetWorkspace(a.Workspace).SetSubject(a.Subject).SaveX(context.Background())
	repo := NewIdentityRepository(client, client, authorization.NewSessionReader(client, sameTransactionDirectory{}))
	s := NewIdentityExchangeService(cfg.config, n, repo, "test-jwt-key")
	s.now = cfg.now
	r := gin.New()
	NewHandler(s, app).RegisterRoutes(r.Group("/api/v1"))
	result, err := s.Exchange(context.Background(), a, "create")
	require.NoError(t, err)
	call := func() *httptest.ResponseRecorder {
		raw, _ := json.Marshal(command)
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/intake/work-items", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+result.Token)
		r.ServeHTTP(w, req)
		return w
	}
	w := call()
	require.Equal(t, 201, w.Code, w.Body.String())
	command.SourceReference = &creation.SourceReference{Provider: "kaf", EventID: "forged"}
	require.Equal(t, 403, call().Code)
	command.SourceReference = nil
	command.Incident = &creation.IncidentInput{Source: "system"}
	require.Equal(t, 403, call().Code)
}

func TestIdentityAssertionRejectsNonCanonicalJSONNames(t *testing.T) {
	s, a, _ := assertionFixture()
	r := gin.New()
	NewHandler(s, nil).RegisterRoutes(r.Group("/api/v1"))
	raw, _ := json.Marshal(a)
	raw = bytes.Replace(raw, []byte(`"version"`), []byte(`"Version"`), 1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/intake/identity-exchange", bytes.NewReader(raw)))
	require.Equal(t, 400, w.Code, w.Body.String())
}
