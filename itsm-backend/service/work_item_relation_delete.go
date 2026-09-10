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
	return s.guardDeletionsTx(ctx, tx, m, []int{workItemID}, 0, nil)
}

// GuardDeletionsTx validates the complete batch before acquiring its sorted locks.
// The owning RR transaction must roll back every deletion on any failure.
func (s *WorkItemRelationService) GuardDeletionsTx(ctx context.Context, tx *ent.Tx, m workitemmutation.Meta, ids []int, precondition func(*ent.Ticket) error) error {
	return s.guardDeletionsTx(ctx, tx, m, ids, 0, precondition)
}

func (s *WorkItemRelationService) GuardSubtaskDeletionTx(ctx context.Context, tx *ent.Tx, m workitemmutation.Meta, parentID, childID int, precondition func(*ent.Ticket) error) error {
	if parentID <= 0 {
		return common.NewValidationError("parent WorkItem is required", nil)
	}
	return s.guardDeletionsTx(ctx, tx, m, []int{childID}, parentID, precondition)
}

func (s *WorkItemRelationService) guardDeletionsTx(ctx context.Context, tx *ent.Tx, m workitemmutation.Meta, ids []int, parentID int, precondition func(*ent.Ticket) error) error {
	if tx == nil || m.ActorID <= 0 || m.TenantID <= 0 {
		return creation.NewAuthenticationRequired("authenticated deletion identity required", nil)
	}
	if len(ids) == 0 {
		return common.NewValidationError("deletion IDs are required", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return common.NewForbiddenError("tenant context mismatch")
	}
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, m.ActorID, m.TenantID)
	if err != nil {
		return err
	}
	identity := creation.Identity{ActorID: actor.ID, ActorTenantID: actor.TenantID, TenantID: m.TenantID, Role: authorization.EffectiveSessionRole(actor)}
	ordered := append([]int(nil), ids...)
	sort.Ints(ordered)
	items := make([]*ent.Ticket, 0, len(ordered))
	for i, id := range ordered {
		if id <= 0 || i > 0 && id == ordered[i-1] {
			return common.NewValidationError("deletion IDs must be positive and distinct", nil)
		}
		item, policy, err := authorization.ResolveWorkItemIdentity(ctx, tx.Client(), id, m.TenantID, authorization.WorkItemReadScope(actor.ID, identity.Role))
		if err != nil {
			return err
		}
		for _, action := range []string{"read", "delete"} {
			if err = authorization.RequireCurrentPermission(ctx, tx, identity, policy.Resource, action); err != nil {
				return err
			}
		}
		items = append(items, item)
	}
	lockIDs := append([]int(nil), ordered...)
	if parentID > 0 {
		if _, err = s.ListTx(ctx, tx, m, parentID); err != nil {
			return err
		}
		if len(items) != 1 || items[0].ParentTicketID != parentID {
			return common.NewValidationError("subtask does not belong to parent", nil)
		}
		lockIDs = append(lockIDs, parentID)
	}
	if err = lockRelationEndpointsTx(ctx, tx, m.TenantID, lockIDs...); err != nil {
		return err
	}
	for _, item := range items {
		relations, err := s.ListTx(ctx, tx, m, item.ID)
		if err != nil {
			return err
		}
		if len(relations) != 0 {
			return common.NewConflictError("work item deletion", "active relations must be removed before deletion")
		}
		if item.RecordClass == creation.RecordClassChangeRequest {
			if err = workitemmutation.RequireSettledChangeCallbacks(ctx, tx, m.TenantID, item.ID); err != nil {
				return err
			}
		}
		if precondition != nil {
			if err = precondition(item); err != nil {
				return err
			}
		}
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
