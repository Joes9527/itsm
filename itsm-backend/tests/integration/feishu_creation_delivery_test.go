package integration

import (
	"context"
	"errors"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/connector"
	feishu "itsm-backend/connector/builtin/feishu"
	"itsm-backend/ent"
	"itsm-backend/service"
)

type creationFeishuConnector struct {
	destination string
	calls       int
	task        *feishu.FeishuTask
	err         error
}

func (*creationFeishuConnector) Manifest() connector.Manifest {
	return connector.Manifest{Name: "feishu", Version: "1", Type: connector.TypeIM, RequiredPermissions: []string{"connector:write", "ticket:write"}}
}
func (*creationFeishuConnector) Init(context.Context, connector.Config) error   { return nil }
func (*creationFeishuConnector) Send(context.Context, *connector.Message) error { return nil }
func (*creationFeishuConnector) HealthCheck(context.Context) connector.HealthStatus {
	return connector.HealthStatus{OK: true}
}
func (*creationFeishuConnector) Close() error { return nil }
func (f *creationFeishuConnector) TaskDestinationIdentity() string {
	if f.destination != "" {
		return f.destination
	}
	return "configured-feishu-app"
}
func (f *creationFeishuConnector) CreateTask(_ context.Context, task *feishu.FeishuTask) (*feishu.FeishuTask, error) {
	f.calls++
	copy := *task
	f.task = &copy
	if f.err != nil {
		return nil, f.err
	}
	copy.GUID = "remote-task"
	return &copy, nil
}
func TestIntakeGenericFeishuIntentFreezesAndDeliversOwningMapping(t *testing.T) {
	fake := &creationFeishuConnector{}
	fixture := newUnifiedIntakeFixture(t, func(client *ent.Client, logger *zap.SugaredLogger) *service.TicketService {
		registry := connector.NewRegistry()
		registry.Register(func() connector.Connector { return fake })
		manager := connector.NewManager(registry, logger)
		t.Cleanup(manager.CloseAll)
		tenant := client.Tenant.Query().OnlyX(context.Background())
		require.NoError(t, manager.Provision(context.Background(), connector.Config{TenantID: tenant.ID, Name: "feishu", Type: connector.TypeIM, Enabled: true}))
		return configuredCreationTicketOwnerWithConnector(client, logger, manager)
	})
	ctx := context.Background()
	result, err := fixture.app.Create(ctx, fixture.identity, fixture.command)
	require.NoError(t, err)
	require.Zero(t, fake.calls)
	event := fixture.client.OutboxEvent.Query().OnlyX(ctx)
	require.Equal(t, "feishu.task.creation.requested", event.EventType)
	fixture.client.Ticket.UpdateOneID(result.WorkItemID).SetTitle("edited later").ExecX(ctx)
	owner := service.NewFeishuSyncService(fixture.client, zap.NewNop().Sugar(), fixture.app)
	handler := service.NewFeishuCreationDeliveryHandler(owner, func(int) (service.FeishuTaskCreator, bool) { return fake, true })
	require.NoError(t, handler.Deliver(ctx, event))
	require.Equal(t, 1, fake.calls)
	require.Contains(t, fake.task.Name, fixture.command.Title)
	require.NotContains(t, fake.task.Name, "edited later")
	require.Equal(t, 1, fixture.client.FeishuTicketSync.Query().CountX(ctx))
	require.NoError(t, handler.Deliver(ctx, event))
	require.Equal(t, 1, fake.calls, "committed mapping is a durable delivery receipt")
	fixture.command.IdempotencyKey = "second-feishu"
	second, err := fixture.app.Create(ctx, fixture.identity, fixture.command)
	require.NoError(t, err)
	secondEvent := fixture.client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(strconv.Itoa(second.WorkItemID))).OnlyX(ctx)
	fake.err = errors.New("unknown remote acceptance")
	require.ErrorContains(t, handler.Deliver(ctx, secondEvent), "delivery_unknown")
}

func TestFeishuManualAndAutomaticSyncShareCreationIntent(t *testing.T) {
	fixture := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	item, err := fixture.app.Create(ctx, fixture.identity, fixture.command)
	require.NoError(t, err)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	fc := feishu.New()
	require.NoError(t, fc.Init(ctx, connector.Config{TenantID: fixture.identity.TenantID, Credentials: map[string]string{"app_id": "app", "app_secret": "unused"}, Settings: map[string]any{"base_url": server.URL}}))
	tx, err := fixture.client.Tx(ctx)
	require.NoError(t, err)
	_, err = fc.UpdateExistingTicketTask(ctx, tx, fixture.client.Ticket.GetX(ctx, item.WorkItemID))
	require.Error(t, err)
	require.NoError(t, tx.Rollback())
	require.Zero(t, calls, "automatic mutation sync cannot create a missing remote task")
	owner := service.NewFeishuSyncService(fixture.client, zap.NewNop().Sugar(), fixture.app)
	actor := service.ActionActor{TenantID: fixture.identity.TenantID, UserID: fixture.identity.ActorID, Role: fixture.identity.Role}
	first, err := owner.SyncTicketToFeishu(ctx, actor, item.WorkItemID, fc)
	require.NoError(t, err)
	require.Equal(t, "pending", first.SyncStatus)
	second, err := owner.SyncTicketToFeishu(ctx, actor, item.WorkItemID, fc)
	require.NoError(t, err)
	require.Equal(t, "pending", second.SyncStatus)
	require.Zero(t, calls)
	require.Equal(t, 1, fixture.client.OutboxEvent.Query().CountX(ctx))
	fake := &creationFeishuConnector{destination: fc.TaskDestinationIdentity()}
	handler := service.NewFeishuCreationDeliveryHandler(owner, func(int) (service.FeishuTaskCreator, bool) { return fake, true })
	event := fixture.client.OutboxEvent.Query().OnlyX(ctx)
	require.NoError(t, handler.Deliver(ctx, event))
	require.Equal(t, 1, fake.calls)
	require.NoError(t, handler.Deliver(ctx, event))
	require.Equal(t, 1, fake.calls)
}

