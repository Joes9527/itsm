package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKAFIntakeConfig(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		cfg, err := loadKAFIntakeConfig(func(string) string { return "" })
		require.NoError(t, err)
		require.False(t, cfg.Enabled)
		require.Equal(t, time.Minute, cfg.AssertionMaxAge)
		require.Equal(t, 5*time.Minute, cfg.TokenTTL)
	})

	t.Run("enabled uses dedicated secret and fixed security windows", func(t *testing.T) {
		env := map[string]string{
			"KAF_INTAKE_EXCHANGE_ENABLED": "true",
			"KAF_INTAKE_EXCHANGE_SECRET":  "dedicated-exchange-secret",
		}
		cfg, err := loadKAFIntakeConfig(func(key string) string { return env[key] })
		require.NoError(t, err)
		require.True(t, cfg.Enabled)
		require.Equal(t, "dedicated-exchange-secret", cfg.ExchangeSecret)
		require.Equal(t, time.Minute, cfg.AssertionMaxAge)
		require.Equal(t, 5*time.Minute, cfg.TokenTTL)
	})

	t.Run("enabled without secret fails closed", func(t *testing.T) {
		_, err := loadKAFIntakeConfig(func(key string) string {
			if key == "KAF_INTAKE_EXCHANGE_ENABLED" {
				return "true"
			}
			return ""
		})
		require.ErrorContains(t, err, "KAF_INTAKE_EXCHANGE_SECRET")
	})

	t.Run("invalid enabled value is rejected", func(t *testing.T) {
		_, err := loadKAFIntakeConfig(func(key string) string {
			if key == "KAF_INTAKE_EXCHANGE_ENABLED" {
				return "sometimes"
			}
			return ""
		})
		require.ErrorContains(t, err, "KAF_INTAKE_EXCHANGE_ENABLED")
	})
}
