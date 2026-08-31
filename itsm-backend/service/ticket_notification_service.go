package service

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"itsm-backend/connector"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketnotification"
	"itsm-backend/ent/user"

	"go.uber.org/zap"
)

func ticketNotificationStringPtr(s string) *string {
	return &s
}

type TicketNotificationService struct {
	client           *ent.Client
	logger           *zap.SugaredLogger
	connectorManager *connector.Manager
	emailService     *EmailService
	smsService       *SMSService
	prefService      *NotificationPreferenceService // 按 event_type 查偏好
	wsService        *WebSocketService              // push 渠道（WebSocket）
	now              func() time.Time
}

// NewTicketNotificationService 创建通知服务
func NewTicketNotificationService(client *ent.Client, logger *zap.SugaredLogger) *TicketNotificationService {
	return &TicketNotificationService{
		client: client,
		logger: logger,
		now:    time.Now,
	}
}

// SetConnectorManager injects the connector runtime used by durable external deliveries.
func (s *TicketNotificationService) SetConnectorManager(manager *connector.Manager) {
	s.connectorManager = manager
}

const (
	ticketNotificationStatusPending    = "pending"
	ticketNotificationStatusProcessing = "processing"
	ticketNotificationStatusSent       = "sent"
	ticketNotificationStatusFailed     = "failed"
	ticketNotificationLeaseDuration    = 60 * time.Second
)

// ProcessPendingDeliveries performs one deterministic durable notification sweep.
func (s *TicketNotificationService) ProcessPendingDeliveries(ctx context.Context, workerID string, limit int) (int, error) {
	if err := validateTicketNotificationWorkerID(workerID); err != nil {
		return 0, err
	}
	if s.client == nil {
		return 0, fmt.Errorf("ticket notification client is required")
	}
	if limit <= 0 {
		return 0, nil
	}

	now := s.clock()
	candidates, err := s.client.TicketNotification.Query().
		Where(
			ticketnotification.DeliveryKeyNotNil(),
			ticketnotification.ChannelNEQ("in_app"),
			ticketnotification.Or(
				ticketnotification.And(
					ticketnotification.StatusEQ(ticketNotificationStatusPending),
					ticketnotification.NextAttemptAtLTE(now),
				),
				ticketnotification.And(
					ticketnotification.StatusEQ(ticketNotificationStatusProcessing),
					ticketnotification.LeaseExpiresAtLT(now),
				),
			),
		).
		Order(ent.Asc(ticketnotification.FieldNextAttemptAt), ent.Asc(ticketnotification.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("ticket notification candidate scan failed")
	}

	completed := 0
	failed := false
	for _, row := range candidates {
		claimed, claimErr := s.claimDelivery(ctx, workerID, row)
		if claimErr != nil {
			failed = true
			continue
		}
		if !claimed {
			continue
		}

		claimedRow := *row
		claimedRow.Status = ticketNotificationStatusProcessing
		claimedRow.AttemptCount++
		claimedRow.LeaseOwner = workerID
		claimedRow.LeaseExpiresAt = s.clock().Add(ticketNotificationLeaseDuration)
		errorClass := s.dispatchClaimedDelivery(ctx, &claimedRow)
		if errorClass != "" {
			failed = true
			if isTicketNotificationPermanentErrorClass(errorClass) {
				_ = s.failDelivery(ctx, workerID, &claimedRow, errorClass)
			} else {
				_ = s.retryDelivery(ctx, workerID, &claimedRow, errorClass)
			}
			continue
		}
		completedRow, completeErr := s.completeDelivery(ctx, workerID, &claimedRow)
		if completeErr != nil || !completedRow {
			failed = true
			continue
		}
		completed++
	}
	if failed {
		return completed, fmt.Errorf("one or more ticket notifications were not completed")
	}
	return completed, nil
}

// RunDeliveryWorker performs an immediate sweep and polls until cancellation.
func (s *TicketNotificationService) RunDeliveryWorker(ctx context.Context, workerID string, interval time.Duration) {
	if validateTicketNotificationWorkerID(workerID) != nil {
		return
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	s.runDeliverySweep(ctx, workerID)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDeliverySweep(ctx, workerID)
		}
	}
}

