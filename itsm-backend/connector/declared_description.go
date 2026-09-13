package connector

import (
	"context"
	"itsm-backend/common/executionscope"
)

// DescribeDeclaredDeliveryTarget verifies configured identity before creating a
// durable intent. It never initializes an instance or authorizes delivery.
// Exact selection is provided by the business owner, never inferred from live
// instances. Standard deployments must read their persisted configuration owner.
func (m *Manager) DescribeDeclaredDeliveryTarget(ctx context.Context, ref executionscope.Ref, capability, name, provider string) (string, error) {
	if m == nil || m.gate == nil || ctx == nil {
		return "", executionscope.ErrDenied
	}
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return "", executionscope.ErrDenied
	}
	target, err := m.gate.DeclaredConnectorTarget(ctx, ref, capability, name, provider)
	if err != nil {
		return "", err
	}
	digest, err := m.registry.DescribeDeliveryDestination(Config{TenantID: target.TenantID, Name: target.Name, Provider: target.Provider, Settings: target.Settings, Credentials: target.Credentials})
	if err != nil || digest != target.DestinationDigest {
		return "", executionscope.ErrDenied
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return "", executionscope.ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return digest, nil
}
