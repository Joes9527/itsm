package bpmn

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"

	"go.uber.org/zap"
)

// IncidentDomainServiceInterface 事件领域服务接口（避免 service/bpmn 反向 import service
// 包造成循环依赖，同 TicketStatusServiceInterface 的理由）
//
// EscalateIncidentLevel/ResolveIncidentForWorkflow/CloseIncidentForWorkflow/
// AcknowledgeIncidentForWorkflow/UpdateIncidentForWorkflow/CategorizeIncidentForWorkflow
// 是 Wave 2（WorkItem 迁移）新增：把本文件里 escalate/resolve/close/acknowledge/update/
// categorize 几个 BPMN 动作原来直接写 Ent 的代码收回到领域服务，理由见
// service/incident_service.go 对应实现前的注释。
type IncidentDomainServiceInterface interface {
	CreateIncident(ctx context.Context, req *dto.CreateIncidentRequest, tenantID, userID int) (*dto.IncidentResponse, error)
	AssignIncident(ctx context.Context, id int, assigneeID int, tenantID int) (*dto.IncidentResponse, error)
	UpdateStatus(ctx context.Context, id int, status string, tenantID int) (*dto.IncidentResponse, error)
	EscalateIncidentLevel(ctx context.Context, id, tenantID, level int) (*dto.IncidentResponse, error)
	ResolveIncidentForWorkflow(ctx context.Context, id, tenantID int, resolution string) (*dto.IncidentResponse, error)
	CloseIncidentForWorkflow(ctx context.Context, id, tenantID int, feedback string) (*dto.IncidentResponse, error)
	AcknowledgeIncidentForWorkflow(ctx context.Context, id, tenantID int) (*dto.IncidentResponse, error)
	UpdateIncidentForWorkflow(ctx context.Context, id, tenantID int, title, description, priority, severity, status string) (*dto.IncidentResponse, error)
	CategorizeIncidentForWorkflow(ctx context.Context, id, tenantID int, category, subcategory string) (*dto.IncidentResponse, error)
}

// IncidentServiceTaskHandler 事件服务任务处理器
type IncidentServiceTaskHandler struct {
	HandlerBase
	client          *ent.Client
	logger          *zap.SugaredLogger
	incidentService IncidentDomainServiceInterface
}

// NewIncidentServiceTaskHandler 创建事件处理器
func NewIncidentServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *IncidentServiceTaskHandler {
	return &IncidentServiceTaskHandler{
		client: client,
		logger: logger,
	}
}

// SetIncidentService 注入事件领域服务，由 bootstrap 在 IncidentService 构造完成后调用。
func (h *IncidentServiceTaskHandler) SetIncidentService(svc IncidentDomainServiceInterface) {
	h.incidentService = svc
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
		return h.createIncident(ctx, task, variables)
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
func (h *IncidentServiceTaskHandler) createIncident(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
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

	if h.incidentService == nil {
		return nil, fmt.Errorf("incident service 未注入，无法创建事件")
	}
	idempotencyKey, _ := variables["idempotency_key"].(string)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" && task != nil {
		idempotencyKey = fmt.Sprintf("bpmn-service-task-%d", task.ID)
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("创建事件需要稳定的 idempotency_key 或持久化流程任务 ID")
	}
	reporterID := GetIntFromVars(variables, "reporter_id")
	resp, err := h.incidentService.CreateIncident(ctx, &dto.CreateIncidentRequest{
		Title:       title,
		Description: description,
		Type:        incidentType,
		Priority:    priority,
		Severity:    severity,
		Metadata:    map[string]interface{}{"idempotency_key": idempotencyKey},
	}, tenantID, reporterID)
	if err != nil {
		return nil, fmt.Errorf("创建事件失败: %w", err)
	}

	h.logger.Infow("Incident created via BPMN", "incident_id", resp.ID, "title", title)

	return &dto.ServiceTaskResult{
		Success:    true,
		Message:    fmt.Sprintf("事件 %d 已创建", resp.ID),
		OutputVars: map[string]interface{}{"incident_id": resp.ID, "incident_number": resp.IncidentNumber},
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

	if h.incidentService == nil {
		return nil, fmt.Errorf("incident service 未注入，无法分配事件")
	}
	if _, err := h.incidentService.AssignIncident(ctx, incidentID, assigneeID, tenantID); err != nil {
		return nil, fmt.Errorf("分配事件失败: %w", err)
	}
	if _, err := h.incidentService.UpdateStatus(ctx, incidentID, common.IncidentStatusAssigned, tenantID); err != nil {
		return nil, fmt.Errorf("更新事件状态失败: %w", err)
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

	if h.incidentService == nil {
		return nil, fmt.Errorf("incident service 未注入，无法升级事件")
	}
	resp, err := h.incidentService.EscalateIncidentLevel(ctx, incidentID, tenantID, escalationLevel)
	if err != nil {
		return nil, fmt.Errorf("升级事件失败: %w", err)
	}

	h.logger.Infow("Incident escalated via BPMN", "incident_id", incidentID, "escalation_level", resp.EscalationLevel, "reason", reason)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("事件 %d 已升级到第 %d 级", incidentID, resp.EscalationLevel),
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

	if h.incidentService == nil {
		return nil, fmt.Errorf("incident service 未注入，无法解决事件")
	}
	if _, err := h.incidentService.ResolveIncidentForWorkflow(ctx, incidentID, tenantID, resolution); err != nil {
		return nil, fmt.Errorf("解决事件失败: %w", err)
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

	if h.incidentService == nil {
		return nil, fmt.Errorf("incident service 未注入，无法关闭事件")
	}
	if _, err := h.incidentService.CloseIncidentForWorkflow(ctx, incidentID, tenantID, feedback); err != nil {
		return nil, fmt.Errorf("关闭事件失败: %w", err)
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

	title, _ := variables["title"].(string)
	description, _ := variables["description"].(string)
	priority, _ := variables["priority"].(string)
	severity, _ := variables["severity"].(string)
	status, _ := variables["status"].(string)

	if h.incidentService == nil {
		return nil, fmt.Errorf("incident service 未注入，无法更新事件")
	}
	if _, err := h.incidentService.UpdateIncidentForWorkflow(ctx, incidentID, tenantID, title, description, priority, severity, status); err != nil {
		return nil, fmt.Errorf("更新事件失败: %w", err)
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

	if h.incidentService == nil {
		return nil, fmt.Errorf("incident service 未注入，无法确认事件")
	}
	if _, err := h.incidentService.AcknowledgeIncidentForWorkflow(ctx, incidentID, tenantID); err != nil {
		return nil, fmt.Errorf("确认事件失败: %w", err)
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

	if h.incidentService == nil {
		return nil, fmt.Errorf("incident service 未注入，无法分类事件")
	}
	if _, err := h.incidentService.CategorizeIncidentForWorkflow(ctx, incidentID, tenantID, category, subcategory); err != nil {
		return nil, fmt.Errorf("分类事件失败: %w", err)
	}

	h.logger.Infow("Incident categorized via BPMN", "incident_id", incidentID, "category", category, "subcategory", subcategory)

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("事件 %d 已分类: %s/%s", incidentID, category, subcategory),
	}, nil
}

// 确保 IncidentServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*IncidentServiceTaskHandler)(nil)
