package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/kaftaskactionledger"
	"itsm-backend/ent/kaftaskcompletionreceipt"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	entsql "entgo.io/ent/dialect/sql"
)

// CompleteKafDelegatedTask joins BPMN completion, callback scheduling and the
// KAF lease fence in one transaction, then reconciles the durable callback.
func (e *CustomProcessEngine) CompleteKafDelegatedTask(ctx context.Context, ledgerID int, leaseOwner, taskID string, variables map[string]interface{}) error {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID <= 0 {
		return common.NewForbiddenError("KAF completion requires an authenticated tenant")
	}
	ledger, err := e.loadExecutingKafLedger(ctx, ledgerID, leaseOwner)
	if err != nil {
		return fmt.Errorf("load KAF completion ledger: %w", err)
	}
	if tenantID != ledger.TenantID {
		return errors.New("KAF completion ledger does not belong to tenant")
	}
	if ledger.TaskID != taskID {
		return errors.New("KAF completion ledger does not match task")
	}
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, ledger.TenantID)
	ctx = bpmn.WithKafActionScope(ctx, bpmn.NewKafActionScope(
		ledger.ID, ledger.TenantID, ledger.TaskID, ledger.RunID, ledger.StepID,
		ledger.Action, ledger.IdempotencyKey, ledger.CorrelationID,
		ledger.ProcedureRef, ledger.ProcedureVersion,
	))
	fence := kafCompletionFence{ledgerID: ledger.ID, leaseOwner: leaseOwner}
	ctx = context.WithValue(ctx, kafCompletionFenceContextKey{}, fence)

	task, err := e.client.ProcessTask.Query().Where(
		processtask.TaskIDEQ(taskID), processtask.TenantIDEQ(ledger.TenantID),
	).Only(ctx)
	if err != nil {
		return fmt.Errorf("load KAF delegated task: %w", err)
	}
	if task.TaskType != bpmn.KafDelegateTaskType {
		return common.NewForbiddenError("task is not a KAF delegation")
	}
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return common.NewForbiddenError("KAF completion requires an authenticated actor")
	}
	scope := BPMNAccessScope{UserID: userID, TenantID: ledger.TenantID}
	if supplied, present := bpmnAccessScopeValue(ctx); present {
		if supplied.UserID != userID || supplied.TenantID != ledger.TenantID {
			return common.NewForbiddenError("KAF completion actor scope does not match authenticated context")
		}
		scope = supplied
	}
	allowedStatus := common.ProcessTaskStatusDelegated
	if task.Status == common.ProcessTaskStatusCompleted {
		allowedStatus = common.ProcessTaskStatusCompleted
	}
	if err := e.authorizeKafAutomationActorForStatusWithClient(ctx, e.client, task, scope, allowedStatus); err != nil {
		return err
	}
	ctx = WithBPMNAccessScope(ctx, scope)
	receipt, err := e.ensureKafCompletionReceipt(ctx, ledger.ID, ledger.TenantID, taskID)
	if err != nil {
		return err
	}
	if task.Status == common.ProcessTaskStatusCompleted {
		return e.recoverKafCompletionCallback(ctx, ledger.ID, leaseOwner, receipt, task)
	}

	completionVariables := cloneKafVariables(task.TaskVariables)
	for key, value := range variables {
		completionVariables[key] = value
	}
	completionVariables, err = validateAndCloneBPMNParticipantVariables(completionVariables, false)
	if err != nil {
		return err
	}
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start KAF BPMN completion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	executionKeys := make([]string, 0)
	effect, err := e.completeTaskWithClient(ctx, tx.Client(), taskID, completionVariables, &executionKeys)
	if err != nil {
		return err
	}
	if err := assertKafCompletionFence(ctx, tx.Client(), fence); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit KAF BPMN completion transaction: %w", err)
	}
	effect.task.Unwrap()
	return e.finishKafCompletionCallback(ctx, ledger.ID, leaseOwner, receipt, effect, executionKeys)
}

