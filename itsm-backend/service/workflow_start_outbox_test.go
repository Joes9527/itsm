package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"itsm-backend/service/bpmn"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/ent/outboxevent"
)

func workflowStartFixture(t *testing.T, xml ...[]byte) (*bpmnAuthorizationFixture, *ent.OutboxEvent) {
	return workflowStartFixtureForActor(t, false, xml...)
}

func workflowStartFixtureForActor(t *testing.T, nativeMSP bool, xml ...[]byte) (*bpmnAuthorizationFixture, *ent.OutboxEvent) {
	t.Helper()
	f := newBPMNAuthorizationFixture(t)
	ctx := context.Background()
	if nativeMSP {
		provider := f.client.Tenant.Create().SetCode("start-provider").SetName("Provider").SetType("msp_provider").SaveX(ctx)
		f.actor = f.client.User.Create().SetTenantID(provider.ID).SetUsername("native-operator").SetName("Native Operator").SetEmail("native@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(ctx)
	}
	if len(xml) > 0 {
		f.definition = f.client.ProcessDefinition.UpdateOneID(f.definition.ID).SetBpmnXML(xml[0]).SaveX(ctx)
	}
	item := f.client.Ticket.Create().SetTitle("VPN").SetTicketNumber("TKT-202609-000001").SetRecordClass("generic").SetTenantID(f.tenant.ID).SetRequesterID(f.outsider.ID).SetOpenedByID(f.actor.ID).SaveX(ctx)
	receipt := f.client.IntakeRequest.Create().SetTenantID(f.tenant.ID).SetActorTenantID(f.actor.TenantID).SetActorID(f.actor.ID).SetRequesterID(f.outsider.ID).SetChannel("itsm_web").SetOperation("create_work_item").SetIdempotencyKey("one").SetRequestDigest("digest").SetDigestVersion("intake-v3").SetStatus("completed").SetWorkItemID(item.ID).SaveX(ctx)
	f.client.IntakeResolutionSnapshot.Create().SetTenantID(f.tenant.ID).SetIntakeRequestID(receipt.ID).SetWorkItemID(item.ID).SetChannel("itsm_web").SetSourceProvider("itsm_web").SetRecordClass("generic").SetWorkflowDefinitionID(f.definition.ID).SetWorkflowDefinitionKey(f.definition.Key).SetWorkflowDefinitionVersion(f.definition.Version).SetResolverVersion("test").SetRequestDigest("digest").SaveX(ctx)
	key := fmt.Sprintf("workflow-start:%d:%d", item.ID, f.definition.ID)
	payload, err := json.Marshal(map[string]any{"tenantId": f.tenant.ID, "workItemId": item.ID, "recordClass": "generic", "workflowDefinitionId": f.definition.ID, "workflowDefinitionKey": f.definition.Key, "workflowDefinitionVersion": f.definition.Version, "workflowDefinitionDigest": FreezeProcessDefinition(f.definition).Digest, "actorId": f.actor.ID, "channel": "itsm_web", "intakeRequestId": receipt.ID, "dedupeKey": key, "variables": map[string]any{"work_item_id": item.ID, "tenant_id": item.TenantID, "record_class": item.RecordClass, "requester_id": f.outsider.ID, "triggered_by": fmt.Sprint(f.actor.ID), "channel": "itsm_web"}})
	require.NoError(t, err)
	event, err := NewOutboxEventRepository(f.client).Enqueue(ctx, nil, NewOutboxEvent{EventID: key, EventType: "workflow.start.requested", TenantID: f.tenant.ID, AggregateType: "work_item", AggregateID: fmt.Sprint(item.ID), Payload: payload, NextAttemptAt: time.Now().UTC().Add(-time.Second)})
	require.NoError(t, err)
	return f, event
}

func TestWorkflowStartDeliveryReplaysAfterCommitBeforeAcknowledgement(t *testing.T) {
	f, event := workflowStartFixture(t)
	handler := NewWorkflowStartOutboxHandler(f.client, f.engine, f.client)
	registry, err := NewOutboxEventTypeRegistry([]OutboxDeliveryHandler{handler})
	require.NoError(t, err)
	repo := NewOutboxEventRepository(f.client)
	worker, err := NewOutboxDeliveryWorker(repo, OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), registry)
	require.NoError(t, err)
	now := time.Now().UTC()
	worker.now = func() time.Time { return now }
	repo.clock = worker.now
	crash := true
	f.client.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if change, ok := m.(*ent.OutboxEventMutation); ok {
				if status, ok := change.Status(); ok && status == "published" && crash {
					crash = false
					return nil, errors.New("injected receipt acknowledgement loss")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	require.ErrorContains(t, worker.DispatchOnce(context.Background()), "injected receipt acknowledgement loss")
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(context.Background()))
	require.Equal(t, "ticket", f.client.ProcessInstance.Query().OnlyX(context.Background()).BusinessType)
	require.Equal(t, "publishing", f.client.OutboxEvent.GetX(context.Background(), event.ID).Status)
	staleToken := f.client.OutboxEvent.GetX(context.Background(), event.ID).ClaimToken
	now = now.Add(outboxEventClaimLeaseDuration + time.Second)
	require.NoError(t, worker.DispatchOnce(context.Background()))
	require.NoError(t, worker.DispatchOnce(context.Background()))
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(context.Background()))
	require.Equal(t, "published", f.client.OutboxEvent.GetX(context.Background(), event.ID).Status)
	require.ErrorIs(t, repo.MarkPublished(context.Background(), event.ID, staleToken, now), ErrOutboxEventClaimLost)
}

