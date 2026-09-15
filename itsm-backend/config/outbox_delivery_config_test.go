package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutboxDeliveryConfigFromEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]string
		want    OutboxDeliveryConfig
		wantErr string
	}{
		{
			name: "safe defaults",
			want: OutboxDeliveryConfig{BatchSize: 20, PollInterval: 5 * time.Second, HandlerTimeout: 5 * time.Second, MaxAttempts: 5},
		},
		{
			name: "explicit limits",
			values: map[string]string{
				"OUTBOX_DELIVERY_BATCH_SIZE":      "100",
				"OUTBOX_DELIVERY_POLL_INTERVAL":   "2s",
				"OUTBOX_DELIVERY_HANDLER_TIMEOUT": "10s",
				"OUTBOX_DELIVERY_MAX_ATTEMPTS":    "9",
			},
			want: OutboxDeliveryConfig{BatchSize: 100, PollInterval: 2 * time.Second, HandlerTimeout: 10 * time.Second, MaxAttempts: 9},
		},
		{name: "invalid batch", values: map[string]string{"OUTBOX_DELIVERY_BATCH_SIZE": "0"}, wantErr: "OUTBOX_DELIVERY_BATCH_SIZE"},
		{name: "invalid poll", values: map[string]string{"OUTBOX_DELIVERY_POLL_INTERVAL": "500ms"}, wantErr: "OUTBOX_DELIVERY_POLL_INTERVAL"},
		{name: "invalid timeout", values: map[string]string{"OUTBOX_DELIVERY_HANDLER_TIMEOUT": "0s"}, wantErr: "OUTBOX_DELIVERY_HANDLER_TIMEOUT"},
		{name: "timeout cannot consume lease", values: map[string]string{"OUTBOX_DELIVERY_HANDLER_TIMEOUT": "5m"}, wantErr: "OUTBOX_DELIVERY_HANDLER_TIMEOUT"},
		{name: "invalid attempts", values: map[string]string{"OUTBOX_DELIVERY_MAX_ATTEMPTS": "21"}, wantErr: "OUTBOX_DELIVERY_MAX_ATTEMPTS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadOutboxDeliveryConfig(func(key string) string { return tt.values[key] })
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg)
		})
	}
}
