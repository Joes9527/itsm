package service_catalog

import (
	"context"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/servicecatalog"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// ListAvailableForIntake uses the same live directory visibility policy as
// ResolveCreationCatalog. The caller owns a current authorized session snapshot.
func (s *Service) ListAvailableForIntake(ctx context.Context, snapshot *authorization.SessionSnapshot, after int, query string, limit int) ([]*creation.CatalogReadDefinition, error) {
	if err := authorization.RequireCurrentPermission(ctx, snapshot.Tx, snapshot.Identity, "service_catalog", "read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 51 || after < 0 {
		return nil, creation.NewInvalidCommand("invalid catalog page", creation.FieldError{}, nil)
	}
	q := snapshot.Tx.ServiceCatalog.Query().Where(servicecatalog.TenantIDEQ(snapshot.Identity.TenantID), servicecatalog.IsActiveEQ(true), servicecatalog.StatusIn("active", "enabled"), servicecatalog.IDGT(after)).Order(ent.Asc(servicecatalog.FieldID)).Limit(limit)
	if query != "" {
		q.Where(servicecatalog.Or(servicecatalog.NameContainsFold(query), servicecatalog.DescriptionContainsFold(query)))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("catalog listing unavailable", err)
	}
	result := make([]*creation.CatalogReadDefinition, 0, len(rows))
	for _, row := range rows {
		if !creation.IsSupportedRecordClass(row.TargetClass) {
			return nil, creation.NewUnsupportedRecordClass("catalog target class unavailable", nil)
		}
		definition, err := s.ReadAvailableForIntake(ctx, snapshot, row.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, definition)
	}
	return result, nil
}
func (s *Service) ReadAvailableForIntake(ctx context.Context, snapshot *authorization.SessionSnapshot, id int) (*creation.CatalogReadDefinition, error) {
	if err := authorization.RequireCurrentPermission(ctx, snapshot.Tx, snapshot.Identity, "service_catalog", "read"); err != nil {
		return nil, err
	}
	row, err := snapshot.Tx.ServiceCatalog.Query().Where(servicecatalog.IDEQ(id), servicecatalog.TenantIDEQ(snapshot.Identity.TenantID), servicecatalog.IsActiveEQ(true), servicecatalog.StatusIn("active", "enabled")).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, creation.NewReferenceNotFound("catalog unavailable", nil)
	}
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("catalog projection unavailable", err)
	}
	revision, definitions, _, err := s.projectCreationCatalog(ctx, snapshot.Tx, snapshot.Identity, row)
	if err != nil {
		return nil, err
	}
	domain := NewEntRepository(snapshot.Tx.Client()).toDomain(row)
	domain.Fields = toFieldDefinitionInputsFromEnt(definitions)
	if err = s.validateForPublicationTx(ctx, snapshot.Tx, snapshot.Identity.TenantID, domain); err != nil {
		return nil, err
	}
	result := &creation.CatalogReadDefinition{ID: row.ID, Name: row.Name, Description: row.Description, TargetClass: row.TargetClass, CatalogVersion: revision.Version, FormSchemaVersion: revision.FormSchemaVersion}
	for _, f := range definitions {
		result.Fields = append(result.Fields, creation.CatalogReadField{Name: f.Name, Label: f.Label, FieldType: f.FieldType, Required: f.Required, Options: f.Options})
	}
	return result, nil
}
