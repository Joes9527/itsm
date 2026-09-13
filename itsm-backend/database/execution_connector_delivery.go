package database

import (
	"context"
	"itsm-backend/common/executionscope"
)

// RequireConnectorDelivery checks deployment identity and the fixed owner's
// transport capability. Persistent source/claim/actor authorization stays with
// the delivery owner, in its existing transaction.
func (p *ExecutionPolicy) RequireConnectorDelivery(ctx context.Context, ref executionscope.Ref, capability string) error {
	switch capability {
	case "webhook", "notification", "outbox":
	default:
		return executionscope.ErrDenied
	}
	if err := p.RequireCapability(ctx, ref.TenantID, capability); err != nil {
		return err
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
