//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/notification"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/workitemrelation"
	"itsm-backend/service"
)

// B2 step 2: a directed relation mutation must emit one durable delivery event in
// the same transaction as the relation row, the source version bump and the
// immutable receipt. The payload is the reviewed RelationFacts handoff.
func TestWorkItemRelationEventsProducerSameTransaction(t *testing.T) {
	f := newRelationFixture(t)
	cmd := f.command("b2-producer")
	result, err := f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)

	events := f.client.OutboxEvent.Query().Where(outboxevent.EventType("work_item.relation_created")).AllX(f.ctx)
	require.Len(t, events, 1, "directed relation create must emit exactly one delivery event")
	event := events[0]
	require.Equal(t, f.tenant.ID, event.TenantID)
	require.Equal(t, "work_item", event.AggregateType)
	require.Equal(t, strconv.Itoa(f.inc.WorkItemID), event.AggregateID)

	var facts service.RelationFacts
	require.NoError(t, json.Unmarshal(event.Payload, &facts))
	require.Equal(t, "investigated_by", facts.Type)
	require.False(t, facts.Removed)
	require.Equal(t, f.inc.WorkItemID, facts.MutationWorkItemID)
	require.Equal(t, f.problem.ID, facts.TargetID)
	require.Equal(t, cmd.Meta.OperationID, facts.OperationID)
	require.Equal(t, result.Version, facts.Version)
	require.Equal(t, f.tenant.ID, facts.TenantID)
	require.Equal(t, f.actor.ID, facts.ActorID)
	require.Equal(t, fmt.Sprintf("work-item-relation:%d:%d:created", facts.RelationID, facts.Version), event.EventID)
}

// Removal of a directed relation emits the removal variant with Removed set.
func TestWorkItemRelationEventsProducerRemoval(t *testing.T) {
	f := newRelationFixture(t)
	added, err := f.owner.Apply(f.ctx, f.command("b2-remove-add"), false)
	require.NoError(t, err)
	remove := f.command("b2-remove")
	remove.Meta.ExpectedVersion = added.Version
	result, err := f.owner.Apply(f.ctx, remove, true)
	require.NoError(t, err)

	events := f.client.OutboxEvent.Query().Where(outboxevent.EventType("work_item.relation_removed")).AllX(f.ctx)
	require.Len(t, events, 1, "directed relation removal must emit exactly one removal event")
	var facts service.RelationFacts
	require.NoError(t, json.Unmarshal(events[0].Payload, &facts))
	require.True(t, facts.Removed)
	require.Equal(t, "investigated_by", facts.Type)
	require.Equal(t, result.Version, facts.Version)
	require.Equal(t, fmt.Sprintf("work-item-relation:%d:%d:removed", facts.RelationID, facts.Version), events[0].EventID)
	// The create event from the same relation remains durable and untouched.
	require.Len(t, f.client.OutboxEvent.Query().Where(outboxevent.EventType("work_item.relation_created")).AllX(f.ctx), 1)
}

// Symmetric/structural relations stay out of the delivery surface: they persist a
// relation row but must not emit a delivery event whose consumer has nothing to do.
func TestWorkItemRelationEventsSymmetricRelationEmitsNoDeliveryEvent(t *testing.T) {
	f := newRelationFixture(t)
	related := f.command("b2-symmetric")
	related.Type = "related_to"
	_, err := f.owner.Apply(f.ctx, related, false)
	require.NoError(t, err)
	require.Equal(t, 1, f.client.WorkItemRelation.Query().Where(workitemrelation.DeletedAtIsNil()).CountX(f.ctx))
	require.Zero(t, f.client.OutboxEvent.Query().Where(outboxevent.EventTypeIn("work_item.relation_created", "work_item.relation_removed")).CountX(f.ctx))
}

