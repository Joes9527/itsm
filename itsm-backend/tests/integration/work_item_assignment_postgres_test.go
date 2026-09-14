//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/ticketnotification"
	"itsm-backend/ent/user"
	"itsm-backend/handlers/common/workitemassignment"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"testing"
	"time"
)

func TestPostgresWorkItemAssignmentSessionRejectsFabricationAndExpiry(t *testing.T) {
	client, cmd := assignmentFixture(t)
	ctx := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
	actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(ctx)
	identity := creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: actor.Role, Channel: "http"}
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	fake := &authorization.SessionSnapshot{Actor: actor, Tx: tx, Identity: identity}
	_, err = service.NewWorkItemAssignmentWriter(fake).Apply(ctx, tx.Client(), cmd)
	require.Error(t, err, "fabricated snapshot must not authorize assignment")
	require.NoError(t, tx.Rollback())
	reader := authorization.NewSessionReader(client, sameTransactionDirectory{})
	var expired *authorization.SessionSnapshot
	require.NoError(t, reader.Write(ctx, identity, func(session *authorization.SessionSnapshot) error {
		expired = session
		// Exported projection fields cannot grant a different identity.
		bad := cmd
		bad.ActorTenantID++
		_, err := service.NewWorkItemAssignmentWriter(session).Apply(ctx, session.Tx.Client(), bad)
		require.Error(t, err)
		original := *session.Actor
		changed := original
		changed.ID++
		session.Actor = &changed
		session.Identity.ActorID++
		require.Error(t, session.ValidateMutationActor(session.Tx.Client(), changed.ID, cmd.ActorTenantID, cmd.TenantID))
		require.Error(t, session.ValidateMutationActor(client, cmd.ActorID, cmd.ActorTenantID, cmd.TenantID))
		_, err = service.NewWorkItemAssignmentWriter(session).Apply(ctx, session.Tx.Client(), cmd)
		return err
	}))
	require.NoError(t, reader.Read(ctx, identity, func(session *authorization.SessionSnapshot) error {
		require.Error(t, session.ValidateMutationActor(session.Tx.Client(), cmd.ActorID, cmd.ActorTenantID, cmd.TenantID))
		return nil
	}))
	var failed *authorization.SessionSnapshot
	require.Error(t, reader.Write(ctx, identity, func(session *authorization.SessionSnapshot) error {
		failed = session
		return errors.New("abort callback")
	}))
	require.Error(t, failed.ValidateMutationActor(failed.Tx.Client(), cmd.ActorID, cmd.ActorTenantID, cmd.TenantID))
	var panicked *authorization.SessionSnapshot
	func() {
		defer func() { require.Equal(t, "abort panic", recover()) }()
		_ = reader.Write(ctx, identity, func(session *authorization.SessionSnapshot) error { panicked = session; panic("abort panic") })
	}()
	require.Error(t, panicked.ValidateMutationActor(panicked.Tx.Client(), cmd.ActorID, cmd.ActorTenantID, cmd.TenantID))
	tx, err = client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	expired.Tx = tx
	cmd.ExpectedVersion++
	_, err = service.NewWorkItemAssignmentWriter(expired).Apply(ctx, tx.Client(), cmd)
	require.Error(t, err, "expired snapshot must not authorize a new transaction")
}

func assignmentTestActor(ctx context.Context, client *ent.Client, cmd workitemassignment.Command) error {
	actor, err := client.User.Query().Where(user.ID(cmd.ActorID), user.TenantID(cmd.ActorTenantID), user.Active(true)).Only(ctx)
	if err != nil {
		return err
	}
	if actor.TenantID != cmd.TenantID {
		return errors.New("actor scope mismatch")
	}
	return nil
}

