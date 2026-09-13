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
