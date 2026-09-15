package delegated_execution

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/outboxevent"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestRequeueRejectsDeliveryUnknown(t *testing.T) {
	service, client := newServiceForTest(t)
	seedBlockedEvent(t, client, "event-unknown", 7, "delivery_unknown: external side effect may have completed")

	err := service.Requeue(context.Background(), 7, "event-unknown", RequeueRequest{ActorID: 9})

	require.Error(t, err)
	appErr, ok := common.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, common.ErrCodeConflict, appErr.Code)
	event := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ("event-unknown")).OnlyX(context.Background())
	require.Equal(t, outboxStatusBlocked, event.Status)
	require.Zero(t, client.AuditLog.Query().CountX(context.Background()))
}

func TestRequeueRestoresBlockedEventWithAudit(t *testing.T) {
	service, client := newServiceForTest(t)
	seedBlockedEvent(t, client, "event-safe", 7, "KAF webhook returned HTTP 401")

	err := service.Reconcile(context.Background(), 7, "event-safe", ReconcileRequest{Conclusion: conclusionNotAccepted, Reason: "KAF confirmed no acceptance", ActorID: 9})
	require.NoError(t, err)
	err = service.Requeue(context.Background(), 7, "event-safe", RequeueRequest{ActorID: 9})

	require.NoError(t, err)
	event := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ("event-safe")).OnlyX(context.Background())
	require.Equal(t, outboxStatusPending, event.Status)
	require.Contains(t, event.LastError, conclusionNotAccepted)
	audit := client.AuditLog.Query().Where(auditlog.ActionEQ("delegated_execution.requeue")).OnlyX(context.Background())
	require.Equal(t, "delegated_execution.requeue", audit.Action)
	require.Equal(t, 9, audit.UserID)
	require.Empty(t, audit.RequestBody)
	reconciliation := client.AuditLog.Query().Where(auditlog.ActionEQ("delegated_execution.reconcile")).OnlyX(context.Background())
	require.NotNil(t, reconciliation.RequestBody)
	var record reconciliationAuditRecord
	require.NoError(t, json.Unmarshal([]byte(*reconciliation.RequestBody), &record))
	require.Equal(t, conclusionNotAccepted, record.Conclusion)
	require.Equal(t, "KAF confirmed no acceptance", record.Reason)
	require.Equal(t, "task-1", record.TaskID)
}

func TestRequeueIsTenantScoped(t *testing.T) {
	service, client := newServiceForTest(t)
	seedBlockedEvent(t, client, "event-tenant", 7, "KAF webhook returned HTTP 401")

	err := service.Requeue(context.Background(), 8, "event-tenant", RequeueRequest{ActorID: 9})

	appErr, ok := common.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, common.ErrCodeNotFound, appErr.Code)
	event := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ("event-tenant")).OnlyX(context.Background())
	require.Equal(t, outboxStatusBlocked, event.Status)
}

func TestRequeueRequiresStoredReconciliation(t *testing.T) {
	service, client := newServiceForTest(t)
	seedBlockedEvent(t, client, "event-without-conclusion", 7, "KAF webhook returned HTTP 401")

	err := service.Requeue(context.Background(), 7, "event-without-conclusion", RequeueRequest{ActorID: 9})
	appErr, ok := common.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, common.ErrCodeConflict, appErr.Code)
	event := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ("event-without-conclusion")).OnlyX(context.Background())
	require.Equal(t, outboxStatusBlocked, event.Status)
}

func TestReconcileRecordsDeliveryUnknownWithoutPermittingRequeue(t *testing.T) {
	service, client := newServiceForTest(t)
	seedBlockedEvent(t, client, "event-unknown-reconciled", 7, "delivery_unknown: external side effect may have completed")

	err := service.Reconcile(context.Background(), 7, "event-unknown-reconciled", ReconcileRequest{Conclusion: conclusionDeliveryUnknown, Reason: "operator will verify KAF outcome", ActorID: 9})
	require.NoError(t, err)
	err = service.Requeue(context.Background(), 7, "event-unknown-reconciled", RequeueRequest{ActorID: 9})
	appErr, ok := common.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, common.ErrCodeConflict, appErr.Code)
}

func TestListIsTenantScopedAndRedactsDeliveryDetail(t *testing.T) {
	service, client := newServiceForTest(t)
	seedBlockedEvent(t, client, "event-visible", 7, "delivery_unknown: KAF response contained sensitive detail")
	seedBlockedEvent(t, client, "event-other-tenant", 8, "KAF webhook returned HTTP 401")

	result, err := service.List(context.Background(), 7, ListFilter{Page: 1, Size: 20})
	require.NoError(t, err)
	require.Equal(t, 1, result.Total)
	require.Len(t, result.Items, 1)
	require.Equal(t, "event-visible", result.Items[0].EventID)
	require.Equal(t, "delivery_unknown", result.Items[0].ErrorClass)
	require.NotContains(t, result.Items[0].ErrorClass, "sensitive")
}

func TestListRejectsUnsupportedStatus(t *testing.T) {
	service, _ := newServiceForTest(t)
	_, err := service.List(context.Background(), 7, ListFilter{Status: "all"})
	appErr, ok := common.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, common.ErrCodeValidation, appErr.Code)
}

func newServiceForTest(t *testing.T) (*Service, *ent.Client) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:delegated_execution?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(context.Background()))
	service := NewService(client)
	service.now = func() time.Time { return time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC) }
	return service, client
}

func seedBlockedEvent(t *testing.T, client *ent.Client, eventID string, tenantID int, lastError string) {
	t.Helper()
	_, err := client.OutboxEvent.Create().
		SetEventID(eventID).
		SetEventType(kafDelegateRequestedEventType).
		SetTenantID(tenantID).
		SetAggregateType("process_task").
		SetAggregateID("task-1").
		SetPayload([]byte(`{}`)).
		SetStatus(outboxStatusBlocked).
		SetLastError(lastError).
		Save(context.Background())
	require.NoError(t, err)
}
