//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/rolepermission"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	"itsm-backend/migration"
	"itsm-backend/service"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"
)

// Uses only the explicitly supplied disposable database, with per-test schema
// and restricted LOGIN roles removed at cleanup. No fixed shared development DB.
func TestPostgresRLSWorkItemReferences(t *testing.T) {
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	require.NotEmpty(t, dsn, "explicit disposable DB required")
	connection, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	parsed := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(connection.Host, strconv.Itoa(int(connection.Port))), Path: "/" + connection.Database, User: url.UserPassword(connection.User, connection.Password)}
	params := url.Values{}
	params.Set("sslmode", "disable")
	parsed.RawQuery = params.Encode()
	ctx := context.Background()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	suffix := fmt.Sprint(time.Now().UnixNano())
	schema := "reference_" + suffix
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); require.NoError(t, err) })
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	owner, err := ent.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Close()) })
	require.NoError(t, owner.Schema.Create(ctx))
	scoped, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scoped.Close()) })
	_, err = scoped.ExecContext(ctx, migration.GetMigrationSQL("009_enable_rls_tenant_isolation"))
	require.NoError(t, err)
	runtimeRole, systemRole := "reference_app_"+suffix, "reference_system_"+suffix
	for _, spec := range []struct{ name, attribute string }{{runtimeRole, "NOBYPASSRLS"}, {systemRole, "BYPASSRLS"}} {
		_, err = admin.ExecContext(ctx, "CREATE ROLE "+spec.name+" LOGIN PASSWORD 'fixture-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT "+spec.attribute)
		require.NoError(t, err)
		role := spec.name
		t.Cleanup(func() {
			_, err := admin.ExecContext(ctx, "DROP OWNED BY "+role)
			require.NoError(t, err)
			_, err = admin.ExecContext(ctx, "DROP ROLE "+role)
			require.NoError(t, err)
		})
		_, err = admin.ExecContext(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role)
		require.NoError(t, err)
	}
	for _, table := range []string{"tickets", "users", "tenants", "roles", "permissions", "role_permissions"} {
		_, err = scoped.ExecContext(ctx, "GRANT SELECT ON "+table+" TO "+runtimeRole)
		require.NoError(t, err)
	}
	for _, grant := range []string{"SELECT ON users,tenants,msp_allocations,external_identities,connector_configs,process_callback_outboxes", "SELECT,UPDATE ON outbox_events,ticket_notifications", "INSERT,SELECT(id) ON audit_logs", "USAGE ON SEQUENCE audit_logs_id_seq"} {
		_, err = scoped.ExecContext(ctx, "GRANT "+grant+" TO "+systemRole)
		require.NoError(t, err)
	}
	port, err := strconv.Atoi(parsed.Port())
	require.NoError(t, err)
	cfg := config.DatabaseConfig{Host: parsed.Hostname(), Port: port, DBName: parsed.Path[1:], SSLMode: "disable", Schema: schema, User: runtimeRole, Password: "fixture-only", SystemRoleUser: systemRole, SystemRolePassword: "fixture-only"}
	clients, err := database.InitRuntimeDatabases(&cfg, &config.RLSConfig{Mode: "enforce"}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, clients.Close()) })
	var super, bypass bool
	require.NoError(t, database.GetRawDB().QueryRowContext(ctx, "SELECT rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&super, &bypass))
	require.False(t, super)
	require.False(t, bypass)
	tenant := owner.Tenant.Create().SetName("Reference").SetCode("reference").SaveX(ctx)
	second := owner.Tenant.Create().SetName("Other").SetCode("other").SaveX(ctx)
	actor := owner.User.Create().SetTenantID(tenant.ID).SetUsername("requester").SetName("Requester").SetEmail("requester@example.test").SetPasswordHash("test").SetRole("requester").SaveX(ctx)
	other := owner.User.Create().SetTenantID(tenant.ID).SetUsername("other").SetName("Other").SetEmail("other@example.test").SetPasswordHash("test").SetRole("requester").SaveX(ctx)
	role := owner.Role.Create().SetTenantID(tenant.ID).SetName("Requester").SetCode("requester").SaveX(ctx)
	permission := owner.Permission.Create().SetTenantID(tenant.ID).SetName("Ticket read").SetCode("ticket-read").SetResource("ticket").SetAction("read").SaveX(ctx)
	owner.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
	seed := func(tenantID, requesterID int, number, status string) {
		owner.Ticket.Create().SetTenantID(tenantID).SetRequesterID(requesterID).SetTitle("private").SetTicketNumber(number).SetStatus(status).SaveX(ctx)
	}
	seed(tenant.ID, actor.ID, "REQ-000037", "closed")
	seed(second.ID, actor.ID, "REQ-000037", "open")
	seed(tenant.ID, other.ID, "OTHER-OWNER", "open")
	seed(tenant.ID, actor.ID, "ACTIVE-1", "open")
	seed(tenant.ID, actor.ID, "DONE", "closed")
	seed(tenant.ID, actor.ID, "ACTIVE-2", "pending")
	read := intake.NewReadService(authorization.NewSessionReader(clients.Tenant, clients.IntakeDirectorySnapshot()), nil, "cursor-secret", intake.ReferenceReadOptions{FrontendURL: "https://support.example.test", PageSize: 1, Lifecycle: intake.NewRequesterLifecycleReader(map[string]authorization.WorkItemLifecycleReader{"ticket": &service.TicketService{}})})
	identity := creation.Identity{ActorID: actor.ID, RequesterID: actor.ID, TenantID: tenant.ID, Role: "requester"}
	scopedCtx := tenantctx.WithTenantID(ctx, tenant.ID)
	exact, err := read.ReferenceByNumber(scopedCtx, identity, "REQ-000037")
	require.NoError(t, err)
	require.Equal(t, "closed", exact.Status)
	_, err = read.ReferenceByNumber(scopedCtx, identity, "OTHER-OWNER")
	require.ErrorIs(t, err, creation.ErrReferenceNotFound)
	page, err := read.UnfinishedReferences(scopedCtx, identity, "")
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "ACTIVE-1", page.Items[0].Number)
	require.NotNil(t, page.NextCursor)
	next, err := read.UnfinishedReferences(scopedCtx, identity, *page.NextCursor)
	require.NoError(t, err)
	require.Len(t, next.Items, 1)
	require.Equal(t, "ACTIVE-2", next.Items[0].Number)
	require.Nil(t, next.NextCursor)
	require.Equal(t, 5, clients.Tenant.Ticket.Query().CountX(scopedCtx), "real RLS excludes the identically numbered other tenant row")
	owner.RolePermission.Delete().Where(rolepermission.TenantIDEQ(tenant.ID)).ExecX(ctx)
	_, err = read.ReferenceByNumber(scopedCtx, identity, "REQ-000037")
	require.ErrorIs(t, err, creation.ErrReferenceNotFound)
	next, err = read.UnfinishedReferences(scopedCtx, identity, *page.NextCursor)
	require.NoError(t, err)
	require.Empty(t, next.Items)
	_, err = scoped.ExecContext(ctx, "REVOKE SELECT ON tickets FROM "+runtimeRole)
	require.NoError(t, err)
	_, err = read.UnfinishedReferences(scopedCtx, identity, "")
	require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
	require.Zero(t, database.GetRawDB().Stats().InUse)
}
