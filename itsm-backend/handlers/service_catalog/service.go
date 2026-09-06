package service_catalog

import (
	"context"
	"database/sql"
	"errors"
	"itsm-backend/dto"
	"itsm-backend/ent/servicecatalog"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service/bpmn"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"itsm-backend/common"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/service"
)

// Service defines the business logic
type Service struct {
	repo              Repository
	client            *ent.Client
	logger            *zap.SugaredLogger
	directory         database.DirectorySnapshot
	publicationEngine *service.CustomProcessEngine
	creators          interface {
		Get(string) (creation.ProfessionalCreator, error)
	}
}

// NewService creates the Catalog owner. Directory is required for authenticated
// Read; callers using only the already-authorized Intake projection may pass nil.
func NewService(repo Repository, client *ent.Client, logger *zap.SugaredLogger, directory database.DirectorySnapshot) *Service {
	return &Service{
		repo:      repo,
		client:    client,
		logger:    logger,
		directory: directory,
	}
}

func (s *Service) Create(ctx context.Context, tenantID int, input dto.CreateServiceCatalogRequest) (*ServiceCatalog, error) {
	delivery := 1
	var err error
	if input.DeliveryTime != "" {
		delivery, err = strconv.Atoi(input.DeliveryTime)
		if err != nil {
			return nil, common.NewBadRequestError("invalid deliveryTime", err)
		}
	}
	status := input.Status
	if status == "" {
		status = "disabled"
	}
	catalog := &ServiceCatalog{Name: strings.TrimSpace(input.Name), Category: strings.TrimSpace(input.Category), Description: input.Description, DeliveryTime: delivery, TenantID: tenantID, Status: status, CITypeID: input.CITypeID, CloudServiceID: input.CloudServiceID, ProcessDefinitionKey: strings.TrimSpace(input.ProcessDefinitionKey), ServiceType: input.ServiceType, TargetClass: input.TargetClass, RequiresApproval: input.RequiresApproval, SLAResponseTime: input.SLAResponseTime, SLAResolutionTime: input.SLAResolutionTime, Fields: nil}
	fields, err := catalogFieldInputs(input.Fields)
	if err != nil {
		return nil, common.NewBadRequestError("invalid fields", err)
	}
	catalog.Fields = fields
	if s.client == nil {
		return nil, creation.NewInfrastructureUnavailable("catalog transaction client is required", nil)
	}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	repo := NewEntRepository(tx.Client())
	if err := s.validateDefinition(ctx, repo, catalog); err != nil {
		return nil, err
	}
	created, err := repo.Create(ctx, catalog)
	if err != nil {
		return nil, err
	}
	if _, err := service.NewFieldDefinitionService(tx.Client()).ReplaceDefinitionsTx(ctx, tx, tenantID, "service_catalog", created.ID, catalog.Fields); err != nil {
		return nil, common.NewBadRequestError("invalid field definitions", err)
	}
	if err := saveAccessPolicy(ctx, tx, tenantID, created.ID, input.AccessPolicy); err != nil {
		return nil, err
	}
	result, err := s.finishDefinition(ctx, tx, tenantID, created.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) validateDefinition(ctx context.Context, repo Repository, catalog *ServiceCatalog) error {
	if catalog.TenantID <= 0 || catalog.Name == "" || catalog.Category == "" {
		return common.NewBadRequestError("name, category and tenant are required", nil)
	}
	if catalog.DeliveryTime < 1 || catalog.DeliveryTime > 3650 {
		return common.NewBadRequestError("Delivery time must be between 1 and 3650 days", nil)
	}
	if !isValidCatalogStatus(catalog.Status) {
		return common.NewBadRequestError("invalid catalog status", nil)
	}
	if catalog.SLAResponseTime < 0 || catalog.SLAResolutionTime < 0 || catalog.CITypeID < 0 || catalog.CloudServiceID < 0 {
		return common.NewBadRequestError("configuration values cannot be negative", nil)
	}
	if catalog.CloudServiceID > 0 && catalog.CITypeID == 0 {
		return common.NewBadRequestError("cloud service requires CI type", nil)
	}
	exists, err := repo.NameExists(ctx, catalog.TenantID, catalog.Name, catalog.ID)
	if err != nil {
		return err
	}
	if exists {
		return common.NewConflictError("Service catalog name", catalog.Name)
	}
	if err := repo.ValidateReferences(ctx, catalog.TenantID, catalog.CITypeID, catalog.CloudServiceID); err != nil {
		return common.NewBadRequestError("invalid catalog references", err)
	}
	return nil
}

// finishDefinition is shared by all mutations: validate the entire persisted
// candidate and expose exactly its confirmation revision before committing.
func (s *Service) finishDefinition(ctx context.Context, tx *ent.Tx, tenantID, id int) (*ServiceCatalog, error) {
	row, err := tx.ServiceCatalog.Query().Where(servicecatalog.IDEQ(id), servicecatalog.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	revision, defs, _, err := s.projectCreationCatalog(ctx, tx, creation.Identity{TenantID: tenantID}, row)
	if err != nil {
		return nil, err
	}
	result := NewEntRepository(tx.Client()).toDomain(row)
	result.Fields = toFieldDefinitionInputsFromEnt(defs)
	result.AccessPolicy = revision.AccessPolicy
	if row.IsActive && (row.Status == "enabled" || row.Status == "active") {
		if err := s.validateForPublicationTx(ctx, tx, tenantID, result); err != nil {
			return nil, err
		}
	}
	result.CatalogVersion = revision.Version
	result.FormSchemaVersion = revision.FormSchemaVersion
	return result, nil
}

func (s *Service) Get(ctx context.Context, tenantID, id int) (*ServiceCatalog, error) {
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row, err := tx.ServiceCatalog.Query().Where(servicecatalog.TenantIDEQ(tenantID), servicecatalog.IDEQ(id)).Only(ctx)
	if err != nil {
		return nil, err
	}
	result := NewEntRepository(tx.Client()).toDomain(row)
	if err := s.attachRevision(ctx, tx, tenantID, result); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) List(ctx context.Context, tenantID int, filters ListFilters) ([]*ServiceCatalog, int, error) {
	return s.listSnapshot(ctx, tenantID, filters, "", false)
}

func (s *Service) listSnapshot(ctx context.Context, tenantID int, filters ListFilters, keyword string, search bool) ([]*ServiceCatalog, int, error) {
	if filters.Page < 1 {
		filters.Page = 1
	}
	if filters.Size < 1 {
		filters.Size = 20
	}
	if filters.Size > 100 {
		filters.Size = 100
	}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	repo := NewEntRepository(tx.Client())
	var catalogs []*ServiceCatalog
	var total int
	if search {
		catalogs, total, err = repo.Search(ctx, tenantID, keyword, filters)
	} else {
		catalogs, total, err = repo.List(ctx, tenantID, filters)
	}
	if err != nil {
		return nil, 0, err
	}
	for _, catalog := range catalogs {
		if err := s.attachRevision(ctx, tx, tenantID, catalog); err != nil {
			return nil, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return catalogs, total, nil
}

func (s *Service) attachRevision(ctx context.Context, tx *ent.Tx, tenantID int, catalog *ServiceCatalog) error {
	row, err := tx.ServiceCatalog.Query().Where(servicecatalog.IDEQ(catalog.ID), servicecatalog.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return err
	}
	revision, defs, _, err := s.projectCreationCatalog(ctx, tx, creation.Identity{TenantID: tenantID}, row)
	if err != nil {
		return err
	}
	catalog.CatalogVersion = revision.Version
	catalog.FormSchemaVersion = revision.FormSchemaVersion
	catalog.Fields = toFieldDefinitionInputsFromEnt(defs)
	catalog.AccessPolicy = revision.AccessPolicy
	return nil
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

func (s *Service) Update(ctx context.Context, tenantID, id int, input dto.UpdateServiceCatalogRequest) (*ServiceCatalog, error) {
	if input.ExpectedCatalogVersion == "" {
		return nil, creation.NewCatalogVersionConflict("expected catalog version is required", nil)
	}
	if s.client == nil {
		return nil, creation.NewInfrastructureUnavailable("catalog transaction client is required", nil)
	}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Lock the host before projecting the old contract. Concurrent writers wait,
	// then compare against the committed current definition, including its fields.
	row, err := tx.ServiceCatalog.UpdateOneID(id).Where(servicecatalog.TenantIDEQ(tenantID)).SetUpdatedAt(time.Now()).Save(ctx)
	if err != nil {
		return nil, catalogMutationError(err)
	}
	old, defs, _, err := s.projectCreationCatalog(ctx, tx, creation.Identity{TenantID: tenantID}, row)
	if err != nil {
		return nil, err
	}
	if old.Version != input.ExpectedCatalogVersion {
		return nil, creation.NewCatalogVersionConflict("catalog changed; reload and reconfirm", nil)
	}
	repo := NewEntRepository(tx.Client())
	current := repo.toDomain(row)
	current.Fields = toFieldDefinitionInputsFromEnt(defs)
	if input.Name != nil {
		current.Name = strings.TrimSpace(*input.Name)
	}
	if input.Category != nil {
		current.Category = strings.TrimSpace(*input.Category)
	}
	if input.Description != nil {
		current.Description = *input.Description
	}
	if input.DeliveryTime != nil {
		current.DeliveryTime, err = strconv.Atoi(*input.DeliveryTime)
		if err != nil {
			return nil, common.NewBadRequestError("invalid deliveryTime", err)
		}
	}
	if input.Status != nil {
		current.Status = *input.Status
	}
	if input.CITypeID != nil {
		current.CITypeID = *input.CITypeID
	}
	if input.CloudServiceID != nil {
		current.CloudServiceID = *input.CloudServiceID
	}
	if input.ProcessDefinitionKey != nil {
		current.ProcessDefinitionKey = strings.TrimSpace(*input.ProcessDefinitionKey)
	}
	if input.ServiceType != nil {
		current.ServiceType = *input.ServiceType
	}
	if input.TargetClass != nil {
		current.TargetClass = *input.TargetClass
	}
	if input.RequiresApproval != nil {
		current.RequiresApproval = *input.RequiresApproval
	}
	if input.SLAResponseTime != nil {
		current.SLAResponseTime = *input.SLAResponseTime
	}
	if input.SLAResolutionTime != nil {
		current.SLAResolutionTime = *input.SLAResolutionTime
	}
	if err := s.validateDefinition(ctx, repo, current); err != nil {
		return nil, err
	}
	if _, err := repo.Update(ctx, tenantID, current); err != nil {
		return nil, err
	}
	if input.Fields != nil {
		fields, err := catalogFieldInputs(input.Fields)
		if err != nil {
			return nil, common.NewBadRequestError("invalid fields", err)
		}
		if _, err := service.NewFieldDefinitionService(tx.Client()).ReplaceDefinitionsTx(ctx, tx, tenantID, "service_catalog", id, fields); err != nil {
			return nil, common.NewBadRequestError("invalid field definitions", err)
		}
	}
	if err := saveAccessPolicy(ctx, tx, tenantID, id, input.AccessPolicy); err != nil {
		return nil, err
	}
	result, err := s.finishDefinition(ctx, tx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) Delete(ctx context.Context, tenantID, id int, expectedVersion string) error {
	disabled := "disabled"
	_, err := s.Update(ctx, tenantID, id, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: expectedVersion, Status: &disabled})
	return err
}

func (s *Service) Search(ctx context.Context, tenantID int, keyword string, filters ListFilters) ([]*ServiceCatalog, int, error) {
	return s.listSnapshot(ctx, tenantID, filters, strings.TrimSpace(keyword), true)
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

func (s *Service) SetPublicationEngine(engine *service.CustomProcessEngine) {
	s.publicationEngine = engine
	if engine != nil {
		for _, h := range engine.CallbackRegistry().ListHandlers() {
			if kaf, ok := h.(*bpmn.KafDelegateServiceTaskHandler); ok {
				kaf.SetPublicationConfiguration(s)
			}
		}
	}
}

func (s *Service) SetCreatorRegistry(registry interface {
	Get(string) (creation.ProfessionalCreator, error)
}) {
	s.creators = registry
}

func catalogMutationError(err error) error {
	var state interface{ SQLState() string }
	if errors.As(err, &state) && (state.SQLState() == "40001" || state.SQLState() == "40P01") {
		return creation.NewCatalogVersionConflict("catalog changed; reload and reconfirm", err)
	}
	return err
}
