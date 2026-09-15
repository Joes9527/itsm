package service_catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
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
	svc := NewService(NewEntRepository(client), client, zap.NewNop().Sugar(), sameTransactionDirectory{})
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

// SQLite fixtures cannot export PostgreSQL snapshots; use the same transaction
// explicitly. Production receives only the RuntimeClients directory capability.
type sameTransactionDirectory struct{}

func (sameTransactionDirectory) Open(_ context.Context, tx *ent.Tx, _ int) (*ent.Client, func() error, error) {
	return tx.Client(), func() error { return nil }, nil
}

type catalogReadDirectoryFixture struct {
	openError     error
	closeError    error
	missingClient bool
	missingClose  bool
	closes        int
}

func (d *catalogReadDirectoryFixture) Open(_ context.Context, tx *ent.Tx, _ int) (*ent.Client, func() error, error) {
	client := tx.Client()
	if d.missingClient {
		client = nil
	}
	var closeDirectory func() error = func() error { d.closes++; return d.closeError }
	if d.missingClose {
		closeDirectory = nil
	}
	return client, closeDirectory, d.openError
}

func TestCatalogReadDirectoryFailuresAreInfrastructure(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	tenant := client.Tenant.Create().SetName("Reader failures").SetCode("reader-failures").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("reader-failures").SetEmail("reader-failures@example.test").SetPasswordHash("unused").SetName("Reader").SetRole("super_admin").SaveX(ctx)
	catalog := client.ServiceCatalog.Create().SetTenantID(tenant.ID).SetName("VPN").SetTargetClass("service_request_item").SaveX(ctx)
	// Requester and channel are creation inputs, not prerequisites for a read.
	identity := creation.Identity{TenantID: tenant.ID, ActorID: actor.ID, Role: actor.Role}
	unavailable := errors.New("directory unavailable")
	for _, scenario := range []struct {
		name      string
		directory *catalogReadDirectoryFixture
		role      string
		wantClose int
	}{
		{"open_with_cleanup", &catalogReadDirectoryFixture{openError: unavailable, closeError: unavailable}, actor.Role, 1},
		{"missing_client", &catalogReadDirectoryFixture{missingClient: true, closeError: unavailable}, actor.Role, 1},
		{"missing_close", &catalogReadDirectoryFixture{missingClose: true}, actor.Role, 0},
		{"close_failure", &catalogReadDirectoryFixture{closeError: unavailable}, actor.Role, 1},
		{"close_failure_overrides_role_denial", &catalogReadDirectoryFixture{closeError: unavailable}, "revoked-role", 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reader := NewService(nil, client, zap.NewNop().Sugar(), scenario.directory)
			current := identity
			current.Role = scenario.role
			result, err := reader.Read(ctx, current, catalog.ID)
			require.Nil(t, result)
			require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
			require.Equal(t, scenario.wantClose, scenario.directory.closes)
		})
	}
	reader := NewService(nil, client, zap.NewNop().Sugar(), sameTransactionDirectory{})
	result, err := reader.Read(ctx, identity, catalog.ID)
	require.NoError(t, err)
	require.NotEmpty(t, result.CatalogVersion)
	missing := NewService(nil, client, zap.NewNop().Sugar(), nil)
	result, err = missing.Read(ctx, identity, catalog.ID)
	require.Nil(t, result)
	require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
}
