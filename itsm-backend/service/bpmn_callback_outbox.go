package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/metrics"
	"itsm-backend/service/bpmn"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	bpmnCallbackStatusPending    = "pending"
	bpmnCallbackStatusProcessing = "processing"
	bpmnCallbackStatusCompleted  = "completed"
	bpmnCallbackStatusBlocked    = "blocked"
	bpmnCallbackLeaseDuration    = 60 * time.Second
)

type bpmnCallbackEnqueueRequest struct {
	ExecutionKey      string
	TenantID          int
	ProcessInstanceID int
	ProcessTaskID     int
	TaskID            string
	CallbackKind      string
	HandlerID         string
	TaskType          string
	ElementID         string
	Action            string
	ConfigRef         string
	Variables         map[string]interface{}
	OptionalDeclared  bool
}

type bpmnCallbackExecutor interface {
	executeClaimedCallback(context.Context, string, *ent.ProcessCallbackOutbox) (bpmnCallbackExecutionResult, error)
}

type bpmnCallbackExecutionResult struct {
	CompletionCommitted bool
	Effect              *bpmn.CallbackEffect
}

// bpmnCallbackExecutionError carries only an allowlisted operational class.
// The original handler or advancement error is intentionally not retained.
type bpmnCallbackExecutionError struct {
	errorClass string
}

func (e *bpmnCallbackExecutionError) Error() string {
	return "bpmn callback execution failed"
}

func newBPMNCallbackExecutionError(errorClass string) error {
	return &bpmnCallbackExecutionError{errorClass: errorClass}
}

// newBPMNCallbackHandlerError marks a handler execution failure without
// retaining its raw text in the durable callback path.
func newBPMNCallbackHandlerError(_ error) error {
	return newBPMNCallbackExecutionError("handler_error")
}

// newBPMNCallbackAdvanceError marks token advancement failure without
// retaining its raw text in the durable callback path.
func newBPMNCallbackAdvanceError(_ error) error {
	return newBPMNCallbackExecutionError("advance_error")
}

// bpmnCallbackOutbox owns the durable callback lease lifecycle. Task 3 supplies
// the executor that performs the BPMN-specific callback and token advancement.
type bpmnCallbackOutbox struct {
	client   *ent.Client
	executor bpmnCallbackExecutor
	now      func() time.Time
}

func (o *bpmnCallbackOutbox) enqueue(ctx context.Context, client *ent.Client, request bpmnCallbackEnqueueRequest) (*ent.ProcessCallbackOutbox, error) {
	if client == nil {
		return nil, fmt.Errorf("bpmn callback outbox client is required")
	}
	executionKey := strings.TrimSpace(request.ExecutionKey)
	if executionKey == "" {
		executionKey = uuid.NewString()
	}

	create := client.ProcessCallbackOutbox.Create().
		SetExecutionKey(executionKey).
		SetTenantID(request.TenantID).
		SetProcessInstanceID(request.ProcessInstanceID).
		SetTaskID(request.TaskID).
		SetCallbackKind(request.CallbackKind).
		SetHandlerID(request.HandlerID).
		SetTaskType(request.TaskType).
		SetElementID(request.ElementID).
		SetAction(request.Action).
		SetConfigRef(request.ConfigRef).
		SetOptionalDeclared(request.OptionalDeclared).
		SetStatus(bpmnCallbackStatusPending).
		SetNextAttemptAt(o.clock())
	if request.ProcessTaskID > 0 {
		create.SetProcessTaskID(request.ProcessTaskID)
	}
	if request.Variables != nil {
		create.SetVariables(copyBPMNCallbackVariables(request.Variables))
	}
	return create.Save(ctx)
}

