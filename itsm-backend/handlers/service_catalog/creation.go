package service_catalog

import (
	"context"
	"encoding/json"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/fielddefinition"
	"itsm-backend/ent/servicecatalog"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
)

// ResolveCreationCatalog is the sole catalog definition/revision projection for
// intake. Revision hashes declared configuration, ordered field definitions and
// declared process/SLA configuration. Request values select a route separately.
func (s *Service) ResolveCreationCatalog(ctx context.Context, tx *ent.Tx, identity creation.Identity, id int) (*creation.ResolvedCatalog, []creation.ResolvedFieldDefinition, error) {
	if err := authorization.RequireCurrentPermission(ctx, tx, identity, "service_catalog", "read"); err != nil {
		return nil, nil, err
	}
	catalog, err := tx.ServiceCatalog.Query().Where(servicecatalog.IDEQ(id), servicecatalog.TenantIDEQ(identity.TenantID), servicecatalog.IsActiveEQ(true), servicecatalog.StatusIn("active", "enabled")).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil, creation.NewReferenceNotFound("catalog is unavailable", err)
	}
	if err != nil {
		return nil, nil, creation.NewInfrastructureUnavailable("could not load catalog", err)
	}
	if !creation.IsSupportedRecordClass(catalog.TargetClass) {
		return nil, nil, creation.NewUnsupportedRecordClass("catalog target class is not configured", nil)
	}
	definitions, err := tx.FieldDefinition.Query().Where(fielddefinition.TenantIDEQ(identity.TenantID), fielddefinition.EntityTypeEQ("service_catalog"), fielddefinition.EntityIDEQ(id), fielddefinition.IsActiveEQ(true)).Order(ent.Asc(fielddefinition.FieldSortOrder), ent.Asc(fielddefinition.FieldID)).All(ctx)
	if err != nil {
		return nil, nil, creation.NewInfrastructureUnavailable("could not load catalog fields", err)
	}
	fields := make([]creation.ResolvedFieldDefinition, 0, len(definitions))
	fieldEvidence := make([]map[string]any, 0, len(definitions))
	for _, field := range definitions {
		fields = append(fields, creation.ResolvedFieldDefinition{ID: field.ID, Key: field.Name, DataType: field.FieldType, Required: field.Required, Options: field.Options})
		fieldEvidence = append(fieldEvidence, map[string]any{"id": field.ID, "name": field.Name, "label": field.Label, "type": field.FieldType, "required": field.Required, "options": field.Options, "sortOrder": field.SortOrder, "config": field.Config})
	}
	routingRevision, err := service.NewProcessBindingService(tx.Client()).CreationConfigurationRevision(ctx, tx, identity.TenantID, catalog.TargetClass, catalog.ProcessDefinitionKey)
	if err != nil {
		return nil, nil, err
	}
	formVersion, err := creationRevision(fieldEvidence)
	if err != nil {
		return nil, nil, err
	}
	// Include every declared catalog field; clearing timestamps excludes unrelated
	// update clocks while future configuration fields automatically join revision.
	raw, err := json.Marshal(catalog)
	if err != nil {
		return nil, nil, creation.NewInternalFailure("could not encode catalog revision", err)
	}
	var properties map[string]json.RawMessage
	if err = json.Unmarshal(raw, &properties); err != nil {
		return nil, nil, creation.NewInternalFailure("could not encode catalog revision", err)
	}
	delete(properties, "created_at")
	delete(properties, "updated_at")
	delete(properties, "edges")
	version, err := creationRevision(map[string]any{"catalog": properties, "fields": fieldEvidence, "routingRevision": routingRevision})
	if err != nil {
		return nil, nil, err
	}
	result := &creation.ResolvedCatalog{ID: catalog.ID, Version: version, TargetClass: catalog.TargetClass, ServiceType: catalog.ServiceType, DeliveryTime: catalog.DeliveryTime, FormSchemaVersion: formVersion, ProcessDefinitionKey: catalog.ProcessDefinitionKey, RequiresApproval: catalog.RequiresApproval, SLAResponseTime: catalog.SLAResponseTime, SLAResolutionTime: catalog.SLAResolutionTime}
	if catalog.CiTypeID > 0 {
		result.ConfigurationItemTypeID = &catalog.CiTypeID
	}
	if catalog.CloudServiceID > 0 {
		result.CloudServiceID = &catalog.CloudServiceID
	}
	return result, fields, nil
}
func creationRevision(value any) (string, error) {
	return creation.ConfigurationRevision("catalog-v1", value)
}