func (e *CustomProcessEngine) finishKafCompletionCallback(ctx context.Context, ledgerID int, leaseOwner string, receipt *ent.KafTaskCompletionReceipt, effect *completedTaskEffect, executionKeys []string) error {
	if _, err := e.loadExecutingKafLedger(ctx, ledgerID, leaseOwner); err != nil {
		return err
	}
	if effect != nil && effect.asyncHandler != nil {
		if _, err := effect.asyncHandler.Execute(ctx, effect.task, effect.variables); err != nil {
			_ = e.updateKafCompletionReceipt(ctx, ledgerID, leaseOwner, receipt.ID, "callback_failed", "callback_failed", nil)
			return errors.New("KAF completion callback failed")
		}
		return e.updateKafCompletionReceipt(ctx, ledgerID, leaseOwner, receipt.ID, "callback_succeeded", "", nil)
	}
	if len(executionKeys) > 0 {
		e.processCommittedCallbackKeys(ctx, receipt.TenantID, executionKeys)
		for _, executionKey := range executionKeys {
			completed, err := e.client.ProcessCallbackOutbox.Query().Where(
				processcallbackoutbox.ExecutionKeyEQ(executionKey),
				processcallbackoutbox.TenantIDEQ(receipt.TenantID),
				processcallbackoutbox.StatusEQ(bpmnCallbackStatusCompleted),
			).Exist(ctx)
			if err != nil || !completed {
				_ = e.updateKafCompletionReceipt(ctx, ledgerID, leaseOwner, receipt.ID, "callback_failed", "callback_failed", nil)
				return errors.New("user task callback failed")
			}
		}
	}
	return e.updateKafCompletionReceipt(ctx, ledgerID, leaseOwner, receipt.ID, "callback_succeeded", "", nil)
}

func (e *CustomProcessEngine) recoverKafCompletionCallback(ctx context.Context, ledgerID int, leaseOwner string, receipt *ent.KafTaskCompletionReceipt, task *ent.ProcessTask) error {
	if receipt.Status == "callback_succeeded" {
		return nil
	}
	if _, err := e.loadExecutingKafLedger(ctx, ledgerID, leaseOwner); err != nil {
		return err
	}
	rows, err := e.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantIDEQ(task.TenantID),
		processcallbackoutbox.ProcessTaskIDEQ(task.ID),
		processcallbackoutbox.CallbackKindEQ("user_task_callback"),
	).All(ctx)
	if err != nil {
		return fmt.Errorf("load durable KAF callback: %w", err)
	}
	if len(rows) == 0 {
		return e.enqueueRecoveredKafCallback(ctx, ledgerID, leaseOwner, receipt, task)
	}
	if err := e.makeKafCallbacksDue(ctx, ledgerID, leaseOwner, task); err != nil {
		return err
	}
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, row.ExecutionKey)
	}
	return e.finishKafCompletionCallback(ctx, ledgerID, leaseOwner, receipt, &completedTaskEffect{task: task}, keys)
}

func (e *CustomProcessEngine) makeKafCallbacksDue(ctx context.Context, ledgerID int, leaseOwner string, task *ent.ProcessTask) error {
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start KAF callback retry transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Client().ProcessCallbackOutbox.Update().Where(
		processcallbackoutbox.TenantIDEQ(task.TenantID),
		processcallbackoutbox.ProcessTaskIDEQ(task.ID),
		processcallbackoutbox.CallbackKindEQ("user_task_callback"),
		processcallbackoutbox.StatusEQ(bpmnCallbackStatusPending),
	).SetNextAttemptAt(time.Now()).Save(ctx); err != nil {
		return fmt.Errorf("schedule KAF callback retry: %w", err)
	}
	if err := assertKafCompletionFence(ctx, tx.Client(), kafCompletionFence{ledgerID: ledgerID, leaseOwner: leaseOwner}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit KAF callback retry transaction: %w", err)
	}
	return nil
}