func (s *TicketNotificationService) runDeliverySweep(ctx context.Context, workerID string) {
	if _, err := s.ProcessPendingDeliveries(ctx, workerID, 50); err != nil && s.logger != nil {
		s.logger.Warnw("ticket notification delivery sweep incomplete",
			"worker_id", workerID,
			"error_class", "notification_sweep_error",
		)
	}
}

func (s *TicketNotificationService) claimDelivery(ctx context.Context, workerID string, row *ent.TicketNotification) (bool, error) {
	if row == nil || row.TenantID <= 0 {
		return false, fmt.Errorf("ticket notification row is missing tenant")
	}
	now := s.clock()
	affected, err := s.client.TicketNotification.Update().
		Where(
			ticketnotification.ID(row.ID),
			ticketnotification.TenantID(row.TenantID),
			ticketnotification.DeliveryKeyNotNil(),
			ticketnotification.Or(
				ticketnotification.And(
					ticketnotification.StatusEQ(ticketNotificationStatusPending),
					ticketnotification.NextAttemptAtLTE(now),
				),
				ticketnotification.And(
					ticketnotification.StatusEQ(ticketNotificationStatusProcessing),
					ticketnotification.LeaseExpiresAtLT(now),
				),
			),
		).
		SetStatus(ticketNotificationStatusProcessing).
		SetLeaseOwner(workerID).
		SetLeaseExpiresAt(now.Add(ticketNotificationLeaseDuration)).
		AddAttemptCount(1).
		Save(ctx)
	if err != nil {
		return false, fmt.Errorf("ticket notification claim failed")
	}
	return affected == 1, nil
}

func (s *TicketNotificationService) completeDelivery(ctx context.Context, workerID string, row *ent.TicketNotification) (bool, error) {
	affected, err := s.client.TicketNotification.Update().
		Where(
			ticketnotification.ID(row.ID),
			ticketnotification.TenantID(row.TenantID),
			ticketnotification.StatusEQ(ticketNotificationStatusProcessing),
			ticketnotification.LeaseOwner(workerID),
		).
		SetStatus(ticketNotificationStatusSent).
		SetSentAt(s.clock()).
		ClearLeaseOwner().
		ClearLeaseExpiresAt().
		ClearLastErrorClass().
		Save(ctx)
	if err != nil {
		return false, fmt.Errorf("ticket notification completion failed")
	}
	return affected == 1, nil
}

func (s *TicketNotificationService) retryDelivery(ctx context.Context, workerID string, row *ent.TicketNotification, errorClass string) error {
	if !isTicketNotificationErrorClass(errorClass) {
		errorClass = "unknown_error"
	}
	affected, err := s.client.TicketNotification.Update().
		Where(
			ticketnotification.ID(row.ID),
			ticketnotification.TenantID(row.TenantID),
			ticketnotification.StatusEQ(ticketNotificationStatusProcessing),
			ticketnotification.LeaseOwner(workerID),
		).
		SetStatus(ticketNotificationStatusPending).
		SetNextAttemptAt(s.clock().Add(ticketNotificationRetryDelay(row.AttemptCount))).
		SetLastErrorClass(errorClass).
		ClearLeaseOwner().
		ClearLeaseExpiresAt().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("ticket notification retry scheduling failed")
	}
	if affected != 1 {
		return fmt.Errorf("ticket notification lease lost")
	}
	return nil
}

func (s *TicketNotificationService) failDelivery(ctx context.Context, workerID string, row *ent.TicketNotification, errorClass string) error {
	if !isTicketNotificationPermanentErrorClass(errorClass) {
		errorClass = "unknown_error"
	}
	affected, err := s.client.TicketNotification.Update().
		Where(
			ticketnotification.ID(row.ID),
			ticketnotification.TenantID(row.TenantID),
			ticketnotification.StatusEQ(ticketNotificationStatusProcessing),
			ticketnotification.LeaseOwner(workerID),
		).
		SetStatus(ticketNotificationStatusFailed).
		SetLastErrorClass(errorClass).
		ClearLeaseOwner().
		ClearLeaseExpiresAt().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("ticket notification terminal failure update failed")
	}
	if affected != 1 {
		return fmt.Errorf("ticket notification lease lost")
	}
	return nil
}

