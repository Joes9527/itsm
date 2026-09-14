// test-coverage-guard: skip
package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"itsm-backend/common/executionscope"

	"itsm-backend/common"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/incidentalert"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type IncidentAlertingService struct {
	execution        *database.ExecutionPolicy
	emailService     *EmailService
	client           *ent.Client
	outboxRepository *OutboxEventRepository
	logger           *zap.SugaredLogger
}

const incidentAlertDeliveryEventType = "incident_alert_delivery"

func NewIncidentAlertingService(client *ent.Client, logger *zap.SugaredLogger, execution *database.ExecutionPolicy) *IncidentAlertingService {
	return &IncidentAlertingService{
		execution:        execution,
		client:           client,
		outboxRepository: NewOutboxEventRepository(client, execution),
		logger:           logger,
	}
}

// SetEmailService wires the existing delivery owner before serving requests.
func (s *IncidentAlertingService) SetEmailService(emailService *EmailService) {
	s.emailService = emailService
}

type incidentAlertDeliveryPayload struct {
	WorkItemID    int         `json:"workItemId"`
	IncidentID    int         `json:"incidentId"`
	Target        EmailTarget `json:"target"`
	Version       int         `json:"version"`
	EventID       string      `json:"eventId"`
	TenantID      int         `json:"tenantId"`
	AlertID       int         `json:"alertId"`
	Channel       string      `json:"channel"`
	Recipients    []string    `json:"recipients"`
	Subject       string      `json:"subject"`
	Message       string      `json:"message"`
	ActorID       int         `json:"actorId,omitempty"`
	Source        string      `json:"source"`
	CorrelationID string      `json:"correlationId"`
}

type incidentAlertActorContextKey struct{}

type incidentAlertActor struct {
	ID            int
	Source        string
	CorrelationID string
}

// WithIncidentAlertActor carries auditable actor/source metadata from an API
// or automation boundary into the durable delivery envelope.
func WithIncidentAlertActor(ctx context.Context, actorID int, source, correlationID string) context.Context {
	return context.WithValue(ctx, incidentAlertActorContextKey{}, incidentAlertActor{
		ID:            actorID,
		Source:        strings.TrimSpace(source),
		CorrelationID: strings.TrimSpace(correlationID),
	})
}

// CreateIncidentAlert 创建事件告警
// IncidentAlertTransactionCreator is required by replayable rule actions. An independently committing creator is not safe.
type IncidentAlertTransactionCreator interface {
	CreateIncidentAlertTx(context.Context, *ent.Tx, *dto.CreateIncidentAlertRequest, int) (*dto.IncidentAlertResponse, error)
}

