package service_catalog

import (
	"context"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
)

func (s *Service) ValidateForPublication(ctx context.Context, tenantID int, catalog *ServiceCatalog) error {
	if s.client == nil {
		return creation.NewInfrastructureUnavailable("catalog transaction client is required", nil)
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return s.validateForPublicationTx(ctx, tx, tenantID, catalog)
}
func (s *Service) validateForPublicationTx(ctx context.Context, tx *ent.Tx, tenantID int, catalog *ServiceCatalog) error {
	if catalog == nil || catalog.TenantID != tenantID {
		return creation.NewDomainValidationFailed("catalog tenant mismatch", nil)
	}
	if !creation.IsSupportedRecordClass(catalog.TargetClass) {
		return creation.NewUnsupportedRecordClass("catalog targetClass must be explicitly configured", nil)
	}
	if catalog.SLAResponseTime < 0 || catalog.SLAResolutionTime < 0 {
		return creation.NewDomainValidationFailed("invalid declared SLA durations", nil)
	}
	if s.creators == nil {
		return creation.NewInfrastructureUnavailable("catalog creator registry is required", nil)
	}
	if _, err := s.creators.Get(catalog.TargetClass); err != nil {
		return err
	}
	for _, field := range catalog.Fields {
		switch field.FieldType {
		case "text", "textarea", "number", "date", "boolean", "file":
		case "select", "multiselect":
			if len(field.Options) == 0 {
				return creation.NewDomainValidationFailed("choice field requires options: "+field.Name, nil)
			}
			for _, option := range field.Options {
				value, ok := option.(map[string]interface{})
				if !ok || value["value"] == nil {
					return creation.NewDomainValidationFailed("choice field requires option values: "+field.Name, nil)
				}
			}
		default:
			return creation.NewDomainValidationFailed("unsupported field type: "+field.Name, nil)
		}
	}
	if catalog.AccessPolicy != nil {
		if catalog.TargetClass != creation.RecordClassServiceRequestItem || !catalog.RequiresApproval {
			return creation.NewDomainValidationFailed("external access requires a requested item and business approval", nil)
		}
		if err := ValidateAccessPolicy(catalog.AccessPolicy, catalog.Fields); err != nil {
			return creation.NewDomainValidationFailed("invalid access policy", err)
		}
	}
	if err := service.NewProcessBindingService(tx.Client()).ValidateAccessPolicyBinding(ctx, tx, tenantID, catalog.TargetClass, catalog.ProcessDefinitionKey, catalog.AccessPolicy); err != nil {
		return creation.NewDomainValidationFailed("access capability binding is incomplete", err)
	}
	// Built-in professional inputs are owned by the registered Creator.Prepare.
	// Catalog Fields describe custom FormValues and must not duplicate typed input.
	err := service.NewProcessBindingService(tx.Client()).ValidateCreationPublication(ctx, tx, tenantID, catalog.TargetClass, catalog.ProcessDefinitionKey, catalog.RequiresApproval, s.publicationEngine)
	if err != nil {
		return creation.NewDomainValidationFailed("catalog publication configuration is incomplete", err)
	}
	return nil
}
