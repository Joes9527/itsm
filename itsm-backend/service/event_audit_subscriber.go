package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"itsm-backend/ent"

	"go.uber.org/zap"
)

// EventAuditSubscriber 事件驱动审计订阅方。
//
// 订阅领域事件（如 sla.breached / ai.triage.completed）并写入 AuditLog，
// 满足"AI 建议、流程流转、自动化动作必须可追踪"的审计要求。
// 事件由 eventbus 以 camelCase map（信封合并后）投递。
type EventAuditSubscriber struct {
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewEventAuditSubscriber 创建事件审计订阅方
func NewEventAuditSubscriber(client *ent.Client, logger *zap.SugaredLogger) *EventAuditSubscriber {
	return &EventAuditSubscriber{client: client, logger: logger}
}

// Handle implements shared.EventHandler。
// 将事件元数据写入审计日志；写入失败返回错误触发 Nack 重试。
func (s *EventAuditSubscriber) Handle(event interface{}) error {
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
		Save(context.Background())
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
