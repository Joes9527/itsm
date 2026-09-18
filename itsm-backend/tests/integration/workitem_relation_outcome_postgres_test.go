//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	executionfixture "itsm-backend/tests/fixtures/execution"

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
	grantWorkItemDeliveryPermission(t, f.client, f.ctx, f.tenant.ID, "agent", "problem")
	clients, cfg := runtimeClients(t, f.incidentEffectsFixture)
	_, grantErr := f.db.ExecContext(f.ctx, "GRANT SELECT ON work_item_relations,notification_preferences TO "+cfg.User)
	require.NoError(t, grantErr)
	notifier := service.NewTicketNotificationService(clients.Tenant, logger, executionfixture.Standard())
	notifier.SetNotificationPreferenceService(service.NewNotificationPreferenceService(clients.Tenant, logger))
	registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{
		service.NewChangeOutcomeDeliveryHandler(clients.Tenant, clients.IntakeDirectorySnapshot(), notifier, logger),
	})
	require.NoError(t, err)
	worker, err := service.NewOutboxDeliveryWorker(
		service.NewOutboxEventRepository(f.client, executionfixture.Standard()),
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
	_, assignee := linkRequiredFix(t, f)
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

	// A genuine re-delivery of the same durable event must not duplicate the prompt.
	// The event is returned to pending first: claiming only selects pending rows, so
	// dispatching a published event again would assert nothing.
	reopenOutboxEvent(t, f.client, f.ctx, f.client.OutboxEvent.Query().Where(outboxevent.EventType(service.ChangeOutcomeEventType)).OnlyX(f.ctx).ID)
	require.NoError(t, worker.DispatchOnce(f.ctx))
	require.Len(t, f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(assignee.ID)).AllX(f.ctx), 1)
}

// reopenOutboxEvent returns a delivered event to the claimable pending state, which
// is what a claim-expiry recovery produces, so a repeated DispatchOnce exercises the
// consumer's idempotency instead of silently doing nothing.
func reopenOutboxEvent(t *testing.T, client *ent.Client, ctx context.Context, eventID int) {
	t.Helper()
	client.OutboxEvent.UpdateOneID(eventID).
		SetStatus("pending").
		ClearClaimToken().
		ClearClaimExpiresAt().
		SetNextAttemptAt(time.Now().Add(-time.Minute)).
		ExecX(ctx)
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

// linkFixDependency attaches a resolved_by_change dependency from the fixture's
// Problem WorkItem to a Change WorkItem whose authoritative outcome is `outcome`.
func linkFixDependency(f *changeOutcomeDriver, t *testing.T, outcome string, required bool) *ent.Change {
	t.Helper()
	changeItem := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).
		SetTitle("fix dependency").SetTicketNumber("CHG-DEP").SetRecordClass("change_request").
		SetStatus("in_progress").SetPriority("high").SaveX(f.ctx)
	changeRecord := f.client.Change.Create().SetWorkItemID(changeItem.ID).SetOutcome(outcome).SaveX(f.ctx)
	f.client.WorkItemRelation.Create().SetTenantID(f.tenant.ID).SetSourceWorkItemID(f.problemWorkItemID).
		SetTargetWorkItemID(changeItem.ID).SetRelationType("resolved_by_change").SetCreatedByID(f.actor.ID).
		SetMetadata(relationmeta.Metadata{Required: required}).SaveX(f.ctx)
	return changeRecord
}

// changeOutcomeDriver adapts the problem lifecycle fixture for the outcome tests.
type changeOutcomeDriver struct {
	*problemLifecycleFixture
	problemWorkItemID int
}

func newChangeOutcomeDriver(t *testing.T) *changeOutcomeDriver {
	t.Helper()
	f := newProblemLifecycleFixture(t)
	return &changeOutcomeDriver{problemLifecycleFixture: f, problemWorkItemID: f.p.WorkItemID}
}

func (f *changeOutcomeDriver) readyToResolve(t *testing.T) {
	t.Helper()
	f.apply(t, "investigate", "investigate")
	f.evidence(t)
	f.apply(t, "verify_resolution", "verify")
}

// B2 step 4 (slice B): Problem resolve must be blocked while a REQUIRED
// resolved_by_change dependency has no successful authoritative Change outcome, and
// the block must lift only when the owning change domain reports success.
func TestWorkItemProblemResolveRequiresSuccessfulFixDependency(t *testing.T) {
	f := newChangeOutcomeDriver(t)
	changeRecord := linkFixDependency(f, t, "failed", true)
	f.readyToResolve(t)

	_, err := f.owner.ApplyCommand(f.ctx, f.command("resolve", "blocked"))
	require.ErrorContains(t, err, "required fix dependency")
	require.Equal(t, "investigating", f.client.Ticket.GetX(f.ctx, f.p.WorkItemID).Status,
		"a blocked resolve must not advance the problem")

	changeRecord.Update().SetOutcome("successful").ExecX(f.ctx)
	result := f.apply(t, "resolve", "allowed")
	require.Equal(t, "resolved", result.Status)
}

