package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/connector"
	"itsm-backend/dto"
	"itsm-backend/ent"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type durableNotificationConnector struct {
	mu       sync.Mutex
	sendErr  error
	messages []*connector.Message
	entered  chan struct{}
	release  <-chan struct{}
}

func (c *durableNotificationConnector) Manifest() connector.Manifest {
	return connector.Manifest{
		Name:                "email",
		Version:             "1.0.0",
		Title:               "Durable notification test connector",
		Type:                connector.TypeEmail,
		Capabilities:        []connector.Capability{connector.CapSendMessage},
		RequiredPermissions: []string{"connector:write"},
	}
}

func (c *durableNotificationConnector) Init(context.Context, connector.Config) error { return nil }

func (c *durableNotificationConnector) Send(ctx context.Context, message *connector.Message) error {
	messageCopy := *message
	messageCopy.Metadata = make(map[string]interface{}, len(message.Metadata))
	for key, value := range message.Metadata {
		messageCopy.Metadata[key] = value
	}
	c.mu.Lock()
	c.messages = append(c.messages, &messageCopy)
	sendErr := c.sendErr
	entered := c.entered
	release := c.release
	c.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return sendErr
}

func (c *durableNotificationConnector) HealthCheck(context.Context) connector.HealthStatus {
	return connector.HealthStatus{OK: true}
}

func (c *durableNotificationConnector) Close() error { return nil }

func (c *durableNotificationConnector) sentMessages() []*connector.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*connector.Message(nil), c.messages...)
}

type durableNotificationFixture struct {
	workflow      *TicketWorkflowService
	notifications *TicketNotificationService
	client        *ent.Client
	ctx           context.Context
	tenant        *ent.Tenant
	operator      *ent.User
	recipient     *ent.User
	ticket        *ent.Ticket
}

func newDurableNotificationFixture(t *testing.T, suffix string) *durableNotificationFixture {
	t.Helper()
	workflow, client, ctx := setupTicketWorkflowTest(t)
	t.Cleanup(func() { _ = client.Close() })
	tenant, err := createTicketWorkflowTestTenant(ctx, client, suffix)
	require.NoError(t, err)
	operator, err := createTicketWorkflowTestUser(ctx, client, tenant.ID, suffix+"-operator")
	require.NoError(t, err)
	recipient, err := createTicketWorkflowTestUser(ctx, client, tenant.ID, suffix+"-recipient")
	require.NoError(t, err)
	tk, err := createTicketWorkflowTestTicket(ctx, client, tenant.ID, operator.ID, "open")
	require.NoError(t, err)
	return &durableNotificationFixture{
		workflow:      workflow,
		notifications: NewTicketNotificationService(client, zaptest.NewLogger(t).Sugar()),
		client:        client,
		ctx:           ctx,
		tenant:        tenant,
		operator:      operator,
		recipient:     recipient,
		ticket:        tk,
	}
}

func (f *durableNotificationFixture) enqueueExternalCC(t *testing.T) *ent.TicketNotification {
	t.Helper()
	require.NoError(t, f.workflow.CCTicket(f.ctx, &dto.CCTicketRequest{
		TicketID:       f.ticket.ID,
		CCUsers:        []int{f.recipient.ID},
		NotifyChannels: []string{"email"},
	}, f.operator.ID, f.tenant.ID))
	return f.client.TicketNotification.Query().OnlyX(f.ctx)
}

func configureDurableNotificationConnector(t *testing.T, service *TicketNotificationService, tenantID int, fake *durableNotificationConnector) {
	t.Helper()
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return fake })
	manager := connector.NewManager(registry, zaptest.NewLogger(t).Sugar())
	require.NoError(t, manager.Provision(context.Background(), connector.Config{
		TenantID: tenantID,
		Name:     "email",
		Type:     connector.TypeEmail,
		Provider: "durable-notification-test",
		Enabled:  true,
	}))
	t.Cleanup(manager.CloseAll)
	service.SetConnectorManager(manager)
}

func TestTicketNotificationWorkerKeepsUnavailableConnectorRetryable(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-unavailable")
	row := fixture.enqueueExternalCC(t)
	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }

	completed, err := fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-worker-unavailable", 10)
	require.Error(t, err)
	require.Zero(t, completed)

	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, "pending", row.Status)
	require.Equal(t, 1, row.AttemptCount)
	require.Equal(t, "connector_unavailable", row.LastErrorClass)
	require.True(t, row.NextAttemptAt.After(now))
	require.Empty(t, row.LeaseOwner)
	require.True(t, row.LeaseExpiresAt.IsZero())

	fake := &durableNotificationConnector{}
	configureDurableNotificationConnector(t, fixture.notifications, fixture.tenant.ID, fake)
	now = row.NextAttemptAt
	completed, err = fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-worker-recovered", 10)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, "sent", row.Status)
	require.Equal(t, 2, row.AttemptCount)
	require.Len(t, fake.sentMessages(), 1)
}