func TestWorkflowStartDeliveryBlocksMalformedOrConflictingEvidence(t *testing.T) {
	for _, mutation := range []string{"malformed", "actor", "class", "definition", "unknown"} {
		t.Run(mutation, func(t *testing.T) {
			f, event := workflowStartFixture(t)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(event.Payload, &payload))
			switch mutation {
			case "actor":
				payload["actorId"] = f.outsider.ID
			case "class":
				payload["recordClass"] = "incident"
			case "definition":
				payload["workflowDefinitionVersion"] = "changed"
			case "unknown":
				payload["surprise"] = true
			}
			raw, err := json.Marshal(payload)
			require.NoError(t, err)
			if mutation == "malformed" {
				raw = []byte(`{"tenantId":"invalid"}`)
			}
			// Payload is immutable; recreate only the fixture event.
			f.client.OutboxEvent.DeleteOneID(event.ID).ExecX(context.Background())
			_, err = NewOutboxEventRepository(f.client).Enqueue(context.Background(), nil, NewOutboxEvent{EventID: event.EventID, EventType: event.EventType, TenantID: event.TenantID, AggregateType: event.AggregateType, AggregateID: event.AggregateID, Payload: raw, NextAttemptAt: time.Now().UTC().Add(-time.Second)})
			require.NoError(t, err)
			handler := NewWorkflowStartOutboxHandler(f.client, f.engine, f.client)
			registry, err := NewOutboxEventTypeRegistry([]OutboxDeliveryHandler{handler})
			require.NoError(t, err)
			worker, err := NewOutboxDeliveryWorker(NewOutboxEventRepository(f.client), OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), registry)
			require.NoError(t, err)
			require.NoError(t, worker.DispatchOnce(context.Background()))
			require.Equal(t, "blocked", f.client.OutboxEvent.Query().Where(outboxevent.EventID(event.EventID)).OnlyX(context.Background()).Status)
			require.Zero(t, f.client.ProcessInstance.Query().CountX(context.Background()))
		})
	}
}

