package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

// GenericServiceTaskHandler 通用服务任务处理器
type GenericServiceTaskHandler struct {
	HandlerBase
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewGenericServiceTaskHandler 创建通用处理器
func NewGenericServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *GenericServiceTaskHandler {
	return &GenericServiceTaskHandler{
		client: client,
		logger: logger,
	}
}

// GetTaskType 返回任务类型
func (h *GenericServiceTaskHandler) GetTaskType() string {
	return "generic_task"
}

// GetHandlerID 返回处理器标识
func (h *GenericServiceTaskHandler) GetHandlerID() string {
	return "generic_service_handler"
}

// Execute 执行通用服务任务。已知的 action 对应内置模板（service_request_flow /
// service_request_urgent_flow / incident_emergency_flow）里真实声明的动作，做真实的
// Ticket/Notification 写入；未识别的 action 保留原有的变量透传行为，不强行猜测语义——
// generic_task 这个类型本身的定位就是给未来自定义模板留的通用出口。
func (h *GenericServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "complete_service":
		return h.completeService(ctx, variables)
	case "notify_rejection":
		return h.notifyRequester(ctx, variables, "服务请求已被驳回")
	case "notify":
		return h.notifyRequester(ctx, variables, "有新的处理进展，请查看")
	default:
		operation, _ := variables["operation"].(string)
		result := &dto.ServiceTaskResult{
			Success:    true,
			Message:    fmt.Sprintf("通用任务 %s 执行完成", operation),
			OutputVars: make(map[string]interface{}),
		}
		for k, v := range variables {
			result.OutputVars[k] = v
		}
		return result, nil
	}
}

// Validate 验证配置
func (h *GenericServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// completeService 对应"服务完成"节点（service_request_flow.bpmn 的 Activity_Complete）：
// 把关联的工单状态置为 resolved，跟 TicketServiceTaskHandler.updateTicketStatus 同款写法。
func (h *GenericServiceTaskHandler) completeService(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	ticketID := GetIntFromVars(variables, "business_id")
	if ticketID <= 0 {
		return nil, fmt.Errorf("无效的 business_id")
	}
	tenantID := GetTenantIDFromVars(variables)
	update := h.client.Ticket.UpdateOneID(ticketID)
	if tenantID > 0 {
		update = update.Where(ticket.TenantID(tenantID))
	}
	if _, err := update.SetStatus("resolved").SetResolvedAt(time.Now()).SetUpdatedAt(time.Now()).Save(ctx); err != nil {
		return nil, fmt.Errorf("完成服务请求失败: %w", err)
	}
	h.logger.Infow("Service request completed via BPMN generic handler", "ticket_id", ticketID)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("工单 %d 已完成", ticketID)}, nil
}

// notifyRequester 对应"驳回通知"/"通知相关方"这类纯通知节点：给工单申请人真实创建一条
// 通知（站内消息 + 统一 Notification），不是只打日志。写法直接抄
// CCTaskHandler.createCCNotifications 已经验证过的模式。
func (h *GenericServiceTaskHandler) notifyRequester(ctx context.Context, variables map[string]interface{}, defaultContent string) (*dto.ServiceTaskResult, error) {
	ticketID := GetIntFromVars(variables, "business_id")
	if ticketID <= 0 {
		return nil, fmt.Errorf("无效的 business_id")
	}
	tenantID := GetTenantIDFromVars(variables)

	ticketEntity, err := h.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("获取工单失败: %w", err)
	}

	content := defaultContent
	if reason, ok := variables["reject_reason"].(string); ok && reason != "" {
		content = fmt.Sprintf("%s：%s", defaultContent, reason)
	}
	content = fmt.Sprintf("工单 %s「%s」：%s", ticketEntity.TicketNumber, ticketEntity.Title, content)

	now := time.Now()
	if _, err := h.client.TicketNotification.Create().
		SetTicketID(ticketID).
		SetUserID(ticketEntity.RequesterID).
		SetType("workflow").
		SetChannel("in_app").
		SetContent(content).
		SetTenantID(tenantID).
		SetStatus("sent").
		SetSentAt(now).
		Save(ctx); err != nil {
		return nil, fmt.Errorf("创建工单通知失败: %w", err)
	}
	if _, err := h.client.Notification.Create().
		SetTitle("工单进展通知").
		SetMessage(content).
		SetType("info").
		SetUserID(ticketEntity.RequesterID).
		SetTenantID(tenantID).
		SetActionURL(fmt.Sprintf("/tickets/%d", ticketID)).
		SetActionText("查看工单").
		Save(ctx); err != nil {
		h.logger.Warnw("Failed to create unified notification via BPMN generic handler", "error", err, "ticket_id", ticketID)
	}

	return &dto.ServiceTaskResult{Success: true, Message: "通知已发送"}, nil
}

// 确保 GenericServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*GenericServiceTaskHandler)(nil)