// A fault while persisting the delivery event must roll back the whole mutation:
// no relation row, no version bump, no receipt, no event.
func TestWorkItemRelationEventsProducerRollsBackWithTransaction(t *testing.T) {
	f := newRelationFixture(t)
	before := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	enabled := true
	hook := func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if enabled {
				return nil, errors.New("injected outbox fault")
			}
			return next.Mutate(ctx, m)
		})
	}
	f.runtime.Tenant.OutboxEvent.Use(hook)
	_, err := f.owner.Apply(f.ctx, f.command("b2-outbox-fault"), false)
	require.ErrorContains(t, err, "injected outbox fault")
	enabled = false
	require.Zero(t, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
	require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
	// The shared fixture already seeds one unrelated incident.created event; the
	// invariant here is that no relation delivery event survived the rollback.
	relationEvents := f.client.OutboxEvent.Query().Where(outboxevent.EventTypeIn(service.RelationCreatedEventType, service.RelationRemovedEventType))
	require.Zero(t, relationEvents.CountX(f.ctx))

	// The same operation succeeds once the fault is gone, proving the rollback was clean.
	_, err = f.owner.Apply(f.ctx, f.command("b2-outbox-fault"), false)
	require.NoError(t, err)
	require.Len(t, f.client.OutboxEvent.Query().Where(outboxevent.EventType(service.RelationCreatedEventType)).AllX(f.ctx), 1)
}

// newRelationDeliveryWorker builds the real registered consumer path: the shared
// outbox worker dispatching through the production registry.
func newRelationDeliveryWorker(t *testing.T, f *relationFixture) *service.OutboxDeliveryWorker {
	t.Helper()
	// Existing delivery fixtures declare valid customer recipient roles explicitly.
	for _, code := range []string{"agent", "admin"} {
		grantWorkItemDeliveryPermission(t, f.client, f.ctx, f.tenant.ID, code, "problem")
	}
	return newRuntimeRelationDeliveryWorker(t, f)
}

func relationEventsOfType(f *relationFixture, eventType string) []*ent.OutboxEvent {
	return f.client.OutboxEvent.Query().Where(outboxevent.EventType(eventType)).AllX(f.ctx)
}

// B2 step 3: the registered consumer delivers exactly one in-app notification to
// the counterpart endpoint's assignee, keyed durably so a repeated delivery cannot
// duplicate the effect.
func TestWorkItemRelationEventsConsumerDeliversOnce(t *testing.T) {
	f := newRelationFixture(t)
	assignee := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("b2-counterpart").SetName("b2 counterpart").
		SetRole("agent").SetActive(true).SetEmail("b2-counterpart@example.test").SetPasswordHash("test").SaveX(f.ctx)
	f.problem.Update().SetAssigneeID(assignee.ID).ExecX(f.ctx)

	_, err := f.owner.Apply(f.ctx, f.command("b2-consumer"), false)
	require.NoError(t, err)
	event := relationEventsOfType(f, service.RelationCreatedEventType)[0]

	worker := newRelationDeliveryWorker(t, f)
	require.NoError(t, worker.DispatchOnce(f.ctx))

	rows := f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(assignee.ID)).AllX(f.ctx)
	require.Len(t, rows, 1, "counterpart assignee must receive exactly one in-app notification")
	require.NotNil(t, rows[0].DeliveryKey, "the durable dedup key must be recorded")
	require.Equal(t, event.EventID+":"+strconv.Itoa(f.problem.ID)+":created", *rows[0].DeliveryKey)
	require.Equal(t, service.RelationCreatedEventType, rows[0].Type)

	// A genuine re-delivery of the same durable event must stay idempotent. The event
	// is returned to pending first, because claiming only selects pending rows.
	reopenOutboxEvent(t, f.client, f.ctx, event.ID)
	require.NoError(t, worker.DispatchOnce(f.ctx))
	require.Len(t, f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(assignee.ID)).AllX(f.ctx), 1,
		"a genuine re-delivery must not duplicate the notification")
}

// A payload that no longer matches the durable event is blocked, not delivered.
func TestWorkItemRelationEventsConsumerBlocksOnTamperedPayload(t *testing.T) {
	f := newRelationFixture(t)
	assignee := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("b2-tamper").SetName("b2 tamper").
		SetRole("agent").SetActive(true).SetEmail("b2-tamper@example.test").SetPasswordHash("test").SaveX(f.ctx)
	f.problem.Update().SetAssigneeID(assignee.ID).ExecX(f.ctx)

	_, err := f.owner.Apply(f.ctx, f.command("b2-tamper"), false)
	require.NoError(t, err)
	event := relationEventsOfType(f, service.RelationCreatedEventType)[0]

	var facts service.RelationFacts
	require.NoError(t, json.Unmarshal(event.Payload, &facts))
	facts.TargetID = facts.SourceID
	tampered, err := json.Marshal(facts)
	require.NoError(t, err)
	f.client.OutboxEvent.UpdateOneID(event.ID).SetPayload(tampered).ExecX(f.ctx)

	worker := newRelationDeliveryWorker(t, f)
	require.NoError(t, worker.DispatchOnce(f.ctx))

	require.Zero(t, f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(assignee.ID)).CountX(f.ctx))
	require.Equal(t, "blocked", f.client.OutboxEvent.GetX(f.ctx, event.ID).Status, "tampered payload must block visibly")
}

