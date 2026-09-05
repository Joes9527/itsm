package service

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	incidentpkg "itsm-backend/ent/incident"
	"itsm-backend/ent/incidentrule"
	"itsm-backend/ent/incidentruleexecution"
	"itsm-backend/ent/ticket"
	"itsm-backend/service/bpmn"

	"go.uber.org/zap"
)

type IncidentRuleEngine struct {
	client       *ent.Client
	logger       *zap.SugaredLogger
	alertCreator IncidentAlertCreator
}

func NewIncidentRuleEngine(client *ent.Client, logger *zap.SugaredLogger) *IncidentRuleEngine {
	return &IncidentRuleEngine{
		client: client,
		logger: logger,
	}
}

// RuleCondition 规则条件接口
type RuleCondition interface {
	Evaluate(ctx context.Context, incident *ent.Incident) (bool, error)
}

// RuleAction 规则动作接口
type RuleAction interface {
	Execute(ctx context.Context, incident *ent.Incident, tenantID int) error
	ExecuteTx(context.Context, *ent.Tx, *ent.Incident, int) error
}

// PriorityCondition 优先级条件
type PriorityCondition struct {
	Priorities []string
}

func (c *PriorityCondition) Evaluate(ctx context.Context, incident *ent.Incident) (bool, error) {
	for _, priority := range c.Priorities {
		if incident.Edges.WorkItem != nil && incident.Edges.WorkItem.Priority == priority {
			return true, nil
		}
	}
	return false, nil
}

// SeverityCondition 严重程度条件
type SeverityCondition struct {
	Severities []string
}

func (c *SeverityCondition) Evaluate(ctx context.Context, incident *ent.Incident) (bool, error) {
	for _, severity := range c.Severities {
		if incident.Severity == severity {
			return true, nil
		}
	}
	return false, nil
}

// StatusCondition 状态条件
type StatusCondition struct {
	Statuses []string
}

func (c *StatusCondition) Evaluate(ctx context.Context, incident *ent.Incident) (bool, error) {
	for _, status := range c.Statuses {
		if incident.Edges.WorkItem != nil && incident.Edges.WorkItem.Status == status {
			return true, nil
		}
	}
	return false, nil
}

// TimeCondition 时间条件
type TimeCondition struct {
	Field    string // created_at, detected_at, resolved_at
	Operator string // >, <, >=, <=, ==
	Duration time.Duration
}

func (c *TimeCondition) Evaluate(ctx context.Context, incident *ent.Incident) (bool, error) {
	var targetTime time.Time
	now := time.Now()

	switch c.Field {
	case "created_at":
		if incident.Edges.WorkItem == nil {
			return false, fmt.Errorf("incident is missing authoritative WorkItem")
		}
		targetTime = incident.Edges.WorkItem.CreatedAt
	case "detected_at":
		targetTime = incident.DetectedAt
	case "resolved_at":
		if incident.Edges.WorkItem == nil || incident.Edges.WorkItem.ResolvedAt.IsZero() {
			return false, nil
		}
		targetTime = incident.Edges.WorkItem.ResolvedAt
	default:
		return false, fmt.Errorf("unknown time field: %s", c.Field)
	}

	timeDiff := now.Sub(targetTime)

	switch c.Operator {
	case ">":
		return timeDiff > c.Duration, nil
	case "<":
		return timeDiff < c.Duration, nil
	case ">=":
		return timeDiff >= c.Duration, nil
	case "<=":
		return timeDiff <= c.Duration, nil
	case "==":
		return timeDiff == c.Duration, nil
	default:
		return false, fmt.Errorf("unknown operator: %s", c.Operator)
	}
}

// CategoryCondition 分类条件
type CategoryCondition struct {
	Categories []string
}

func (c *CategoryCondition) Evaluate(ctx context.Context, incident *ent.Incident) (bool, error) {
	if incident.Edges.WorkItem == nil || incident.Edges.WorkItem.Edges.Category == nil {
		return false, nil
	}
	categoryName := incident.Edges.WorkItem.Edges.Category.Name
	if parent := incident.Edges.WorkItem.Edges.Category.Edges.Parent; parent != nil {
		categoryName = parent.Name
	}
	for _, category := range c.Categories {
		if categoryName == category {
			return true, nil
		}
	}
	return false, nil
}

// EscalationAction 升级动作
type EscalationAction struct {
	Level        int
	Reason       string
	NotifyUsers  []int
	AutoAssign   bool
	client       *ent.Client
	logger       *zap.SugaredLogger
	alertCreator IncidentAlertCreator
}