func (e *CustomProcessEngine) enqueueRecoveredKafCallback(ctx context.Context, ledgerID int, leaseOwner string, receipt *ent.KafTaskCompletionReceipt, task *ent.ProcessTask) error {
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start KAF callback recovery transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var process *BPMNProcess
	legacyTaskType, _ := task.TaskVariables[bpmnMetaDataServiceTaskType].(string)
	if strings.TrimSpace(task.CallbackHandlerID) == "" && strings.TrimSpace(legacyTaskType) == "" {
		instance, loadErr := tx.Client().ProcessInstance.Query().Where(
			processinstance.IDEQ(task.ProcessInstanceID), processinstance.TenantIDEQ(task.TenantID),
		).Only(ctx)
		if loadErr != nil {
			return fmt.Errorf("load KAF callback process instance: %w", loadErr)
		}
		definition, loadErr := tx.Client().ProcessDefinition.Query().Where(
			processdefinition.IDEQ(instance.ProcessDefinitionID), processdefinition.TenantIDEQ(task.TenantID),
		).Only(ctx)
		if loadErr != nil {
			return fmt.Errorf("load KAF callback process definition: %w", loadErr)
		}
		definitions, parseErr := e.parser.ParseXML(definition.BpmnXML)
		if parseErr != nil || len(definitions.Processes) == 0 {
			return errors.New("parse KAF callback process definition failed")
		}
		process = definitions.Processes[0]
	}
	descriptor, err := e.recoveredKafCallbackDescriptor(ctx, tx.Client(), task, process)
	if err != nil {
		return err
	}
	if descriptor.HandlerID == bpmnNoUserTaskCallbackHandlerID {
		if err := assertKafCompletionFence(ctx, tx.Client(), kafCompletionFence{ledgerID: ledgerID, leaseOwner: leaseOwner}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return e.updateKafCompletionReceipt(ctx, ledgerID, leaseOwner, receipt.ID, "callback_succeeded", "", nil)
	}
	handler := e.resolveCallbackDescriptorHandler(descriptor)
	if handler == nil {
		return errors.New("KAF completion callback handler is unavailable")
	}
	if isAsyncHandler(handler) {
		payload, err := filterBPMNCallbackPayload(handler, descriptor.Action, task.TaskVariables)
		if err != nil {
			return err
		}
		if err := assertKafCompletionFence(ctx, tx.Client(), kafCompletionFence{ledgerID: ledgerID, leaseOwner: leaseOwner}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		payload[bpmnMetaDataAction] = descriptor.Action
		return e.finishKafCompletionCallback(ctx, ledgerID, leaseOwner, receipt, &completedTaskEffect{task: task, variables: payload, asyncHandler: handler}, nil)
	}
	action := descriptor.Action
	if handler.GetTaskType() == "cc_task" {
		action = ""
	}
	plan, err := BuildCallbackEnqueuePlan(CallbackDescriptor{
		HandlerID: descriptor.HandlerID, TaskType: descriptor.TaskType, Action: action, ConfigRef: descriptor.ConfigRef,
	}, task.TaskVariables, false, e.callbackRegistry)
	if err != nil {
		return err
	}
	keys := make([]string, 0, 1)
	txEngine := e.forClient(tx.Client(), &keys)
	if err := txEngine.enqueueUserTaskCallback(ctx, task, descriptor, plan); err != nil {
		return err
	}
	if err := assertKafCompletionFence(ctx, tx.Client(), kafCompletionFence{ledgerID: ledgerID, leaseOwner: leaseOwner}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit KAF callback recovery transaction: %w", err)
	}
	return e.finishKafCompletionCallback(ctx, ledgerID, leaseOwner, receipt, &completedTaskEffect{task: task}, keys)
}

func (e *CustomProcessEngine) recoveredKafCallbackDescriptor(ctx context.Context, client *ent.Client, task *ent.ProcessTask, process *BPMNProcess) (bpmnCallbackDescriptor, error) {
	if strings.TrimSpace(task.CallbackHandlerID) != "" {
		return e.descriptorForProcessTask(ctx, client, task, process)
	}
	taskType, _ := task.TaskVariables[bpmnMetaDataServiceTaskType].(string)
	if strings.TrimSpace(taskType) == "" {
		if process == nil {
			return bpmnCallbackDescriptor{}, errors.New("KAF callback process definition is required")
		}
		return e.descriptorForProcessTask(ctx, client, task, process)
	}
	action, _ := task.TaskVariables[bpmnMetaDataAction].(string)
	descriptor := e.callbackDescriptor(taskType, action, "")
	if descriptor.HandlerID == bpmnUnresolvedUserTaskCallbackHandlerID {
		return bpmnCallbackDescriptor{}, errors.New("KAF completion callback handler is unavailable")
	}
	if err := client.ProcessTask.UpdateOneID(task.ID).
		Where(processtask.TenantID(task.TenantID)).
		SetCallbackHandlerID(descriptor.HandlerID).
		SetCallbackTaskType(descriptor.TaskType).
		SetCallbackAction(descriptor.Action).
		SetCallbackConfigRef(descriptor.ConfigRef).
		Exec(ctx); err != nil {
		return bpmnCallbackDescriptor{}, fmt.Errorf("persist recovered KAF callback descriptor: %w", err)
	}
	task.CallbackHandlerID = descriptor.HandlerID
	task.CallbackTaskType = descriptor.TaskType
	task.CallbackAction = descriptor.Action
	return descriptor, nil
}

func (e *CustomProcessEngine) loadExecutingKafLedger(ctx context.Context, ledgerID int, leaseOwner string) (*ent.KafTaskActionLedger, error) {
	if strings.TrimSpace(leaseOwner) == "" {
		return nil, errors.New("KAF completion lease owner is required")
	}
	ledger, err := e.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.IDEQ(ledgerID),
		kaftaskactionledger.ResultStatusEQ("executing"),
		kaftaskactionledger.LeaseOwnerEQ(leaseOwner),
		kaftaskactionledger.LeaseExpiresAtGT(time.Now()),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.New("KAF completion lease owner is stale or expired")
	}
	if err != nil {
		return nil, fmt.Errorf("load executing KAF completion ledger: %w", err)
	}
	return ledger, nil
}

func (e *CustomProcessEngine) ensureKafCompletionReceipt(ctx context.Context, ledgerID, tenantID int, taskID string) (*ent.KafTaskCompletionReceipt, error) {
	receipt, err := e.client.KafTaskCompletionReceipt.Query().Where(kaftaskcompletionreceipt.LedgerIDEQ(ledgerID)).Only(ctx)
	if err == nil {
		if receipt.TenantID != tenantID || receipt.TaskID != taskID {
			return nil, errors.New("KAF completion receipt scope mismatch")
		}
		return receipt, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("load KAF completion receipt: %w", err)
	}
	err = e.runKafFencedWrite(ctx, func(client *ent.Client) error {
		var createErr error
		receipt, createErr = client.KafTaskCompletionReceipt.Create().
			SetLedgerID(ledgerID).SetTenantID(tenantID).SetTaskID(taskID).
			SetStatus("callback_pending").Save(ctx)
		return createErr
	})
	if err == nil {
		return receipt, nil
	}
	if !ent.IsConstraintError(err) {
		return nil, err
	}
	return e.ensureKafCompletionReceipt(ctx, ledgerID, tenantID, taskID)
}

func (e *CustomProcessEngine) updateKafCompletionReceipt(ctx context.Context, ledgerID int, leaseOwner string, receiptID int, status, errorCode string, callbackErr error) error {
	update := e.client.KafTaskCompletionReceipt.Update().Where(
		kaftaskcompletionreceipt.IDEQ(receiptID),
		kaftaskcompletionreceipt.LedgerIDEQ(ledgerID),
		kaftaskcompletionreceipt.StatusIn("callback_pending", "callback_failed"),
		kafReceiptOwnedByExecutingLease(ledgerID, leaseOwner, time.Now()),
	).SetStatus(status)
	if errorCode == "" {
		update.ClearErrorCode()
	} else {
		update.SetErrorCode(errorCode)
	}
	updated, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("update KAF completion receipt: %w", err)
	}
	if updated != 1 {
		receipt, loadErr := e.client.KafTaskCompletionReceipt.Get(ctx, receiptID)
		if loadErr == nil && receipt.Status == "callback_succeeded" && status == "callback_succeeded" {
			return nil
		}
		return errors.New("KAF completion receipt transition is stale or non-monotonic")
	}
	return callbackErr
}