// An actor that is no longer active in the tenant blocks delivery instead of
// notifying on behalf of an unverifiable actor.
func TestWorkItemRelationEventsConsumerBlocksWhenActorUnavailable(t *testing.T) {
	f := newRelationFixture(t)
	assignee := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("b2-inactive").SetName("b2 inactive").
		SetRole("agent").SetActive(true).SetEmail("b2-inactive@example.test").SetPasswordHash("test").SaveX(f.ctx)
	f.problem.Update().SetAssigneeID(assignee.ID).ExecX(f.ctx)

	_, err := f.owner.Apply(f.ctx, f.command("b2-inactive"), false)
	require.NoError(t, err)
	event := relationEventsOfType(f, service.RelationCreatedEventType)[0]
	f.actor.Update().SetActive(false).ExecX(f.ctx)

	worker := newRelationDeliveryWorker(t, f)
	require.NoError(t, worker.DispatchOnce(f.ctx))

	require.Zero(t, f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(assignee.ID)).CountX(f.ctx))
	require.Equal(t, "blocked", f.client.OutboxEvent.GetX(f.ctx, event.ID).Status, "unavailable actor must block visibly")
}

// B2 round-2 fix: an MSP provider actor's native tenant differs from the event
// tenant. Resolving the actor inside the event tenant would permanently block every
// MSP event, even though B1's own tests prove the MSP mutation path is supported.
// The provider actor owns the incident it mutates and is the counterpart's requester,
// which is what the requester/assignee row policy requires; the counterpart's assignee
// is a customer-tenant handler, which is the actual delivery target.
func TestWorkItemRelationEventsDeliversForMspProviderActor(t *testing.T) {
	f := newRelationFixture(t)
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("events-provider").SetName("provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("events-msp").SetName("operator").
		SetEmail("events-msp@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP tech").SetIsActive(true).SaveX(f.ctx)
	for _, grant := range []struct{ resource, action string }{{"incident", "read"}, {"incident", "write"}, {"problem", "read"}} {
		permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(grant.resource + ":" + grant.action).
			SetName(fmt.Sprint(grant)).SetResource(grant.resource).SetAction(grant.action).SaveX(f.ctx)
		f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	}
	_, err := f.runtime.Tenant.User.Get(f.ctx, actor.ID)
	require.True(t, ent.IsNotFound(err), "the provider actor is genuinely outside the customer tenant")

	// The provider actor needs row visibility on both endpoints (requester or
	// assignee): it owns the incident it mutates, and it is the counterpart's
	// requester. The counterpart's assignee is a customer-tenant handler, which is
	// the actual delivery target — a provider user cannot receive a customer-tenant
	// notification, since the pipeline resolves recipients inside the event tenant.
	handler := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("msp-customer-handler").SetName("handler").
		SetEmail("msp-handler@example.test").SetPasswordHash("test").SetRole("admin").SetActive(true).SaveX(f.ctx)
	f.client.Ticket.UpdateOneID(f.inc.WorkItemID).SetAssigneeID(actor.ID).ExecX(f.ctx)
	f.client.Ticket.UpdateOneID(f.problem.ID).SetRequesterID(actor.ID).SetAssigneeID(handler.ID).ExecX(f.ctx)

	cmd := f.command("msp-delivery")
	cmd.Meta.ActorID = actor.ID
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)

	event := relationEventsOfType(f, service.RelationCreatedEventType)[0]
	var facts service.RelationFacts
	require.NoError(t, json.Unmarshal(event.Payload, &facts))
	require.Equal(t, provider.ID, facts.ActorTenantID, "the event must carry the actor's native tenant")

	worker := newRelationDeliveryWorker(t, f)
	require.NoError(t, worker.DispatchOnce(f.ctx))

	ev := f.client.OutboxEvent.GetX(f.ctx, event.ID)
	require.NotEqual(t, "blocked", ev.Status,
		"an MSP provider actor's event must not be terminally blocked: "+ev.LastError)
	require.Len(t, f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(handler.ID)).AllX(f.ctx), 1,
		"the customer-tenant counterpart assignee must be notified for an MSP provider actor")
}
