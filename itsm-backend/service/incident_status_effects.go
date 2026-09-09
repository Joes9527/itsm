package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/user"
	"strconv"
	"time"
)

// This registration exposes a second known input to the existing rule engine;
// selection, action receipts and delivery continue to use its one execution path.
type IncidentStatusDeliveryHandler struct{ engine *IncidentRuleEngine }

func NewIncidentStatusDeliveryHandler(e *IncidentRuleEngine) *IncidentStatusDeliveryHandler {
	return &IncidentStatusDeliveryHandler{engine: e}
}
func (*IncidentStatusDeliveryHandler) EventType() string { return "incident.status_changed" }
func (*IncidentStatusDeliveryHandler) ReplaySafe() bool  { return true }
func (h *IncidentStatusDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	return h.engine.ExecuteCreatedEvent(ctx, event)
}

type incidentRuleSnapshot struct {
	Status     string    `json:"status"`
	Priority   string    `json:"priority"`
	Severity   string    `json:"severity"`
	Category   string    `json:"category"`
	CreatedAt  time.Time `json:"createdAt"`
	DetectedAt time.Time `json:"detectedAt"`
	ResolvedAt time.Time `json:"resolvedAt"`
	At         time.Time `json:"at"`
}
type incidentStatusPayload struct {
	Reason              string               `json:"reason"`
	Resolution          string               `json:"resolution"`
	TenantID            int                  `json:"tenantId"`
	IncidentID          int                  `json:"incidentId"`
	WorkItemID          int                  `json:"workItemId"`
	ActorID             int                  `json:"actorId"`
	Source              string               `json:"source"`
	OperationID         string               `json:"operationId"`
	CorrelationID       string               `json:"correlationId"`
	OldStatus           string               `json:"oldStatus"`
	Status              string               `json:"status"`
	Version             int                  `json:"version"`
	CycleNumber         int                  `json:"cycleNumber"`
	PreviousCycleNumber int                  `json:"previousCycleNumber"`
	Snapshot            incidentRuleSnapshot `json:"snapshot"`
}

func validateIncidentRuleEvent(ctx context.Context, client, directory *ent.Client, event *ent.OutboxEvent) (incidentCreatedPayload, error) {
	if event != nil && event.EventType == "incident.created" {
		return validateIncidentCreatedEvent(ctx, client, directory, event)
	}
	var result incidentCreatedPayload
	if event == nil || event.EventType != "incident.status_changed" {
		return result, blockOutboxDelivery("unknown incident rule event")
	}
	var p incidentStatusPayload
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return result, blockOutboxDelivery("invalid incident status payload")
	}
	if p.TenantID <= 0 || p.ActorID <= 0 || p.IncidentID <= 0 || p.WorkItemID <= 0 || p.Version <= 1 || p.Source == "" || p.OperationID == "" || p.Snapshot.At.IsZero() || p.Status != p.Snapshot.Status || event.TenantID != p.TenantID || event.AggregateType != "work_item" || event.AggregateID != strconv.Itoa(p.WorkItemID) || event.EventID != fmt.Sprintf("incident-status:%d:%d", p.WorkItemID, p.Version) {
		return result, blockOutboxDelivery("incident status identity mismatch")
	}
	stored, err := client.OutboxEvent.Query().Where(outboxevent.ID(event.ID), outboxevent.TenantID(p.TenantID), outboxevent.EventID(event.EventID), outboxevent.EventType(event.EventType)).Only(ctx)
	if err != nil {
		return result, err
	}
	var durable incidentStatusPayload
	if json.Unmarshal(stored.Payload, &durable) != nil || durable != p {
		return result, blockOutboxDelivery("status payload differs from durable event")
	}
	receipt, err := client.AuditLog.Query().Where(auditlog.TenantID(p.TenantID), auditlog.UserID(p.ActorID), auditlog.OperationID(p.OperationID), auditlog.ResultVersion(p.Version), auditlog.ResultStatus(p.Status), auditlog.Path(strconv.Itoa(p.WorkItemID)), auditlog.Resource("work_item")).Only(ctx)
	if err != nil {
		return result, err
	}
	var facts incidentStatusPayload
	if receipt.RequestBody == nil || json.Unmarshal([]byte(*receipt.RequestBody), &facts) != nil || facts != p {
		return result, blockOutboxDelivery("status event lacks immutable command provenance")
	}
	exists, err := client.User.Query().Where(user.ID(p.ActorID), user.TenantID(p.TenantID), user.Active(true)).Exist(ctx)
	if err != nil {
		return result, err
	}
	if !exists {
		return result, blockOutboxDelivery("Incident status actor unavailable in tenant")
	}
	result = incidentCreatedPayload{TenantID: p.TenantID, IncidentID: p.IncidentID, WorkItemID: p.WorkItemID, ActorID: p.ActorID, ActorTenantID: p.TenantID, Channel: p.Source, StatusSnapshot: &p.Snapshot, OperationID: p.OperationID}
	return result, nil
}

func (s incidentRuleSnapshot) incident(id, workItemID int) *ent.Incident {
	item := &ent.Ticket{ID: workItemID, Status: s.Status, Priority: s.Priority, CreatedAt: s.CreatedAt, ResolvedAt: s.ResolvedAt}
	if s.Category != "" {
		item.Edges.Category = &ent.TicketCategory{Name: s.Category}
	}
	inc := &ent.Incident{ID: id, WorkItemID: workItemID, Severity: s.Severity, DetectedAt: s.DetectedAt}
	inc.Edges.WorkItem = item
	return inc
}

type incidentRuleClockKey struct{}

type incidentRuleEventTypeKey struct{}
type EventTypeCondition struct{ Types []string }

func (c *EventTypeCondition) Evaluate(ctx context.Context, _ *ent.Incident) (bool, error) {
	eventType, _ := ctx.Value(incidentRuleEventTypeKey{}).(string)
	for _, v := range c.Types {
		if v == eventType {
			return true, nil
		}
	}
	return false, nil
}