func TestTicketNotificationWorkerRetriesSendFailureWithStableDeliveryKey(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-send-failure")
	row := fixture.enqueueExternalCC(t)
	fake := &durableNotificationConnector{sendErr: errors.New("external provider unavailable")}
	configureDurableNotificationConnector(t, fixture.notifications, fixture.tenant.ID, fake)
	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }

	completed, err := fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-worker-send-failure", 10)
	require.Error(t, err)
	require.Zero(t, completed)

	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, "pending", row.Status)
	require.Equal(t, 1, row.AttemptCount)
	require.Equal(t, "connector_send", row.LastErrorClass)
	messages := fake.sentMessages()
	require.Len(t, messages, 1)
	require.Equal(t, *row.DeliveryKey, messages[0].ID)
	require.Equal(t, *row.DeliveryKey, messages[0].Metadata["delivery_key"])
}

func TestTicketNotificationWorkerRecoversExpiredLeaseAfterSentStatusWriteFailure(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-status-recovery")
	row := fixture.enqueueExternalCC(t)
	fake := &durableNotificationConnector{}
	configureDurableNotificationConnector(t, fixture.notifications, fixture.tenant.ID, fake)
	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }
	failSentStatus := atomic.Bool{}
	failSentStatus.Store(true)
	fixture.client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if notificationMutation, ok := mutation.(*ent.TicketNotificationMutation); ok {
				status, statusSet := notificationMutation.Status()
				if statusSet && status == "sent" && failSentStatus.CompareAndSwap(true, false) {
					return nil, errors.New("injected sent status persistence failure")
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})

	completed, err := fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-worker-status-one", 10)
	require.Error(t, err)
	require.Zero(t, completed)
	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, "processing", row.Status)
	require.Equal(t, 1, row.AttemptCount)
	require.False(t, row.LeaseExpiresAt.IsZero())

	now = row.LeaseExpiresAt.Add(time.Second)
	completed, err = fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-worker-status-two", 10)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, "sent", row.Status)
	require.Equal(t, 2, row.AttemptCount)
	require.False(t, row.SentAt.IsZero())
	require.Empty(t, row.LeaseOwner)
	require.Len(t, fake.sentMessages(), 2)
	require.Equal(t, fake.sentMessages()[0].Metadata["delivery_key"], fake.sentMessages()[1].Metadata["delivery_key"])
}

func TestTicketNotificationWorkerCASPreventsCompetingLiveLeaseDispatch(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-competing-claims")
	fixture.enqueueExternalCC(t)
	release := make(chan struct{})
	fake := &durableNotificationConnector{entered: make(chan struct{}, 1), release: release}
	configureDurableNotificationConnector(t, fixture.notifications, fixture.tenant.ID, fake)
	otherWorker := NewTicketNotificationService(fixture.client, zaptest.NewLogger(t).Sugar())
	configureDurableNotificationConnector(t, otherWorker, fixture.tenant.ID, fake)
	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }
	otherWorker.now = func() time.Time { return now }

	firstResult := make(chan error, 1)
	go func() {
		_, err := fixture.notifications.ProcessPendingDeliveries(context.Background(), "notification-worker-claim-one", 10)
		firstResult <- err
	}()
	select {
	case <-fake.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first notification worker did not dispatch after claiming")
	}
	completed, err := otherWorker.ProcessPendingDeliveries(fixture.ctx, "notification-worker-claim-two", 10)
	require.NoError(t, err)
	require.Zero(t, completed)
	close(release)
	require.NoError(t, <-firstResult)
	require.Len(t, fake.sentMessages(), 1)
}

func TestTicketNotificationWorkerRecoversExpiredLease(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-expired-lease")
	row := fixture.enqueueExternalCC(t)
	fake := &durableNotificationConnector{}
	configureDurableNotificationConnector(t, fixture.notifications, fixture.tenant.ID, fake)
	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }
	fixture.client.TicketNotification.UpdateOneID(row.ID).
		SetStatus("processing").
		SetLeaseOwner("stale-worker").
		SetLeaseExpiresAt(now.Add(-time.Second)).
		SetAttemptCount(1).
		ExecX(fixture.ctx)

	completed, err := fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-worker-recovery", 10)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, "sent", row.Status)
	require.Equal(t, 2, row.AttemptCount)
	require.Empty(t, row.LeaseOwner)
	require.Len(t, fake.sentMessages(), 1)
}

func TestTicketNotificationWorkerRunsImmediateSweepAndStopsOnCancellation(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-worker-lifecycle")
	fixture.enqueueExternalCC(t)
	fake := &durableNotificationConnector{entered: make(chan struct{}, 1)}
	configureDurableNotificationConnector(t, fixture.notifications, fixture.tenant.ID, fake)
	fixture.notifications.now = func() time.Time { return time.Now().Add(time.Hour) }
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		fixture.notifications.RunDeliveryWorker(ctx, "notification-worker-lifecycle", time.Hour)
		close(stopped)
	}()

	select {
	case <-fake.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("notification worker did not perform its immediate sweep")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("notification worker did not stop after cancellation")
	}
	require.Eventually(t, func() bool {
		return fixture.client.TicketNotification.Query().OnlyX(context.Background()).Status == "sent"
	}, 5*time.Second, 10*time.Millisecond)
}