// enqueueBlocked records a definition-time contract failure as a terminal
// outbox row.  It deliberately uses the same durable/audited representation
// as a worker-discovered blocked effect: a malformed definition must never be
// retried as if it were transient infrastructure failure.
func (o *bpmnCallbackOutbox) enqueueBlocked(ctx context.Context, client *ent.Client, request bpmnCallbackEnqueueRequest, code bpmn.CallbackBlockCode) (*ent.ProcessCallbackOutbox, error) {
	if !bpmn.IsAllowedCallbackBlockCode(code) {
		return nil, fmt.Errorf("bpmn callback block code is invalid")
	}
	row, err := o.enqueue(ctx, client, request)
	if err != nil {
		return nil, err
	}
	updated, err := client.ProcessCallbackOutbox.Update().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(row.TenantID), processcallbackoutbox.StatusEQ(bpmnCallbackStatusPending)).
		SetStatus(bpmnCallbackStatusBlocked).
		SetCompletedAt(o.clock()).
		SetLastErrorClass(string(code)).
		Save(ctx)
	if err != nil || updated != 1 {
		return nil, fmt.Errorf("bpmn callback blocked enqueue failed")
	}
	row.Status = bpmnCallbackStatusBlocked
	row.CompletedAt = o.clock()
	row.LastErrorClass = string(code)
	if err := NewBPMNAuditService(client, zap.NewNop().Sugar()).RecordCallbackBlocked(ctx, row, code); err != nil {
		return nil, fmt.Errorf("bpmn callback blocked audit persistence failed")
	}
	metrics.RecordBPMNCallbackEffect(row.HandlerID, row.Action, string(bpmn.CallbackEffectBlocked))
	return row, nil
}

func (o *bpmnCallbackOutbox) processPending(ctx context.Context, workerID string, limit int) (int, error) {
	if err := validateBPMNCallbackWorkerID(workerID); err != nil {
		return 0, err
	}
	if limit <= 0 {
		return 0, nil
	}

	now := o.clock()
	// This is a system worker scan. Every row-specific claim and transition below
	// includes the authoritative tenant predicate carried by the candidate row.
	candidates, err := o.client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.Or(
			processcallbackoutbox.And(
				processcallbackoutbox.StatusEQ(bpmnCallbackStatusPending),
				processcallbackoutbox.NextAttemptAtLTE(now),
			),
			processcallbackoutbox.And(
				processcallbackoutbox.StatusEQ(bpmnCallbackStatusProcessing),
				processcallbackoutbox.LeaseExpiresAtLT(now),
			),
		)).
		Order(ent.Asc(processcallbackoutbox.FieldNextAttemptAt), ent.Asc(processcallbackoutbox.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("bpmn callback candidate scan failed")
	}

	completed := 0
	failed := false
	for _, row := range candidates {
		claimed, claimErr := o.claim(ctx, workerID, row)
		if claimErr != nil {
			failed = true
			continue
		}
		if !claimed {
			continue
		}

		claimedRow := *row
		claimedRow.AttemptCount++
		claimedRow.Status = bpmnCallbackStatusProcessing
		claimedRow.LeaseOwner = workerID
		claimedRow.LeaseExpiresAt = o.clock().Add(bpmnCallbackLeaseDuration)
		claimedRow.Variables = copyBPMNCallbackVariables(row.Variables)
		claimedRow.Variables["bpmn_callback_execution_key"] = claimedRow.ExecutionKey

		if strings.TrimSpace(claimedRow.ExecutionKey) == "" {
			failed = true
			_ = o.retry(ctx, workerID, &claimedRow, "unknown_error")
			continue
		}

		executionCtx := bpmn.WithBPMNCallbackExecutionKey(ctx, claimedRow.ExecutionKey)
		executionResult, err := o.executor.executeClaimedCallback(executionCtx, workerID, &claimedRow)
		if err != nil {
			failed = true
			if retryErr := o.retry(ctx, workerID, &claimedRow, bpmnCallbackExecutionErrorClass(err)); retryErr != nil {
				failed = true
			}
			continue
		}
		if executionResult.CompletionCommitted {
			persisted, verifyErr := o.completionPersisted(ctx, &claimedRow)
			if verifyErr != nil || !persisted {
				failed = true
				_ = o.retry(ctx, workerID, &claimedRow, "lease_lost")
				continue
			}
			completed++
			continue
		}

		outcome := bpmn.ResolveCallbackOutcome(executionResult.Effect, claimedRow.OptionalDeclared)
		persisted, persistErr := o.persistCallbackOutcome(ctx, workerID, &claimedRow, outcome)
		if persistErr != nil || !persisted {
			failed = true
			_ = o.retry(ctx, workerID, &claimedRow, "unknown_error")
			continue
		}
		if outcome.Advance {
			completed++
		}
	}
	if failed {
		return completed, fmt.Errorf("one or more bpmn callbacks were not completed")
	}
	return completed, nil
}

