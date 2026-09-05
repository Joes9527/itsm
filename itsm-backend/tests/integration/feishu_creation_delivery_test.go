package integration

import (
	"context"
	"errors"
	"itsm-backend/ent/outboxevent"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/connector"
	feishu "itsm-backend/connector/builtin/feishu"
	"itsm-backend/ent"
	"itsm-backend/service"
)

type creationFeishuConnector struct {
	calls int
	task  *feishu.FeishuTask
	err   error
}

func (*creationFeishuConnector) Manifest() connector.Manifest {
	return connector.Manifest{Name: "feishu", Version: "1", Type: connector.TypeIM, RequiredPermissions: []string{"connector:write", "ticket:write"}}
}
func (*creationFeishuConnector) Init(context.Context, connector.Config) error   { return nil }
func (*creationFeishuConnector) Send(context.Context, *connector.Message) error { return nil }
func (*creationFeishuConnector) HealthCheck(context.Context) connector.HealthStatus {
	return connector.HealthStatus{OK: true}
}
func (*creationFeishuConnector) Close() error                    { return nil }
func (*creationFeishuConnector) TaskDestinationIdentity() string { return "configured-feishu-app" }
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
