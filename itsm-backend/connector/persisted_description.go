package connector

import (
	"context"
	"encoding/json"
	"fmt"

	"itsm-backend/common/executionscope"
	"itsm-backend/ent"
	"itsm-backend/ent/connectorconfig"
)

// DescribePersistedDeliveryTarget reads the existing standard configuration
// store using the business owner's transaction client. It never reads activated
// instances or starts a provider. The caller must retain transaction, tenant,
// business authorization and durable intent checks; a digest authorizes no send.
func (m *Manager) DescribePersistedDeliveryTarget(ctx context.Context, ref executionscope.Ref, capability string, client *ent.Client, name, provider string) (string, error) {
	if m == nil || m.gate == nil || ctx == nil || client == nil || name == "" || provider == "" {
		return "", executionscope.ErrDenied
	}
	if err := m.gate.RequirePersistedConnectorDescription(ctx, ref, capability); err != nil {
		return "", err
	}
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return "", executionscope.ErrDenied
	}
	// Multiple enabled providers for one route are ambiguous. Do not silently
	// choose one by iteration order or hide another enabled provider in a filter.
	rows, err := client.ConnectorConfig.Query().Where(connectorconfig.TenantIDEQ(ref.TenantID), connectorconfig.NameEQ(name), connectorconfig.EnabledEQ(true)).Limit(2).All(ctx)
	if err != nil {
		return "", fmt.Errorf("read connector configuration: %w", err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("%w: %w", executionscope.ErrDenied, executionscope.ErrTargetNotConfigured)
	}
	if len(rows) != 1 || rows[0].Provider != provider {
		return "", executionscope.ErrDenied
	}
	row := rows[0]
	cfg := Config{TenantID: row.TenantID, Name: row.Name, Provider: row.Provider, Enabled: row.Enabled}
	if json.Unmarshal([]byte(row.Settings), &cfg.Settings) != nil || json.Unmarshal([]byte(row.Credentials), &cfg.Credentials) != nil {
		return "", executionscope.ErrDenied
	}
	digest, err := m.registry.DescribeDeliveryDestination(cfg)
	if err != nil {
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
