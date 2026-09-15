package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"

	"itsm-backend/connector"
	_ "itsm-backend/connector/builtin/msgraph"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type durableNotificationConnector struct {
	name     string
	mu       sync.Mutex
	sendErr  error
	messages []*connector.Message
	entered  chan struct{}
	release  <-chan struct{}
}

type durableNotificationGraphSender struct {
	mu      sync.Mutex
	sendErr error
	calls   []string
}

func (s *durableNotificationGraphSender) SendMail(_ context.Context, _ string, to, _, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, to)
	return s.sendErr
}

func (s *durableNotificationGraphSender) setSendError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendErr = err
}

func (s *durableNotificationGraphSender) sentCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (c *durableNotificationConnector) Manifest() connector.Manifest {
	name := c.name
	if name == "" {
		name = "webhook"
	}
	return connector.Manifest{
		Name:                name,
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
func (c *durableNotificationConnector) DeliveryDestinationIdentity() string {
	return strings.Repeat("a", 64)
}

func (c *durableNotificationConnector) sentMessages() []*connector.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*connector.Message(nil), c.messages...)
}

func (c *durableNotificationConnector) setSendError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sendErr = err
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
	notifications := NewTicketNotificationService(client, zaptest.NewLogger(t).Sugar(), standardNotificationPolicy(t))
	notifications.SetDeliveryQueueClient(client)
	ctx = tenantctx.WithTenantID(ctx, tenant.ID)
	producer := NewTicketNotificationService(client, zaptest.NewLogger(t).Sugar(), standardNotificationPolicy(t))
	configureDurableNotificationConnector(t, producer, tenant.ID, &durableNotificationConnector{})
	workflow.SetNotificationService(producer)
	return &durableNotificationFixture{
		workflow:      workflow,
		notifications: notifications,
		client:        client,
		ctx:           ctx,
		tenant:        tenant,
		operator:      operator,
		recipient:     recipient,
		ticket:        tk,
	}
}

func (f *durableNotificationFixture) enqueueExternalCC(t *testing.T) *ent.TicketNotification {
	return f.enqueueExternalCCWithChannel(t, "webhook")
}

func (f *durableNotificationFixture) enqueueExternalCCWithChannel(t *testing.T, channel string) *ent.TicketNotification {
	t.Helper()
	require.NoError(t, f.workflow.CCTicket(f.ctx, &dto.CCTicketRequest{
		TicketID:       f.ticket.ID,
		CCUsers:        []int{f.recipient.ID},
		NotifyChannels: []string{channel},
	}, f.operator.ID, f.tenant.ID))
	return f.client.TicketNotification.Query().OnlyX(f.ctx)
}

func configureDurableNotificationConnector(t *testing.T, service *TicketNotificationService, tenantID int, fake *durableNotificationConnector) {
	t.Helper()
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return fake })
	manager := connector.NewManager(registry, zaptest.NewLogger(t).Sugar(), standardNotificationPolicy(t))
	connectorName := fake.name
	if connectorName == "" {
		connectorName = "webhook"
	}
	require.NoError(t, manager.Provision(tenantctx.WithTenantID(context.Background(), tenantID), connector.Config{
		TenantID: tenantID,
		Name:     connectorName,
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

func TestTicketNotificationWorkerFencesUnknownSendWithStableDeliveryKey(t *testing.T) {
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
	require.Equal(t, "failed", row.Status)
	require.Equal(t, 1, row.AttemptCount)
	require.Equal(t, "delivery_unknown", row.LastErrorClass)
	messages := fake.sentMessages()
	require.Len(t, messages, 1)
	require.Equal(t, *row.DeliveryKey, messages[0].ID)
	require.Equal(t, *row.DeliveryKey, messages[0].Metadata["delivery_key"])
}

func TestTicketNotificationWorkerRoutesLogicalEmailThroughBootstrapEmailWiring(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-bootstrap-email")
	_, registered := connector.Default().Get("msgraph-email")
	require.True(t, registered)
	graph := &durableNotificationGraphSender{}
	configureDurableGraphQueue(t, fixture, graph)
	row := fixture.enqueueExternalCCWithChannel(t, "email")
	require.Empty(t, graph.sentCalls())
	require.Equal(t, "msgraph-email", *row.TargetConnectorName)
	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }
	completed, err := fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-bootstrap-email-worker", 10)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	require.Equal(t, []string{fixture.recipient.Email}, graph.sentCalls())
	require.Equal(t, ticketNotificationStatusSent, fixture.client.TicketNotification.GetX(fixture.ctx, row.ID).Status)
}

func TestTicketNotificationWorkerRetriesEmailServiceFailure(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-email-retry")
	graph := &durableNotificationGraphSender{sendErr: errors.New("private receiver unavailable")}
	configureDurableGraphQueue(t, fixture, graph)
	row := fixture.enqueueExternalCCWithChannel(t, "email")
	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }
	completed, err := fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-email-retry-worker", 10)
	require.Error(t, err)
	require.Zero(t, completed)

	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, ticketNotificationStatusPending, row.Status)
	require.Equal(t, "connector_send", row.LastErrorClass)
	require.Empty(t, row.LeaseOwner)

	graph.setSendError(nil)
	now = row.NextAttemptAt
	completed, err = fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-email-retry-worker", 10)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	require.Equal(t, []string{fixture.recipient.Email, fixture.recipient.Email}, graph.sentCalls())
	require.Equal(t, ticketNotificationStatusSent, fixture.client.TicketNotification.GetX(fixture.ctx, row.ID).Status)
}

