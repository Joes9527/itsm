package bpmn

import (
	"context"
	"fmt"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

func TestKafDelegateServiceTaskHandler_TypeAndAsync(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:kaf_delegate_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	logger := zaptest.NewLogger(t).Sugar()

	handler := NewKafDelegateServiceTaskHandler(client, logger)

	assert.Equal(t, "kaf_delegate", handler.GetTaskType())
	assert.Equal(t, "kaf_delegate_handler", handler.GetHandlerID())
	assert.True(t, handler.IsAsync())
}

func TestKafDelegateHandler_DoesNotLogCompletionVariables(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	handler := NewKafDelegateServiceTaskHandler(nil, zap.New(core).Sugar())
	sentinel := "kaf-completion-secret"

	_, err := handler.Execute(context.Background(), &ent.ProcessTask{TaskID: "task-1", TenantID: 7}, map[string]interface{}{
		"completion_note": sentinel,
	})
	require.NoError(t, err)
	for _, entry := range logs.All() {
		assert.NotContains(t, entry.Message+fmt.Sprint(entry.ContextMap()), sentinel)
	}
}

func TestKafDelegateServiceTaskHandler_Execute_ReturnsSuccessWithoutSideEffects(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:kaf_delegate_handler_test2?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	logger := zaptest.NewLogger(t).Sugar()

	handler := NewKafDelegateServiceTaskHandler(client, logger)

	result, err := handler.Execute(context.Background(), nil, map[string]interface{}{"resultSummary": "done"})
	require.NoError(t, err)
	assert.True(t, result.Status == CallbackEffectApplied)
}

func TestCallbackRegistry_RegistersKafDelegateHandlerByDefault(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:kaf_delegate_registry_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	logger := zaptest.NewLogger(t).Sugar()

	registry := NewCallbackRegistry(client, logger)
	handler := registry.GetHandler("kaf_delegate_handler")
	require.NotNil(t, handler)
	assert.Equal(t, "kaf_delegate", handler.GetTaskType())

	asyncHandler, ok := handler.(AsyncServiceTaskHandler)
	require.True(t, ok)
	assert.True(t, asyncHandler.IsAsync())
}
