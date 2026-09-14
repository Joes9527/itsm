package service

import (
	"context"
	"errors"
	"fmt"
	"itsm-backend/common/executionscope"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/kaftaskactionledger"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"
)

// The caller retains actor authorization, lease fencing and the write transaction.
func requireKafExecutionTx(ctx context.Context, tx *ent.Tx, policy *database.ExecutionPolicy, tenantID int, taskID string) error {
	if err := policy.BindEnt(ctx, tx, tenantID); err != nil {
		return kafExecutionFailure(err)
	}
	if !policy.IsCandidate() {
		return nil
	}
	task, err := tx.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID), processtask.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return err
	}
	return requireKafInstanceExecutionTx(ctx, tx, policy, tenantID, task.ProcessInstanceID)
}

func requireKafInstanceExecutionTx(ctx context.Context, tx *ent.Tx, policy *database.ExecutionPolicy, tenantID, instanceID int) error {
	if err := policy.BindEnt(ctx, tx, tenantID); err != nil {
		return kafExecutionFailure(err)
	}
	if !policy.IsCandidate() {
		return nil
	}
	instance, err := tx.ProcessInstance.Query().Where(processinstance.IDEQ(instanceID), processinstance.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return err
	}
	if instance.ExecutionWorkItemID == nil {
		return fmt.Errorf("%w: %w: KAF task has no execution WorkItem", ErrKafDelegationForbidden, executionscope.ErrDenied)
	}
	if err := policy.RequireEntMembers(ctx, tx, tenantID, *instance.ExecutionWorkItemID); err != nil {
		return kafExecutionFailure(err)
	}
	return nil
}

func requireKafLedgerExecutionTx(ctx context.Context, tx *ent.Tx, policy *database.ExecutionPolicy, ledgerID int) error {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if err := policy.BindEnt(ctx, tx, tenantID); err != nil {
		return kafExecutionFailure(err)
	}
	if !policy.IsCandidate() {
		return nil
	}
	ledger, err := tx.KafTaskActionLedger.Query().Where(kaftaskactionledger.IDEQ(ledgerID), kaftaskactionledger.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return err
	}
	return requireKafExecutionTx(ctx, tx, policy, tenantID, ledger.TaskID)
}

func kafExecutionFailure(err error) error {
	if errors.Is(err, executionscope.ErrDenied) {
		return fmt.Errorf("%w: %w", ErrKafDelegationForbidden, err)
	}
	return fmt.Errorf("KAF execution scope: %w", err)
}
