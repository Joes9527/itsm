package service

import (
	"context"
	"encoding/json"
	"fmt"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/sladefinition"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/shared/slacontract"
	"time"

	"itsm-backend/handlers/shared/workitemmutation"
)

const slaCycleCompletedAction = "sla.cycle.completed"

type SLACycleClock struct {
	Number        int
	StartedAt     time.Time
	ResponseAt    *time.Time
	ResolvedAt    *time.Time
	PausedMinutes int
}

func ResetSLACycle(previous SLACycleClock, at time.Time) SLACycleClock {
	return SLACycleClock{Number: previous.Number + 1, StartedAt: at}
}
func slaTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
func slaCycleStart(item *ent.Ticket) time.Time {
	if !item.SLACycleStartedAt.IsZero() {
		return item.SLACycleStartedAt
	}
	return item.CreatedAt
}
func slaMeasuredAt(completed, now time.Time) time.Time {
	if !completed.IsZero() {
		return completed
	}
	return now
}

// Deadlines already include any applied pause extension. Paused minutes adjust elapsed
// time only; subtracting them again from the breach clock would extend the contract twice.
func projectSLACycle(item *ent.Ticket, now time.Time) TicketSLAInfoResult {
	// Closure stops unfinished clocks without inventing a response or resolution.
	// Already observed completion and breach facts remain authoritative.
	if item.ClosedAt != nil && item.ClosedAt.Before(now) {
		now = *item.ClosedAt
	}
	responseAt := slaMeasuredAt(item.FirstResponseAt, now)
	resolvedAt := slaMeasuredAt(item.ResolvedAt, now)
	used := func(at time.Time) int {
		n := int(at.Sub(slaCycleStart(item)).Minutes()) - item.SLAPausedMinutes
		if n < 0 {
			return 0
		}
		return n
	}
	result := TicketSLAInfoResult{TicketID: item.ID, TicketNumber: item.TicketNumber, Priority: item.Priority, ResponseDeadline: slaTime(item.SLAResponseDeadline), ResolutionDeadline: slaTime(item.SLAResolutionDeadline), ResponseTimeUsed: used(responseAt), ResolutionTimeUsed: used(resolvedAt), CycleNumber: item.SLACycleNumber, CycleStartedAt: slaTime(item.SLACycleStartedAt), PausedMinutes: item.SLAPausedMinutes, AppliedPolicy: item.AppliedSLAPolicy, SLAStatus: "ok"}
	result.ResponseBreached = !item.SLAResponseDeadline.IsZero() && responseAt.After(item.SLAResponseDeadline)
	result.ResolutionBreached = !item.SLAResolutionDeadline.IsZero() && resolvedAt.After(item.SLAResolutionDeadline)
	if result.ResponseBreached || result.ResolutionBreached {
		result.SLAStatus = "breached"
	} else if (item.FirstResponseAt.IsZero() && !item.SLAResponseDeadline.IsZero() && item.SLAResponseDeadline.Sub(now) < 30*time.Minute) || (item.ResolvedAt.IsZero() && !item.SLAResolutionDeadline.IsZero() && item.SLAResolutionDeadline.Sub(now) < 30*time.Minute) {
		result.SLAStatus = "warning"
	}
	if result.ResponseDeadline == nil && result.ResolutionDeadline == nil {
		result.SLAStatus = "unknown"
	}
	return result
}

// ResetCycleTx never commits, changes professional state, or consumes the command
// operation key. The domain first performs CAS in tx and persists its sole receipt
// in tx. Any error requires that caller to roll back the entire transaction.
func (s *TicketSLAService) ResetCycleTx(ctx context.Context, tx *ent.Tx, item *ent.Ticket, at time.Time, meta workitemmutation.Meta) error {
	current, err := s.cycleCommandItem(ctx, tx, item, at, meta)
	if err != nil {
		return err
	}
	if current.SLADefinitionID == 0 && current.AppliedSLAPolicy == nil && current.SLAResponseDeadline.IsZero() && current.SLAResolutionDeadline.IsZero() {
		return nil
	}
	policy := current.AppliedSLAPolicy
	if policy == nil || policy.SchemaVersion != 1 || policy.DefinitionID != current.SLADefinitionID || policy.ResponseMinutes <= 0 || policy.ResolutionMinutes <= 0 {
		return fmt.Errorf("applied SLA policy missing or invalid; explicit authorized policy application required")
	}
	return s.advanceCycleTx(ctx, tx, current, at, meta, policy)
}

