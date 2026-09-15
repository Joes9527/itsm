package bootstrap

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service"
)

type scopeRecordingSLAMonitor struct {
	t   *testing.T
	ids []int
}

func (m *scopeRecordingSLAMonitor) CheckSLAViolations(ctx context.Context, id int) (*service.SLACheckStats, error) {
	require.False(m.t, tenantctx.IsSystemBypass(ctx))
	actual, ok := tenantctx.TenantID(ctx)
	require.True(m.t, ok)
	require.Equal(m.t, id, actual)
	m.ids = append(m.ids, id)
	return &service.SLACheckStats{}, nil
}

func TestSLACycleUsesFrozenTenantContexts(t *testing.T) {
	cfg := config.ExecutionConfig{Mode: "candidate", DeploymentID: "sla-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 7, ScopeID: "11111111-1111-4111-8111-111111111111"}}}
	policy, err := database.NewExecutionPolicy(cfg)
	require.NoError(t, err)
	monitor := &scopeRecordingSLAMonitor{t: t}
	app := &Application{executionPolicy: policy, slaMonitor: monitor}
	cfg.Scopes[0].TenantID = 99
	require.NoError(t, app.runSLACycle(context.Background()))
	require.Equal(t, []int{7}, monitor.ids)
}

func TestSLAStartupRequiresConfiguredMonitor(t *testing.T) {
	for _, missing := range []string{"monitor", "policy", "discovery"} {
		t.Run(missing, func(t *testing.T) {
			cfg := config.ExecutionConfig{Mode: "standard", DeploymentID: "sla-test", Capabilities: map[string]string{"sla": "enabled"}}
			policy, err := database.NewExecutionPolicy(cfg)
			require.NoError(t, err)
			app := &Application{Cfg: &config.Config{Execution: cfg}, DBClient: &ent.Client{}, systemClient: &ent.Client{}, executionPolicy: policy, slaMonitor: &scopeRecordingSLAMonitor{t: t}, startBackgroundTasksFunc: func(context.Context) { t.Error("started without required dependency") }}
			switch missing {
			case "monitor":
				app.slaMonitor = nil
			case "policy":
				app.executionPolicy = nil
			case "discovery":
				app.systemClient = nil
			}
			stop, err := app.startAPIRuntime(context.Background())
			require.Nil(t, stop)
			require.ErrorContains(t, err, "enabled capability sla")
		})
	}
}

func TestSLACycleStandardDiscovery(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sla-cycle-discovery?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	first := client.Tenant.Create().SetName("SLA one").SetCode("sla-one").SaveX(ctx)
	second := client.Tenant.Create().SetName("SLA two").SetCode("sla-two").SaveX(ctx)
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "sla-standard"})
	require.NoError(t, err)
	monitor := &scopeRecordingSLAMonitor{t: t}
	app := &Application{executionPolicy: policy, slaMonitor: monitor, systemClient: client}
	require.NoError(t, app.runSLACycle(ctx))
	require.ElementsMatch(t, []int{first.ID, second.ID}, monitor.ids)
}

type scopeRecordingEscalation struct {
	t   *testing.T
	ids []int
}

func (m *scopeRecordingEscalation) ProcessEscalations(ctx context.Context, id int) error {
	require.False(m.t, tenantctx.IsSystemBypass(ctx))
	actual, ok := tenantctx.TenantID(ctx)
	require.True(m.t, ok)
	require.Equal(m.t, id, actual)
	m.ids = append(m.ids, id)
	return nil
}

func TestEscalationCycleUsesFrozenTenantContexts(t *testing.T) {
	cfg := config.ExecutionConfig{Mode: "candidate", DeploymentID: "escalation-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 7, ScopeID: "11111111-1111-4111-8111-111111111111"}}}
	policy, err := database.NewExecutionPolicy(cfg)
	require.NoError(t, err)
	processor := &scopeRecordingEscalation{t: t}
	app := &Application{executionPolicy: policy, escalationService: processor}
	cfg.Scopes[0].TenantID = 99
	require.NoError(t, app.runEscalationCycle(context.Background()))
	require.Equal(t, []int{7}, processor.ids)
}

func TestEscalationStartupRequiresConfiguredProcessor(t *testing.T) {
	for _, missing := range []string{"processor", "policy", "discovery"} {
		t.Run(missing, func(t *testing.T) {
			cfg := config.ExecutionConfig{Mode: "standard", DeploymentID: "escalation-test", Capabilities: map[string]string{"escalation": "enabled"}}
			policy, err := database.NewExecutionPolicy(cfg)
			require.NoError(t, err)
			app := &Application{Cfg: &config.Config{Execution: cfg}, DBClient: &ent.Client{}, systemClient: &ent.Client{}, executionPolicy: policy, escalationService: &scopeRecordingEscalation{t: t}, startBackgroundTasksFunc: func(context.Context) { t.Error("started without required dependency") }}
			switch missing {
			case "processor":
				app.escalationService = nil
			case "policy":
				app.executionPolicy = nil
			case "discovery":
				app.systemClient = nil
			}
			stop, err := app.startAPIRuntime(context.Background())
			require.Nil(t, stop)
			require.ErrorContains(t, err, "enabled capability escalation")
		})
	}
}
