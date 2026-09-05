package service

import (
	"context"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/cloudresource"
	"itsm-backend/ent/configurationitem"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func (*ConfigurationItemService) ResolveCreationCIs(ctx context.Context, tx *ent.Tx, identity creation.Identity, ids []int, cloudRef *int) ([]*ent.ConfigurationItem, error) {
	if len(ids) == 0 && cloudRef == nil {
		return nil, nil
	}
	if err := authorization.RequireCurrentPermission(ctx, tx, identity, "cmdb", "read"); err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 {
			return nil, creation.NewDomainValidationFailed("invalid configuration item ID", nil)
		}
		seen[id] = true
	}
	if cloudRef != nil {
		exists, err := tx.CloudResource.Query().Where(cloudresource.IDEQ(*cloudRef), cloudresource.TenantIDEQ(identity.TenantID)).Exist(ctx)
		if err != nil {
			return nil, creation.NewInfrastructureUnavailable("could not resolve cloud resource", err)
		}
		if !exists {
			return nil, creation.NewReferenceNotFound("cloud resource is outside tenant", nil)
		}
		ci, err := tx.ConfigurationItem.Query().Where(configurationitem.CloudResourceRefIDEQ(*cloudRef), configurationitem.TenantIDEQ(identity.TenantID)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return nil, creation.NewInfrastructureUnavailable("could not resolve linked CI", err)
		}
		if ci != nil {
			seen[ci.ID] = true
		}
	}
	requested := make([]int, 0, len(seen))
	for id := range seen {
		requested = append(requested, id)
	}
	items, err := tx.ConfigurationItem.Query().Where(configurationitem.IDIn(requested...), configurationitem.TenantIDEQ(identity.TenantID)).Order(ent.Asc(configurationitem.FieldID)).All(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not resolve configuration items", err)
	}
	if len(items) != len(seen) {
		return nil, creation.NewReferenceNotFound("configuration item is outside tenant", nil)
	}
	return items, nil
}