func (o *bpmnCallbackOutbox) processExecutionKeys(ctx context.Context, workerID string, keys []string) (int, error) {
	if err := validateBPMNCallbackWorkerID(workerID); err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 0, nil
	}
	tenantID, err := bpmnCallbackTenantID(ctx)
	if err != nil {
		return 0, err
	}
	now := o.clock()
	rows, err := o.client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.TenantID(tenantID), processcallbackoutbox.ExecutionKeyIn(keys...)).
		Order(ent.Asc(processcallbackoutbox.FieldNextAttemptAt), ent.Asc(processcallbackoutbox.FieldID)).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("bpmn callback execution key scan failed")
	}

	completed := 0
	failed := false
	for _, row := range rows {
		eligible := row.Status == bpmnCallbackStatusPending && !row.NextAttemptAt.After(now)
		eligible = eligible || (row.Status == bpmnCallbackStatusProcessing && row.LeaseExpiresAt.Before(now))
		if !eligible {
			continue
		}
		claimed, claimErr := o.claim(ctx, workerID, row)
		if claimErr != nil || !claimed {
			failed = failed || claimErr != nil
			continue
		}
		claimedRow := *row
		claimedRow.AttemptCount++
		claimedRow.Status = bpmnCallbackStatusProcessing
		claimedRow.LeaseOwner = workerID
		claimedRow.LeaseExpiresAt = o.clock().Add(bpmnCallbackLeaseDuration)
		claimedRow.Variables = copyBPMNCallbackVariables(row.Variables)
		claimedRow.Variables["bpmn_callback_execution_key"] = claimedRow.ExecutionKey
		if strings.TrimSpace(claimedRow.ExecutionKey) == "" {
			failed = true
			_ = o.retry(ctx, workerID, &claimedRow, "unknown_error")
			continue
		}
		executionResult, err := o.executor.executeClaimedCallback(bpmn.WithBPMNCallbackExecutionKey(ctx, claimedRow.ExecutionKey), workerID, &claimedRow)
		if err != nil {
			failed = true
			_ = o.retry(ctx, workerID, &claimedRow, bpmnCallbackExecutionErrorClass(err))
			continue
		}
		if executionResult.CompletionCommitted {
			persisted, verifyErr := o.completionPersisted(ctx, &claimedRow)
			if verifyErr != nil || !persisted {
				failed = true
				_ = o.retry(ctx, workerID, &claimedRow, "lease_lost")
				continue
			}
			completed++
			continue
		}
		outcome := bpmn.ResolveCallbackOutcome(executionResult.Effect, claimedRow.OptionalDeclared)
		persisted, persistErr := o.persistCallbackOutcome(ctx, workerID, &claimedRow, outcome)
		if persistErr != nil || !persisted {
			failed = true
			_ = o.retry(ctx, workerID, &claimedRow, "unknown_error")
			continue
		}
		if outcome.Advance {
			completed++
		}
	}
	if failed {
		return completed, fmt.Errorf("one or more bpmn callbacks were not completed")
	}
	return completed, nil
}

func (o *bpmnCallbackOutbox) claim(ctx context.Context, workerID string, row *ent.ProcessCallbackOutbox) (bool, error) {
	if err := validateBPMNCallbackWorkerID(workerID); err != nil {
		return false, err
	}
	if row == nil || row.TenantID <= 0 {
		return false, fmt.Errorf("bpmn callback row is missing tenant")
	}
	now := o.clock()
	affected, err := o.client.ProcessCallbackOutbox.Update().
		Where(
			processcallbackoutbox.ID(row.ID),
			processcallbackoutbox.TenantID(row.TenantID),
			processcallbackoutbox.Or(
				processcallbackoutbox.And(processcallbackoutbox.StatusEQ(bpmnCallbackStatusPending), processcallbackoutbox.NextAttemptAtLTE(now)),
				processcallbackoutbox.And(processcallbackoutbox.StatusEQ(bpmnCallbackStatusProcessing), processcallbackoutbox.LeaseExpiresAtLT(now)),
			),
		).
		SetStatus(bpmnCallbackStatusProcessing).
		SetLeaseOwner(workerID).
		SetLeaseExpiresAt(now.Add(bpmnCallbackLeaseDuration)).
		AddAttemptCount(1).
		Save(ctx)
	if err != nil {
		return false, fmt.Errorf("bpmn callback claim failed")
	}
	return affected == 1, nil
}

