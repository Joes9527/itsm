package bpmn

import (
	"context"
	"fmt"
	"reflect"
	"time"

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
func (h *ServiceRequestServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*CallbackEffect, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "create_request":
		return BlockedEffect(CallbackBlockHandlerContract, "service requests are created before BPMN starts"), nil
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
		return nil, fmt.Errorf("不支持的服务请求回调动作")
	}
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

func (h *ServiceRequestServiceTaskHandler) updateRequest(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
	sr, _, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}

	// 真实写入：只更新纯表单元数据字段（无状态语义），状态/审批/工作流语义
	// 仍委托给关联工单（见类型注释）。此前是"加载+打日志假装成功"的空实现。
	update := sr.Update()
	changed := false
	if formData, ok := variables["form_data"].(map[string]interface{}); ok && len(formData) > 0 {
		if !reflect.DeepEqual(sr.FormData, formData) {
			update.SetFormData(formData)
			changed = true
		}
	}
	if costCenter, ok := variables["cost_center"].(string); ok && costCenter != "" {
		if sr.CostCenter != costCenter {
			update.SetCostCenter(costCenter)
			changed = true
		}
	}
	if dataClass, ok := variables["data_classification"].(string); ok && dataClass != "" {
		if sr.DataClassification != dataClass {
			update.SetDataClassification(dataClass)
			changed = true
		}
	}
	if needsPublicIP, ok := variables["needs_public_ip"].(bool); ok {
		if sr.NeedsPublicIP != needsPublicIP {
			update.SetNeedsPublicIP(needsPublicIP)
			changed = true
		}
	}
	if whitelist, ok := variables["source_ip_whitelist"].([]interface{}); ok {
		ips := make([]string, 0, len(whitelist))
		for _, item := range whitelist {
			if s, ok := item.(string); ok {
				ips = append(ips, s)
			}
		}
		if !reflect.DeepEqual(sr.SourceIPWhitelist, ips) {
			update.SetSourceIPWhitelist(ips)
			changed = true
		}
	} else if ips, ok := variables["source_ip_whitelist"].([]string); ok && !reflect.DeepEqual(sr.SourceIPWhitelist, ips) {
		update.SetSourceIPWhitelist(ips)
		changed = true
	}
	if expireAtStr, ok := variables["expire_at"].(string); ok && expireAtStr != "" {
		if expireAt, parseErr := time.Parse(time.RFC3339, expireAtStr); parseErr == nil {
			if !sr.ExpireAt.Equal(expireAt) {
				update.SetExpireAt(expireAt)
				changed = true
			}
		}
	}
	if complianceAck, ok := variables["compliance_ack"].(bool); ok {
		if sr.ComplianceAck != complianceAck {
			update.SetComplianceAck(complianceAck)
			changed = true
		}
	}
	if !changed {
		return &CallbackEffect{Status: CallbackEffectApplied, Message: fmt.Sprintf("服务请求 %d 已是目标表单状态", sr.ID)}, nil
	}
	if _, err := update.Save(ctx); err != nil {
		return nil, fmt.Errorf("更新服务请求失败: %w", err)
	}
	h.logger.Infow("Service request updated via BPMN", "request_id", sr.ID)
	return &CallbackEffect{Status: CallbackEffectApplied, Message: fmt.Sprintf("服务请求 %d 已更新", sr.ID)}, nil
}

// setLinkedTicketStatus 把服务请求关联工单的状态改成 newStatus，可选附一条完成备注。
// 先读当前状态做转换校验（带租户约束），非法转换明确报错，不再无条件覆盖关联工单。
func (h *ServiceRequestServiceTaskHandler) setLinkedTicketStatus(ctx context.Context, variables map[string]interface{}, newStatus, note string) (*CallbackEffect, error) {
	sr, tenantID, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}

	current, err := h.client.Ticket.Query().
		Where(ticket.ID(sr.TicketID), ticket.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("关联工单 %d 不存在或不属于当前租户", sr.TicketID)
		}
		return nil, fmt.Errorf("查询关联工单失败: %w", err)
	}
	if !isValidLinkedTicketStatusTransition(current.Status, newStatus) {
		return nil, fmt.Errorf("非法的关联工单状态转换: %s -> %s", current.Status, newStatus)
	}
	if current.Status == newStatus {
		if note != "" && sr.CompletionNote != note {
			if _, err := sr.Update().SetCompletionNote(note).Save(ctx); err != nil {
				return nil, fmt.Errorf("记录服务请求备注失败: %w", err)
			}
		}
		return &CallbackEffect{Status: CallbackEffectApplied, Message: fmt.Sprintf("服务请求 %d 对应工单已处于 %s", sr.ID, newStatus)}, nil
	}

	update := current.Update()
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
	return &CallbackEffect{Status: CallbackEffectApplied, Message: fmt.Sprintf("服务请求 %d 对应工单状态已更新为 %s", sr.ID, newStatus)}, nil
}

func (h *ServiceRequestServiceTaskHandler) rejectRequest(ctx context.Context, variables map[string]interface{}, reason string) (*CallbackEffect, error) {
	note := reason
	if note == "" {
		note = "已驳回"
	}
	return h.setLinkedTicketStatus(ctx, variables, "closed", note)
}

