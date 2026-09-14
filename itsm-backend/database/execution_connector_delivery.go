package database

import (
	"context"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
)

// RequireConnectorDelivery checks deployment identity and the fixed owner's
// transport capability. Persistent source/claim/actor authorization stays with
// the delivery owner, in its existing transaction.
func (p *ExecutionPolicy) RequireConnectorDelivery(ctx context.Context, ref executionscope.Ref, capability string) error {
	if err := p.RequireDeliveryIdentity(ctx, ref, capability); err != nil {
		return err
	}
	return p.RequireCapability(ctx, ref.TenantID, capability)
}

// RequirePersistedConnectorDescription permits standard-mode configuration
// reads only. Candidate descriptions must use their frozen declarations.
// This checks source identity, not execution enablement or business authority.
func (p *ExecutionPolicy) RequirePersistedConnectorDescription(ctx context.Context, ref executionscope.Ref, capability string) error {
	if err := p.RequireDeliveryIdentity(ctx, ref, capability); err != nil {
		return err
	}
	if p.mode != "standard" {
		return executionscope.ErrDenied
	}
	return nil
}

// RequireDeliveryIdentity verifies an owner's frozen deployment/tenant identity.
// It permits pure description while execution is disabled, not a business effect.
func (p *ExecutionPolicy) RequireDeliveryIdentity(ctx context.Context, ref executionscope.Ref, capability string) error {
	if p == nil || ctx == nil || tenantctx.IsSystemBypass(ctx) {
		return executionscope.ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if tenantID, ok := tenantctx.TenantID(ctx); !ok || tenantID != ref.TenantID {
		return executionscope.ErrDenied
	}
	switch capability {
	case "webhook", "notification", "outbox":
	default:
		return executionscope.ErrDenied
	}
	expected, err := p.EventRef(ref.TenantID)
	if err != nil {
		return err
	}
	if expected != ref {
		return executionscope.ErrDenied
	}
	return nil
}
