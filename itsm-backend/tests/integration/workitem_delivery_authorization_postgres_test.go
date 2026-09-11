//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/ent/notification"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/permission"
	entrole "itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	"testing"
	"time"
)

func newRuntimeRelationDeliveryWorker(t *testing.T, f *relationFixture) *service.OutboxDeliveryWorker {
	t.Helper()
	_, grantErr := f.db.ExecContext(f.ctx, "GRANT SELECT ON notification_preferences TO "+f.runtimeRole)
	require.NoError(t, grantErr)
	logger := zap.NewNop().Sugar()
	notifier := service.NewTicketNotificationService(f.runtime.Tenant, logger)
	notifier.SetNotificationPreferenceService(service.NewNotificationPreferenceService(f.runtime.Tenant, logger))
	registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{
		service.NewWorkItemRelationCreatedDeliveryHandler(f.runtime.Tenant, f.runtime.IntakeDirectorySnapshot(), notifier, logger),
		service.NewWorkItemRelationRemovedDeliveryHandler(f.runtime.Tenant, f.runtime.IntakeDirectorySnapshot(), notifier, logger),
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

func TestWorkItemDeliveryRuntimeMSP(t *testing.T) {
	for _, mode := range []string{"allow", "allocation_revoked", "actor_read_revoked", "actor_role_revoked", "recipient_read_revoked", "tenant_inactive", "directory_timeout", "endpoint_timeout", "receipt_failure"} {
		t.Run(mode, func(t *testing.T) {
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
			grantWorkItemDeliveryPermission(t, f.client, f.ctx, f.tenant.ID, "admin", "problem")
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

			faulty := true
			failure := ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
					if faulty {
						return nil, context.DeadlineExceeded
					}
					return next.Query(ctx, q)
				})
			})
			switch mode {
			case "receipt_failure":
				f.client.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if mutation, ok := m.(*ent.OutboxEventMutation); ok {
							if status, exists := mutation.Status(); exists && status == "published" && faulty {
								return nil, fmt.Errorf("injected delivery receipt failure")
							}
						}
						return next.Mutate(ctx, m)
					})
				})
			case "allocation_revoked":
				f.client.MSPAllocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
			case "actor_role_revoked":
				role.Update().SetIsActive(false).ExecX(f.ctx)
			case "actor_read_revoked":
				p := f.client.Permission.Query().Where(permission.TenantID(f.tenant.ID), permission.Code("problem:read")).OnlyX(f.ctx)
				f.client.RolePermission.Delete().Where(rolepermission.RoleID(role.ID), rolepermission.PermissionID(p.ID)).ExecX(f.ctx)
			case "recipient_read_revoked":
				receiverRole := f.client.Role.Query().Where(entrole.Code("admin"), entrole.TenantID(f.tenant.ID)).OnlyX(f.ctx)
				p := f.client.Permission.Query().Where(permission.TenantID(f.tenant.ID), permission.Code("problem:read")).OnlyX(f.ctx)
				f.client.RolePermission.Delete().Where(rolepermission.RoleID(receiverRole.ID), rolepermission.PermissionID(p.ID)).ExecX(f.ctx)
			case "tenant_inactive":
				f.client.Tenant.UpdateOneID(f.tenant.ID).SetStatus("suspended").ExecX(f.ctx)
			case "directory_timeout":
				f.runtime.System.User.Intercept(failure)
			case "endpoint_timeout":
				f.runtime.Tenant.Ticket.Intercept(failure)
			}

			worker := newRuntimeRelationDeliveryWorker(t, f)
			dispatchErr := worker.DispatchOnce(f.ctx)
			if mode == "receipt_failure" {
				require.ErrorContains(t, dispatchErr, "injected delivery receipt failure")
			} else {
				require.NoError(t, dispatchErr)
			}

			ev := f.client.OutboxEvent.GetX(f.ctx, event.ID)

			if mode == "receipt_failure" {
				require.Equal(t, "publishing", ev.Status)
				require.Equal(t, 1, f.client.Notification.Query().Where(notification.UserID(handler.ID)).CountX(f.ctx))
				faulty = false
				reopenOutboxEvent(t, f.client, f.ctx, event.ID)
				require.NoError(t, worker.DispatchOnce(f.ctx))
				ev = f.client.OutboxEvent.GetX(f.ctx, event.ID)
			}
			if mode == "directory_timeout" || mode == "endpoint_timeout" {
				require.Equal(t, "pending", ev.Status, ev.LastError)
				require.Equal(t, 1, ev.AttemptCount)
				require.Zero(t, f.client.Notification.Query().Where(notification.UserID(handler.ID)).CountX(f.ctx))
				faulty = false
				reopenOutboxEvent(t, f.client, f.ctx, event.ID)
				require.NoError(t, worker.DispatchOnce(f.ctx))
				ev = f.client.OutboxEvent.GetX(f.ctx, event.ID)
			} else if mode != "allow" && mode != "receipt_failure" {
				require.Equal(t, "blocked", ev.Status, ev.LastError)
				require.Zero(t, f.client.Notification.Query().Where(notification.UserID(handler.ID)).CountX(f.ctx))
				return
			}
			require.Equal(t, "published", ev.Status, ev.LastError)
			rows := f.client.Notification.Query().Where(notification.TenantID(f.tenant.ID), notification.UserID(handler.ID)).AllX(f.ctx)
			require.Len(t, rows, 1)
			require.Contains(t, rows[0].Message, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).TicketNumber)
			reopenOutboxEvent(t, f.client, f.ctx, event.ID)
			require.NoError(t, worker.DispatchOnce(f.ctx))
			require.Equal(t, 1, f.client.Notification.Query().Where(notification.UserID(handler.ID)).CountX(f.ctx))
		})
	}
}

