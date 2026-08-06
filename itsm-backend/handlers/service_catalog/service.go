package service_catalog

import (
	"context"
	"strings"

	"go.uber.org/zap"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/service"
)

// Service defines the business logic
type Service struct {
	repo   Repository
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewService creates a new Service
func NewService(repo Repository, client *ent.Client, logger *zap.SugaredLogger) *Service {
	return &Service{
		repo:   repo,
		client: client,
		logger: logger,
	}
}

func (s *Service) Create(ctx context.Context, name, category, description string, deliveryTime, tenantID int, status string, ciTypeID, cloudServiceID int, fields []service.FieldDefinitionInput) (*ServiceCatalog, error) {
	name = strings.TrimSpace(name)
	category = strings.TrimSpace(category)
	if name == "" || category == "" {
		return nil, common.NewBadRequestError("Service name and category are required", nil)
	}
	if deliveryTime == 0 {
		deliveryTime = 1
	}
	if deliveryTime < 1 || deliveryTime > 3650 {
		return nil, common.NewBadRequestError("Delivery time must be between 1 and 3650 days", nil)
	}
	if status == "" {
		status = "enabled"
	}
	if !isValidCatalogStatus(status) {
		return nil, common.NewBadRequestError("Invalid service catalog status", nil)
	}
	if cloudServiceID > 0 && ciTypeID == 0 {
		return nil, common.NewBadRequestError("CI type is required when linking a cloud service", nil)
	}
	exists, err := s.repo.NameExists(ctx, tenantID, name, 0)
	if err != nil {
		return nil, common.NewInternalError("Failed to validate service catalog name", err)
	}
	if exists {
		return nil, common.NewConflictError("Service catalog name", name)
	}
	if err := s.repo.ValidateReferences(ctx, tenantID, ciTypeID, cloudServiceID); err != nil {
		return nil, common.NewBadRequestError(err.Error(), err)
	}
	catalog := &ServiceCatalog{
		Name:           name,
		Category:       category,
		Description:    description,
		DeliveryTime:   deliveryTime,
		CITypeID:       ciTypeID,
		CloudServiceID: cloudServiceID,
		Status:         status,
		TenantID:       tenantID,
	}
	created, err := s.repo.Create(ctx, catalog)
	if err != nil {
		return nil, err
	}
	if s.client != nil {
		if _, err := service.NewFieldDefinitionService(s.client).ReplaceDefinitions(ctx, tenantID, "service_catalog", created.ID, fields); err != nil {
			return nil, common.NewInternalError("Failed to save custom field definitions", err)
		}
	}
	created.Fields = fields
	return created, nil
}

func (s *Service) Get(ctx context.Context, tenantID int, id int) (*ServiceCatalog, error) {
	catalog, err := s.repo.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if s.client != nil {
		defs, err := service.NewFieldDefinitionService(s.client).ListDefinitions(ctx, tenantID, "service_catalog", catalog.ID)
		if err != nil {
			s.logger.Warnw("Failed to load field definitions for service catalog", "error", err, "catalog_id", catalog.ID)
		} else {
			catalog.Fields = toFieldDefinitionInputsFromEnt(defs)
		}
	}
	return catalog, nil
}

func (s *Service) List(ctx context.Context, tenantID int, filters ListFilters) ([]*ServiceCatalog, int, error) {
	if filters.Page < 1 {
		filters.Page = 1
	}
	if filters.Size < 1 {
		filters.Size = 10
	}
	if filters.Size > 100 {
		filters.Size = 100
	}
	catalogs, total, err := s.repo.List(ctx, tenantID, filters)
	if err != nil {
		return nil, 0, err
	}
	if s.client != nil && len(catalogs) > 0 {
		ids := make([]int, len(catalogs))
		for i, c := range catalogs {
			ids[i] = c.ID
		}
		defsByCatalog, err := service.NewFieldDefinitionService(s.client).ListDefinitionsForEntities(ctx, tenantID, "service_catalog", ids)
		if err != nil {
			s.logger.Warnw("Failed to batch-load field definitions for service catalogs", "error", err)
		} else {
			for _, c := range catalogs {
				c.Fields = toFieldDefinitionInputsFromEnt(defsByCatalog[c.ID])
			}
		}
	}
	return catalogs, total, nil
}

// toFieldDefinitionInputsFromEnt 把查出来的 ent.FieldDefinition 转成领域层的 FieldDefinitionInput。
func toFieldDefinitionInputsFromEnt(defs []*ent.FieldDefinition) []service.FieldDefinitionInput {
	result := make([]service.FieldDefinitionInput, 0, len(defs))
	for _, d := range defs {
		result = append(result, service.FieldDefinitionInput{
			Name: d.Name, Label: d.Label, FieldType: d.FieldType,
			Required: d.Required, Options: d.Options, SortOrder: d.SortOrder,
		})
	}
	return result
}

func (s *Service) Update(ctx context.Context, tenantID int, id int, name, category, description string, deliveryTime int, status string, ciTypeID, cloudServiceID int, fields []service.FieldDefinitionInput) (*ServiceCatalog, error) {
	// First check if exists
	current, err := s.repo.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	name = strings.TrimSpace(name)
	category = strings.TrimSpace(category)
	if deliveryTime < 0 || deliveryTime > 3650 {
		return nil, common.NewBadRequestError("Delivery time must be between 1 and 3650 days", nil)
	}
	if status != "" && !isValidCatalogStatus(status) {
		return nil, common.NewBadRequestError("Invalid service catalog status", nil)
	}
	effectiveName := current.Name
	if name != "" {
		effectiveName = name
	}
	exists, err := s.repo.NameExists(ctx, tenantID, effectiveName, id)
	if err != nil {
		return nil, common.NewInternalError("Failed to validate service catalog name", err)
	}
	if exists {
		return nil, common.NewConflictError("Service catalog name", effectiveName)
	}
	effectiveCITypeID := current.CITypeID
	if ciTypeID > 0 {
		effectiveCITypeID = ciTypeID
	}
	effectiveCloudServiceID := current.CloudServiceID
	if cloudServiceID > 0 {
		effectiveCloudServiceID = cloudServiceID
	}
	if effectiveCloudServiceID > 0 && effectiveCITypeID == 0 {
		return nil, common.NewBadRequestError("CI type is required when linking a cloud service", nil)
	}
	if err := s.repo.ValidateReferences(ctx, tenantID, effectiveCITypeID, effectiveCloudServiceID); err != nil {
		return nil, common.NewBadRequestError(err.Error(), err)
	}

	// Apply updates
	if name != "" {
		current.Name = name
	}
	if category != "" {
		current.Category = category
	}
	if description != "" {
		current.Description = description
	}
	if deliveryTime > 0 {
		current.DeliveryTime = deliveryTime
	}
	if status != "" {
		current.Status = status
	}
	if ciTypeID > 0 {
		current.CITypeID = ciTypeID
	}
	if cloudServiceID > 0 {
		current.CloudServiceID = cloudServiceID
	}

	updated, err := s.repo.Update(ctx, tenantID, current)
	if err != nil {
		return nil, err
	}
	if fields != nil && s.client != nil {
		if _, err := service.NewFieldDefinitionService(s.client).ReplaceDefinitions(ctx, tenantID, "service_catalog", id, fields); err != nil {
			return nil, common.NewInternalError("Failed to save custom field definitions", err)
		}
		updated.Fields = fields
	}
	return updated, nil
}

func (s *Service) Delete(ctx context.Context, tenantID int, id int) error {
	if _, err := s.repo.Get(ctx, tenantID, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, tenantID, id)
}

func (s *Service) Search(ctx context.Context, tenantID int, keyword string, filters ListFilters) ([]*ServiceCatalog, int, error) {
	if filters.Page < 1 {
		filters.Page = 1
	}
	if filters.Size < 1 {
		filters.Size = 20
	}
	if filters.Size > 100 {
		filters.Size = 100
	}
	return s.repo.Search(ctx, tenantID, strings.TrimSpace(keyword), filters)
}

func isValidCatalogStatus(status string) bool {
	return status == "enabled" || status == "disabled"
}

func (s *Service) GetStats(ctx context.Context, tenantID int) (*ServiceStats, error) {
	// Count total services
	total, err := s.repo.Count(ctx, tenantID, ListFilters{})
	if err != nil {
		return nil, err
	}

	// Count published (enabled) services
	enabled, err := s.repo.Count(ctx, tenantID, ListFilters{Status: "enabled"})
	if err != nil {
		return nil, err
	}

	// Count by category
	byCategory, err := s.repo.CountByCategory(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	return &ServiceStats{
		TotalServices:     total,
		PublishedServices: enabled,
		Categories:        byCategory,
	}, nil
}