func (s *TicketNotificationService) dispatchClaimedDelivery(ctx context.Context, row *ent.TicketNotification) string {
	ticketEntity, err := s.client.Ticket.Query().Where(ticket.ID(row.TicketID), ticket.TenantID(row.TenantID)).Only(ctx)
	if err != nil {
		return "delivery_target_invalid"
	}
	userEntity, err := s.client.User.Query().Where(user.ID(row.UserID), user.TenantID(row.TenantID), user.Active(true)).Only(ctx)
	if err != nil {
		return "delivery_target_invalid"
	}
	deliveryKey := ""
	if row.DeliveryKey != nil {
		deliveryKey = strings.TrimSpace(*row.DeliveryKey)
	}
	if deliveryKey == "" {
		return "delivery_target_invalid"
	}
	if row.Channel == "email" {
		if s.emailService == nil || strings.TrimSpace(userEntity.Email) == "" {
			return "delivery_target_invalid"
		}
		if _, err := mail.ParseAddress(userEntity.Email); err != nil {
			return "delivery_target_invalid"
		}
		// EmailService owns Graph-to-SMTP fallback. The stable internal key keeps
		// retries deterministic, while the external email effect remains at-least-once.
		if err := s.emailService.SendTicketNotificationForTenant(
			ctx,
			row.TenantID,
			[]string{userEntity.Email},
			ticketEntity.TicketNumber,
			ticketEntity.Title,
			row.Type,
			row.Content,
		); err != nil {
			return "connector_send"
		}
		return ""
	}
	if s.connectorManager == nil {
		return "connector_unavailable"
	}
	target := ticketNotificationTarget(row.Channel, userEntity)
	if target == "" {
		return "delivery_target_invalid"
	}
	if err := s.connectorManager.Send(ctx, row.TenantID, row.Channel, &connector.Message{
		ID:      deliveryKey,
		Channel: target,
		Type:    "text",
		Title:   "工单抄送",
		Content: row.Content,
		Actions: []connector.Action{{Type: "link", Text: "查看工单", URL: fmt.Sprintf("/tickets/%d", ticketEntity.ID)}},
		Metadata: map[string]interface{}{
			"delivery_key":  deliveryKey,
			"ticket_id":     ticketEntity.ID,
			"ticket_number": ticketEntity.TicketNumber,
			"event":         "ticket_cc",
		},
	}); err != nil {
		return "connector_send"
	}
	return ""
}

func ticketNotificationTarget(channel string, recipient *ent.User) string {
	if recipient == nil {
		return ""
	}
	switch channel {
	case "feishu":
		return recipient.FeishuOpenID
	case "dingtalk", "wecom":
		return recipient.Username
	case "webhook":
		return recipient.Email
	case "sms":
		return recipient.Phone
	default:
		return ""
	}
}

func (s *TicketNotificationService) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func validateTicketNotificationWorkerID(workerID string) error {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || len(workerID) > 255 {
		return fmt.Errorf("ticket notification worker id is invalid")
	}
	return nil
}

func isTicketNotificationErrorClass(errorClass string) bool {
	switch errorClass {
	case "connector_unavailable", "connector_send", "delivery_target_invalid", "unknown_error":
		return true
	default:
		return false
	}
}

func isTicketNotificationPermanentErrorClass(errorClass string) bool {
	return errorClass == "delivery_target_invalid"
}

func ticketNotificationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt >= 10 {
		return 5 * time.Minute
	}
	return time.Second << (attempt - 1)
}

// SetEmailService 设置邮件服务
func (s *TicketNotificationService) SetEmailService(emailService *EmailService) {
	s.emailService = emailService
}

// SetSMSService 设置短信服务
func (s *TicketNotificationService) SetSMSService(smsService *SMSService) {
	s.smsService = smsService
}

// SetNotificationPreferenceService 注入通知偏好服务（按 event_type 查偏好）
func (s *TicketNotificationService) SetNotificationPreferenceService(p *NotificationPreferenceService) {
	s.prefService = p
}

// SetWebSocketService 注入 WebSocket 服务（push 渠道）
func (s *TicketNotificationService) SetWebSocketService(w *WebSocketService) {
	s.wsService = w
}

