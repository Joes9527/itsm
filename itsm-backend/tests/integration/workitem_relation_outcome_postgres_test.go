//go:build integration_postgres

package integration

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	relationmeta "itsm-backend/common/workitemrelation"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/notification"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/service"
)

// B2 step 4 (slice A): recording a Change professional outcome must emit one
// durable delivery event in the same transaction as the outcome write and its
// immutable receipt.
func TestWorkItemChangeOutcomeEventProducer(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	f.authorize(t)
	finish := f.command("record_outcome", "b2-outcome")
	finish.Outcome = "successful"
	end := time.Now()
	finish.ActualEnd = &end
	result := f.apply(t, finish)

	events := f.client.OutboxEvent.Query().Where(outboxevent.EventType(service.ChangeOutcomeEventType)).AllX(f.ctx)
	require.Len(t, events, 1, "recording an outcome must emit exactly one outcome event")
	event := events[0]
	require.Equal(t, f.tenant.ID, event.TenantID)
	require.Equal(t, "work_item", event.AggregateType)
	require.Equal(t, fmt.Sprint(f.c.WorkItemID), event.AggregateID)

	var facts service.ChangeOutcomeFacts
	require.NoError(t, json.Unmarshal(event.Payload, &facts))
	require.Equal(t, "successful", facts.Outcome)
	require.Equal(t, f.c.WorkItemID, facts.WorkItemID)
	require.Equal(t, f.c.ID, facts.ChangeID)
	require.Equal(t, result.Version, facts.Version)
	require.Equal(t, finish.Meta.OperationID, facts.OperationID)
	require.Equal(t, f.tenant.ID, facts.TenantID)
	require.Equal(t, fmt.Sprintf("change-outcome:%d:%d", facts.WorkItemID, facts.Version), event.EventID)
}

// newChangeOutcomeDeliveryWorker builds the real registered consumer path for the
// Change outcome event.
func newChangeOutcomeDeliveryWorker(t *testing.T, f *changeLifecycleFixture) *service.OutboxDeliveryWorker {
	t.Helper()
	logger := zap.NewNop().Sugar()
	notifier := service.NewTicketNotificationService(f.client, logger)
	notifier.SetNotificationPreferenceService(service.NewNotificationPreferenceService(f.client, logger))
	registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{
		service.NewChangeOutcomeDeliveryHandler(f.client, notifier, logger),
	})
	require.NoError(t, err)
	worker, err := service.NewOutboxDeliveryWorker(
		service.NewOutboxEventRepository(f.client),
		service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 20 * time.Second, MaxAttempts: 3},
		logger,
		registry,
	)
	require.NoError(t, err)
	return worker
}

// linkRequiredFix attaches a REQUIRED resolved_by_change dependency from a Problem
// WorkItem to the fixture's Change WorkItem and returns the Problem WorkItem and
// the user that must be prompted.
func linkRequiredFix(t *testing.T, f *changeLifecycleFixture) (*ent.Ticket, *ent.User) {
	t.Helper()
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).
		SetTitle("problem awaiting verified fix").SetTicketNumber("PRB-OUTCOME").SetRecordClass("problem").
		SetStatus("investigating").SetPriority("high").SaveX(f.ctx)
	f.client.Problem.Create().SetWorkItemID(item.ID).SaveX(f.ctx)
	assignee := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("b2-problem-owner").SetName("b2 problem owner").
		SetRole("agent").SetActive(true).SetEmail("b2-problem-owner@example.test").SetPasswordHash("test").SaveX(f.ctx)
	item.Update().SetAssigneeID(assignee.ID).ExecX(f.ctx)
	f.client.WorkItemRelation.Create().SetTenantID(f.tenant.ID).SetSourceWorkItemID(item.ID).
		SetTargetWorkItemID(f.c.WorkItemID).SetRelationType("resolved_by_change").SetCreatedByID(f.actor.ID).
		SetMetadata(relationmeta.Metadata{Required: true}).SaveX(f.ctx)
	return item, assignee
}

// A successful Change must prompt verification from the Problem it is the
// resolved_by_change for, through the existing notification pipeline.
func TestWorkItemChangeOutcomeConsumerRequestsProblemVerification(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	problemItem, assignee := linkRequiredFix(t, f)
	f.authorize(t)
	finish := f.command("record_outcome", "b2-verify")
	finish.Outcome = "successful"
	end := time.Now()
	finish.ActualEnd = &end
	f.apply(t, finish)

	worker := newChangeOutcomeDeliveryWorker(t, f)
	require.NoError(t, worker.DispatchOnce(f.ctx))

	rows := f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(assignee.ID)).AllX(f.ctx)
	require.Len(t, rows, 1, "the resolved_by_change problem owner must receive one verification prompt")
	require.Equal(t, service.ChangeOutcomeEventType, rows[0].Type)
	require.NotNil(t, rows[0].DeliveryKey)

	// Re-dispatch must not duplicate the prompt.
	require.NoError(t, worker.DispatchOnce(f.ctx))
	require.Len(t, f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(assignee.ID)).AllX(f.ctx), 1)
	require.Equal(t, problemItem.ID, problemItem.ID)
}

// A non-successful outcome is a declared "no verification required" case: it must
// not prompt, but the decision must remain visible as an audit record rather than a
// silent skip.
func TestWorkItemChangeOutcomeConsumerRecordsDeclaredSkip(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	_, assignee := linkRequiredFix(t, f)
	f.authorize(t)
	finish := f.command("record_outcome", "b2-failed")
	finish.Outcome = "failed"
	end := time.Now()
	finish.ActualEnd = &end
	f.apply(t, finish)

	worker := newChangeOutcomeDeliveryWorker(t, f)
	require.NoError(t, worker.DispatchOnce(f.ctx))

	require.Zero(t, f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(assignee.ID)).CountX(f.ctx),
		"a failed outcome must not request verification")
	decisions := f.client.AuditLog.Query().Where(auditlog.TenantID(f.tenant.ID), auditlog.Resource("change_outcome"), auditlog.Action("verification_not_required")).AllX(f.ctx)
	require.Len(t, decisions, 1, "the declared skip must be observable as an audit record")
	require.Equal(t, fmt.Sprint(f.c.WorkItemID), decisions[0].Path)
}
