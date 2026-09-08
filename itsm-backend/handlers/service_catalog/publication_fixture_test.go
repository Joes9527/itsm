package service_catalog

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	"itsm-backend/service"
	"strconv"
	"testing"
)

func catalogTestFields(fields []service.FieldDefinitionInput) []map[string]interface{} {
	if fields == nil {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(fields))
	for _, f := range fields {
		result = append(result, map[string]interface{}{"name": f.Name, "label": f.Label, "type": f.FieldType, "required": f.Required, "options": f.Options, "sortOrder": f.SortOrder})
	}
	return result
}
func catalogCreateInput(name, category, description string, days int, status string, ci, cloud int, fields []service.FieldDefinitionInput, key, serviceType string) dto.CreateServiceCatalogRequest {
	return dto.CreateServiceCatalogRequest{Name: name, Category: category, Description: description, DeliveryTime: strconv.Itoa(days), Status: status, CITypeID: ci, CloudServiceID: cloud, Fields: catalogTestFields(fields), ProcessDefinitionKey: key, ServiceType: serviceType, TargetClass: "generic"}
}
func catalogUpdateInput(t *testing.T, s *Service, ctx context.Context, tenant, id int, name, category, description string, days int, status string, ci, cloud int, fields []service.FieldDefinitionInput, key, serviceType string) dto.UpdateServiceCatalogRequest {
	t.Helper()
	tx, err := s.client.Tx(ctx)
	require.NoError(t, err)
	row, err := tx.ServiceCatalog.Get(ctx, id)
	require.NoError(t, err)
	revision, _, _, err := s.projectCreationCatalog(ctx, tx, creation.Identity{TenantID: tenant}, row)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	input := dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: revision.Version, Fields: catalogTestFields(fields)}
	if name != "" {
		input.Name = &name
	}
	if category != "" {
		input.Category = &category
	}
	if description != "" {
		input.Description = &description
	}
	if days > 0 {
		v := strconv.Itoa(days)
		input.DeliveryTime = &v
	}
	if status != "" {
		input.Status = &status
	}
	if ci > 0 {
		input.CITypeID = &ci
	}
	if cloud > 0 {
		input.CloudServiceID = &cloud
	}
	if key != "" {
		input.ProcessDefinitionKey = &key
	}
	if serviceType != "" {
		input.ServiceType = &serviceType
	}
	return input
}
func scPtr[T any](v T) *T { return &v }

func newCatalogPublisher(repo Repository, client *ent.Client, logger *zap.SugaredLogger, directory database.DirectorySnapshot) *Service {
	s := NewService(repo, client, logger, directory)
	registry := intake.NewCreatorRegistry()
	if client != nil {
		if err := registry.Register(service.NewTicketServiceForTest(client, logger)); err != nil {
			panic(err)
		}
	}
	s.SetCreatorRegistry(registry)
	return s
}
