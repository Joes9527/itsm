package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestIncidentCallbackFreezesActorOutsideVariables(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	outbox := newBPMNCallbackOutboxForTest(client, &fakeBPMNCallbackExecutor{}, time.Now())
	initial := enqueueBPMNCallbackOutboxForTest(t, outbox, "incident-actor-initial")
	row, err := outbox.enqueue(context.Background(), client, bpmnCallbackEnqueueRequest{ExecutionKey: "incident-actor", TenantID: 7, ProcessInstanceID: initial.ProcessInstanceID, HandlerID: "incident_service_handler", TaskType: "incident_task", ElementID: "resolve", CallbackKind: "user_task_callback", ActorID: 17, ActorSource: "workflow", Variables: map[string]interface{}{"actor_id": 999}}, nil)
	require.NoError(t, err)
	loaded := client.ProcessCallbackOutbox.GetX(context.Background(), row.ID)
	require.Equal(t, 17, loaded.ActorID)
	require.Equal(t, "workflow", loaded.ActorSource)
}
