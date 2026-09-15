package config

import (
	"os"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestCookieSecureConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name                               string
		yamlValue                          interface{}
		env                                string
		setEnv, wantPresent, want, wantErr bool
	}{
		{name: "omitted"},
		{name: "yaml false", yamlValue: false, wantPresent: true},
		{name: "yaml true", yamlValue: true, wantPresent: true, want: true},
		{name: "env false overrides yaml", yamlValue: true, env: "false", setEnv: true, wantPresent: true},
		{name: "env true", env: "true", setEnv: true, wantPresent: true, want: true},
		{name: "invalid rejected", env: "typo", setEnv: true, wantErr: true},
		{name: "empty rejected", setEnv: true, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.yamlValue != nil {
				v.Set("server.cookie_secure", tt.yamlValue)
			}
			var cfg Config
			require.NoError(t, v.Unmarshal(&cfg))
			err := cfg.Server.applyCookieSecureEnvironment(func(string) (string, bool) { return tt.env, tt.setEnv })
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if !tt.wantPresent {
				require.Nil(t, cfg.Server.CookieSecure)
				return
			}
			require.NotNil(t, cfg.Server.CookieSecure)
			require.Equal(t, tt.want, *cfg.Server.CookieSecure)
		})
	}
}

func TestLoadConfigPreservesCookieSecurePresence(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, env string
		present, want   bool
	}{
		{name: "absent", yaml: "server: {}\n"},
		{name: "explicit YAML false", yaml: "server:\n  cookie_secure: false\n", present: true},
		{name: "explicit YAML true", yaml: "server:\n  cookie_secure: true\n", present: true, want: true},
		{name: "environment overrides YAML", yaml: "server:\n  cookie_secure: true\n", env: "false", present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			viper.Reset()
			t.Cleanup(viper.Reset)
			t.Setenv("ITSM_COOKIE_SECURE", tc.env)
			if tc.env == "" {
				require.NoError(t, os.Unsetenv("ITSM_COOKIE_SECURE"))
			}
			require.NoError(t, os.WriteFile("config.yaml", []byte(tc.yaml), 0o600))
			cfg, err := LoadConfig()
			require.NoError(t, err)
			if !tc.present {
				require.Nil(t, cfg.Server.CookieSecure)
			} else {
				require.NotNil(t, cfg.Server.CookieSecure)
				require.Equal(t, tc.want, *cfg.Server.CookieSecure)
			}
		})
	}
}
