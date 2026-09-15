package service

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/slaalerthistory"
	"itsm-backend/ent/slaalertrule"
	"itsm-backend/ent/sladefinition"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

// defaultAlertCooldownMinutes 默认告警抑制间隔（分钟）
//
// 为避免高频扫描（默认 5 分钟一轮）在同一工单同一规则上重复触发告警，
// 同一 (ticket_id, alert_rule_id) 在 cooldown 窗口内只保留一条未解决的
// alert history。抑制间隔可通过 alert_rule.EscalationLevels JSON 中
// 第一个元素的 cooldown_minutes 字段覆盖（0 或负数表示禁用）。
const defaultAlertCooldownMinutes = 15

type SLAAlertService struct {
	client          *ent.Client
	execution       *database.ExecutionPolicy
	logger          *zap.SugaredLogger
	notificationSvc *TicketNotificationService
}

func NewSLAAlertService(client *ent.Client, logger *zap.SugaredLogger, execution *database.ExecutionPolicy) *SLAAlertService {
	return &SLAAlertService{
		client:    client,
		execution: execution,
		logger:    logger,
	}
}

// SetNotificationService 设置通知服务
func (s *SLAAlertService) SetNotificationService(notificationSvc *TicketNotificationService) {
	s.notificationSvc = notificationSvc
}

// CreateAlertRule 创建SLA预警规则
func (s *SLAAlertService) CreateAlertRule(ctx context.Context, req *dto.CreateSLAAlertRuleRequest, tenantID int) (*dto.SLAAlertRuleResponse, error) {
	s.logger.Infow("Creating SLA alert rule", "name", req.Name, "tenant_id", tenantID)
	if err := validateIncidentAlertChannels(req.NotificationChannels); err != nil {
		return nil, err
	}

	// 验证SLA定义是否存在
	_, err := s.client.SLADefinition.Query().
		Where(sladefinition.IDEQ(req.SLADefinitionID), sladefinition.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("SLA definition not found: %w", err)
	}

	// 转换EscalationLevels为map格式
	escalationLevelsMap := make([]map[string]interface{}, len(req.EscalationLevels))
	for i, level := range req.EscalationLevels {
		escalationLevelsMap[i] = map[string]interface{}{
			"level":        level.Level,
			"threshold":    level.Threshold,
			"notify_users": level.NotifyUsers,
		}
	}

	alertRule, err := s.client.SLAAlertRule.Create().
		SetName(req.Name).
		SetSLADefinitionID(req.SLADefinitionID).
		SetAlertLevel(req.AlertLevel).
		SetThresholdPercentage(req.ThresholdPercentage).
		SetNotificationChannels(req.NotificationChannels).
		SetEscalationEnabled(req.EscalationEnabled).
		SetEscalationLevels(escalationLevelsMap).
		SetIsActive(req.IsActive).
		SetTenantID(tenantID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create SLA alert rule", "error", err)
		return nil, fmt.Errorf("failed to create SLA alert rule: %w", err)
	}

	return s.toAlertRuleResponse(alertRule), nil
}

