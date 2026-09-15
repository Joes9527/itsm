package connector

import (
	"context"
	"errors"
	"fmt"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
)

type targetAuthority struct {
	scopeID           string
	destinationDigest string
	capabilities      map[string]bool
}

// ActivateStartupTargets initializes the frozen candidate declaration once.
// A failed attempt requires a new Manager; no partial batch becomes visible.
// It neither enumerates persisted configurations nor starts provider polling.
func (m *Manager) ActivateStartupTargets(ctx context.Context) error {
	if m == nil || m.gate == nil {
		return executionscope.ErrDenied
	}
	targets, err := m.gate.ConnectorActivationTargets(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed || m.startupAttempted || len(m.instances) != 0 {
		m.mu.Unlock()
		return executionscope.ErrDenied
	}
	m.startupAttempted = true
	m.initializing.Add(1)
	m.mu.Unlock()
	defer m.initializing.Done()
	manifests := make([]Manifest, len(targets))
	for i, target := range targets {
		manifest, ok := m.registry.GetManifest(target.Name)
		if !ok || manifest.InitializationBehavior != InitializationLocalOnly {
			return fmt.Errorf("%w: target %d initialization is not admitted", executionscope.ErrDenied, i)
		}
		manifests[i] = manifest
	}
	prepared := make(map[string]*instance, len(targets))
	cleanup := func(cause error) error {
		for _, inst := range prepared {
			cause = errors.Join(cause, inst.conn.Close())
		}
		return cause
	}
	for i, target := range targets {
		if err := ctx.Err(); err != nil {
			return cleanup(err)
		}
		cfg := Config{
			TenantID: target.TenantID, Name: target.Name, Provider: target.Provider, Enabled: true,
			Credentials: target.Credentials, Settings: target.Settings,
		}
		conn, err := m.initializeConnector(tenantctx.WithTenantID(ctx, target.TenantID), cfg, &manifests[i], target.DestinationDigest)
		if err != nil {
			return cleanup(err)
		}
		capabilities := make(map[string]bool, len(target.Capabilities))
		for _, capability := range target.Capabilities {
			capabilities[capability] = true
		}
		prepared[instanceKey(cfg)] = &instance{cfg: cfg, conn: conn, target: &targetAuthority{target.ScopeID, target.DestinationDigest, capabilities}}
	}
	m.mu.Lock()
	if m.closed || len(m.instances) != 0 || ctx.Err() != nil {
		m.mu.Unlock()
		return cleanup(errors.Join(executionscope.ErrDenied, ctx.Err()))
	}
	for key, inst := range prepared {
		m.nextGeneration++
		inst.generation = m.nextGeneration
		m.instances[key] = inst
	}
	m.mu.Unlock()
	return nil
}

// Both ordinary provisioning and declared startup use the same construction
// path. Startup adds checks before Init and against its captured destination.
func (m *Manager) initializeConnector(ctx context.Context, cfg Config, admitted *Manifest, destination string) (Connector, error) {
	factory, ok := m.registry.Get(cfg.Name)
	if !ok {
		return nil, fmt.Errorf("connector is not registered")
	}
	conn := factory()
	if conn == nil {
		return nil, fmt.Errorf("connector factory returned no instance")
	}
	failed := func(err error) (Connector, error) { return nil, errors.Join(err, conn.Close()) }
	if admitted != nil && conn.Manifest().ComputeChecksum() != admitted.Checksum {
		return failed(fmt.Errorf("%w: connector manifest changed", executionscope.ErrDenied))
	}
	if err := conn.Init(ctx, cfg); err != nil {
		return failed(fmt.Errorf("connector initialization failed: %w", err))
	}
	if admitted != nil {
		target, ok := conn.(DeliveryDestination)
		if !ok || target.DeliveryDestinationIdentity() != destination {
			return failed(fmt.Errorf("%w: connector destination mismatch", executionscope.ErrDenied))
		}
	}
	return conn, nil
}
