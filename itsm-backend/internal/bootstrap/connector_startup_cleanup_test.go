package bootstrap

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	webhook "itsm-backend/connector/builtin/webhook"
	"itsm-backend/database"
)

func TestConnectorCloseWaitsForStartupInitializationCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var closed atomic.Int32
	reg := connector.NewRegistry()
	reg.Register(func() connector.Connector {
		return &startupLifecycleProbe{Webhook: webhook.New(), beforeInit: func(ctx context.Context, _ connector.Config) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, onClose: func() error { closed.Add(1); return nil }}
	})
	policy, err := database.NewExecutionPolicy(connectorStartupExecution(t, "http://127.0.0.1:12345"))
	require.NoError(t, err)
	manager := connector.NewManager(reg, nil, policy)
	started := make(chan error, 1)
	go func() {
		started <- manager.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test:startup", "verify close joins initialization"))
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	closeStarted, closeDone := make(chan struct{}), make(chan struct{})
	go func() { close(closeStarted); manager.CloseAll(); close(closeDone) }()
	<-closeStarted
	select {
	case <-closeDone:
		t.Error("CloseAll returned before the initializing object was cleaned")
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-started:
		require.ErrorIs(t, err, executionscope.ErrDenied)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-closeDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.EqualValues(t, 1, closed.Load())
	require.Empty(t, manager.ListByTenant(1))
}

func TestConnectorStartupFailureClosesCurrentAndPreparedObjects(t *testing.T) {
	for _, failure := range []string{"init", "destination", "missing identity"} {
		t.Run(failure, func(t *testing.T) {
			cfg := connectorStartupExecution(t, "http://127.0.0.1:12345")
			second := cfg.ConnectorTargets[0]
			second.Provider = "second"
			cfg.ConnectorTargets = append(cfg.ConnectorTargets, second)
			initErr, closeErr := errors.New("synthetic init failure"), errors.New("synthetic close failure")
			var inits, closes atomic.Int32
			reg := connector.NewRegistry()
			reg.Register(func() connector.Connector {
				probe := &startupLifecycleProbe{Webhook: webhook.New(), beforeInit: func(_ context.Context, c connector.Config) error {
					inits.Add(1)
					if c.Provider == "second" {
						if failure == "init" {
							return initErr
						}
						if failure == "destination" {
							c.Settings["url"] = "http://127.0.0.1:12345/changed"
						}
					}
					return nil
				}, onClose: func() error { closes.Add(1); return closeErr }}
				if failure == "missing identity" {
					return &struct{ connector.Connector }{probe}
				}
				return probe
			})
			policy, err := database.NewExecutionPolicy(cfg)
			require.NoError(t, err)
			manager := connector.NewManager(reg, nil, policy)
			err = manager.ActivateStartupTargets(tenantctx.SystemContext(context.Background(), "test:startup", "verify failure cleanup"))
			require.ErrorIs(t, err, closeErr)
			if failure == "init" {
				require.ErrorIs(t, err, initErr)
			} else {
				require.ErrorIs(t, err, executionscope.ErrDenied)
			}
			if failure == "missing identity" {
				require.EqualValues(t, 1, inits.Load())
			} else {
				require.EqualValues(t, 2, inits.Load())
			}
			require.Equal(t, inits.Load(), closes.Load())
			require.Empty(t, manager.ListByTenant(1))
			require.ErrorIs(t, manager.ActivateStartupTargets(tenantctx.SystemContext(context.Background(), "test:repeat", "failed manager cannot retry")), executionscope.ErrDenied)
			manager.CloseAll()
			require.Equal(t, inits.Load(), closes.Load())
		})
	}
}

func TestConsumerStartupFailureClosesActivatedTargets(t *testing.T) {
	cfg := connectorStartupExecution(t, "http://127.0.0.1:12345")
	cfg.Capabilities["tool_queue"] = "scoped"
	policy, err := database.NewExecutionPolicy(cfg)
	require.NoError(t, err)
	var closes atomic.Int32
	reg := connector.NewRegistry()
	reg.Register(func() connector.Connector {
		return &startupLifecycleProbe{Webhook: webhook.New(), onClose: func() error { closes.Add(1); return nil }}
	})
	manager := connector.NewManager(reg, nil, policy)
	sentinel := errors.New("synthetic tool startup failure")
	var queueClosed atomic.Bool
	queue := failingConnectorStartupQueue{start: func(context.Context) error {
		require.Len(t, manager.ListByTenant(1), 1)
		return sentinel
	}, close: func() {
		require.Len(t, manager.ListByTenant(1), 1, "stop a failed consumer before closing its connector dependency")
		queueClosed.Store(true)
	}}
	app := &Application{Cfg: &config.Config{Execution: cfg}, executionPolicy: policy, connectorManager: manager,
		notificationWorker: &recordingTicketNotificationWorker{}, toolQueue: queue,
		startBackgroundTasksFunc: func(context.Context) { t.Error("background started after startup failure") }}
	stop, err := app.startAPIRuntime(context.Background())
	require.Nil(t, stop)
	require.ErrorIs(t, err, sentinel)
	require.Empty(t, manager.ListByTenant(1))
	require.EqualValues(t, 1, closes.Load())
	require.True(t, queueClosed.Load(), "failed startup must join the attempted consumer")
}

type startupLifecycleProbe struct {
	*webhook.Webhook
	beforeInit func(context.Context, connector.Config) error
	onClose    func() error
}

func (p *startupLifecycleProbe) Init(ctx context.Context, cfg connector.Config) error {
	if p.beforeInit != nil {
		if err := p.beforeInit(ctx, cfg); err != nil {
			return err
		}
	}
	return p.Webhook.Init(ctx, cfg)
}
func (p *startupLifecycleProbe) Close() error { return p.onClose() }

type failingConnectorStartupQueue struct {
	start func(context.Context) error
	close func()
}

func (q failingConnectorStartupQueue) Start(ctx context.Context) error { return q.start(ctx) }
func (q failingConnectorStartupQueue) Close() {
	if q.close != nil {
		q.close()
	}
}