// ApplyPolicyTx is the explicit replacement path for an authorized domain command.
// It can establish a missing legacy contract, recording the old facts without
// guessing historical policy values. It shares ResetCycleTx's CAS/receipt rules.
func (s *TicketSLAService) ApplyPolicyTx(ctx context.Context, tx *ent.Tx, item *ent.Ticket, definitionID int, at time.Time, meta workitemmutation.Meta) error {
	current, err := s.cycleCommandItem(ctx, tx, item, at, meta)
	if err != nil {
		return err
	}
	definition, err := tx.SLADefinition.Query().Where(sladefinition.ID(definitionID), sladefinition.TenantID(meta.TenantID), sladefinition.IsActive(true)).Only(ctx)
	if err != nil {
		return fmt.Errorf("explicit SLA policy unavailable: %w", err)
	}
	return s.advanceCycleTx(ctx, tx, current, at, meta, appliedSLAPolicy(definition))
}
func (s *TicketSLAService) cycleCommandItem(ctx context.Context, tx *ent.Tx, item *ent.Ticket, at time.Time, meta workitemmutation.Meta) (*ent.Ticket, error) {
	if tx == nil || item == nil || meta.TenantID <= 0 || meta.ActorID <= 0 || meta.ExpectedVersion <= 0 || meta.Source == "" || meta.OperationID == "" || at.IsZero() {
		return nil, fmt.Errorf("SLA cycle requires transaction and trusted command metadata")
	}
	if item.TenantID != meta.TenantID {
		return nil, fmt.Errorf("SLA cycle tenant mismatch")
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != meta.TenantID {
		return nil, fmt.Errorf("SLA cycle tenant context mismatch")
	}
	if _, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, meta.ActorID, meta.TenantID); err != nil {
		return nil, err
	}
	current, err := tx.Ticket.Query().Where(ticket.ID(item.ID), ticket.TenantID(meta.TenantID), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return nil, err
	}
	// Require the caller's successful version increment, not an unguarded reset.
	if current.Version != meta.ExpectedVersion+1 || current.SLACycleNumber != item.SLACycleNumber {
		return nil, fmt.Errorf("SLA cycle requires caller CAS in the same transaction")
	}
	return current, nil
}
func (s *TicketSLAService) advanceCycleTx(ctx context.Context, tx *ent.Tx, current *ent.Ticket, at time.Time, meta workitemmutation.Meta, policy *slacontract.Policy) error {
	if at.Before(slaCycleStart(current)) {
		return fmt.Errorf("SLA cycle start precedes previous cycle")
	}
	response, err := s.calculateDeadlineWithBusinessHours(at, policy.ResponseMinutes, policy.BusinessHours)
	if err != nil {
		return err
	}
	resolution, err := s.calculateDeadlineWithBusinessHours(at, policy.ResolutionMinutes, policy.BusinessHours)
	if err != nil {
		return err
	}
	projection := projectSLACycle(current, at)
	facts := dto.SLACycleResult{Number: current.SLACycleNumber, StartedAt: slaTime(current.SLACycleStartedAt), EndedAt: at, ResponseAt: slaTime(current.FirstResponseAt), ResolvedAt: slaTime(current.ResolvedAt), ResponseDeadline: projection.ResponseDeadline, ResolutionDeadline: projection.ResolutionDeadline, PausedMinutes: current.SLAPausedMinutes, ResponseBreached: projection.ResponseBreached, ResolutionBreached: projection.ResolutionBreached, Policy: current.AppliedSLAPolicy, ActorID: meta.ActorID, Source: meta.Source, CorrelationID: meta.CorrelationID}
	body, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	_, err = tx.AuditLog.Create().SetTenantID(meta.TenantID).SetUserID(meta.ActorID).SetResource(fmt.Sprintf("work_item:%d", current.ID)).SetAction(slaCycleCompletedAction).SetPath(fmt.Sprintf("/work-items/%d/sla", current.ID)).SetMethod("COMMAND").SetRequestID(meta.CorrelationID).SetRequestBody(string(body)).Save(ctx)
	if err != nil {
		return err
	}
	next := ResetSLACycle(SLACycleClock{Number: current.SLACycleNumber}, at)
	_, err = tx.Ticket.UpdateOneID(current.ID).Where(ticket.TenantID(meta.TenantID), ticket.Version(meta.ExpectedVersion+1), ticket.SLACycleNumber(current.SLACycleNumber)).SetSLADefinitionID(policy.DefinitionID).SetAppliedSLAPolicy(policy).SetSLACycleNumber(next.Number).SetSLACycleStartedAt(next.StartedAt).SetSLAPausedMinutes(0).ClearFirstResponseAt().ClearResolvedAt().SetSLAResponseDeadline(response).SetSLAResolutionDeadline(resolution).Save(ctx)
	if err != nil {
		return err
	}
	return nil
}
func (s *TicketSLAService) cycleHistory(ctx context.Context, item *ent.Ticket) ([]dto.SLACycleResult, error) {
	rows, err := s.client.AuditLog.Query().Where(auditlog.TenantID(item.TenantID), auditlog.Resource(fmt.Sprintf("work_item:%d", item.ID)), auditlog.Action(slaCycleCompletedAction)).Order(ent.Asc(auditlog.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]dto.SLACycleResult, 0, len(rows))
	for _, row := range rows {
		if row.RequestBody == nil {
			return nil, fmt.Errorf("SLA audit fact missing")
		}
		var fact dto.SLACycleResult
		if err := json.Unmarshal([]byte(*row.RequestBody), &fact); err != nil {
			return nil, err
		}
		result = append(result, fact)
	}
	return result, nil
}
