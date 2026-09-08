package service

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketassignmentrule"
	"itsm-backend/ent/user"

	"go.uber.org/zap"
)

type TicketAssignmentRuleService struct {
	client *ent.Client
	logger *zap.SugaredLogger
}

func NewTicketAssignmentRuleService(client *ent.Client, logger *zap.SugaredLogger) *TicketAssignmentRuleService {
	return &TicketAssignmentRuleService{
		client: client,
		logger: logger,
	}
}

// ListAssignmentRules 获取分配规则列表
func (s *TicketAssignmentRuleService) ListAssignmentRules(
	ctx context.Context,
	tenantID int,
) ([]*dto.AssignmentRuleResponse, error) {
	rules, err := s.client.TicketAssignmentRule.Query().
		Where(ticketassignmentrule.TenantID(tenantID)).
		Order(ent.Desc(ticketassignmentrule.FieldPriority)).
		Order(ent.Desc(ticketassignmentrule.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to list assignment rules", "error", err)
		return nil, fmt.Errorf("failed to list assignment rules: %w", err)
	}

	responses := make([]*dto.AssignmentRuleResponse, 0, len(rules))
	for _, rule := range rules {
		responses = append(responses, s.toAssignmentRuleResponse(rule))
	}

	return responses, nil
}

// GetAssignmentRule 获取分配规则详情
func (s *TicketAssignmentRuleService) GetAssignmentRule(
	ctx context.Context,
	ruleID, tenantID int,
) (*dto.AssignmentRuleResponse, error) {
	rule, err := s.client.TicketAssignmentRule.Query().
		Where(
			ticketassignmentrule.ID(ruleID),
			ticketassignmentrule.TenantID(tenantID),
		).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("assignment rule not found: %w", err)
	}

	return s.toAssignmentRuleResponse(rule), nil
}

// CreateAssignmentRule 创建分配规则
func (s *TicketAssignmentRuleService) CreateAssignmentRule(
	ctx context.Context,
	req *dto.CreateAssignmentRuleRequest,
	tenantID int,
) (*dto.AssignmentRuleResponse, error) {
	rule, err := s.client.TicketAssignmentRule.Create().
		SetName(req.Name).
		SetNillableDescription(&req.Description).
		SetPriority(req.Priority).
		SetConditions(req.Conditions).
		SetActions(req.Actions).
		SetIsActive(req.IsActive).
		SetTenantID(tenantID).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create assignment rule", "error", err)
		return nil, fmt.Errorf("failed to create assignment rule: %w", err)
	}

	return s.toAssignmentRuleResponse(rule), nil
}

// UpdateAssignmentRule 更新分配规则
func (s *TicketAssignmentRuleService) UpdateAssignmentRule(
	ctx context.Context,
	ruleID int,
	req *dto.UpdateAssignmentRuleRequest,
	tenantID int,
) (*dto.AssignmentRuleResponse, error) {
	updateQuery := s.client.TicketAssignmentRule.UpdateOneID(ruleID).
		Where(ticketassignmentrule.TenantID(tenantID))

	if req.Name != nil {
		updateQuery.SetName(*req.Name)
	}
	if req.Description != nil {
		updateQuery.SetNillableDescription(req.Description)
	}
	if req.Priority != nil {
		updateQuery.SetPriority(*req.Priority)
	}
	if req.Conditions != nil {
		updateQuery.SetConditions(req.Conditions)
	}
	if req.Actions != nil {
		updateQuery.SetActions(req.Actions)
	}
	if req.IsActive != nil {
		updateQuery.SetIsActive(*req.IsActive)
	}

	rule, err := updateQuery.Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to update assignment rule", "error", err)
		return nil, fmt.Errorf("failed to update assignment rule: %w", err)
	}

	return s.toAssignmentRuleResponse(rule), nil
}

// DeleteAssignmentRule 删除分配规则
func (s *TicketAssignmentRuleService) DeleteAssignmentRule(
	ctx context.Context,
	ruleID, tenantID int,
) error {
	_, err := s.client.TicketAssignmentRule.Delete().
		Where(
			ticketassignmentrule.ID(ruleID),
			ticketassignmentrule.TenantID(tenantID),
		).
		Exec(ctx)
	if err != nil {
		s.logger.Errorw("Failed to delete assignment rule", "error", err)
		return fmt.Errorf("failed to delete assignment rule: %w", err)
	}

	return nil
}

// TestAssignmentRule 测试分配规则
func (s *TicketAssignmentRuleService) TestAssignmentRule(
	ctx context.Context,
	req *dto.TestAssignmentRuleRequest,
	tenantID int,
) (*dto.TestAssignmentRuleResponse, error) {
	// 获取规则
	rule, err := s.client.TicketAssignmentRule.Get(ctx, req.RuleID)
	if err != nil {
		return nil, fmt.Errorf("assignment rule not found: %w", err)
	}

	if rule.TenantID != tenantID {
		return nil, fmt.Errorf("assignment rule not found")
	}

	// 获取工单
	ticketEntity, err := s.client.Ticket.Get(ctx, req.TicketID)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	// 检查规则是否匹配
	matched, reason := s.matchRule(ctx, rule, ticketEntity)
	if !matched {
		return &dto.TestAssignmentRuleResponse{
			Matched: false,
			Reason:  reason,
		}, nil
	}

	// 执行规则动作
	assignedTo, score, err := s.executeRuleAction(ctx, rule, ticketEntity)
	if err != nil {
		return &dto.TestAssignmentRuleResponse{
			Matched: true,
			Reason:  fmt.Sprintf("规则匹配，但执行失败: %v", err),
		}, nil
	}

	return &dto.TestAssignmentRuleResponse{
		Matched:    true,
		AssignedTo: assignedTo,
		Reason:     fmt.Sprintf("规则匹配成功，将分配给用户ID: %d", *assignedTo),
		Score:      score,
	}, nil
}

