package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"time"
)

func (e *CustomProcessEngine) TerminateProcess(ctx context.Context, processInstanceID, reason string) error {
	tx, err := e.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = e.TerminateProcessTx(ctx, tx, processInstanceID, reason); err != nil {
		return err
	}
	return tx.Commit()
}

// TerminateProcessTx applies the existing instance/task CAS and audit in the caller's
// transaction. Authorization remains the trusted instance mutation scope; the caller
// must roll back on any error and owns commit. Unresolved callbacks block termination.
func (e *CustomProcessEngine) TerminateProcessTx(ctx context.Context, tx *ent.Tx, processInstanceID, reason string) error {
	if tx == nil || e.transactionBound {
		return errors.New("TerminateProcessTx requires a caller transaction and the root process engine")
	}
	if err := e.requireActorSnapshot(ctx, tx); err != nil {
		return err
	}
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return err
	}
	txEngine := e.forClient(tx.Client(), nil, tx)
	instance, err := txEngine.instanceAccessPolicy.loadForUpdate(ctx, processInstanceID)
	if err != nil {
		return err
	}
	if err := ValidateBPMNProcessLifecycle(BPMNProcessCommandTerminate, instance.Status); err != nil {
		return err
	}
	actor, err := txEngine.loadTaskMutationActor(ctx, tx.Client(), scope)
	if err != nil {
		return err
	}

	unresolved, err := tx.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.TenantID(scope.TenantID), processcallbackoutbox.ProcessInstanceID(instance.ID), processcallbackoutbox.StatusNEQ("completed")).Exist(ctx)
	if err != nil {
		return err
	}
	if unresolved {
		return common.NewConflictError("BPMN termination", "prior callback is unresolved")
	}
	terminatedAt := time.Now()

	predicate, err := bpmnProcessLifecyclePredicate(BPMNProcessCommandTerminate, instance.Version)
	if err != nil {
		return err
	}
	affected, err := tx.Client().ProcessInstance.Update().Where(
		processinstance.ID(instance.ID), processinstance.TenantID(scope.TenantID), predicate,
	).
		SetStatus("terminated").
		SetEndTime(terminatedAt).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("终止流程实例失败: %w", err)
	}
	if affected != 1 {
		return bpmnProcessLifecycleConflict(BPMNProcessCommandTerminate)
	}
	activeStatuses, err := bpmnTaskSourceStatuses(BPMNTaskCommandCancel)
	if err != nil {
		return err
	}
	activeTasks, err := tx.Client().ProcessTask.Query().Where(
		processtask.ProcessInstanceID(instance.ID), processtask.TenantID(scope.TenantID),
		processtask.StatusIn(activeStatuses...),
	).All(ctx)
	if err != nil {
		return fmt.Errorf("加载待取消流程任务失败: %w", err)
	}
	for _, task := range activeTasks {
		taskPredicate, predicateErr := bpmnTaskLifecyclePredicate(BPMNTaskCommandCancel, task.AggregationVersion)
		if predicateErr != nil {
			return predicateErr
		}
		cancelled, updateErr := tx.Client().ProcessTask.Update().Where(
			processtask.ID(task.ID), processtask.TenantID(scope.TenantID), taskPredicate,
		).SetStatus(common.ProcessTaskStatusCancelled).
			SetCompletedTime(terminatedAt).
			AddAggregationVersion(1).
			Save(ctx)
		if updateErr != nil {
			return fmt.Errorf("取消流程任务失败: %w", updateErr)
		}
		if cancelled != 1 {
			return bpmnTaskLifecycleConflict(BPMNTaskCommandCancel)
		}
	}
	if err := e.auditService.ForClient(tx.Client()).RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           instance.CurrentActivityID,
		ActivityName:         instance.CurrentActivityName,
		ActivityType:         ActivityTypeEndEvent,
		Action:               AuditActionProcessTerminated,
		UserID:               actor.ID,
		UserName:             actor.Name,
		VariablesBefore:      map[string]interface{}{"status": instance.Status},
		VariablesAfter:       map[string]interface{}{"status": "terminated"},
		Comment:              reason,
		TenantID:             instance.TenantID,
	}); err != nil {
		return err
	}
	return nil
}
