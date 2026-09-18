package connector_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/database"
)

type healthProbe struct {
	check func(context.Context) connector.HealthStatus
}

func (*healthProbe) Manifest() connector.Manifest {
	return connector.Manifest{Name: "health-probe", Version: "1.0.0", Title: "Probe", Type: connector.TypeCustom, RequiredPermissions: []string{"connector:write"}}
}
func (*healthProbe) Init(context.Context, connector.Config) error             { return nil }
func (*healthProbe) Send(context.Context, *connector.Message) error           { return nil }
func (*healthProbe) Close() error                                             { return nil }
func (p *healthProbe) HealthCheck(ctx context.Context) connector.HealthStatus { return p.check(ctx) }

func diagnosticPolicy(t *testing.T) *database.ExecutionPolicy {
	t.Helper()
	p, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "diagnostic-test", Capabilities: map[string]string{"connector_diagnostics": "enabled"}})
	require.NoError(t, err)
	return p
}

func TestHealthSnapshotsAreTenantScopedAndDetached(t *testing.T) {
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	var calls atomic.Int32
	reg := connector.NewRegistry()
	reg.Register(func() connector.Connector {
		return &healthProbe{check: func(context.Context) connector.HealthStatus {
			calls.Add(1)
			return connector.HealthStatus{OK: false, Message: "provider unavailable", Extra: map[string]interface{}{"nested": map[string]interface{}{"value": "original"}}}
		}}
	})
	mgr := connector.NewManager(reg, nil, diagnosticPolicy(t))
	defer mgr.CloseAll()
	for _, id := range []int{1, 2} {
		require.NoError(t, mgr.Provision(tenantctx.WithTenantID(ctx, id), connector.Config{TenantID: id, Name: "health-probe", Provider: "local", Enabled: true}))
	}
	initial, err := mgr.HealthSnapshot(1)
	require.NoError(t, err)
	require.Empty(t, initial)
	require.Zero(t, calls.Load())
	require.NoError(t, mgr.RefreshHealth(ctx, 1))
	require.EqualValues(t, 1, calls.Load())
	first, err := mgr.HealthSnapshot(1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	observed := first["1/health-probe/local"]
	require.False(t, observed.OK)
	require.Equal(t, "provider unavailable", observed.Message)
	require.False(t, observed.CheckedAt.IsZero())
	observed.Extra["nested"].(map[string]interface{})["value"] = "changed"
	again, err := mgr.HealthSnapshot(1)
	require.NoError(t, err)
	require.Equal(t, "original", again["1/health-probe/local"].Extra["nested"].(map[string]interface{})["value"])
	foreign, err := mgr.HealthSnapshot(2)
	require.NoError(t, err)
	require.Empty(t, foreign)
	require.EqualValues(t, 1, calls.Load())
	require.ErrorIs(t, mgr.RefreshHealth(ctx, 2), executionscope.ErrDenied)
	require.ErrorIs(t, mgr.RefreshHealth(context.Background(), 1), executionscope.ErrDenied)
	candidate, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "diagnostic-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}, Capabilities: map[string]string{"connector_diagnostics": "disabled"}})
	require.NoError(t, err)
	require.ErrorIs(t, connector.NewManager(reg, nil, candidate).RefreshHealth(ctx, 1), executionscope.ErrDenied)
	require.ErrorIs(t, connector.NewManager(reg, nil, nil).RefreshHealth(ctx, 1), executionscope.ErrDenied)
	require.EqualValues(t, 1, calls.Load())
}

func TestHealthRefreshDiscardsReplacedGeneration(t *testing.T) {
	ctx, cancel := context.WithTimeout(tenantctx.WithTenantID(context.Background(), 1), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	reg := connector.NewRegistry()
	reg.Register(func() connector.Connector {
		return &healthProbe{check: func(c context.Context) connector.HealthStatus {
			close(entered)
			select {
			case <-release:
			case <-c.Done():
			}
			return connector.HealthStatus{OK: true}
		}}
	})
	mgr := connector.NewManager(reg, nil, diagnosticPolicy(t))
	defer mgr.CloseAll()
	cfg := connector.Config{TenantID: 1, Name: "health-probe", Provider: "local", Enabled: true}
	require.NoError(t, mgr.Provision(ctx, cfg))
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); result <- mgr.RefreshHealth(ctx, 1) }()
	defer func() { cancel(); <-finished }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, mgr.Provision(ctx, cfg))
	close(release)
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	snapshot, err := mgr.HealthSnapshot(1)
	require.NoError(t, err)
	require.Empty(t, snapshot, "old generation result must not certify replacement")
}

func TestHealthRefreshCancellationIsNotSuccessfulHealth(t *testing.T) {
	ctx, cancel := context.WithTimeout(tenantctx.WithTenantID(context.Background(), 1), 5*time.Second)
	defer cancel()
	entered := make(chan struct{})
	reg := connector.NewRegistry()
	reg.Register(func() connector.Connector {
		return &healthProbe{check: func(c context.Context) connector.HealthStatus {
			close(entered)
			<-c.Done()
			return connector.HealthStatus{OK: true}
		}}
	})
	mgr := connector.NewManager(reg, nil, diagnosticPolicy(t))
	defer mgr.CloseAll()
	require.NoError(t, mgr.Provision(ctx, connector.Config{TenantID: 1, Name: "health-probe", Provider: "local", Enabled: true}))
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); result <- mgr.RefreshHealth(ctx, 1) }()
	defer func() { cancel(); <-finished }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	snapshot, err := mgr.HealthSnapshot(1)
	require.NoError(t, err)
	require.Empty(t, snapshot)
}