// UpdateAlertRule 更新SLA预警规则
func (s *SLAAlertService) UpdateAlertRule(ctx context.Context, id int, req *dto.UpdateSLAAlertRuleRequest, tenantID int) (*dto.SLAAlertRuleResponse, error) {
	s.logger.Infow("Updating SLA alert rule", "id", id, "tenant_id", tenantID)

	update := s.client.SLAAlertRule.Update().
		Where(
			slaalertrule.IDEQ(id),
			slaalertrule.TenantIDEQ(tenantID),
		).
		SetUpdatedAt(time.Now())

	if req.Name != nil {
		update = update.SetName(*req.Name)
	}
	if req.AlertLevel != nil {
		update = update.SetAlertLevel(*req.AlertLevel)
	}
	if req.ThresholdPercentage != nil {
		update = update.SetThresholdPercentage(*req.ThresholdPercentage)
	}
	if req.NotificationChannels != nil {
		if err := validateIncidentAlertChannels(*req.NotificationChannels); err != nil {
			return nil, err
		}
		update = update.SetNotificationChannels(*req.NotificationChannels)
	}
	if req.EscalationEnabled != nil {
		update = update.SetEscalationEnabled(*req.EscalationEnabled)
	}
	if req.EscalationLevels != nil {
		// 转换EscalationLevels为map格式
		escalationLevelsMap := make([]map[string]interface{}, len(*req.EscalationLevels))
		for i, level := range *req.EscalationLevels {
			escalationLevelsMap[i] = map[string]interface{}{
				"level":        level.Level,
				"threshold":    level.Threshold,
				"notify_users": level.NotifyUsers,
			}
		}
		update = update.SetEscalationLevels(escalationLevelsMap)
	}
	if req.IsActive != nil {
		update = update.SetIsActive(*req.IsActive)
	}

	_, err := update.Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to update SLA alert rule", "error", err)
		return nil, fmt.Errorf("failed to update SLA alert rule: %w", err)
	}

	// 重新获取更新后的规则
	alertRule, err := s.client.SLAAlertRule.Query().
		Where(
			slaalertrule.IDEQ(id),
			slaalertrule.TenantIDEQ(tenantID),
		).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get updated alert rule: %w", err)
	}

	return s.toAlertRuleResponse(alertRule), nil
}

// DeleteAlertRule 删除SLA预警规则
func (s *SLAAlertService) DeleteAlertRule(ctx context.Context, id int, tenantID int) error {
	s.logger.Infow("Deleting SLA alert rule", "id", id, "tenant_id", tenantID)

	_, err := s.client.SLAAlertRule.Delete().
		Where(
			slaalertrule.IDEQ(id),
			slaalertrule.TenantIDEQ(tenantID),
		).
		Exec(ctx)
	if err != nil {
		s.logger.Errorw("Failed to delete SLA alert rule", "error", err)
		return fmt.Errorf("failed to delete SLA alert rule: %w", err)
	}

	return nil
}

// ListAlertRules 获取SLA预警规则列表
func (s *SLAAlertService) ListAlertRules(ctx context.Context, filters map[string]interface{}, tenantID int) ([]*dto.SLAAlertRuleResponse, error) {
	s.logger.Infow("Listing SLA alert rules", "tenant_id", tenantID)

	query := s.client.SLAAlertRule.Query().
		Where(slaalertrule.TenantIDEQ(tenantID))

	if slaDefinitionID, ok := filters["sla_definition_id"].(int); ok {
		query = query.Where(slaalertrule.SLADefinitionIDEQ(slaDefinitionID))
	}
	if isActive, ok := filters["is_active"].(bool); ok {
		query = query.Where(slaalertrule.IsActiveEQ(isActive))
	}
	if alertLevel, ok := filters["alert_level"].(string); ok && alertLevel != "" {
		query = query.Where(slaalertrule.AlertLevelEQ(alertLevel))
	}

	alertRules, err := query.Order(ent.Desc(slaalertrule.FieldCreatedAt)).All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to list SLA alert rules", "error", err)
		return nil, fmt.Errorf("failed to list SLA alert rules: %w", err)
	}

	responses := make([]*dto.SLAAlertRuleResponse, len(alertRules))
	for i, rule := range alertRules {
		responses[i] = s.toAlertRuleResponse(rule)
	}

	return responses, nil
}

// GetAlertRule 获取SLA预警规则详情
func (s *SLAAlertService) GetAlertRule(ctx context.Context, id int, tenantID int) (*dto.SLAAlertRuleResponse, error) {
	s.logger.Infow("Getting SLA alert rule", "id", id, "tenant_id", tenantID)

	alertRule, err := s.client.SLAAlertRule.Query().
		Where(
			slaalertrule.IDEQ(id),
			slaalertrule.TenantIDEQ(tenantID),
		).
		Only(ctx)
	if err != nil {
		s.logger.Errorw("Failed to get SLA alert rule", "error", err)
		return nil, fmt.Errorf("failed to get SLA alert rule: %w", err)
	}

	return s.toAlertRuleResponse(alertRule), nil
}

