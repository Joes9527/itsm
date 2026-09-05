package service_catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent/enttest"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func TestCatalogDetailConfirmationRevision(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	tenant := client.Tenant.Create().SetName("Catalog reader").SetCode("catalog-reader").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("catalog-reader").SetEmail("reader@example.test").SetPasswordHash("unused").SetName("Reader").SetRole("super_admin").SaveX(ctx)
	catalog := client.ServiceCatalog.Create().SetTenantID(tenant.ID).SetName("VPN").SetTargetClass("service_request_item").SaveX(ctx)
	field := client.FieldDefinition.Create().SetTenantID(tenant.ID).SetEntityType("service_catalog").SetEntityID(catalog.ID).SetName("device_count").SetLabel("Devices").SetFieldType("number").SetRequired(true).SaveX(ctx)
	svc := NewService(NewEntRepository(client), client, zap.NewNop().Sugar())
	identity := creation.Identity{TenantID: tenant.ID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "web"}
	read := func(id int) (int, map[string]any) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("GET", fmt.Sprintf("/service-catalogs/%d", id), nil)
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(id)}}
		c.Set("tenant_id", identity.TenantID)
		c.Set("user_id", identity.ActorID)
		c.Set("role", identity.Role)
		NewHandler(svc).Get(c)
		var response map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		return recorder.Code, response
	}
	status, response := read(catalog.ID)
	require.Equal(t, 200, status)
	data := response["data"].(map[string]any)
	require.NotEmpty(t, data["catalogVersion"], "detail must expose the exact confirmation revision")
	require.NotEmpty(t, data["formSchemaVersion"])
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	resolved, _, err := svc.ResolveCreationCatalog(ctx, tx, identity, catalog.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	require.Equal(t, resolved.Version, data["catalogVersion"])
	require.Equal(t, resolved.FormSchemaVersion, data["formSchemaVersion"])
	client.FieldDefinition.UpdateOneID(field.ID).SetRequired(false).ExecX(ctx)
	_, response = read(catalog.ID)
	changed := response["data"].(map[string]any)
	require.NotEqual(t, data["catalogVersion"], changed["catalogVersion"])
	require.NotEqual(t, data["formSchemaVersion"], changed["formSchemaVersion"])
	foreign := client.ServiceCatalog.Create().SetTenantID(tenant.ID + 1).SetName("Foreign").SaveX(ctx)
	status, _ = read(foreign.ID)
	require.Equal(t, 404, status)
	client.User.UpdateOneID(actor.ID).SetRole("viewer").ExecX(ctx)
	status, _ = read(catalog.ID)
	require.Equal(t, 401, status)
	identity.Role = "viewer"
	status, _ = read(catalog.ID)
	require.Equal(t, 403, status)
}
