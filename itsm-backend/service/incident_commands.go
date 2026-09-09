package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	"itsm-backend/handlers/shared/workitemmutation"
	"strconv"
	"strings"
	"time"
)

// ValidateIncidentRecovery requires observed service recovery; Problem completion
// is deliberately independent of an Incident's restoration lifecycle.
func ValidateIncidentRecovery(resolution string) error {
	if strings.TrimSpace(resolution) == "" {
		return errors.New("recovery evidence required")
	}
	return nil
}

func (s *IncidentService) ApplyIncidentCommand(ctx context.Context, cmd dto.IncidentCommand) (workitemmutation.Result, error) {
	var empty workitemmutation.Result
	cmd.Action = strings.TrimSpace(cmd.Action)
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	cmd.Resolution = strings.TrimSpace(cmd.Resolution)
	m := cmd.Meta
	if m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.OperationID) == "" || strings.TrimSpace(m.Source) == "" {
		return empty, common.NewValidationError("trusted actor, tenant, version, source and operationId required", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	actor, err := s.client.User.Query().Where(user.ID(m.ActorID), user.TenantID(m.TenantID), user.Active(true)).Only(ctx)
	if err != nil {
		return empty, common.NewForbiddenError("command actor unavailable")
	}
	current, err := s.getIncidentEntity(ctx, cmd.IncidentID, m.TenantID)
	if err != nil {
		return empty, common.NewNotFoundError("incident")
	}
	if _, _, err = authorization.AuthorizeWorkItem(ctx, s.client, current.WorkItemID, m.TenantID, authorization.EffectiveSessionRole(actor), "update"); err != nil {
		return empty, err
	}
	digest, err := incidentCommandDigest(cmd)
	if err != nil {
		return empty, err
	}
	if result, ok, err := workitemmutation.Replay(ctx, s.client, m, current.WorkItemID, digest); ok || err != nil {
		return result, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	result, err := s.applyIncidentCommandTx(ctx, tx, cmd, digest)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = tx.Rollback()
		// A concurrent same-key winner can commit while this transaction waits on CAS.
		if replay, ok, replayErr := workitemmutation.Replay(ctx, s.client, m, current.WorkItemID, digest); ok || replayErr != nil {
			return replay, replayErr
		}
	}
	return result, err
}

func incidentCommandDigest(cmd dto.IncidentCommand) (string, error) {
	return workitemmutation.Digest(struct {
		IncidentID, Version        int
		Action, Reason, Resolution string
	}{cmd.IncidentID, cmd.Meta.ExpectedVersion, cmd.Action, cmd.Reason, cmd.Resolution})
}

// The owning HTTP/BPMN entry and configured rule action share this transaction
// core; the caller commits its complete unit of work, including rule receipts.
func (s *IncidentService) applyIncidentCommandTx(ctx context.Context, tx *ent.Tx, cmd dto.IncidentCommand, digest string) (workitemmutation.Result, error) {
	var empty workitemmutation.Result
	m := cmd.Meta
	if tx == nil || m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || m.OperationID == "" || m.Source == "" {
		return empty, common.NewValidationError("trusted command identity required", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	current, err := tx.Incident.Query().Where(incident.ID(cmd.IncidentID), incidentTenantScope(m.TenantID, ticket.RecordClass("incident"))).WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		return empty, common.NewNotFoundError("incident")
	}
	item := current.Edges.WorkItem
	actor, err := tx.User.Query().Where(user.ID(m.ActorID), user.TenantID(m.TenantID), user.Active(true)).Only(ctx)
	if err != nil {
		return empty, common.NewForbiddenError("command actor unavailable")
	}
	if _, _, err = authorization.AuthorizeWorkItem(ctx, tx.Client(), item.ID, m.TenantID, authorization.EffectiveSessionRole(actor), "update"); err != nil {
		return empty, err
	}
	if replay, ok, err := workitemmutation.Replay(ctx, tx.Client(), m, item.ID, digest); ok || err != nil {
		return replay, err
	}
	if item.Version != m.ExpectedVersion {
		return empty, common.NewVersionConflictError("incident", current.ID, m.ExpectedVersion, item.Version)
	}
	target := ""
	switch cmd.Action {
	case "acknowledge":
		target = common.IncidentStatusAcknowledged
	case "start":
		target = common.IncidentStatusInProgress
	case "resolve":
		target = common.IncidentStatusResolved
		if err = ValidateIncidentRecovery(cmd.Resolution); err != nil {
			return empty, common.NewValidationError(err.Error(), err)
		}
	case "close":
		target = common.IncidentStatusClosed
		if cmd.Reason == "" {
			return empty, common.NewValidationError("close reason required", nil)
		}
	case "reopen":
		target = common.IncidentStatusInProgress
	default:
		return empty, common.NewValidationError("unsupported incident action", nil)
	}
	valid := isValidIncidentStatusTransition(item.Status, target)
	if cmd.Action == "start" && (item.Status == common.IncidentStatusResolved || common.IsIncidentFinalStatus(item.Status)) {
		valid = false
	}
	if cmd.Action == "reopen" {
		valid = item.Status == common.IncidentStatusResolved || item.Status == common.IncidentStatusClosed
	}
	if !valid {
		return empty, common.NewValidationError(fmt.Sprintf("invalid incident action %s from %s", cmd.Action, item.Status), nil)
	}
	now := time.Now().UTC()
	update := tx.Ticket.UpdateOneID(item.ID).Where(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil(), ticket.Version(m.ExpectedVersion)).SetStatus(target).SetVersion(m.ExpectedVersion + 1).SetUpdatedAt(now)
	switch cmd.Action {
	case "acknowledge":
		if item.FirstResponseAt.IsZero() {
			update.SetFirstResponseAt(now)
		}
	case "resolve":
		update.SetResolvedAt(now).ClearClosedAt()
	case "close":
		update.SetClosedAt(now)
	}
	updated, err := update.Save(ctx)
	if ent.IsNotFound(err) {
		return empty, common.NewVersionConflictError("incident", current.ID, m.ExpectedVersion, item.Version)
	}
	if err != nil {
		return empty, err
	}
	if cmd.Action == "reopen" {
		if err = NewTicketSLAService(s.client, s.logger).ResetCycleTx(ctx, tx, item, now, m); err != nil {
			return empty, err
		}
		// The helper archives the completed cycle before any completion is cleared.
		updated, err = tx.Ticket.UpdateOneID(item.ID).ClearFirstResponseAt().ClearResolvedAt().ClearClosedAt().Save(ctx)
		if err != nil {
			return empty, err
		}
	}
	if cmd.Action == "resolve" {
		steps := append(current.ResolutionSteps, map[string]any{"step": len(current.ResolutionSteps) + 1, "description": cmd.Resolution, "executedBy": strconv.Itoa(m.ActorID), "executedAt": now, "status": "completed"})
		current, err = tx.Incident.UpdateOneID(current.ID).SetResolutionSteps(steps).Save(ctx)
		if err != nil {
			return empty, err
		}
	}
	result := workitemmutation.Result{WorkItemID: item.ID, Version: updated.Version, Status: updated.Status}
	facts := map[string]any{"tenantId": m.TenantID, "incidentId": current.ID, "workItemId": item.ID, "actorId": m.ActorID, "source": m.Source, "operationId": m.OperationID, "correlationId": m.CorrelationID, "oldStatus": item.Status, "status": updated.Status, "version": updated.Version, "cycleNumber": updated.SLACycleNumber, "previousCycleNumber": item.SLACycleNumber}
	facts["reason"] = cmd.Reason
	facts["resolution"] = cmd.Resolution
	category := ""
	if item.Edges.Category != nil {
		category = item.Edges.Category.Name
		if parent := item.Edges.Category.Edges.Parent; parent != nil {
			category = parent.Name
		}
	}
	facts["snapshot"] = incidentRuleSnapshot{Status: updated.Status, Priority: updated.Priority, Severity: current.Severity, Category: category, CreatedAt: updated.CreatedAt, DetectedAt: current.DetectedAt, ResolvedAt: updated.ResolvedAt, At: now}
	if err = workitemmutation.RecordTx(ctx, tx, m, result, "incident."+cmd.Action, digest, facts); err != nil {
		return empty, err
	}
	_, err = tx.IncidentEvent.Create().SetIncidentID(current.ID).SetTenantID(m.TenantID).SetUserID(m.ActorID).SetSource(m.Source).SetEventType("status_changed").SetEventName(cmd.Action).SetDescription(cmd.Reason).SetStatus("active").SetSeverity("info").SetOccurredAt(now).SetMetadata(facts).Save(ctx)
	if err != nil {
		return empty, err
	}
	payload, err := json.Marshal(facts)
	if err != nil {
		return empty, err
	}
	_, err = tx.OutboxEvent.Create().SetEventID(fmt.Sprintf("incident-status:%d:%d", item.ID, updated.Version)).SetEventType("incident.status_changed").SetTenantID(m.TenantID).SetAggregateType("work_item").SetAggregateID(strconv.Itoa(item.ID)).SetPayload(payload).Save(ctx)
	if err != nil {
		return empty, err
	}
	return result, nil
}