// GetAlertHistory 获取SLA预警历史
func (s *SLAAlertService) GetAlertHistory(ctx context.Context, req *dto.GetSLAAlertHistoryRequest, tenantID int) ([]*dto.SLAAlertHistoryResponse, int, error) {
	s.logger.Infow("Getting SLA alert history", "tenant_id", tenantID)

	query := s.client.SLAAlertHistory.Query().
		Where(slaalerthistory.TenantIDEQ(tenantID))

	if req.SLADefinitionID != nil {
		// 需要通过alert_rule关联查询
		alertRuleIDs, err := s.client.SLAAlertRule.Query().
			Where(
				slaalertrule.SLADefinitionIDEQ(*req.SLADefinitionID),
				slaalertrule.TenantIDEQ(tenantID),
			).
			IDs(ctx)
		if err == nil && len(alertRuleIDs) > 0 {
			query = query.Where(slaalerthistory.AlertRuleIDIn(alertRuleIDs...))
		}
	}
	if req.AlertRuleID != nil {
		query = query.Where(slaalerthistory.AlertRuleIDEQ(*req.AlertRuleID))
	}
	if req.TicketID != nil {
		query = query.Where(slaalerthistory.TicketIDEQ(*req.TicketID))
	}
	if req.AlertLevel != nil && *req.AlertLevel != "" {
		query = query.Where(slaalerthistory.AlertLevelEQ(*req.AlertLevel))
	}

	// 解析时间范围
	if req.StartTime != "" && req.EndTime != "" {
		startTime, err := time.Parse(time.RFC3339, req.StartTime)
		if err == nil {
			endTime, err := time.Parse(time.RFC3339, req.EndTime)
			if err == nil {
				query = query.Where(
					slaalerthistory.CreatedAtGTE(startTime),
					slaalerthistory.CreatedAtLTE(endTime),
				)
			}
		}
	}

	// 获取总数
	total, err := query.Count(ctx)
	if err != nil {
		s.logger.Errorw("Failed to count SLA alert history", "error", err)
		return nil, 0, fmt.Errorf("failed to count SLA alert history: %w", err)
	}

	// 分页查询
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	histories, err := query.
		Order(ent.Desc(slaalerthistory.FieldCreatedAt)).
		Offset(offset).
		Limit(pageSize).
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to get SLA alert history", "error", err)
		return nil, 0, fmt.Errorf("failed to get SLA alert history: %w", err)
	}

	sent, err := s.projectAlertNotificationStatus(ctx, histories, tenantID)
	if err != nil {
		return nil, 0, err
	}
	responses := make([]*dto.SLAAlertHistoryResponse, len(histories))
	for i, history := range histories {
		responses[i] = s.toAlertHistoryResponse(history, sent[history.ID])
	}

	return responses, total, nil
}

// CheckAndTriggerAlerts checks both SLA deadlines in one owning transaction.
func (s *SLAAlertService) CheckAndTriggerAlerts(ctx context.Context, ticketID, tenantID int) (bool, error) {
	return s.triggerAlerts(ctx, ticketID, tenantID, "")
}

func (s *SLAAlertService) TriggerSLAWarning(ctx context.Context, ticketID int, warningType string, tenantID int) (bool, error) {
	if warningType != "response_time" && warningType != "resolution_time" {
		return false, fmt.Errorf("unsupported SLA warning type: %s", warningType)
	}
	return s.triggerAlerts(ctx, ticketID, tenantID, warningType)
}

