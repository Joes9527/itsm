package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateKAFWorkerStartupConfigRejectsMissingWebhookURL(t *testing.T) {
	cfg := &Config{KAFOutbox: KAFOutboxConfig{WebhookSecret: "test-secret", BatchSize: 1, MaxAttempts: 1, HealthPort: 8081}}

	require.ErrorContains(t, ValidateKAFWorkerStartupConfig(cfg), "KAF_WEBHOOK_URL")
}

func TestReadEnvironmentOrSecretReadsTrimmedSecretFile(t *testing.T) {
	secretFile := t.TempDir() + "/kaf-webhook-secret"
	require.NoError(t, os.WriteFile(secretFile, []byte(" test-secret\n"), 0o600))
	t.Setenv("KAF_WEBHOOK_SECRET_FILE", secretFile)

	value, err := readEnvironmentOrSecret("KAF_WEBHOOK_SECRET")

	require.NoError(t, err)
	require.Equal(t, "test-secret", value)
}

func TestReadEnvironmentOrSecretRejectsDirectAndFileValues(t *testing.T) {
	secretFile := t.TempDir() + "/kaf-webhook-secret"
	require.NoError(t, os.WriteFile(secretFile, []byte("file-secret"), 0o600))
	t.Setenv("KAF_WEBHOOK_SECRET", "direct-secret")
	t.Setenv("KAF_WEBHOOK_SECRET_FILE", secretFile)

	_, err := readEnvironmentOrSecret("KAF_WEBHOOK_SECRET")

	require.ErrorContains(t, err, "KAF_WEBHOOK_SECRET")
}

func TestReadEnvironmentOrSecretRejectsUnreadableOrEmptyFile(t *testing.T) {
	t.Run("unreadable", func(t *testing.T) {
		t.Setenv("KAF_WEBHOOK_SECRET_FILE", t.TempDir()+"/missing-secret")

		_, err := readEnvironmentOrSecret("KAF_WEBHOOK_SECRET")

		require.ErrorContains(t, err, "KAF_WEBHOOK_SECRET_FILE")
	})

	t.Run("empty", func(t *testing.T) {
		secretFile := t.TempDir() + "/kaf-webhook-secret"
		require.NoError(t, os.WriteFile(secretFile, []byte(" \n"), 0o600))
		t.Setenv("KAF_WEBHOOK_SECRET_FILE", secretFile)

		_, err := readEnvironmentOrSecret("KAF_WEBHOOK_SECRET")

		require.ErrorContains(t, err, "KAF_WEBHOOK_SECRET_FILE")
	})
}

func TestValidateKAFWorkerStartupConfigRejectsMissingWebhookSecret(t *testing.T) {
	cfg := &Config{KAFOutbox: KAFOutboxConfig{WebhookURL: "https://kaf.example.test/webhooks/itsm", BatchSize: 1, MaxAttempts: 1, HealthPort: 8081}}

	require.ErrorContains(t, ValidateKAFWorkerStartupConfig(cfg), "KAF_WEBHOOK_SECRET")
}

func TestValidateKAFWorkerStartupConfigAcceptsCompleteConfig(t *testing.T) {
	cfg := &Config{KAFOutbox: KAFOutboxConfig{
		WebhookURL:    "https://kaf.example.test/webhooks/itsm",
		WebhookSecret: "test-secret",
		BatchSize:     1,
		PollInterval:  time.Second,
		MaxAttempts:   1,
		HealthPort:    8081,
	}}

	require.NoError(t, ValidateKAFWorkerStartupConfig(cfg))
}