func (a *EscalationAction) Execute(ctx context.Context, incident *ent.Incident, tenantID int) error {
	return executeIncidentRuleAction(ctx, a.client, a, incident, tenantID)
}

func (a *EscalationAction) ExecuteTx(ctx context.Context, tx *ent.Tx, incident *ent.Incident, tenantID int) error {
	incidentService := NewIncidentService(a.client, a.logger)
	incidentService.SetAlertCreator(a.alertCreator)

	_, err := incidentService.EscalateIncidentTx(ctx, tx, &dto.IncidentEscalationRequest{
		IncidentID:      incident.ID,
		EscalationLevel: a.Level,
		Reason:          a.Reason,
		NotifyUsers:     a.NotifyUsers,
		AutoAssign:      a.AutoAssign,
	}, tenantID)

	return err
}

// NotificationAction 通知动作
type NotificationAction struct {
	Channels     []string
	Recipients   []string
	Message      string
	Severity     string
	client       *ent.Client
	logger       *zap.SugaredLogger
	alertCreator IncidentAlertCreator
}

func (a *NotificationAction) Execute(ctx context.Context, incident *ent.Incident, tenantID int) error {
	return executeIncidentRuleAction(ctx, a.client, a, incident, tenantID)
}

func (a *NotificationAction) ExecuteTx(ctx context.Context, tx *ent.Tx, incident *ent.Incident, tenantID int) error {
	creator, ok := a.alertCreator.(IncidentAlertTransactionCreator)
	if !ok {
		return fmt.Errorf("incident alerting service is not configured")
	}
	if err := validateIncidentRuleRecipients(ctx, tx.Client(), a.Recipients, tenantID); err != nil {
		return err
	}
	_, err := creator.CreateIncidentAlertTx(ctx, tx, &dto.CreateIncidentAlertRequest{
		IncidentID: incident.ID,
		AlertType:  "notification",
		AlertName:  "规则触发通知",
		Message:    a.Message,
		Severity:   a.Severity,
		Channels:   a.Channels,
		Recipients: a.Recipients,
	}, tenantID)

	return err
}

// AssignmentAction 分配动作
type AssignmentAction struct {
	AssigneeID int
	Reason     string
	client     *ent.Client
	logger     *zap.SugaredLogger
}

func (a *AssignmentAction) Execute(ctx context.Context, incident *ent.Incident, tenantID int) error {
	return executeIncidentRuleAction(ctx, a.client, a, incident, tenantID)
}

func (a *AssignmentAction) ExecuteTx(ctx context.Context, tx *ent.Tx, incident *ent.Incident, tenantID int) error {
	incidentService := NewIncidentService(a.client, a.logger)

	_, err := incidentService.UpdateIncidentTx(ctx, tx, incident.ID, &dto.UpdateIncidentRequest{
		AssigneeID: &a.AssigneeID,
	}, tenantID)

	return err
}

// StatusChangeAction 状态变更动作
type StatusChangeAction struct {
	Status string
	Reason string
	client *ent.Client
	logger *zap.SugaredLogger
}

func (a *StatusChangeAction) Execute(ctx context.Context, incident *ent.Incident, tenantID int) error {
	return executeIncidentRuleAction(ctx, a.client, a, incident, tenantID)
}

func (a *StatusChangeAction) ExecuteTx(ctx context.Context, tx *ent.Tx, incident *ent.Incident, tenantID int) error {
	incidentService := NewIncidentService(a.client, a.logger)

	_, err := incidentService.UpdateIncidentTx(ctx, tx, incident.ID, &dto.UpdateIncidentRequest{
		Status: &a.Status,
	}, tenantID)

	return err
}

// MetricCollectionAction 指标收集动作
type MetricCollectionAction struct {
	MetricType  string
	MetricName  string
	MetricValue float64
	Unit        string
	Tags        map[string]string
	client      *ent.Client
	logger      *zap.SugaredLogger
}

func (a *MetricCollectionAction) Execute(ctx context.Context, incident *ent.Incident, tenantID int) error {
	return executeIncidentRuleAction(ctx, a.client, a, incident, tenantID)
}

func (a *MetricCollectionAction) ExecuteTx(ctx context.Context, tx *ent.Tx, incident *ent.Incident, tenantID int) error {
	incidentService := NewIncidentService(tx.Client(), a.logger)

	_, err := incidentService.CreateIncidentMetric(ctx, &dto.CreateIncidentMetricRequest{
		IncidentID:  incident.ID,
		MetricType:  a.MetricType,
		MetricName:  a.MetricName,
		MetricValue: a.MetricValue,
		Unit:        a.Unit,
		Tags:        a.Tags,
	}, tenantID)

	return err
}

