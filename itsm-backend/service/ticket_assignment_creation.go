package service

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/ticketassignmentrule"
)

func (s *TicketAssignmentSmartService) prepareCreation(ctx context.Context, tx *ent.Tx, item *ent.Ticket) (*int, error) {
	if s == nil || s.assignmentService == nil || s.ruleService == nil {
		return nil, fmt.Errorf("ticket assignment owner is required")
	}
	rules, err := tx.TicketAssignmentRule.Query().Where(ticketassignmentrule.TenantIDEQ(item.TenantID), ticketassignmentrule.IsActiveEQ(true)).Order(ent.Desc(ticketassignmentrule.FieldPriority), ent.Asc(ticketassignmentrule.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	ruleOwner := *s.ruleService
	ruleOwner.client = tx.Client()
	for _, rule := range rules {
		matched, err := evaluateTicketRuleConditions(rule.Conditions, item)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		target, _, err := ruleOwner.executeRuleAction(ctx, rule, item)
		if err != nil {
			return nil, err
		}
		if err := tx.TicketAssignmentRule.UpdateOneID(rule.ID).AddExecutionCount(1).SetLastExecutedAt(time.Now()).Exec(ctx); err != nil {
			return nil, err
		}
		return target, nil
	}
	assignment := *s.assignmentService
	assignment.client = tx.Client()
	var category *int
	if item.CategoryID > 0 {
		category = &item.CategoryID
	}
	result, err := assignment.selectAutoAssignment(ctx, &AssignmentRequest{TenantID: item.TenantID, Priority: item.Priority, CategoryID: category, AutoAssign: true})
	if err != nil {
		return nil, err
	}
	return result.AssignedTo, nil
}