func TestPostgresWorkItemAssignmentWorkerRetryRestartAndUnknownTransport(t *testing.T) {
	client, cmd := assignmentFixture(t)
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	_, err = workitemassignment.NewWriter(service.EnqueueWorkItemAssignment, assignmentTestActor).Apply(ctx, tx.Client(), cmd)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	persisted := client.OutboxEvent.Query().OnlyX(ctx)
	// Durable enqueue survives a restart before dispatch. No transport is called
	// by the assignment handler, even when its materialization transaction fails.
	fail := true
	client.TicketNotification.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, m)
			if err != nil {
				return nil, err
			}
			if fail {
				return nil, errors.New("queue insert fault")
			}
			return value, nil
		})
	})
	graph := &notificationRuntimeGraph{t: t, tenantID: cmd.TenantID, err: errors.New("provider accepted but disconnected")}
	email := service.NewEmailService(service.EmailConfig{}, zap.NewNop().Sugar())
	email.SetGraphProvider(func(int) (service.GraphMailSender, string, bool) { return graph, "support@example.test", true })
	notifications := service.NewTicketNotificationService(client, zap.NewNop().Sugar())
	notifications.SetEmailService(email)
	notifications.SetDeliveryQueueClient(client)
	newWorker := func() *service.OutboxDeliveryWorker {
		registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{service.NewWorkItemAssignmentNotificationHandler(client, notifications)})
		require.NoError(t, err)
		worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(client), service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: time.Minute, MaxAttempts: 3}, zap.NewNop().Sugar(), registry)
		require.NoError(t, err)
		return worker
	}
	require.NoError(t, newWorker().DispatchOnce(ctx))
	require.Equal(t, "pending", client.OutboxEvent.GetX(ctx, persisted.ID).Status)
	require.Zero(t, client.TicketNotification.Query().CountX(ctx))
	require.Zero(t, client.Notification.Query().CountX(ctx))
	require.Zero(t, graph.calls)
	fail = false
	client.OutboxEvent.UpdateOneID(persisted.ID).SetNextAttemptAt(time.Now().Add(-time.Second)).ExecX(ctx)
	require.NoError(t, newWorker().DispatchOnce(ctx))
	require.Equal(t, "published", client.OutboxEvent.GetX(ctx, persisted.ID).Status)
	require.Equal(t, 2, client.TicketNotification.Query().CountX(ctx))
	require.Zero(t, graph.calls)
	// Lost acknowledgement after durable materialization is safe to replay.
	require.NoError(t, service.NewWorkItemAssignmentNotificationHandler(client, notifications).Deliver(ctx, persisted))
	require.Equal(t, 2, client.TicketNotification.Query().CountX(ctx))
	_, err = notifications.ProcessPendingDeliveries(ctx, "assignment-test", 10)
	require.Error(t, err)
	row := client.TicketNotification.Query().Where(ticketnotification.Channel("email")).OnlyX(ctx)
	require.Equal(t, "failed", row.Status)
	require.Equal(t, "delivery_unknown", row.LastErrorClass)
	require.Equal(t, 1, graph.calls)
	_, _ = notifications.ProcessPendingDeliveries(ctx, "assignment-restarted", 10)
	require.Equal(t, 1, graph.calls, "ambiguous external delivery must not be replayed")
}