// ExecuteRule 执行规则
func (e *IncidentRuleEngine) ExecuteRule(ctx context.Context, rule *ent.IncidentRule, incident *ent.Incident, tenantID int) error {
	e.logger.Infow("Executing incident rule", "rule_id", rule.ID, "incident_id", incident.ID)
	if incident.Edges.WorkItem == nil || rule.TenantID != tenantID || incident.Edges.WorkItem.TenantID != tenantID {
		return fmt.Errorf("rule or incident does not belong to current tenant")
	}
	if !rule.IsActive {
		return fmt.Errorf("incident rule is disabled")
	}
	authoritativeIncident, err := e.client.Incident.Query().
		Where(incidentpkg.IDEQ(incident.ID), incidentTenantScope(tenantID)).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("incident not found")
		}
		return fmt.Errorf("failed to validate incident: %w", err)
	}
	incident = authoritativeIncident

	// 记录规则执行开始
	execution, err := e.client.IncidentRuleExecution.Create().
		SetRuleID(rule.ID).
		SetIncidentID(incident.ID).
		SetStatus("running").
		SetStartedAt(time.Now()).
		SetInputData(map[string]interface{}{
			"incident_id": incident.ID,
			"rule_id":     rule.ID,
		}).
		SetTenantID(tenantID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		e.logger.Errorw("Failed to create rule execution", "error", err)
		return err
	}

	// 评估条件
	conditions, err := e.parseConditions(rule.Conditions)
	if err != nil {
		e.logger.Errorw("Failed to parse rule conditions", "error", err)
		e.updateExecutionStatus(ctx, execution.ID, tenantID, "failed", fmt.Sprintf("Failed to parse conditions: %v", err))
		return err
	}

	// 检查所有条件是否满足
	allConditionsMet := true
	for _, condition := range conditions {
		met, err := condition.Evaluate(ctx, incident)
		if err != nil {
			e.logger.Errorw("Failed to evaluate condition", "error", err)
			e.updateExecutionStatus(ctx, execution.ID, tenantID, "failed", fmt.Sprintf("Failed to evaluate condition: %v", err))
			return err
		}
		if !met {
			allConditionsMet = false
			break
		}
	}

	if !allConditionsMet {
		e.logger.Infow("Rule conditions not met", "rule_id", rule.ID, "incident_id", incident.ID)
		e.updateExecutionStatus(ctx, execution.ID, tenantID, "skipped", "Rule conditions not met")
		return nil
	}

	// 执行动作
	actions, err := e.parseActions(rule.Actions)
	if err != nil {
		e.logger.Errorw("Failed to parse rule actions", "error", err)
		e.updateExecutionStatus(ctx, execution.ID, tenantID, "failed", fmt.Sprintf("Failed to parse actions: %v", err))
		return err
	}

	var executionResults []map[string]interface{}
	var firstActionErr error
	for i, action := range actions {
		e.logger.Infow("Executing action", "rule_id", rule.ID, "action_index", i)

		err := action.Execute(ctx, incident, tenantID)
		result := map[string]interface{}{
			"action_index": i,
			"success":      err == nil,
			"error":        nil,
		}

		if err != nil {
			e.logger.Errorw("Failed to execute action", "error", err, "action_index", i)
			result["error"] = err.Error()
			if firstActionErr == nil {
				firstActionErr = err
			}
		}

		executionResults = append(executionResults, result)
	}

	// 更新规则执行状态
	outputData := map[string]interface{}{
		"conditions_met":   true,
		"actions_executed": len(actions),
		"results":          executionResults,
	}

	executionStatus := "completed"
	executionResult := "Rule executed successfully"
	if firstActionErr != nil {
		executionStatus = "failed"
		executionResult = "One or more rule actions failed"
	}
	err = e.updateExecutionStatus(ctx, execution.ID, tenantID, executionStatus, executionResult, outputData)
	if err != nil {
		e.logger.Errorw("Failed to update execution status", "error", err)
	}

	// 更新规则统计
	_, err = e.client.IncidentRule.Update().
		Where(incidentrule.IDEQ(rule.ID), incidentrule.TenantIDEQ(tenantID)).
		AddExecutionCount(1).
		SetLastExecutedAt(time.Now()).
		Save(ctx)
	if err != nil {
		e.logger.Errorw("Failed to update rule statistics", "error", err)
	}

	if firstActionErr != nil {
		return fmt.Errorf("incident rule action failed: %w", firstActionErr)
	}
	e.logger.Infow("Rule executed successfully", "rule_id", rule.ID, "incident_id", incident.ID)
	return nil
}

