package dto

import (
	"time"

	"itsm-backend/ent"
)

// TicketNotificationResponse 工单通知响应
type TicketNotificationResponse struct {
	ID        int        `json:"id"`
	TicketID  int        `json:"ticketId"`
	UserID    int        `json:"userId"`
	Type      string     `json:"type"`    // created, assigned, status_changed, commented, sla_warning, resolved, closed
	Channel   string     `json:"channel"` // email, in_app, sms
	Content   string     `json:"content"`
	SentAt    *time.Time `json:"sentAt,omitempty"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
	Status    string     `json:"status"` // pending, sent, read
	CreatedAt time.Time  `json:"createdAt"`
	User      *UserInfo  `json:"user,omitempty"` // 接收人信息
}

// ListTicketNotificationsResponse 工单通知列表响应
type ListTicketNotificationsResponse struct {
	Notifications []*TicketNotificationResponse `json:"notifications"`
	Total         int                           `json:"total"`
}

// SendTicketNotificationRequest 发送工单通知请求
type SendTicketNotificationRequest struct {
	UserIDs     []int  `json:"userIds" binding:"required,min=1"` // 接收人ID列表
	EventType   string `json:"eventType" binding:"required"`     // 事件类型（偏好查询键）：ticket_created / comment_added 等
	Content     string `json:"content" binding:"required"`       // 通知内容
	DeliveryKey string `json:"-"`                                // 仅供内部 durable callback 去重
	InAppOnly   bool   `json:"-"`                                // durable callback 不直接调用无幂等协议的外部渠道
}

const (
	TicketNotificationEffectApplied    = "applied"
	TicketNotificationEffectIdempotent = "idempotent"
	TicketNotificationEffectBlocked    = "blocked"
)

// SendTicketNotificationResult is the durable evidence produced by the
// authoritative ticket-notification service. BlockCode is internal-only so a
// deterministic delivery block cannot expose implementation detail through the
// public API.
type SendTicketNotificationResult struct {
	Effect          string `json:"effect"`
	RecipientCount  int    `json:"recipientCount"`
	AppliedCount    int    `json:"appliedCount"`
	IdempotentCount int    `json:"idempotentCount"`
	DeliveryCount   int    `json:"deliveryCount"`
	BlockCode       string `json:"-"`
}

// UpdateNotificationPreferencesRequest 更新通知偏好请求
type UpdateNotificationPreferencesRequest struct {
	EmailEnabled   bool `json:"emailEnabled"`   // 是否启用邮件通知
	InAppEnabled   bool `json:"inAppEnabled"`   // 是否启用站内消息通知
	SmsEnabled     bool `json:"smsEnabled"`     // 是否启用短信通知（可选）
	SlaWarningTime int  `json:"slaWarningTime"` // SLA警告提前时间（分钟）
}

// NotificationPreferencesResponse 通知偏好响应
type NotificationPreferencesResponse struct {
	UserID         int  `json:"userId"`
	EmailEnabled   bool `json:"emailEnabled"`
	InAppEnabled   bool `json:"inAppEnabled"`
	SmsEnabled     bool `json:"smsEnabled"`
	SlaWarningTime int  `json:"slaWarningTime"`
}

// ToTicketNotificationResponse 将 Ent 实体转换为 DTO
func ToTicketNotificationResponse(notification *ent.TicketNotification, user *ent.User) *TicketNotificationResponse {
	resp := &TicketNotificationResponse{
		ID:        notification.ID,
		TicketID:  notification.TicketID,
		UserID:    notification.UserID,
		Type:      notification.Type,
		Channel:   notification.Channel,
		Content:   notification.Content,
		Status:    notification.Status,
		CreatedAt: notification.CreatedAt,
	}

	// SentAt 和 ReadAt 在 Ent 中可能是 time.Time 或 *time.Time
	// 检查是否为零值来判断是否已设置
	if !notification.SentAt.IsZero() {
		resp.SentAt = &notification.SentAt
	}
	if !notification.ReadAt.IsZero() {
		resp.ReadAt = &notification.ReadAt
	}

	// 设置用户信息
	if user != nil {
		resp.User = &UserInfo{
			ID:         user.ID,
			Username:   user.Username,
			Name:       user.Name,
			Email:      user.Email,
			Role:       string(user.Role),
			Department: user.Department,
			TenantID:   user.TenantID,
		}
	}

	return resp
}
