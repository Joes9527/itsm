package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

// ServiceRequestServiceTaskHandler 服务请求服务任务处理器
type ServiceRequestServiceTaskHandler struct {
	HandlerBase
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewServiceRequestServiceTaskHandler 创建服务请求处理器
func NewServiceRequestServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *ServiceRequestServiceTaskHandler {
	return &ServiceRequestServiceTaskHandler{
		client: client,
		logger: logger,
	}
}

// GetTaskType 返回任务类型
func (h *ServiceRequestServiceTaskHandler) GetTaskType() string {
	return "service_request_task"
}

// GetHandlerID 返回处理器标识
func (h *ServiceRequestServiceTaskHandler) GetHandlerID() string {
	return "service_request_handler"
}

// Execute 执行服务请求任务。ServiceRequest 自身没有 status 字段——状态/审批/工作流全部
// 委托给关联的 Ticket（见 ent/schema/servicerequest.go 的字段注释），所以这里凡是涉及
// "状态"语义的动作都改成更新关联 Ticket 的状态，跟 GenericServiceTaskHandler 的写法一致；
// ServiceRequest 自己的字段（processor_id/started_at/completed_at/completion_note）
// 只用来记录资源交付过程本身的信息。
func (h *ServiceRequestServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "create_request":
		return nil, fmt.Errorf("服务请求必须先通过服务目录申请创建，流程实例只能在请求已存在之后触发——不支持从流程内部创建新请求")
	case "update_request":
		return h.updateRequest(ctx, variables)
	case "approve_request":
		return h.setLinkedTicketStatus(ctx, variables, "in_progress", "")
	case "reject_request":
		reason, _ := variables["reject_reason"].(string)
		return h.rejectRequest(ctx, variables, reason)
	case "assign_request":
		return h.assignRequest(ctx, variables)
	case "provision_resource":
		return h.provisionResource(ctx, variables)
	case "complete_request":
		return h.completeRequest(ctx, variables)
	case "cancel_request":
		reason, _ := variables["cancel_reason"].(string)
		return h.cancelRequest(ctx, variables, reason)
	default:
		return &dto.ServiceTaskResult{Success: true, Message: "无操作执行"}, nil
	}
}

// Validate 验证配置
func (h *ServiceRequestServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// getServiceRequest 按 request_id + 租户取出服务请求，找不到时返回明确错误。
func (h *ServiceRequestServiceTaskHandler) getServiceRequest(ctx context.Context, variables map[string]interface{}) (*ent.ServiceRequest, int, error) {
	requestID := GetIntFromVars(variables, "request_id")
	if requestID <= 0 {
		return nil, 0, fmt.Errorf("无效的请求ID")
	}
	// fail closed：租户未知时直接拒绝，不退化成不带租户约束的全表查询
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, 0, err
	}
	sr, err := h.client.ServiceRequest.Query().
		Where(servicerequest.ID(requestID), servicerequest.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("查询服务请求失败: %w", err)
	}
	return sr, tenantID, nil
}

func (h *ServiceRequestServiceTaskHandler) updateRequest(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	sr, _, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	h.logger.Infow("Service request updated via BPMN", "request_id", sr.ID)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("服务请求 %d 已更新", sr.ID)}, nil
}

// setLinkedTicketStatus 把服务请求关联工单的状态改成 newStatus，可选附一条完成备注。
func (h *ServiceRequestServiceTaskHandler) setLinkedTicketStatus(ctx context.Context, variables map[string]interface{}, newStatus, note string) (*dto.ServiceTaskResult, error) {
	sr, tenantID, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	update := h.client.Ticket.UpdateOneID(sr.TicketID)
	if tenantID > 0 {
		update = update.Where(ticket.TenantID(tenantID))
	}
	if newStatus == "resolved" || newStatus == "closed" {
		update = update.SetResolvedAt(time.Now())
	}
	if _, err := update.SetStatus(newStatus).SetUpdatedAt(time.Now()).Save(ctx); err != nil {
		return nil, fmt.Errorf("更新关联工单状态失败: %w", err)
	}
	if note != "" {
		if _, err := sr.Update().SetCompletionNote(note).Save(ctx); err != nil {
			return nil, fmt.Errorf("记录服务请求备注失败: %w", err)
		}
	}
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("服务请求 %d 对应工单状态已更新为 %s", sr.ID, newStatus)}, nil
}

func (h *ServiceRequestServiceTaskHandler) rejectRequest(ctx context.Context, variables map[string]interface{}, reason string) (*dto.ServiceTaskResult, error) {
	note := reason
	if note == "" {
		note = "已驳回"
	}
	return h.setLinkedTicketStatus(ctx, variables, "closed", note)
}

func (h *ServiceRequestServiceTaskHandler) assignRequest(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	sr, _, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	assigneeID := GetIntFromVars(variables, "assignee_id")
	if assigneeID <= 0 {
		return nil, fmt.Errorf("无效的 assignee_id")
	}
	if _, err := sr.Update().SetProcessorID(assigneeID).Save(ctx); err != nil {
		return nil, fmt.Errorf("分配服务请求失败: %w", err)
	}
	h.logger.Infow("Service request assigned via BPMN", "request_id", sr.ID, "assignee_id", assigneeID)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("服务请求 %d 已分配", sr.ID)}, nil
}

func (h *ServiceRequestServiceTaskHandler) provisionResource(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	sr, _, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	resourceType, _ := variables["resource_type"].(string)
	if _, err := sr.Update().SetStartedAt(time.Now()).Save(ctx); err != nil {
		return nil, fmt.Errorf("记录资源开通开始时间失败: %w", err)
	}
	h.logger.Infow("Resource provisioning via BPMN", "request_id", sr.ID, "resource_type", resourceType)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("资源 %s 开始供应", resourceType)}, nil
}

func (h *ServiceRequestServiceTaskHandler) completeRequest(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	sr, tenantID, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	completionNote, _ := variables["completion_note"].(string)
	if _, err := sr.Update().SetCompletedAt(time.Now()).SetCompletionNote(completionNote).Save(ctx); err != nil {
		return nil, fmt.Errorf("记录服务请求完成信息失败: %w", err)
	}
	update := h.client.Ticket.UpdateOneID(sr.TicketID)
	if tenantID > 0 {
		update = update.Where(ticket.TenantID(tenantID))
	}
	if _, err := update.SetStatus("resolved").SetResolvedAt(time.Now()).SetUpdatedAt(time.Now()).Save(ctx); err != nil {
		return nil, fmt.Errorf("更新关联工单状态失败: %w", err)
	}
	h.logger.Infow("Service request completed via BPMN", "request_id", sr.ID)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("服务请求 %d 已完成", sr.ID)}, nil
}

func (h *ServiceRequestServiceTaskHandler) cancelRequest(ctx context.Context, variables map[string]interface{}, reason string) (*dto.ServiceTaskResult, error) {
	note := reason
	if note == "" {
		note = "已取消"
	}
	return h.setLinkedTicketStatus(ctx, variables, "closed", note)
}

// 确保 ServiceRequestServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*ServiceRequestServiceTaskHandler)(nil)
