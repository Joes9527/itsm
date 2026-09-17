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
	return s.validatePublishedDefaultCTI(ctx, tx, tenantID, catalog)
}

// validatePublishedDefaultCTI 校验已发布目录的默认分类。
//
//   - 已启用目录强制门禁（cti_governance_v1.catalogEnforced）时，缺少默认分类即拒绝发布；
//   - 已配置默认分类时，无论门禁是否启用都必须是当前有效的完整三级路径，
//     否则申请入口会拿到无法解析的默认值。
//
// 未启用门禁且未配置默认值的旧目录保持既有发布行为，不会被本次代码部署阻断。
func (s *Service) validatePublishedDefaultCTI(ctx context.Context, tx *ent.Tx, tenantID int, catalog *ServiceCatalog) error {
	governance, err := service.ReadCTIGovernance(ctx, tx, tenantID)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not read CTI governance record", err)
	}
	if catalog.DefaultTicketCategoryID <= 0 {
		if governance.CatalogEnforced {
			return creation.NewDomainValidationFailed("published catalog requires a complete three-level default classification", nil)
		}
		return nil
	}
	if _, err := service.NewTicketCategoryService(tx.Client()).ResolveCTIPath(ctx, tx, tenantID, catalog.DefaultTicketCategoryID, true, true); err != nil {
		return creation.NewDomainValidationFailed("catalog default classification is not a complete active three-level path", err)
	}
	return nil
}