func grantWorkItemDeliveryPermission(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, code, resource string) {
	t.Helper()
	r, err := client.Role.Query().Where(entrole.TenantID(tenantID), entrole.Code(code)).Only(ctx)
	if ent.IsNotFound(err) {
		r = client.Role.Create().SetTenantID(tenantID).SetCode(code).SetName(code).SetIsActive(true).SaveX(ctx)
	} else {
		require.NoError(t, err)
	}
	p, err := client.Permission.Query().Where(permission.TenantID(tenantID), permission.Code(resource+":read")).Only(ctx)
	if ent.IsNotFound(err) {
		p = client.Permission.Create().SetTenantID(tenantID).SetCode(resource + ":read").SetName(resource + " read").SetResource(resource).SetAction("read").SaveX(ctx)
	} else {
		require.NoError(t, err)
	}
	exists := client.RolePermission.Query().Where(rolepermission.TenantID(tenantID), rolepermission.RoleID(r.ID), rolepermission.PermissionID(p.ID)).ExistX(ctx)
	if !exists {
		client.RolePermission.Create().SetTenantID(tenantID).SetRoleID(r.ID).SetPermissionID(p.ID).SaveX(ctx)
	}
}

// Build immutable producer facts with fixture privileges, but run every delivery
// and notification read/write through the actual customer RLS client.
func TestWorkItemDeliveryRuntimeOutcomes(t *testing.T) {
	for _, kind := range []string{"change", "problem"} {
		for _, mode := range []string{"allow", "allocation_revoked", "endpoint_timeout"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				f := newRelationFixture(t)
				f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
				provider := f.client.Tenant.Create().SetCode("outcome-provider").SetName("Provider").SetType("msp_provider").SetStatus("active").SaveX(f.ctx)
				actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("outcome-actor").SetName("Provider").SetEmail("provider@outcome.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
				allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
				recipient := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("outcome-recipient").SetName("Recipient").SetEmail("recipient@outcome.test").SetPasswordHash("test").SetRole("agent").SetActive(true).SaveX(f.ctx)
				for _, resource := range []string{"incident", "problem", "change"} {
					grantWorkItemDeliveryPermission(t, f.client, f.ctx, f.tenant.ID, "msp_tech", resource)
					grantWorkItemDeliveryPermission(t, f.client, f.ctx, f.tenant.ID, "agent", resource)
				}
				_, err := f.runtime.Tenant.User.Get(f.ctx, actor.ID)
				require.True(t, ent.IsNotFound(err))
				f.client.Ticket.UpdateOneID(f.inc.WorkItemID).SetRequesterID(actor.ID).SetAssigneeID(recipient.ID).ExecX(f.ctx)
				f.client.Ticket.UpdateOneID(f.problem.ID).SetRequesterID(actor.ID).SetAssigneeID(recipient.ID).ExecX(f.ctx)
				logger := zap.NewNop().Sugar()
				_, err = f.db.ExecContext(f.ctx, "GRANT SELECT ON notification_preferences TO "+f.runtimeRole)
				require.NoError(t, err)
				sender := service.NewTicketNotificationService(f.runtime.Tenant, logger)
				sender.SetNotificationPreferenceService(service.NewNotificationPreferenceService(f.runtime.Tenant, logger))
				var handler service.OutboxDeliveryHandler
				var mutationID, targetID int
				var eventType string
				tx, err := f.client.Tx(f.ctx)
				require.NoError(t, err)
				meta := workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: actor.ID, ExpectedVersion: 1, OperationID: "outcome-delivery", Source: "http"}
				if kind == "change" {
					item := tx.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetTitle("Change outcome").SetTicketNumber("CHG-DELIVERY").SetRecordClass("change_request").SetStatus("in_progress").SetPriority("high").SetVersion(2).SaveX(f.ctx)
					record := tx.Change.Create().SetWorkItemID(item.ID).SetOutcome("successful").SaveX(f.ctx)
					tx.WorkItemRelation.Create().SetTenantID(f.tenant.ID).SetSourceWorkItemID(f.problem.ID).SetTargetWorkItemID(item.ID).SetRelationType("resolved_by_change").SetCreatedByID(actor.ID).SaveX(f.ctx)
					require.NoError(t, workitemmutation.RecordTx(f.ctx, tx, meta, workitemmutation.Result{WorkItemID: item.ID, Version: 2, Status: item.Status}, "change.record_outcome", "fixture-outcome", map[string]any{"changeId": record.ID, "outcome": "successful"}))
					require.NoError(t, service.EmitChangeOutcomeEventTx(f.ctx, tx, service.ChangeOutcomeFacts{TenantID: f.tenant.ID, ActorID: actor.ID, ActorTenantID: provider.ID, ChangeID: record.ID, WorkItemID: item.ID, Version: 2, Outcome: "successful", Source: meta.Source, OperationID: meta.OperationID}))
					handler = service.NewChangeOutcomeDeliveryHandler(f.runtime.Tenant, f.runtime.IntakeDirectorySnapshot(), sender, logger)
					mutationID, targetID, eventType = item.ID, f.problem.ID, service.ChangeOutcomeEventType
				} else {
					record := tx.Problem.Query().OnlyX(f.ctx)
					tx.Ticket.UpdateOneID(f.problem.ID).SetStatus("resolved").SetVersion(2).ExecX(f.ctx)
					tx.WorkItemRelation.Create().SetTenantID(f.tenant.ID).SetSourceWorkItemID(f.inc.WorkItemID).SetTargetWorkItemID(f.problem.ID).SetRelationType("investigated_by").SetCreatedByID(actor.ID).SaveX(f.ctx)
					require.NoError(t, workitemmutation.RecordTx(f.ctx, tx, meta, workitemmutation.Result{WorkItemID: f.problem.ID, Version: 2, Status: "resolved"}, "problem.resolve", "fixture-outcome", map[string]any{"problemId": record.ID}))
					require.NoError(t, service.EmitProblemResolvedEventTx(f.ctx, tx, service.ProblemResolvedFacts{TenantID: f.tenant.ID, ActorID: actor.ID, ActorTenantID: provider.ID, ProblemID: record.ID, WorkItemID: f.problem.ID, Version: 2, Source: meta.Source, OperationID: meta.OperationID}))
					handler = service.NewProblemResolvedDeliveryHandler(f.runtime.Tenant, f.runtime.IntakeDirectorySnapshot(), sender, logger)
					mutationID, targetID, eventType = f.problem.ID, f.inc.WorkItemID, service.ProblemResolvedEventType
				}
				require.NoError(t, tx.Commit())
				event := f.client.OutboxEvent.Query().Where(outboxevent.EventType(eventType)).OnlyX(f.ctx)
				targetBefore := f.client.Ticket.GetX(f.ctx, targetID)
				faulty := true
				if mode == "allocation_revoked" {
					allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
				}
				if mode == "endpoint_timeout" {
					f.runtime.Tenant.Ticket.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
						return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
							if faulty {
								return nil, context.DeadlineExceeded
							}
							return next.Query(ctx, q)
						})
					}))
				}
				registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{handler})
				require.NoError(t, err)
				worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(f.client), service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 20 * time.Second, MaxAttempts: 3}, logger, registry)
				require.NoError(t, err)
				require.NoError(t, worker.DispatchOnce(f.ctx))
				stored := f.client.OutboxEvent.GetX(f.ctx, event.ID)
				if mode == "allocation_revoked" {
					require.Equal(t, "blocked", stored.Status, stored.LastError)
					require.Zero(t, f.client.Notification.Query().Where(notification.Type(eventType)).CountX(f.ctx))
					return
				}
				if mode == "endpoint_timeout" {
					require.Equal(t, "pending", stored.Status, stored.LastError)
					faulty = false
					reopenOutboxEvent(t, f.client, f.ctx, event.ID)
					require.NoError(t, worker.DispatchOnce(f.ctx))
					stored = f.client.OutboxEvent.GetX(f.ctx, event.ID)
				}
				require.Equal(t, "published", stored.Status, stored.LastError)
				row := f.client.Notification.Query().Where(notification.Type(eventType)).OnlyX(f.ctx)
				require.Equal(t, recipient.ID, row.UserID)
				require.Contains(t, row.Message, f.client.Ticket.GetX(f.ctx, mutationID).TicketNumber)
				after := f.client.Ticket.GetX(f.ctx, targetID)
				require.Equal(t, targetBefore.Version, after.Version)
				require.Equal(t, targetBefore.Status, after.Status)
				reopenOutboxEvent(t, f.client, f.ctx, event.ID)
				require.NoError(t, worker.DispatchOnce(f.ctx))
				require.Equal(t, 1, f.client.Notification.Query().Where(notification.Type(eventType)).CountX(f.ctx))
			})
		}
	}
}
