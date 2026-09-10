package service

import (
	"context"
	"sort"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
)

// GuardDeletionTx is the shared reference precondition for application-owned
// deletion. The owner supplies RR, deletes in this same transaction, and rolls
// back the whole operation on every error. It never removes relationships.
func (s *WorkItemRelationService) GuardDeletionTx(ctx context.Context, tx *ent.Tx, m workitemmutation.Meta, workItemID int) error {
	if tx == nil || m.ActorID <= 0 || m.TenantID <= 0 || workItemID <= 0 {
		return creation.NewAuthenticationRequired("authenticated deletion identity required", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return common.NewForbiddenError("tenant context mismatch")
	}
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, m.ActorID, m.TenantID)
	if err != nil {
		return err
	}
	identity := creation.Identity{ActorID: actor.ID, ActorTenantID: actor.TenantID, TenantID: m.TenantID, Role: authorization.EffectiveSessionRole(actor)}
	_, policy, err := authorization.ResolveWorkItemIdentity(ctx, tx.Client(), workItemID, m.TenantID, authorization.WorkItemReadScope(actor.ID, identity.Role))
	if err != nil {
		return err
	}
	for _, action := range []string{"read", "delete"} {
		if err = authorization.RequireCurrentPermission(ctx, tx, identity, policy.Resource, action); err != nil {
			return err
		}
	}
	if err = lockRelationEndpointsTx(ctx, tx, m.TenantID, workItemID); err != nil {
		return err
	}
	relations, err := s.ListTx(ctx, tx, m, workItemID)
	if err != nil {
		return err
	}
	if len(relations) != 0 {
		return common.NewConflictError("work item deletion", "active relations must be removed before deletion")
	}
	return nil
}

// Every reference mutator locks endpoints in ascending WorkItem ID order. SQLite
// uses its transaction writer lock; production PostgreSQL acquires row locks.
func lockRelationEndpointsTx(ctx context.Context, tx *ent.Tx, tenantID int, ids ...int) error {
	ordered := append([]int(nil), ids...)
	sort.Ints(ordered)
	for _, id := range ordered {
		_, err := tx.Ticket.Query().Where(ticket.ID(id), ticket.TenantID(tenantID), ticket.DeletedAtIsNil(), func(selector *entsql.Selector) {
			if selector.Dialect() != dialect.SQLite {
				selector.ForUpdate()
			}
		}).Select(ticket.FieldID).Only(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}