// SendNotification 发送工单通知（按用户 event_type 偏好路由到多个渠道）
func (s *TicketNotificationService) SendNotification(
	ctx context.Context,
	ticketID int,
	req *dto.SendTicketNotificationRequest,
	tenantID int,
) error {
	s.logger.Infow("Sending ticket notification", "ticket_id", ticketID, "event_type", req.EventType)

	// 验证工单是否存在
	ticketEntity, err := s.client.Ticket.Query().
		Where(
			ticket.ID(ticketID),
			ticket.TenantID(tenantID),
		).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("ticket not found")
	}

	now := time.Now()
	for _, userID := range req.UserIDs {
		// 验证用户是否存在
		userEntity, err := s.client.User.Query().Where(user.ID(userID), user.TenantID(tenantID)).Only(ctx)
		if err != nil || userEntity == nil {
			s.logger.Warnw("User not found, skipping notification", "user_id", userID)
			continue
		}
		if req.DeliveryKey != "" {
			exists, err := s.client.TicketNotification.Query().Where(
				ticketnotification.TenantID(tenantID),
				ticketnotification.TicketID(ticketID),
				ticketnotification.UserID(userID),
				ticketnotification.DeliveryKey(req.DeliveryKey),
			).Exist(ctx)
			if err != nil {
				return fmt.Errorf("failed to check notification delivery state")
			}
			if exists {
				continue
			}
		}

		// 查该用户对该事件类型的偏好（带默认值兜底）
		prefs := s.resolvePreferences(ctx, userID, tenantID, req.EventType)
		if req.InAppOnly {
			prefs = &dto.NotificationPreferenceResponse{InAppEnabled: prefs.InAppEnabled}
		}

		// 1. 站内信：总是创建通知记录（现有语义）
		if prefs.InAppEnabled {
			s.createInAppNotification(ctx, ticketID, userID, req, tenantID, now)
		}

		// 2. 邮件
		if prefs.EmailEnabled && s.emailService != nil && userEntity.Email != "" {
			if err := s.emailService.SendTicketNotificationForTenant(
				ctx,
				tenantID,
				[]string{userEntity.Email},
				ticketEntity.TicketNumber,
				ticketEntity.Title,
				req.EventType,
				req.Content,
			); err != nil {
				s.logger.Errorw("ticket email notification failed", "error_class", emailErrorClassDelivery)
			}
		}

		// 3. 短信
		if prefs.SmsEnabled && s.smsService != nil && userEntity.Phone != "" {
			if err := s.smsService.SendTicketNotification(
				ctx,
				[]string{userEntity.Phone},
				ticketEntity.TicketNumber,
				req.EventType,
			); err != nil {
				s.logger.Errorw("Failed to send SMS notification", "error", err, "user_id", userID)
			}
		}

		// 4. push（WebSocket 实时推送）
		if prefs.PushEnabled && s.wsService != nil {
			s.wsService.GetHub().SendToUser(userID, WebSocketMessage{
				Type:    req.EventType,
				Payload: map[string]interface{}{"ticket_id": ticketID, "content": req.Content},
			})
		}
	}

	return nil
}

// resolvePreferences 解析用户偏好；偏好服务未注入或查询失败时回退默认偏好。
func (s *TicketNotificationService) resolvePreferences(
	ctx context.Context, userID, tenantID int, eventType string,
) *dto.NotificationPreferenceResponse {
	if s.prefService != nil {
		prefs, err := s.prefService.GetUserPreferenceByEventType(ctx, userID, tenantID, eventType)
		if err == nil && prefs != nil {
			return prefs
		}
		if err != nil {
			s.logger.Warnw("Failed to get preference, using defaults", "user_id", userID, "event_type", eventType, "error", err)
		}
	}
	return &dto.NotificationPreferenceResponse{
		EmailEnabled: true,
		InAppEnabled: true,
		SmsEnabled:   false,
		PushEnabled:  false,
	}
}

