package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/connector"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketnotification"
	"itsm-backend/ent/user"
	"itsm-backend/service/bpmn"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

var (
	ErrTicketNotificationNotFound = errors.New("ticket notification not found")
	ErrTicketNotificationStorage  = errors.New("ticket notification storage failed")
)

const ticketNotificationReadStorageErrorClass = "ticket_notification_read_storage"

func ticketNotificationStringPtr(s string) *string {
	return &s
}

type TicketNotificationService struct {
	assignmentDirectory database.DirectorySnapshot
	queueClient         *ent.Client
	execution           *database.ExecutionPolicy
	client              *ent.Client
	logger              *zap.SugaredLogger
	connectorManager    *connector.Manager
	emailService        *EmailService
	smsService          *SMSService
	prefService         *NotificationPreferenceService // 按 event_type 查偏好
	wsService           *WebSocketService              // push 渠道（WebSocket）
	now                 func() time.Time
}

// NewTicketNotificationService 创建通知服务
func NewTicketNotificationService(client *ent.Client, logger *zap.SugaredLogger, execution *database.ExecutionPolicy) *TicketNotificationService {
	return &TicketNotificationService{
		execution: execution,
		client:    client,
		logger:    logger,
		now:       time.Now,
	}
}

// SetAssignmentDirectory injects the existing restricted directory snapshot for
// assignment recipients, including allocated MSP technicians.
func (s *TicketNotificationService) SetAssignmentDirectory(directory database.DirectorySnapshot) {
	s.assignmentDirectory = directory
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

// SetDeliveryQueueClient selects the restricted SELECT/UPDATE queue capability.
// Domain lookups and producer writes remain on the tenant client.
func (s *TicketNotificationService) SetDeliveryQueueClient(client *ent.Client) {
	s.queueClient = client
}

// ProcessPendingDeliveries performs one deterministic durable notification sweep.
func (s *TicketNotificationService) ProcessPendingDeliveries(ctx context.Context, workerID string, limit int) (int, error) {
	if err := validateTicketNotificationWorkerID(workerID); err != nil {
		return 0, err
	}
	if s.queueClient == nil {
		return 0, fmt.Errorf("ticket notification queue client is required")
	}
	if limit <= 0 {
		return 0, nil
	}

	ctx = tenantctx.SystemContext(ctx, "notification:poll", "claim and acknowledge scoped ticket notifications")
	now := s.clock()
	scanTx, err := s.queueClient.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer scanTx.Rollback()
	scope, err := s.execution.WorkerPredicate(ctx, scanTx, ticketnotification.FieldTenantID, ticketnotification.FieldTicketID)
	if err != nil {
		return 0, err
	}
	candidates, err := scanTx.TicketNotification.Query().Where(scope).
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

	if err := scanTx.Rollback(); err != nil {
		return 0, err
	}
	completed := 0
	failed := false
	var causes []error
	for _, row := range candidates {
		rowCtx := tenantctx.WithTenantID(ctx, row.TenantID)
		if err := s.execution.RequireCapability(rowCtx, row.TenantID, "notification"); err != nil {
			causes = append(causes, err)
			failed = true
			continue
		}
		if row.Status == ticketNotificationStatusProcessing {
			changed, err := s.recoverExpiredDelivery(ctx, row, now)
			if err != nil || changed > 0 {
				causes = append(causes, err)
				failed = true
			}
			continue
		}
		claimed, claimErr := s.claimDelivery(ctx, workerID, row)
		if claimErr != nil {
			causes = append(causes, claimErr)
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
		errorClass, dispatchErr := s.dispatchClaimedDelivery(rowCtx, &claimedRow)
		causes = append(causes, dispatchErr)
		if errorClass != "" {
			failed = true
			if isTicketNotificationPermanentErrorClass(errorClass) {
				causes = append(causes, s.failDelivery(ctx, workerID, &claimedRow, errorClass))
			} else {
				causes = append(causes, s.retryDelivery(ctx, workerID, &claimedRow, errorClass))
			}
			continue
		}
		completedRow, completeErr := s.completeDelivery(ctx, workerID, &claimedRow)
		if completeErr != nil || !completedRow {
			causes = append(causes, completeErr)
			failed = true
			continue
		}
		completed++
	}
	if failed {
		return completed, errors.Join(fmt.Errorf("one or more ticket notifications were not completed"), errors.Join(causes...))
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
	tx, err := s.queueClient.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	scope, err := s.execution.WorkerPredicate(ctx, tx, ticketnotification.FieldTenantID, ticketnotification.FieldTicketID)
	if err != nil {
		return false, err
	}
	affected, err := tx.TicketNotification.Update().Where(scope).
		Where(
			ticketnotification.ID(row.ID),
			ticketnotification.TenantID(row.TenantID),
			ticketnotification.DeliveryKeyNotNil(),
			ticketnotification.StatusEQ(ticketNotificationStatusPending), ticketnotification.NextAttemptAtLTE(now),
		).
		SetStatus(ticketNotificationStatusProcessing).
		SetLeaseOwner(workerID).
		SetLeaseExpiresAt(now.Add(ticketNotificationLeaseDuration)).
		AddAttemptCount(1).
		Save(ctx)
	if err != nil {
		return false, fmt.Errorf("ticket notification claim failed")
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (s *TicketNotificationService) completeDelivery(ctx context.Context, workerID string, row *ent.TicketNotification) (bool, error) {
	tx, err := s.queueClient.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	scope, err := s.execution.WorkerPredicate(ctx, tx, ticketnotification.FieldTenantID, ticketnotification.FieldTicketID)
	if err != nil {
		return false, err
	}
	affected, err := tx.TicketNotification.Update().Where(scope).
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
		return false, fmt.Errorf("ticket notification completion failed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (s *TicketNotificationService) retryDelivery(ctx context.Context, workerID string, row *ent.TicketNotification, errorClass string) error {
	if !isTicketNotificationErrorClass(errorClass) {
		errorClass = "unknown_error"
	}
	tx, err := s.queueClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	scope, err := s.execution.WorkerPredicate(ctx, tx, ticketnotification.FieldTenantID, ticketnotification.FieldTicketID)
	if err != nil {
		return err
	}
	affected, err := tx.TicketNotification.Update().Where(scope).
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
		return fmt.Errorf("ticket notification retry scheduling failed: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("ticket notification lease lost")
	}
	return tx.Commit()
}

func (s *TicketNotificationService) failDelivery(ctx context.Context, workerID string, row *ent.TicketNotification, errorClass string) error {
	if !isTicketNotificationPermanentErrorClass(errorClass) {
		errorClass = "unknown_error"
	}
	tx, err := s.queueClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	scope, err := s.execution.WorkerPredicate(ctx, tx, ticketnotification.FieldTenantID, ticketnotification.FieldTicketID)
	if err != nil {
		return err
	}
	affected, err := tx.TicketNotification.Update().Where(scope).
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
		return fmt.Errorf("ticket notification terminal failure update failed: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("ticket notification lease lost")
	}
	return tx.Commit()
}

func (s *TicketNotificationService) dispatchClaimedDelivery(ctx context.Context, row *ent.TicketNotification) (string, error) {
	ticketEntity, err := s.client.Ticket.Query().Where(ticket.ID(row.TicketID), ticket.TenantID(row.TenantID)).Only(ctx)
	if err != nil {
		return "delivery_target_invalid", nil
	}
	var userEntity *ent.User
	// Persisted recipient identity was authorized when the intent was written.
	// Recheck its current native/directory allocation in one read snapshot; owner
	// changes do not rewrite the recipient of an already materialized intent.
	tx, openErr := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if openErr != nil {
		return "delivery_target_invalid", openErr
	}
	userEntity, err = s.currentNotificationRecipient(ctx, tx, row.UserID, row.TenantID)
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		return "delivery_target_invalid", nil
	}
	deliveryKey := ""
	if row.DeliveryKey != nil {
		deliveryKey = strings.TrimSpace(*row.DeliveryKey)
	}
	if deliveryKey == "" {
		return "delivery_target_invalid", nil
	}
	if row.Channel == "email" {
		target, targetErr := notificationEmailTarget(row)
		if targetErr != nil {
			return "delivery_target_invalid", targetErr
		}
		if s.emailService == nil || strings.TrimSpace(userEntity.Email) == "" {
			return "delivery_target_invalid", nil
		}
		if _, err := mail.ParseAddress(userEntity.Email); err != nil {
			return "delivery_target_invalid", nil
		}
		// Content and recipient identity are durable queue facts. Resolve only
		// the current address of that same active recipient at delivery time.
		message := &EmailMessage{To: []string{userEntity.Email}, Subject: fmt.Sprintf("[ITSM] 工单 %s - %s", ticketEntity.TicketNumber, row.Type), BodyText: row.Content, DeliveryID: deliveryKey, DisableProviderFallback: true}
		if err := s.emailService.SendToTarget(ctx, row.TenantID, "notification", target, message); err != nil {
			if emailTransportOutcomeOf(err) == emailAcceptanceUnknown {
				return "delivery_unknown", err
			}
			if errors.Is(err, executionscope.ErrDenied) {
				return "delivery_target_invalid", err
			}
			return "connector_send", err
		}
		return "", nil
	}
	if row.Channel == "push" {
		if s.wsService == nil {
			return "connector_unavailable", nil
		}
		if err := s.wsService.GetHub().DeliverToUser(ctx, row.TenantID, row.UserID, WebSocketMessage{Type: row.Type, Payload: map[string]interface{}{"ticket_id": row.TicketID, "content": row.Content}}); err != nil {
			if errors.Is(err, errPushNotAccepted) {
				return "connector_unavailable", err
			}
			return "delivery_unknown", err
		}
		return "", nil
	}
	if s.connectorManager == nil {
		return "connector_unavailable", nil
	}
	target := ticketNotificationTarget(row.Channel, userEntity)
	if target == "" {
		return "delivery_target_invalid", nil
	}
	bound, generation, err := s.resolveNotificationConnectorTarget(ctx, row)
	if err != nil {
		if errors.Is(err, executionscope.ErrDenied) {
			return "delivery_target_invalid", err
		}
		return "connector_unavailable", err
	}
	if err := bound.Send(ctx, &connector.Message{
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
		if emailTransportOutcomeOf(err) == emailNotAccepted {
			return "connector_send", nil
		}
		return "delivery_unknown", nil
	}
	_, currentGeneration, err := s.resolveNotificationConnectorTarget(ctx, row)
	if err != nil || currentGeneration != generation {
		return "delivery_unknown", nil
	}
	return "", nil
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
	case "connector_unavailable", "connector_send", "delivery_target_invalid", "unknown_error", "delivery_unknown":
		return true
	default:
		return false
	}
}

func isTicketNotificationPermanentErrorClass(errorClass string) bool {
	return errorClass == "delivery_target_invalid" || errorClass == "delivery_unknown"
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
) (*dto.SendTicketNotificationResult, error) {
	if s == nil || req == nil || s.client == nil {
		return nil, fmt.Errorf("ticket notification request and client are required")
	}
	request := *req
	request.UserIDs = uniqueTicketNotificationUserIDs(req.UserIDs)
	if len(request.UserIDs) == 0 {
		return blockedTicketNotificationResult(0, bpmn.CallbackBlockRecipientEmpty), nil
	}
	// This identity belongs to this invocation only; it is not HTTP retry deduplication.
	if request.DeliveryKey == "" {
		request.DeliveryKey = "notification:" + uuid.NewString()
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("ticket notification transaction begin failed: %w", err)
	}
	defer tx.Rollback()
	result, err := s.enqueueNotificationResultTx(ctx, tx, ticketID, tenantID, &request, nil, 0)
	if errors.Is(err, errNotificationRecipientMissing) {
		return blockedTicketNotificationResult(len(request.UserIDs), bpmn.CallbackBlockRecipientMissing), nil
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("ticket notification transaction commit failed: %w", err)
	}
	return result, nil
}

func uniqueTicketNotificationUserIDs(userIDs []int) []int {
	seen := make(map[int]struct{}, len(userIDs))
	result := make([]int, 0, len(userIDs))
	for _, userID := range userIDs {
		if _, ok := seen[userID]; !ok {
			seen[userID] = struct{}{}
			result = append(result, userID)
		}
	}
	return result
}

func blockedTicketNotificationResult(recipientCount int, code bpmn.CallbackBlockCode) *dto.SendTicketNotificationResult {
	return &dto.SendTicketNotificationResult{Effect: dto.TicketNotificationEffectBlocked, RecipientCount: recipientCount, BlockCode: string(code)}
}

func ticketNotificationDeliveryError(result *dto.SendTicketNotificationResult, err error) error {
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("ticket notification result is missing")
	}
	if result.Effect == dto.TicketNotificationEffectQueued || result.Effect == dto.TicketNotificationEffectApplied || result.Effect == dto.TicketNotificationEffectIdempotent {
		return nil
	}
	if result.Effect == dto.TicketNotificationEffectBlocked && bpmn.IsAllowedCallbackBlockCode(bpmn.CallbackBlockCode(result.BlockCode)) {
		return fmt.Errorf("ticket notification blocked: %s", result.BlockCode)
	}
	return fmt.Errorf("ticket notification result is invalid")
}

// resolvePreferences 解析用户偏好；偏好服务未注入或查询失败时回退默认偏好。
func (s *TicketNotificationService) resolvePreferences(
	ctx context.Context, userID, tenantID int, eventType string,
) (*dto.NotificationPreferenceResponse, error) {
	if s.prefService != nil {
		prefs, err := s.prefService.GetUserPreferenceByEventType(ctx, userID, tenantID, eventType)
		if err == nil && prefs != nil {
			return prefs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("ticket notification preference lookup failed: %w", err)
		}
	}
	return &dto.NotificationPreferenceResponse{
		EmailEnabled: true,
		InAppEnabled: true,
		SmsEnabled:   false,
		PushEnabled:  false,
	}, nil
}

// createInAppNotification 创建站内通知记录（TicketNotification + Notification）并标记已发送。
func createInAppNotificationPair(
	ctx context.Context, client *ent.Client, ticketID, userID int,
	req *dto.SendTicketNotificationRequest, tenantID int, now time.Time,
) error {
	create := client.TicketNotification.Create().
		SetNillableSLAAlertHistoryID(req.SLAAlertHistoryID).
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
		return fmt.Errorf("ticket notification delivery write failed: %w", err)
	}

	// 同步创建到通用 notifications 表（供前端统一查询）
	unifiedCreate := client.Notification.Create().
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
		return fmt.Errorf("unified notification write failed: %w", err)
	}

	// 站内消息立即标记为已发送
	_, err = client.TicketNotification.UpdateOneID(notificationEntity.ID).
		SetStatus("sent").
		SetNillableSentAt(&now).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("ticket notification completion write failed: %w", err)
	}
	return nil
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
	result, err := s.SendNotification(ctx, ticket.ID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "ticket_created",
		Content:   content,
	}, ticket.TenantID)
	return ticketNotificationDeliveryError(result, err)
}