func TestWorkflowStartUsesRequestedForTaskAssignmentAndTenantScope(t *testing.T) {
	xml := strings.Replace(string(startProcessUserTaskXML()), `<bpmn:userTask id="approval" name="Approval" />`, `<bpmn:userTask id="approval" name="Approval"><bpmn:extensionElements><bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData><bpmn:metaData name="action">update_status</bpmn:metaData></bpmn:extensionElements></bpmn:userTask>`, 1)
	f, event := workflowStartFixture(t, []byte(xml))
	handler := f.engine.CallbackRegistry().GetHandler("ticket_service_handler")
	require.NotNil(t, handler)
	handler.(*bpmn.TicketServiceTaskHandler).SetTicketService(NewTicketServiceForTest(f.client, zap.NewNop().Sugar()))
	ctx := context.Background()
	require.NoError(t, NewWorkflowStartOutboxHandler(f.client, f.engine, f.client).Deliver(ctx, event))
	task := f.client.ProcessTask.Query().OnlyX(ctx)
	require.Equal(t, fmt.Sprint(f.outsider.ID), task.Assignee, "requested-for user, not the event actor, owns the user task")
	require.Error(t, f.engine.CompleteTask(f.scopedCtx(false, false, false, false), task.TaskID, nil))
	otherCtx := WithBPMNAccessScope(ctx, BPMNAccessScope{TenantID: f.otherTenant.ID, UserID: f.otherActor.ID})
	require.Error(t, f.engine.CompleteTask(otherCtx, task.TaskID, nil))
	requesterCtx := WithBPMNAccessScope(ctx, BPMNAccessScope{TenantID: f.tenant.ID, UserID: f.outsider.ID})
	require.NoError(t, f.engine.CompleteTask(requesterCtx, task.TaskID, map[string]interface{}{"new_status": "in_progress"}))
	require.Equal(t, "in_progress", f.client.Ticket.Query().OnlyX(ctx).Status)
	require.Equal(t, bpmnCallbackStatusCompleted, f.client.ProcessCallbackOutbox.Query().OnlyX(ctx).Status)
	require.Equal(t, "completed", f.client.ProcessInstance.Query().OnlyX(ctx).Status)
}

func TestWorkflowStartDeliveryReplaysAfterDefinitionMetadataChanges(t *testing.T) {
	f, event := workflowStartFixture(t)
	ctx := context.Background()
	handler := NewWorkflowStartOutboxHandler(f.client, f.engine, f.client)
	require.NoError(t, handler.Deliver(ctx, event))
	first := f.client.ProcessInstance.Query().OnlyX(ctx)
	f.client.ProcessDefinition.UpdateOneID(f.definition.ID).SetVersion("edited").SetBpmnXML(startProcessUserTaskXML()).SetIsActive(false).ExecX(ctx)
	require.NoError(t, handler.Deliver(ctx, event))
	require.Equal(t, first.ID, f.client.ProcessInstance.Query().OnlyX(ctx).ID)
}

func TestWorkflowStartPreservesNativeMSPActor(t *testing.T) {
	f, event := workflowStartFixtureForActor(t, true)
	ctx := context.Background()
	handler := NewWorkflowStartOutboxHandler(f.client, f.engine, f.client)
	require.NoError(t, handler.Deliver(ctx, event))
	require.Equal(t, fmt.Sprint(f.actor.ID), f.client.ProcessInstance.Query().OnlyX(ctx).Initiator)
}

// An upgrade must not change the digest of a start that committed before its
// outbox acknowledgement. The old handler passed the frozen variables verbatim.
func TestWorkflowStartReplaysCommittedStartBeforeProvenanceUpgrade(t *testing.T) {
	f, event := workflowStartFixture(t)
	var payload workflowStartPayload
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&payload))
	ctx := WithTrustedBPMNTenantContext(context.Background(), f.tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.actor.ID)
	first, err := f.engine.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition),
		fmt.Sprintf("ticket:%d", payload.WorkItemID), "ticket", payload.WorkItemID,
		payload.Variables, payload.DedupeKey)
	require.NoError(t, err)
	audits := f.client.ProcessAuditLog.Query().CountX(ctx)

	require.NoError(t, NewWorkflowStartOutboxHandler(f.client, f.engine, f.client).Deliver(ctx, event))
	stored := f.client.ProcessInstance.Query().OnlyX(ctx)
	require.Equal(t, first.ID, stored.ID)
	require.Equal(t, first.StartRequestDigest, stored.StartRequestDigest)
	require.Equal(t, audits, f.client.ProcessAuditLog.Query().CountX(ctx))
}
