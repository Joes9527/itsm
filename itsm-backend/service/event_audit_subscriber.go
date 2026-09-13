package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/pkg/eventbus"

	"go.uber.org/zap"
)

// EventAuditSubscriber 事件驱动审计订阅方。
//
// 订阅领域事件（如 sla.breached / ai.triage.completed）并写入 AuditLog，
// 满足"AI 建议、流程流转、自动化动作必须可追踪"的审计要求。
// Standard 事件以 camelCase map 投递；candidate 事件保留完整 typed Envelope。
type EventAuditSubscriber struct {
	client *ent.Client
	policy *database.ExecutionPolicy
	logger *zap.SugaredLogger
}

// NewEventAuditSubscriber 创建事件审计订阅方
func NewEventAuditSubscriber(client *ent.Client, logger *zap.SugaredLogger, policy *database.ExecutionPolicy) *EventAuditSubscriber {
	return &EventAuditSubscriber{client: client, logger: logger, policy: policy}
}

// Handle implements shared.EventHandler。
// 候选审计在同一事务内核验来源并写唯一回执；失败返回错误供传输层处理。
func (s *EventAuditSubscriber) Handle(event interface{}) error {
	return s.HandleContext(context.Background(), event)
}

func (s *EventAuditSubscriber) HandleContext(ctx context.Context, event interface{}) error {
	if s == nil || s.client == nil || s.policy == nil || ctx == nil {
		return executionscope.ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.policy.IsCandidate() {
		return s.handleExecutionEvent(ctx, event)
	}
	// 事件投递形态：信封合并后的 map（eventType/tenantId/occurredAt + payload 字段）
	raw, ok := event.(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected event shape %T", event)
	}

	eventType, _ := raw["eventType"].(string)
	if eventType == "" {
		return fmt.Errorf("event missing eventType")
	}

	tenantID := 0
	if v, ok := raw["tenantId"].(string); ok {
		tenantID, _ = strconv.Atoi(v)
	}

	if tenantID <= 0 {
		return fmt.Errorf("event requires tenant identity")
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != tenantID {
		return executionscope.ErrDenied
	}
	if tenantctx.IsSystemBypass(ctx) {
		return executionscope.ErrDenied
	}
	known := false
	for _, topic := range AuditedEventTopics() {
		if topic == eventType {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("unsupported audit event type")
	}
	payloadJSON, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}

	now := time.Now()
	_, err = s.client.AuditLog.Create().
		SetCreatedAt(now).
		SetTenantID(tenantID).
		SetUserID(0). // 系统事件，无操作用户
		SetResource("event").
		SetAction(eventType).
		SetPath("eventbus://" + eventType).
		SetMethod("PUBLISH").
		SetStatusCode(0).
		SetRequestBody(string(payloadJSON)).
		Save(tenantctx.WithTenantID(ctx, tenantID))
	if err != nil {
		s.logger.Warnw("failed to write event audit log", "error", err, "event_type", eventType)
		return err
	}

	s.logger.Debugw("event audit recorded", "event_type", eventType, "tenant_id", tenantID)
	return nil
}

// AuditedEventTopics 需要审计的事件 topic 列表
func AuditedEventTopics() []string {
	return []string{
		"sla.breached",
		"ai.triage.completed",
	}
}

func (s *EventAuditSubscriber) handleExecutionEvent(ctx context.Context, event interface{}) error {
	env, ok := event.(eventbus.Envelope)
	if !ok {
		return fmt.Errorf("candidate audit requires the complete typed envelope")
	}
	wire, err := json.Marshal(env)
	if err != nil {
		return err
	}
	env, err = eventbus.DecodeExecutionEnvelope(wire)
	if err != nil {
		return err
	}
	tenantID, err := strconv.Atoi(env.TenantID)
	if err != nil || tenantID <= 0 || strconv.Itoa(tenantID) != env.TenantID {
		return executionscope.ErrDenied
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != tenantID {
		return executionscope.ErrDenied
	}
	if tenantctx.IsSystemBypass(ctx) {
		return executionscope.ErrDenied
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	ref := executionscope.Ref{DeploymentID: env.Execution.DeploymentID, ScopeID: env.Execution.ScopeID, TenantID: tenantID}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := NewExecutionEventAuthority(s.client, s.policy).ValidateEventTx(ctx, tx, ref, env); err != nil {
		return err
	}
	// Whitespace and equivalent UTC offsets cannot change the event's identity.
	var compact bytes.Buffer
	if err := json.Compact(&compact, env.Payload); err != nil {
		return err
	}
	env.Payload = compact.Bytes()
	env.OccurredAt = env.OccurredAt.UTC()
	wire, err = json.Marshal(env)
	if err != nil {
		return err
	}
	digest, err := workitemmutation.Digest(env)
	if err != nil {
		return err
	}
	operationID := "event_audit:" + env.EventID
	receipt, err := tx.AuditLog.Query().Where(auditlog.TenantID(tenantID), auditlog.UserID(0), auditlog.OperationID(operationID)).Only(ctx)
	if err == nil {
		if receipt.Resource != "event" || receipt.Action != env.EventType || receipt.Path != "eventbus://"+env.EventType || receipt.Method != "PUBLISH" || receipt.StatusCode != 200 || receipt.RequestDigest == nil || *receipt.RequestDigest != digest || receipt.RequestBody == nil || *receipt.RequestBody != string(wire) || receipt.ResultStatus == nil || *receipt.ResultStatus != "recorded" || receipt.ResultVersion != nil {
			return fmt.Errorf("event audit operation conflicts with existing receipt")
		}
		return nil // Read-only replay; deferred rollback closes the transaction.
	}
	if !ent.IsNotFound(err) {
		return err
	}
	_, err = tx.AuditLog.Create().SetTenantID(tenantID).SetUserID(0).SetOperationID(operationID).SetRequestDigest(digest).SetResultStatus("recorded").SetResource("event").SetAction(env.EventType).SetPath("eventbus://" + env.EventType).SetMethod("PUBLISH").SetStatusCode(200).SetRequestBody(string(wire)).Save(ctx)
	if err != nil {
		return err
	} // A unique conflict aborts; only a fresh delivery may replay.
	return tx.Commit()
}
