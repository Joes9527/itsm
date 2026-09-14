package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
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
	if s == nil || s.client == nil || s.execution == nil {
		return empty, common.NewForbiddenError("incident execution policy required")
	}
	cmd.Action = strings.TrimSpace(cmd.Action)
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	cmd.Resolution = strings.TrimSpace(cmd.Resolution)
	if cmd.Action == "escalate" && cmd.EscalationLevel <= 0 {
		cmd.EscalationLevel = 1
	}
	m := cmd.Meta
	if m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.OperationID) == "" || strings.TrimSpace(m.Source) == "" {
		return empty, common.NewValidationError("trusted actor, tenant, version, source and operationId required", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	current, err := tx.Incident.Query().Where(incident.ID(cmd.IncidentID), incidentTenantScope(m.TenantID, ticket.RecordClass("incident"))).WithWorkItem().Only(ctx)
	if err != nil {
		return empty, common.NewNotFoundError("incident")
	}
	if err = s.authorizeIncidentCommandActor(ctx, tx, cmd, current.WorkItemID); err != nil {
		return empty, err
	}
	digest, err := incidentCommandDigest(cmd)
	if err != nil {
		return empty, err
	}
	if result, ok, err := workitemmutation.Replay(ctx, tx.Client(), m, current.WorkItemID, digest); ok || err != nil {
		return result, err
	}
	result, err := s.applyIncidentCommandTx(ctx, tx, cmd, digest)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = tx.Rollback()
		// A concurrent same-key winner can commit while this transaction waits on CAS.
		replayTx, openErr := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if openErr != nil {
			return result, err
		}
		defer replayTx.Rollback()
		if authErr := s.authorizeIncidentCommandActor(ctx, replayTx, cmd, current.WorkItemID); authErr != nil {
			return empty, authErr
		}
		if replay, ok, replayErr := workitemmutation.Replay(ctx, replayTx.Client(), m, current.WorkItemID, digest); ok || replayErr != nil {
			return replay, replayErr
		}
	}
	return result, err
}