func (s *IncidentAlertingService) CreateIncidentAlert(ctx context.Context, req *dto.CreateIncidentAlertRequest, tenantID int) (*dto.IncidentAlertResponse, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := s.CreateIncidentAlertTx(ctx, tx, req, tenantID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *IncidentAlertingService) CreateIncidentAlertTx(ctx context.Context, tx *ent.Tx, req *dto.CreateIncidentAlertRequest, tenantID int) (*dto.IncidentAlertResponse, error) {
	if s == nil || tx == nil || s.execution == nil {
		return nil, common.NewForbiddenError("incident alert execution policy and transaction required")
	}
	if req == nil {
		return nil, common.NewValidationError("incident alert request required", nil)
	}
	owner := *s
	owner.client = tx.Client()
	return owner.createIncidentAlertTx(ctx, tx, req, tenantID)
}

func (s *IncidentAlertingService) createIncidentAlertTx(ctx context.Context, tx *ent.Tx, req *dto.CreateIncidentAlertRequest, tenantID int) (*dto.IncidentAlertResponse, error) {
	s.logger.Infow("Creating incident alert", "incident_id", req.IncidentID, "type", req.AlertType)
	if err := s.validateAlertRequest(ctx, req, tenantID); err != nil {
		return nil, err
	}
	if err := requireIncidentExecutionTx(ctx, tx, s.execution, req.IncidentID, tenantID); err != nil {
		return nil, err
	}
	triggeredAt := time.Now()
	if req.TriggeredAt != nil {
		triggeredAt = *req.TriggeredAt
	}

	actor := resolveIncidentAlertActor(ctx)
	if actor.ID <= 0 || strings.TrimSpace(actor.Source) == "" {
		return nil, common.NewForbiddenError("explicit incident alert actor and source required")
	}
	if err := s.validateAlertActor(ctx, actor.ID, tenantID); err != nil {
		return nil, err
	}
	if actor.CorrelationID == "" {
		actor.CorrelationID = uuid.NewString()
	}

	alert, err := tx.IncidentAlert.Create().
		SetIncidentID(req.IncidentID).
		SetAlertType(req.AlertType).
		SetAlertName(req.AlertName).
		SetMessage(req.Message).
		SetSeverity(req.Severity).
		SetStatus("active").
		SetChannels(req.Channels).
		SetRecipients(req.Recipients).
		SetTriggeredAt(triggeredAt).
		SetMetadata(req.Metadata).
		SetTenantID(tenantID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create incident alert", "error", err)
		return nil, fmt.Errorf("failed to create incident alert: %w", err)
	}

	if err := s.createSystemNotification(ctx, tx, alert, tenantID); err != nil {
		return nil, fmt.Errorf("create incident alert in-app notification: %w", err)
	}
	var accepted []incidentAlertIntentReceipt
	for _, channel := range req.Channels {
		if channel != "email" {
			continue
		}
		for _, recipient := range req.Recipients {
			intent, err := s.enqueueAlertDelivery(ctx, tx, alert, channel, recipient, actor)
			if err != nil {
				return nil, fmt.Errorf("enqueue incident alert delivery: %w", err)
			}
			accepted = append(accepted, intent)
		}
	}
	if len(accepted) > 0 {
		if err := recordIncidentAlertDeliveryAudit(ctx, tx, alert, actor, accepted); err != nil {
			return nil, fmt.Errorf("audit incident alert delivery acceptance: %w", err)
		}
	}
	s.logger.Infow("Incident alert created successfully", "id", alert.ID)
	return s.toIncidentAlertResponse(alert), nil
}

func (s *IncidentAlertingService) enqueueAlertDelivery(ctx context.Context, tx *ent.Tx, alert *ent.IncidentAlert, channel, recipient string, actor incidentAlertActor) (incidentAlertIntentReceipt, error) {
	if s.emailService == nil {
		return incidentAlertIntentReceipt{}, executionscope.ErrDenied
	}
	target, err := s.emailService.DescribeDeliveryTarget(ctx, tx, alert.TenantID, "outbox")
	if err != nil {
		return incidentAlertIntentReceipt{}, err
	}
	if err = target.Validate(); err != nil {
		return incidentAlertIntentReceipt{}, err
	}
	incidentRecord, err := tx.Incident.Query().Where(incident.IDEQ(alert.IncidentID), incident.HasWorkItemWith(ticket.TenantIDEQ(alert.TenantID))).Only(ctx)
	if err != nil {
		return incidentAlertIntentReceipt{}, fmt.Errorf("resolve alert execution WorkItem: %w", err)
	}
	eventID := uuid.NewString()
	if actor.CorrelationID == "" {
		actor.CorrelationID = eventID
	}
	envelope := incidentAlertDeliveryPayload{
		WorkItemID: incidentRecord.WorkItemID, IncidentID: incidentRecord.ID, Target: target,
		Version:       2,
		EventID:       eventID,
		TenantID:      alert.TenantID,
		AlertID:       alert.ID,
		Channel:       channel,
		Recipients:    []string{recipient},
		Subject:       alert.AlertName,
		Message:       alert.Message,
		ActorID:       actor.ID,
		Source:        actor.Source,
		CorrelationID: actor.CorrelationID,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return incidentAlertIntentReceipt{}, err
	}
	_, err = s.outboxRepository.Enqueue(ctx, tx, NewOutboxEvent{
		ExecutionWorkItemID: incidentRecord.WorkItemID,
		EventID:             eventID,
		EventType:           incidentAlertDeliveryEventType,
		NextAttemptAt:       time.Now().UTC(),
		TenantID:            alert.TenantID,
		AggregateType:       "incident_alert",
		AggregateID:         fmt.Sprint(alert.ID),
		Payload:             payload,
	})
	if err != nil {
		return incidentAlertIntentReceipt{}, err
	}
	digest, err := incidentAlertJSONDigest(envelope)
	if err != nil {
		return incidentAlertIntentReceipt{}, err
	}
	return incidentAlertIntentReceipt{EventID: eventID, PayloadDigest: digest}, nil
}

func resolveIncidentAlertActor(ctx context.Context) incidentAlertActor {
	actor := incidentAlertActor{}
	if carried, ok := ctx.Value(incidentAlertActorContextKey{}).(incidentAlertActor); ok {
		actor = carried
	}
	return actor
}

type incidentAlertIntentReceipt struct {
	EventID       string `json:"eventId"`
	PayloadDigest string `json:"payloadDigest"`
}

type incidentAlertAcceptanceReceipt struct {
	Version       int                          `json:"version"`
	TenantID      int                          `json:"tenantId"`
	WorkItemID    int                          `json:"workItemId"`
	IncidentID    int                          `json:"incidentId"`
	AlertID       int                          `json:"alertId"`
	ActorID       int                          `json:"actorId"`
	Source        string                       `json:"source"`
	CorrelationID string                       `json:"correlationId"`
	Intents       []incidentAlertIntentReceipt `json:"intents"`
}

func incidentAlertJSONDigest(value interface{}) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var canonical interface{}
	if err = decoder.Decode(&canonical); err != nil {
		return "", err
	}
	encoded, err = json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func recordIncidentAlertDeliveryAudit(ctx context.Context, tx *ent.Tx, alert *ent.IncidentAlert, actor incidentAlertActor, intents []incidentAlertIntentReceipt) error {
	source, err := tx.Incident.Query().Where(incident.IDEQ(alert.IncidentID), incident.HasWorkItemWith(ticket.TenantIDEQ(alert.TenantID))).Only(ctx)
	if err != nil {
		return err
	}
	receipt := incidentAlertAcceptanceReceipt{Version: 2, TenantID: alert.TenantID, WorkItemID: source.WorkItemID, IncidentID: source.ID, AlertID: alert.ID, ActorID: actor.ID, Source: actor.Source, CorrelationID: actor.CorrelationID, Intents: intents}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	digest, err := incidentAlertJSONDigest(receipt)
	if err != nil {
		return err
	}
	return tx.AuditLog.Create().SetTenantID(alert.TenantID).SetUserID(actor.ID).
		SetOperationID("incident_alert_accept:" + fmt.Sprint(alert.ID)).SetRequestID(actor.CorrelationID).
		SetResource("incident_alert").SetAction("incident_alert.delivery_accepted").SetPath("incident_alerts/delivery").
		SetMethod("OUTBOX").SetStatusCode(202).SetResultStatus("accepted").SetRequestDigest(digest).SetRequestBody(string(encoded)).Exec(ctx)
}

func (s *IncidentAlertingService) validateAlertRequest(ctx context.Context, req *dto.CreateIncidentAlertRequest, tenantID int) error {
	if req.IncidentID <= 0 {
		return fmt.Errorf("incident id is required")
	}
	exists, err := s.client.Incident.Query().
		Where(incident.IDEQ(req.IncidentID), incidentTenantScope(tenantID)).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate incident: %w", err)
	}
	if !exists {
		return fmt.Errorf("incident not found")
	}
	if strings.TrimSpace(req.AlertType) == "" || strings.TrimSpace(req.AlertName) == "" || strings.TrimSpace(req.Message) == "" {
		return fmt.Errorf("alert type, name, and message are required")
	}
	switch req.Severity {
	case "", "low", "medium", "high", "critical":
	default:
		return fmt.Errorf("invalid alert severity: %s", req.Severity)
	}
	if err := validateIncidentAlertChannels(req.Channels); err != nil {
		return err
	}
	seenChannels := make(map[string]struct{}, len(req.Channels))
	for _, channel := range req.Channels {
		seenChannels[channel] = struct{}{}
	}
	if _, sendsEmail := seenChannels["email"]; sendsEmail {
		if len(req.Recipients) == 0 {
			return fmt.Errorf("email alert recipient is required")
		}
		for _, recipient := range req.Recipients {
			if _, err := mail.ParseAddress(recipient); err != nil {
				return fmt.Errorf("invalid email alert recipient")
			}
		}
	}
	return nil
}

