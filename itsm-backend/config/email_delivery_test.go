package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestEmailDeliveryTransportIsExplicitAndFailClosed(t *testing.T) {
	for _, transport := range []string{"", "graph", "smtp", "automatic", "SMTP", " smtp", "smtp "} {
		t.Run(transport, func(t *testing.T) {
			v := viper.New()
			v.Set("email_delivery.transport", transport)
			var cfg Config
			require.NoError(t, v.Unmarshal(&cfg))
			if transport == "" || transport == "graph" || transport == "smtp" {
				require.NoError(t, cfg.EmailDelivery.Validate())
				expected := transport
				if expected == "" {
					expected = "graph"
				}
				require.Equal(t, expected, cfg.EmailDelivery.EffectiveTransport())
			} else {
				require.Error(t, cfg.EmailDelivery.Validate())
			}
		})
	}
}