func (o *bpmnCallbackOutbox) complete(ctx context.Context, workerID string, row *ent.ProcessCallbackOutbox) (bool, error) {
	return o.completeWithClient(ctx, o.client, workerID, row)
}

func (o *bpmnCallbackOutbox) completeWithClient(ctx context.Context, client *ent.Client, workerID string, row *ent.ProcessCallbackOutbox) (bool, error) {
	if err := validateBPMNCallbackWorkerID(workerID); err != nil {
		return false, err
	}
	affected, err := client.ProcessCallbackOutbox.Update().
		Where(
			processcallbackoutbox.ID(row.ID),
			processcallbackoutbox.TenantID(row.TenantID),
			processcallbackoutbox.StatusEQ(bpmnCallbackStatusProcessing),
			processcallbackoutbox.LeaseOwner(workerID),
		).
		SetStatus(bpmnCallbackStatusCompleted).
		SetCompletedAt(o.clock()).
		ClearLeaseOwner().
		ClearLeaseExpiresAt().
		ClearLastErrorClass().
		Save(ctx)
	if err != nil {
		return false, fmt.Errorf("bpmn callback completion failed")
	}
	return affected == 1, nil
}

// persistCallbackOutcome atomically makes a handler effect terminal and, for
// blocked outcomes, stores only sanitized audit metadata. A blocked row cannot
// be reclaimed because neither the candidate scan nor claim predicate selects
// the terminal blocked status.
func (o *bpmnCallbackOutbox) persistCallbackOutcome(ctx context.Context, workerID string, row *ent.ProcessCallbackOutbox, outcome bpmn.CallbackOutcome) (bool, error) {
	if err := validateBPMNCallbackWorkerID(workerID); err != nil {
		return false, err
	}
	if row == nil || row.TenantID <= 0 {
		return false, fmt.Errorf("bpmn callback row is missing tenant")
	}
	if outcome.OutboxStatus != bpmn.CallbackOutboxCompleted && outcome.OutboxStatus != bpmn.CallbackOutboxBlocked {
		return false, fmt.Errorf("bpmn callback outcome status is invalid")
	}
	if outcome.OutboxStatus == bpmn.CallbackOutboxBlocked && !bpmn.IsAllowedCallbackBlockCode(outcome.LastErrorClass) {
		return false, fmt.Errorf("bpmn callback block code is invalid")
	}

	tx, err := o.client.Tx(ctx)
	if err != nil {
		return false, fmt.Errorf("bpmn callback outcome transaction failed")
	}
	defer func() { _ = tx.Rollback() }()

	update := tx.Client().ProcessCallbackOutbox.Update().
		Where(
			processcallbackoutbox.ID(row.ID),
			processcallbackoutbox.TenantID(row.TenantID),
			processcallbackoutbox.StatusEQ(bpmnCallbackStatusProcessing),
			processcallbackoutbox.LeaseOwner(workerID),
		).
		SetStatus(outcome.OutboxStatus).
		SetCompletedAt(o.clock()).
		ClearLeaseOwner().
		ClearLeaseExpiresAt()
	if outcome.OutboxStatus == bpmn.CallbackOutboxBlocked {
		update.SetLastErrorClass(string(outcome.LastErrorClass))
	} else {
		update.ClearLastErrorClass()
	}
	affected, err := update.Save(ctx)
	if err != nil {
		return false, fmt.Errorf("bpmn callback outcome persistence failed")
	}
	if affected != 1 {
		return false, fmt.Errorf("bpmn callback lease lost")
	}

	if outcome.AuditAction != "" {
		audit := NewBPMNAuditService(tx.Client(), zap.NewNop().Sugar())
		switch outcome.AuditAction {
		case bpmn.CallbackAuditActionBlocked:
			err = audit.RecordCallbackBlocked(ctx, row, outcome.BlockCode)
		case bpmn.CallbackAuditActionSkippedOptional:
			err = audit.RecordCallbackSkippedOptional(ctx, row, outcome.BlockCode)
		default:
			return false, fmt.Errorf("bpmn callback audit action is invalid")
		}
		if err != nil {
			return false, fmt.Errorf("bpmn callback audit persistence failed")
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("bpmn callback outcome commit failed")
	}
	metrics.RecordBPMNCallbackEffect(row.HandlerID, row.Action, string(outcome.MetricEffect))
	return true, nil
}

func (o *bpmnCallbackOutbox) completionPersisted(ctx context.Context, row *ent.ProcessCallbackOutbox) (bool, error) {
	if row == nil || row.TenantID <= 0 {
		return false, fmt.Errorf("bpmn callback row is missing tenant")
	}
	persisted, err := o.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ID(row.ID),
		processcallbackoutbox.TenantID(row.TenantID),
		processcallbackoutbox.StatusEQ(bpmnCallbackStatusCompleted),
	).Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("bpmn callback completion verification failed")
	}
	return persisted, nil
}

