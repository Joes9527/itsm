package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdentityHTTPErrorDetailsStrictContract(t *testing.T) {
	client, app, i, command, _, _ := intakeFixture(t)
	cfg, a, n := assertionFixture()
	m := client.ExternalIdentity.Create().SetTenantID(i.TenantID).SetUserID(i.ActorID).SetProvider(a.Provider).SetWorkspace(a.Workspace).SetSubject(a.Subject).SaveX(context.Background())
	sessions := authorization.NewSessionReader(client, sameTransactionDirectory{})
	ex := NewIdentityExchangeService(cfg.config, n, NewIdentityRepository(client, client, sessions), "test-jwt")
	ex.now = cfg.now
	h := NewHandler(ex, app)
	h.SetReaders(NewReadService(sessions, nil, "test-cursor"))
	r := gin.New()
	h.RegisterRoutes(r.Group("/api/v1"))
	token := func(scopes []string) string {
		raw, err := authentication.GenerateIntakeToken(authentication.IntakeClaims{UserID: i.ActorID, TenantID: i.TenantID, Role: i.Role, TokenType: "intake", Scope: scopes, Provider: a.Provider, Channel: a.Channel, EventID: a.EventID, MappingID: m.ID, MappingVersion: 1}, "test-jwt", time.Minute)
		require.NoError(t, err)
		return raw
	}
	write, read := token([]string{"intake:create"}), token([]string{"intake:catalog:read", "intake:workitem:read"})
	call := func(method, path string, body any, credential string) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	require.Equal(t, 201, call("POST", "/api/v1/intake/work-items", command, write).Code)
	changed := command
	changed.Title = "Conflict"
	for _, tc := range []struct {
		status, code int
		errorCode    string
		request      func() *httptest.ResponseRecorder
	}{
		{400, 1001, "InvalidCommand", func() *httptest.ResponseRecorder {
			return call("POST", "/api/v1/intake/identity-exchange", map[string]any{"role": "admin"}, "")
		}},
		{401, 2001, "AuthenticationRequired", func() *httptest.ResponseRecorder { return call("GET", "/api/v1/intake/catalog-items", nil, "") }},
		{403, 2003, "PermissionDenied", func() *httptest.ResponseRecorder { return call("GET", "/api/v1/intake/catalog-items", nil, write) }},
		{404, 4004, "ReferenceNotFound", func() *httptest.ResponseRecorder { return call("GET", "/api/v1/intake/work-items/999999", nil, read) }},
		{409, 4090, "IdempotencyConflict", func() *httptest.ResponseRecorder { return call("POST", "/api/v1/intake/work-items", changed, write) }},
		{503, 5003, "InfrastructureUnavailable", func() *httptest.ResponseRecorder {
			n.err = errors.New("redis unavailable")
			return call("POST", "/api/v1/intake/identity-exchange", a, "")
		}},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			w := tc.request()
			require.Equal(t, tc.status, w.Code, w.Body.String())
			assertIdentityErrorDetails(t, w, tc.code, tc.errorCode, tc.status == 503, false)
		})
	}
	command.Title = ""
	command.IdempotencyKey = "invalid-fields"
	w := call("POST", "/api/v1/intake/work-items", command, write)
	require.Equal(t, 400, w.Code, w.Body.String())
	assertIdentityErrorDetails(t, w, 1001, "InvalidCommand", false, true)
}
func assertIdentityErrorDetails(t *testing.T, w *httptest.ResponseRecorder, code int, errorCode string, retryable, nonempty bool) {
	t.Helper()
	var wire map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &wire))
	require.Len(t, wire, 3)
	require.Equal(t, float64(code), wire["code"])
	require.IsType(t, "", wire["message"])
	data, ok := wire["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, errorCode, data["errorCode"])
	require.Equal(t, retryable, data["retryable"])
	for key := range data {
		require.Contains(t, []string{"errorCode", "retryable", "fieldErrors"}, key)
	}
	value, present := data["fieldErrors"]
	if nonempty {
		require.True(t, present)
	}
	if present {
		fields, ok := value.([]any)
		require.True(t, ok, "published fieldErrors must be array or omitted, got %T", value)
		if nonempty {
			require.NotEmpty(t, fields)
		}
		for _, raw := range fields {
			field, ok := raw.(map[string]any)
			require.True(t, ok)
			require.Len(t, field, 2)
			require.IsType(t, "", field["field"])
			require.IsType(t, "", field["message"])
		}
	}
}
