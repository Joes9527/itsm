package bpmn

import (
	"context"
	"fmt"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/notification"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNotificationHandler_DurableExternalActionBlocksWithoutLoggingPayload(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	handler := NewNotificationHandler(nil, zap.New(core).Sugar())
	ctx := WithBPMNCallbackExecutionKey(context.Background(), "notification-retry-key")
	sentinel := "callback-secret-message"

	effect, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "send_sms", "phone_numbers": "+8613800000000", "message": sentinel,
	})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, effect.Status)
	require.Equal(t, CallbackBlockChannelUnavailable, effect.BlockCode)
	assert.NotContains(t, logs.AllUntimed(), sentinel)
	for _, entry := range logs.All() {
		assert.NotContains(t, entry.Message+fmt.Sprint(entry.ContextMap()), sentinel)
	}
}

func TestNotificationHandler_InAppRetryDeduplicatesByExecutionKey(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:notification_handler_retry?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("notification-retry").SetDomain("notification-retry.test").SetStatus("active").SaveX(ctx)
	user := client.User.Create().SetUsername("notification-user").SetEmail("notification-user@test.invalid").SetPasswordHash("x").SetName("User").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	handler := NewNotificationHandler(client, zap.NewNop().Sugar())
	ctx = context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)
	ctx = WithBPMNCallbackExecutionKey(ctx, "notification-in-app-retry-key")
	variables := map[string]interface{}{
		"action": "send_in_app", "user_ids": user.ID, "title": "Update", "content": "Ready", "notification_type": "info",
	}

	_, err := handler.Execute(ctx, nil, variables)
	require.NoError(t, err)
	_, err = handler.Execute(ctx, nil, variables)
	require.NoError(t, err)

	assert.Equal(t, 1, client.Notification.Query().Where(
		notification.TenantID(tenant.ID), notification.UserID(user.ID),
	).CountX(ctx))
}