// createInAppNotification 创建站内通知记录（TicketNotification + Notification）并标记已发送。
func (s *TicketNotificationService) createInAppNotification(
	ctx context.Context, ticketID, userID int,
	req *dto.SendTicketNotificationRequest, tenantID int, now time.Time,
) {
	create := s.client.TicketNotification.Create().
		SetTicketID(ticketID).
		SetUserID(userID).
		SetType(req.EventType).
		SetChannel("in_app").
		SetContent(req.Content).
		SetTenantID(tenantID).
		SetStatus("pending")
	if req.DeliveryKey != "" {
		create.SetDeliveryKey(req.DeliveryKey)
	}
	notificationEntity, err := create.Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create notification", "error", err, "user_id", userID)
		return
	}

	// 同步创建到通用 notifications 表（供前端统一查询）
	unifiedCreate := s.client.Notification.Create().
		SetTitle(req.EventType).
		SetMessage(req.Content).
		SetType(req.EventType).
		SetUserID(userID).
		SetTenantID(tenantID).
		SetNillableActionURL(ticketNotificationStringPtr(fmt.Sprintf("/tickets/%d", ticketID))).
		SetNillableActionText(ticketNotificationStringPtr("查看工单"))
	if req.DeliveryKey != "" {
		unifiedCreate.SetDeliveryKey(req.DeliveryKey)
	}
	if _, err := unifiedCreate.Save(ctx); err != nil {
		s.logger.Warnw("Failed to create unified notification", "user_id", userID)
	}

	// 站内消息立即标记为已发送
	_, err = s.client.TicketNotification.UpdateOneID(notificationEntity.ID).
		SetStatus("sent").
		SetNillableSentAt(&now).
		Save(ctx)
	if err != nil {
		s.logger.Warnw("Failed to update notification status", "error", err)
	}
}

// NotifyTicketCreated 工单创建时发送通知
// 通知目标:
//  1. 工单处理人(AssigneeID),如果有
//  2. 工单创建人(ReporterID),如果是普通用户
//  3. 所有租户内管理员(如果没有处理人)
func (s *TicketNotificationService) NotifyTicketCreated(ctx context.Context, ticket *ent.Ticket) error {
	userIDs := []int{}

	// 1. 处理人
	if ticket.AssigneeID > 0 {
		userIDs = append(userIDs, ticket.AssigneeID)
	}

	// 2. 创建人(去重)
	if ticket.RequesterID > 0 {
		dup := false
		for _, id := range userIDs {
			if id == ticket.RequesterID {
				dup = true
				break
			}
		}
		if !dup {
			userIDs = append(userIDs, ticket.RequesterID)
		}
	}

	// 3. 如果只有创建人(没有处理人),广播给所有admin
	if len(userIDs) <= 1 && ticket.RequesterID > 0 {
		admins, err := s.client.User.Query().
			Where(user.TenantID(ticket.TenantID)).
			Where(user.IDNEQ(ticket.RequesterID)).
			All(ctx)
		if err == nil {
			for _, admin := range admins {
				dup := false
				for _, id := range userIDs {
					if id == admin.ID {
						dup = true
						break
					}
				}
				if !dup {
					userIDs = append(userIDs, admin.ID)
				}
			}
		}
	}

	if len(userIDs) == 0 {
		return nil
	}

	content := fmt.Sprintf("新工单已创建：%s (#%s)", ticket.Title, ticket.TicketNumber)
	return s.SendNotification(ctx, ticket.ID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "ticket_created",
		Content:   content,
	}, ticket.TenantID)
}

// NotifyTicketAssigned 工单分配时发送通知
func (s *TicketNotificationService) NotifyTicketAssigned(ctx context.Context, ticketID, assigneeID, tenantID int) error {
	ticket, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("failed to get ticket: %w", err)
	}

	content := fmt.Sprintf("您被分配了工单：%s (#%s)", ticket.Title, ticket.TicketNumber)
	return s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{assigneeID},
		EventType: "ticket_assigned",
		Content:   content,
	}, tenantID)
}

// NotifyTicketStatusChanged 工单状态变更时发送通知
func (s *TicketNotificationService) NotifyTicketStatusChanged(
	ctx context.Context,
	ticketID int,
	oldStatus, newStatus string,
	tenantID int,
) error {
	ticket, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("failed to get ticket: %w", err)
	}

	content := fmt.Sprintf("工单 #%s 状态已从 %s 变更为 %s", ticket.TicketNumber, oldStatus, newStatus)
	userIDs := []int{ticket.RequesterID}
	if ticket.AssigneeID > 0 && ticket.AssigneeID != ticket.RequesterID {
		userIDs = append(userIDs, ticket.AssigneeID)
	}

	return s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "ticket_updated",
		Content:   content,
	}, tenantID)
}

