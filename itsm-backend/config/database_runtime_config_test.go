package config

import (
	"os"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigKeepsSystemCredentialIndependent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	viper.Reset()
	t.Cleanup(viper.Reset)
	require.NoError(t, os.WriteFile("config.yaml", []byte("database:\n  user: runtime\n  password: runtime-test\n  admin_role_user: migration\n  admin_role_password: migration-test\n"), 0600))
	for _, name := range []string{"DB_SYSTEM_ROLE_PASSWORD", "ITSM_DB_SYSTEM_ROLE_PASSWORD", "DB_SYSTEM_ROLE_PASSWORD_FILE", "ITSM_DB_SYSTEM_ROLE_PASSWORD_FILE", "DB_PASSWORD", "DB_PASSWORD_FILE", "ITSM_DB_PASSWORD", "ITSM_DB_PASSWORD_FILE", "DB_APP_ROLE_USER", "DB_ADMIN_ROLE_USER", "DB_ADMIN_ROLE_PASSWORD"} {
		t.Setenv(name, "")
	}
	t.Setenv("DB_SYSTEM_ROLE_USER", "restricted-system")
	t.Setenv("DB_SCHEMA", "application_schema")
	file := dir + "/system-password"
	require.NoError(t, os.WriteFile(file, []byte("system-test\n"), 0600))
	t.Setenv("DB_SYSTEM_ROLE_PASSWORD_FILE", file)
	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "runtime", cfg.Database.User)
	require.Equal(t, "runtime-test", cfg.Database.Password)
	require.Equal(t, "migration", cfg.Database.AdminRoleUser)
	require.Equal(t, "restricted-system", cfg.Database.SystemRoleUser)
	require.Equal(t, "system-test", cfg.Database.SystemRolePassword)
	require.Equal(t, "application_schema", cfg.Database.Schema)
	t.Setenv("DB_SYSTEM_ROLE_PASSWORD_FILE", dir+"/missing")
	_, err = LoadConfig()
	require.ErrorContains(t, err, "DB_SYSTEM_ROLE_PASSWORD_FILE")
	t.Setenv("DB_SYSTEM_ROLE_PASSWORD_FILE", file)
	t.Setenv("DB_SYSTEM_ROLE_PASSWORD", "conflicting-source")
	_, err = LoadConfig()
	require.ErrorContains(t, err, "exactly one")
}
