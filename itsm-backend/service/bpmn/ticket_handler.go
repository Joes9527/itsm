package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

// TicketNotificationServiceInterface 通知服务接口
// 用于避免循环依赖
type TicketNotificationServiceInterface interface {
	SendNotification(ctx context.Context, ticketID int, req *dto.SendTicketNotificationRequest, tenantID int) (*dto.SendTicketNotificationResult, error)
}

// TicketStatusServiceInterface 工单状态更新服务接口（避免循环依赖，见
// TicketService.UpdateTicketStatusForWorkflow 的注释）
type TicketStatusServiceInterface interface {
	UpdateTicketStatusForWorkflow(ctx context.Context, ticketID int, status string, tenantID int, operatorID int) error
}

// TicketServiceTaskHandler 工单服务任务处理器
type TicketServiceTaskHandler struct {
	HandlerBase
	client              *ent.Client
	logger              *zap.SugaredLogger
	notificationService TicketNotificationServiceInterface
	statusService       TicketStatusServiceInterface
}

// NewTicketServiceTaskHandler 创建工单处理器
func NewTicketServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *TicketServiceTaskHandler {
	handler := &TicketServiceTaskHandler{
		client: client,
		logger: logger,
	}
	return handler
}

// SetNotificationService 设置通知服务
func (h *TicketServiceTaskHandler) SetNotificationService(svc TicketNotificationServiceInterface) {
	h.notificationService = svc
}

func (h *TicketServiceTaskHandler) sendNotification(ctx context.Context, ticketID int, req *dto.SendTicketNotificationRequest, tenantID int) (*CallbackEffect, error) {
	if h.notificationService == nil {
		return nil, fmt.Errorf("ticket notification service 未注入")
	}
	if req == nil {
		return nil, fmt.Errorf("工单通知请求不能为空")
	}
	if key, durable := BPMNCallbackExecutionKey(ctx); durable {
		req.DeliveryKey = key
		req.InAppOnly = true
	}
	result, err := h.notificationService.SendNotification(ctx, ticketID, req, tenantID)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("ticket notification result is missing")
	}
	switch result.Effect {
	case dto.TicketNotificationEffectApplied:
		return AppliedEffect("ticket notification delivered", nil), nil
	case dto.TicketNotificationEffectIdempotent:
		return IdempotentEffect("ticket notification already delivered", nil), nil
	case dto.TicketNotificationEffectBlocked:
		code := CallbackBlockCode(result.BlockCode)
		if !IsAllowedCallbackBlockCode(code) {
			return nil, fmt.Errorf("ticket notification result has invalid block code")
		}
		return BlockedEffect(code, "ticket notification delivery blocked"), nil
	default:
		return nil, fmt.Errorf("ticket notification result has invalid effect")
	}
}

// SetTicketService 注入工单状态服务，由 bootstrap 在 TicketService 构造完成后调用
// （TicketService 构造时依赖的东西比 CallbackRegistry 晚初始化，不能在 NewTicketServiceTaskHandler
// 里直接注入，跟 SetNotificationService 是同一个延迟装配模式）。
func (h *TicketServiceTaskHandler) SetTicketService(svc TicketStatusServiceInterface) {
	h.statusService = svc
}

// GetTaskType 返回任务类型
func (h *TicketServiceTaskHandler) GetTaskType() string {
	return "ticket_task"
}

// GetHandlerID 返回处理器标识
func (h *TicketServiceTaskHandler) GetHandlerID() string {
	return "ticket_service_handler"
}

func (h *TicketServiceTaskHandler) getTenantID(ctx context.Context, variables map[string]interface{}) (int, error) {
	return RequireTenantID(ctx, variables)
}

func (h *TicketServiceTaskHandler) getTicket(ctx context.Context, ticketID int, tenantID int) (*ent.Ticket, error) {
	return h.client.Ticket.Query().
		Where(ticket.ID(ticketID), ticket.TenantID(tenantID)).
		Only(ctx)
}

// Execute 执行工单服务任务
func (h *TicketServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*CallbackEffect, error) {
	// 提取业务ID
	var businessID int
	switch v := variables["business_id"].(type) {
	case int:
		businessID = v
	case float64:
		businessID = int(v)
	default:
		return nil, fmt.Errorf("无效的 business_id")
	}

	// 根据任务类型执行不同操作
	action, _ := variables["action"].(string)
	switch action {
	case "update_status":
		// 更新状态
		return h.updateTicketStatus(ctx, businessID, variables)
	case "notify_requester":
		// 通知请求人
		return h.notifyRequester(ctx, businessID, variables)
	case "notify_handler":
		// 通知处理人
		return h.notifyHandler(ctx, businessID, variables)
	case "escalate":
		// 升级处理
		return h.escalateTicket(ctx, businessID, variables)
	case "assign":
		// 分配任务
		return h.assignTicket(ctx, businessID, variables)
	default:
		return nil, fmt.Errorf("不支持的工单回调动作")
	}
}