// NotifyTicketCommented 工单评论时发送通知
func (s *TicketNotificationService) NotifyTicketCommented(
	ctx context.Context,
	ticketID int,
	commenterID int,
	mentionedUserIDs []int,
	tenantID int,
) error {
	ticket, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("failed to get ticket: %w", err)
	}

	// 通知工单相关人员和被@的用户
	userIDs := []int{ticket.RequesterID}
	if ticket.AssigneeID > 0 && ticket.AssigneeID != commenterID {
		userIDs = append(userIDs, ticket.AssigneeID)
	}

	// 添加被@的用户（排除评论者自己）
	for _, userID := range mentionedUserIDs {
		if userID != commenterID {
			exists := false
			for _, id := range userIDs {
				if id == userID {
					exists = true
					break
				}
			}
			if !exists {
				userIDs = append(userIDs, userID)
			}
		}
	}

	if len(userIDs) == 0 {
		return nil
	}

	content := fmt.Sprintf("工单 #%s 有新的评论", ticket.TicketNumber)
	return s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "comment_added",
		Content:   content,
	}, tenantID)
}

// NotifySLAWarning SLA即将到期时发送提醒
func (s *TicketNotificationService) NotifySLAWarning(
	ctx context.Context,
	ticketID int,
	warningType string, // response_deadline, resolution_deadline
	deadline time.Time,
	tenantID int,
) error {
	ticket, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("failed to get ticket: %w", err)
	}

	content := fmt.Sprintf("工单 #%s 的SLA %s 即将在 %s 到期",
		ticket.TicketNumber,
		map[string]string{
			"response_deadline":   "响应时间",
			"resolution_deadline": "解决时间",
		}[warningType],
		deadline.Format("2006-01-02 15:04:05"))

	userIDs := []int{ticket.RequesterID}
	if ticket.AssigneeID > 0 {
		userIDs = append(userIDs, ticket.AssigneeID)
	}

	return s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "sla_warning",
		Content:   content,
	}, tenantID)
}

// NotifySLABreached SLA违规时发送通知
func (s *TicketNotificationService) NotifySLABreached(
	ctx context.Context,
	ticketID int,
	violationType string, // response_time, resolution_time
	exceededMinutes float64,
	tenantID int,
) error {
	ticket, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("failed to get ticket: %w", err)
	}

	slaType := map[string]string{
		"response_time":   "响应时间",
		"resolution_time": "解决时间",
	}[violationType]

	content := fmt.Sprintf("【SLA违规】工单 #%s 的%s已违反SLA，超时 %.1f 分钟",
		ticket.TicketNumber, slaType, exceededMinutes)

	// 获取需要通知的用户列表（创建人、处理人、相关经理）
	userIDs := []int{ticket.RequesterID}
	if ticket.AssigneeID > 0 {
		userIDs = append(userIDs, ticket.AssigneeID)
	}

	// 根据配置的通知渠道发送
	// 1. 站内消息
	if err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "sla_violated",
		Content:   content,
	}, tenantID); err != nil {
		s.logger.Errorw("Failed to send in-app SLA breach notification", "error", err)
	}

	// 2. 邮件通知
	if s.emailService != nil {
		// 获取所有需要通知的用户邮箱
		var emails []string
		for _, userID := range userIDs {
			userEntity, _ := s.client.User.Get(ctx, userID)
			if userEntity != nil && userEntity.Email != "" {
				emails = append(emails, userEntity.Email)
			}
		}
		if len(emails) > 0 {
			if err := s.emailService.SendTicketNotificationForTenant(ctx, tenantID, emails, ticket.TicketNumber, ticket.Title, "sla_breached", content); err != nil {
				s.logger.Warnw("SLA breach email notification failed", "error_class", emailErrorClassDelivery, "ticket_id", ticketID)
			}
		}
	}

	// 3. 短信通知（严重级别时）
	if exceededMinutes > 60 && s.smsService != nil {
		var phones []string
		for _, userID := range userIDs {
			userEntity, _ := s.client.User.Get(ctx, userID)
			if userEntity != nil && userEntity.Phone != "" {
				phones = append(phones, userEntity.Phone)
			}
		}
		if len(phones) > 0 {
			smsContent := fmt.Sprintf("【ITSM系统】SLA告警：工单 %s 的%s已超时 %.1f 分钟，请立即处理！",
				ticket.TicketNumber, slaType, exceededMinutes)
			if err := s.smsService.Send(ctx, &SMSMessage{
				PhoneNumbers: phones,
				Content:      smsContent,
			}); err != nil {
				s.logger.Warnw("failed to send SLA breach SMS notification", "error", err, "ticket_id", ticketID)
			}
		}
	}

	return nil
}