func (h *ServiceRequestServiceTaskHandler) assignRequest(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
	sr, _, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	assigneeID := GetIntFromVars(variables, "assignee_id")
	if assigneeID <= 0 {
		return nil, fmt.Errorf("无效的 assignee_id")
	}
	if sr.ProcessorID == assigneeID {
		return &CallbackEffect{Status: CallbackEffectApplied, Message: fmt.Sprintf("服务请求 %d 已分配", sr.ID)}, nil
	}
	if _, err := sr.Update().SetProcessorID(assigneeID).Save(ctx); err != nil {
		return nil, fmt.Errorf("分配服务请求失败: %w", err)
	}
	h.logger.Infow("Service request assigned via BPMN", "request_id", sr.ID, "assignee_id", assigneeID)
	return &CallbackEffect{Status: CallbackEffectApplied, Message: fmt.Sprintf("服务请求 %d 已分配", sr.ID)}, nil
}

func (h *ServiceRequestServiceTaskHandler) provisionResource(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
	sr, _, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}
	resourceType, _ := variables["resource_type"].(string)
	if !sr.StartedAt.IsZero() {
		return &CallbackEffect{Status: CallbackEffectApplied, Message: fmt.Sprintf("资源 %s 已开始供应", resourceType)}, nil
	}
	if _, err := sr.Update().SetStartedAt(time.Now()).Save(ctx); err != nil {
		return nil, fmt.Errorf("记录资源开通开始时间失败: %w", err)
	}
	h.logger.Infow("Resource provisioning via BPMN", "request_id", sr.ID, "resource_type", resourceType)
	return &CallbackEffect{Status: CallbackEffectApplied, Message: fmt.Sprintf("资源 %s 开始供应", resourceType)}, nil
}

func (h *ServiceRequestServiceTaskHandler) completeRequest(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
	sr, tenantID, err := h.getServiceRequest(ctx, variables)
	if err != nil {
		return nil, err
	}

	current, err := h.client.Ticket.Query().
		Where(ticket.ID(sr.TicketID), ticket.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("关联工单 %d 不存在或不属于当前租户", sr.TicketID)
		}
		return nil, fmt.Errorf("查询关联工单失败: %w", err)
	}
	if !isValidLinkedTicketStatusTransition(current.Status, "resolved") {
		return nil, fmt.Errorf("非法的关联工单状态转换: %s -> %s", current.Status, "resolved")
	}

	completionNote, _ := variables["completion_note"].(string)
	if sr.CompletedAt.IsZero() || (completionNote != "" && sr.CompletionNote != completionNote) {
		update := sr.Update()
		if sr.CompletedAt.IsZero() {
			update.SetCompletedAt(time.Now())
		}
		if completionNote != "" && sr.CompletionNote != completionNote {
			update.SetCompletionNote(completionNote)
		}
		if _, err := update.Save(ctx); err != nil {
			return nil, fmt.Errorf("记录服务请求完成信息失败: %w", err)
		}
	}
	if current.Status != "resolved" || current.ResolvedAt.IsZero() {
		update := current.Update().SetStatus("resolved")
		if current.ResolvedAt.IsZero() {
			update.SetResolvedAt(time.Now())
		}
		if _, err := update.SetUpdatedAt(time.Now()).Save(ctx); err != nil {
			return nil, fmt.Errorf("更新关联工单状态失败: %w", err)
		}
	}
	h.logger.Infow("Service request completed via BPMN", "request_id", sr.ID)
	return &CallbackEffect{Status: CallbackEffectApplied, Message: fmt.Sprintf("服务请求 %d 已完成", sr.ID)}, nil
}

func (h *ServiceRequestServiceTaskHandler) cancelRequest(ctx context.Context, variables map[string]interface{}, reason string) (*CallbackEffect, error) {
	note := reason
	if note == "" {
		note = "已取消"
	}
	return h.setLinkedTicketStatus(ctx, variables, "closed", note)
}

// isValidLinkedTicketStatusTransition 服务请求关联工单的状态转换白名单。
// ServiceRequest 自身的状态语义全部委托给关联工单，这里只放行服务请求生命周期
// 会触发的转换（approve→in_progress、reject/cancel→closed、complete→resolved），
// 防止动作被任意调用时把工单跳成非法状态。同状态幂等放行（重复完成/驳回不报错）。
func isValidLinkedTicketStatusTransition(current, newStatus string) bool {
	if current == newStatus {
		return true
	}
	transitions := map[string]map[string]struct{}{
		"new":         {"in_progress": {}, "resolved": {}, "closed": {}},
		"open":        {"in_progress": {}, "resolved": {}, "closed": {}},
		"assigned":    {"in_progress": {}, "resolved": {}, "closed": {}},
		"pending":     {"in_progress": {}, "resolved": {}, "closed": {}},
		"in_progress": {"resolved": {}, "closed": {}},
	}
	allowed, ok := transitions[current]
	if !ok {
		return false
	}
	_, ok = allowed[newStatus]
	return ok
}

// 确保 ServiceRequestServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*ServiceRequestServiceTaskHandler)(nil)
