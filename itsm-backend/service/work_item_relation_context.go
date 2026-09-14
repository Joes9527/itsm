package service

import (
	"context"
	"database/sql"
	"errors"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	relationmeta "itsm-backend/common/workitemrelation"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
)

type RelationMutationAvailability struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}
type RelationContext struct {
	Source   relationmeta.Endpoint        `json:"source"`
	Mutation RelationMutationAvailability `json:"mutation"`
}

// Context projects source-only eligibility in one read-only RR snapshot. Target
// access, direction and tuple validity remain the mutation owner's responsibility.
func (s *WorkItemRelationService) Context(ctx context.Context, meta workitemmutation.Meta, id int) (*RelationContext, error) {
	if s.client == nil {
		return nil, common.NewInternalError("relation application unavailable", nil)
	}
	if meta.ActorID <= 0 || meta.TenantID <= 0 {
		return nil, common.NewUnauthorizedError("authenticated actor and tenant required")
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != meta.TenantID {
		return nil, common.NewForbiddenError("tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, meta.TenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, meta.ActorID, meta.TenantID)
	if err != nil {
		return nil, err
	}
	identity := creation.Identity{TenantID: meta.TenantID, ActorID: actor.ID, ActorTenantID: actor.TenantID, Role: authorization.EffectiveSessionRole(actor)}
	item, policy, err := authorization.ResolveWorkItemIdentity(ctx, tx.Client(), id, meta.TenantID, authorization.WorkItemReadScope(actor.ID, identity.Role))
	if err != nil {
		return nil, err
	}
	if err = authorization.RequireCurrentPermission(ctx, tx, identity, policy.Resource, "read"); err != nil {
		return nil, err
	}
	result := &RelationContext{Source: relationmeta.Endpoint{WorkItemID: item.ID, Number: item.TicketNumber, RecordClass: item.RecordClass, Title: item.Title, Status: item.Status, Version: item.Version}, Mutation: RelationMutationAvailability{Allowed: true}}
	if err = authorization.RequireCurrentPermission(ctx, tx, identity, policy.Resource, policy.ResolveAction("update")); err != nil {
		if errors.Is(err, creation.ErrPermissionDenied) {
			result.Mutation = RelationMutationAvailability{Reason: "current actor cannot update source relations"}
			return result, nil
		}
		if app, ok := common.AsAppError(err); ok && app.Code == common.ErrCodeForbidden {
			result.Mutation = RelationMutationAvailability{Reason: app.Message}
			return result, nil
		}
		return nil, err
	}
	if item.RecordClass == "change_request" {
		if err = workitemmutation.RequireSettledChangeCallbacks(ctx, tx, meta.TenantID, id); err != nil {
			var unresolved *workitemmutation.UnresolvedChangeCallbackError
			if errors.As(err, &unresolved) {
				result.Mutation = RelationMutationAvailability{Reason: err.Error()}
				return result, nil
			}
			return nil, err
		}
	}
	return result, nil
}
