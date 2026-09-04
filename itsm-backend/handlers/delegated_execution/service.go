package delegated_execution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	itsmservice "itsm-backend/service"
)

const (
	kafDelegateRequestedEventType = "kaf_delegate_requested"
	outboxStatusBlocked           = "blocked"
	outboxStatusPending           = "pending"
	deliveryUnknownPrefix         = "delivery_unknown:"
	conclusionNotAccepted         = "not_accepted_not_started"
	conclusionDeliveryUnknown     = "delivery_unknown_manual_followup"
)

// ReconcileRequest records a bounded operator conclusion. Reason is required
// for accountability but is intentionally not persisted as free text.
type ReconcileRequest struct {
	Conclusion string
	Reason     string
	ActorID    int
}

type reconciliationAuditRecord struct {
	Conclusion    string `json:"conclusion"`
	Reason        string `json:"reason"`
	TaskID        string `json:"taskId"`
	CorrelationID string `json:"correlationId,omitempty"`
}

// RequeueRequest is the narrowly permitted repair request. It may only follow
// a stored not_accepted_not_started reconciliation conclusion.
type RequeueRequest struct {
	ActorID int
}

// ListFilter only exposes operational identifiers. Outbox payload, delivery
// error text, and leases remain internal because they can contain sensitive
// data from the delegated task.
type ListFilter struct {
	EventID string
	TaskID  string
	Status  string
	Page    int
	Size    int
}

