//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	catalogdomain "itsm-backend/handlers/service_catalog"
	approuter "itsm-backend/router"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPostgresCatalogReaderSignedCurrentSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newIncidentEffectsFixture(t)
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("catalog-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("catalog-operator").SetName("Operator").SetEmail("operator@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("Catalog reader").SaveX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("service_catalog:read").SetName("Catalog read").SetResource("service_catalog").SetAction("read").SaveX(f.ctx)
	link := f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	catalog := f.client.ServiceCatalog.Create().SetTenantID(f.tenant.ID).SetName("VPN").SetTargetClass("service_request_item").SaveX(f.ctx)
	field := f.client.FieldDefinition.Create().SetTenantID(f.tenant.ID).SetEntityType("service_catalog").SetEntityID(catalog.ID).SetName("device_count").SetLabel("Devices").SetFieldType("number").SetRequired(true).SaveX(f.ctx)
	foreign := f.client.ServiceCatalog.Create().SetTenantID(provider.ID).SetName("Foreign secret catalog").SetTargetClass("service_request_item").SaveX(f.ctx)
	nativeRole := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode(f.actor.Role).SetName("Native reader").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(nativeRole.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	clients, cfg := runtimeClients(t, f)
	for _, table := range []string{"service_catalogs", "field_definitions", "process_bindings", "process_definitions", "sla_definitions"} {
		_, err := f.db.ExecContext(f.ctx, "GRANT SELECT ON "+table+" TO "+cfg.User)
		require.NoError(t, err)
	}
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	_, err := clients.Tenant.User.Get(ctx, actor.ID)
	require.True(t, ent.IsNotFound(err))
	logger := zap.NewNop().Sugar()
	const secret = "isolated-catalog-reader-signing-key"
	auth := service.NewAuthService(clients.Tenant, clients.System, secret, logger)
	session, err := auth.SwitchTenant(f.ctx, actor.ID, f.tenant.ID)
	require.NoError(t, err)
	snapshots := &catalogReaderDirectory{directory: clients.IntakeDirectorySnapshot()}
	owner := catalogdomain.NewService(catalogdomain.NewEntRepository(clients.Tenant), clients.Tenant, logger, snapshots)
	engine := gin.New()
	approuter.SetupRoutes(engine, &approuter.RouterConfig{JWTSecret: secret, Logger: logger, Client: clients.Tenant, TenantDirectoryClient: clients.System, ServiceCatalogHandler: catalogdomain.NewHandler(owner)})
	read := func(token string, id int) (int, conversionHTTPEnvelope) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/service-catalogs/%d", id), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		var envelope conversionHTTPEnvelope
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
		require.Zero(t, database.GetRawDB().Stats().InUse, "target transaction released")
		require.Zero(t, clients.SystemDB.Stats().InUse, "directory transaction released")
		return recorder.Code, envelope
	}
	decode := func(response conversionHTTPEnvelope) dto.ServiceCatalogResponse {
		t.Helper()
		var projection dto.ServiceCatalogResponse
		require.NoError(t, json.Unmarshal(response.Data, &projection))
		require.NotEmpty(t, projection.CatalogVersion)
		require.NotEmpty(t, projection.FormSchemaVersion)
		return projection
	}
	status, envelope := read(session.AccessToken, catalog.ID)
	require.Equal(t, http.StatusOK, status, envelope.Message)
	original := decode(envelope)
	require.Equal(t, "VPN", original.Name)
	nativeSession, err := auth.SwitchTenant(f.ctx, f.actor.ID, f.tenant.ID)
	require.NoError(t, err)
	status, envelope = read(nativeSession.AccessToken, catalog.ID)
	require.Equal(t, http.StatusOK, status, envelope.Message)
	require.Equal(t, original, decode(envelope))
	// No target requester is needed; even a supplied native-tenant hint is not authority.
	identity := creation.Identity{TenantID: f.tenant.ID, ActorID: actor.ID, ActorTenantID: f.tenant.ID, Role: "msp_tech"}
	result, err := owner.Read(ctx, identity, catalog.ID)
	require.NoError(t, err)
	require.Equal(t, original.CatalogVersion, result.CatalogVersion)
	require.Equal(t, original.FormSchemaVersion, result.FormSchemaVersion)
	status, envelope = read(session.AccessToken, foreign.ID)
	require.Equal(t, http.StatusNotFound, status, envelope.Message)
	require.NotContains(t, string(envelope.Data), "Foreign secret catalog")
	status, _ = read("invalid-token", catalog.ID)
	require.Equal(t, http.StatusUnauthorized, status)

	for _, test := range []struct {
		name            string
		revoke, restore func()
		want            error
	}{
		{"allocation", func() { f.client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).ExecX(f.ctx) }, func() { f.client.MSPAllocation.UpdateOneID(allocation.ID).ClearDeassignedAt().ExecX(f.ctx) }, creation.ErrPermissionDenied},
		{"inactive_customer", func() { f.client.Tenant.UpdateOneID(f.tenant.ID).SetStatus("suspended").ExecX(f.ctx) }, func() { f.client.Tenant.UpdateOneID(f.tenant.ID).SetStatus("active").ExecX(f.ctx) }, creation.ErrPermissionDenied},
		{"expired_customer", func() { f.client.Tenant.UpdateOneID(f.tenant.ID).SetExpiresAt(time.Now().Add(-time.Hour)).ExecX(f.ctx) }, func() { f.client.Tenant.UpdateOneID(f.tenant.ID).ClearExpiresAt().ExecX(f.ctx) }, creation.ErrPermissionDenied},
		{"inactive_actor", func() { f.client.User.UpdateOneID(actor.ID).SetActive(false).ExecX(f.ctx) }, func() { f.client.User.UpdateOneID(actor.ID).SetActive(true).ExecX(f.ctx) }, creation.ErrAuthenticationRequired},
		{"actor_current_role", func() { f.client.User.UpdateOneID(actor.ID).SetMspRole("provider_admin").ExecX(f.ctx) }, func() { f.client.User.UpdateOneID(actor.ID).SetMspRole("provider_agent").ExecX(f.ctx) }, creation.ErrAuthenticationRequired},
		{"role_inactive", func() { f.client.Role.UpdateOneID(role.ID).SetIsActive(false).ExecX(f.ctx) }, func() { f.client.Role.UpdateOneID(role.ID).SetIsActive(true).ExecX(f.ctx) }, creation.ErrPermissionDenied},
		{"read_permission_removed", func() { f.client.RolePermission.DeleteOneID(link.ID).ExecX(f.ctx) }, func() {
			link = f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
		}, creation.ErrPermissionDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.revoke()
			defer test.restore()
			status, envelope := read(session.AccessToken, catalog.ID)
			require.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, status, envelope.Message)
			// Independently prove the service repeats current checks, even if middleware/cache denies early.
			result, err := owner.Read(ctx, identity, catalog.ID)
			require.Nil(t, result)
			require.ErrorIs(t, err, test.want)
			require.Zero(t, database.GetRawDB().Stats().InUse)
			require.Zero(t, clients.SystemDB.Stats().InUse)
		})
	}
	// Import establishes the same snapshot for identity, RBAC, fields and versions.
	// Mutations committed after import cannot alter the in-flight confirmation.
	snapshots.afterImport = func() {
		f.client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).ExecX(f.ctx)
		f.client.RolePermission.DeleteOneID(link.ID).ExecX(f.ctx)
		f.client.FieldDefinition.UpdateOneID(field.ID).SetRequired(false).ExecX(f.ctx)
		f.client.ServiceCatalog.UpdateOneID(catalog.ID).SetName("VPN revised").ExecX(f.ctx)
	}
	status, envelope = read(session.AccessToken, catalog.ID)
	require.Equal(t, http.StatusOK, status, envelope.Message)
	require.Equal(t, original, decode(envelope))
	status, _ = read(session.AccessToken, catalog.ID)
	require.Equal(t, http.StatusForbidden, status)
	f.client.MSPAllocation.UpdateOneID(allocation.ID).ClearDeassignedAt().ExecX(f.ctx)
	link = f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	status, envelope = read(session.AccessToken, catalog.ID)
	require.Equal(t, http.StatusOK, status, envelope.Message)
	revised := decode(envelope)
	require.Equal(t, "VPN revised", revised.Name)
	require.NotEqual(t, original.CatalogVersion, revised.CatalogVersion)
	require.NotEqual(t, original.FormSchemaVersion, revised.FormSchemaVersion)

	for _, stage := range []string{"export", "import", "close", "lookup"} {
		t.Run(stage, func(t *testing.T) {
			snapshots.failure = stage
			status, envelope := read(session.AccessToken, catalog.ID)
			require.Equal(t, http.StatusServiceUnavailable, status, envelope.Message)
			var failure struct {
				ErrorCode string `json:"errorCode"`
				Retryable bool   `json:"retryable"`
			}
			require.NoError(t, json.Unmarshal(envelope.Data, &failure))
			require.Equal(t, "InfrastructureUnavailable", failure.ErrorCode)
			require.True(t, failure.Retryable)
			require.NotContains(t, string(envelope.Data), "catalogVersion")
			if stage == "export" || stage == "import" {
				require.Error(t, snapshots.databaseError, "real PostgreSQL operation must fail")
			}
			snapshots.failure = ""
		})
	}
	// Missing dependency fails closed even for a same-tenant reader.
	missing := catalogdomain.NewService(nil, clients.Tenant, logger, nil)
	result, err = missing.Read(ctx, creation.Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, Role: f.actor.Role}, catalog.ID)
	require.Nil(t, result)
	require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
	t.Log("registered signed HTTP: native/MSP read-only success, runtime-hidden native actor, revocation and foreign IDs fail closed, one-snapshot versions, directory failures return 503, both pools released")
}

