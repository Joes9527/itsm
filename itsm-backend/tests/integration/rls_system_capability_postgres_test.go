//go:build integration_postgres

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/connector/builtin/webhook"
	"itsm-backend/controller"
	"itsm-backend/database"
)

// Real LOGIN identities exercise the same factory used by API and KAF startup.
// The explicit DSN check and schema/migration owner are supplied by the fixture.
func runtimeClients(t *testing.T, f *incidentEffectsFixture) (*database.RuntimeClients, config.DatabaseConfig) {
	t.Helper()
	var schema string
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT current_schema()").Scan(&schema))
	suffix := fmt.Sprint(time.Now().UnixNano())
	runtimeRole, systemRole := "entry_app_"+suffix, "entry_system_"+suffix
	for _, spec := range []struct{ name, attributes string }{{runtimeRole, "NOBYPASSRLS"}, {systemRole, "BYPASSRLS"}} {
		_, err := f.db.ExecContext(f.ctx, "CREATE ROLE "+spec.name+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT "+spec.attributes)
		require.NoError(t, err)
		role := spec.name
		t.Cleanup(func() {
			_, err := f.db.ExecContext(context.Background(), "DROP OWNED BY "+role)
			require.NoError(t, err)
			_, err = f.db.ExecContext(context.Background(), "DROP ROLE "+role)
			require.NoError(t, err)
			var remaining int
			require.NoError(t, f.db.QueryRowContext(context.Background(), "SELECT count(*) FROM pg_roles WHERE rolname=$1", role).Scan(&remaining))
			require.Zero(t, remaining)
			t.Logf("isolated LOGIN role %s removed; remaining=%d", role, remaining)
		})
		_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role)
		require.NoError(t, err)
	}
	// Grant only tables exercised by the actual Incident consumer and auth role
	// permission lookup, with only their sequences; never grant ALL TABLES.
	for _, table := range []string{"tickets", "incidents", "incident_rules", "incident_rule_executions", "incident_rule_action_receipts", "incident_metrics", "incident_alerts", "notifications", "ticket_notifications", "outbox_events", "audit_logs", "intake_requests", "users", "tenants", "roles", "permissions", "role_permissions", "incident_events", "ticket_categories"} {
		_, err := f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+schema+"."+table+" TO "+runtimeRole)
		require.NoError(t, err)
		if table == "role_permissions" {
			continue
		}
		var sequence *string
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", schema+"."+table).Scan(&sequence))
		if sequence != nil {
			_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*sequence+" TO "+runtimeRole)
			require.NoError(t, err)
		}
	}
	for _, grant := range []string{"SELECT ON users,tenants,msp_allocations,connector_configs", "SELECT,UPDATE ON outbox_events,ticket_notifications", "INSERT,SELECT(id) ON audit_logs", "USAGE ON SEQUENCE audit_logs_id_seq"} {
		_, err := f.db.ExecContext(f.ctx, "GRANT "+grant+" TO "+systemRole)
		require.NoError(t, err)
	}
	cfg := config.DatabaseConfig{Host: "127.0.0.1", Port: 36444, DBName: "sslvpn_test", SSLMode: "disable", Schema: schema, User: runtimeRole, SystemRoleUser: systemRole}
	clients, err := database.InitRuntimeDatabases(&cfg, &config.RLSConfig{Mode: "enforce"}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, clients.Close()) })
	for _, pool := range []struct {
		label      string
		wantBypass bool
	}{{"runtime", false}, {"system", true}} {
		db := database.GetRawDB()
		if pool.wantBypass {
			db = clients.SystemDB
		}
		var actual string
		var super, bypass bool
		require.NoError(t, db.QueryRowContext(f.ctx, "SELECT current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&actual, &super, &bypass))
		require.False(t, super)
		require.Equal(t, pool.wantBypass, bypass)
		t.Logf("%s actual current_user=%s super=%v bypass=%v", pool.label, actual, super, bypass)
	}
	return clients, cfg
}

func TestPostgresRLSSystemCapabilityConstruction(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	clients, cfg := runtimeClients(t, f)
	_, err := clients.System.Ticket.Query().Count(f.ctx)
	require.ErrorContains(t, err, "permission denied")
	_, err = clients.System.Incident.Query().Count(f.ctx)
	require.ErrorContains(t, err, "permission denied")
	_, err = clients.System.User.UpdateOneID(f.actor.ID).SetName("forbidden").Save(f.ctx)
	require.ErrorContains(t, err, "permission denied")
	_, err = clients.SystemDB.ExecContext(f.ctx, "SELECT request_body FROM audit_logs")
	require.ErrorContains(t, err, "permission denied")
	_, err = clients.Tenant.User.Query().Count(f.ctx)
	require.Error(t, err, "missing tenant cannot inherit system pool")
	require.Equal(t, 1, clients.System.User.Query().CountX(f.ctx))
	require.Equal(t, 1, clients.Tenant.User.Query().CountX(tenantctx.WithTenantID(f.ctx, f.tenant.ID)))
	require.Zero(t, clients.Tenant.User.Query().CountX(tenantctx.WithTenantID(f.ctx, f.tenant.ID+1)))
	var protected int
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname IN ('users','outbox_events') AND c.relrowsecurity AND c.relowner<>(SELECT oid FROM pg_roles WHERE rolname=$1)", cfg.User).Scan(&protected))
	require.Equal(t, 2, protected, "registered 009 protects both credential and queue tables owned by the distinct migration role")
	var unscoped int
	require.NoError(t, database.GetRawDB().QueryRowContext(f.ctx, "SELECT count(*) FROM users").Scan(&unscoped))
	require.Zero(t, unscoped, "raw runtime role has no implicit cross-tenant lookup capability")
	missing := cfg
	missing.SystemRoleUser = ""
	_, err = database.InitRuntimeDatabases(&missing, &config.RLSConfig{Mode: "enforce"}, nil)
	require.ErrorContains(t, err, "DB_SYSTEM_ROLE_USER")
	broad := cfg
	broad.SystemRoleUser = "postgres"
	_, err = database.InitRuntimeDatabases(&broad, &config.RLSConfig{Mode: "enforce"}, nil)
	require.ErrorContains(t, err, "NOSUPERUSER")
	_, err = f.db.ExecContext(f.ctx, "GRANT SELECT ON tickets TO "+cfg.SystemRoleUser)
	require.NoError(t, err)
	_, err = database.InitRuntimeDatabases(&cfg, &config.RLSConfig{Mode: "enforce"}, nil)
	require.ErrorContains(t, err, "tickets SELECT")
	_, err = f.db.ExecContext(f.ctx, "REVOKE SELECT ON tickets FROM "+cfg.SystemRoleUser)
	require.NoError(t, err)
	// Column-only and sequence privileges must not widen the capability either.
	for _, privilege := range []struct{ grant, revoke, diagnostic string }{
		{"SELECT(title) ON tickets", "SELECT(title) ON tickets", "tickets SELECT"},
		{"SELECT(request_body) ON audit_logs", "SELECT(request_body) ON audit_logs", "SELECT(id) only"},
		{"USAGE ON SEQUENCE tickets_id_seq", "USAGE ON SEQUENCE tickets_id_seq", "sequence privileges"},
	} {
		_, err = f.db.ExecContext(f.ctx, "GRANT "+privilege.grant+" TO "+cfg.SystemRoleUser)
		require.NoError(t, err)
		_, err = database.InitRuntimeDatabases(&cfg, &config.RLSConfig{Mode: "enforce"}, nil)
		require.ErrorContains(t, err, privilege.diagnostic)
		_, err = f.db.ExecContext(f.ctx, "REVOKE "+privilege.revoke+" FROM "+cfg.SystemRoleUser)
		require.NoError(t, err)
	}
	_, err = f.db.ExecContext(f.ctx, "REVOKE SELECT ON users FROM "+cfg.SystemRoleUser)
	require.NoError(t, err)
	_, err = database.InitRuntimeDatabases(&cfg, &config.RLSConfig{Mode: "enforce"}, nil)
	require.ErrorContains(t, err, "users SELECT")
	_, err = f.db.ExecContext(f.ctx, "GRANT SELECT ON users TO "+cfg.SystemRoleUser)
	require.NoError(t, err)
	// A non-bypass table owner still bypasses non-FORCE policies. Reject it.
	_, err = f.db.ExecContext(f.ctx, "ALTER TABLE tickets OWNER TO "+cfg.User)
	require.NoError(t, err)
	_, err = database.InitRuntimeDatabases(&cfg, &config.RLSConfig{Mode: "enforce"}, nil)
	require.ErrorContains(t, err, "non-owner")
	_, err = f.db.ExecContext(f.ctx, "ALTER TABLE tickets OWNER TO postgres")
	require.NoError(t, err)

}

func TestPostgresRLSRuntimeConnectorRestore(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	other := f.client.Tenant.Create().SetCode("connector-other").SetName("connector-other").SaveX(f.ctx)
	for _, id := range []int{f.tenant.ID, other.ID} {
		f.client.ConnectorConfig.Create().SetTenantID(id).SetName("webhook").SetProvider("generic").SetEnabled(true).SetSettings(`{"url":"http://127.0.0.1:1/not-called"}`).SaveX(f.ctx)
	}
	clients, _ := runtimeClients(t, f)
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return webhook.New() })
	manager := connector.NewManager(registry, zap.NewNop().Sugar())
	owner := controller.NewConnectorController(manager, registry, nil, zap.NewNop().Sugar(), clients.Tenant, clients.System)
	require.NoError(t, owner.LoadAll(tenantctx.SystemContext(f.ctx, "test:restore", "restore persisted connectors")))
	for _, id := range []int{f.tenant.ID, other.ID} {
		_, ok := manager.Get(id, "webhook")
		require.True(t, ok)
	}
	_, err := clients.System.ConnectorConfig.Delete().Exec(f.ctx)
	require.ErrorContains(t, err, "permission denied")
}
