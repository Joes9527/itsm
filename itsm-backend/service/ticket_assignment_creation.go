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
	target, matched, err := s.prepareConfiguredAssignment(ctx, tx, item)
	if err != nil || matched {
		return target, err
	}
	// 没有命中任何 active 配置规则时不自动分配：工单保持未分配，交给流程的派单节点
	// （ticket_general_flow 的 Activity_Assign）人工指派。这里不做打分兜底——那样候选池是
	// 租户内全部 active 用户，无工单的人分数完全相同、同分按最小 user_id 裁决，会得到一个
	// 与工单内容无关的固定"处理人"。需要自动分配时，由管理员显式配置 auto_assign 规则动作。
	return nil, nil
}

func (s *TicketAssignmentSmartService) prepareConfiguredAssignment(ctx context.Context, tx *ent.Tx, item *ent.Ticket) (*int, bool, error) {
	if s == nil || s.ruleService == nil {
		return nil, false, fmt.Errorf("configured assignment owner is required")
	}
	rules, err := tx.TicketAssignmentRule.Query().Where(ticketassignmentrule.TenantIDEQ(item.TenantID), ticketassignmentrule.IsActiveEQ(true)).Order(ent.Desc(ticketassignmentrule.FieldPriority), ent.Asc(ticketassignmentrule.FieldID)).All(ctx)
	if err != nil {
		return nil, false, err
	}
	ruleOwner := *s.ruleService
	ruleOwner.client = tx.Client()
	categoryPath, err := ResolveRuleMatchPath(ctx, tx, item.TenantID, item.CategoryID)
	if err != nil {
		return nil, false, err
	}
	for _, rule := range rules {
		matched, err := EvaluateTicketRuleConditions(TicketRuleMatch{Item: item, CategoryPath: categoryPath}, rule.Conditions)
		if err != nil {
			return nil, false, err
		}
		if !matched {
			continue
		}
		target, _, err := ruleOwner.executeRuleAction(ctx, rule, item)
		if err != nil {
			return nil, false, err
		}
		if err := tx.TicketAssignmentRule.UpdateOneID(rule.ID).AddExecutionCount(1).SetLastExecutedAt(time.Now()).Exec(ctx); err != nil {
			return nil, false, err
		}
		return target, true, nil
	}
	return nil, false, nil
}