func TestPostgresWorkItemAssignmentSuppressedDeliverySurvivesPreferenceChangeAndRestart(t *testing.T) {
	client, cmd := assignmentFixture(t)
	ctx := context.Background()
	pref := client.NotificationPreference.Create().SetTenantID(cmd.TenantID).SetUserID(cmd.AssigneeID).SetEventType("ticket_assigned").SetInAppEnabled(false).SetEmailEnabled(false).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = workitemassignment.NewWriter(service.EnqueueWorkItemAssignment, assignmentTestActor).Apply(ctx, tx.Client(), cmd)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	repository := service.NewOutboxEventRepository(client)
	claimed, err := repository.ClaimDue(ctx, time.Now().Add(time.Second), 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	notifications := service.NewTicketNotificationService(client, zap.NewNop().Sugar())
	require.NoError(t, service.NewWorkItemAssignmentNotificationHandler(client, notifications).Deliver(ctx, claimed[0]))
	require.Zero(t, client.TicketNotification.Query().CountX(ctx))
	completedAt := client.OutboxEvent.GetX(ctx, claimed[0].ID).PublishedAt
	require.False(t, completedAt.IsZero())
	// Successful delivery was not acknowledged. Preference changes cannot alter
	// that durable historical outcome when a new worker recovers the claim.
	pref.Update().SetInAppEnabled(true).SaveX(ctx)
	client.OutboxEvent.UpdateOneID(claimed[0].ID).SetClaimExpiresAt(time.Now().Add(-time.Second)).ExecX(ctx)
	registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{service.NewWorkItemAssignmentNotificationHandler(client, notifications)})
	require.NoError(t, err)
	worker, err := service.NewOutboxDeliveryWorker(repository, service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: time.Minute, MaxAttempts: 3}, zap.NewNop().Sugar(), registry)
	require.NoError(t, err)
	require.NoError(t, worker.DispatchOnce(ctx))
	require.Zero(t, client.TicketNotification.Query().CountX(ctx), "suppressed historical outcome must remain suppressed")
	require.Zero(t, client.Notification.Query().CountX(ctx))
	require.Equal(t, "published", client.OutboxEvent.GetX(ctx, claimed[0].ID).Status)
	require.Equal(t, completedAt, client.OutboxEvent.GetX(ctx, claimed[0].ID).PublishedAt)
}

func TestPostgresWorkItemAssignmentDeliveryReceiptFailureRollsBackMaterialization(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			client, cmd := assignmentFixture(t)
			ctx := context.Background()
			pref := client.NotificationPreference.Create().SetTenantID(cmd.TenantID).SetUserID(cmd.AssigneeID).SetEventType("ticket_assigned").SetInAppEnabled(enabled).SetEmailEnabled(false).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = workitemassignment.NewWriter(service.EnqueueWorkItemAssignment, assignmentTestActor).Apply(ctx, tx.Client(), cmd)
			require.NoError(t, err)
			require.NoError(t, tx.Commit())
			event := client.OutboxEvent.Query().OnlyX(ctx)
			fail := true
			client.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					v, err := next.Mutate(ctx, m)
					if err != nil {
						return nil, err
					}
					if fail {
						return nil, errors.New("delivery receipt fault")
					}
					return v, nil
				})
			})
			notifications := service.NewTicketNotificationService(client, zap.NewNop().Sugar())
			err = service.NewWorkItemAssignmentNotificationHandler(client, notifications).Deliver(ctx, event)
			require.ErrorContains(t, err, "delivery receipt fault")
			require.True(t, client.OutboxEvent.GetX(ctx, event.ID).PublishedAt.IsZero())
			require.Zero(t, client.Notification.Query().CountX(ctx))
			require.Zero(t, client.TicketNotification.Query().CountX(ctx))
			fail = false
			pref.Update().SetInAppEnabled(true).SaveX(ctx)
			require.NoError(t, service.NewWorkItemAssignmentNotificationHandler(client, notifications).Deliver(ctx, event))
			require.Equal(t, 1, client.TicketNotification.Query().CountX(ctx))
			require.Equal(t, 1, client.Notification.Query().CountX(ctx))
		})
	}
}

func assignmentFixture(t *testing.T) (*ent.Client, workitemassignment.Command) {
	db := openBPMNAssignmentSourceMigrationDB(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))
	tenant := client.Tenant.Create().SetCode("assignment").SetName("Assignment").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("actor").SetName("Actor").SetEmail("actor@example.test").SetPasswordHash("unused").SetActive(true).SaveX(ctx)
	owner := client.User.Create().SetTenantID(tenant.ID).SetUsername("owner").SetName("Owner").SetEmail("owner@example.test").SetPasswordHash("unused").SetActive(true).SaveX(ctx)
	item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetTitle("Assignment").SetTicketNumber("T-ASGN").SetStatus("open").SetPriority("medium").SaveX(ctx)
	return client, workitemassignment.Command{WorkItemID: item.ID, TenantID: tenant.ID, ActorTenantID: tenant.ID, ActorID: actor.ID, AssigneeID: owner.ID, ExpectedVersion: item.Version, Source: "manual", Reason: "handover"}
}

