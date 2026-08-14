package service

import (
	"context"
	"encoding/json"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestEventAuditSubscriber_HandleWritesAuditLog(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:event_audit_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	sub := NewEventAuditSubscriber(client, zaptest.NewLogger(t).Sugar())

	event := map[string]interface{}{
		"eventType":  "sla.breached",
		"tenantId":   "7",
		"occurredAt": "2026-08-14T10:00:00Z",
		"ticketId":   "27",
	}

	require.NoError(t, sub.Handle(event))

	auditLogs, err := client.AuditLog.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, auditLogs, 1)

	log := auditLogs[0]
	assert.Equal(t, 7, log.TenantID)
	assert.Equal(t, "event", log.Resource)
	assert.Equal(t, "sla.breached", log.Action)
	assert.Equal(t, "eventbus://sla.breached", log.Path)

	// 验证请求体为信封合并 JSON
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(*log.RequestBody), &body))
	assert.Equal(t, "27", body["ticketId"])
}

func TestEventAuditSubscriber_HandleRejectsWrongShape(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:event_audit_shape_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	sub := NewEventAuditSubscriber(client, zaptest.NewLogger(t).Sugar())
	err := sub.Handle("not-a-map")
	require.Error(t, err)

	count, _ := client.AuditLog.Query().Count(context.Background())
	assert.Equal(t, 0, count)
}

func TestEventAuditSubscriber_HandleMissingEventType(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:event_audit_missing_type_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	sub := NewEventAuditSubscriber(client, zaptest.NewLogger(t).Sugar())
	err := sub.Handle(map[string]interface{}{"tenantId": "1"})
	require.Error(t, err)

	count, _ := client.AuditLog.Query().Count(context.Background())
	assert.Equal(t, 0, count)
}

func TestAuditedEventTopics(t *testing.T) {
	topics := AuditedEventTopics()
	assert.Contains(t, topics, "sla.breached")
	assert.Contains(t, topics, "ai.triage.completed")
}
