package service_catalog

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"testing"
	"time"
)

func TestPublicationFieldFailureRollsBackCatalog(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	svc := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), nil)
	_, err := svc.Create(ctx, 1, catalogCreateInput("Rollback", "IT", "", 1, "disabled", 0, 0, []service.FieldDefinitionInput{{Name: "duplicate", Label: "One", FieldType: "text"}, {Name: "duplicate", Label: "Two", FieldType: "text"}}, "", ""))
	require.Error(t, err)
	require.Zero(t, client.ServiceCatalog.Query().CountX(ctx), "failed field save must not leave a catalog")
}

func TestPublicationRevisionPublicContract(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	catalog := client.ServiceCatalog.Create().SetTenantID(1).SetName("Catalog").SetTargetClass("generic").SaveX(ctx)
	field := client.FieldDefinition.Create().SetTenantID(1).SetEntityType("service_catalog").SetEntityID(catalog.ID).SetName("choice").SetLabel("Choice").SetFieldType("select").SetOptions([]interface{}{"b", "a"}).SaveX(ctx)
	svc := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), nil)
	revision := func() string {
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		row, err := tx.ServiceCatalog.Get(ctx, catalog.ID)
		require.NoError(t, err)
		result, _, _, err := svc.projectCreationCatalog(ctx, tx, creation.Identity{TenantID: 1}, row)
		require.NoError(t, err)
		return result.Version
	}
	before := revision()
	client.ServiceCatalog.UpdateOneID(catalog.ID).SetIcon("new-icon").SetSortOrder(17).SetUpdatedAt(time.Now().Add(time.Hour)).ExecX(ctx)
	client.FieldDefinition.UpdateOneID(field.ID).SetOptions([]interface{}{"a", "b"}).ExecX(ctx)
	require.Equal(t, before, revision(), "incidental display fields and option storage order cannot invalidate confirmation")
	client.FieldDefinition.UpdateOneID(field.ID).SetRequired(true).ExecX(ctx)
	require.NotEqual(t, before, revision(), "a required form field changes the confirmed contract")
}

func TestPublicationPreservesExplicitZeroFieldOrder(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	svc := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), nil)
	result, err := svc.Create(ctx, 1, dto.CreateServiceCatalogRequest{Name: "Ordering", Category: "IT", Fields: []map[string]interface{}{
		{"name": "later", "label": "Later", "type": "text", "sortOrder": 5},
		{"name": "first", "label": "First", "type": "text", "sortOrder": 0},
	}})
	require.NoError(t, err)
	require.Equal(t, "first", result.Fields[0].Name)
	require.Zero(t, result.Fields[0].SortOrder)
}
