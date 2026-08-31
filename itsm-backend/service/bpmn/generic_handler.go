package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketnotification"

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

// Execute 执行通用服务任务。已知 action 对应内置模板的真实业务写入；
// 未知 action 必须失败关闭，不能在未产生业务效果时推进流程。
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
		return nil, fmt.Errorf("不支持的通用回调动作")
	}
}

// Validate 验证配置
func (h *GenericServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// ticketBackedBusinessTypes 列出 business_id 确实指向 Ticket 主键的业务类型。
//
// generic_task 的 complete_service / notify_rejection / notify 三个动作都把 business_id
// 当工单 ID 用（这是 service_request_flow 的设计），但 incident_emergency_flow 的
// Activity_Notify 同样声明 generic_task/notify，而它是以
// business_type=incident、business_id=<事件ID> 触发的——事件 ID 和工单 ID 是两个完全
// 独立的 ID 空间，撞号时会读到毫不相干的工单，甚至是别的租户的工单。
//
// ProcessTriggerService 将 business_type 写入实例，回调执行时再从权威实例身份补齐。
// 空串仅保留给不涉及持久化 UserTask 回调的直接调用兼容路径。
var ticketBackedBusinessTypes = map[string]struct{}{
	"":                {},
	"ticket":          {},
	"service_request": {},
}

// isTicketBackedFlow 判断当前流程实例的 business_id 是否可以当作工单 ID 使用。
func isTicketBackedFlow(variables map[string]interface{}) bool {
	_, ok := ticketBackedBusinessTypes[GetStringFromVars(variables, "business_type")]
	return ok
}

// getTicket 按 ID + 租户取工单。租户约束是强制的，不做"tenant<=0 就退化成全表 Get"的兜底。
func (h *GenericServiceTaskHandler) getTicket(ctx context.Context, ticketID, tenantID int) (*ent.Ticket, error) {
	return h.client.Ticket.Query().
		Where(ticket.ID(ticketID), ticket.TenantID(tenantID)).
		Only(ctx)
}

// completeService 对应"服务完成"节点（service_request_flow.bpmn 的 Activity_Complete）：
// 把关联的工单状态置为 resolved，跟 TicketServiceTaskHandler.updateTicketStatus 同款写法。
func (h *GenericServiceTaskHandler) completeService(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	if !isTicketBackedFlow(variables) {
		h.logger.Warnw("complete_service 节点被非工单业务类型的流程触发，跳过（business_id 不是工单ID）",
			"business_type", GetStringFromVars(variables, "business_type"))
		return &dto.ServiceTaskResult{Success: true, Message: "非工单业务类型，跳过服务完成写入"}, nil
	}
	ticketID := GetIntFromVars(variables, "business_id")
	if ticketID <= 0 {
		return nil, fmt.Errorf("无效的 business_id")
	}
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	current, err := h.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("完成服务请求失败: %w", err)
	}
	if current.Status == "resolved" && !current.ResolvedAt.IsZero() {
		return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("工单 %d 已完成", ticketID)}, nil
	}
	update := current.Update().SetStatus("resolved").SetUpdatedAt(time.Now())
	if current.ResolvedAt.IsZero() {
		update.SetResolvedAt(time.Now())
	}
	if _, err := update.Save(ctx); err != nil {
		return nil, fmt.Errorf("完成服务请求失败: %w", err)
	}
	h.logger.Infow("Service request completed via BPMN generic handler", "ticket_id", ticketID)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("工单 %d 已完成", ticketID)}, nil
}