func TestFeishuManualSyncKeepsGovernedIntentAndCurrentAuthority(t *testing.T) {
	fake := &creationFeishuConnector{}
	f := newUnifiedIntakeFixture(t, func(client *ent.Client, logger *zap.SugaredLogger) *service.TicketService {
		registry := connector.NewRegistry()
		registry.Register(func() connector.Connector { return fake })
		manager := connector.NewManager(registry, logger)
		t.Cleanup(manager.CloseAll)
		tenant := client.Tenant.Query().OnlyX(context.Background())
		require.NoError(t, manager.Provision(context.Background(), connector.Config{TenantID: tenant.ID, Name: "feishu", Type: connector.TypeIM, Enabled: true}))
		return configuredCreationTicketOwnerWithConnector(client, logger, manager)
	})
	ctx := context.Background()
	result, err := f.app.Create(ctx, f.identity, f.command)
	require.NoError(t, err)
	original := f.client.OutboxEvent.Query().OnlyX(ctx)
	fc := feishu.New()
	require.NoError(t, fc.Init(ctx, connector.Config{TenantID: f.identity.TenantID, Credentials: map[string]string{"app_id": "different-app", "app_secret": "unused"}}))
	owner := service.NewFeishuSyncService(f.client, zap.NewNop().Sugar(), f.app)
	actor := service.ActionActor{TenantID: f.identity.TenantID, UserID: f.identity.ActorID, Role: "super_admin"}
	response, err := owner.SyncTicketToFeishu(ctx, actor, result.WorkItemID, fc)
	require.NoError(t, err)
	require.Equal(t, "pending", response.SyncStatus)
	require.Equal(t, original.Payload, f.client.OutboxEvent.GetX(ctx, original.ID).Payload, "manual request cannot replace destination, actor or frozen task of an existing creation intent")
	f.client.OutboxEvent.UpdateOneID(original.ID).SetStatus("blocked").SetLastError("delivery_unknown").ExecX(ctx)
	response, err = owner.SyncTicketToFeishu(ctx, actor, result.WorkItemID, fc)
	require.NoError(t, err)
	require.Equal(t, "blocked", response.SyncStatus)
	require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(ctx))
	f.client.RolePermission.Delete().ExecX(ctx)
	_, err = owner.SyncTicketToFeishu(ctx, actor, result.WorkItemID, fc)
	require.Error(t, err, "caller role cannot override current persisted authority")
	handler := service.NewFeishuCreationDeliveryHandler(owner, func(int) (service.FeishuTaskCreator, bool) { return fake, true })
	require.Error(t, handler.Deliver(ctx, original))
	require.Zero(t, fake.calls)
	actor.TenantID++
	_, err = owner.SyncTicketToFeishu(ctx, actor, result.WorkItemID, fc)
	require.Error(t, err)
}

func TestFeishuConcurrentManualSyncWritesOneIntent(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	result, err := f.app.Create(ctx, f.identity, f.command)
	require.NoError(t, err)
	fc := feishu.New()
	require.NoError(t, fc.Init(ctx, connector.Config{TenantID: f.identity.TenantID, Credentials: map[string]string{"app_id": "app", "app_secret": "unused"}}))
	owner := service.NewFeishuSyncService(f.client, zap.NewNop().Sugar(), f.app)
	actor := service.ActionActor{TenantID: f.identity.TenantID, UserID: f.identity.ActorID}
	reached := make(chan struct{}, 2)
	release := make(chan struct{})
	f.client.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) {
				reached <- struct{}{}
				<-release
			}
			return next.Mutate(ctx, m)
		})
	})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := owner.SyncTicketToFeishu(ctx, actor, result.WorkItemID, fc); results <- err }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-reached:
		case <-time.After(10 * time.Second):
			t.Fatal("manual calls did not reach concurrent intent creation")
		}
	}
	close(release)
	successes := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			successes++
		}
	}
	require.Positive(t, successes)
	require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(ctx))
	require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.ActionEQ("feishu_sync_requested")).CountX(ctx))
	// A concurrent loser retries into the same immutable intent.
	replay, err := owner.SyncTicketToFeishu(ctx, actor, result.WorkItemID, fc)
	require.NoError(t, err)
	require.Equal(t, "pending", replay.SyncStatus)
	require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(ctx))
}
