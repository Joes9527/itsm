package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadWorkflowOutboxConfigDefaultsAndBounds(t *testing.T) {
	empty := func(string) string { return "" }
	defaults, err := loadWorkflowOutboxConfig(empty)
	require.NoError(t, err)
	require.Equal(t, 20, defaults.BatchSize)
	require.Equal(t, 5*time.Second, defaults.PollInterval)
	require.Equal(t, 10, defaults.MaxAttempts)

	values := map[string]string{
		"WORKFLOW_OUTBOX_BATCH_SIZE":    "8",
		"WORKFLOW_OUTBOX_POLL_INTERVAL": "2s",
		"WORKFLOW_OUTBOX_MAX_ATTEMPTS":  "4",
	}
	configured, err := loadWorkflowOutboxConfig(func(key string) string { return values[key] })
	require.NoError(t, err)
	require.Equal(t, WorkflowOutboxConfig{BatchSize: 8, PollInterval: 2 * time.Second, MaxAttempts: 4}, configured)

	for name, values := range map[string]map[string]string{
		"batch":    {"WORKFLOW_OUTBOX_BATCH_SIZE": "0"},
		"poll":     {"WORKFLOW_OUTBOX_POLL_INTERVAL": "100ms"},
		"attempts": {"WORKFLOW_OUTBOX_MAX_ATTEMPTS": "101"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadWorkflowOutboxConfig(func(key string) string { return values[key] })
			require.Error(t, err)
		})
	}
}