// createSystemNotification 创建系统通知记录
func (s *IncidentAlertingService) createSystemNotification(ctx context.Context, tx *ent.Tx, alert *ent.IncidentAlert, tenantID int) error {
	query := tx.User.Query().Where(user.TenantIDEQ(tenantID), user.ActiveEQ(true))
	if len(alert.Recipients) > 0 {
		query.Where(user.EmailIn(alert.Recipients...))
	} else {
		query.Where(user.RoleIn("super_admin"))
	}
	recipients, err := query.All(ctx)
	if err != nil {
		return fmt.Errorf("resolve in-app alert recipients: %w", err)
	}
	for _, recipient := range recipients {
		_, err := tx.Notification.Create().
			SetTitle(alert.AlertName).
			SetMessage(alert.Message).
			SetType("incident_alert").
			SetUserID(recipient.ID).
			SetTenantID(tenantID).
			SetCreatedAt(time.Now()).
			SetUpdatedAt(time.Now()).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("create in-app alert notification: %w", err)
		}
	}
	return nil
}

// AcknowledgeAlert 确认告警
func (s *IncidentAlertingService) AcknowledgeAlert(ctx context.Context, alertID int, userID int, tenantID int) error {
	if s == nil || s.client == nil || s.execution == nil {
		return common.NewForbiddenError("incident alert execution policy required")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	owner := *s
	owner.client = tx.Client()

	s.logger.Infow("Acknowledging alert", "alert_id", alertID, "user_id", userID)
	if err := owner.validateAlertActor(ctx, userID, tenantID); err != nil {
		return err
	}

	current, err := tx.IncidentAlert.Query().Where(incidentalert.IDEQ(alertID), incidentalert.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return err
	}
	if err := requireIncidentExecutionTx(ctx, tx, s.execution, current.IncidentID, tenantID); err != nil {
		return err
	}
	now := time.Now()
	alert, err := tx.IncidentAlert.UpdateOneID(alertID).
		Where(incidentalert.TenantIDEQ(tenantID), incidentalert.StatusEQ("active")).
		SetStatus("acknowledged").
		SetAcknowledgedAt(now).
		SetAcknowledgedBy(userID).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("alert not found")
		}
		s.logger.Errorw("Failed to acknowledge alert", "error", err)
		return fmt.Errorf("failed to acknowledge alert: %w", err)
	}

	// 记录确认活动
	if err := owner.createAlertEvent(ctx, alert, "acknowledged", fmt.Sprintf("告警已被用户 %d 确认", userID), userID, tenantID); err != nil {
		return err
	}

	s.logger.Infow("Alert acknowledged successfully", "alert_id", alertID)
	return tx.Commit()
}