func incidentCommandDigest(cmd dto.IncidentCommand) (string, error) {
	return workitemmutation.Digest(struct {
		IncidentID, Version, AssigneeID, EscalationLevel int
		Action, Reason, Resolution                       string
	}{cmd.IncidentID, cmd.Meta.ExpectedVersion, cmd.AssigneeID, cmd.EscalationLevel, cmd.Action, cmd.Reason, cmd.Resolution})
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
	if err = s.authorizeIncidentCommandActor(ctx, tx, cmd, item.ID); err != nil {
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
	case "assign":
		if !canAssignIncidentStatus(item.Status) {
			return empty, common.NewValidationError("incidents in the current status cannot be reassigned", nil)
		}
		if item.AssigneeID == cmd.AssigneeID {
			return empty, common.NewValidationError("assignment requires a different assignee", nil)
		}
		if item.AssigneeID > 0 && strings.TrimSpace(cmd.Reason) == "" {
			return empty, common.NewValidationError("assignment reason required", nil)
		}
		target = item.Status
		if item.Status == common.IncidentStatusNew && item.AssigneeID == 0 {
			target = common.IncidentStatusAssigned
		}
		if err := NewIncidentService(tx.Client(), s.logger, s.execution).validateIncidentAssignee(ctx, cmd.AssigneeID, m.TenantID); err != nil {
			return empty, err
		}
	case "escalate":
		target = common.IncidentStatusEscalated
		if cmd.EscalationLevel <= current.EscalationLevel {
			return empty, common.NewValidationError("escalation level must increase", nil)
		}
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
	if cmd.Action == "assign" {
		valid = canAssignIncidentStatus(item.Status)
	}
	statusChanged := item.Status != target
	if !statusChanged && cmd.Action != "escalate" && cmd.Action != "assign" {
		return empty, common.NewValidationError("Incident command requires a state change", nil)
	}
	if cmd.Action == "start" && (item.Status == common.IncidentStatusResolved || common.IsIncidentFinalStatus(item.Status)) {
		valid = false
	}
	if cmd.Action == "reopen" {
		valid = item.Status == common.IncidentStatusResolved || item.Status == common.IncidentStatusClosed
	}
	if !valid {
		return empty, common.NewValidationError(fmt.Sprintf("invalid incident action %s from %s", cmd.Action, item.Status), nil)
	}
	if err := s.execution.BindEnt(ctx, tx, m.TenantID); err != nil {
		return empty, incidentExecutionFailure(err)
	}
	if err := s.execution.RequireEntMembers(ctx, tx, m.TenantID, item.ID); err != nil {
		return empty, incidentExecutionFailure(err)
	}
	now := time.Now().UTC()
	update := tx.Ticket.UpdateOneID(item.ID).Where(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil(), ticket.Version(m.ExpectedVersion)).SetVersion(m.ExpectedVersion + 1).SetUpdatedAt(now)
	if statusChanged {
		update.SetStatus(target)
	}
	if cmd.Action == "assign" {
		update.SetAssigneeID(cmd.AssigneeID)
	}
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
		sla := NewTicketSLAService(s.client, s.logger)
		sla.SetDirectorySnapshot(s.directory)
		if err = sla.ResetCycleTx(ctx, tx, item, now, m); err != nil {
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
	if cmd.Action == "escalate" {
		current, err = tx.Incident.UpdateOneID(current.ID).SetEscalationLevel(cmd.EscalationLevel).SetEscalatedAt(now).Save(ctx)
		if err != nil {
			return empty, err
		}
	}
	result := workitemmutation.Result{WorkItemID: item.ID, Version: updated.Version, Status: updated.Status}
	facts := map[string]any{"tenantId": m.TenantID, "incidentId": current.ID, "workItemId": item.ID, "actorId": m.ActorID, "source": m.Source, "operationId": m.OperationID, "correlationId": m.CorrelationID, "oldStatus": item.Status, "status": updated.Status, "version": updated.Version, "cycleNumber": updated.SLACycleNumber, "previousCycleNumber": item.SLACycleNumber}
	facts["reason"] = cmd.Reason
	facts["resolution"] = cmd.Resolution
	facts["assigneeId"] = cmd.AssigneeID
	if cmd.Action == "assign" {
		facts["previousAssigneeId"] = item.AssigneeID
	}
	facts["escalationLevel"] = cmd.EscalationLevel
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
	eventType := "status_changed"
	if cmd.Action == "assign" {
		eventType = "assignment"
	} else if !statusChanged {
		eventType = "escalation"
	}
	_, err = tx.IncidentEvent.Create().SetIncidentID(current.ID).SetTenantID(m.TenantID).SetUserID(m.ActorID).SetSource(m.Source).SetEventType(eventType).SetEventName(cmd.Action).SetDescription(cmd.Reason).SetStatus("active").SetSeverity("info").SetOccurredAt(now).SetMetadata(facts).Save(ctx)
	if err != nil {
		return empty, err
	}
	if !statusChanged {
		return result, nil
	}
	payload, err := json.Marshal(facts)
	if err != nil {
		return empty, err
	}
	_, err = tx.OutboxEvent.Create().SetExecutionWorkItemID(item.ID).SetEventID(fmt.Sprintf("incident-status:%d:%d", item.ID, updated.Version)).SetEventType("incident.status_changed").SetTenantID(m.TenantID).SetAggregateType("work_item").SetAggregateID(strconv.Itoa(item.ID)).SetPayload(payload).Save(ctx)
	if err != nil {
		return empty, err
	}
	return result, nil
}

func (s *IncidentService) authorizeIncidentCommandActor(ctx context.Context, tx *ent.Tx, cmd dto.IncidentCommand, itemID int) error {
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, cmd.Meta.ActorID, cmd.Meta.TenantID)
	if err != nil {
		return err
	}
	if _, _, err = authorization.AuthorizeWorkItem(ctx, tx.Client(), itemID, cmd.Meta.TenantID, authorization.EffectiveSessionRole(actor), "update"); err != nil {
		return err
	}
	if err = authorization.RequireCurrentPermission(ctx, tx, creation.Identity{TenantID: cmd.Meta.TenantID, ActorID: actor.ID, Role: authorization.EffectiveSessionRole(actor)}, "incident", "write"); err != nil {
		return common.NewForbiddenError("insufficient current Incident permission")
	}
	return nil
}

func incidentExecutionFailure(err error) error {
	if errors.Is(err, executionscope.ErrDenied) {
		return common.NewForbiddenError("incident execution scope denied")
	}
	return fmt.Errorf("verify incident execution scope: %w", err)
}
