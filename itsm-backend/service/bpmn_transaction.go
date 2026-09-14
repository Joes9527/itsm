package service

import (
	"context"
	"errors"

	"itsm-backend/ent"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"
)

// CheckTaskCompletionTx projects the existing completion authorization and state
// guards without writes. The actual completion repeats these checks.
func (e *CustomProcessEngine) CheckTaskCompletionTx(ctx context.Context, tx *ent.Tx, taskID string) error {
	if tx == nil || e.transactionBound {
		return errors.New("CheckTaskCompletionTx requires the root engine and owning transaction")
	}
	if err := e.requireActorSnapshot(ctx, tx); err != nil {
		return err
	}
	tenantID, err := bpmnTaskMutationTenant(ctx)
	if err != nil {
		return err
	}
	scoped := e.forClient(tx.Client(), nil, tx)
	task, err := tx.ProcessTask.Query().Where(processtask.TaskID(taskID), processtask.TenantID(tenantID)).Only(ctx)
	if err != nil {
		return err
	}
	if err = scoped.authorizeTaskActorWithClient(ctx, tx.Client(), task); err != nil {
		return err
	}
	if _, err = scoped.captureTaskMutationActor(ctx, tx.Client(), task); err != nil {
		return err
	}
	return ValidateBPMNTaskLifecycle(BPMNTaskCommandComplete, task.Status)
}

// StartProcessTx schedules the existing process execution in the caller's
// transaction. The caller must roll back on error and owns the final commit.
// Callback effects are durable in the same transaction and attempted only after
// successful commit, using the original engine's nontransactional client.
// The returned instance is transaction-bound until that commit succeeds.
// The commit hook unwraps it automatically; callers must never call Unwrap.
func (e *CustomProcessEngine) StartProcessTx(ctx context.Context, tx *ent.Tx, definitionKey, businessKey, businessType string, businessID int, variables map[string]interface{}) (*ent.ProcessInstance, error) {
	if tx == nil || e.transactionBound {
		return nil, errors.New("StartProcessTx requires a caller transaction and the root process engine")
	}
	if err := e.requireActorSnapshot(ctx, tx); err != nil {
		return nil, err
	}
	keys := make([]string, 0)
	instance, err := e.forClient(tx.Client(), &keys, tx).startProcessWithClient(ctx, definitionKey, businessKey, businessType, businessID, variables)
	if err != nil {
		return nil, err
	}
	tx.OnCommit(func(next ent.Committer) ent.Committer {
		return ent.CommitFunc(func(commitCtx context.Context, tx *ent.Tx) error {
			if err := next.Commit(commitCtx, tx); err != nil {
				return err
			}
			instance.Unwrap()
			e.processCommittedCallbackKeys(ctx, instance.TenantID, keys)
			return nil
		})
	})
	return instance, nil
}

// CompleteTaskTx uses the same participant authorization, task CAS, approval
// decision, audit and callback enqueue path as CompleteTask. It never commits or
// rolls back the supplied transaction, including when completion is rejected.
// Duplicate completion remains the existing task lifecycle conflict; it does
// not create another decision or callback. The owner must roll back on error.
func (e *CustomProcessEngine) CompleteTaskTx(ctx context.Context, tx *ent.Tx, taskID string, variables map[string]interface{}) error {
	if tx == nil || e.transactionBound {
		return errors.New("CompleteTaskTx requires a caller transaction and the root process engine")
	}
	if err := e.requireActorSnapshot(ctx, tx); err != nil {
		return err
	}
	participantVariables, err := validateAndCloneBPMNParticipantVariables(variables, false)
	if err != nil {
		return err
	}
	keys := make([]string, 0)
	effect, err := e.forClient(tx.Client(), &keys, tx).completeTaskWithClient(ctx, tx.Client(), taskID, participantVariables, &keys)
	if err != nil {
		return err
	}
	tx.OnCommit(func(next ent.Committer) ent.Committer {
		return ent.CommitFunc(func(commitCtx context.Context, tx *ent.Tx) error {
			if err := next.Commit(commitCtx, tx); err != nil {
				return err
			}
			effect.task.Unwrap()
			callbackCtx := ctx
			if tenantID, _ := callbackCtx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID <= 0 {
				callbackCtx = context.WithValue(callbackCtx, bpmn.BPMNTenantIDContextKey, effect.task.TenantID)
			}
			e.processCommittedCallbackKeys(callbackCtx, effect.task.TenantID, keys)
			e.executeAsyncUserTaskCompletion(callbackCtx, effect)
			return nil
		})
	})
	return nil
}