// ResolveAlert 解决告警
func (s *IncidentAlertingService) ResolveAlert(ctx context.Context, alertID int, userID int, tenantID int) error {
	if s == nil || s.client == nil || s.execution == nil {
		return common.NewForbiddenError("incident alert execution policy required")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	owner := *s
	owner.client = tx.Client()

	s.logger.Infow("Resolving alert", "alert_id", alertID, "user_id", userID)
	if err := owner.validateAlertActor(ctx, userID, tenantID); err != nil {
		return err
	}

	current, err := tx.IncidentAlert.Query().Where(incidentalert.IDEQ(alertID), incidentalert.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return err
	}
	if err := requireIncidentExecutionTx(ctx, tx, s.execution, current.IncidentID, tenantID); err != nil {
		return err
	}
	now := time.Now()
	alert, err := tx.IncidentAlert.UpdateOneID(alertID).
		Where(incidentalert.TenantIDEQ(tenantID), incidentalert.StatusIn("active", "acknowledged")).
		SetStatus("resolved").
		SetResolvedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("alert not found")
		}
		s.logger.Errorw("Failed to resolve alert", "error", err)
		return fmt.Errorf("failed to resolve alert: %w", err)
	}

	// 记录解决活动
	if err := owner.createAlertEvent(ctx, alert, "resolved", fmt.Sprintf("告警已被用户 %d 解决", userID), userID, tenantID); err != nil {
		return err
	}

	s.logger.Infow("Alert resolved successfully", "alert_id", alertID)
	return tx.Commit()
}

// createAlertEvent 创建告警活动记录
func (s *IncidentAlertingService) createAlertEvent(ctx context.Context, alert *ent.IncidentAlert, eventType, description string, userID, tenantID int) error {
	_, err := s.client.IncidentEvent.Create().
		SetIncidentID(alert.IncidentID).
		SetEventType("alert_" + eventType).
		SetEventName("告警" + eventType).
		SetDescription(description).
		SetStatus("active").
		SetSeverity(alert.Severity).
		SetSource("user").
		SetUserID(userID).
		SetOccurredAt(time.Now()).
		SetTenantID(tenantID).
		SetData(map[string]interface{}{"alertId": alert.ID}).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("create alert audit event: %w", err)
	}
	return nil
}

