package service

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketautomationrule"

	"go.uber.org/zap"
)

type TicketAutomationRuleService struct {
	client              *ent.Client
	logger              *zap.SugaredLogger
	ticketService       *TicketService
	assignmentService   *TicketAssignmentService
	notificationService *TicketNotificationService
}

func NewTicketAutomationRuleService(
	client *ent.Client,
	logger *zap.SugaredLogger,
) *TicketAutomationRuleService {
	return &TicketAutomationRuleService{
		client: client,
		logger: logger,
	}
}

// SetTicketService 设置工单服务（用于依赖注入）
func (s *TicketAutomationRuleService) SetTicketService(ticketService *TicketService) {
	s.ticketService = ticketService
}

// SetAssignmentService 设置分配服务（用于依赖注入）
func (s *TicketAutomationRuleService) SetAssignmentService(assignmentService *TicketAssignmentService) {
	s.assignmentService = assignmentService
}

// SetNotificationService 设置通知服务（用于依赖注入）
func (s *TicketAutomationRuleService) SetNotificationService(notificationService *TicketNotificationService) {
	s.notificationService = notificationService
}

// ListAutomationRules 获取自动化规则列表
func (s *TicketAutomationRuleService) ListAutomationRules(
	ctx context.Context,
	tenantID int,
) ([]*dto.AutomationRuleResponse, error) {
	rules, err := s.client.TicketAutomationRule.Query().
		Where(ticketautomationrule.TenantID(tenantID)).
		Order(ent.Desc(ticketautomationrule.FieldPriority)).
		Order(ent.Desc(ticketautomationrule.FieldCreatedAt)).
		WithCreator().
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to list automation rules", "error", err, "tenant_id", tenantID)
		return nil, fmt.Errorf("failed to list automation rules: %w", err)
	}

	responses := make([]*dto.AutomationRuleResponse, 0, len(rules))
	for _, rule := range rules {
		var creator *ent.User
		if rule.Edges.Creator != nil {
			creator = rule.Edges.Creator
		} else {
			creator, _ = s.client.User.Get(ctx, rule.CreatedBy)
		}
		responses = append(responses, dto.ToAutomationRuleResponse(rule, creator))
	}

	return responses, nil
}

// GetAutomationRule 获取自动化规则详情
func (s *TicketAutomationRuleService) GetAutomationRule(
	ctx context.Context,
	ruleID, tenantID int,
) (*dto.AutomationRuleResponse, error) {
	rule, err := s.client.TicketAutomationRule.Query().
		Where(
			ticketautomationrule.ID(ruleID),
			ticketautomationrule.TenantID(tenantID),
		).
		WithCreator().
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("automation rule not found: %w", err)
	}

	var creator *ent.User
	if rule.Edges.Creator != nil {
		creator = rule.Edges.Creator
	} else {
		creator, _ = s.client.User.Get(ctx, rule.CreatedBy)
	}

	return dto.ToAutomationRuleResponse(rule, creator), nil
}

// CreateAutomationRule 创建自动化规则
func (s *TicketAutomationRuleService) CreateAutomationRule(
	ctx context.Context,
	req *dto.CreateAutomationRuleRequest,
	userID, tenantID int,
) (*dto.AutomationRuleResponse, error) {
	s.logger.Infow("Creating automation rule", "name", req.Name, "user_id", userID, "tenant_id", tenantID)

	rule, err := s.client.TicketAutomationRule.Create().
		SetName(req.Name).
		SetNillableDescription(req.Description).
		SetPriority(req.Priority).
		SetConditions(req.Conditions).
		SetActions(req.Actions).
		SetIsActive(req.IsActive).
		SetCreatedBy(userID).
		SetTenantID(tenantID).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create automation rule", "error", err)
		return nil, fmt.Errorf("failed to create automation rule: %w", err)
	}

	creator, _ := s.client.User.Get(ctx, userID)
	return dto.ToAutomationRuleResponse(rule, creator), nil
}