func (o *bpmnCallbackOutbox) retry(ctx context.Context, workerID string, row *ent.ProcessCallbackOutbox, errorClass string) error {
	if err := validateBPMNCallbackWorkerID(workerID); err != nil {
		return err
	}
	if !isBPMNCallbackErrorClass(errorClass) {
		errorClass = "unknown_error"
	}
	affected, err := o.client.ProcessCallbackOutbox.Update().
		Where(
			processcallbackoutbox.ID(row.ID),
			processcallbackoutbox.TenantID(row.TenantID),
			processcallbackoutbox.StatusEQ(bpmnCallbackStatusProcessing),
			processcallbackoutbox.LeaseOwner(workerID),
		).
		SetStatus(bpmnCallbackStatusPending).
		SetNextAttemptAt(o.clock().Add(bpmnCallbackRetryDelay(row.AttemptCount))).
		SetLastErrorClass(errorClass).
		ClearLeaseOwner().
		ClearLeaseExpiresAt().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("bpmn callback retry scheduling failed")
	}
	if affected != 1 {
		return fmt.Errorf("bpmn callback lease lost")
	}
	return nil
}

func (o *bpmnCallbackOutbox) clock() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

func bpmnCallbackRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// 2^9 seconds already exceeds the five-minute ceiling, so cap before
	// shifting to keep unbounded attempt counts from overflowing Duration.
	if attempt >= 10 {
		return 300 * time.Second
	}
	return time.Second << (attempt - 1)
}

func isBPMNCallbackErrorClass(errorClass string) bool {
	switch errorClass {
	case "handler_error", "advance_error", "lease_lost", "unknown_error":
		return true
	default:
		return false
	}
}

func bpmnCallbackExecutionErrorClass(err error) string {
	var executionErr *bpmnCallbackExecutionError
	if errors.As(err, &executionErr) && isBPMNCallbackErrorClass(executionErr.errorClass) {
		return executionErr.errorClass
	}
	return "unknown_error"
}

func validateBPMNCallbackWorkerID(workerID string) error {
	if strings.TrimSpace(workerID) == "" {
		return fmt.Errorf("bpmn callback worker id is required")
	}
	return nil
}

func bpmnCallbackTenantID(ctx context.Context) (int, error) {
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		return tenantID, nil
	}
	if _, present := bpmnAccessScopeValue(ctx); present {
		scope, err := BPMNAccessScopeFromContext(ctx)
		if err == nil && scope.TenantID > 0 {
			return scope.TenantID, nil
		}
	}
	return 0, fmt.Errorf("bpmn callback tenant is required")
}

func copyBPMNCallbackVariables(variables map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(variables)+1)
	for key, value := range variables {
		cloned, err := cloneBPMNJSONValue(value, 0)
		if err == nil {
			copy[key] = cloned
		}
	}
	return copy
}
