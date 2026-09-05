//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"itsm-backend/authentication"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/database/rls"
	"itsm-backend/ent"
	authcommon "itsm-backend/handlers/common"
	"itsm-backend/middleware"
	"itsm-backend/service"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// The consumer must use the real configured Ent decorator under a non-bypass role.
// The distinct privileged repository owns only cross-tenant transport polling/ack.
func TestPostgresRLSRuntimeConsumer(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	f.rule(metricAction("scoped"), map[string]interface{}{"type": "escalate", "level": 1, "reason": "runtime policy", "notify_users": []int{f.actor.ID}})
	clients, _ := runtimeClients(t, f)
	client := clients.Tenant
	db := database.GetRawDB()
	owner := service.NewIncidentService(client, zap.NewNop().Sugar())
	owner.SetAlertCreator(service.NewIncidentAlertingService(client, zap.NewNop().Sugar()))
	registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{owner.RuleEngine()}, "incident_alert_delivery")
	require.NoError(t, err)
	worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(clients.System), service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 10 * time.Second, MaxAttempts: 5}, zap.NewNop().Sugar(), registry)
	require.NoError(t, err)
	require.NoError(t, worker.DispatchOnce(f.ctx))
	require.Equal(t, "published", f.client.OutboxEvent.GetX(f.ctx, f.event.ID).Status, f.client.OutboxEvent.GetX(f.ctx, f.event.ID).LastError)
	require.Equal(t, 1, f.client.IncidentMetric.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.IncidentAlert.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.Notification.Query().CountX(f.ctx))
	require.Equal(t, 2, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
	require.NoError(t, worker.DispatchOnce(f.ctx))
	require.Equal(t, 1, f.client.IncidentMetric.Query().CountX(f.ctx))
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID+1)
	require.Zero(t, client.IncidentRuleActionReceipt.Query().CountX(ctx))
	_, err = client.IncidentRuleActionReceipt.Create().SetTenantID(f.tenant.ID).SetExecutionID(f.client.IncidentRuleExecution.Query().Where().FirstX(f.ctx).ID).SetActionIndex(0).Save(ctx)
	require.Error(t, err)
	require.Equal(t, 0, db.Stats().InUse)
	// Simulate a corrupted stored transport identity, bypassing Ent immutability.
	_, err = f.db.ExecContext(f.ctx, "UPDATE outbox_events SET tenant_id=$1,status='pending' WHERE id=$2", f.tenant.ID+1, f.event.ID)
	require.NoError(t, err)
	require.NoError(t, worker.DispatchOnce(f.ctx))
	require.Equal(t, "blocked", f.client.OutboxEvent.GetX(f.ctx, f.event.ID).Status, "mismatched durable tenant must fail closed through the actual consumer")
	require.Equal(t, 1, f.client.IncidentMetric.Query().CountX(f.ctx))
}