// NotifyTicketAssigned 工单分配时发送通知
func (s *TicketNotificationService) NotifyTicketAssigned(ctx context.Context, ticketID, assigneeID, tenantID int) error {
	ticket, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("failed to get ticket: %w", err)
	}

	content := fmt.Sprintf("您被分配了工单：%s (#%s)", ticket.Title, ticket.TicketNumber)
	result, err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{assigneeID},
		EventType: "ticket_assigned",
		Content:   content,
	}, tenantID)
	return ticketNotificationDeliveryError(result, err)
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

	result, err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "ticket_updated",
		Content:   content,
	}, tenantID)
	return ticketNotificationDeliveryError(result, err)
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
	result, err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "comment_added",
		Content:   content,
	}, tenantID)
	return ticketNotificationDeliveryError(result, err)
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

	result, err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   userIDs,
		EventType: "sla_warning",
		Content:   content,
	}, tenantID)
	return ticketNotificationDeliveryError(result, err)
}

// EnqueueSLABreachedTx records breach notifications with their owning violation.
func (s *TicketNotificationService) EnqueueSLABreachedTx(ctx context.Context, tx *ent.Tx, item *ent.Ticket, violationID int, violationType string, exceededMinutes float64) error {
	slaType := map[string]string{"response_time": "响应时间", "resolution_time": "解决时间"}[violationType]
	if slaType == "" || violationID <= 0 {
		return fmt.Errorf("invalid SLA violation notification")
	}
	users := []int{item.RequesterID}
	if item.AssigneeID > 0 {
		users = append(users, item.AssigneeID)
	}
	return s.EnqueueNotificationTx(ctx, tx, item.ID, item.TenantID, &dto.SendTicketNotificationRequest{
		UserIDs: users, EventType: "sla_violated", DeliveryKey: fmt.Sprintf("sla-violation:%d", violationID),
		Content: fmt.Sprintf("【SLA违规】工单 #%s 的%s已违反SLA，超时 %.1f 分钟", item.TicketNumber, slaType, exceededMinutes),
	})
}