func TestTicketNotificationWorkerTreatsMalformedEmailAsPermanent(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-malformed-email")
	fixture.recipient = fixture.client.User.UpdateOneID(fixture.recipient.ID).
		SetEmail("malformed-address").
		SaveX(fixture.ctx)
	graph := &durableNotificationGraphSender{}
	configureDurableGraphQueue(t, fixture, graph)
	row := fixture.enqueueExternalCCWithChannel(t, "email")
	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }
	completed, err := fixture.notifications.ProcessPendingDeliveries(
		fixture.ctx,
		"notification-malformed-email-worker",
		10,
	)
	require.Error(t, err)
	require.Zero(t, completed)

	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, ticketNotificationStatusFailed, row.Status)
	require.Equal(t, 1, row.AttemptCount)
	require.Equal(t, "delivery_target_invalid", row.LastErrorClass)
	require.Empty(t, row.LeaseOwner)
	require.True(t, row.LeaseExpiresAt.IsZero())
	require.Empty(t, graph.sentCalls())

	now = now.Add(24 * time.Hour)
	completed, err = fixture.notifications.ProcessPendingDeliveries(
		fixture.ctx,
		"notification-malformed-email-worker",
		10,
	)
	require.NoError(t, err)
	require.Zero(t, completed)
	require.Equal(t, 1, fixture.client.TicketNotification.GetX(fixture.ctx, row.ID).AttemptCount)
}

func TestTicketNotificationWorkerMarksPermanentTargetFailureTerminal(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-terminal-target")
	row := fixture.enqueueExternalCC(t)
	row = fixture.client.TicketNotification.UpdateOneID(row.ID).SetChannel("sms").SaveX(fixture.ctx)
	fixture.notifications.SetConnectorManager(connector.NewManager(connector.NewRegistry(), zaptest.NewLogger(t).Sugar(), nil))

	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }
	completed, err := fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-terminal-target-worker", 10)
	require.Error(t, err)
	require.Zero(t, completed)

	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, "failed", row.Status)
	require.Equal(t, 1, row.AttemptCount)
	require.Equal(t, "delivery_target_invalid", row.LastErrorClass)
	require.Empty(t, row.LeaseOwner)
	require.True(t, row.LeaseExpiresAt.IsZero())

	now = now.Add(24 * time.Hour)
	completed, err = fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-terminal-target-worker", 10)
	require.NoError(t, err)
	require.Zero(t, completed)
	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, "failed", row.Status)
	require.Equal(t, 1, row.AttemptCount)
}

