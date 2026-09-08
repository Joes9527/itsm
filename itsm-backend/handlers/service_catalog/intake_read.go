package service_catalog

import (
	"context"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/servicecatalog"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
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
	result := make([]*creation.CatalogReadDefinition, 0, limit)
	// Advance the private scan cursor over every source row, including omitted
	// entries. The caller still cursors on the last returned valid item.
	for len(result) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q := snapshot.Tx.ServiceCatalog.Query().Where(servicecatalog.TenantIDEQ(snapshot.Identity.TenantID), servicecatalog.IsActiveEQ(true), servicecatalog.StatusIn("active", "enabled"), servicecatalog.IDGT(after)).Order(ent.Asc(servicecatalog.FieldID)).Limit(51)
		if query != "" {
			q.Where(servicecatalog.Or(servicecatalog.NameContainsFold(query), servicecatalog.DescriptionContainsFold(query)))
		}
		rows, err := q.All(ctx)
		if err != nil {
			return nil, creation.NewInfrastructureUnavailable("catalog listing unavailable", err)
		}
		for _, row := range rows {
			after = row.ID
			if !creation.IsSupportedRecordClass(row.TargetClass) {
				return nil, creation.NewUnsupportedRecordClass("catalog target class unavailable", nil)
			}
			definition, err := s.ReadAvailableForIntake(ctx, snapshot, row.ID)
			if err != nil {
				if !isUnavailableIntakeCatalog(err) {
					return nil, err
				}
				logger := s.logger
				if logger == nil {
					logger = zap.S()
				}
				logger.Warnw("intake catalog omitted: publication configuration unavailable", "tenant_id", snapshot.Identity.TenantID, "catalog_id", row.ID, "error_class", "publication_configuration")
				continue
			}
			result = append(result, definition)
			if len(result) == limit {
				return result, nil
			}
		}
		if len(rows) < 51 {
			break
		}
	}
	return result, nil
}

// Only deterministic owner errors on a single cause chain are item-local.
// Security/infrastructure envelopes and joined failures always propagate.
func isUnavailableIntakeCatalog(err error) bool {
	for err != nil {
		if _, joined := err.(interface{ Unwrap() []error }); joined {
			return false
		}
		if typed, ok := err.(*creation.IntakeError); ok && typed.Code != creation.DomainValidationFailed {
			return false
		}
		if _, ok := err.(*bpmn.PublicationConfigurationError); ok {
			return true
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = wrapped.Unwrap()
	}
	return false
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
	domain.AccessPolicy = revision.AccessPolicy
	if err = s.validateForPublicationTx(ctx, snapshot.Tx, snapshot.Identity.TenantID, domain); err != nil {
		return nil, err
	}
	result := &creation.CatalogReadDefinition{ID: row.ID, Name: row.Name, Description: row.Description, TargetClass: row.TargetClass, CatalogVersion: revision.Version, FormSchemaVersion: revision.FormSchemaVersion}
	for _, f := range definitions {
		options, err := service.ProjectCatalogOptions(f.Options)
		if err != nil {
			return nil, err
		}
		result.Fields = append(result.Fields, creation.CatalogReadField{Name: f.Name, Label: f.Label, FieldType: f.FieldType, Required: f.Required, Options: options})
	}
	return result, nil
}