// ExecuteRulesForIncident 为特定事件执行所有适用规则
func (e *IncidentRuleEngine) ExecuteRulesForIncident(ctx context.Context, incidentID int, tenantID int) error {
	e.logger.Infow("Executing rules for incident", "incident_id", incidentID)

	// 获取事件
	incidentEntity, err := e.client.Incident.Query().
		Where(
			incidentpkg.IDEQ(incidentID),
			incidentTenantScope(tenantID),
		).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("incident not found")
		}
		return fmt.Errorf("failed to get incident: %w", err)
	}

	// 获取激活的规则
	rules, err := e.client.IncidentRule.Query().
		Where(
			incidentrule.TenantIDEQ(tenantID),
			incidentrule.IsActiveEQ(true),
		).
		All(ctx)
	if err != nil {
		e.logger.Errorw("Failed to get incident rules", "error", err)
		return fmt.Errorf("failed to get incident rules: %w", err)
	}

	// 执行每个规则
	var failedRules int
	for _, rule := range rules {
		err := e.ExecuteRule(ctx, rule, incidentEntity, tenantID)
		if err != nil {
			failedRules++
			e.logger.Errorw("Failed to execute rule", "error", err, "rule_id", rule.ID)
		}
	}
	if failedRules > 0 {
		return fmt.Errorf("%d incident rules failed", failedRules)
	}
	return nil
}

// ExecuteRulesForAllIncidents 为所有事件执行规则（批量处理）
func (e *IncidentRuleEngine) ExecuteRulesForAllIncidents(ctx context.Context, tenantID int) error {
	e.logger.Infow("Executing rules for all incidents", "tenant_id", tenantID)

	// 获取所有激活的规则
	rules, err := e.client.IncidentRule.Query().
		Where(
			incidentrule.TenantIDEQ(tenantID),
			incidentrule.IsActiveEQ(true),
		).
		All(ctx)
	if err != nil {
		e.logger.Errorw("Failed to get incident rules", "error", err)
		return fmt.Errorf("failed to get incident rules: %w", err)
	}

	// 获取所有需要处理的事件
	incidents, err := e.client.Incident.Query().
		Where(
			incidentTenantScope(tenantID, ticket.StatusIn("new", "acknowledged", "assigned", "triaged", "in_progress", "on_hold", "escalated")),
		).
		WithWorkItem(withIncidentWorkItemProjection).All(ctx)
	if err != nil {
		e.logger.Errorw("Failed to get incidents", "error", err)
		return fmt.Errorf("failed to get incidents: %w", err)
	}

	e.logger.Infow("Processing incidents", "count", len(incidents))

	// 为每个事件执行规则
	var failedExecutions int
	for _, incidentEntity := range incidents {
		for _, rule := range rules {
			err := e.ExecuteRule(ctx, rule, incidentEntity, tenantID)
			if err != nil {
				failedExecutions++
				e.logger.Errorw("Failed to execute rule", "error", err,
					"rule_id", rule.ID, "incident_id", incidentEntity.ID)
				// 继续执行其他规则，不中断
			}
		}
	}

	e.logger.Infow("Batch rule execution completed", "incidents_processed", len(incidents))
	if failedExecutions > 0 {
		return fmt.Errorf("%d incident rule executions failed", failedExecutions)
	}
	return nil
}