func TestBPMNCCFanoutUsesDistinctStableConnectorDeliveryKeys(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-bpmn-fanout")
	secondRecipient, err := createTicketWorkflowTestUser(
		fixture.ctx,
		fixture.client,
		fixture.tenant.ID,
		"notification-bpmn-fanout-second",
	)
	require.NoError(t, err)
	handler := bpmn.NewCCTaskHandler(fixture.client, zaptest.NewLogger(t).Sugar())
	handler.SetNotificationTargetBinder(fixture.workflow.notifications)
	callbackCtx := context.WithValue(fixture.ctx, bpmn.BPMNTenantIDContextKey, fixture.tenant.ID)
	callbackCtx = bpmn.WithBPMNCallbackExecutionKey(callbackCtx, "callback-fanout-key")
	_, err = handler.Execute(callbackCtx, nil, map[string]interface{}{
		"ticket_id":         fixture.ticket.ID,
		"ccType":            "variable",
		"ccResolvedUserIds": []int{fixture.recipient.ID, secondRecipient.ID},
		"ccNotify":          true,
		"notifyChannels":    "webhook",
		"addedBy":           fixture.operator.ID,
	})
	require.NoError(t, err)

	rows := fixture.client.TicketNotification.Query().AllX(fixture.ctx)
	require.Len(t, rows, 2)
	require.NotNil(t, rows[0].DeliveryKey)
	require.NotNil(t, rows[1].DeliveryKey)
	require.NotEqual(t, *rows[0].DeliveryKey, *rows[1].DeliveryKey)

	fake := &durableNotificationConnector{sendErr: newEmailTransportError("connector", "connect", emailNotAccepted, errors.New("retry fanout"))}
	configureDurableNotificationConnector(t, fixture.notifications, fixture.tenant.ID, fake)
	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }
	_, err = fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-worker-fanout-one", 10)
	require.Error(t, err)
	fake.setSendError(nil)
	rows = fixture.client.TicketNotification.Query().AllX(fixture.ctx)
	for _, row := range rows {
		if row.NextAttemptAt.After(now) {
			now = row.NextAttemptAt
		}
	}
	completed, err := fixture.notifications.ProcessPendingDeliveries(fixture.ctx, "notification-worker-fanout-two", 10)
	require.NoError(t, err)
	require.Equal(t, 2, completed)

	messageCounts := make(map[string]int)
	for _, message := range fake.sentMessages() {
		messageCounts[message.ID]++
	}
	require.Len(t, messageCounts, 2, "each recipient effect must have a distinct connector Message.ID")
	for _, count := range messageCounts {
		require.Equal(t, 2, count, "retry must retain the same effect-level Message.ID")
	}
}

func TestTicketNotificationWorkerFencesExpiredLeaseAfterSentStatusWriteFailure(t *testing.T) {
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
	require.Error(t, err)
	require.Zero(t, completed)
	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, "failed", row.Status)
	require.Equal(t, "delivery_unknown", row.LastErrorClass)
	require.Equal(t, 1, row.AttemptCount)
	require.Len(t, fake.sentMessages(), 1)
}

func TestTicketNotificationWorkerCASPreventsCompetingLiveLeaseDispatch(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-competing-claims")
	fixture.enqueueExternalCC(t)
	release := make(chan struct{})
	fake := &durableNotificationConnector{entered: make(chan struct{}, 1), release: release}
	configureDurableNotificationConnector(t, fixture.notifications, fixture.tenant.ID, fake)
	otherWorker := NewTicketNotificationService(fixture.client, zaptest.NewLogger(t).Sugar(), standardNotificationPolicy(t))
	otherWorker.SetDeliveryQueueClient(fixture.client)
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

func TestTicketNotificationWorkerFencesExpiredLease(t *testing.T) {
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
	require.Error(t, err)
	require.Zero(t, completed)
	row = fixture.client.TicketNotification.GetX(fixture.ctx, row.ID)
	require.Equal(t, "failed", row.Status)
	require.Equal(t, 1, row.AttemptCount)
	require.Empty(t, row.LeaseOwner)
	require.Empty(t, fake.sentMessages())
	require.Equal(t, "delivery_unknown", row.LastErrorClass)
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
	require.Eventually(t, func() bool {
		return fixture.client.TicketNotification.Query().OnlyX(context.Background()).Status == "sent"
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("notification worker did not stop after cancellation")
	}
}

func standardNotificationPolicy(t *testing.T) *database.ExecutionPolicy {
	t.Helper()
	p, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "test-standard", Capabilities: map[string]string{"notification": "enabled"}})
	require.NoError(t, err)
	return p
}

