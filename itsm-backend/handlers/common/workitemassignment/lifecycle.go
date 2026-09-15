package workitemassignment

import (
	"context"
	"sync/atomic"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
)

// WithLifecycleWriter binds shared assignment to an existing professional
// command transaction. actorID and tenantID must come from the command's trusted
// authentication metadata; resolving an active directory user is not authentication.
// The caller must authorize the professional command and row before Apply and
// roll back the owning transaction on any error. This function never commits or
// begins a transaction, and never grants professional permission.
//
// The writer is valid only synchronously inside use. Its private identity is
// independent of the mutable actor projection supplied for domain authorization.
func WithLifecycleWriter(ctx context.Context, tx *ent.Tx, directory database.DirectorySnapshot, actorID, tenantID int, enqueue Enqueue, use func(*Writer, *ent.User) error) error {
	if tx == nil || enqueue == nil || use == nil || tenantctx.IsSystemBypass(ctx) {
		return common.NewForbiddenError("assignment command context unavailable")
	}
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, directory, actorID, tenantID)
	if err != nil {
		return err
	}
	boundActorID, nativeTenantID, client := actor.ID, actor.TenantID, tx.Client()
	var active atomic.Bool
	active.Store(true)
	defer active.Store(false)
	writer := NewWriter(enqueue, func(checkCtx context.Context, checkClient *ent.Client, cmd Command) error {
		if !active.Load() || checkClient != client || cmd.ActorID != boundActorID || cmd.ActorTenantID != nativeTenantID || cmd.TenantID != tenantID || cmd.AssigneeID < 0 || tenantctx.IsSystemBypass(checkCtx) {
			return common.NewForbiddenError("assignment command identity mismatch")
		}
		// Resolve in the owning transaction on every mutation, including clears. A
		// finished transaction cannot pass this read, even while use is still active.
		current, err := authorization.ResolveLifecycleActor(checkCtx, tx, directory, boundActorID, tenantID)
		if err != nil {
			return err
		}
		if current.TenantID != nativeTenantID {
			return common.NewForbiddenError("assignment actor tenant changed")
		}
		if cmd.AssigneeID != 0 {
			// ResolveLifecycleActor uses the same active-user/AuthorizeTenantSession
			// policy as ResolveCurrentTenantUser, without rebinding a tenant transaction
			// to SystemContext when no restricted directory capability is available.
			_, err = authorization.ResolveLifecycleActor(checkCtx, tx, directory, cmd.AssigneeID, tenantID)
		}
		return err
	})
	return use(writer, actor)
}
