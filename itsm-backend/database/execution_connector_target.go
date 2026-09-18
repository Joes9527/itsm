package database

import (
	"context"
	"encoding/json"
	"fmt"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
)

// ConnectorStartupTargets supplies owned copies to the trusted startup owner.
// The internal context marker is not database-role or business authorization.
// Consumers must still validate initialization behavior and actual destination,
// and delivery owners must authorize the persisted business intent separately.
func (p *ExecutionPolicy) ConnectorStartupTargets(ctx context.Context) ([]config.ConnectorTargetConfig, error) {
	if p == nil || p.mode != "candidate" || ctx == nil || !tenantctx.IsSystemBypass(ctx) {
		return nil, executionscope.ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var targets []config.ConnectorTargetConfig
	if err := json.Unmarshal(p.connectorTargets, &targets); err != nil {
		return nil, fmt.Errorf("connector startup declarations unavailable")
	}
	return targets, nil
}

// ConnectorActivationTargets narrows the owned declarations using the same
// frozen policy that guards delivery. Description never implies activation.
// A target shared by several owners retains only currently enabled owners.
func (p *ExecutionPolicy) ConnectorActivationTargets(ctx context.Context) ([]config.ConnectorTargetConfig, error) {
	targets, err := p.ConnectorStartupTargets(ctx)
	if err != nil {
		return nil, err
	}
	active := make([]config.ConnectorTargetConfig, 0, len(targets))
	for _, target := range targets {
		capabilities := make([]string, 0, len(target.Capabilities))
		for _, capability := range target.Capabilities {
			if p.capabilities[capability] {
				capabilities = append(capabilities, capability)
			}
		}
		if len(capabilities) != 0 {
			target.Capabilities = capabilities
			active = append(active, target)
		}
	}
	return active, nil
}

// DeclaredConnectorTarget reads one exact candidate declaration for an owning
// tenant. It does not require execution to be enabled and does not authorize a
// business action. Never expose the returned protected configuration over HTTP.
func (p *ExecutionPolicy) DeclaredConnectorTarget(ctx context.Context, ref executionscope.Ref, capability, name, provider string) (config.ConnectorTargetConfig, error) {
	if err := p.RequireDeliveryIdentity(ctx, ref, capability); err != nil {
		return config.ConnectorTargetConfig{}, err
	}
	if p.mode != "candidate" {
		return config.ConnectorTargetConfig{}, executionscope.ErrDenied
	}
	var targets []config.ConnectorTargetConfig
	if err := json.Unmarshal(p.connectorTargets, &targets); err != nil {
		return config.ConnectorTargetConfig{}, fmt.Errorf("connector declarations unavailable")
	}
	for _, target := range targets {
		if target.TenantID != ref.TenantID || target.ScopeID != ref.ScopeID || target.Name != name {
			continue
		}
		if target.Provider != provider {
			return config.ConnectorTargetConfig{}, executionscope.ErrDenied
		}
		for _, declared := range target.Capabilities {
			if declared == capability {
				return target, nil
			}
		}
	}
	return config.ConnectorTargetConfig{}, fmt.Errorf("%w: %w", executionscope.ErrDenied, executionscope.ErrTargetNotConfigured)
}
