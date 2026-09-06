package contract

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"itsm-backend/handlers/intake"
	"itsm-backend/router"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityRoutesAreRegisteredWithScopedAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	router.SetupRoutes(r, &router.RouterConfig{JWTSecret: "test-jwt-key", Logger: zap.NewNop().Sugar(), IntakeHandler: intake.NewHandler(nil, nil)})
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"POST", "/api/v1/intake/identity-exchange", 400}, {"POST", "/api/v1/intake/identity-exchange/read", 400}, {"POST", "/api/v1/intake/work-items", 401}, {"GET", "/api/v1/intake/catalog-items", 401}, {"GET", "/api/v1/intake/catalog-items/1", 401}, {"GET", "/api/v1/intake/work-items/1", 401}, {"POST", "/api/v1/intake/identity-mappings", 401}, {"PATCH", "/api/v1/intake/identity-mappings/1", 401}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(`{"role":"admin"}`)))
		require.Equal(t, tc.status, w.Code, tc.path+" "+w.Body.String())
	}
}
func TestIdentityAssertionSharedCrossLanguageFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/contracts/fixtures/intake-identity-signature.json")
	require.NoError(t, err)
	var f struct {
		Fields                       []string `json:"fields"`
		Secret, Canonical, Signature string
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, 10, len(f.Fields))
	require.Equal(t, strings.Join(f.Fields, "\n"), f.Canonical)
	m := hmac.New(sha256.New, []byte(f.Secret))
	m.Write([]byte(f.Canonical))
	require.Equal(t, f.Signature, hex.EncodeToString(m.Sum(nil)))
}

func TestIdentityACLManifestIncludesCapabilityRoutes(t *testing.T) {
	out := filepath.Join(t.TempDir(), "acl.yaml")
	cmd := exec.Command("node", "../../../scripts/generate-acl-manifest.js", "--output", out)
	raw, err := cmd.CombinedOutput()
	require.NoError(t, err, string(raw))
	manifest, err := os.ReadFile(out)
	require.NoError(t, err)
	var parsed struct {
		Routes []struct{ Method, Path, Permission string }
	}
	require.NoError(t, yaml.Unmarshal(manifest, &parsed))
	expected := map[string]string{"POST /api/v1/intake/identity-exchange": "assertion.v2.create", "POST /api/v1/intake/identity-exchange/read": "assertion.v2.read", "POST /api/v1/intake/work-items": "intake:create", "GET /api/v1/intake/catalog-items": "intake:catalog:read", "GET /api/v1/intake/catalog-items/:id": "intake:catalog:read", "GET /api/v1/intake/work-items/:id": "intake:workitem:read", "GET /api/v1/intake/identity-mappings": "intake_identity_mapping.read", "POST /api/v1/intake/identity-mappings": "intake_identity_mapping.write", "PATCH /api/v1/intake/identity-mappings/:id": "intake_identity_mapping.write"}
	for _, r := range parsed.Routes {
		key := r.Method + " " + r.Path
		if permission, ok := expected[key]; ok {
			require.Equal(t, permission, r.Permission, key)
			delete(expected, key)
		}
	}
	require.Empty(t, expected, "all exact capability routes must be represented")
}