// updateTicketStatus 更新工单状态
func (h *TicketServiceTaskHandler) updateTicketStatus(ctx context.Context, ticketID int, variables map[string]interface{}) (*CallbackEffect, error) {
	// 获取新状态
	newStatus, _ := variables["new_status"].(string)
	if newStatus == "" {
		newStatus = "in_progress"
	}

	// 解析附加字段
	additionalData := make(map[string]interface{})
	if formFields, ok := variables["form_fields"].(map[string]interface{}); ok {
		additionalData["form_fields"] = formFields
	}

	// 执行更新——通过 TicketService 走状态机校验、通知、飞书同步等既有业务规则，
	// 不再绕过领域服务直接改 Ent（AGENTS.md：Handler 不能绕过专业服务直接修改状态）。
	if h.statusService == nil {
		return nil, fmt.Errorf("ticket status service 未注入，无法更新工单状态")
	}
	tenantID, err := h.getTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	current, err := h.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("工单不存在: %w", err)
	}
	if current.Status == newStatus {
		return IdempotentEffect(fmt.Sprintf("工单 %d 已处于 %s", ticketID, newStatus), additionalData), nil
	}
	// operatorID=0 表示系统身份（BPMN 引擎驱动的状态变更，不是某个登录用户点的按钮）；
	// TicketService.UpdateTicketStatus 本身不强制 operatorID>0。
	if err := h.statusService.UpdateTicketStatusForWorkflow(ctx, ticketID, newStatus, tenantID, 0); err != nil {
		h.logger.Errorw("Failed to update ticket status", "ticket_id", ticketID, "error_class", "domain_update")
		return nil, fmt.Errorf("更新工单状态失败: %w", err)
	}

	h.logger.Infow("Ticket status updated via BPMN", "ticket_id", ticketID, "new_status", newStatus)

	return &CallbackEffect{Status: CallbackEffectApplied,
		Message:     fmt.Sprintf("工单 %d 状态已更新为 %s", ticketID, newStatus),
		UpdatedData: additionalData,
	}, nil
}

// notifyRequester 通知请求人
func (h *TicketServiceTaskHandler) notifyRequester(ctx context.Context, ticketID int, variables map[string]interface{}) (*CallbackEffect, error) {
	// 获取通知内容
	notificationType, _ := variables["notification_type"].(string)
	if notificationType == "" {
		notificationType = "status_update"
	}
	content, _ := variables["content"].(string)

	// 获取工单信息
	tenantID, err := h.getTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	ticketEntity, err := h.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("工单不存在: %w", err)
	}

	if ticketEntity.RequesterID <= 0 {
		return BlockedEffect(CallbackBlockRecipientMissing, "ticket requester is missing"), nil
	}
	// 发送通知给请求人
	{
		req := &dto.SendTicketNotificationRequest{
			UserIDs:   []int{ticketEntity.RequesterID},
			EventType: mapBPMNNotificationType(notificationType),
			Content:   content,
		}
		if key, durable := BPMNCallbackExecutionKey(ctx); durable {
			req.DeliveryKey = key
			req.InAppOnly = true
		}
		effect, err := h.sendNotification(ctx, ticketID, req, ticketEntity.TenantID)
		if err != nil {
			return nil, fmt.Errorf("通知请求人失败")
		}
		return effect, nil
	}

	h.logger.Infow("Requester notified via BPMN", "ticket_id", ticketID, "requester_id", ticketEntity.RequesterID)

	return nil, fmt.Errorf("unreachable requester notification path")
}

// notifyHandler 通知处理人
func (h *TicketServiceTaskHandler) notifyHandler(ctx context.Context, ticketID int, variables map[string]interface{}) (*CallbackEffect, error) {
	// 获取通知内容
	notificationType, _ := variables["notification_type"].(string)
	if notificationType == "" {
		notificationType = "assignment"
	}
	content, _ := variables["content"].(string)

	// 获取工单信息
	tenantID, err := h.getTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	ticketEntity, err := h.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("工单不存在: %w", err)
	}

	if ticketEntity.AssigneeID <= 0 {
		return BlockedEffect(CallbackBlockRecipientMissing, "ticket assignee is missing"), nil
	}
	// 发送通知给处理人
	{
		req := &dto.SendTicketNotificationRequest{
			UserIDs:   []int{ticketEntity.AssigneeID},
			EventType: mapBPMNNotificationType(notificationType),
			Content:   content,
		}
		if key, durable := BPMNCallbackExecutionKey(ctx); durable {
			req.DeliveryKey = key
			req.InAppOnly = true
		}
		effect, err := h.sendNotification(ctx, ticketID, req, ticketEntity.TenantID)
		if err != nil {
			return nil, fmt.Errorf("通知处理人失败")
		}
		return effect, nil
	}

	h.logger.Infow("Handler notified via BPMN", "ticket_id", ticketID, "handler_id", ticketEntity.AssigneeID)

	return nil, fmt.Errorf("unreachable handler notification path")
}

