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
