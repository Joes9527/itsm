package service_catalog

import (
	"context"
	"database/sql"

	"errors"
	"strings"
	"time"

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
	result, defs, fields, err := s.projectCreationCatalog(ctx, tx, identity, catalog)
	if err != nil {
		return nil, nil, err
	}
	if result.AccessPolicy != nil {
		if err := ValidateAccessPolicy(result.AccessPolicy, toFieldDefinitionInputsFromEnt(defs)); err != nil {
			return nil, nil, creation.NewDomainValidationFailed("invalid access policy", err)
		}
	}
	if err := service.NewProcessBindingService(tx.Client()).ValidateAccessPolicyBinding(ctx, tx, identity.TenantID, catalog.TargetClass, catalog.ProcessDefinitionKey, result.AccessPolicy); err != nil {
		return nil, nil, creation.NewDomainValidationFailed("access capability binding is incomplete", err)
	}
	return result, fields, nil
}

func (s *Service) projectCreationCatalog(ctx context.Context, tx *ent.Tx, identity creation.Identity, catalog *ent.ServiceCatalog) (*creation.ResolvedCatalog, []*ent.FieldDefinition, []creation.ResolvedFieldDefinition, error) {
	id := catalog.ID
	definitions, err := tx.FieldDefinition.Query().Where(fielddefinition.TenantIDEQ(identity.TenantID), fielddefinition.EntityTypeEQ("service_catalog"), fielddefinition.EntityIDEQ(id), fielddefinition.IsActiveEQ(true)).Order(ent.Asc(fielddefinition.FieldSortOrder), ent.Asc(fielddefinition.FieldID)).All(ctx)
	if err != nil {
		return nil, nil, nil, creation.NewInfrastructureUnavailable("could not load catalog fields", err)
	}
	fields := make([]creation.ResolvedFieldDefinition, 0, len(definitions))
	fieldEvidence, err := publicFields(definitions)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, field := range definitions {
		fields = append(fields, creation.ResolvedFieldDefinition{ID: field.ID, Key: field.Name, DataType: field.FieldType, Required: field.Required, Options: field.Options})
	}
	routingRevision, err := service.NewProcessBindingService(tx.Client()).CreationConfigurationRevision(ctx, tx, identity.TenantID, catalog.TargetClass, catalog.ProcessDefinitionKey, s.publicationEngine)
	if err != nil {
		return nil, nil, nil, err
	}
	formVersion, err := creationRevision(fieldEvidence)
	if err != nil {
		return nil, nil, nil, err
	}
	policy, err := ReadAccessPolicy(ctx, tx.Client(), identity.TenantID, catalog.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	version, err := creationRevision(publicCatalogDefinition{
		AccessPolicy: policy,
		TargetClass:  catalog.TargetClass, ServiceType: catalog.ServiceType,
		Name: catalog.Name, Description: catalog.Description, Category: catalog.Category,
		DeliveryTime: catalog.DeliveryTime, RequiresApproval: catalog.RequiresApproval,
		SLAResponseTime: catalog.SLAResponseTime, SLAResolutionTime: catalog.SLAResolutionTime,
		CITypeID: catalog.CiTypeID, CloudServiceID: catalog.CloudServiceID,
		ProcessDefinitionKey: catalog.ProcessDefinitionKey, Status: catalog.Status, IsActive: catalog.IsActive,
		Fields: fieldEvidence, RoutingRevision: routingRevision,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	result := &creation.ResolvedCatalog{AccessPolicy: policy, ID: catalog.ID, Version: version, TargetClass: catalog.TargetClass, ServiceType: catalog.ServiceType, DeliveryTime: catalog.DeliveryTime, FormSchemaVersion: formVersion, ProcessDefinitionKey: catalog.ProcessDefinitionKey, RequiresApproval: catalog.RequiresApproval, SLAResponseTime: catalog.SLAResponseTime, SLAResolutionTime: catalog.SLAResolutionTime}
	if catalog.CiTypeID > 0 {
		result.ConfigurationItemTypeID = &catalog.CiTypeID
	}
	if catalog.CloudServiceID > 0 {
		result.CloudServiceID = &catalog.CloudServiceID
	}
	return result, definitions, fields, nil
}
func creationRevision(value any) (string, error) {
	return creation.ConfigurationRevision("catalog-v1", value)
}

// Read returns display fields and confirmation revisions from one database
// snapshot. The submission later validates these exact revisions in Intake.
func (s *Service) Read(ctx context.Context, identity creation.Identity, id int) (*ServiceCatalog, error) {
	if identity.TenantID <= 0 || identity.ActorID <= 0 || strings.TrimSpace(identity.Role) == "" {
		return nil, creation.NewAuthenticationRequired("authenticated catalog reader is required", nil)
	}
	if s.client == nil || s.directory == nil {
		return nil, creation.NewInfrastructureUnavailable("catalog directory snapshot is required", nil)
	}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not begin catalog read", err)
	}
	defer tx.Rollback()
	directory, closeDirectory, err := s.directory.Open(ctx, tx, identity.TenantID)
	if err != nil {
		if closeDirectory != nil {
			err = errors.Join(err, closeDirectory())
		}
		return nil, creation.NewInfrastructureUnavailable("could not open catalog directory snapshot", err)
	}
	if directory == nil || closeDirectory == nil {
		var closeErr error
		if closeDirectory != nil {
			closeErr = closeDirectory()
		}
		return nil, creation.NewInfrastructureUnavailable("invalid catalog directory snapshot", closeErr)
	}
	directoryClosed := false
	defer func() {
		if !directoryClosed {
			_ = closeDirectory()
		}
	}()
	_, authErr := authorization.ResolveCurrentSessionActor(ctx, directory, identity.ActorID, identity.TenantID, identity.Role, time.Now())
	closeErr := closeDirectory()
	directoryClosed = true
	if closeErr != nil {
		return nil, creation.NewInfrastructureUnavailable("could not close catalog directory snapshot", errors.Join(authErr, closeErr))
	}
	if authErr != nil {
		return nil, authErr
	}
	if err := authorization.RequireCurrentPermission(ctx, tx, identity, "service_catalog", "read"); err != nil {
		return nil, err
	}
	record, err := tx.ServiceCatalog.Query().Where(servicecatalog.IDEQ(id), servicecatalog.TenantIDEQ(identity.TenantID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, creation.NewReferenceNotFound("catalog is unavailable", err)
	}
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not read catalog", err)
	}
	revision, definitions, _, err := s.projectCreationCatalog(ctx, tx, identity, record)
	if err != nil {
		return nil, err
	}
	result := NewEntRepository(tx.Client()).toDomain(record)
	result.Fields = toFieldDefinitionInputsFromEnt(definitions)
	result.AccessPolicy = revision.AccessPolicy
	result.CatalogVersion, result.FormSchemaVersion = revision.Version, revision.FormSchemaVersion
	if err := tx.Commit(); err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not complete catalog read", err)
	}
	return result, nil
}