// EnqueueSLAAlertTx honors the rule's declared channels and user preferences.
func (s *TicketNotificationService) EnqueueSLAAlertTx(ctx context.Context, tx *ent.Tx, item *ent.Ticket, history *ent.SLAAlertHistory, channels []string) error {
	if err := validateIncidentAlertChannels(channels); err != nil {
		return err
	}
	selected := make(map[string]bool, len(channels))
	for _, channel := range channels {
		selected[channel] = true
	}
	users := []int{item.RequesterID}
	if item.AssigneeID > 0 {
		users = append(users, item.AssigneeID)
	}
	return s.enqueueNotificationTx(ctx, tx, item.ID, item.TenantID, &dto.SendTicketNotificationRequest{
		UserIDs: users, EventType: "sla_violated", DeliveryKey: fmt.Sprintf("sla-alert:%d", history.ID), SLAAlertHistoryID: &history.ID,
		Content: fmt.Sprintf("【SLA预警 %s】工单 #%s 剩余时间 %.1f%%，请及时处理！", history.AlertLevel, item.TicketNumber, history.ActualPercentage),
	}, selected)
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
	updated, err := s.client.TicketNotification.Update().
		Where(
			ticketnotification.ID(notificationID),
			ticketnotification.UserID(userID),
			ticketnotification.TenantID(tenantID),
		).
		SetReadAt(time.Now()).
		Save(ctx)
	if err != nil {
		return s.ticketNotificationReadStorageError()
	}
	if updated == 0 {
		return ErrTicketNotificationNotFound
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
		return s.ticketNotificationReadStorageError()
	}

	return nil
}