func (s *SLAAlertService) triggerAlerts(ctx context.Context, ticketID, tenantID int, onlyType string) (bool, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	member, err := s.execution.TenantPredicate(ctx, tx, tenantID, ticket.FieldTenantID, ticket.FieldID)
	if err != nil {
		return false, err
	}
	if err := s.execution.RequireEntMembers(ctx, tx, tenantID, ticketID); err != nil {
		return false, err
	}
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(ticketID), ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil(), member).Only(ctx)
	if err != nil {
		return false, err
	}
	if item.ClosedAt != nil || item.SLADefinitionID == 0 {
		return false, tx.Commit()
	}
	rules, err := tx.SLAAlertRule.Query().Where(slaalertrule.TenantIDEQ(tenantID), slaalertrule.SLADefinitionIDEQ(item.SLADefinitionID), slaalertrule.IsActiveEQ(true)).Order(ent.Asc(slaalertrule.FieldID)).All(ctx)
	if err != nil {
		return false, err
	}
	now := time.Now()
	triggered := false
	for _, kind := range []struct {
		name     string
		deadline time.Time
		complete bool
	}{
		{"response_time", item.SLAResponseDeadline, !item.FirstResponseAt.IsZero()},
		{"resolution_time", item.SLAResolutionDeadline, !item.ResolvedAt.IsZero()},
	} {
		if onlyType != "" && kind.name != onlyType {
			continue
		}
		if kind.complete || kind.deadline.IsZero() || !now.Before(kind.deadline) {
			continue
		}
		total := kind.deadline.Sub(slaCycleStart(item)) - time.Duration(item.SLAPausedMinutes)*time.Minute
		if total <= 0 {
			continue
		}
		percentage := kind.deadline.Sub(now).Seconds() / total.Seconds() * 100
		for _, rule := range rules {
			if percentage > float64(rule.ThresholdPercentage) {
				continue
			}
			if err := validateIncidentAlertChannels(rule.NotificationChannels); err != nil {
				return false, err
			}
			exists, err := tx.SLAAlertHistory.Query().Where(slaalerthistory.TenantIDEQ(tenantID), slaalerthistory.TicketIDEQ(ticketID), slaalerthistory.AlertRuleIDEQ(rule.ID), slaalerthistory.ResolvedAtIsNil()).Exist(ctx)
			if err != nil {
				return false, err
			}
			if exists {
				continue
			}
			cooldown := resolveCooldownMinutes(rule)
			if cooldown > 0 {
				exists, err = tx.SLAAlertHistory.Query().Where(slaalerthistory.TenantIDEQ(tenantID), slaalerthistory.TicketIDEQ(ticketID), slaalerthistory.AlertRuleIDEQ(rule.ID), slaalerthistory.CreatedAtGT(now.Add(-time.Duration(cooldown)*time.Minute))).Exist(ctx)
				if err != nil {
					return false, err
				}
				if exists {
					continue
				}
			}
			if len(rule.NotificationChannels) > 0 && s.notificationSvc == nil {
				return false, fmt.Errorf("SLA alert notification service is required")
			}
			if !triggered {
				count, err := tx.Ticket.Update().Where(ticket.IDEQ(ticketID), ticket.TenantIDEQ(tenantID), ticket.VersionEQ(item.Version), ticket.DeletedAtIsNil(), member).AddVersion(1).Save(ctx)
				if err != nil {
					return false, err
				}
				if count != 1 {
					return false, fmt.Errorf("SLA alert cycle changed during scan")
				}
			}
			history, err := tx.SLAAlertHistory.Create().SetNotificationTrackingVersion(1).SetTicketID(ticketID).SetTicketNumber(item.TicketNumber).SetTicketTitle(item.Title).SetAlertRuleID(rule.ID).SetAlertRuleName(rule.Name).SetAlertLevel(rule.AlertLevel).SetThresholdPercentage(rule.ThresholdPercentage).SetActualPercentage(percentage).SetNotificationSent(false).SetEscalationLevel(0).SetTenantID(tenantID).SetCreatedAt(now).Save(ctx)
			if err != nil {
				return false, err
			}
			if s.notificationSvc != nil {
				if err := s.notificationSvc.EnqueueSLAAlertTx(ctx, tx, item, history, rule.NotificationChannels); err != nil {
					return false, err
				}
			}
			triggered = true
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return triggered, nil
}

// 辅助方法：转换为响应DTO
func (s *SLAAlertService) toAlertRuleResponse(rule *ent.SLAAlertRule) *dto.SLAAlertRuleResponse {
	escalationLevels := make([]dto.EscalationLevelConfig, 0)
	if rule.EscalationLevels != nil {
		// EscalationLevels已经是[]map[string]interface{}类型
		for _, levelMap := range rule.EscalationLevels {
			config := dto.EscalationLevelConfig{}
			if levelVal, ok := levelMap["level"].(float64); ok {
				config.Level = int(levelVal)
			} else if levelVal, ok := levelMap["level"].(int); ok {
				config.Level = levelVal
			}
			if thresholdVal, ok := levelMap["threshold"].(float64); ok {
				config.Threshold = int(thresholdVal)
			} else if thresholdVal, ok := levelMap["threshold"].(int); ok {
				config.Threshold = thresholdVal
			}
			if notifyUsers, ok := levelMap["notify_users"].([]interface{}); ok {
				config.NotifyUsers = make([]int, 0, len(notifyUsers))
				for _, u := range notifyUsers {
					if userID, ok := u.(float64); ok {
						config.NotifyUsers = append(config.NotifyUsers, int(userID))
					} else if userID, ok := u.(int); ok {
						config.NotifyUsers = append(config.NotifyUsers, userID)
					}
				}
			}
			escalationLevels = append(escalationLevels, config)
		}
	}

	return &dto.SLAAlertRuleResponse{
		ID:                   rule.ID,
		Name:                 rule.Name,
		SLADefinitionID:      rule.SLADefinitionID,
		AlertLevel:           rule.AlertLevel,
		ThresholdPercentage:  rule.ThresholdPercentage,
		NotificationChannels: rule.NotificationChannels,
		EscalationEnabled:    rule.EscalationEnabled,
		EscalationLevels:     escalationLevels,
		IsActive:             rule.IsActive,
		TenantID:             rule.TenantID,
		CreatedAt:            rule.CreatedAt,
		UpdatedAt:            rule.UpdatedAt,
	}
}

func (s *SLAAlertService) toAlertHistoryResponse(history *ent.SLAAlertHistory, notificationSent bool) *dto.SLAAlertHistoryResponse {
	response := &dto.SLAAlertHistoryResponse{
		ID:                  history.ID,
		TicketID:            history.TicketID,
		TicketNumber:        history.TicketNumber,
		TicketTitle:         history.TicketTitle,
		AlertRuleID:         history.AlertRuleID,
		AlertRuleName:       history.AlertRuleName,
		AlertLevel:          history.AlertLevel,
		ThresholdPercentage: history.ThresholdPercentage,
		ActualPercentage:    history.ActualPercentage,
		NotificationSent:    notificationSent,
		EscalationLevel:     history.EscalationLevel,
		CreatedAt:           history.CreatedAt,
	}

	if !history.ResolvedAt.IsZero() {
		response.ResolvedAt = &history.ResolvedAt
	}

	// Cooldown 信息：未解决的告警仍然处于冷却窗口时返回剩余秒数
	// 默认 cooldown 为 defaultAlertCooldownMinutes
	// （自定义场景需从 alert_rule.EscalationLevels JSON 读取，这里以默认值为准）
	response.CooldownMinutes = defaultAlertCooldownMinutes
	if history.ResolvedAt.IsZero() {
		elapsed := time.Since(history.CreatedAt)
		cooldownDur := time.Duration(defaultAlertCooldownMinutes) * time.Minute
		if elapsed < cooldownDur {
			remaining := cooldownDur - elapsed
			response.CooldownRemainingSeconds = int(remaining.Seconds())
			response.SuppressedByCooldown = true
		} else {
			response.CooldownRemainingSeconds = 0
			response.SuppressedByCooldown = false
		}
	}

	return response
}

// resolveCooldownMinutes 解析告警规则的 cooldown 配置
//
// 优先级（高到低）：
//  1. rule.EscalationLevels JSON 第一个元素的 cooldown_minutes 字段
//  2. 默认常量 defaultAlertCooldownMinutes
//
// 约定：cooldown_minutes <= 0 表示禁用抑制（生产环境慎用）。
func resolveCooldownMinutes(rule *ent.SLAAlertRule) int {
	if rule == nil {
		return defaultAlertCooldownMinutes
	}
	if len(rule.EscalationLevels) == 0 {
		return defaultAlertCooldownMinutes
	}
	first := rule.EscalationLevels[0]
	if first == nil {
		return defaultAlertCooldownMinutes
	}
	v, ok := first["cooldown_minutes"]
	if !ok {
		return defaultAlertCooldownMinutes
	}
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float32:
		return int(n)
	case float64:
		return int(n)
	}
	return defaultAlertCooldownMinutes
}
