package service

import (
	"context"
	"fmt"

	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
)

// Committed tenant-policy effects use immutable receipt provenance, not a new
// discretionary role/allocation check. Actor deletion/inactivity still blocks.
func loadIntakeActor(ctx context.Context, directory *ent.Client, receipt *ent.IntakeRequest) (*ent.User, error) {
	if directory == nil {
		return nil, blockOutboxDelivery("intake actor directory unavailable")
	}
	if receipt == nil || receipt.ActorTenantID <= 0 || receipt.Status != "completed" {
		return nil, blockOutboxDelivery("intake actor provenance unavailable")
	}
	lookup := tenantctx.SystemContext(ctx, "intake:committed-actor", "validate committed creation actor activity and native provenance")
	actor, err := directory.User.Get(lookup, receipt.ActorID)
	if ent.IsNotFound(err) || (err == nil && (!actor.Active || actor.TenantID != receipt.ActorTenantID)) {
		return nil, blockOutboxDelivery("intake actor is unavailable or native provenance changed")
	}
	if err != nil {
		return nil, fmt.Errorf("load intake actor directory: %w", err)
	}
	return actor, nil
}

// Private service context is created only after outbox/receipt graph validation.
// It contains a materialized actor, never an unrestricted directory client.
type intakeStartActorKey struct{}
type intakeStartActor struct {
	actor                                 ent.User
	targetTenantID, workItemID, receiptID int
}
