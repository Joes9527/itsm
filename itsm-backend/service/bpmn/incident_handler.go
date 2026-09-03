package bpmn

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"

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
	AssignIncidentForWorkflow(ctx context.Context, id int, assigneeID int, tenantID int) (*dto.IncidentMutationOutcome, error)
	EscalateIncidentLevel(ctx context.Context, id, tenantID, level int) (*dto.IncidentMutationOutcome, error)
	ResolveIncidentForWorkflow(ctx context.Context, id, tenantID int, resolution string) (*dto.IncidentMutationOutcome, error)
	CloseIncidentForWorkflow(ctx context.Context, id, tenantID int, feedback string) (*dto.IncidentMutationOutcome, error)
	AcknowledgeIncidentForWorkflow(ctx context.Context, id, tenantID int) (*dto.IncidentMutationOutcome, error)
	UpdateIncidentForWorkflow(ctx context.Context, id, tenantID int, title, description, priority, severity, status string) (*dto.IncidentMutationOutcome, error)
	CategorizeIncidentForWorkflow(ctx context.Context, id, tenantID int, category, subcategory string) (*dto.IncidentMutationOutcome, error)
}

const IntakeKindIncident = "incident"

type IntakeIdentity struct {
	TenantID    int
	ActorID     int
	RequesterID int
	Role        string
	Channel     string
}

type IntakeIncidentInput struct {
	Type             string
	Severity         string
	ExplicitPriority string
}

type IntakeCreateWorkItemCommand struct {
	IdempotencyKey string
	IntakeKind     string
	Title          string
	Description    string
	Incident       *IntakeIncidentInput
}

type IntakeProfessionalReference struct {
	Type string
	ID   int
}

type IntakeCreateWorkItemResult struct {
	WorkItemID            int
	Number                string
	ProfessionalReference IntakeProfessionalReference
}

type intakeCreator interface {
	Create(ctx context.Context, identity IntakeIdentity, command IntakeCreateWorkItemCommand) (*IntakeCreateWorkItemResult, error)
}

// IncidentServiceTaskHandler 事件服务任务处理器
type IncidentServiceTaskHandler struct {
	HandlerBase
	client          *ent.Client
	logger          *zap.SugaredLogger
	incidentService IncidentDomainServiceInterface
	intakeService   intakeCreator
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

// SetIntakeService 注入 Unified Intake 应用服务，由 bootstrap 在其构造完成后调用。
func (h *IncidentServiceTaskHandler) SetIntakeService(svc intakeCreator) {
	h.intakeService = svc
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
		return BlockedEffect(CallbackBlockHandlerContract, "unsupported incident callback action"), nil
	}
}

