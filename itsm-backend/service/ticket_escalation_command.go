package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
)

// EscalateTicket owns the authenticated manual command. Professional lifecycle
// commands and scheduler escalation remain owned by their respective domains.
func (s *TicketService) EscalateTicket(ctx context.Context, cmd dto.TicketEscalationCommand) (workitemmutation.Result, error) {
	var empty workitemmutation.Result
	if s == nil || s.client == nil || s.execution == nil {
		return empty, common.NewForbiddenError("ticket execution policy required")
	}
	m := cmd.Meta
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	if m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.Source) == "" || strings.TrimSpace(m.OperationID) == "" || len(m.OperationID) > 200 || cmd.WorkItemID <= 0 || cmd.Reason == "" || len(cmd.Reason) > 4000 {
		return empty, common.NewValidationError("actor, tenant, version, operationId and reason required", nil)
	}
	if id, ok := tenantctx.TenantID(ctx); ok && id != m.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(cmd.WorkItemID), ticket.TenantIDEQ(m.TenantID), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return empty, err
	}
	if err = rejectProfessionalTicketMutation(item.RecordClass); err != nil {
		return empty, err
	}
	if item.RecordClass != "generic" {
		return empty, common.NewValidationError("professional writes require the owning domain command", nil)
	}
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, m.ActorID, m.TenantID)
	if err != nil {
		return empty, err
	}
	if _, _, err = authorization.AuthorizeWorkItem(ctx, tx.Client(), item.ID, m.TenantID, authorization.EffectiveSessionRole(actor), "update"); err != nil {
		return empty, err
	}
	if err = authorization.RequireCurrentPermission(ctx, tx, creation.Identity{TenantID: m.TenantID, ActorID: actor.ID, Role: authorization.EffectiveSessionRole(actor)}, "ticket", "escalate"); err != nil {
		return empty, err
	}
	digest, err := workitemmutation.Digest(struct {
		Action, Reason      string
		WorkItemID, Version int
	}{"manual_escalation", cmd.Reason, item.ID, m.ExpectedVersion})
	if err != nil {
		return empty, err
	}
	if result, replayed, err := workitemmutation.Replay(ctx, tx.Client(), m, item.ID, digest); err != nil || replayed {
		return result, err
	}
	if err = s.execution.BindEnt(ctx, tx, m.TenantID); err != nil {
		return empty, err
	}
	if err = s.execution.RequireEntMembers(ctx, tx, m.TenantID, item.ID); err != nil {
		return empty, err
	}
	if item.Version != m.ExpectedVersion {
		return empty, common.NewVersionConflictError("ticket", item.ID, m.ExpectedVersion, item.Version)
	}
	switch item.Status {
	case "new", "open", "in_progress", "pending":
	default:
		return empty, common.NewValidationError("ticket status does not permit manual escalation", nil)
	}
	priority, err := escalatedTicketPriority(item.Priority)
	if err != nil {
		return empty, err
	}
	if s.notificationSvc == nil {
		return empty, fmt.Errorf("manual escalation notification service required")
	}
	// Until ordered, durable updates have a registered consumer, configured Feishu
	// synchronization is an explicit precondition failure, never a discarded effect.
	if s.connectorManager != nil {
		if _, configured := s.connectorManager.Get(m.TenantID, "feishu"); configured {
			return empty, fmt.Errorf("manual escalation requires durable Feishu update delivery")
		}
	}
	member, err := s.execution.TenantPredicate(ctx, tx, m.TenantID, ticket.FieldTenantID, ticket.FieldID)
	if err != nil {
		return empty, err
	}
	count, err := tx.Ticket.Update().Where(ticket.IDEQ(item.ID), ticket.TenantIDEQ(m.TenantID), ticket.VersionEQ(m.ExpectedVersion), ticket.DeletedAtIsNil(), member).SetPriority(priority).SetStatus("in_progress").AddVersion(1).Save(ctx)
	if err != nil {
		return empty, err
	}
	if count != 1 {
		return empty, common.NewVersionConflictError("ticket", item.ID, m.ExpectedVersion, item.Version)
	}
	recipients := []int{item.RequesterID}
	if item.AssigneeID > 0 {
		recipients = append(recipients, item.AssigneeID)
	}
	if err = s.notificationSvc.EnqueueNotificationTx(ctx, tx, item.ID, m.TenantID, &dto.SendTicketNotificationRequest{UserIDs: recipients, EventType: "ticket_updated", Content: fmt.Sprintf("【工单升级】#%s (%s)：%s → %s。原因：%s", item.TicketNumber, item.Title, item.Priority, priority, cmd.Reason), DeliveryKey: fmt.Sprintf("escalation:manual:%d:%s", m.ActorID, m.OperationID)}); err != nil {
		return empty, err
	}
	result := workitemmutation.Result{WorkItemID: item.ID, Version: item.Version + 1, Status: "in_progress"}
	if err = workitemmutation.RecordTx(ctx, tx, m, result, "work_item.escalation.manual", digest, map[string]interface{}{"reason": cmd.Reason, "previousPriority": item.Priority, "priority": priority, "previousStatus": item.Status, "assigneeId": item.AssigneeID}); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func escalatedTicketPriority(current string) (string, error) {
	switch current {
	case "low":
		return "medium", nil
	case "medium":
		return "high", nil
	case "high", "critical":
		return "critical", nil
	default:
		return "", common.NewValidationError("unknown ticket priority", nil)
	}
}
