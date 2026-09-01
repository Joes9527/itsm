package bpmn

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/notification"
	"itsm-backend/ent/user"

	"go.uber.org/zap"
)

// NotificationHandler 通知服务任务处理器
type NotificationHandler struct {
	HandlerBase
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewNotificationHandler 创建通知处理器
func NewNotificationHandler(client *ent.Client, logger *zap.SugaredLogger) *NotificationHandler {
	return &NotificationHandler{
		client: client,
		logger: logger,
	}
}

// GetTaskType 返回任务类型
func (h *NotificationHandler) GetTaskType() string {
	return "notification_task"
}

// GetHandlerID 返回处理器标识
func (h *NotificationHandler) GetHandlerID() string {
	return "notification_handler"
}

// Execute 执行通知服务任务
func (h *NotificationHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*CallbackEffect, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "send_email":
		return h.sendEmail(ctx, variables)
	case "send_sms":
		return h.sendSMS(ctx, variables)
	case "send_in_app":
		return h.sendInAppNotification(ctx, variables)
	case "send_webhook":
		return BlockedEffect(CallbackBlockChannelUnavailable, "webhook callback channel is unavailable"), nil
	default:
		return BlockedEffect(CallbackBlockHandlerContract, "unsupported notification callback action"), nil
	}
}

// sendEmail 发送邮件通知
func (h *NotificationHandler) sendEmail(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
	return BlockedEffect(CallbackBlockChannelUnavailable, "email callback channel is unavailable"), nil
}

// sendSMS 发送短信通知
func (h *NotificationHandler) sendSMS(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
	return BlockedEffect(CallbackBlockChannelUnavailable, "sms callback channel is unavailable"), nil
}

// sendInAppNotification 发送应用内通知
func (h *NotificationHandler) sendInAppNotification(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
	userIDs := GetIntFromVars(variables, "user_ids")
	title := GetStringFromVars(variables, "title")
	content := GetStringFromVars(variables, "content")
	notificationType := GetStringFromVars(variables, "notification_type")

	if userIDs == 0 {
		return BlockedEffect(CallbackBlockRecipientEmpty, "notification recipient is empty"), nil
	}
	if h.client == nil {
		return nil, fmt.Errorf("通知存储未配置")
	}
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	if _, err := h.client.User.Query().Where(user.ID(userIDs), user.TenantID(tenantID), user.Active(true)).Only(ctx); err != nil {
		return nil, fmt.Errorf("通知接收人不存在或不属于当前租户")
	}
	if notificationType == "" {
		notificationType = "info"
	}
	deliveryKey, durable := BPMNCallbackExecutionKey(ctx)
	if durable {
		exists, err := h.client.Notification.Query().Where(
			notification.TenantID(tenantID),
			notification.UserID(userIDs),
			notification.DeliveryKey(deliveryKey),
		).Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查通知幂等状态失败")
		}
		if exists {
			return IdempotentEffect(fmt.Sprintf("应用内通知已发送给用户 %d", userIDs), nil), nil
		}
	}

	create := h.client.Notification.Create().
		SetTitle(title).
		SetMessage(content).
		SetType(notificationType).
		SetUserID(userIDs).
		SetTenantID(tenantID)
	if durable {
		create.SetDeliveryKey(deliveryKey)
	}
	if _, err := create.Save(ctx); err != nil {
		return nil, fmt.Errorf("创建应用内通知失败")
	}

	h.logger.Infow("In-app notification persisted via BPMN", "user_id", userIDs, "tenant_id", tenantID)

	return &CallbackEffect{Status: CallbackEffectApplied,
		Message: fmt.Sprintf("应用内通知已发送给用户 %d", userIDs),
	}, nil
}

// 确保 NotificationHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*NotificationHandler)(nil)