// NotifySLAAlertLevelChanged SLA预警级别变更时发送通知
func (s *TicketNotificationService) NotifySLAAlertLevelChanged(
	ctx context.Context,
	ticketID int,
	alertLevel string, // warning, critical
	percentage float64,
	tenantID int,
) error {
	ticket, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("failed to get ticket: %w", err)
	}

	levelText := map[string]string{
		"warning":  "警告",
		"critical": "严重",
	}[alertLevel]

	content := fmt.Sprintf("【SLA%s】工单 #%s 剩余时间不足 %.1f%%，请及时处理！",
		levelText, ticket.TicketNumber, percentage)

	userIDs := []int{ticket.RequesterID}
	if ticket.AssigneeID > 0 {
		userIDs = append(userIDs, ticket.AssigneeID)
	}

	return s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "sla_violated",
		Content:   content,
	}, tenantID)
}

// ListTicketNotifications 获取工单通知列表
func (s *TicketNotificationService) ListTicketNotifications(
	ctx context.Context,
	ticketID, tenantID int,
) ([]*dto.TicketNotificationResponse, error) {
	notifications, err := s.client.TicketNotification.Query().
		Where(
			ticketnotification.TicketID(ticketID),
			ticketnotification.TenantID(tenantID),
		).
		Order(ent.Desc(ticketnotification.FieldCreatedAt)).
		WithUser().
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to list ticket notifications", "error", err)
		return nil, fmt.Errorf("failed to list ticket notifications: %w", err)
	}

	responses := make([]*dto.TicketNotificationResponse, 0, len(notifications))
	for _, notification := range notifications {
		var userEntity *ent.User
		if notification.Edges.User != nil {
			userEntity = notification.Edges.User
		} else {
			userEntity, err = s.client.User.Get(ctx, notification.UserID)
			if err != nil {
				s.logger.Warnw("failed to get user for notification response", "error", err, "user_id", notification.UserID)
			}
		}
		responses = append(responses, dto.ToTicketNotificationResponse(notification, userEntity))
	}

	return responses, nil
}

// ListUserNotifications 获取用户通知列表
func (s *TicketNotificationService) ListUserNotifications(
	ctx context.Context,
	userID, tenantID int,
	page, pageSize int,
	read *bool,
) ([]*dto.TicketNotificationResponse, int, error) {
	query := s.client.TicketNotification.Query().
		Where(
			ticketnotification.UserID(userID),
			ticketnotification.TenantID(tenantID),
		)

	if read != nil {
		if *read {
			query = query.Where(ticketnotification.ReadAtNotNil())
		} else {
			query = query.Where(ticketnotification.ReadAtIsNil())
		}
	}

	// 获取总数
	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count notifications: %w", err)
	}

	// 分页查询
	notifications, err := query.
		Order(ent.Desc(ticketnotification.FieldCreatedAt)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		WithUser().
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to list user notifications", "error", err)
		return nil, 0, fmt.Errorf("failed to list user notifications: %w", err)
	}

	responses := make([]*dto.TicketNotificationResponse, 0, len(notifications))
	for _, notification := range notifications {
		var userEntity *ent.User
		if notification.Edges.User != nil {
			userEntity = notification.Edges.User
		} else {
			userEntity, err = s.client.User.Get(ctx, notification.UserID)
			if err != nil {
				s.logger.Warnw("failed to get user for notification response", "error", err, "user_id", notification.UserID)
			}
		}
		responses = append(responses, dto.ToTicketNotificationResponse(notification, userEntity))
	}

	return responses, total, nil
}

// MarkNotificationRead 标记通知为已读
func (s *TicketNotificationService) MarkNotificationRead(
	ctx context.Context,
	notificationID, userID, tenantID int,
) error {
	_, err := s.client.TicketNotification.Query().
		Where(
			ticketnotification.ID(notificationID),
			ticketnotification.UserID(userID),
			ticketnotification.TenantID(tenantID),
		).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("notification not found: %w", err)
	}

	now := time.Now()
	_, err = s.client.TicketNotification.UpdateOneID(notificationID).
		SetNillableReadAt(&now).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to mark notification as read", "error", err)
		return fmt.Errorf("failed to mark notification as read: %w", err)
	}

	return nil
}