// escalateTicket 升级工单
func (h *TicketServiceTaskHandler) escalateTicket(ctx context.Context, ticketID int, variables map[string]interface{}) (*CallbackEffect, error) {
	// 获取升级优先级
	escalateTo, _ := variables["escalate_to"].(string)
	if escalateTo == "" {
		escalateTo = "high"
	}
	escalationReason, _ := variables["escalation_reason"].(string)

	// 获取工单信息
	tenantID, err := h.getTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	ticketEntity, err := h.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("工单不存在: %w", err)
	}

	if ticketEntity.Priority == escalateTo && ticketEntity.Status == "escalated" {
		return IdempotentEffect(fmt.Sprintf("工单 %d 已升级为 %s", ticketID, escalateTo), nil), nil
	}

	// 通知管理员或升级处理人
	adminIDs := GetIntSliceFromVars(variables, "notify_admin_ids")
	if len(adminIDs) > 0 {
		content := fmt.Sprintf("工单 %s (#%s) 已升级，原因：%s", ticketEntity.Title, ticketEntity.TicketNumber, escalationReason)
		for _, adminID := range adminIDs {
			effect, err := h.sendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
				UserIDs:   []int{adminID},
				EventType: "ticket_updated",
				Content:   content,
			}, ticketEntity.TenantID)
			if err != nil {
				h.logger.Warnw("failed to send escalation notification", "error_class", "notification_delivery", "ticket_id", ticketID, "admin_id", adminID)
				return nil, fmt.Errorf("升级通知失败")
			}
			if effect.Status == CallbackEffectBlocked {
				return effect, nil
			}
		}
	}

	// Notify first. A stable delivery key deduplicates a retry if the state write
	// fails after notification persistence.
	_, err = h.client.Ticket.UpdateOneID(ticketID).Where(ticket.TenantID(tenantID)).
		SetPriority(escalateTo).
		SetStatus("escalated").
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("升级工单失败: %w", err)
	}

	h.logger.Infow("Ticket escalated via BPMN", "ticket_id", ticketID, "escalated_to", escalateTo, "reason", escalationReason)

	return &CallbackEffect{Status: CallbackEffectApplied,
		Message: fmt.Sprintf("工单 %d 已升级为 %s", ticketID, escalateTo),
	}, nil
}

// assignTicket 分配工单
func (h *TicketServiceTaskHandler) assignTicket(ctx context.Context, ticketID int, variables map[string]interface{}) (*CallbackEffect, error) {
	// 获取分配的处理人ID
	assigneeIDFloat, ok := variables["assignee_id"].(float64)
	assigneeID := int(assigneeIDFloat)
	if !ok || assigneeID == 0 {
		// 尝试从变量中获取
		assigneeID, _ = variables["assignee_id"].(int)
	}

	if assigneeID == 0 {
		return nil, fmt.Errorf("分配失败: 未指定处理人ID")
	}

	// 获取工单信息
	tenantID, err := h.getTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	ticketEntity, err := h.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("工单不存在: %w", err)
	}

	if ticketEntity.AssigneeID == assigneeID && ticketEntity.Status == common.TicketStatusAssigned {
		return IdempotentEffect(fmt.Sprintf("工单 %d 已分配给用户 %d", ticketID, assigneeID), nil), nil
	}

	// 发送通知给新的处理人
	notifyContent, _ := variables["notify_content"].(string)
	if notifyContent == "" {
		notifyContent = fmt.Sprintf("您被分配了一个新工单：%s (#%s)", ticketEntity.Title, ticketEntity.TicketNumber)
	}
	effect, err := h.sendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{assigneeID},
		EventType: "ticket_assigned",
		Content:   notifyContent,
	}, ticketEntity.TenantID)
	if err != nil {
		h.logger.Warnw("failed to send assignment notification", "error_class", "notification_delivery", "ticket_id", ticketID, "assignee_id", assigneeID)
		return nil, fmt.Errorf("分配通知失败")
	}
	if effect.Status == CallbackEffectBlocked {
		return effect, nil
	}

	_, err = h.client.Ticket.UpdateOneID(ticketID).Where(ticket.TenantID(tenantID)).
		SetAssigneeID(assigneeID).
		SetStatus(common.TicketStatusAssigned).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("分配工单失败: %w", err)
	}

	h.logger.Infow("Ticket assigned via BPMN", "ticket_id", ticketID, "assignee_id", assigneeID)

	return &CallbackEffect{Status: CallbackEffectApplied,
		Message: fmt.Sprintf("工单 %d 已分配给用户 %d", ticketID, assigneeID),
	}, nil
}

// 确保 TicketServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*TicketServiceTaskHandler)(nil)

// mapBPMNNotificationType 将 BPMN 通知类型映射为标准 event_type（偏好查询键）。
func mapBPMNNotificationType(notificationType string) string {
	switch notificationType {
	case "assignment":
		return "ticket_assigned"
	case "escalation", "status_update":
		return "ticket_updated"
	default:
		return "ticket_updated"
	}
}