func runtimeRLSDriver(t *testing.T, f *incidentEffectsFixture) (*rls.Driver, *sql.DB) {
	t.Helper()
	var schema string
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT current_schema()").Scan(&schema))
	role := fmt.Sprintf("entry_rls_%d", time.Now().UnixNano())
	_, err := f.db.ExecContext(f.ctx, "CREATE ROLE "+role+" NOLOGIN NOSUPERUSER NOBYPASSRLS")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := f.db.ExecContext(context.Background(), "DROP OWNED BY "+role)
		require.NoError(t, err)
		_, err = f.db.ExecContext(context.Background(), "DROP ROLE "+role)
		require.NoError(t, err)
		var remaining int
		require.NoError(t, f.db.QueryRowContext(context.Background(), "SELECT count(*) FROM pg_roles WHERE rolname=$1", role).Scan(&remaining))
		require.Zero(t, remaining)
		t.Logf("isolated role %s removed", role)
	})
	_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role)
	require.NoError(t, err)
	for _, table := range []string{"incident_rule_executions", "incident_rule_action_receipts", "incident_rules", "tickets", "users", "incidents", "ticket_attachments", "outbox_events", "intake_requests", "variable_probe", "rls_probe"} {
		var exists bool
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT to_regclass($1) IS NOT NULL", schema+"."+table).Scan(&exists))
		if !exists {
			continue
		} // Optional probe tables are created only by their owning test.
		_, err = f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+schema+"."+table+" TO "+role)
		require.NoError(t, err)
		var sequence *string
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", schema+"."+table).Scan(&sequence))
		if sequence != nil {
			_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*sequence+" TO "+role)
			require.NoError(t, err)
		}
	}

	db, err := sql.Open("postgres", "host=127.0.0.1 port=36444 user=postgres dbname=sslvpn_test sslmode=disable search_path="+schema+" role="+role)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var super, bypass bool
	require.NoError(t, db.QueryRowContext(f.ctx, "SELECT rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&super, &bypass))
	require.False(t, super)
	require.False(t, bypass)
	t.Logf("runtime role %s: super=%v bypass=%v", role, super, bypass)
	return rls.From(entsql.OpenDB("postgres", db), "enforce", zap.NewNop().Sugar()), db
}

func TestPostgresRLSRuntimeStatementsAndTransactions(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	// A minimal table isolates the actual driver boundary from domain validation.
	_, err := f.db.ExecContext(f.ctx, `CREATE TABLE rls_probe(id bigint PRIMARY KEY, tenant_id bigint NOT NULL); INSERT INTO rls_probe VALUES(1,1),(2,2); ALTER TABLE rls_probe ENABLE ROW LEVEL SECURITY; ALTER TABLE rls_probe FORCE ROW LEVEL SECURITY; CREATE POLICY tenant ON rls_probe USING(tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint) WITH CHECK(tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint)`)
	require.NoError(t, err)
	drv, db := runtimeRLSDriver(t, f)
	one := tenantctx.WithTenantID(f.ctx, 1)
	two := tenantctx.WithTenantID(f.ctx, 2)
	for _, ctx := range []context.Context{one, two, one} {
		rows := &entsql.Rows{}
		require.NoError(t, drv.Query(ctx, "SELECT tenant_id FROM rls_probe", []any{}, rows))
		require.True(t, rows.Next())
		var tenant int
		require.NoError(t, rows.Scan(&tenant))
		want, _ := tenantctx.TenantID(ctx)
		require.Equal(t, want, tenant)
		require.False(t, rows.Next())
		require.NoError(t, rows.Close())
	}
	require.Error(t, drv.Exec(f.ctx, "DELETE FROM rls_probe", []any{}, nil))
	require.Error(t, drv.Query(f.ctx, "SELECT * FROM rls_probe", []any{}, &entsql.Rows{}))
	_, err = drv.Tx(f.ctx)
	require.Error(t, err)
	for _, tenant := range []int{0, -1} {
		require.Error(t, drv.Exec(tenantctx.WithTenantID(f.ctx, tenant), "DELETE FROM rls_probe", []any{}, nil))
	}
	require.ErrorContains(t, drv.Exec(one, "INSERT INTO rls_probe VALUES(3,2)", []any{}, nil), "row-level security")
	require.NoError(t, drv.Exec(one, "INSERT INTO rls_probe VALUES(3,1)", []any{}, nil))
	tx, err := drv.BeginTx(one, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	require.NoError(t, tx.Exec(one, "INSERT INTO rls_probe VALUES(4,1)", []any{}, nil))
	require.Error(t, tx.Exec(two, "DELETE FROM rls_probe", []any{}, nil), "transaction cannot change tenant")
	require.NoError(t, tx.Rollback())
	tx, err = drv.Tx(two)
	require.NoError(t, err)
	require.NoError(t, tx.Exec(two, "INSERT INTO rls_probe VALUES(5,2)", []any{}, nil))
	require.NoError(t, tx.Commit())
	// INSERT RETURNING retains ordinary autocommit semantics and scanner metadata.
	returned := &entsql.Rows{}
	require.NoError(t, drv.Query(two, "INSERT INTO rls_probe VALUES(6,2) RETURNING id", []any{}, returned))
	columns, err := returned.Columns()
	require.NoError(t, err)
	require.Equal(t, []string{"id"}, columns)
	require.True(t, returned.Next())
	var inserted int
	require.NoError(t, returned.Scan(&inserted))
	require.Equal(t, 6, inserted)
	require.NoError(t, returned.Close())
	// Preserve multiple result sets instead of closing at the end of the first.
	sets := &entsql.Rows{}
	require.NoError(t, drv.Query(one, "SELECT 1; SELECT 2", []any{}, sets))
	require.True(t, sets.Next())
	require.NoError(t, sets.Scan(&inserted))
	require.Equal(t, 1, inserted)
	require.False(t, sets.Next())
	require.True(t, sets.NextResultSet())
	require.True(t, sets.Next())
	require.NoError(t, sets.Scan(&inserted))
	require.Equal(t, 2, inserted)
	require.NoError(t, sets.Close())
	running, cancelQuery := context.WithCancel(one)
	defer cancelQuery()
	queryResult := make(chan error, 1)
	const sleepingQuery = "SELECT pg_sleep(5) /* rls-runtime-cancellation */"
	go func() {
		rows := &entsql.Rows{}
		err := drv.Query(running, sleepingQuery, []any{}, rows)
		if err == nil {
			err = rows.Close()
		}
		queryResult <- err
	}()
	require.Eventually(t, func() bool {
		var active bool
		err := f.db.QueryRowContext(f.ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query=$1 AND state='active')", sleepingQuery).Scan(&active)
		return err == nil && active
	}, 3*time.Second, 10*time.Millisecond, "cancel only after the real SQL is executing")
	cancelQuery()
	require.Error(t, <-queryResult)
	txCtx, cancelTx := context.WithCancel(one)
	cancelledTx, err := drv.Tx(txCtx)
	require.NoError(t, err)
	require.NoError(t, cancelledTx.Exec(txCtx, "INSERT INTO rls_probe VALUES(7,1)", []any{}, nil))
	cancelTx()
	require.Error(t, cancelledTx.Commit())
	var cancelledWrites int
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT count(*) FROM rls_probe WHERE id=7").Scan(&cancelledWrites))
	require.Zero(t, cancelledWrites)
	openCtx, cancelRows := context.WithCancel(one)
	openRows := &entsql.Rows{}
	require.NoError(t, drv.Query(openCtx, "SELECT * FROM rls_probe", []any{}, openRows))
	cancelRows()
	require.NoError(t, openRows.Close())
	// A statement returning an execution error must not commit its write.
	require.Error(t, drv.Query(one, "INSERT INTO rls_probe VALUES(8,1) RETURNING 1/0", []any{}, &entsql.Rows{}))
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT count(*) FROM rls_probe WHERE id=8").Scan(&cancelledWrites))
	require.Zero(t, cancelledWrites)
	// SQL errors and cancellation must release the only pool connection and its GUC.
	require.Error(t, drv.Query(one, "SELECT no_such_column FROM rls_probe", []any{}, &entsql.Rows{}))
	cancelled, cancel := context.WithCancel(one)
	cancel()
	require.Error(t, drv.Exec(cancelled, "SELECT 1", []any{}, nil))
	rows := &entsql.Rows{}
	require.NoError(t, drv.Query(two, "SELECT tenant_id FROM rls_probe ORDER BY id", []any{}, rows))
	require.True(t, rows.Next())
	require.NoError(t, rows.Close())
	require.Equal(t, 0, db.Stats().InUse)
	var guc string
	require.NoError(t, db.QueryRowContext(f.ctx, "SELECT COALESCE(current_setting('app.current_tenant',true),'')").Scan(&guc))
	require.Empty(t, guc)
	var privileged bool
	require.NoError(t, db.QueryRowContext(f.ctx, "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&privileged))
	require.False(t, privileged, "pool cleanup must preserve the configured non-bypass role")
}

func TestPostgresRLSRuntimeAuthenticationAndTenantLookup(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	hashed, err := bcrypt.GenerateFromPassword([]byte("runtime-auth"), bcrypt.MinCost)
	require.NoError(t, err)
	f.actor.Update().SetPasswordHash(string(hashed)).SaveX(f.ctx)
	clients, _ := runtimeClients(t, f)
	client := clients.Tenant
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	refresh := authentication.NewRefreshTokenConsumer("runtime-secret", authentication.NewRedisRefreshTokenStore(redisClient))
	svc := authcommon.NewService(authcommon.NewEntRepository(client), "runtime-secret", zap.NewNop().Sugar(), clients.System, refresh)
	result, err := svc.Login(f.ctx, f.actor.Username, "runtime-auth", 0, f.tenant.Code)
	require.NoError(t, err)
	require.Equal(t, f.actor.ID, result.User.ID)
	rotated, err := svc.RefreshToken(f.ctx, result.RefreshToken)
	require.NoError(t, err)
	require.Equal(t, f.actor.ID, rotated.User.ID)
	_, err = svc.RefreshToken(f.ctx, result.RefreshToken)
	require.Error(t, err, "refresh replay is rejected through real Redis consumer")
	_, err = svc.Login(f.ctx, f.actor.Username, "wrong-password", 0, f.tenant.Code)
	require.Error(t, err)
	_, err = svc.Login(f.ctx, "unknown-user", "wrong-password", 0, "")
	require.Error(t, err)
	require.Equal(t, 3, f.client.AuditLog.Query().CountX(f.ctx), "success and both resolved/unresolved authentication failures persist under 009")
	tokens, err := authentication.IssueSessionTokens(authentication.SessionIdentity{UserID: f.actor.ID, Username: f.actor.Username, Role: "agent", TenantID: f.tenant.ID}, "runtime-secret")
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/tenant", middleware.AuthMiddleware("runtime-secret"), middleware.TenantMiddleware(clients.System), func(c *gin.Context) {
		require.False(t, tenantctx.IsSystemBypass(c.Request.Context()))
		id, ok := tenantctx.TenantID(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, f.tenant.ID, id)
		c.Status(204)
	})
	request := httptest.NewRequest("GET", "/tenant", nil)
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	require.Equal(t, 204, response.Code)
}

func TestPostgresRLSRuntimeMSPSession(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	provider := f.tenant.Update().SetType("msp_provider").SaveX(f.ctx)
	actor := f.actor.Update().SetMspRole("provider_agent").SaveX(f.ctx)
	customer := f.client.Tenant.Create().SetCode("customer").SetName("customer").SetType("msp_customer").SaveX(f.ctx)
	other := f.client.Tenant.Create().SetCode("other").SetName("other").SetType("msp_customer").SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(customer.ID).SaveX(f.ctx)
	clients, _ := runtimeClients(t, f)
	svc := service.NewAuthService(clients.Tenant, clients.System, "msp-runtime", zap.NewNop().Sugar())
	selected, err := svc.SwitchTenant(tenantctx.WithTenantID(f.ctx, provider.ID), actor.ID, customer.ID)
	require.NoError(t, err)
	require.Equal(t, customer.ID, selected.User.TenantID)
	_, err = svc.SwitchTenant(f.ctx, actor.ID, other.ID)
	require.Error(t, err)
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	common := authcommon.NewService(authcommon.NewEntRepository(clients.Tenant), "msp-runtime", zap.NewNop().Sugar(), clients.System, authentication.NewRefreshTokenConsumer("msp-runtime", authentication.NewRedisRefreshTokenStore(redisClient)))
	refreshed, err := common.RefreshToken(f.ctx, selected.RefreshToken)
	require.NoError(t, err)
	require.Equal(t, customer.ID, refreshed.User.TenantID)
	allocation.Update().SetDeassignedAt(time.Now()).SaveX(f.ctx)
	_, err = svc.SwitchTenant(f.ctx, actor.ID, customer.ID)
	require.Error(t, err)
	_, err = common.RefreshToken(f.ctx, refreshed.RefreshToken)
	require.Error(t, err, "revoked MSP allocation invalidates session refresh")
}

func TestPostgresRLSRuntimeRejectsPrivilegedTenantRole(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	drv := rls.From(entsql.OpenDB("postgres", f.db), "enforce", zap.NewNop().Sugar())
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	require.Error(t, drv.Exec(ctx, "SELECT 1", []any{}, nil))
	_, err := drv.Tx(ctx)
	require.Error(t, err)
	// Explicit server-owned migration access is still available through the configured privileged pool.
	require.NoError(t, drv.Exec(tenantctx.WithSystemBypass(f.ctx), "CREATE TABLE system_probe(id bigint)", []any{}, nil))
	require.Error(t, ent.NewClient(ent.Driver(drv)).Schema.Create(tenantctx.WithSystemBypass(f.ctx)), "Atlas background inspection must not implicitly bypass the tenant driver")
	// RunInitialization uses this ordinary Ent SQL driver with migration credentials.
	migrationClient := ent.NewClient(ent.Driver(entsql.OpenDB("postgres", f.db)))
	require.NoError(t, migrationClient.Schema.Create(tenantctx.WithSystemBypass(f.ctx)), "the dedicated migration client supports actual Atlas bootstrap")
}

func TestPostgresRLSRuntimeKAFTransport(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	clients, _ := runtimeClients(t, f)
	queue := clients.System
	event := f.client.OutboxEvent.Create().SetTenantID(f.tenant.ID).SetEventID("runtime-kaf").SetEventType(service.KafDelegateRequestedEventType).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(f.inc.WorkItemID)).SetPayload([]byte(`{"task":"scoped"}`)).SaveX(f.ctx)
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	dispatcher, err := service.NewKafOutboxDispatcher(service.NewOutboxEventRepository(queue), service.KafOutboxConfig{WebhookURL: server.URL, WebhookSecret: "runtime-test", BatchSize: 10, PollInterval: time.Second, MaxAttempts: 5})
	require.NoError(t, err)
	require.NoError(t, dispatcher.DispatchOnce(f.ctx))
	require.NoError(t, dispatcher.DispatchOnce(f.ctx))
	require.Equal(t, int32(1), sent.Load())
	require.Equal(t, "published", f.client.OutboxEvent.GetX(f.ctx, event.ID).Status)
}

func TestPostgresRLSRuntimeEvictsFailedConnection(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	drv, db := runtimeRLSDriver(t, f)
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	rows := &entsql.Rows{}
	require.NoError(t, drv.Query(ctx, "SELECT pg_backend_pid()", []any{}, rows))
	require.True(t, rows.Next())
	var firstPID int
	require.NoError(t, rows.Scan(&firstPID))
	var terminated bool
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_terminate_backend($1)", firstPID).Scan(&terminated))
	require.True(t, terminated)
	require.Error(t, rows.Close(), "cleanup of a terminated backend must not return its dirty connection to the pool")
	next := &entsql.Rows{}
	require.NoError(t, drv.Query(ctx, "SELECT pg_backend_pid(),current_setting('app.current_tenant')", []any{}, next))
	require.True(t, next.Next())
	var nextPID int
	var tenant string
	require.NoError(t, next.Scan(&nextPID, &tenant))
	require.NotEqual(t, firstPID, nextPID)
	require.Equal(t, fmt.Sprint(f.tenant.ID), tenant)
	require.NoError(t, next.Close())
	require.Equal(t, 0, db.Stats().InUse)
}
