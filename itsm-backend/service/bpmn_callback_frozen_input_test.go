package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/service/bpmn"
)

type frozenInputCallbackHandler struct {
	*countingIdempotentCallbackHandler
	contract bpmn.CallbackActionContract
	declared bool
}

func (h *frozenInputCallbackHandler) CallbackContract(string) (bpmn.CallbackActionContract, bool) {
	return h.contract, h.declared
}

func seedFrozenInputCallback(t *testing.T, payload map[string]interface{}, contract bpmn.CallbackActionContract) (*bpmnAuthorizationFixture, *frozenInputCallbackHandler, *ent.ProcessCallbackOutbox) {
	t.Helper()
	f := newBPMNAuthorizationFixture(t)
	base := newCountingIdempotentCallbackHandler("frozen_input_task", "frozen_input_handler", 1)
	task, instance := seedDurableUserCallbackTask(t, f, "frozen-input", base)
	f.client.ProcessTask.UpdateOne(task).SetStatus("completed").SaveX(f.userCtx)
	handler := &frozenInputCallbackHandler{countingIdempotentCallbackHandler: base, contract: contract, declared: true}
	f.engine.CallbackRegistry().RegisterHandler(handler)
	row := f.client.ProcessCallbackOutbox.Create().
		SetExecutionKey("frozen-input-key").SetTenantID(f.tenant.ID).
		SetProcessInstanceID(instance.ID).SetProcessTaskID(task.ID).SetTaskID(task.TaskID).
		SetCallbackKind("user_task_callback").SetHandlerID(handler.GetHandlerID()).
		SetTaskType(handler.GetTaskType()).SetElementID(task.TaskDefinitionKey).
		SetAction("record_completion").SetVariables(payload).SetAttemptCount(83).
		SetLastErrorClass("handler_error").SaveX(f.userCtx)
	return f, handler, f.client.ProcessCallbackOutbox.GetX(f.userCtx, row.ID)
}

func TestFrozenCallbackInvalidInputBlocksBeforeHandler(t *testing.T) {
	contract := bpmn.CallbackActionContract{
		PayloadFields:         []string{"assignee_id", "new_status"},
		RequiredFields:        []string{"assignee_id"},
		PositiveIntegerFields: []string{"assignee_id"},
		NonEmptyStringFields:  []string{"new_status"},
	}
	for _, tc := range []struct {
		name    string
		payload map[string]interface{}
	}{
		{"missing", map[string]interface{}{}},
		{"invalid_integer", map[string]interface{}{"assignee_id": "sensitive-payload"}},
		{"nonpositive", map[string]interface{}{"assignee_id": 0}},
		{"empty_status", map[string]interface{}{"assignee_id": 1, "new_status": " "}},
		{"invalid_status_type", map[string]interface{}{"assignee_id": 1, "new_status": 99}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, handler, before := seedFrozenInputCallback(t, tc.payload, contract)
			instanceBefore := f.client.ProcessInstance.GetX(f.userCtx, before.ProcessInstanceID)
			taskBefore := f.client.ProcessTask.GetX(f.userCtx, before.ProcessTaskID)
			completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "frozen-worker", 10)
			require.NoError(t, err)
			require.Zero(t, completed)
			saved := f.client.ProcessCallbackOutbox.GetX(f.userCtx, before.ID)
			require.Equal(t, bpmnCallbackStatusBlocked, saved.Status)
			require.Equal(t, string(bpmn.CallbackBlockHandlerContract), saved.LastErrorClass)
			require.Zero(t, handler.AttemptCount())
			require.Equal(t, before.Variables, saved.Variables)
			require.Equal(t, before.ExecutionKey, saved.ExecutionKey)
			require.Equal(t, before.AttemptCount+1, saved.AttemptCount)
			require.Empty(t, saved.LeaseOwner)
			require.Equal(t, taskBefore.Status, f.client.ProcessTask.GetX(f.userCtx, before.ProcessTaskID).Status)
			instanceAfter := f.client.ProcessInstance.GetX(f.userCtx, before.ProcessInstanceID)
			require.Equal(t, instanceBefore.Status, instanceAfter.Status)
			require.Equal(t, instanceBefore.CurrentActivityID, instanceAfter.CurrentActivityID)
			audit := f.client.ProcessAuditLog.Query().Where(processauditlog.Action(bpmn.CallbackAuditActionBlocked)).OnlyX(f.userCtx)
			require.Equal(t, string(bpmn.CallbackBlockHandlerContract), audit.Metadata["block_code"])
			require.NotContains(t, audit.Metadata, "payload")
			require.Empty(t, audit.Comment)
			now := time.Now().Add(48 * time.Hour)
			setCallbackTestClock(f.engine, &now)
			completed, err = f.engine.ProcessPendingCallbacks(context.Background(), "restarted-worker", 10)
			require.NoError(t, err)
			require.Zero(t, completed)
			require.Equal(t, saved.AttemptCount, f.client.ProcessCallbackOutbox.GetX(f.userCtx, before.ID).AttemptCount)
			require.Zero(t, handler.AttemptCount())
			require.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(processauditlog.Action(bpmn.CallbackAuditActionBlocked)).CountX(f.userCtx))
		})
	}
}

