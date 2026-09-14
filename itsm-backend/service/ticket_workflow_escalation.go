package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workflowcallback"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service/bpmn"
)

// ApplyTicketWorkflowEscalation owns the domain transaction. The process engine
// advances separately, and retries this operation through its immutable receipt.
func (s *TicketService) ApplyTicketWorkflowEscalation(ctx context.Context) (workitemmutation.Result, error) {
	var empty workitemmutation.Result
	claim, ok := workflowcallback.CurrentClaim(ctx)
	if !ok || s == nil || s.client == nil || s.execution == nil || s.notificationSvc == nil {
		return empty, common.NewForbiddenError("workflow escalation requires live callback and dependencies")
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != claim.TenantID {
		return empty, common.NewForbiddenError("workflow tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, claim.TenantID)
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	scope, err := s.execution.CallbackPredicate(ctx, tx, claim.TenantID)
	if err != nil {
		return empty, err
	}
	callback, err := tx.ProcessCallbackOutbox.Query().Where(scope,
		processcallbackoutbox.IDEQ(claim.ID), processcallbackoutbox.TenantIDEQ(claim.TenantID), processcallbackoutbox.ExecutionKeyEQ(claim.ExecutionKey), processcallbackoutbox.LeaseOwnerEQ(claim.LeaseOwner), processcallbackoutbox.AttemptCountEQ(claim.AttemptCount), processcallbackoutbox.LeaseExpiresAtGT(time.Now()), processcallbackoutbox.StatusEQ("processing"), processcallbackoutbox.HandlerIDEQ("ticket_service_handler"), processcallbackoutbox.TaskTypeEQ("ticket_task"), processcallbackoutbox.ActionEQ("escalate"), func(q *entsql.Selector) { q.ForUpdate() }).Only(ctx)
	if err != nil {
		return empty, err
	}
	instance, err := tx.ProcessInstance.Query().Where(processinstance.IDEQ(callback.ProcessInstanceID), processinstance.TenantIDEQ(claim.TenantID), processinstance.StatusEQ("running"), func(q *entsql.Selector) { q.ForUpdate() }).Only(ctx)
	if err != nil {
		return empty, err
	}
	if instance.BusinessType != "generic" || instance.BusinessID <= 0 || instance.BusinessKey != fmt.Sprintf("generic:%d", instance.BusinessID) || instance.ExecutionWorkItemID == nil || *instance.ExecutionWorkItemID != instance.BusinessID {
		return empty, common.NewForbiddenError("workflow WorkItem identity mismatch")
	}
	actorID := callback.ActorID
	switch callback.CallbackKind {
	case "service_task":
		actorID, err = strconv.Atoi(instance.Initiator)
		if err != nil || actorID <= 0 || instance.CurrentActivityID != callback.ElementID {
			return empty, common.NewForbiddenError("workflow initiator or activity mismatch")
		}
	case "user_task_callback":
		if actorID <= 0 || callback.ActorSource != "workflow" || callback.ProcessTaskID <= 0 {
			return empty, common.NewForbiddenError("workflow completion actor missing")
		}
		_, err = tx.ProcessTask.Query().Where(processtask.IDEQ(callback.ProcessTaskID), processtask.TenantIDEQ(claim.TenantID), processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskDefinitionKeyEQ(callback.ElementID), processtask.StatusEQ("completed")).Only(ctx)
		if err != nil {
			return empty, err
		}
	default:
		return empty, common.NewForbiddenError("unsupported workflow callback kind")
	}
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(instance.BusinessID), ticket.TenantIDEQ(claim.TenantID), ticket.DeletedAtIsNil(), func(q *entsql.Selector) { q.ForUpdate() }).Only(ctx)
	if err != nil {
		return empty, err
	}
	if item.RecordClass != "generic" {
		return empty, common.NewValidationError("professional writes require the owning domain command", nil)
	}
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, actorID, claim.TenantID)
	if err != nil {
		return empty, err
	}
	if _, _, err = authorization.AuthorizeWorkItem(ctx, tx.Client(), item.ID, claim.TenantID, authorization.EffectiveSessionRole(actor), "update"); err != nil {
		return empty, err
	}
	if err = authorization.RequireCurrentPermission(ctx, tx, creation.Identity{TenantID: claim.TenantID, ActorID: actorID, Role: authorization.EffectiveSessionRole(actor)}, "ticket", "escalate"); err != nil {
		return empty, err
	}
	priority := "high"
	if raw, exists := callback.Variables["escalate_to"]; exists {
		value, valid := raw.(string)
		if !valid {
			return empty, fmt.Errorf("invalid escalation priority")
		}
		if value != "" {
			priority = value
		}
	}
	switch priority {
	case "low", "medium", "high", "critical":
	default:
		return empty, fmt.Errorf("unknown escalation priority")
	}
	reason := ""
	if raw, exists := callback.Variables["escalation_reason"]; exists {
		value, valid := raw.(string)
		if !valid || len(value) > 4000 {
			return empty, fmt.Errorf("invalid escalation reason")
		}
		reason = strings.TrimSpace(value)
	}
	recipients := []int{}
	if raw, exists := callback.Variables["notify_admin_ids"]; exists {
		values, valid := raw.([]interface{})
		if !valid {
			return empty, fmt.Errorf("invalid escalation recipients")
		}
		seen := map[int]bool{}
		for _, rawID := range values {
			id, e := bpmn.CallbackInteger(rawID)
			if e != nil || id <= 0 {
				return empty, fmt.Errorf("invalid escalation recipient")
			}
			if !seen[id] {
				recipients = append(recipients, id)
				seen[id] = true
			}
		}
	}
	sort.Ints(recipients)
	expectedVersion, err := bpmn.CallbackInteger(callback.Variables["version"])
	if err != nil || expectedVersion <= 0 {
		return empty, fmt.Errorf("workflow expected version required")
	}
	meta := workitemmutation.Meta{TenantID: claim.TenantID, ActorID: actorID, Source: "workflow", ExpectedVersion: expectedVersion, OperationID: claim.ExecutionKey, CorrelationID: instance.ProcessInstanceID}
	digest, err := workitemmutation.Digest(struct {
		Action                                              string
		CallbackID, InstanceID, WorkItemID, ExpectedVersion int
		Priority, Reason                                    string
		Recipients                                          []int
	}{"workflow_escalation", callback.ID, instance.ID, item.ID, expectedVersion, priority, reason, recipients})
	if err != nil {
		return empty, err
	}
	if result, replayed, e := workitemmutation.Replay(ctx, tx.Client(), meta, item.ID, digest); e != nil || replayed {
		return result, e
	}
	for _, id := range recipients {
		if _, err = tx.User.Query().Where(user.IDEQ(id), user.TenantIDEQ(claim.TenantID), user.ActiveEQ(true)).Only(ctx); err != nil {
			return empty, err
		}
	}
	if item.Version != expectedVersion {
		return empty, common.NewVersionConflictError("ticket", item.ID, expectedVersion, item.Version)
	}
	if err = s.execution.RequireEntMembers(ctx, tx, claim.TenantID, item.ID); err != nil {
		return empty, err
	}
	member, err := s.execution.TenantPredicate(ctx, tx, claim.TenantID, ticket.FieldTenantID, ticket.FieldID)
	if err != nil {
		return empty, err
	}
	count, err := tx.Ticket.Update().Where(ticket.IDEQ(item.ID), ticket.TenantIDEQ(claim.TenantID), ticket.VersionEQ(item.Version), ticket.DeletedAtIsNil(), member).SetPriority(priority).SetStatus("escalated").AddVersion(1).Save(ctx)
	if err != nil {
		return empty, err
	}
	if count != 1 {
		return empty, common.NewVersionConflictError("ticket", item.ID, item.Version, item.Version)
	}
	if len(recipients) > 0 {
		err = s.notificationSvc.EnqueueNotificationTx(ctx, tx, item.ID, claim.TenantID, &dto.SendTicketNotificationRequest{UserIDs: recipients, EventType: "ticket_updated", Content: fmt.Sprintf("工单 %s (#%s) 已升级，原因：%s", item.Title, item.TicketNumber, reason), InAppOnly: true, DeliveryKey: "workflow-escalation:" + claim.ExecutionKey})
		if err != nil {
			return empty, err
		}
	}
	result := workitemmutation.Result{WorkItemID: item.ID, Version: item.Version + 1, Status: "escalated"}
	if err = workitemmutation.RecordTx(ctx, tx, meta, result, "work_item.escalation.workflow", digest, map[string]interface{}{"callbackId": callback.ID, "priority": priority, "reason": reason, "recipients": recipients}); err != nil {
		return empty, err
	}
	// Recheck time at the final write boundary while the locked callback excludes recovery.
	if time.Now().After(callback.LeaseExpiresAt) {
		return empty, common.NewForbiddenError("workflow callback lease expired")
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}