// No socket or Hub goroutine is needed: each case has no eligible writable
// recipient. A durable worker must not manufacture delivery evidence.
func TestTicketNotificationPushWithoutEligibleRecipientIsNotDelivered(t *testing.T) {
	for _, scenario := range []string{"offline", "full", "other tenant"} {
		t.Run(scenario, func(t *testing.T) {
			client, svc, ctx := setupTicketNotificationTest(t)
			defer client.Close()
			tenant, recipient, item := createNotifTestData(t, client, ctx)
			logger := zaptest.NewLogger(t).Sugar()
			svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, logger))
			client.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(recipient.ID).SetEventType("ticket_updated").SetInAppEnabled(false).SetEmailEnabled(false).SetSmsEnabled(false).SetPushEnabled(true).SaveX(ctx)
			hub := NewWebSocketHub(logger)
			var connection *WebSocketClient
			if scenario != "offline" {
				connection = &WebSocketClient{UserID: recipient.ID, TenantID: tenant.ID, Send: make(chan websocketFrame, 1)}
				if scenario == "full" {
					connection.Send <- websocketFrame{payload: []byte("existing message")}
				}
				if scenario == "other tenant" {
					connection.TenantID = tenant.ID + 1
				}
				hub.clients[connection] = true
			}
			svc.SetWebSocketService(&WebSocketService{hub: hub, logger: logger})
			svc.SetDeliveryQueueClient(client)
			result, err := svc.SendNotification(ctx, item.ID, &dto.SendTicketNotificationRequest{UserIDs: []int{recipient.ID}, EventType: "ticket_updated", Content: "private push", DeliveryKey: "push-no-recipient"}, tenant.ID)
			require.NoError(t, err)
			require.Equal(t, dto.TicketNotificationEffectQueued, result.Effect)
			n, err := svc.ProcessPendingDeliveries(ctx, "push-no-recipient", 10)
			assert.Error(t, err)
			assert.Zero(t, n)
			row := client.TicketNotification.Query().OnlyX(ctx)
			assert.NotEqual(t, ticketNotificationStatusSent, row.Status)
			assert.True(t, row.SentAt.IsZero())
			if scenario == "other tenant" {
				assert.Empty(t, connection.Send, "another tenant cannot receive the queued notification")
			}
		})
	}
}

func TestTicketNotificationDisabledCapabilityPreservesQueuedIntent(t *testing.T) {
	for _, channel := range []string{"email", "push"} {
		for _, state := range []string{"pending", "expired processing"} {
			t.Run(channel+"/"+state, func(t *testing.T) {
				client, svc, ctx := setupTicketNotificationTest(t)
				defer client.Close()
				tenant, recipient, item := createNotifTestData(t, client, ctx)
				ctx = tenantctx.WithTenantID(ctx, tenant.ID)
				logger := zaptest.NewLogger(t).Sugar()
				svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, logger))
				client.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(recipient.ID).SetEventType("ticket_updated").SetInAppEnabled(false).SetEmailEnabled(channel == "email").SetSmsEnabled(false).SetPushEnabled(channel == "push").SaveX(ctx)
				policy, policyErr := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "test-standard"})
				require.NoError(t, policyErr)
				svc.execution = policy
				_, calls := configureNotificationSMTPProbe(svc)
				svc.SetWebSocketService(&WebSocketService{hub: NewWebSocketHub(logger), logger: logger})
				svc.SetDeliveryQueueClient(client)
				_, err := svc.SendNotification(ctx, item.ID, &dto.SendTicketNotificationRequest{UserIDs: []int{recipient.ID}, EventType: "ticket_updated", Content: "disabled worker", DeliveryKey: "disabled-worker"}, tenant.ID)
				require.NoError(t, err)
				if state == "expired processing" {
					row := client.TicketNotification.Query().OnlyX(ctx)
					row.Update().SetStatus(ticketNotificationStatusProcessing).SetAttemptCount(1).SetLeaseOwner("previous-worker").SetLeaseExpiresAt(time.Now().Add(-time.Minute)).SaveX(ctx)
				}
				before := client.TicketNotification.Query().OnlyX(ctx)
				if channel == "email" {
					require.NotNil(t, before.TargetTransport)
					require.Equal(t, "smtp", *before.TargetTransport)
					require.NotNil(t, before.TargetDestinationDigest)
				}
				n, err := svc.ProcessPendingDeliveries(ctx, "disabled-notification", 10)
				assert.ErrorIs(t, err, executionscope.ErrDenied)
				assert.Zero(t, n)
				after := client.TicketNotification.Query().OnlyX(ctx)
				// Compare every exported persisted field, including json-hidden lease and
				// target fields; Ent's private config contains incomparable functions.
				bv, av := reflect.ValueOf(before).Elem(), reflect.ValueOf(after).Elem()
				for i := 0; i < bv.NumField(); i++ {
					if bv.Type().Field(i).IsExported() {
						assert.Equal(t, bv.Field(i).Interface(), av.Field(i).Interface(), bv.Type().Field(i).Name)
					}
				}
				assert.Empty(t, *calls)
			})
		}
	}
}

func TestTicketNotificationEmailRejectsTargetChangeAfterEnqueue(t *testing.T) {
	for _, field := range []string{"mailbox", "graph endpoint", "aad endpoint", "client identity", "in flight", "stable"} {
		t.Run(field, func(t *testing.T) { verifyNotificationGraphTargetQueue(t, field) })
	}
}
