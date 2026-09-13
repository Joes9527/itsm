package bootstrap

import (
	"context"
	"fmt"
	"itsm-backend/common/tenantctx"
	"sync"
)

// startAPIRuntime owns cancellation and waits before callers close dependencies.
// The dedicated KAF process remains the only owner of KAF delivery.
func (app *Application) startAPIRuntime(ctx context.Context) (func(), error) {
	if app.Cfg == nil {
		return nil, fmt.Errorf("execution configuration required")
	}
	if err := app.Cfg.Execution.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("runtime context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	requirements := []struct {
		name  string
		ready bool
	}{
		{"tool_queue", app.toolQueue != nil},
		{"outbox", app.outboxDeliveryWorker != nil},
		{"callback", app.callbackWorker != nil},
		{"notification", app.notificationWorker != nil},
		{"event_audit", app.eventRuntime != nil},
		{"webhook", app.eventRuntime != nil},
		{"connector_poll", app.connectorRuntime != nil && app.connectorManager != nil},
		{"embedding", app.DBClient != nil && app.Embedder != nil && app.VectorStore != nil},
		{"sla", app.slaMonitor != nil && app.executionPolicy != nil && (app.executionPolicy.IsCandidate() || app.systemClient != nil)},
		{"escalation", app.DBClient != nil},
	}
	for _, requirement := range requirements {
		if app.Cfg.Execution.Enabled(requirement.name) && !requirement.ready {
			return nil, fmt.Errorf("enabled capability %s requires its runtime dependencies", requirement.name)
		}
	}
	runtimeCtx, cancel := context.WithCancel(ctx)
	if app.eventRuntime != nil {
		if err := app.eventRuntime.Start(runtimeCtx); err != nil {
			cancel()
			return nil, fmt.Errorf("start event runtime: %w", err)
		}
	}
	if app.toolQueue != nil && app.Cfg.Execution.Enabled("tool_queue") {
		if err := app.toolQueue.Start(runtimeCtx); err != nil {
			cancel()
			if app.eventRuntime != nil {
				_ = app.eventRuntime.Close()
			}
			return nil, fmt.Errorf("start tool queue: %w", err)
		}
	}
	if app.Cfg.Execution.Enabled("connector_poll") {
		if app.connectorRuntime == nil || app.connectorManager == nil {
			cancel()
			if app.eventRuntime != nil {
				_ = app.eventRuntime.Close()
			}
			if app.toolQueue != nil {
				app.toolQueue.Close()
			}
			return nil, fmt.Errorf("connector runtime required")
		}
		if err := app.connectorRuntime.LoadAll(tenantctx.SystemContext(runtimeCtx, "runtime:connectors", "activate explicitly enabled connector runtime")); err != nil {
			cancel()
			if app.connectorRuntime != nil {
				app.connectorRuntime.ClosePolling()
			}
			app.connectorManager.CloseAll()
			if app.eventRuntime != nil {
				_ = app.eventRuntime.Close()
			}
			if app.toolQueue != nil {
				app.toolQueue.Close()
			}
			return nil, fmt.Errorf("activate connectors: %w", err)
		}
	}
	waitForOutboxDelivery := func() {}
	if app.Cfg.Execution.Enabled("outbox") {
		waitForOutboxDelivery = app.startOutboxDeliveryWorker(runtimeCtx)
	}
	app.startBackground(runtimeCtx)
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			if app.eventRuntime != nil {
				_ = app.eventRuntime.Close()
			}
			if app.toolQueue != nil {
				app.toolQueue.Close()
			}
			waitForOutboxDelivery()
			app.backgroundTasks.Wait()
			if app.connectorManager != nil {
				if app.connectorRuntime != nil {
					app.connectorRuntime.ClosePolling()
				}
				app.connectorManager.CloseAll()
			}
		})
	}, nil
}