// notifyRequester 对应"驳回通知"/"通知相关方"这类纯通知节点：给工单申请人真实创建一条
// 通知（站内消息 + 统一 Notification），不是只打日志。写法直接抄
// CCTaskHandler.createCCNotifications 已经验证过的模式。
//
// 两道防线（Finding 2）：
//   - business_type 不是工单口径时直接跳过——incident_emergency_flow 的 Activity_Notify
//     声明的也是 generic_task/notify，但它的 business_id 是事件 ID；
//   - 工单查询强制带租户过滤（原来是不带任何过滤的 Ticket.Get），撞号时会把别的租户的
//     工单标题/编号写进一条持久化通知里。
//
// 查不到工单按空态跳过而不是硬失败：通知是流程的旁路副作用，不是状态流转，
// 让它把整条流程卡在通知节点上（handleElement 会把 error 往上抛）得不偿失。
// 跳过一律留 Warnw，便于事后排查。
func (h *GenericServiceTaskHandler) notifyRequester(ctx context.Context, variables map[string]interface{}, defaultContent string) (*dto.ServiceTaskResult, error) {
	businessType := GetStringFromVars(variables, "business_type")
	if !isTicketBackedFlow(variables) {
		h.logger.Warnw("通知节点被非工单业务类型的流程触发，跳过（business_id 不是工单ID）",
			"business_type", businessType, "business_id", GetIntFromVars(variables, "business_id"))
		return &dto.ServiceTaskResult{Success: true, Message: "非工单业务类型，跳过工单通知"}, nil
	}

	ticketID := GetIntFromVars(variables, "business_id")
	if ticketID <= 0 {
		return nil, fmt.Errorf("无效的 business_id")
	}
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	ticketEntity, err := h.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		if ent.IsNotFound(err) {
			h.logger.Warnw("通知节点未在当前租户下找到对应工单，跳过通知",
				"ticket_id", ticketID, "tenant_id", tenantID, "business_type", businessType)
			return &dto.ServiceTaskResult{Success: true, Message: "未找到对应工单，跳过通知"}, nil
		}
		return nil, fmt.Errorf("获取工单失败: %w", err)
	}

	content := defaultContent
	if reason, ok := variables["reject_reason"].(string); ok && reason != "" {
		content = fmt.Sprintf("%s：%s", defaultContent, reason)
	}
	content = fmt.Sprintf("工单 %s「%s」：%s", ticketEntity.TicketNumber, ticketEntity.Title, content)

	deliveryKey, durable := BPMNCallbackExecutionKey(ctx)
	tx, err := h.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("开启通知事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if durable {
		exists, err := tx.TicketNotification.Query().Where(
			ticketnotification.TenantID(tenantID),
			ticketnotification.TicketID(ticketID),
			ticketnotification.UserID(ticketEntity.RequesterID),
			ticketnotification.DeliveryKey(deliveryKey),
		).Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查通知幂等状态失败: %w", err)
		}
		if exists {
			return &dto.ServiceTaskResult{Success: true, Message: "通知已发送"}, nil
		}
	}

	now := time.Now()
	ticketCreate := tx.TicketNotification.Create().
		SetTicketID(ticketID).
		SetUserID(ticketEntity.RequesterID).
		SetType("workflow").
		SetChannel("in_app").
		SetContent(content).
		SetTenantID(tenantID).
		SetStatus("sent").
		SetSentAt(now)
	if durable {
		ticketCreate.SetDeliveryKey(deliveryKey)
	}
	if _, err := ticketCreate.Save(ctx); err != nil {
		return nil, fmt.Errorf("创建工单通知失败: %w", err)
	}
	notificationCreate := tx.Notification.Create().
		SetTitle("工单进展通知").
		SetMessage(content).
		SetType("info").
		SetUserID(ticketEntity.RequesterID).
		SetTenantID(tenantID).
		SetActionURL(fmt.Sprintf("/tickets/%d", ticketID)).
		SetActionText("查看工单")
	if durable {
		notificationCreate.SetDeliveryKey(deliveryKey)
	}
	if _, err := notificationCreate.Save(ctx); err != nil {
		return nil, fmt.Errorf("创建统一通知失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交通知事务失败: %w", err)
	}

	return &dto.ServiceTaskResult{Success: true, Message: "通知已发送"}, nil
}

// 确保 GenericServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*GenericServiceTaskHandler)(nil)
