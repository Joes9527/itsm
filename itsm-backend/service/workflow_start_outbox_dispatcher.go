package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/service/bpmn"
)

type WorkflowStartOutboxConfig struct {
	BatchSize    int
	PollInterval time.Duration
	MaxAttempts  int
}

type WorkflowStartRequested struct {
	TenantID                  int    `json:"tenantId"`
	WorkItemID                int    `json:"workItemId"`
	RecordClass               string `json:"recordClass"`
	WorkflowDefinitionID      int    `json:"workflowDefinitionId"`
	WorkflowDefinitionKey     string `json:"workflowDefinitionKey"`
	WorkflowDefinitionVersion string `json:"workflowDefinitionVersion"`
	ActorID                   int    `json:"actorId"`
	Channel                   string `json:"channel"`
	IntakeRequestID           int    `json:"intakeRequestId"`
	DedupeKey                 string `json:"dedupeKey"`
}

type frozenDefinitionProcessEngine interface {
	StartProcessByDefinitionID(context.Context, int, string, string, int, map[string]any) (*ent.ProcessInstance, error)
}

type workflowStartRepository interface {
	ClaimDueByEventType(context.Context, time.Time, int, string) ([]*ent.OutboxEvent, error)
	MarkRetry(context.Context, int, string, string, time.Time) error
	MarkDead(context.Context, int, string, string, OutboxRetryAudit) error
	MarkPublished(context.Context, int, string, time.Time) error
}

type WorkflowStartOutboxDispatcher struct {
	repository workflowStartRepository
	engine     frozenDefinitionProcessEngine
	config     WorkflowStartOutboxConfig
	now        func() time.Time
}

func NewWorkflowStartOutboxDispatcher(repository workflowStartRepository, engine frozenDefinitionProcessEngine, config WorkflowStartOutboxConfig) *WorkflowStartOutboxDispatcher {
	if config.BatchSize <= 0 {
		config.BatchSize = 20
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 5 * time.Second
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 10
	}
	return &WorkflowStartOutboxDispatcher{repository: repository, engine: engine, config: config, now: time.Now}
}

func (d *WorkflowStartOutboxDispatcher) DispatchOnce(ctx context.Context) error {
	if d == nil || d.repository == nil || d.engine == nil {
		return fmt.Errorf("workflow start dispatcher is not configured")
	}
	events, err := d.repository.ClaimDueByEventType(ctx, d.now().UTC(), d.config.BatchSize, workflowStartRequestedEventType)
	if err != nil {
		return fmt.Errorf("claim workflow start events: %w", err)
	}
	for _, event := range events {
		if err := d.dispatchEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (d *WorkflowStartOutboxDispatcher) Run(ctx context.Context) {
	_ = d.DispatchOnce(ctx)
	ticker := time.NewTicker(d.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = d.DispatchOnce(ctx)
		}
	}
}

func (d *WorkflowStartOutboxDispatcher) dispatchEvent(ctx context.Context, event *ent.OutboxEvent) error {
	eventContext := tenantctx.WithTenantID(ctx, event.TenantID)
	payload, err := decodeWorkflowStartRequested(event)
	if err == nil {
		tenantContext := tenantctx.WithTenantID(eventContext, payload.TenantID)
		tenantContext = context.WithValue(tenantContext, bpmn.BPMNTenantIDContextKey, payload.TenantID)
		_, err = d.engine.StartProcessByDefinitionID(
			tenantContext, payload.WorkflowDefinitionID, payload.DedupeKey, "work_item", payload.WorkItemID,
			map[string]any{
				"tenant_id": payload.TenantID, "workItemId": payload.WorkItemID, "recordClass": payload.RecordClass,
				"actorId": payload.ActorID, "channel": payload.Channel, "intakeRequestId": payload.IntakeRequestID,
			},
		)
	}
	if err != nil {
		return d.recordFailure(eventContext, event, err)
	}
	if err := d.repository.MarkPublished(eventContext, event.ID, event.ClaimToken, d.now().UTC()); err != nil {
		return fmt.Errorf("mark workflow start published: %w", err)
	}
	return nil
}

func decodeWorkflowStartRequested(event *ent.OutboxEvent) (WorkflowStartRequested, error) {
	if event == nil || event.EventType != workflowStartRequestedEventType || event.AggregateType != "work_item" {
		return WorkflowStartRequested{}, fmt.Errorf("unsupported workflow start event envelope")
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	var payload WorkflowStartRequested
	if err := decoder.Decode(&payload); err != nil {
		return WorkflowStartRequested{}, fmt.Errorf("invalid workflow start payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return WorkflowStartRequested{}, fmt.Errorf("workflow start payload must contain one object")
	}
	if payload.TenantID <= 0 || payload.WorkItemID <= 0 || payload.WorkflowDefinitionID <= 0 || payload.ActorID <= 0 || payload.IntakeRequestID <= 0 {
		return WorkflowStartRequested{}, fmt.Errorf("workflow start payload is missing required IDs")
	}
	if payload.TenantID != event.TenantID || strconv.Itoa(payload.WorkItemID) != event.AggregateID {
		return WorkflowStartRequested{}, fmt.Errorf("workflow start payload does not match its tenant or aggregate")
	}
	if payload.RecordClass != "incident" && payload.RecordClass != "service_request_item" {
		return WorkflowStartRequested{}, fmt.Errorf("workflow start record class is unsupported")
	}
	if strings.TrimSpace(payload.WorkflowDefinitionKey) == "" || strings.TrimSpace(payload.WorkflowDefinitionVersion) == "" || strings.TrimSpace(payload.Channel) == "" {
		return WorkflowStartRequested{}, fmt.Errorf("workflow start payload is missing frozen binding metadata")
	}
	expectedDedupeKey := NewWorkflowStartEventID(payload.WorkItemID, payload.WorkflowDefinitionID)
	if payload.DedupeKey != event.EventID || payload.DedupeKey != expectedDedupeKey {
		return WorkflowStartRequested{}, fmt.Errorf("workflow start dedupe key is invalid")
	}
	return payload, nil
}

func (d *WorkflowStartOutboxDispatcher) recordFailure(ctx context.Context, event *ent.OutboxEvent, deliveryErr error) error {
	attempt := event.AttemptCount + 1
	if attempt >= d.config.MaxAttempts {
		err := d.repository.MarkDead(ctx, event.ID, event.ClaimToken, deliveryErr.Error(), OutboxRetryAudit{
			TenantID: event.TenantID, RequestID: event.EventID,
			Resource: "work_item:" + event.AggregateID, Action: "intake.workflow_start.manual_intervention_required",
			Path: "workflow/start", Method: "DISPATCH", StatusCode: 500,
		})
		if err != nil {
			return fmt.Errorf("mark workflow start dead: %w", err)
		}
		return nil
	}
	nextAttemptAt := d.now().UTC().Add(workflowStartRetryDelay(attempt))
	if err := d.repository.MarkRetry(ctx, event.ID, event.ClaimToken, deliveryErr.Error(), nextAttemptAt); err != nil {
		return fmt.Errorf("schedule workflow start retry: %w", err)
	}
	return nil
}

func workflowStartRetryDelay(attempt int) time.Duration {
	const maximum = 5 * time.Minute
	delay := time.Second
	for i := 1; i < attempt && delay < maximum; i++ {
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}