type DelegatedExecution struct {
	EventID       string     `json:"eventId"`
	TaskID        string     `json:"taskId"`
	CorrelationID string     `json:"correlationId,omitempty"`
	Status        string     `json:"status"`
	AttemptCount  int        `json:"attemptCount"`
	ErrorClass    string     `json:"errorClass,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	PublishedAt   *time.Time `json:"publishedAt,omitempty"`
}

type ListResult struct {
	Items []DelegatedExecution `json:"items"`
	Total int                  `json:"total"`
	Page  int                  `json:"page"`
	Size  int                  `json:"size"`
}

type Service struct {
	client *ent.Client
	now    func() time.Time
}

func NewService(client *ent.Client) *Service {
	return &Service{client: client, now: time.Now}
}

func (s *Service) Reconcile(ctx context.Context, tenantID int, eventID string, request ReconcileRequest) error {
	if s == nil || s.client == nil {
		return common.NewInternalError("delegated execution service is unavailable", nil)
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if strings.TrimSpace(eventID) == "" || request.Reason == "" {
		return common.NewValidationError("eventId and reason are required", nil)
	}
	if len(request.Reason) > 1024 {
		return common.NewValidationError("reconciliation reason must not exceed 1024 characters", nil)
	}
	if request.Conclusion != conclusionNotAccepted && request.Conclusion != conclusionDeliveryUnknown {
		return common.NewValidationError("unsupported reconciliation conclusion", nil)
	}
	event, err := s.client.OutboxEvent.Query().Where(
		outboxevent.EventIDEQ(eventID), outboxevent.TenantIDEQ(tenantID), outboxevent.EventTypeEQ(kafDelegateRequestedEventType),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return common.NewNotFoundError("delegated execution")
	}
	if err != nil {
		return fmt.Errorf("query delegated execution for reconciliation: %w", err)
	}
	var payload itsmservice.KafDelegateRequested
	_ = json.Unmarshal(event.Payload, &payload)
	auditBody, err := json.Marshal(reconciliationAuditRecord{
		Conclusion: request.Conclusion, Reason: request.Reason, TaskID: event.AggregateID, CorrelationID: payload.CorrelationID,
	})
	if err != nil {
		return fmt.Errorf("serialize delegated execution reconciliation audit: %w", err)
	}
	if err := s.client.AuditLog.Create().SetTenantID(tenantID).SetUserID(request.ActorID).
		SetRequestID(eventID).SetResource("delegated_execution").SetAction("delegated_execution.reconcile").
		SetPath("delegated-executions/" + eventID + "/reconcile").SetMethod("POST").SetStatusCode(200).
		SetRequestBody(string(auditBody)).Exec(ctx); err != nil {
		return fmt.Errorf("audit delegated execution reconciliation: %w", err)
	}
	return nil
}

// List returns tenant-scoped operational state without exposing an outbox
// payload or delivery failure detail. The query is deliberately bounded so an
// operator view cannot become a bulk export surface.
func (s *Service) List(ctx context.Context, tenantID int, filter ListFilter) (ListResult, error) {
	if s == nil || s.client == nil {
		return ListResult{}, common.NewInternalError("delegated execution service is unavailable", nil)
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Size < 1 {
		filter.Size = 20
	}
	if filter.Size > 100 {
		filter.Size = 100
	}
	if filter.Status != "" && !isSupportedStatus(filter.Status) {
		return ListResult{}, common.NewValidationError("unsupported delegated execution status", nil)
	}
	query := s.client.OutboxEvent.Query().Where(
		outboxevent.TenantIDEQ(tenantID),
		outboxevent.EventTypeEQ(kafDelegateRequestedEventType),
	)
	if filter.EventID != "" {
		query.Where(outboxevent.EventIDEQ(filter.EventID))
	}
	if filter.TaskID != "" {
		query.Where(outboxevent.AggregateIDEQ(filter.TaskID))
	}
	if filter.Status != "" {
		query.Where(outboxevent.StatusEQ(filter.Status))
	}
	total, err := query.Count(ctx)
	if err != nil {
		return ListResult{}, fmt.Errorf("count delegated executions: %w", err)
	}
	events, err := query.Order(ent.Desc(outboxevent.FieldCreatedAt)).Offset((filter.Page - 1) * filter.Size).Limit(filter.Size).All(ctx)
	if err != nil {
		return ListResult{}, fmt.Errorf("list delegated executions: %w", err)
	}
	items := make([]DelegatedExecution, 0, len(events))
	for _, event := range events {
		item := DelegatedExecution{EventID: event.EventID, TaskID: event.AggregateID, Status: event.Status, AttemptCount: event.AttemptCount, CreatedAt: event.CreatedAt, ErrorClass: deliveryErrorClass(event.LastError)}
		if !event.PublishedAt.IsZero() {
			publishedAt := event.PublishedAt
			item.PublishedAt = &publishedAt
		}
		var payload itsmservice.KafDelegateRequested
		if err := json.Unmarshal(event.Payload, &payload); err == nil {
			item.CorrelationID = payload.CorrelationID
		}
		items = append(items, item)
	}
	return ListResult{Items: items, Total: total, Page: filter.Page, Size: filter.Size}, nil
}

func isSupportedStatus(status string) bool {
	for _, supported := range []string{"pending", "publishing", "published", "blocked", "dead_letter"} {
		if status == supported {
			return true
		}
	}
	return false
}

func deliveryErrorClass(lastError string) string {
	if strings.HasPrefix(lastError, deliveryUnknownPrefix) {
		return "delivery_unknown"
	}
	if lastError != "" {
		return "delivery_failed"
	}
	return ""
}

// Requeue restores only an unambiguously unstarted, tenant-owned KAF event.
// A delivery_unknown event may have completed externally and is therefore
// reconcile-only: automatic or operator-initiated resend is forbidden.
func (s *Service) Requeue(ctx context.Context, tenantID int, eventID string, request RequeueRequest) error {
	if s == nil || s.client == nil {
		return common.NewInternalError("delegated execution service is unavailable", nil)
	}
	if strings.TrimSpace(eventID) == "" {
		return common.NewValidationError("eventId is required", nil)
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start delegated execution requeue transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	event, err := tx.OutboxEvent.Query().Where(
		outboxevent.EventIDEQ(eventID),
		outboxevent.TenantIDEQ(tenantID),
		outboxevent.EventTypeEQ(kafDelegateRequestedEventType),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return common.NewNotFoundError("delegated execution")
	}
	if err != nil {
		return fmt.Errorf("query delegated execution: %w", err)
	}
	if event.Status != outboxStatusBlocked {
		return common.NewConflictError("delegated execution", "only blocked events may be requeued")
	}
	if strings.HasPrefix(event.LastError, deliveryUnknownPrefix) {
		return common.NewConflictError("delegated execution", "delivery_unknown is reconcile-only and cannot be requeued")
	}
	reconciliations, err := tx.AuditLog.Query().Where(
		auditlog.TenantIDEQ(tenantID), auditlog.RequestIDEQ(eventID),
		auditlog.ActionEQ("delegated_execution.reconcile"),
	).All(ctx)
	if err != nil {
		return fmt.Errorf("query delegated execution reconciliation: %w", err)
	}
	if !hasNotAcceptedConclusion(reconciliations) {
		return common.NewConflictError("delegated execution", "requeue requires a stored not_accepted_not_started reconciliation")
	}

	_, err = tx.OutboxEvent.UpdateOneID(event.ID).
		Where(outboxevent.TenantIDEQ(tenantID), outboxevent.StatusEQ(outboxStatusBlocked)).
		SetStatus(outboxStatusPending).
		SetNextAttemptAt(s.now().UTC()).
		ClearClaimToken().
		ClearClaimExpiresAt().
		SetLastError("requeued after operator conclusion: " + conclusionNotAccepted).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("requeue delegated execution: %w", err)
	}
	if err := tx.AuditLog.Create().
		SetTenantID(tenantID).
		SetUserID(request.ActorID).
		SetRequestID(event.EventID).
		SetResource("delegated_execution").
		SetAction("delegated_execution.requeue").
		SetPath("delegated-executions/" + event.EventID + "/requeue").
		SetMethod("POST").
		SetStatusCode(202).
		Exec(ctx); err != nil {
		return fmt.Errorf("audit delegated execution requeue: %w", err)
	}
	return tx.Commit()
}

func hasNotAcceptedConclusion(reconciliations []*ent.AuditLog) bool {
	for _, audit := range reconciliations {
		if audit.RequestBody == nil {
			continue
		}
		var record reconciliationAuditRecord
		if json.Unmarshal([]byte(*audit.RequestBody), &record) == nil && record.Conclusion == conclusionNotAccepted {
			return true
		}
	}
	return false
}