// parseConditions 解析规则条件
func (e *IncidentRuleEngine) parseConditions(conditions map[string]interface{}) ([]RuleCondition, error) {
	var parsedConditions []RuleCondition

	for conditionType, conditionData := range conditions {
		switch conditionType {
		case "priority":
			priorities, err := toStringSlice(conditionData)
			if err != nil {
				return nil, fmt.Errorf("invalid priority condition: %w", err)
			}
			parsedConditions = append(parsedConditions, &PriorityCondition{Priorities: priorities})
		case "severity":
			severities, err := toStringSlice(conditionData)
			if err != nil {
				return nil, fmt.Errorf("invalid severity condition: %w", err)
			}
			parsedConditions = append(parsedConditions, &SeverityCondition{Severities: severities})
		case "status":
			statuses, err := toStringSlice(conditionData)
			if err != nil {
				return nil, fmt.Errorf("invalid status condition: %w", err)
			}
			parsedConditions = append(parsedConditions, &StatusCondition{Statuses: statuses})
		case "category":
			categories, err := toStringSlice(conditionData)
			if err != nil {
				return nil, fmt.Errorf("invalid category condition: %w", err)
			}
			parsedConditions = append(parsedConditions, &CategoryCondition{Categories: categories})
		case "time":
			timeData, ok := conditionData.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("invalid time condition")
			}
			condition, err := e.parseTimeCondition(timeData)
			if err != nil {
				return nil, err
			}
			parsedConditions = append(parsedConditions, condition)
		default:
			return nil, fmt.Errorf("unsupported condition type: %s", conditionType)
		}
	}

	return parsedConditions, nil
}

// parseTimeCondition 解析时间条件
func (e *IncidentRuleEngine) parseTimeCondition(timeData map[string]interface{}) (*TimeCondition, error) {
	field, ok := timeData["field"].(string)
	if !ok {
		return nil, fmt.Errorf("time condition field is required")
	}

	operator, ok := timeData["operator"].(string)
	if !ok {
		return nil, fmt.Errorf("time condition operator is required")
	}

	durationStr, ok := timeData["duration"].(string)
	if !ok {
		return nil, fmt.Errorf("time condition duration is required")
	}

	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return nil, fmt.Errorf("invalid duration format: %w", err)
	}

	return &TimeCondition{
		Field:    field,
		Operator: operator,
		Duration: duration,
	}, nil
}

// parseActions 解析规则动作
func (e *IncidentRuleEngine) parseActions(actions []map[string]interface{}) ([]RuleAction, error) {
	var parsedActions []RuleAction

	for _, actionData := range actions {
		if _, present := actionData["optional"]; present {
			return nil, fmt.Errorf("optional incident rule actions are unsupported")
		}
		actionType, ok := actionData["type"].(string)
		if !ok {
			return nil, fmt.Errorf("action type is required")
		}

		switch actionType {
		case "escalate":
			action, err := e.parseEscalationAction(actionData)
			if err != nil {
				return nil, err
			}
			parsedActions = append(parsedActions, action)
		case "notify":
			action, err := e.parseNotificationAction(actionData)
			if err != nil {
				return nil, err
			}
			parsedActions = append(parsedActions, action)
		case "assign":
			action, err := e.parseAssignmentAction(actionData)
			if err != nil {
				return nil, err
			}
			parsedActions = append(parsedActions, action)
		case "change_status":
			action, err := e.parseStatusChangeAction(actionData)
			if err != nil {
				return nil, err
			}
			parsedActions = append(parsedActions, action)
		case "collect_metric":
			action, err := e.parseMetricCollectionAction(actionData)
			if err != nil {
				return nil, err
			}
			parsedActions = append(parsedActions, action)
		default:
			return nil, fmt.Errorf("unsupported action type: %s", actionType)
		}
	}

	return parsedActions, nil
}

// parseEscalationAction 解析升级动作
func (e *IncidentRuleEngine) parseEscalationAction(actionData map[string]interface{}) (*EscalationAction, error) {
	level, ok := toInt(actionData["level"])
	if !ok {
		return nil, fmt.Errorf("escalation level is required")
	}

	reason, _ := actionData["reason"].(string)
	notifyUsers, err := toIntSlice(actionData["notify_users"])
	if err != nil {
		return nil, err
	}
	autoAssign, _ := actionData["auto_assign"].(bool)
	if raw, exists := actionData["auto_assign"]; exists {
		if _, ok := raw.(bool); !ok {
			return nil, fmt.Errorf("auto_assign must be boolean")
		}
	}
	if autoAssign {
		return nil, fmt.Errorf("automatic escalation assignment is unsupported; configure an explicit assign action")
	}

	return &EscalationAction{
		Level:        level,
		Reason:       reason,
		NotifyUsers:  notifyUsers,
		AutoAssign:   autoAssign,
		client:       e.client,
		logger:       e.logger,
		alertCreator: e.alertCreator,
	}, nil
}