func (s *IncidentAlertingService) validateAlertActor(ctx context.Context, userID, tenantID int) error {
	exists, err := s.client.User.Query().
		Where(user.IDEQ(userID), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate alert actor: %w", err)
	}
	if !exists {
		return common.NewForbiddenError("alert actor not found or inactive")
	}
	return nil
}

// GetActiveAlerts 获取活跃告警
func (s *IncidentAlertingService) GetActiveAlerts(ctx context.Context, tenantID int, page, size int) ([]*dto.IncidentAlertResponse, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 100 {
		size = 100
	}
	query := s.client.IncidentAlert.Query().
		Where(
			incidentalert.TenantIDEQ(tenantID),
			incidentalert.StatusEQ("active"),
		)

	// 获取总数
	total, err := query.Count(ctx)
	if err != nil {
		s.logger.Errorw("Failed to count active alerts", "error", err)
		return nil, 0, fmt.Errorf("failed to count active alerts: %w", err)
	}

	// 分页查询
	alerts, err := query.
		Offset((page - 1) * size).
		Limit(size).
		Order(ent.Desc(incidentalert.FieldTriggeredAt)).
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to get active alerts", "error", err)
		return nil, 0, fmt.Errorf("failed to get active alerts: %w", err)
	}

	responses := make([]*dto.IncidentAlertResponse, len(alerts))
	for i, alert := range alerts {
		responses[i] = s.toIncidentAlertResponse(alert)
	}

	return responses, total, nil
}

// GetAlertStatistics 获取告警统计
func (s *IncidentAlertingService) GetAlertStatistics(ctx context.Context, tenantID int, startTime, endTime time.Time) (map[string]interface{}, error) {
	s.logger.Infow("Getting alert statistics", "tenant_id", tenantID, "start_time", startTime, "end_time", endTime)
	if endTime.Before(startTime) {
		return nil, fmt.Errorf("end time must not be before start time")
	}

	// 获取告警总数
	totalAlerts, err := s.client.IncidentAlert.Query().
		Where(
			incidentalert.TenantIDEQ(tenantID),
			incidentalert.TriggeredAtGTE(startTime),
			incidentalert.TriggeredAtLTE(endTime),
		).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count total alerts: %w", err)
	}

	// 获取活跃告警数
	activeAlerts, err := s.client.IncidentAlert.Query().
		Where(
			incidentalert.TenantIDEQ(tenantID),
			incidentalert.StatusEQ("active"),
			incidentalert.TriggeredAtGTE(startTime),
			incidentalert.TriggeredAtLTE(endTime),
		).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count active alerts: %w", err)
	}

	// 获取已确认告警数
	acknowledgedAlerts, err := s.client.IncidentAlert.Query().
		Where(
			incidentalert.TenantIDEQ(tenantID),
			incidentalert.StatusEQ("acknowledged"),
			incidentalert.TriggeredAtGTE(startTime),
			incidentalert.TriggeredAtLTE(endTime),
		).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count acknowledged alerts: %w", err)
	}

	// 获取已解决告警数
	resolvedAlerts, err := s.client.IncidentAlert.Query().
		Where(
			incidentalert.TenantIDEQ(tenantID),
			incidentalert.StatusEQ("resolved"),
			incidentalert.TriggeredAtGTE(startTime),
			incidentalert.TriggeredAtLTE(endTime),
		).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count resolved alerts: %w", err)
	}

	// 计算平均响应时间
	var avgResponseTime float64
	if acknowledgedAlerts > 0 {
		// 计算实际平均响应时间 (Minutes)
		// 聚合查询: AVG(EXTRACT(EPOCH FROM (acknowledged_at - triggered_at))/60)
		// 由于Ent聚合查询较复杂，这里使用简化逻辑：查询最近100条已确认告警计算平均值
		recentAlerts, err := s.client.IncidentAlert.Query().
			Where(
				incidentalert.TenantIDEQ(tenantID),
				incidentalert.TriggeredAtGTE(startTime),
				incidentalert.TriggeredAtLTE(endTime),
				incidentalert.AcknowledgedAtNotNil(),
			).
			Limit(100).
			Select(incidentalert.FieldTriggeredAt, incidentalert.FieldAcknowledgedAt).
			All(ctx)

		if err == nil && len(recentAlerts) > 0 {
			var totalDiff float64
			count := 0
			for _, a := range recentAlerts {
				if a.AcknowledgedAt.After(a.TriggeredAt) {
					totalDiff += a.AcknowledgedAt.Sub(a.TriggeredAt).Minutes()
					count++
				}
			}
			if count > 0 {
				avgResponseTime = totalDiff / float64(count)
			}
		} else {
			// Fallback if query fails or no data
			avgResponseTime = 0
		}
	}

	// 计算解决率
	var resolutionRate float64
	if totalAlerts > 0 {
		resolutionRate = float64(resolvedAlerts) / float64(totalAlerts) * 100
	}

	statistics := map[string]interface{}{
		"total_alerts":        totalAlerts,
		"active_alerts":       activeAlerts,
		"acknowledged_alerts": acknowledgedAlerts,
		"resolved_alerts":     resolvedAlerts,
		"avg_response_time":   avgResponseTime,
		"resolution_rate":     resolutionRate,
		"period": map[string]interface{}{
			"start_time": startTime,
			"end_time":   endTime,
		},
	}

	return statistics, nil
}

