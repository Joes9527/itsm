package bootstrap

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/connector"
	msgraph "itsm-backend/connector/builtin/msgraph"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type recordingBPMNCallbackWorker struct {
	starts  atomic.Int32
	started chan callbackWorkerStart
	stopped chan struct{}
}

type callbackWorkerStart struct {
	workerID string
	interval time.Duration
}

type recordingTicketNotificationWorker struct {
	starts  atomic.Int32
	started chan callbackWorkerStart
	stopped chan struct{}
}

func (w *recordingTicketNotificationWorker) RunDeliveryWorker(ctx context.Context, workerID string, interval time.Duration) {
	w.starts.Add(1)
	w.started <- callbackWorkerStart{workerID: workerID, interval: interval}
	<-ctx.Done()
	close(w.stopped)
}

func (w *recordingBPMNCallbackWorker) RunCallbackOutboxWorker(ctx context.Context, workerID string, interval time.Duration) {
	w.starts.Add(1)
	w.started <- callbackWorkerStart{workerID: workerID, interval: interval}
	<-ctx.Done()
	close(w.stopped)
}

func TestApplicationStartsOneCallbackWorkerAndStopsOnCancellation(t *testing.T) {
	worker := &recordingBPMNCallbackWorker{
		started: make(chan callbackWorkerStart, 1),
		stopped: make(chan struct{}),
	}
	app := &Application{callbackWorker: worker}
	ctx, cancel := context.WithCancel(context.Background())
	app.startCallbackOutboxWorker(ctx)

	select {
	case start := <-worker.started:
		require.True(t, strings.HasPrefix(start.workerID, "bpmn-callback-"))
		require.Greater(t, len(start.workerID), len("bpmn-callback-"))
		require.Equal(t, 2*time.Second, start.interval)
	case <-time.After(time.Second):
		t.Fatal("application did not start callback worker immediately")
	}
	require.Equal(t, int32(1), worker.starts.Load())

	cancel()
	select {
	case <-worker.stopped:
	case <-time.After(time.Second):
		t.Fatal("application callback worker did not stop after cancellation")
	}
}

func TestApplicationStartsOneNotificationDeliveryWorkerAndStopsOnCancellation(t *testing.T) {
	worker := &recordingTicketNotificationWorker{
		started: make(chan callbackWorkerStart, 1),
		stopped: make(chan struct{}),
	}
	app := &Application{notificationWorker: worker}
	ctx, cancel := context.WithCancel(context.Background())
	app.startNotificationDeliveryWorker(ctx)

	select {
	case start := <-worker.started:
		require.True(t, strings.HasPrefix(start.workerID, "ticket-notification-"))
		require.Greater(t, len(start.workerID), len("ticket-notification-"))
		require.Equal(t, 2*time.Second, start.interval)
	case <-time.After(time.Second):
		t.Fatal("application did not start notification delivery worker immediately")
	}
	require.Equal(t, int32(1), worker.starts.Load())

	cancel()
	select {
	case <-worker.stopped:
	case <-time.After(time.Second):
		t.Fatal("application notification delivery worker did not stop after cancellation")
	}
}

func TestBootstrapEmailGraphProviderLookupUsesRequestedTenant(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return msgraph.New() })
	manager := connector.NewManager(registry, zaptest.NewLogger(t).Sugar())
	t.Cleanup(manager.CloseAll)
	for _, fixture := range []struct {
		tenantID int
		mailbox  string
	}{
		{tenantID: 1, mailbox: "tenant-one@example.test"},
		{tenantID: 2, mailbox: "tenant-two@example.test"},
	} {
		require.NoError(t, manager.Provision(context.Background(), connector.Config{
			TenantID: fixture.tenantID,
			Name:     "msgraph-email",
			Type:     connector.TypeEmail,
			Provider: fixture.mailbox,
			Enabled:  true,
			Credentials: map[string]string{
				"azure_client_id":     "client-id",
				"azure_client_secret": "client-secret",
			},
			Settings: map[string]interface{}{
				"azure_tenant_id": "azure-tenant",
				"mailbox":         fixture.mailbox,
			},
		}))
	}

	provider := newTenantGraphProvider(manager)
	sender, mailbox, ok := provider(2)
	require.True(t, ok)
	require.NotNil(t, sender)
	require.Equal(t, "tenant-two@example.test", mailbox)
	_, _, ok = provider(3)
	require.False(t, ok)
}