// MarkAllNotificationsRead 标记所有通知为已读
func (s *TicketNotificationService) MarkAllNotificationsRead(
	ctx context.Context,
	userID, tenantID int,
) error {
	now := time.Now()
	_, err := s.client.TicketNotification.Update().
		Where(
			ticketnotification.UserID(userID),
			ticketnotification.TenantID(tenantID),
			ticketnotification.ReadAtIsNil(),
		).
		SetNillableReadAt(&now).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to mark all notifications as read", "error", err)
		return fmt.Errorf("failed to mark all notifications as read: %w", err)
	}

	return nil
}

// GetUserNotificationPreferences 获取用户通知偏好
func (s *TicketNotificationService) GetUserNotificationPreferences(
	ctx context.Context,
	userID int,
) (*dto.NotificationPreferencesResponse, error) {
	// 注意：用户通知偏好存储在用户表的 preferences JSON 字段中
	// 如果需要单独的 preference 表，可以在未来版本中实现
	return &dto.NotificationPreferencesResponse{
		UserID:         userID,
		EmailEnabled:   true,
		InAppEnabled:   true,
		SmsEnabled:     false,
		SlaWarningTime: 30, // 默认30分钟
	}, nil
}

// UpdateUserNotificationPreferences 更新用户通知偏好
func (s *TicketNotificationService) UpdateUserNotificationPreferences(
	ctx context.Context,
	userID int,
	req *dto.UpdateNotificationPreferencesRequest,
) (*dto.NotificationPreferencesResponse, error) {
	// 注意：偏好应该保存到用户表的 preferences 字段
	// 当前实现仅返回更新后的值
	return &dto.NotificationPreferencesResponse{
		UserID:         userID,
		EmailEnabled:   req.EmailEnabled,
		InAppEnabled:   req.InAppEnabled,
		SmsEnabled:     req.SmsEnabled,
		SlaWarningTime: req.SlaWarningTime,
	}, nil
}

// SendAssignmentNotification 发送工单分配通知
func (s *TicketNotificationService) SendAssignmentNotification(ticketID, assigneeID, assignedBy int) {
	if s == nil {
		return
	}
	ctx := context.Background()
	content := fmt.Sprintf("您被分配了工单 #%d", ticketID)

	tenantID := s.resolveTenantID(ctx, ticketID)
	if err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{assigneeID},
		EventType: "ticket_assigned",
		Content:   content,
	}, tenantID); err != nil {
		s.logger.Warnw("failed to send assignment notification", "error", err, "ticket_id", ticketID)
	}
}

// SendEscalationNotification 发送工单升级通知
func (s *TicketNotificationService) SendEscalationNotification(ticketID, newAssignee, escalatedBy int, reason string) {
	if s == nil {
		return
	}
	ctx := context.Background()
	content := fmt.Sprintf("工单 #%d 已被升级，新处理人: %d", ticketID, newAssignee)

	tenantID := s.resolveTenantID(ctx, ticketID)
	if err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{newAssignee},
		EventType: "ticket_updated",
		Content:   content,
	}, tenantID); err != nil {
		s.logger.Warnw("failed to send escalation notification", "error", err, "ticket_id", ticketID)
	}
}

// SendResolutionNotification 发送工单解决通知
func (s *TicketNotificationService) SendResolutionNotification(ticketID, requesterID, resolvedBy int) {
	if s == nil {
		return
	}
	ctx := context.Background()
	content := fmt.Sprintf("工单 #%d 已被解决", ticketID)

	tenantID := s.resolveTenantID(ctx, ticketID)
	if err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{requesterID},
		EventType: "ticket_resolved",
		Content:   content,
	}, tenantID); err != nil {
		s.logger.Warnw("failed to send resolution notification", "error", err, "ticket_id", ticketID)
	}
}

// resolveTenantID resolves the tenant ID for a ticket from the database.
// Returns 0 if the ticket cannot be found (callers should handle this appropriately).
func (s *TicketNotificationService) resolveTenantID(ctx context.Context, ticketID int) int {
	ticketEntity, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		s.logger.Warnw("failed to resolve tenant ID for ticket", "error", err, "ticket_id", ticketID)
		return 0
	}
	return ticketEntity.TenantID
}