func kafReceiptOwnedByExecutingLease(ledgerID int, leaseOwner string, now time.Time) predicate.KafTaskCompletionReceipt {
	return func(selector *entsql.Selector) {
		ledger := entsql.Table(kaftaskactionledger.Table)
		owned := entsql.Select(ledger.C(kaftaskactionledger.FieldID)).From(ledger).Where(entsql.And(
			entsql.EQ(ledger.C(kaftaskactionledger.FieldID), ledgerID),
			entsql.EQ(ledger.C(kaftaskactionledger.FieldResultStatus), "executing"),
			entsql.EQ(ledger.C(kaftaskactionledger.FieldLeaseOwner), leaseOwner),
			entsql.GT(ledger.C(kaftaskactionledger.FieldLeaseExpiresAt), now),
		))
		selector.Where(entsql.In(selector.C(kaftaskcompletionreceipt.FieldLedgerID), owned))
	}
}

func (e *CustomProcessEngine) runKafFencedWrite(ctx context.Context, write func(*ent.Client) error) error {
	fence, fenced := ctx.Value(kafCompletionFenceContextKey{}).(kafCompletionFence)
	if !fenced {
		return write(e.client)
	}
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start KAF owner-fenced write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := write(tx.Client()); err != nil {
		return err
	}
	if err := assertKafCompletionFence(ctx, tx.Client(), fence); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit KAF owner-fenced write: %w", err)
	}
	return nil
}

