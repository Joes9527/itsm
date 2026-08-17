package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"

	"go.uber.org/zap"
)

// IncidentServiceTaskHandler 事件服务任务处理器
type IncidentServiceTaskHandler struct {
	HandlerBase
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewIncidentServiceTaskHandler 创建事件处理器
func NewIncidentServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *IncidentServiceTaskHandler {
	return &IncidentServiceTaskHandler{
		client: client,
		logger: logger,
	}
}

// GetTaskType 返回任务类型
func (h *IncidentServiceTaskHandler) GetTaskType() string {
	return "incident_task"
}

// GetHandlerID 返回处理器标识
func (h *IncidentServiceTaskHandler) GetHandlerID() string {
	return "incident_service_handler"
}

// Execute 执行事件服务任务
func (h *IncidentServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "create_incident":
		return h.createIncident(ctx, variables)
	case "assign_incident":
		return h.assignIncident(ctx, variables)
	case "escalate_incident":
		return h.escalateIncident(ctx, variables)
	case "resolve_incident":
		return h.resolveIncident(ctx, variables)
	case "close_incident":
		return h.closeIncident(ctx, variables)
	case "update_incident":
		return h.updateIncident(ctx, variables)
	case "acknowledge_incident":
		return h.acknowledgeIncident(ctx, variables)
	case "categorize_incident":
		return h.categorizeIncident(ctx, variables)
	default:
		return &dto.ServiceTaskResult{Success: true, Message: "无操作执行"}, nil
	}
}

// Validate 验证配置
func (h *IncidentServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// createIncident 创建事件
func (h *IncidentServiceTaskHandler) createIncident(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	title, _ := variables["title"].(string)
	description, _ := variables["description"].(string)
	incidentType, _ := variables["type"].(string)
	priority, _ := variables["priority"].(string)
	severity, _ := variables["severity"].(string)
	// tenant_id 为 0 时 Ent 的 Positive() 校验会直接拒绝创建，天然 fail closed
	tenantID := GetTenantIDFromVars(ctx, variables)

	if title == "" {
		return nil, fmt.Errorf("事件标题不能为空")
	}

	incident, err := h.client.Incident.Create().
		SetTitle(title).
		SetDescription(description).
		SetType(incidentType).
		SetPriority(priority).
		SetSeverity(severity).
		SetStatus(common.IncidentStatusNew).
		SetReporterID(GetIntFromVars(variables, "reporter_id")).
		SetTenantID(tenantID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建事件失败: %w", err)
	}

	h.logger.Infow("Incident created via BPMN", "incident_id", incident.ID, "title", title)

	return &dto.ServiceTaskResult{
		Success:    true,
		Message:    fmt.Sprintf("事件 %d 已创建", incident.ID),
		OutputVars: map[string]interface{}{"incident_id": incident.ID, "incident_number": incident.IncidentNumber},
	}, nil
}

// assignIncident 分配事件。
//
// incident_emergency_flow.bpmn 的 Activity_AutoAssign 是起始事件之后的第一个 serviceTask
// （service_task_type=incident_task, action=assign_incident），而 Incident.assignee_id 在
// ent schema 里是 Optional——新建事件的 assignee_id 天生是 0。所以"自动分配时没有可用处理人"
// 是这个节点的正常空态，不是失败：这里必须返回成功的空操作。
//
// 反例（不要改回去）：对空处理人返回 error → handleElement 把错误往上抛 → StartProcess 整体
// 失败，而触发方（incident_service.go 的 fire-and-forget goroutine）只 Warnw 一句，
// 流程实例就永久卡在起始事件上，对任何用户都不可见。
//
// incident_id 无效则继续硬失败：那说明没人告诉这个节点该操作哪条事件，是真实的接线错误，
// 跟"暂时没有处理人"是两回事，必须报出来。
func (h *IncidentServiceTaskHandler) assignIncident(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	incidentID := GetIntFromVars(variables, "incident_id")
	assigneeID := GetIntFromVars(variables, "assignee_id")

	if incidentID <= 0 {
		return nil, fmt.Errorf("无效的事件ID")
	}

	if assigneeID <= 0 {
		h.logger.Warnw("BPMN 自动分配未取到可用处理人，按空态跳过（不改事件状态）",
			"incident_id", incidentID)
		return &dto.ServiceTaskResult{
			Success: true,
			Message: fmt.Sprintf("事件 %d 当前无可用处理人，跳过自动分配", incidentID),
		}, nil
	}

	// 分配是一次真实写入，且 incident_id 在 UserTask 回调路径上来自客户端提交的变量，
	// 必须带租户约束；租户未知时 fail closed。
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	updated, err := h.client.Incident.Update().
		Where(incident.ID(incidentID), incident.TenantID(tenantID)).
		SetAssigneeID(assigneeID).
		SetStatus(common.IncidentStatusAssigned).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("分配事件失败: %w", err)
	}
	if updated == 0 {
		return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", incidentID)
	}

	h.logger.Infow("Incident assigned via BPMN", "incident_id", incidentID, "assignee_id", assigneeID)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("事件 %d 已分配给用户 %d", incidentID, assigneeID),
	}, nil
}

