package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/service/bpmn"
)

func TestCallbackProvenanceExactEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "duplicate", "wrong_event", "spoofed_variables"} {
		t.Run(mode, func(t *testing.T) {
			client := openBPMNCallbackOutboxClient(t)
			row := enqueueBPMNCallbackOutboxForTest(t, newBPMNCallbackOutboxForTest(client, nil, time.Now()), mode)
			ctx := context.Background()
			if mode != "missing" && mode != "spoofed_variables" {
				ctx = context.WithValue(ctx, callbackActorKey{}, callbackActor{11, 7, 7, "bpmn_task_complete"})
				require.NoError(t, recordCallbackProvenance(ctx, client, row))
			}
			if mode == "duplicate" {
				require.NoError(t, recordCallbackProvenance(ctx, client, row))
			}
			if mode == "wrong_event" {
				record := client.ProcessAuditLog.Query().Where(processauditlog.ActionEQ(callbackProvenanceAction)).OnlyX(ctx)
				meta := record.Metadata
				meta["outbox_id"] = row.ID + 1
				record.Update().SetMetadata(meta).SaveX(ctx)
			}
			if mode == "spoofed_variables" {
				row.Variables = map[string]interface{}{"actor_id": 11, "triggered_by": 11, "native_tenant_id": 7}
			}
			actor, err := loadCallbackProvenance(ctx, client, row)
			if mode == "valid" {
				require.NoError(t, err)
				require.Equal(t, 11, actor.id)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestCallbackProvenanceEnqueueAuditRollback(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	outbox := newBPMNCallbackOutboxForTest(client, nil, time.Now())
	original := enqueueBPMNCallbackOutboxForTest(t, outbox, "original")
	client.ProcessAuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
			return nil, errors.New("injected audit failure")
		})
	})
	ctx := context.WithValue(context.Background(), callbackActorKey{}, callbackActor{11, 7, 7, "bpmn_start"})
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	_, err = outbox.enqueue(ctx, tx.Client(), bpmnCallbackEnqueueRequest{ExecutionKey: "must-rollback", TenantID: 7, ProcessInstanceID: original.ProcessInstanceID, CallbackKind: "service_task", HandlerID: "test", TaskType: "test", ElementID: "activity", Action: "assign_ticket"}, tx)
	require.ErrorContains(t, err, "injected audit failure")
	require.NoError(t, tx.Rollback())
	require.Equal(t, 1, client.ProcessCallbackOutbox.Query().CountX(ctx))
	require.Zero(t, client.ProcessAuditLog.Query().CountX(ctx))
}

// Fixture directory uses the same test transaction; PostgreSQL role evidence
// lives in tests/integration and is not inferred from this fixture.
type callbackFixtureDirectory struct{}

func (callbackFixtureDirectory) Open(_ context.Context, tx *ent.Tx, _ int) (*ent.Client, func() error, error) {
	return tx.Client(), func() error { return nil }, nil
}

func callbackAssignmentTestContext(t *testing.T, client *ent.Client, actor *ent.User, target, workItemID int) context.Context {
	t.Helper()
	ctx := tenantctx.WithTenantID(context.Background(), target)
	key := uuid.NewString()
	deployment := client.ProcessDeployment.Create().SetDeploymentID(key).SetDeploymentName(key).SetTenantID(target).SaveX(ctx)
	definition := client.ProcessDefinition.Create().SetKey(key).SetName(key).SetBpmnXML([]byte("<definitions/>")).SetDeploymentID(deployment.ID).SetTenantID(target).SaveX(ctx)
	instance := client.ProcessInstance.Create().SetProcessInstanceID(key).SetProcessDefinitionKey(key).SetProcessDefinitionID(definition.ID).SetTenantID(target).SetBusinessID(workItemID).SetBusinessType("incident").SaveX(ctx)
	ctx = context.WithValue(ctx, callbackActorKey{}, callbackActor{actor.ID, actor.TenantID, target, "bpmn_start"})
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	_, err = newBPMNCallbackOutboxForTest(client, nil, time.Now()).enqueue(ctx, tx.Client(), bpmnCallbackEnqueueRequest{ExecutionKey: key, TenantID: target, ProcessInstanceID: instance.ID, CallbackKind: "service_task", HandlerID: "test", TaskType: "test", ElementID: "assign", Action: "assign_incident"}, tx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return bpmn.WithBPMNCallbackExecutionKey(context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, target), key)
}