func assertKafCompletionFence(ctx context.Context, client *ent.Client, fence kafCompletionFence) error {
	now := time.Now()
	updated, err := client.KafTaskActionLedger.Update().Where(
		kaftaskactionledger.IDEQ(fence.ledgerID),
		kaftaskactionledger.ResultStatusEQ("executing"),
		kaftaskactionledger.LeaseOwnerEQ(fence.leaseOwner),
		kaftaskactionledger.LeaseExpiresAtGT(now),
	).SetUpdatedAt(now).Save(ctx)
	if err != nil {
		return fmt.Errorf("fence KAF completion write: %w", err)
	}
	if updated != 1 {
		return errors.New("KAF completion lease owner is stale or expired")
	}
	return nil
}

func (e *CustomProcessEngine) validateWorkItemRecordClassInputWithClient(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance, variables map[string]interface{}) error {
	provided, present := variables["record_class"]
	if !present {
		return nil
	}
	if instance.BusinessID <= 0 {
		return fmt.Errorf("WorkItem process instance %d has no work item ID", instance.ID)
	}
	workItem, policy, err := authorization.ResolveWorkItemIdentity(ctx, client, instance.BusinessID, instance.TenantID)
	if err != nil {
		return fmt.Errorf("load WorkItem record class: %w", err)
	}
	if string(policy.BusinessType) != instance.BusinessType {
		return errors.New("process business type conflicts with persisted work item record class")
	}
	recordClass, ok := provided.(string)
	if !ok || recordClass != workItem.RecordClass {
		return errors.New("record class variable conflicts with persisted work item record class")
	}
	return nil
}