func (s *TicketNotificationService) ticketNotificationReadStorageError() error {
	if s.logger != nil {
		s.logger.Errorw(
			"ticket notification read storage failed",
			"error_class", ticketNotificationReadStorageErrorClass,
		)
	}
	return ErrTicketNotificationStorage
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
	result, err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{assigneeID},
		EventType: "ticket_assigned",
		Content:   content,
	}, tenantID)
	if deliveryErr := ticketNotificationDeliveryError(result, err); deliveryErr != nil {
		s.logger.Warnw("failed to send assignment notification", "error_class", "ticket_notification_delivery", "ticket_id", ticketID)
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
	result, err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{newAssignee},
		EventType: "ticket_updated",
		Content:   content,
	}, tenantID)
	if deliveryErr := ticketNotificationDeliveryError(result, err); deliveryErr != nil {
		s.logger.Warnw("failed to send escalation notification", "error_class", "ticket_notification_delivery", "ticket_id", ticketID)
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
	result, err := s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{requesterID},
		EventType: "ticket_resolved",
		Content:   content,
	}, tenantID)
	if deliveryErr := ticketNotificationDeliveryError(result, err); deliveryErr != nil {
		s.logger.Warnw("failed to send resolution notification", "error_class", "ticket_notification_delivery", "ticket_id", ticketID)
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

func (s *TicketNotificationService) recoverExpiredDelivery(ctx context.Context, row *ent.TicketNotification, now time.Time) (int, error) {
	tx, err := s.queueClient.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	scope, err := s.execution.WorkerPredicate(ctx, tx, ticketnotification.FieldTenantID, ticketnotification.FieldTicketID)
	if err != nil {
		return 0, err
	}
	changed, err := tx.TicketNotification.Update().Where(scope).Where(ticketnotification.IDEQ(row.ID), ticketnotification.TenantIDEQ(row.TenantID), ticketnotification.StatusEQ(ticketNotificationStatusProcessing), ticketnotification.LeaseExpiresAtLT(now)).SetStatus(ticketNotificationStatusFailed).SetLastErrorClass("delivery_unknown").ClearLeaseOwner().ClearLeaseExpiresAt().Save(ctx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return changed, nil
}