// ProcessEscalationAlerts 处理升级告警
func (s *IncidentAlertingService) ProcessEscalationAlerts(ctx context.Context, incidentID int, escalationLevel int, tenantID int) error {
	s.logger.Infow("Processing escalation alerts", "incident_id", incidentID, "level", escalationLevel)

	// 获取事件信息
	incidentEntity, err := s.client.Incident.Query().
		Where(
			incident.IDEQ(incidentID),
			incidentTenantScope(tenantID),
		).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("incident not found")
		}
		return fmt.Errorf("failed to get incident: %w", err)
	}

	// 根据升级级别确定告警内容
	var alertMessage string
	var severity string
	var channels []string
	var recipients []string

	switch escalationLevel {
	case 1:
		alertMessage = fmt.Sprintf("事件 %s 已升级到级别 1，需要关注", incidentEntity.Edges.WorkItem.TicketNumber)
		severity = "high"
		channels = []string{"email"}
		recipients = []string{"manager@company.com", "team@company.com"}
	case 2:
		alertMessage = fmt.Sprintf("事件 %s 已升级到级别 2，需要立即处理", incidentEntity.Edges.WorkItem.TicketNumber)
		severity = "critical"
		channels = []string{"email"}
		recipients = []string{"director@company.com", "manager@company.com", "team@company.com"}
	case 3:
		alertMessage = fmt.Sprintf("事件 %s 已升级到级别 3，需要紧急处理", incidentEntity.Edges.WorkItem.TicketNumber)
		severity = "critical"
		channels = []string{"email"}
		recipients = []string{"cto@company.com", "director@company.com", "manager@company.com", "team@company.com"}
	default:
		alertMessage = fmt.Sprintf("事件 %s 已升级到级别 %d", incidentEntity.Edges.WorkItem.TicketNumber, escalationLevel)
		severity = "high"
		channels = []string{"email"}
		recipients = []string{"admin@company.com"}
	}

	// 创建升级告警
	_, err = s.CreateIncidentAlert(ctx, &dto.CreateIncidentAlertRequest{
		IncidentID: incidentID,
		AlertType:  "escalation",
		AlertName:  "事件升级告警",
		Message:    alertMessage,
		Severity:   severity,
		Channels:   channels,
		Recipients: recipients,
		Metadata: map[string]interface{}{
			"escalation_level":  escalationLevel,
			"incident_title":    incidentEntity.Edges.WorkItem.Title,
			"incident_severity": incidentEntity.Severity,
		},
	}, tenantID)
	if err != nil {
		s.logger.Errorw("Failed to create escalation alert", "error", err)
		return fmt.Errorf("failed to create escalation alert: %w", err)
	}

	s.logger.Infow("Escalation alerts processed successfully", "incident_id", incidentID, "level", escalationLevel)
	return nil
}

