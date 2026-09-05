package service

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/ticketassignmentrule"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func (s *TicketService) prepareCreationEffects(ctx context.Context, tx *ent.Tx, plan *creation.CreationPlan) error {
	draft := plan.WorkItem
	item := &ent.Ticket{TenantID: draft.TenantID, RequesterID: draft.RequesterID, Title: draft.Title, Description: draft.Description, Status: draft.Status, Priority: draft.Priority, RecordClass: draft.RecordClass}
	if draft.AssigneeID != nil {
		item.AssigneeID = *draft.AssigneeID
	}
	if draft.CategoryID != nil {
		item.CategoryID = *draft.CategoryID
	}
	requester, err := tx.User.Query().Where(user.IDEQ(item.RequesterID), user.TenantIDEQ(item.TenantID)).Only(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not resolve requester routing facts", err)
	}
	item.DepartmentID = requester.DepartmentID
	if item.AssigneeID == 0 {
		if s.assignmentSmartService != nil {
			assignee, err := s.assignmentSmartService.prepareCreation(ctx, tx, item)
			if err != nil {
				return creation.NewDomainValidationFailed("could not prepare ticket assignment", err)
			}
			if assignee != nil {
				item.AssigneeID = *assignee
			}
		} else {
			configured, err := tx.TicketAssignmentRule.Query().Where(ticketassignmentrule.TenantIDEQ(item.TenantID), ticketassignmentrule.IsActiveEQ(true)).Exist(ctx)
			if err != nil {
				return creation.NewInfrastructureUnavailable("could not load assignment configuration", err)
			}
			if configured {
				return creation.NewDomainValidationFailed("configured assignment rules have no owner", nil)
			}
		}
	}
	effects, err := s.automationRuleSvc.prepareCreationRules(ctx, tx, item)
	if err != nil {
		return err
	}
	if s.connectorManager != nil && plan.Resolved.Command.FeishuTask == nil {
		if connector, configured := s.connectorManager.Get(item.TenantID, "feishu"); configured {
			tasks, ok := connector.(FeishuTaskCreator)
			if !ok || tasks.TaskDestinationIdentity() == "" {
				return creation.NewDomainValidationFailed("configured Feishu connector has no task creation capability", nil)
			}
			effects.FeishuDestination = tasks.TaskDestinationIdentity()
		}
	}
	plan.ProfessionalInput = effects
	plan.WorkItem.Priority = item.Priority
	plan.WorkItem.Status = item.Status
	if item.AssigneeID > 0 {
		plan.WorkItem.AssigneeID = &item.AssigneeID
	}
	if item.CategoryID > 0 {
		plan.WorkItem.CategoryID = &item.CategoryID
	}
	if item.CategoryID > 0 && (draft.CategoryID == nil || *draft.CategoryID != item.CategoryID) {
		command := plan.Resolved.Command
		command.CTI = &creation.CTIInput{CategoryID: &item.CategoryID}
		cti, err := NewTicketCategoryService(tx.Client()).ResolveCreationClassification(ctx, tx, plan.Resolved.Identity, command)
		if err != nil {
			return err
		}
		plan.Resolved.CTI = cti
	}
	plan.RoutingValues = map[string]any{"priority": item.Priority, "status": item.Status, "assignee_id": item.AssigneeID, "category_id": item.CategoryID}
	plan.WorkflowVariables["priority"] = item.Priority
	plan.WorkflowVariables["status"] = item.Status
	plan.WorkflowVariables["assignee_id"] = item.AssigneeID
	plan.WorkflowVariables["category_id"] = item.CategoryID
	return nil
}
func (s *TicketService) writeCreationEffects(ctx context.Context, tx *ent.Tx, item *ent.Ticket, plan *creation.CreationPlan) error {
	effects, ok := plan.ProfessionalInput.(*ticketCreationEffects)
	if !ok {
		return creation.NewInternalFailure("prepared ticket effects are missing", nil)
	}
	for _, ruleID := range effects.RuleIDs {
		if err := tx.AuditLog.Create().SetTenantID(item.TenantID).SetUserID(plan.Resolved.Identity.ActorID).SetResource("ticket").SetAction("creation_rule_applied").SetPath("intake:ticket_rules").SetMethod("INTERNAL").SetRequestBody(fmt.Sprintf(`{"ruleId":%d,"workItemId":%d}`, ruleID, item.ID)).Exec(ctx); err != nil {
			return creation.NewInfrastructureUnavailable("could not record creation rule audit", err)
		}
	}
	for _, notification := range effects.Notifications {
		if s.automationRuleSvc == nil || s.automationRuleSvc.notificationService == nil {
			return creation.NewDomainValidationFailed("creation notification rule owner is missing", nil)
		}
		if err := s.automationRuleSvc.notificationService.EnqueueCreationTx(ctx, tx, item, plan.Resolved.Identity.ActorID, "ticket_updated", notification.Content, fmt.Sprintf("creation:%d:rule:%d:action:%d", item.ID, notification.RuleID, notification.ActionIndex), notification.RecipientIDs); err != nil {
			return err
		}
	}
	if item.AssigneeID > 0 && s.notificationSvc != nil {
		content := fmt.Sprintf("工单 %s 已创建：%s", item.TicketNumber, item.Title)
		if err := s.notificationSvc.EnqueueCreationTx(ctx, tx, item, plan.Resolved.Identity.ActorID, "ticket_created", content, fmt.Sprintf("creation:%d:ticket_created", item.ID), []int{item.RequesterID, item.AssigneeID}); err != nil {
			return err
		}
	}
	if effects.FeishuDestination != "" {
		if err := enqueueFeishuCreation(ctx, tx, item, plan.Resolved.Identity.ActorID, effects.FeishuDestination); err != nil {
			return err
		}
	}

	return nil
}
