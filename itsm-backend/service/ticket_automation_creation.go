package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/ticketautomationrule"
	"itsm-backend/ent/ticketcategory"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
)

type ticketCreationNotification struct {
	RuleID       int
	ActionIndex  int
	Content      string
	RecipientIDs []int
}
type ticketCreationEffects struct {
	FeishuDestination string
	RuleIDs           []int
	Notifications     []ticketCreationNotification
}

func (s *TicketAutomationRuleService) prepareCreationRules(ctx context.Context, tx *ent.Tx, item *ent.Ticket) (*ticketCreationEffects, error) {
	effects := &ticketCreationEffects{}
	rules, err := tx.TicketAutomationRule.Query().Where(ticketautomationrule.TenantIDEQ(item.TenantID), ticketautomationrule.IsActiveEQ(true)).Order(ent.Desc(ticketautomationrule.FieldPriority), ent.Asc(ticketautomationrule.FieldID)).All(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not load ticket creation rules", err)
	}
	if len(rules) > 0 && s == nil {
		return nil, creation.NewDomainValidationFailed("configured ticket rules have no owner", nil)
	}
	for _, rule := range rules {
		matched, err := evaluateTicketRuleConditions(rule.Conditions, item)
		if err != nil {
			return nil, creation.NewDomainValidationFailed("malformed ticket rule conditions", err)
		}
		if !matched {
			continue
		}
		if err := s.prepareRuleActions(ctx, tx, rule, item, effects); err != nil {
			return nil, err
		}
		if err := tx.TicketAutomationRule.UpdateOneID(rule.ID).AddExecutionCount(1).SetLastExecutedAt(time.Now()).Exec(ctx); err != nil {
			return nil, creation.NewInfrastructureUnavailable("could not record ticket rule execution", err)
		}
		effects.RuleIDs = append(effects.RuleIDs, rule.ID)
	}
	return effects, nil
}
func (s *TicketAutomationRuleService) prepareRuleActions(ctx context.Context, tx *ent.Tx, rule *ent.TicketAutomationRule, item *ent.Ticket, effects *ticketCreationEffects) error {
	if len(rule.Actions) == 0 {
		return creation.NewDomainValidationFailed("ticket rule actions are required", nil)
	}
	for index, action := range rule.Actions {
		kind, ok := action["type"].(string)
		if !ok {
			return creation.NewDomainValidationFailed("ticket rule action type is required", nil)
		}
		switch kind {
		case "set_category":
			id, err := ticketRulePositiveID(action["category_id"])
			if err != nil {
				return creation.NewDomainValidationFailed("invalid rule category", err)
			}
			exists, err := tx.TicketCategory.Query().Where(ticketcategory.IDEQ(id), ticketcategory.TenantIDEQ(item.TenantID), ticketcategory.IsActiveEQ(true)).Exist(ctx)
			if err != nil {
				return creation.NewInfrastructureUnavailable("could not resolve rule category", err)
			}
			if !exists {
				return creation.NewReferenceNotFound("rule category is unavailable", nil)
			}
			item.CategoryID = id
		case "set_priority":
			priority, ok := action["priority"].(string)
			if !ok || !validTicketRulePriority(priority) {
				return creation.NewDomainValidationFailed("invalid rule priority", nil)
			}
			item.Priority = priority
		case "assign":
			id, err := ticketRulePositiveID(action["user_id"])
			if err != nil {
				return creation.NewDomainValidationFailed("invalid rule assignee", err)
			}
			exists, err := tx.User.Query().Where(user.IDEQ(id), user.TenantIDEQ(item.TenantID), user.ActiveEQ(true)).Exist(ctx)
			if err != nil {
				return creation.NewInfrastructureUnavailable("could not resolve rule assignee", err)
			}
			if !exists {
				return creation.NewReferenceNotFound("rule assignee is unavailable", nil)
			}
			item.AssigneeID = id
		case "auto_assign":
			if s.assignmentService == nil {
				return creation.NewDomainValidationFailed("auto_assign rule has no assignment owner", nil)
			}
			assignment := *s.assignmentService
			assignment.client = tx.Client()
			var category *int
			if item.CategoryID > 0 {
				category = &item.CategoryID
			}
			result, err := assignment.selectAutoAssignment(ctx, &AssignmentRequest{TenantID: item.TenantID, Priority: item.Priority, CategoryID: category, AutoAssign: true})
			if err != nil {
				return creation.NewInfrastructureUnavailable("could not apply rule assignment", err)
			}
			if result.AssignedTo != nil {
				item.AssigneeID = *result.AssignedTo
			}
		case "escalate":
			priority, ok := map[string]string{"low": "medium", "medium": "high", "high": "urgent", "urgent": "urgent", "critical": "critical"}[item.Priority]
			if !ok {
				return creation.NewDomainValidationFailed("cannot escalate unsupported priority", nil)
			}
			item.Priority = priority
		case "set_status":
			status, ok := action["status"].(string)
			if !ok {
				return creation.NewDomainValidationFailed("rule status is required", nil)
			}
			switch status {
			case "new", "open", "in_progress", "pending", "resolved", "closed", "cancelled":
				item.Status = status
			default:
				return creation.NewDomainValidationFailed("unsupported generic rule status", nil)
			}
		case "send_notification":
			if s.notificationService == nil {
				return creation.NewDomainValidationFailed("notification rule has no notification owner", nil)
			}
			content, ok := action["content"].(string)
			if !ok || strings.TrimSpace(content) == "" {
				return creation.NewDomainValidationFailed("notification rule content is required", nil)
			}
			ids := []int{item.RequesterID}
			if item.AssigneeID > 0 {
				ids = append(ids, item.AssigneeID)
			}
			effects.Notifications = append(effects.Notifications, ticketCreationNotification{RuleID: rule.ID, ActionIndex: index, Content: content, RecipientIDs: uniqueTicketNotificationUserIDs(ids)})
		default:
			return creation.NewDomainValidationFailed(fmt.Sprintf("unsupported ticket rule action: %s", kind), nil)
		}
	}
	return nil
}
func validTicketRulePriority(priority string) bool {
	switch priority {
	case "low", "medium", "high", "urgent", "critical":
		return true
	}
	return false
}
