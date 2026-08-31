package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/require"
)

type stubFrozenDefinitionEngine struct {
	err          error
	calls        int
	definitionID int
	businessKey  string
	tenantID     int
}

type failPublishOnceWorkflowRepository struct {
	*OutboxEventRepository
	fail bool
}

func (r *failPublishOnceWorkflowRepository) MarkPublished(ctx context.Context, eventID int, claimToken string, publishedAt time.Time) error {
	if r.fail {
		r.fail = false
		return errors.New("injected post-start persistence failure")
	}
	return r.OutboxEventRepository.MarkPublished(ctx, eventID, claimToken, publishedAt)
}

func (e *stubFrozenDefinitionEngine) StartProcessByDefinitionID(ctx context.Context, definitionID int, businessKey, _ string, _ int, _ map[string]any) (*ent.ProcessInstance, error) {
	e.calls++
	e.definitionID = definitionID
	e.businessKey = businessKey
	e.tenantID, _ = ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if e.err != nil {
		return nil, e.err
	}
	return &ent.ProcessInstance{ID: 91, ProcessDefinitionID: definitionID, BusinessKey: businessKey}, nil
}

func TestWorkflowStartOutboxDispatcherUsesFrozenDefinitionAndPublishes(t *testing.T) {
	repo, client := newOutboxRepository(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	repo.clock = func() time.Time { return now }
	payload := validWorkflowStartPayload(7, 501, 22)
	seedWorkflowStartEvent(t, repo, payload, now.Add(-time.Second))
	engine := &stubFrozenDefinitionEngine{}
	dispatcher := NewWorkflowStartOutboxDispatcher(repo, engine, WorkflowStartOutboxConfig{BatchSize: 10, PollInterval: time.Second, MaxAttempts: 3})
	dispatcher.now = func() time.Time { return now }

	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	require.Equal(t, 1, engine.calls)
	require.Equal(t, 22, engine.definitionID)
	require.Equal(t, NewWorkflowStartEventID(501, 22), engine.businessKey)
	require.Equal(t, 7, engine.tenantID)
	event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(payload.DedupeKey)).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, outboxEventStatusPublished, event.Status)
}

func TestWorkflowStartOutboxDispatcherRetriesThenMarksDeadWithSafeAudit(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		repo, client := newOutboxRepository(t)
		now := time.Now().UTC().Truncate(time.Millisecond)
		repo.clock = func() time.Time { return now }
		payload := validWorkflowStartPayload(7, 502, 23)
		seedWorkflowStartEvent(t, repo, payload, now.Add(-time.Second))
		engine := &stubFrozenDefinitionEngine{err: errors.New("database password=workflow-secret")}
		dispatcher := NewWorkflowStartOutboxDispatcher(repo, engine, WorkflowStartOutboxConfig{BatchSize: 1, PollInterval: time.Second, MaxAttempts: 2})
		dispatcher.now = func() time.Time { return now }

		require.NoError(t, dispatcher.DispatchOnce(context.Background()))
		event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(payload.DedupeKey)).Only(context.Background())
		require.NoError(t, err)
		require.Equal(t, outboxEventStatusPending, event.Status)
		require.Equal(t, 1, event.AttemptCount)
		require.NotContains(t, event.LastError, "workflow-secret")
	})

	t.Run("invalid payload dead letters", func(t *testing.T) {
		repo, client := newOutboxRepository(t)
		now := time.Now().UTC().Truncate(time.Millisecond)
		repo.clock = func() time.Time { return now }
		_, err := repo.Enqueue(context.Background(), nil, NewOutboxEvent{
			EventID: "workflow-start:503:24", EventType: workflowStartRequestedEventType,
			TenantID: 7, AggregateType: "work_item", AggregateID: "503",
			Payload: json.RawMessage(`{"tenantId":7,"workItemId":503,"unexpected":"secret"}`), NextAttemptAt: now.Add(-time.Second),
		})
		require.NoError(t, err)
		dispatcher := NewWorkflowStartOutboxDispatcher(repo, &stubFrozenDefinitionEngine{}, WorkflowStartOutboxConfig{BatchSize: 1, PollInterval: time.Second, MaxAttempts: 1})
		dispatcher.now = func() time.Time { return now }

		require.NoError(t, dispatcher.DispatchOnce(context.Background()))
		event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ("workflow-start:503:24")).Only(context.Background())
		require.NoError(t, err)
		require.Equal(t, outboxEventStatusDead, event.Status)
		auditCount, err := client.AuditLog.Query().Where(auditlog.ActionEQ("intake.workflow_start.manual_intervention_required")).Count(context.Background())
		require.NoError(t, err)
		require.Equal(t, 1, auditCount)
	})
}

func TestWorkflowStartOutboxDispatcherRecoversCrashAfterProcessStart(t *testing.T) {
	repo, client := newOutboxRepository(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	repo.clock = func() time.Time { return now }
	payload := validWorkflowStartPayload(7, 504, 25)
	seedWorkflowStartEvent(t, repo, payload, now.Add(-time.Second))
	engine := &stubFrozenDefinitionEngine{}
	flaky := &failPublishOnceWorkflowRepository{OutboxEventRepository: repo, fail: true}
	dispatcher := NewWorkflowStartOutboxDispatcher(flaky, engine, WorkflowStartOutboxConfig{BatchSize: 1, PollInterval: time.Second, MaxAttempts: 3})
	dispatcher.now = func() time.Time { return now }

	require.Error(t, dispatcher.DispatchOnce(context.Background()))
	now = now.Add(outboxEventClaimLeaseDuration + time.Second)
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	require.Equal(t, 2, engine.calls, "delivery is retried; the exact-definition engine owns process idempotency")
	event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(payload.DedupeKey)).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, outboxEventStatusPublished, event.Status)
}

func validWorkflowStartPayload(tenantID, workItemID, definitionID int) WorkflowStartRequested {
	return WorkflowStartRequested{
		TenantID: tenantID, WorkItemID: workItemID, RecordClass: "incident",
		WorkflowDefinitionID: definitionID, WorkflowDefinitionKey: "incident-flow", WorkflowDefinitionVersion: "1",
		ActorID: 31, Channel: "itsm_web", IntakeRequestID: 41,
		DedupeKey: NewWorkflowStartEventID(workItemID, definitionID),
	}
}

func seedWorkflowStartEvent(t *testing.T, repo *OutboxEventRepository, payload WorkflowStartRequested, due time.Time) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	_, err = repo.Enqueue(context.Background(), nil, NewOutboxEvent{
		EventID: payload.DedupeKey, EventType: workflowStartRequestedEventType, TenantID: payload.TenantID,
		AggregateType: "work_item", AggregateID: jsonNumber(payload.WorkItemID), Payload: encoded, NextAttemptAt: due,
	})
	require.NoError(t, err)
}

func jsonNumber(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
