package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"itsm-backend/ent"
	"itsm-backend/pkg/eventbus"
	"itsm-backend/service/common/event"
)

const slaBreachEventType = "sla.breached"

type slaBreachDeliveryPayload struct {
	Version      int       `json:"version"`
	EventID      string    `json:"event_id"`
	TenantID     int       `json:"tenant_id"`
	TicketID     int       `json:"ticket_id"`
	ViolationID  int       `json:"violation_id"`
	SLAPolicyID  int       `json:"sla_policy_id"`
	BreachedType string    `json:"breached_type"`
	BreachedAt   time.Time `json:"breached_at"`
}

// Preserve the public SLA event contract and its persisted occurrence time.
type persistedSLABreachEvent struct {
	*event.SLABreachedEvent
	EventID string `json:"event_id"`
}

func (e *persistedSLABreachEvent) OccurredAt() time.Time { return e.BreachedAt }

type SLABreachDeliveryHandler struct{}

func NewSLABreachDeliveryHandler() *SLABreachDeliveryHandler { return &SLABreachDeliveryHandler{} }
func (*SLABreachDeliveryHandler) EventType() string          { return slaBreachEventType }
func (*SLABreachDeliveryHandler) Deliver(ctx context.Context, row *ent.OutboxEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if row == nil {
		return blockOutboxDelivery("SLA breach event is required")
	}
	var p slaBreachDeliveryPayload
	if err := json.Unmarshal(row.Payload, &p); err != nil {
		return blockOutboxDelivery("invalid SLA breach payload")
	}
	if p.Version != 1 || p.EventID == "" || p.EventID != row.EventID || row.EventType != slaBreachEventType || p.TenantID <= 0 || p.TenantID != row.TenantID || p.TicketID <= 0 || (row.ExecutionWorkItemID == nil || p.TicketID != *row.ExecutionWorkItemID) || p.ViolationID <= 0 || row.AggregateType != "sla_violation" || row.AggregateID != strconv.Itoa(p.ViolationID) || p.SLAPolicyID <= 0 || p.BreachedAt.IsZero() || (p.BreachedType != "response" && p.BreachedType != "resolve") {
		return blockOutboxDelivery("SLA breach event identity or contract mismatch")
	}
	bus := eventbus.GetGlobalEventBus()
	if bus == nil {
		return blockOutboxDelivery("SLA event bus is not configured")
	}
	ev := &persistedSLABreachEvent{SLABreachedEvent: event.NewSLABreachedEvent(strconv.Itoa(p.TenantID), strconv.Itoa(p.TicketID), strconv.Itoa(p.SLAPolicyID), p.BreachedType, p.BreachedAt), EventID: p.EventID}
	if err := bus.Publish(ev); err != nil {
		// The publisher cannot prove non-acceptance. Never replay a possibly delivered
		// enterprise event automatically, including an ordinary Publish error.
		return blockOutboxDelivery(fmt.Sprintf("delivery_unknown: SLA event %s publish outcome requires reconciliation", p.EventID))
	}
	return nil
}
