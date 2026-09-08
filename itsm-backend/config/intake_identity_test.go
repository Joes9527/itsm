package config

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityConfigRequiresRoleLimitedSeparateSecrets(t *testing.T) {
	file := filepath.Join(t.TempDir(), "identity.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"providers":{"kaf":{"secret":"test-only-key","channels":["kaf_web"],"purposes":["create","read"]}},"maxAge":"60s","futureSkew":"5s","tokenTTL":"5m"}`), 0600))
	getenv := func(key string) string {
		if key == "INTAKE_IDENTITY_CONFIG_FILE" {
			return file
		}
		return ""
	}
	cfg, err := loadIntakeIdentityConfig(getenv)
	require.NoError(t, err)
	require.Contains(t, cfg.Providers, "kaf")
	require.NoError(t, os.Chmod(file, 0644))
	_, err = loadIntakeIdentityConfig(getenv)
	require.Error(t, err)
}
func TestIdentityConfigMissingIsExplicitlyDisabled(t *testing.T) {
	cfg, err := loadIntakeIdentityConfig(func(string) string { return "" })
	require.NoError(t, err)
	require.Empty(t, cfg.Providers)
}

func TestIdentityConfigRejectsInvalidProviderAndWindow(t *testing.T) {
	for _, raw := range []string{
		`{"providers":{"kaf":{"secret":"test","channels":["kaf_web"],"purposes":["admin"]}}}`,
		`{"providers":{"kaf":{"secret":"test","channels":["kaf_web","kaf_web"],"purposes":["create"]}}}`,
		`{"providers":{"kaf":{"secret":"test","channels":[],"purposes":["create"]}}}`,
		`{"providers":{"kaf":{"secret":"test","channels":["kaf_web"],"purposes":["create"],"unknown":true}}}`,
		`{"maxAge":"0s"}`, `{"futureSkew":"31s"}`, `{"tokenTTL":"16m"}`, `{"maxAge":"1.5s"}`, `{"providers":{},"providers":{}}`,
	} {
		file := filepath.Join(t.TempDir(), "identity.json")
		require.NoError(t, os.WriteFile(file, []byte(raw), 0600))
		_, err := loadIntakeIdentityConfig(func(string) string { return file })
		require.Error(t, err)
	}
}
func TestIdentityConfigRejectsOtherCredentialReuse(t *testing.T) {
	cfg := IntakeIdentityConfig{Providers: map[string]IntakeIdentityProviderConfig{"kaf": {Secret: "test-exchange"}}}
	require.Error(t, validateIdentitySecretSeparation(cfg, "test-exchange", "different"))
	require.Error(t, validateIdentitySecretSeparation(cfg, "different", "test-exchange"))
	require.NoError(t, validateIdentitySecretSeparation(cfg, "test-jwt", "test-webhook"))
}
