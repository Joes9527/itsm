package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKafOutboxConfigFromEnvironment(t *testing.T) {
	// A removed bound check must make the invalid cases below pass.
	tests := []struct {
		name    string
		values  map[string]string
		want    KAFOutboxConfig
		wantErr string
	}{
		{
			name: "disabled uses safe defaults",
			want: KAFOutboxConfig{
				BatchSize:    20,
				PollInterval: 5 * time.Second,
			},
		},
		{
			name: "enabled accepts explicit limits",
			values: map[string]string{
				"KAF_WEBHOOK_URL":          "https://kaf.example.test/webhooks/itsm",
				"KAF_WEBHOOK_SECRET":       "test-secret",
				"KAF_OUTBOX_BATCH_SIZE":    "100",
				"KAF_OUTBOX_POLL_INTERVAL": "2s",
			},
			want: KAFOutboxConfig{
				WebhookURL:    "https://kaf.example.test/webhooks/itsm",
				WebhookSecret: "test-secret",
				BatchSize:     100,
				PollInterval:  2 * time.Second,
			},
		},
		{
			name: "URL without secret is rejected",
			values: map[string]string{
				"KAF_WEBHOOK_URL": "https://kaf.example.test/webhooks/itsm",
			},
			wantErr: "KAF_WEBHOOK_SECRET",
		},
		{
			name: "non HTTP URL is rejected",
			values: map[string]string{
				"KAF_WEBHOOK_URL":    "file:///tmp/kaf-webhook",
				"KAF_WEBHOOK_SECRET": "test-secret",
			},
			wantErr: "KAF_WEBHOOK_URL",
		},
		{
			name: "URL userinfo is rejected",
			values: map[string]string{
				"KAF_WEBHOOK_URL":    "https://user:password@kaf.example.test/webhooks/itsm",
				"KAF_WEBHOOK_SECRET": "test-secret",
			},
			wantErr: "KAF_WEBHOOK_URL",
		},
		{
			name: "batch size outside allowed range is rejected",
			values: map[string]string{
				"KAF_OUTBOX_BATCH_SIZE": "101",
			},
			wantErr: "KAF_OUTBOX_BATCH_SIZE",
		},
		{
			name: "poll interval below one second is rejected",
			values: map[string]string{
				"KAF_OUTBOX_POLL_INTERVAL": "500ms",
			},
			wantErr: "KAF_OUTBOX_POLL_INTERVAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadKAFOutboxConfig(func(key string) string {
				return tt.values[key]
			})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg)
		})
	}
}
