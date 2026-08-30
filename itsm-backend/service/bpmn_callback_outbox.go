package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/service/bpmn"

	"github.com/google/uuid"
)

const (
	bpmnCallbackStatusPending    = "pending"
	bpmnCallbackStatusProcessing = "processing"
	bpmnCallbackStatusCompleted  = "completed"
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
	Variables         map[string]interface{}
}

type bpmnCallbackExecutor interface {
	executeClaimedCallback(context.Context, string, *ent.ProcessCallbackOutbox) error
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

func (o *bpmnCallbackOutbox) processPending(ctx context.Context, workerID string, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	if strings.TrimSpace(workerID) == "" {
		return 0, fmt.Errorf("bpmn callback worker id is required")
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
		if err := o.executor.executeClaimedCallback(executionCtx, workerID, &claimedRow); err != nil {
			failed = true
			if retryErr := o.retry(ctx, workerID, &claimedRow, "handler_error"); retryErr != nil {
				failed = true
			}
			continue
		}

		completedRow, completeErr := o.complete(ctx, workerID, &claimedRow)
		if completeErr != nil || !completedRow {
			failed = true
			continue
		}
		completed++
	}
	if failed {
		return completed, fmt.Errorf("one or more bpmn callbacks were not completed")
	}
	return completed, nil
}

func (o *bpmnCallbackOutbox) processExecutionKeys(ctx context.Context, workerID string, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	now := o.clock()
	rows, err := o.client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ExecutionKeyIn(keys...)).
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
		if err := o.executor.executeClaimedCallback(bpmn.WithBPMNCallbackExecutionKey(ctx, claimedRow.ExecutionKey), workerID, &claimedRow); err != nil {
			failed = true
			_ = o.retry(ctx, workerID, &claimedRow, "handler_error")
			continue
		}
		completedRow, completeErr := o.complete(ctx, workerID, &claimedRow)
		if completeErr != nil || !completedRow {
			failed = true
			continue
		}
		completed++
	}
	if failed {
		return completed, fmt.Errorf("one or more bpmn callbacks were not completed")
	}
	return completed, nil
}

func (o *bpmnCallbackOutbox) claim(ctx context.Context, workerID string, row *ent.ProcessCallbackOutbox) (bool, error) {
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
	affected, err := o.client.ProcessCallbackOutbox.Update().
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

func (o *bpmnCallbackOutbox) retry(ctx context.Context, workerID string, row *ent.ProcessCallbackOutbox, errorClass string) error {
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

func copyBPMNCallbackVariables(variables map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(variables)+1)
	for key, value := range variables {
		copy[key] = value
	}
	return copy
}