// matchRule 检查规则是否匹配工单
func (s *TicketAssignmentRuleService) matchRule(
	ctx context.Context,
	rule *ent.TicketAssignmentRule,
	ticketEntity *ent.Ticket,
) (bool, string) {
	matched, err := evaluateTicketRuleConditions(rule.Conditions, ticketEntity)
	if err != nil {
		return false, err.Error()
	}
	return matched, "条件已评估"
}

// compareValue 比较值
func (s *TicketAssignmentRuleService) compareValue(actual interface{}, operator string, expected interface{}) bool {
	switch operator {
	case "equals":
		return actual == expected
	case "not_equals":
		return actual != expected
	case "contains":
		actualStr := fmt.Sprintf("%v", actual)
		expectedStr := fmt.Sprintf("%v", expected)
		return containsStringInRule(actualStr, expectedStr)
	case "greater_than":
		return compareNumbers(actual, expected) > 0
	case "less_than":
		return compareNumbers(actual, expected) < 0
	default:
		return false
	}
}

// ExecuteRuleAction 执行规则动作（公开方法）
func (s *TicketAssignmentRuleService) ExecuteRuleAction(
	ctx context.Context,
	rule *ent.TicketAssignmentRule,
	ticketEntity *ent.Ticket,
) (*int, float64, error) {
	return s.executeRuleAction(ctx, rule, ticketEntity)
}

// executeRuleAction 执行规则动作
func (s *TicketAssignmentRuleService) executeRuleAction(
	ctx context.Context,
	rule *ent.TicketAssignmentRule,
	ticketEntity *ent.Ticket,
) (*int, float64, error) {
	actionType, _ := rule.Actions["type"].(string)
	var ids []int
	switch actionType {
	case "user":
		id, err := ticketRulePositiveID(rule.Actions["value"])
		if err != nil {
			return nil, 0, err
		}
		ids = []int{id}
	case "round_robin", "load_balance":
		values, ok := rule.Actions["value"].([]interface{})
		if !ok || len(values) == 0 {
			return nil, 0, fmt.Errorf("assignment rule requires users")
		}
		for _, value := range values {
			id, err := ticketRulePositiveID(value)
			if err != nil {
				return nil, 0, err
			}
			ids = append(ids, id)
		}
	default:
		return nil, 0, fmt.Errorf("unknown assignment action: %s", actionType)
	}
	for _, id := range ids {
		exists, err := s.client.User.Query().Where(user.IDEQ(id), user.TenantIDEQ(ticketEntity.TenantID), user.ActiveEQ(true)).Exist(ctx)
		if err != nil {
			return nil, 0, err
		}
		if !exists {
			return nil, 0, fmt.Errorf("assignment user is unavailable")
		}
	}
	selected := ids[0]
	if actionType == "round_robin" {
		selected = ids[rule.ExecutionCount%len(ids)]
	}
	if actionType == "load_balance" {
		least := -1
		for _, id := range ids {
			count, err := s.client.Ticket.Query().Where(ticket.AssigneeIDEQ(id), ticket.TenantIDEQ(ticketEntity.TenantID), ticket.StatusNotIn("closed", "resolved", "cancelled")).Count(ctx)
			if err != nil {
				return nil, 0, err
			}
			if least < 0 || count < least || (count == least && id < selected) {
				selected = id
				least = count
			}
		}
	}
	return &selected, 1, nil
}

// toAssignmentRuleResponse 转换为响应DTO
func (s *TicketAssignmentRuleService) toAssignmentRuleResponse(rule *ent.TicketAssignmentRule) *dto.AssignmentRuleResponse {
	resp := &dto.AssignmentRuleResponse{
		ID:             rule.ID,
		Name:           rule.Name,
		Description:    rule.Description,
		Priority:       rule.Priority,
		Conditions:     rule.Conditions,
		Actions:        rule.Actions,
		IsActive:       rule.IsActive,
		ExecutionCount: rule.ExecutionCount,
		CreatedAt:      rule.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      rule.UpdatedAt.Format(time.RFC3339),
	}

	if !rule.LastExecutedAt.IsZero() {
		lastExecutedAt := rule.LastExecutedAt.Format(time.RFC3339)
		resp.LastExecutedAt = &lastExecutedAt
	}

	return resp
}

// 辅助函数
func containsStringInRule(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || containsHelperInRule(s, substr))
}

func containsHelperInRule(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func compareNumbers(a, b interface{}) int {
	af, ok1 := toFloat64(a)
	bf, ok2 := toFloat64(b)
	if !ok1 || !ok2 {
		return 0
	}
	if af > bf {
		return 1
	} else if af < bf {
		return -1
	}
	return 0
}

func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}