// createIncident 创建事件
func (h *IncidentServiceTaskHandler) createIncident(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*CallbackEffect, error) {
	title, _ := variables["title"].(string)
	if title == "" {
		return nil, fmt.Errorf("事件标题不能为空")
	}
	description, _ := variables["description"].(string)
	incidentType, _ := variables["type"].(string)
	priority, _ := variables["priority"].(string)
	severity, _ := variables["severity"].(string)
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	if h.intakeService == nil {
		return nil, fmt.Errorf("intake service 未注入，无法创建事件")
	}
	reporterID := GetIntFromVars(variables, "reporter_id")
	if reporterID <= 0 {
		return nil, fmt.Errorf("reporter_id 缺失，无法确定创建人")
	}
	idempotencyKey, err := bpmnCreateIncidentIdempotencyKey(ctx, task)
	if err != nil {
		return nil, err
	}
	// Identity.Role is a real RBAC role name looked up by handlers/intake's
	// Resolver (authorization.HasResourcePermission(client, identity.Role, ...)),
	// not a free-text label -- it must be the reporter's actual role, the same
	// way controller/incident_controller.go's HTTP CreateIncident passes the
	// authenticated caller's real ctx.GetString("role"). Fail closed if we
	// cannot resolve it rather than fabricate a role string that either always
	// gets PermissionDenied (if it matches no real role) or silently grants
	// the wrong privilege level (if it happens to collide with one).
	reporterRole, err := h.resolveReporterRole(ctx, reporterID, tenantID)
	if err != nil {
		return nil, err
	}
	result, err := h.intakeService.Create(ctx, IntakeIdentity{
		TenantID:    tenantID,
		ActorID:     reporterID,
		RequesterID: reporterID,
		Role:        reporterRole,
		Channel:     "bpmn",
	}, IntakeCreateWorkItemCommand{
		IdempotencyKey: idempotencyKey,
		IntakeKind:     IntakeKindIncident,
		Title:          title,
		Description:    description,
		Incident: &IntakeIncidentInput{
			Type:             incidentType,
			Severity:         severity,
			ExplicitPriority: priority,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("创建事件失败: %w", err)
	}

	// CreateWorkItemResult.Number is tickets.ticket_number (TKT-...), not the
	// incident's own incidents.incident_number (INC-...) -- handlers/intake's
	// Service always populates Number from the shared WorkItem row. Read the
	// incident back the same way controller/incident_controller.go's
	// incidentCreateReader does (by ProfessionalReference.ID, tenant-scoped)
	// to surface the real incident_number instead of conflating the two
	// distinct identifiers.
	incidentNumber, err := h.readCreatedIncidentNumber(ctx, result.ProfessionalReference.ID, tenantID)
	if err != nil {
		h.logger.Errorw("Incident created but reading back incident_number failed", "error", err, "incident_id", result.ProfessionalReference.ID)
		return nil, fmt.Errorf("事件已创建但读取事件编号失败: %w", err)
	}

	h.logger.Infow("Incident created via BPMN", "work_item_id", result.WorkItemID, "title", title)

	return &CallbackEffect{Status: CallbackEffectApplied,
		Message:    fmt.Sprintf("事件 %d 已创建", result.ProfessionalReference.ID),
		OutputVars: map[string]interface{}{"incident_id": result.ProfessionalReference.ID, "incident_number": incidentNumber},
	}, nil
}

// resolveReporterRole 查询 reporter_id 在当前租户下的真实 RBAC 角色名——
// Identity.Role 被 handlers/intake/resolver.go 的 Resolve 直接传给
// authorization.HasResourcePermission 做权限判定，不是展示用的标签，因此必须
// 是 authorization/rbac.go 里真实存在的角色（如 end_user/msp_tech/...），
// 不能硬编码一个虚构值。
func (h *IncidentServiceTaskHandler) resolveReporterRole(ctx context.Context, reporterID, tenantID int) (string, error) {
	if h.client == nil {
		return "", fmt.Errorf("db client 未注入，无法确定创建人角色")
	}
	reporter, err := h.client.User.Query().
		Where(user.ID(reporterID), user.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", fmt.Errorf("reporter_id=%d 在租户 %d 下不存在，无法确定创建人角色", reporterID, tenantID)
		}
		return "", fmt.Errorf("查询创建人角色失败: %w", err)
	}
	return reporter.Role, nil
}

// readCreatedIncidentNumber 按 Intake 创建结果里的 ProfessionalReference.ID
// 租户范围内读回刚创建的 Incident，取其真实 incident_number（INC-...）。
func (h *IncidentServiceTaskHandler) readCreatedIncidentNumber(ctx context.Context, incidentID, tenantID int) (string, error) {
	if h.client == nil {
		return "", fmt.Errorf("db client 未注入，无法读取事件编号")
	}
	entity, err := h.client.Incident.Query().
		Where(incident.ID(incidentID), incident.HasWorkItemWith(ticket.TenantID(tenantID))).
		Only(ctx)
	if err != nil {
		return "", fmt.Errorf("读取事件 %d 失败: %w", incidentID, err)
	}
	return entity.IncidentNumber, nil
}

func bpmnCreateIncidentIdempotencyKey(ctx context.Context, task *ent.ProcessTask) (string, error) {
	if key, ok := BPMNCallbackExecutionKey(ctx); ok && key != "" {
		return "bpmn-create-incident:" + key, nil
	}
	if task == nil || strings.TrimSpace(task.TaskID) == "" {
		return "", fmt.Errorf("无法确定幂等键：既没有持久化回调执行标识，也没有关联的 ProcessTask")
	}
	return "bpmn-create-incident:" + strings.TrimSpace(task.TaskID), nil
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

// resolveIncident 解决事件
func (h *IncidentServiceTaskHandler) resolveIncident(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
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
	outcome, err := h.incidentService.ResolveIncidentForWorkflow(ctx, incidentID, tenantID, resolution)
	if err != nil {
		return nil, fmt.Errorf("解决事件失败: %w", err)
	}

	h.logger.Infow("Incident resolved via BPMN", "incident_id", incidentID, "resolution", resolution)

	return incidentMutationEffect(outcome, fmt.Sprintf("事件 %d 已解决: %s", incidentID, resolution))
}

// closeIncident 关闭事件
func (h *IncidentServiceTaskHandler) closeIncident(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
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
	outcome, err := h.incidentService.CloseIncidentForWorkflow(ctx, incidentID, tenantID, feedback)
	if err != nil {
		return nil, fmt.Errorf("关闭事件失败: %w", err)
	}

	h.logger.Infow("Incident closed via BPMN", "incident_id", incidentID, "feedback", feedback)

	return incidentMutationEffect(outcome, fmt.Sprintf("事件 %d 已关闭", incidentID))
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
	outcome, err := h.incidentService.UpdateIncidentForWorkflow(ctx, incidentID, tenantID, title, description, priority, severity, status)
	if err != nil {
		return nil, fmt.Errorf("更新事件失败: %w", err)
	}

	h.logger.Infow("Incident updated via BPMN", "incident_id", incidentID)

	return incidentMutationEffect(outcome, fmt.Sprintf("事件 %d 已更新", incidentID))
}

// acknowledgeIncident 确认事件
func (h *IncidentServiceTaskHandler) acknowledgeIncident(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
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
	outcome, err := h.incidentService.AcknowledgeIncidentForWorkflow(ctx, incidentID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("确认事件失败: %w", err)
	}

	h.logger.Infow("Incident acknowledged via BPMN", "incident_id", incidentID)

	return incidentMutationEffect(outcome, fmt.Sprintf("事件 %d 已确认", incidentID))
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
	outcome, err := h.incidentService.CategorizeIncidentForWorkflow(ctx, incidentID, tenantID, category, subcategory)
	if err != nil {
		return nil, fmt.Errorf("分类事件失败: %w", err)
	}

	h.logger.Infow("Incident categorized via BPMN", "incident_id", incidentID, "category", category, "subcategory", subcategory)

	return incidentMutationEffect(outcome, fmt.Sprintf("事件 %d 已分类: %s/%s", incidentID, category, subcategory))
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
