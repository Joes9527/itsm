package connector

import (
	"context"
	"itsm-backend/common/executionscope"
)

// ResolveDeliveryTarget captures one exact instance after checking the frozen
// deployment and target declaration. It does not authorize a business effect:
// callers must retain their persistent source, member, claim and receipt checks.
func (m *Manager) ResolveDeliveryTarget(ctx context.Context, ref executionscope.Ref, capability, name, provider string) (Connector, uint64, string, error) {
	if m == nil || m.gate == nil {
		return nil, 0, "", executionscope.ErrDenied
	}
	if err := m.gate.RequireConnectorDelivery(ctx, ref, capability); err != nil {
		return nil, 0, "", err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, 0, "", executionscope.ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, "", err
	}
	inst, ok := m.instances[instanceKey(Config{TenantID: ref.TenantID, Name: name, Provider: provider})]
	if !ok || !inst.cfg.Enabled || inst.cfg.TenantID != ref.TenantID || inst.cfg.Name != name || inst.cfg.Provider != provider {
		return nil, 0, "", executionscope.ErrDenied
	}
	destination, ok := inst.conn.(DeliveryDestination)
	if !ok {
		return nil, 0, "", executionscope.ErrDenied
	}
	digest := destination.DeliveryDestinationIdentity()
	if digest == "" {
		return nil, 0, "", executionscope.ErrDenied
	}
	if ref.ScopeID != "" {
		if inst.target == nil || inst.target.scopeID != ref.ScopeID || !inst.target.capabilities[capability] || inst.target.destinationDigest != digest {
			return nil, 0, "", executionscope.ErrDenied
		}
	} else if inst.target != nil {
		return nil, 0, "", executionscope.ErrDenied
	}
	return inst.conn, inst.generation, digest, nil
}