func TestFrozenCallbackNonInputFailuresRemainRetryable(t *testing.T) {
	for _, mode := range []string{"valid_transient", "unknown_action", "invalid_contract", "unknown_handler"} {
		t.Run(mode, func(t *testing.T) {
			contract := bpmn.CallbackActionContract{PayloadFields: []string{"assignee_id"}, RequiredFields: []string{"assignee_id"}, PositiveIntegerFields: []string{"assignee_id"}}
			f, handler, row := seedFrozenInputCallback(t, map[string]interface{}{"assignee_id": 1}, contract)
			switch mode {
			case "unknown_action":
				handler.declared = false
			case "invalid_contract":
				handler.contract.RequiredFields = []string{"undeclared"}
			case "unknown_handler":
				f.client.ProcessCallbackOutbox.UpdateOne(row).SetHandlerID("unavailable").SaveX(f.userCtx)
			}
			completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "retry-worker", 10)
			require.Error(t, err)
			require.Zero(t, completed)
			saved := f.client.ProcessCallbackOutbox.GetX(f.userCtx, row.ID)
			require.Equal(t, bpmnCallbackStatusPending, saved.Status)
			require.Equal(t, "handler_error", saved.LastErrorClass)
			if mode == "valid_transient" {
				require.Equal(t, 1, handler.AttemptCount())
			} else {
				require.Zero(t, handler.AttemptCount())
			}
			require.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.Action(bpmn.CallbackAuditActionBlocked)).CountX(f.userCtx))
		})
	}
}

func TestFrozenCallbackBlockedOutcomeAuditRollbackAndLeaseCAS(t *testing.T) {
	for _, failAudit := range []bool{false, true} {
		t.Run(map[bool]string{false: "lost_lease", true: "audit_failure"}[failAudit], func(t *testing.T) {
			f, handler, row := seedFrozenInputCallback(t, nil, bpmn.CallbackActionContract{PayloadFields: []string{"assignee_id"}, RequiredFields: []string{"assignee_id"}})
			row = f.client.ProcessCallbackOutbox.UpdateOne(row).SetStatus(bpmnCallbackStatusProcessing).SetLeaseOwner("owner").SaveX(f.userCtx)
			result, err := f.engine.executeClaimedCallback(f.userCtx, "owner", row)
			require.NoError(t, err)
			require.NotNil(t, result.Effect)
			require.Zero(t, handler.AttemptCount())
			worker := "other"
			if failAudit {
				worker = "owner"
				f.client.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if _, ok := m.(*ent.ProcessAuditLogMutation); ok {
							return nil, errors.New("audit unavailable")
						}
						return next.Mutate(ctx, m)
					})
				})
			}
			persisted, err := f.engine.callbackOutbox.persistCallbackOutcome(f.userCtx, worker, row, bpmn.ResolveCallbackOutcome(result.Effect, false))
			require.Error(t, err)
			require.False(t, persisted)
			saved := f.client.ProcessCallbackOutbox.GetX(f.userCtx, row.ID)
			require.Equal(t, bpmnCallbackStatusProcessing, saved.Status)
			require.Equal(t, "owner", saved.LeaseOwner)
			require.Equal(t, row.AttemptCount, saved.AttemptCount)
			require.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.Action(bpmn.CallbackAuditActionBlocked)).CountX(f.userCtx))
		})
	}
}

func TestFrozenCallbackInvalidOptionalInputKeepsAuditedSkipPolicy(t *testing.T) {
	f, handler, row := seedFrozenInputCallback(t, nil, bpmn.CallbackActionContract{PayloadFields: []string{"assignee_id"}, RequiredFields: []string{"assignee_id"}})
	f.client.ProcessCallbackOutbox.UpdateOne(row).SetOptionalDeclared(true).SaveX(f.userCtx)
	completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "optional-worker", 10)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	saved := f.client.ProcessCallbackOutbox.GetX(f.userCtx, row.ID)
	require.Equal(t, bpmnCallbackStatusCompleted, saved.Status)
	require.Zero(t, handler.AttemptCount())
	require.Equal(t, row.Variables, saved.Variables)
	require.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(processauditlog.Action(bpmn.CallbackAuditActionSkippedOptional)).CountX(f.userCtx))
}