// ProcessThresholdAlerts 处理阈值告警
func (s *IncidentAlertingService) ProcessThresholdAlerts(ctx context.Context, incidentID int, metricType string, metricValue float64, threshold float64, tenantID int) error {
	s.logger.Infow("Processing threshold alerts", "metric_type", metricType, "value", metricValue, "threshold", threshold)

	// 检查是否超过阈值
	if metricValue <= threshold {
		return nil
	}

	// 创建阈值告警
	alertMessage := fmt.Sprintf("指标 %s 当前值 %.2f 超过阈值 %.2f", metricType, metricValue, threshold)

	_, err := s.CreateIncidentAlert(ctx, &dto.CreateIncidentAlertRequest{
		IncidentID: incidentID,
		AlertType:  "threshold",
		AlertName:  "阈值告警",
		Message:    alertMessage,
		Severity:   "medium",
		Channels:   []string{"email"},
		Recipients: []string{"monitoring@company.com", "team@company.com"},
		Metadata: map[string]interface{}{
			"metric_type":  metricType,
			"metric_value": metricValue,
			"threshold":    threshold,
			"exceeded_by":  metricValue - threshold,
		},
	}, tenantID)
	if err != nil {
		s.logger.Errorw("Failed to create threshold alert", "error", err)
		return fmt.Errorf("failed to create threshold alert: %w", err)
	}

	s.logger.Infow("Threshold alert processed successfully", "metric_type", metricType)
	return nil
}

// ProcessSLAViolationAlerts 处理SLA违规告警
func (s *IncidentAlertingService) ProcessSLAViolationAlerts(ctx context.Context, incidentID int, violationType string, tenantID int) error {
	s.logger.Infow("Processing SLA violation alerts", "incident_id", incidentID, "violation_type", violationType)

	// 获取事件信息
	incidentEntity, err := s.client.Incident.Query().
		Where(
			incident.IDEQ(incidentID),
			incidentTenantScope(tenantID),
		).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("incident not found")
		}
		return fmt.Errorf("failed to get incident: %w", err)
	}

	// 根据违规类型确定告警内容
	var alertMessage string
	var severity string
	var channels []string
	var recipients []string

	switch violationType {
	case "response_time":
		alertMessage = fmt.Sprintf("事件 %s 响应时间超时，违反SLA", incidentEntity.Edges.WorkItem.TicketNumber)
		severity = "high"
		channels = []string{"email"}
		recipients = []string{"manager@company.com", "team@company.com"}
	case "resolution_time":
		alertMessage = fmt.Sprintf("事件 %s 解决时间超时，违反SLA", incidentEntity.Edges.WorkItem.TicketNumber)
		severity = "critical"
		channels = []string{"email"}
		recipients = []string{"director@company.com", "manager@company.com", "team@company.com"}
	default:
		alertMessage = fmt.Sprintf("事件 %s 违反SLA: %s", incidentEntity.Edges.WorkItem.TicketNumber, violationType)
		severity = "medium"
		channels = []string{"email"}
		recipients = []string{"admin@company.com"}
	}

	// 创建SLA违规告警
	_, err = s.CreateIncidentAlert(ctx, &dto.CreateIncidentAlertRequest{
		IncidentID: incidentID,
		AlertType:  "sla_violation",
		AlertName:  "SLA违规告警",
		Message:    alertMessage,
		Severity:   severity,
		Channels:   channels,
		Recipients: recipients,
		Metadata: map[string]interface{}{
			"violation_type":    violationType,
			"incident_title":    incidentEntity.Edges.WorkItem.Title,
			"incident_priority": incidentEntity.Edges.WorkItem.Priority,
		},
	}, tenantID)
	if err != nil {
		s.logger.Errorw("Failed to create SLA violation alert", "error", err)
		return fmt.Errorf("failed to create SLA violation alert: %w", err)
	}

	s.logger.Infow("SLA violation alert processed successfully", "incident_id", incidentID, "violation_type", violationType)
	return nil
}

// 转换为响应DTO
func (s *IncidentAlertingService) toIncidentAlertResponse(alert *ent.IncidentAlert) *dto.IncidentAlertResponse {
	return &dto.IncidentAlertResponse{
		ID:             alert.ID,
		IncidentID:     alert.IncidentID,
		AlertType:      alert.AlertType,
		AlertName:      alert.AlertName,
		Message:        alert.Message,
		Severity:       alert.Severity,
		Status:         alert.Status,
		Channels:       alert.Channels,
		Recipients:     alert.Recipients,
		TriggeredAt:    alert.TriggeredAt,
		AcknowledgedAt: &alert.AcknowledgedAt,
		ResolvedAt:     &alert.ResolvedAt,
		AcknowledgedBy: &alert.AcknowledgedBy,
		Metadata:       alert.Metadata,
		TenantID:       alert.TenantID,
		CreatedAt:      alert.CreatedAt,
		UpdatedAt:      alert.UpdatedAt,
	}
}
