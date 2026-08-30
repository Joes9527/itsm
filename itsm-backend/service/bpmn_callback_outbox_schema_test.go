package service

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func openBPMNSchemaTestClient(t *testing.T) *ent.Client {
	t.Helper()

	client := enttest.Open(t, "sqlite3", "file:bpmn_callback_outbox_schema?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestProcessCallbackOutboxSchemaEnforcesExecutionKeyAndTenant(t *testing.T) {
	client := openBPMNSchemaTestClient(t)
	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))

	row := client.ProcessCallbackOutbox.Create().
		SetExecutionKey("bpmn-callback-00000001").
		SetTenantID(7).
		SetProcessInstanceID(101).
		SetProcessTaskID(202).
		SetCallbackKind("service_task").
		SetHandlerID("webhook_handler").
		SetTaskType("webhook").
		SetElementID("Activity_Notify").
		SetVariables(map[string]interface{}{"ticket_id": 42}).
		SetStatus("pending").
		SaveX(ctx)
	require.Equal(t, 7, row.TenantID)
	require.Equal(t, 0, row.AttemptCount)
	require.False(t, row.NextAttemptAt.IsZero())

	_, err := client.ProcessCallbackOutbox.Create().
		SetExecutionKey(row.ExecutionKey).
		SetTenantID(7).
		SetProcessInstanceID(101).
		SetCallbackKind("service_task").
		SetHandlerID("webhook_handler").
		SetTaskType("webhook").
		SetElementID("Activity_Notify").
		SetStatus("pending").
		Save(ctx)
	require.Error(t, err, "execution_key must be globally unique and opaque")
}