// UpdateAutomationRule 更新自动化规则
func (s *TicketAutomationRuleService) UpdateAutomationRule(
	ctx context.Context,
	ruleID int,
	req *dto.UpdateAutomationRuleRequest,
	tenantID int,
) (*dto.AutomationRuleResponse, error) {
	s.logger.Infow("Updating automation rule", "rule_id", ruleID, "tenant_id", tenantID)

	updateQuery := s.client.TicketAutomationRule.UpdateOneID(ruleID).
		Where(ticketautomationrule.TenantID(tenantID))

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

	updateQuery.SetUpdatedAt(time.Now())

	updatedRule, err := updateQuery.Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to update automation rule", "error", err)
		return nil, fmt.Errorf("failed to update automation rule: %w", err)
	}

	creator, _ := s.client.User.Get(ctx, updatedRule.CreatedBy)
	return dto.ToAutomationRuleResponse(updatedRule, creator), nil
}

// DeleteAutomationRule 删除自动化规则
func (s *TicketAutomationRuleService) DeleteAutomationRule(
	ctx context.Context,
	ruleID, tenantID int,
) error {
	s.logger.Infow("Deleting automation rule", "rule_id", ruleID, "tenant_id", tenantID)

	err := s.client.TicketAutomationRule.DeleteOneID(ruleID).
		Where(ticketautomationrule.TenantID(tenantID)).
		Exec(ctx)
	if err != nil {
		s.logger.Errorw("Failed to delete automation rule", "error", err)
		return fmt.Errorf("failed to delete automation rule: %w", err)
	}

	return nil
}

// TestAutomationRule 测试自动化规则
func (s *TicketAutomationRuleService) TestAutomationRule(
	ctx context.Context,
	req *dto.TestAutomationRuleRequest,
	tenantID int,
) (*dto.TestAutomationRuleResponse, error) {
	s.logger.Infow("Testing automation rule", "rule_id", req.RuleID, "ticket_id", req.TicketID)

	// 获取规则
	rule, err := s.client.TicketAutomationRule.Query().
		Where(
			ticketautomationrule.ID(req.RuleID),
			ticketautomationrule.TenantID(tenantID),
		).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("automation rule not found: %w", err)
	}

	// 获取工单
	ticketEntity, err := s.client.Ticket.Query().
		Where(
			ticket.ID(req.TicketID),
			ticket.TenantID(tenantID),
		).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	// 评估条件
	matched, reason := s.evaluateConditions(ctx, rule.Conditions, ticketEntity)
	if !matched {
		return &dto.TestAutomationRuleResponse{
			Matched: false,
			Reason:  reason,
		}, nil
	}

	// 如果匹配，返回将执行的动作
	actionDescriptions := s.getActionDescriptions(rule.Actions)
	return &dto.TestAutomationRuleResponse{
		Matched: true,
		Actions: actionDescriptions,
		Reason:  "规则条件匹配，将执行以下动作",
	}, nil
}

func (s *TicketAutomationRuleService) evaluateConditions(ctx context.Context, conditions []map[string]interface{}, item *ent.Ticket) (bool, string) {
	matched, err := evaluateTicketRuleConditions(conditions, item)
	if err != nil {
		return false, err.Error()
	}
	return matched, "条件已评估"
}

// getActionDescriptions 获取动作描述
func (s *TicketAutomationRuleService) getActionDescriptions(actions []map[string]interface{}) []string {
	descriptions := make([]string, 0, len(actions))
	for _, action := range actions {
		actionType, _ := action["type"].(string)
		switch actionType {
		case "set_category":
			descriptions = append(descriptions, "设置分类")
		case "set_priority":
			descriptions = append(descriptions, "设置优先级")
		case "assign":
			descriptions = append(descriptions, "分配给用户")
		case "auto_assign":
			descriptions = append(descriptions, "自动分配")
		case "escalate":
			descriptions = append(descriptions, "升级优先级")
		case "send_notification":
			descriptions = append(descriptions, "发送通知")
		case "set_status":
			descriptions = append(descriptions, "设置状态")
		default:
			descriptions = append(descriptions, fmt.Sprintf("执行动作: %s", actionType))
		}
	}
	return descriptions
}