type catalogReaderDirectory struct {
	directory     database.DirectorySnapshot
	afterImport   func()
	failure       string
	databaseError error
}

func (d *catalogReaderDirectory) Open(ctx context.Context, tx *ent.Tx, tenantID int) (*ent.Client, func() error, error) {
	if d.failure == "export" {
		_ = tx.Rollback()
	}
	client, closeDirectory, err := d.directory.Open(ctx, tx, tenantID)
	if err != nil {
		d.databaseError = err
		return nil, nil, err
	}
	if d.failure == "import" {
		// Real PostgreSQL rejects the expired/nonexistent snapshot; release the
		// successfully opened restricted pool before surfacing the import failure.
		_, err = client.ExecContext(ctx, "SET TRANSACTION SNAPSHOT '00000000-00000000-0'")
		d.databaseError = err
		return nil, nil, errors.Join(err, closeDirectory())
	}
	if d.afterImport != nil {
		run := d.afterImport
		d.afterImport = nil
		run()
	}
	if d.failure == "lookup" {
		_ = closeDirectory()
		return client, func() error { return nil }, nil
	}
	if d.failure == "close" {
		return client, func() error { return errors.Join(closeDirectory(), errors.New("injected directory close failure")) }, nil
	}
	return client, closeDirectory, nil
}
