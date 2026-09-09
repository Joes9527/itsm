package bpmn

import (
	"context"
	"fmt"

	"itsm-backend/dto"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"

	"go.uber.org/zap"
)

// IncidentDomainServiceInterface 事件领域服务接口（避免 service/bpmn 反向 import service
// 包造成循环依赖，同 TicketStatusServiceInterface 的理由）
type IncidentDomainServiceInterface interface {
	ApplyIncidentCommand(context.Context, dto.IncidentCommand) (workitemmutation.Result, error)
	AssignIncidentForWorkflow(ctx context.Context, id int, assigneeID int, tenantID int) (*dto.IncidentMutationOutcome, error)
	EscalateIncidentLevel(ctx context.Context, id, tenantID, level int) (*dto.IncidentMutationOutcome, error)
	UpdateIncident(ctx context.Context, id int, req *dto.UpdateIncidentRequest, tenantID int) (*dto.IncidentResponse, error)
	UpdateClassification(ctx context.Context, id, tenantID, version int, category, subcategory string) (*dto.IncidentResponse, error)
}

// IncidentServiceTaskHandler 事件服务任务处理器
type IncidentServiceTaskHandler struct {
	creationApplication creation.Application
	creationDirectory   *ent.Client
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
func (h *IncidentServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*CallbackEffect, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "create_incident":
		return h.createIncident(ctx, variables)
	case "assign_incident":
		return h.assignIncident(ctx, variables)
	case "escalate_incident":
		return h.escalateIncident(ctx, variables)
	case "resolve_incident":
		return h.applyLifecycle(ctx, "resolve_incident")
	case "start_incident":
		return h.applyLifecycle(ctx, "start_incident")
	case "close_incident":
		return h.applyLifecycle(ctx, "close_incident")
	case "reopen_incident":
		return h.applyLifecycle(ctx, "reopen_incident")
	case "update_incident":
		return h.updateIncident(ctx, variables)
	case "acknowledge_incident":
		return h.applyLifecycle(ctx, "acknowledge_incident")
	case "categorize_incident":
		return h.categorizeIncident(ctx, variables)
	default:
		return BlockedEffect(CallbackBlockHandlerContract, "unsupported incident callback action"), nil
	}
}

// createIncident 创建事件
func (h *IncidentServiceTaskHandler) SetCreationApplication(app creation.Application, directory *ent.Client) {
	h.creationApplication = app
	h.creationDirectory = directory
}
func (h *IncidentServiceTaskHandler) createIncident(ctx context.Context, _ map[string]interface{}) (*CallbackEffect, error) {
	return executeWorkItemCreation(ctx, h.client, h.creationDirectory, h.creationApplication, h.GetHandlerID(), "create_incident", creation.RecordClassIncident)
}

// assignIncident 分配事件。
//
// incident_emergency_flow.bpmn 的 Activity_AutoAssign 是起始事件之后的第一个 serviceTask
// （service_task_type=incident_task, action=assign_incident），而 Incident.assignee_id 在
// ent schema 里是 Optional。没有可用处理人时 handler 不得虚报成功：它返回 typed blocked，
// 由 engine 根据持久化的 definition-declared optional 快照决定是否记录 optional skip。
//
// incident_id 无效则继续硬失败：那说明没人告诉这个节点该操作哪条事件，是真实的接线错误，
// 跟"暂时没有处理人"是两回事，必须报出来。
func (h *IncidentServiceTaskHandler) assignIncident(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
	incidentID := GetIntFromVars(variables, "incident_id")
	assigneeID := GetIntFromVars(variables, "assignee_id")

	if incidentID <= 0 {
		return nil, fmt.Errorf("无效的事件ID")
	}

	if assigneeID <= 0 {
		h.logger.Warnw("BPMN 自动分配未取到可用处理人，按空态跳过（不改事件状态）",
			"incident_id", incidentID)
		return BlockedEffect(CallbackBlockTargetMissing,
			fmt.Sprintf("事件 %d 当前无可用处理人", incidentID)), nil
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
	outcome, err := h.incidentService.AssignIncidentForWorkflow(ctx, incidentID, assigneeID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("分配事件失败: %w", err)
	}

	h.logger.Infow("Incident assigned via BPMN", "incident_id", incidentID, "assignee_id", assigneeID)

	return incidentMutationEffect(outcome, fmt.Sprintf("事件 %d 已分配给用户 %d", incidentID, assigneeID))
}

// escalateIncident 升级事件
func (h *IncidentServiceTaskHandler) escalateIncident(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
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
	outcome, err := h.incidentService.EscalateIncidentLevel(ctx, incidentID, tenantID, escalationLevel)
	if err != nil {
		return nil, fmt.Errorf("升级事件失败: %w", err)
	}
	if outcome == nil || outcome.Incident == nil {
		return nil, fmt.Errorf("升级事件失败: incident domain returned an empty outcome")
	}

	h.logger.Infow("Incident escalated via BPMN", "incident_id", incidentID, "escalation_level", outcome.Incident.EscalationLevel, "reason", reason)

	return incidentMutationEffect(outcome, fmt.Sprintf("事件 %d 已升级到第 %d 级", incidentID, outcome.Incident.EscalationLevel))
}

// updateIncident 更新事件
func (h *IncidentServiceTaskHandler) updateIncident(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
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
	if status != "" {
		return BlockedEffect(CallbackBlockHandlerContract, "status changes require Incident lifecycle commands"), nil
	}
	version := GetIntFromVars(variables, "version")
	req := &dto.UpdateIncidentRequest{Version: version}
	if title != "" {
		req.Title = &title
	}
	if description != "" {
		req.Description = &description
	}
	if priority != "" {
		req.Priority = &priority
	}
	if severity != "" {
		req.Severity = &severity
	}
	updated, err := h.incidentService.UpdateIncident(ctx, incidentID, req, tenantID)
	if err != nil {
		return nil, fmt.Errorf("更新事件失败: %w", err)
	}

	h.logger.Infow("Incident updated via BPMN", "incident_id", incidentID)

	return incidentMutationEffect(&dto.IncidentMutationOutcome{Incident: updated, Applied: true}, fmt.Sprintf("事件 %d 已更新", incidentID))
}

// categorizeIncident 分类事件
func (h *IncidentServiceTaskHandler) categorizeIncident(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
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
	updated, err := h.incidentService.UpdateClassification(ctx, incidentID, tenantID, GetIntFromVars(variables, "version"), category, subcategory)
	if err != nil {
		return nil, fmt.Errorf("分类事件失败: %w", err)
	}

	h.logger.Infow("Incident categorized via BPMN", "incident_id", incidentID, "category", category, "subcategory", subcategory)

	return incidentMutationEffect(&dto.IncidentMutationOutcome{Incident: updated, Applied: true}, fmt.Sprintf("事件 %d 已分类: %s/%s", incidentID, category, subcategory))
}

func incidentMutationEffect(outcome *dto.IncidentMutationOutcome, message string) (*CallbackEffect, error) {
	if outcome == nil || outcome.Incident == nil {
		return nil, fmt.Errorf("incident domain returned an empty mutation outcome")
	}
	if !outcome.Applied {
		return IdempotentEffect(message, nil), nil
	}
	return AppliedEffect(message, nil), nil
}

// 确保 IncidentServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*IncidentServiceTaskHandler)(nil)