func TestPostgresWorkItemAssignmentCapturesOnlyLiveBoundTasksInStableOrder(t *testing.T) {
	client, cmd := assignmentFixture(t)
	ctx := context.Background()
	deployment := client.ProcessDeployment.Create().SetTenantID(cmd.TenantID).SetDeploymentID("deployment").SetDeploymentName("deployment").SaveX(ctx)
	definition := client.ProcessDefinition.Create().SetTenantID(cmd.TenantID).SetKey("assignment").SetName("assignment").SetDeploymentID(deployment.ID).SetBpmnXML([]byte("test")).SaveX(ctx)
	instance := client.ProcessInstance.Create().SetTenantID(cmd.TenantID).SetProcessInstanceID("instance").SetProcessDefinitionID(definition.ID).SetProcessDefinitionKey(definition.Key).SetBusinessID(cmd.WorkItemID).SetBusinessType("ticket").SaveX(ctx)
	expected := []int{}
	for i, status := range []string{"created", "completed", "started", "cancelled", "assigned"} {
		task := client.ProcessTask.Create().SetTenantID(cmd.TenantID).SetTaskID(fmt.Sprint(i)).SetProcessInstanceID(instance.ID).SetProcessDefinitionKey(definition.Key).SetTaskDefinitionKey(fmt.Sprint(i)).SetTaskName("fulfill").SetStatus(status).SetAssigneeSource("work_item_assignee").SaveX(ctx)
		if status != "completed" && status != "cancelled" {
			expected = append(expected, task.ID)
		}
	}
	client.ProcessTask.Create().SetTenantID(cmd.TenantID).SetTaskID("explicit").SetProcessInstanceID(instance.ID).SetProcessDefinitionKey(definition.Key).SetTaskDefinitionKey("explicit").SetTaskName("explicit").SetAssignee("123").SaveX(ctx)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = workitemassignment.NewWriter(service.EnqueueWorkItemAssignment, assignmentTestActor).Apply(ctx, tx.Client(), cmd)
	require.NoError(t, err)
	audit := tx.TicketWorkflowRecord.Query().OnlyX(ctx)
	data, err := json.Marshal(audit.Metadata["affectedTaskIds"])
	require.NoError(t, err)
	var got []int
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, expected, got)
	for _, id := range expected {
		require.Empty(t, tx.ProcessTask.GetX(ctx, id).Assignee)
	}
}

