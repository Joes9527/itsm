package authorization

import (
	"context"
	"errors"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"time"
)

// ResolveLifecycleActor applies the established tenant-session policy at the
// owning transaction's snapshot. Only the separate restricted directory view
// receives SystemContext; the tenant transaction is never rebound.
func ResolveLifecycleActor(ctx context.Context, tx *ent.Tx, snapshots database.DirectorySnapshot, actorID, targetTenantID int) (*ent.User, error) {
	if tx == nil || actorID <= 0 || targetTenantID <= 0 {
		return nil, common.NewForbiddenError("command actor unavailable")
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != targetTenantID {
		return nil, common.NewForbiddenError("tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, targetTenantID)
	// Without a directory capability, only identities visible to the scoped
	// transaction can be resolved; RLS remains enforced and invisible actors fail closed.
	directory, lookup := tx.Client(), ctx
	closeDirectory := func() error { return nil }
	if snapshots != nil {
		var err error
		directory, closeDirectory, err = snapshots.Open(ctx, tx, targetTenantID)
		if err != nil || directory == nil || closeDirectory == nil {
			if closeDirectory != nil {
				err = errors.Join(err, closeDirectory())
			}
			return nil, common.NewInternalError("command directory snapshot unavailable", err)
		}
		lookup = tenantctx.SystemContext(ctx, "lifecycle:actor", "read native actor and selected tenant at command snapshot")
	}
	actor, err := directory.User.Get(lookup, actorID)
	if err == nil && !actor.Active {
		err = common.NewForbiddenError("command actor unavailable")
	}
	if err == nil {
		_, err = AuthorizeTenantSession(lookup, directory, actor, targetTenantID, time.Now())
	}
	closeErr := closeDirectory()
	if closeErr != nil {
		return nil, common.NewInternalError("close command directory snapshot", errors.Join(err, closeErr))
	}
	if err != nil {
		if ent.IsNotFound(err) || errors.Is(err, ErrTenantAccessDenied) || errors.Is(err, ErrTenantInactive) || errors.Is(err, ErrTenantExpired) {
			return nil, common.NewForbiddenError("command actor is not authorized for selected tenant")
		}
		if app, ok := common.AsAppError(err); ok && app.Code == common.ErrCodeForbidden {
			return nil, err
		}
		return nil, common.NewInternalError("command actor authorization unavailable", err)
	}
	return actor, nil
}
