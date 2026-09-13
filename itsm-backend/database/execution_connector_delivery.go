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
	if err := p.requireConnectorIdentity(ctx, ref, capability); err != nil {
		return err
	}
	return p.RequireCapability(ctx, ref.TenantID, capability)
}

func (p *ExecutionPolicy) requireConnectorIdentity(ctx context.Context, ref executionscope.Ref, capability string) error {
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