// An optional (non-required) resolved_by_change dependency must never block resolve:
// only dependencies explicitly marked as required fix dependencies do.
func TestWorkItemProblemResolveIgnoresOptionalFixDependency(t *testing.T) {
	f := newChangeOutcomeDriver(t)
	linkFixDependency(f, t, "failed", false)
	f.readyToResolve(t)

	result := f.apply(t, "resolve", "optional-ok")
	require.Equal(t, "resolved", result.Status)
}

// newProblemResolvedDeliveryWorker builds the real registered consumer path for the
// problem-resolved event.
func newProblemResolvedDeliveryWorker(t *testing.T, f *problemLifecycleFixture) *service.OutboxDeliveryWorker {
	t.Helper()
	logger := zap.NewNop().Sugar()
	grantWorkItemDeliveryPermission(t, f.client, f.ctx, f.tenant.ID, "agent", "incident")
	clients, cfg := runtimeClients(t, f.incidentEffectsFixture)
	_, grantErr := f.db.ExecContext(f.ctx, "GRANT SELECT ON work_item_relations,notification_preferences TO "+cfg.User)
	require.NoError(t, grantErr)
	notifier := service.NewTicketNotificationService(clients.Tenant, logger, executionfixture.Standard())
	notifier.SetNotificationPreferenceService(service.NewNotificationPreferenceService(clients.Tenant, logger))
	registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{
		service.NewProblemResolvedDeliveryHandler(clients.Tenant, clients.IntakeDirectorySnapshot(), notifier, logger),
	})
	require.NoError(t, err)
	worker, err := service.NewOutboxDeliveryWorker(
		service.NewOutboxEventRepository(f.client, executionfixture.Standard()),
		service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 20 * time.Second, MaxAttempts: 3},
		logger,
		registry,
	)
	require.NoError(t, err)
	return worker
}

// B2 step 4 (slice C): resolving a Problem notifies the handler of every Incident
// that investigated it, and must never close those Incidents as a side effect.
func TestWorkItemProblemResolvedNotifiesIncidentHandler(t *testing.T) {
	f := newChangeOutcomeDriver(t)
	incidentItem := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).
		SetTitle("recurring outage").SetTicketNumber("INC-HANDLER").SetRecordClass("incident").
		SetStatus("in_progress").SetPriority("high").SaveX(f.ctx)
	f.client.Incident.Create().SetWorkItemID(incidentItem.ID).SetSeverity("high").SetDetectedAt(time.Now()).SaveX(f.ctx)
	handler := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("b2-incident-handler").SetName("b2 incident handler").
		SetRole("agent").SetActive(true).SetEmail("b2-incident-handler@example.test").SetPasswordHash("test").SaveX(f.ctx)
	incidentItem.Update().SetAssigneeID(handler.ID).ExecX(f.ctx)
	f.client.WorkItemRelation.Create().SetTenantID(f.tenant.ID).SetSourceWorkItemID(incidentItem.ID).
		SetTargetWorkItemID(f.p.WorkItemID).SetRelationType("investigated_by").SetCreatedByID(f.actor.ID).SaveX(f.ctx)

	f.readyToResolve(t)
	f.apply(t, "resolve", "solved")

	events := f.client.OutboxEvent.Query().Where(outboxevent.EventType(service.ProblemResolvedEventType)).AllX(f.ctx)
	require.Len(t, events, 1, "resolving a problem must emit exactly one resolution event")

	worker := newProblemResolvedDeliveryWorker(t, f.problemLifecycleFixture)
	require.NoError(t, worker.DispatchOnce(f.ctx))
	rows := f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(handler.ID)).AllX(f.ctx)
	require.Len(t, rows, 1, "the investigating incident handler must be notified once")
	require.Equal(t, service.ProblemResolvedEventType, rows[0].Type)

	reopenOutboxEvent(t, f.client, f.ctx, events[0].ID)
	require.NoError(t, worker.DispatchOnce(f.ctx))
	require.Len(t, f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(handler.ID)).AllX(f.ctx), 1,
		"a genuine re-delivery must not duplicate the notification")

	require.Equal(t, "in_progress", f.client.Ticket.GetX(f.ctx, incidentItem.ID).Status,
		"resolving the problem must not close the investigating incident")
}
