package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// Only authoritative denials are terminal. Unknown and infrastructure errors
// retain their cause so the existing worker can retry them.
func classifyWorkItemDeliveryError(err error) error {
	if err == nil {
		return nil
	}
	if ent.IsNotFound(err) || errors.Is(err, creation.ErrPermissionDenied) || errors.Is(err, creation.ErrAuthenticationRequired) || errors.Is(err, creation.ErrReferenceNotFound) {
		return blockOutboxDelivery("current WorkItem delivery authorization denied")
	}
	if app, ok := common.AsAppError(err); ok && (app.Code == common.ErrCodeForbidden || app.Code == common.ErrCodeNotFound) {
		return blockOutboxDelivery("current WorkItem delivery authorization denied")
	}
	return fmt.Errorf("WorkItem delivery authorization unavailable: %w", err)
}

// Authorize the current selected tenant session and every endpoint at the
// tenant transaction's snapshot. Only native directory identity crosses RLS.
func authorizeWorkItemDelivery(ctx context.Context, tx *ent.Tx, directory database.DirectorySnapshot, actorID, tenantID int, endpointIDs []int) (*ent.User, error) {
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, directory, actorID, tenantID)
	if err != nil {
		return nil, classifyWorkItemDeliveryError(err)
	}
	role := authorization.EffectiveSessionRole(actor)
	for _, id := range endpointIDs {
		_, policy, err := authorization.ResolveWorkItemIdentity(ctx, tx.Client(), id, tenantID, authorization.WorkItemReadScope(actor.ID, role))
		if err != nil {
			return nil, classifyWorkItemDeliveryError(err)
		}
		if err = authorization.RequireCurrentPermission(ctx, tx, creation.Identity{TenantID: tenantID, ActorID: actor.ID, Role: role}, policy.Resource, "read"); err != nil {
			return nil, classifyWorkItemDeliveryError(err)
		}
	}
	return actor, nil
}

// Finish the read snapshot before calling the notification owner, which has its
// own transaction and persistent delivery-key deduplication. A fan-out target is
// checked independently immediately before that target is sent.
func loadWorkItemDeliveryTarget(ctx context.Context, client *ent.Client, directory database.DirectorySnapshot, actorID, tenantID, mutationID, targetID int) (mutation, target *ent.Ticket, recipient int, err error) {
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != tenantID {
		return nil, nil, 0, blockOutboxDelivery("delivery tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	tx, err := client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, 0, err
	}
	defer tx.Rollback()
	ids := []int{mutationID}
	if targetID != mutationID {
		ids = append(ids, targetID)
	}
	if _, err = authorizeWorkItemDelivery(ctx, tx, directory, actorID, tenantID, ids); err != nil {
		return nil, nil, 0, err
	}
	mutation, _, err = authorization.ResolveWorkItemIdentity(ctx, tx.Client(), mutationID, tenantID)
	if err != nil {
		return nil, nil, 0, classifyWorkItemDeliveryError(err)
	}
	target, _, err = authorization.ResolveWorkItemIdentity(ctx, tx.Client(), targetID, tenantID)
	if err != nil {
		return nil, nil, 0, classifyWorkItemDeliveryError(err)
	}
	recipient = target.AssigneeID
	if recipient <= 0 {
		recipient = target.RequesterID
	}
	if recipient <= 0 {
		return nil, nil, 0, blockOutboxDelivery("delivery target has no eligible recipient")
	}
	if _, err = authorizeWorkItemDelivery(ctx, tx, directory, recipient, tenantID, []int{targetID}); err != nil {
		return nil, nil, 0, err
	}
	if err = tx.Rollback(); err != nil {
		return nil, nil, 0, err
	}
	return mutation, target, recipient, nil
}

func authorizeWorkItemDeliveryActor(ctx context.Context, client *ent.Client, directory database.DirectorySnapshot, actorID, tenantID, workItemID int) error {
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != tenantID {
		return blockOutboxDelivery("delivery tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	tx, err := client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = authorizeWorkItemDelivery(ctx, tx, directory, actorID, tenantID, []int{workItemID}); err != nil {
		return err
	}
	return tx.Rollback()
}