// parseNotificationAction 解析通知动作
func (e *IncidentRuleEngine) parseNotificationAction(actionData map[string]interface{}) (*NotificationAction, error) {
	channels, err := toStringSlice(actionData["channels"])
	if err != nil {
		return nil, err
	}
	recipients, err := toStringSlice(actionData["recipients"])
	if err != nil {
		return nil, err
	}
	message, _ := actionData["message"].(string)
	severity, _ := actionData["severity"].(string)

	if len(channels) == 0 {
		channels = []string{"email"}
	}
	if err := validateIncidentAlertChannels(channels); err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("configured notification recipients are required")
	}
	if message == "" {
		message = "事件需要关注"
	}
	if severity == "" {
		severity = "medium"
	}

	return &NotificationAction{
		Channels:     channels,
		Recipients:   recipients,
		Message:      message,
		Severity:     severity,
		client:       e.client,
		logger:       e.logger,
		alertCreator: e.alertCreator,
	}, nil
}

// parseAssignmentAction 解析分配动作
func (e *IncidentRuleEngine) parseAssignmentAction(actionData map[string]interface{}) (*AssignmentAction, error) {
	assigneeID, ok := toInt(actionData["assignee_id"])
	if !ok {
		return nil, fmt.Errorf("assignee_id is required for assignment action")
	}

	reason, _ := actionData["reason"].(string)

	return &AssignmentAction{
		AssigneeID: assigneeID,
		Reason:     reason,
		client:     e.client,
		logger:     e.logger,
	}, nil
}

// parseStatusChangeAction 解析状态变更动作
func (e *IncidentRuleEngine) parseStatusChangeAction(actionData map[string]interface{}) (*StatusChangeAction, error) {
	status, ok := actionData["status"].(string)
	if !ok {
		return nil, fmt.Errorf("status is required for status change action")
	}

	reason, _ := actionData["reason"].(string)

	return &StatusChangeAction{
		Status: status,
		Reason: reason,
		client: e.client,
		logger: e.logger,
	}, nil
}

// parseMetricCollectionAction 解析指标收集动作
func (e *IncidentRuleEngine) parseMetricCollectionAction(actionData map[string]interface{}) (*MetricCollectionAction, error) {
	metricType, ok := actionData["metric_type"].(string)
	if !ok {
		return nil, fmt.Errorf("metric_type is required for metric collection action")
	}

	metricName, ok := actionData["metric_name"].(string)
	if !ok {
		return nil, fmt.Errorf("metric_name is required for metric collection action")
	}

	metricValue, ok := actionData["metric_value"].(float64)
	if !ok {
		return nil, fmt.Errorf("metric_value is required for metric collection action")
	}

	unit, _ := actionData["unit"].(string)
	tags := toStringMap(actionData["tags"])

	return &MetricCollectionAction{
		MetricType:  metricType,
		MetricName:  metricName,
		MetricValue: metricValue,
		Unit:        unit,
		Tags:        tags,
		client:      e.client,
		logger:      e.logger,
	}, nil
}

func toInt(value interface{}) (int, bool) {
	n, err := bpmn.CallbackInteger(value)
	return n, err == nil
}

func toStringSlice(value interface{}) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case []string:
		return typed, nil
	case []interface{}:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected string array")
			}
			result = append(result, text)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("expected string array")
	}
}

func toIntSlice(value interface{}) ([]int, error) {
	items, ok := value.([]interface{})
	if !ok {
		if typed, ok := value.([]int); ok {
			return typed, nil
		}
		if value == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("expected integer array")
	}
	result := make([]int, 0, len(items))
	for _, item := range items {
		value, ok := toInt(item)
		if !ok {
			return nil, fmt.Errorf("expected integer array")
		}
		result = append(result, value)
	}
	return result, nil
}

func toStringMap(value interface{}) map[string]string {
	if typed, ok := value.(map[string]string); ok {
		return typed
	}
	result := make(map[string]string)
	if typed, ok := value.(map[string]interface{}); ok {
		for key, item := range typed {
			if text, ok := item.(string); ok {
				result[key] = text
			}
		}
	}
	return result
}

// updateExecutionStatus 更新执行状态
func (e *IncidentRuleEngine) updateExecutionStatus(ctx context.Context, executionID, tenantID int, status, result string, outputData ...map[string]interface{}) error {
	updateQuery := e.client.IncidentRuleExecution.UpdateOneID(executionID).
		Where(incidentruleexecution.TenantIDEQ(tenantID), incidentruleexecution.StatusEQ("running")).
		SetStatus(status).
		SetResult(result).
		SetCompletedAt(time.Now()).
		SetUpdatedAt(time.Now())

	if len(outputData) > 0 {
		updateQuery.SetOutputData(outputData[0])
	}

	_, err := updateQuery.Save(ctx)
	return err
}