func TestPostgresWorkItemAssignmentAtomicAndIdempotent(t *testing.T) {
	client, cmd := assignmentFixture(t)
	ctx := context.Background()
	writer := workitemassignment.NewWriter(service.EnqueueWorkItemAssignment, assignmentTestActor)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	item, err := writer.Apply(ctx, tx.Client(), cmd)
	require.NoError(t, err)
	require.Equal(t, cmd.AssigneeID, item.AssigneeID)
	require.Equal(t, cmd.ExpectedVersion+1, item.Version)
	audit := tx.TicketWorkflowRecord.Query().OnlyX(ctx)
	require.Equal(t, cmd.ActorID, audit.OperatorID)
	require.Equal(t, cmd.AssigneeID, audit.ToUserID)
	require.Equal(t, "manual", audit.Metadata["source"])
	event := tx.OutboxEvent.Query().OnlyX(ctx)
	require.Equal(t, "pending", event.Status)
	require.Equal(t, "work_item.assigned", event.EventType)
	cmd.ExpectedVersion = item.Version
	_, err = writer.Apply(ctx, tx.Client(), cmd)
	require.NoError(t, err)
	require.Equal(t, 1, tx.TicketWorkflowRecord.Query().CountX(ctx))
	require.Equal(t, 1, tx.OutboxEvent.Query().CountX(ctx))
	require.NoError(t, tx.Commit())
	// Restarted delivery instances reuse durable event identity, including external queue rows.
	notifications := service.NewTicketNotificationService(client, zap.NewNop().Sugar())
	notifications.SetEmailService(service.NewEmailService(service.EmailConfig{}, zap.NewNop().Sugar()))
	for i := 0; i < 2; i++ {
		require.NoError(t, service.NewWorkItemAssignmentNotificationHandler(client, notifications).Deliver(ctx, event))
	}
	require.Equal(t, 1, client.TicketNotification.Query().Where(ticketnotification.Channel("in_app")).CountX(ctx))
	require.Equal(t, 1, client.TicketNotification.Query().Where(ticketnotification.Channel("email")).CountX(ctx))
	// Clear is a real owner change, never a user with ID zero.
	tx, err = client.Tx(ctx)
	require.NoError(t, err)
	cmd.AssigneeID = 0
	item, err = writer.Apply(ctx, tx.Client(), cmd)
	require.NoError(t, err)
	require.Zero(t, item.AssigneeID)
	require.NoError(t, tx.Commit())
	require.Equal(t, 2, client.OutboxEvent.Query().Where(outboxevent.EventType("work_item.assigned")).CountX(ctx))
}

func TestPostgresWorkItemAssignmentFailureRollsBackOwnerAuditOutbox(t *testing.T) {
	for _, stage := range []string{"audit", "outbox"} {
		t.Run(stage, func(t *testing.T) {
			client, cmd := assignmentFixture(t)
			ctx := context.Background()
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					_, err := next.Mutate(ctx, m)
					if err != nil {
						return nil, err
					}
					return nil, errors.New("injected failure")
				})
			}
			if stage == "audit" {
				client.TicketWorkflowRecord.Use(hook)
			} else {
				client.OutboxEvent.Use(hook)
			}
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			_, err = workitemassignment.NewWriter(service.EnqueueWorkItemAssignment, assignmentTestActor).Apply(ctx, tx.Client(), cmd)
			require.ErrorContains(t, err, "injected failure")
			require.NoError(t, tx.Rollback())
			item := client.Ticket.GetX(ctx, cmd.WorkItemID)
			require.Zero(t, item.AssigneeID)
			require.Equal(t, cmd.ExpectedVersion, item.Version)
			require.Zero(t, client.TicketWorkflowRecord.Query().CountX(ctx))
			require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
		})
	}
}

func TestPostgresWorkItemAssignmentRejectsInvalidIdentitiesAndStaleVersion(t *testing.T) {
	for _, kind := range []string{"inactive", "cross_tenant", "actor", "stale", "non_transaction"} {
		t.Run(kind, func(t *testing.T) {
			client, cmd := assignmentFixture(t)
			ctx := context.Background()
			switch kind {
			case "inactive":
				client.User.UpdateOneID(cmd.AssigneeID).SetActive(false).ExecX(ctx)
			case "cross_tenant":
				other := client.Tenant.Create().SetName("Other").SetCode("other").SaveX(ctx)
				client.User.UpdateOneID(cmd.AssigneeID).SetTenantID(other.ID).ExecX(ctx)
			case "actor":
				cmd.ActorTenantID++
			case "stale":
				cmd.ExpectedVersion++
			}
			writer := workitemassignment.NewWriter(service.EnqueueWorkItemAssignment, assignmentTestActor)
			if kind == "non_transaction" {
				_, err := writer.Apply(ctx, client, cmd)
				require.Error(t, err)
				return
			}
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = writer.Apply(ctx, tx.Client(), cmd)
			require.Error(t, err)
			require.Zero(t, tx.OutboxEvent.Query().CountX(ctx))
			require.Zero(t, tx.TicketWorkflowRecord.Query().CountX(ctx))
		})
	}
}