// escalateIncident 升级事件
func (h *IncidentServiceTaskHandler) escalateIncident(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	incidentID := GetIntFromVars(variables, "incident_id")
	escalationLevel := GetIntFromVars(variables, "escalation_level")
	reason, _ := variables["escalation_reason"].(string)

	if incidentID <= 0 {
		return nil, fmt.Errorf("无效的事件ID")
	}

	// incident_id 在 UserTask 回调路径上来自客户端提交的变量，读写都必须带租户约束
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	// 获取当前升级级别（带租户约束）
	entity, err := h.client.Incident.Query().
		Where(incident.ID(incidentID), incident.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", incidentID)
		}
		return nil, fmt.Errorf("查询事件失败: %w", err)
	}

	newLevel := escalationLevel
	if newLevel <= 0 {
		newLevel = entity.EscalationLevel + 1
	}

	now := time.Now()
	if _, err := entity.Update().
		SetEscalationLevel(newLevel).
		SetEscalatedAt(now).
		SetStatus(common.IncidentStatusEscalated).
		Save(ctx); err != nil {
		return nil, fmt.Errorf("升级事件失败: %w", err)
	}

	h.logger.Infow("Incident escalated via BPMN", "incident_id", incidentID, "escalation_level", newLevel, "reason", reason)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("事件 %d 已升级到第 %d 级", incidentID, newLevel),
	}, nil
}

// resolveIncident 解决事件
func (h *IncidentServiceTaskHandler) resolveIncident(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	incidentID := GetIntFromVars(variables, "incident_id")
	resolution, _ := variables["resolution"].(string)

	if incidentID <= 0 {
		return nil, fmt.Errorf("无效的事件ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	updated, err := h.client.Incident.Update().
		Where(incident.ID(incidentID), incident.TenantID(tenantID)).
		SetStatus(common.IncidentStatusResolved).
		SetResolvedAt(now).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("解决事件失败: %w", err)
	}
	if updated == 0 {
		return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", incidentID)
	}

	h.logger.Infow("Incident resolved via BPMN", "incident_id", incidentID, "resolution", resolution)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("事件 %d 已解决: %s", incidentID, resolution),
	}, nil
}

// closeIncident 关闭事件
func (h *IncidentServiceTaskHandler) closeIncident(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	incidentID := GetIntFromVars(variables, "incident_id")
	feedback, _ := variables["feedback"].(string)

	if incidentID <= 0 {
		return nil, fmt.Errorf("无效的事件ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	updated, err := h.client.Incident.Update().
		Where(incident.ID(incidentID), incident.TenantID(tenantID)).
		SetStatus(common.IncidentStatusClosed).
		SetClosedAt(now).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("关闭事件失败: %w", err)
	}
	if updated == 0 {
		return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", incidentID)
	}

	h.logger.Infow("Incident closed via BPMN", "incident_id", incidentID, "feedback", feedback)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("事件 %d 已关闭", incidentID),
	}, nil
}

// updateIncident 更新事件
func (h *IncidentServiceTaskHandler) updateIncident(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	incidentID := GetIntFromVars(variables, "incident_id")

	if incidentID <= 0 {
		return nil, fmt.Errorf("无效的事件ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	updateQuery := h.client.Incident.Update().
		Where(incident.ID(incidentID), incident.TenantID(tenantID))

	if title, ok := variables["title"].(string); ok && title != "" {
		updateQuery.SetTitle(title)
	}
	if description, ok := variables["description"].(string); ok && description != "" {
		updateQuery.SetDescription(description)
	}
	if priority, ok := variables["priority"].(string); ok && priority != "" {
		updateQuery.SetPriority(priority)
	}
	if severity, ok := variables["severity"].(string); ok && severity != "" {
		updateQuery.SetSeverity(severity)
	}
	if status, ok := variables["status"].(string); ok && status != "" {
		updateQuery.SetStatus(status)
	}

	updated, err := updateQuery.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("更新事件失败: %w", err)
	}
	if updated == 0 {
		return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", incidentID)
	}

	h.logger.Infow("Incident updated via BPMN", "incident_id", incidentID)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("事件 %d 已更新", incidentID),
	}, nil
}

// acknowledgeIncident 确认事件
func (h *IncidentServiceTaskHandler) acknowledgeIncident(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	incidentID := GetIntFromVars(variables, "incident_id")

	if incidentID <= 0 {
		return nil, fmt.Errorf("无效的事件ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	updated, err := h.client.Incident.Update().
		Where(incident.ID(incidentID), incident.TenantID(tenantID)).
		SetStatus(common.IncidentStatusAcknowledged).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("确认事件失败: %w", err)
	}
	if updated == 0 {
		return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", incidentID)
	}

	h.logger.Infow("Incident acknowledged via BPMN", "incident_id", incidentID)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("事件 %d 已确认", incidentID),
	}, nil
}

// categorizeIncident 分类事件
func (h *IncidentServiceTaskHandler) categorizeIncident(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	incidentID := GetIntFromVars(variables, "incident_id")
	category, _ := variables["category"].(string)
	subcategory, _ := variables["subcategory"].(string)

	if incidentID <= 0 {
		return nil, fmt.Errorf("无效的事件ID")
	}

	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	updateQuery := h.client.Incident.Update().
		Where(incident.ID(incidentID), incident.TenantID(tenantID)).
		SetStatus(common.IncidentStatusTriaged)
	if category != "" {
		updateQuery.SetCategory(category)
	}
	if subcategory != "" {
		updateQuery.SetSubcategory(subcategory)
	}

	updated, err := updateQuery.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("分类事件失败: %w", err)
	}
	if updated == 0 {
		return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", incidentID)
	}

	h.logger.Infow("Incident categorized via BPMN", "incident_id", incidentID, "category", category, "subcategory", subcategory)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("事件 %d 已分类: %s/%s", incidentID, category, subcategory),
	}, nil
}

// 确保 IncidentServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*IncidentServiceTaskHandler)(nil)
