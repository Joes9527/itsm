//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/migration"
	"itsm-backend/service/workitemcutover"
)

// C1 的只读切换预检必须在隔离库上被证明：活跃旧依赖使切换失败（exit 2 语义），
// 规范记录允许切换（exit 0 语义），而预检本身绝不改动任何一行。
type cutoverFixture struct {
	db         *sql.DB
	scopedDB   *sql.DB
	client     *ent.Client
	ctx        context.Context
	tenant     *ent.Tenant
	actor      *ent.User
	definition *ent.ProcessDefinition
}

func newCutoverFixture(t *testing.T) *cutoverFixture {
	t.Helper()
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	require.NotEmpty(t, dsn, "explicit disposable DB required")
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "/sslvpn_test", parsed.Path)
	require.Equal(t, "127.0.0.1:36444", parsed.Host)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	schema := fmt.Sprintf("c1_workitem_cutover_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
		var remaining int
		require.NoError(t, db.QueryRowContext(context.Background(), "SELECT count(*) FROM pg_namespace WHERE nspname=$1", schema).Scan(&remaining))
		require.Zero(t, remaining)
		t.Logf("isolated schema %s removed; remaining=%d", schema, remaining)
	})

	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	client, err := ent.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	scopedDB, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scopedDB.Close()) })
	require.NoError(t, migration.NewMigrator(scopedDB, zap.NewNop().Sugar()).EnsureMigrationsTable(ctx))
	require.NoError(t, client.Schema.Create(ctx))

	tenant := client.Tenant.Create().SetCode("cutover").SetName("cutover").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("cutover-actor").SetName("actor").
		SetEmail("cutover@example.test").SetPasswordHash("test").SetRole("agent").SetActive(true).SaveX(ctx)
	deployment := client.ProcessDeployment.Create().SetTenantID(tenant.ID).
		SetDeploymentID("cutover_deployment").SetDeploymentName("cutover").SaveX(ctx)
	definition := client.ProcessDefinition.Create().SetTenantID(tenant.ID).
		SetKey("cutover_definition").SetName("cutover").SetDeploymentID(deployment.ID).
		SetBpmnXML([]byte("<bpmn/>")).SaveX(ctx)
	return &cutoverFixture{db: db, scopedDB: scopedDB, client: client, ctx: ctx, tenant: tenant, actor: actor, definition: definition}
}

// inspect runs the preflight the same way the CLI does: inside a read-only transaction,
// so any accidental write would fail the call outright.
func (f *cutoverFixture) inspect(t *testing.T) workitemcutover.Report {
	t.Helper()
	tx, err := f.client.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback()) }()
	rep, err := workitemcutover.Inspect(f.ctx, tx, f.tenant.ID)
	require.NoError(t, err)
	return rep
}

func (f *cutoverFixture) instance(t *testing.T, businessType, businessKey string, businessID int, status string) *ent.ProcessInstance {
	t.Helper()
	return f.client.ProcessInstance.Create().
		SetTenantID(f.tenant.ID).
		SetProcessInstanceID(fmt.Sprintf("pi-%s-%d-%d", businessType, businessID, time.Now().UnixNano())).
		SetProcessDefinitionKey(f.definition.Key).
		SetProcessDefinitionID(f.definition.ID).
		SetBusinessType(businessType).
		SetBusinessKey(businessKey).
		SetBusinessID(businessID).
		SetStatus(status).
		SaveX(f.ctx)
}

func (f *cutoverFixture) pendingCallback(t *testing.T, instance *ent.ProcessInstance) *ent.ProcessCallbackOutbox {
	t.Helper()
	return f.client.ProcessCallbackOutbox.Create().
		SetTenantID(f.tenant.ID).
		SetExecutionKey(fmt.Sprintf("exec-%d-%d", instance.ID, time.Now().UnixNano())).
		SetProcessInstanceID(instance.ID).
		SetCallbackKind("service_task").
		SetHandlerID("cutover_handler").
		SetTaskType("service_task").
		SetElementID("task_1").
		SetStatus("pending").
		SaveX(f.ctx)
}

func (f *cutoverFixture) binding(t *testing.T, businessType string, active bool) *ent.ProcessBinding {
	t.Helper()
	return f.client.ProcessBinding.Create().
		SetTenantID(f.tenant.ID).
		SetBusinessType(businessType).
		SetProcessDefinitionKey("cutover_definition").
		SetProcessVersion(1).
		SetIsActive(active).
		SaveX(f.ctx)
}

// canonicalChange creates a canonical identity: WorkItem with record_class
// change_request plus its professional extension, and returns the WorkItem ID.
func (f *cutoverFixture) canonicalChange(t *testing.T, number string) int {
	t.Helper()
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).
		SetTitle("cutover").SetTicketNumber(number).SetRecordClass(dto.RecordClassChangeRequest).
		SetStatus("new").SetPriority("medium").SaveX(f.ctx)
	f.client.Change.Create().SetWorkItemID(item.ID).SaveX(f.ctx)
	return item.ID
}

// databaseDigest is the pre/post summary the cutover gate requires: counts plus a
// content hash, so an UPDATE (not just an INSERT or DELETE) would be visible.
func (f *cutoverFixture) databaseDigest(t *testing.T) string {
	t.Helper()
	queries := []string{
		`SELECT count(*)::text || ':' || coalesce(md5(string_agg(id::text || '|' || coalesce(business_type,'') || '|' || coalesce(business_key,'') || '|' || coalesce(business_id::text,'') || '|' || coalesce(status,''), ',' ORDER BY id)), '') FROM process_instances`,
		`SELECT count(*)::text || ':' || coalesce(md5(string_agg(id::text || '|' || coalesce(status,'') || '|' || coalesce(attempt_count::text,''), ',' ORDER BY id)), '') FROM process_callback_outboxes`,
		`SELECT count(*)::text || ':' || coalesce(md5(string_agg(id::text || '|' || coalesce(business_type,'') || '|' || coalesce(is_active::text,''), ',' ORDER BY id)), '') FROM process_bindings`,
	}
	digest := ""
	for _, query := range queries {
		var part string
		require.NoError(t, f.scopedDB.QueryRow(query).Scan(&part))
		digest += part + "|"
	}
	return digest
}

func blockerKinds(rep workitemcutover.Report) []string {
	kinds := make([]string, 0, len(rep.Blockers))
	for _, blocker := range rep.Blockers {
		kinds = append(kinds, blocker.Kind)
	}
	return kinds
}

func TestWorkItemCutoverBlocksOnActiveLegacyInstance(t *testing.T) {
	f := newCutoverFixture(t)
	f.instance(t, "change", "change:1", 1, "running")

	before := f.databaseDigest(t)
	rep := f.inspect(t)
	after := f.databaseDigest(t)

	require.False(t, rep.Switchable, "a running legacy instance must block the cutover")
	require.Contains(t, blockerKinds(rep), "active_legacy_instances")
	require.Equal(t, before, after, "the preflight must not change a single row")
}

func TestWorkItemCutoverBlocksOnSuspendedLegacyInstance(t *testing.T) {
	f := newCutoverFixture(t)
	f.instance(t, "ticket", "ticket:9", 9, "suspended")

	rep := f.inspect(t)

	require.False(t, rep.Switchable)
	require.Contains(t, blockerKinds(rep), "active_legacy_instances")
}

func TestWorkItemCutoverBlocksOnPendingLegacyCallback(t *testing.T) {
	f := newCutoverFixture(t)
	// Finished instance: only the outstanding callback keeps the cutover blocked.
	instance := f.instance(t, "service_request", "service_request:3", 3, "completed")
	f.pendingCallback(t, instance)

	before := f.databaseDigest(t)
	rep := f.inspect(t)
	after := f.databaseDigest(t)

	require.False(t, rep.Switchable, "an outstanding legacy callback must block the cutover")
	require.Contains(t, blockerKinds(rep), "pending_legacy_callbacks")
	require.Equal(t, before, after)
}

func TestWorkItemCutoverBlocksOnActiveLegacyBinding(t *testing.T) {
	f := newCutoverFixture(t)
	f.binding(t, "change", true)

	rep := f.inspect(t)

	require.False(t, rep.Switchable, "an active legacy binding must block the cutover")
	require.Contains(t, blockerKinds(rep), "active_legacy_bindings")
}

func TestWorkItemCutoverAllowsCanonicalRecords(t *testing.T) {
	f := newCutoverFixture(t)
	workItemID := f.canonicalChange(t, "CHG-CUTOVER-1")
	key, err := dto.WorkItemBusinessKey(dto.RecordClassChangeRequest, workItemID)
	require.NoError(t, err)
	f.instance(t, dto.RecordClassChangeRequest, key, workItemID, "running")

	before := f.databaseDigest(t)
	rep := f.inspect(t)
	after := f.databaseDigest(t)

	require.True(t, rep.Switchable, "canonical identity must be switchable, blockers=%v", blockerKinds(rep))
	require.Empty(t, rep.Blockers)
	require.Equal(t, before, after)
}

func TestWorkItemCutoverBlocksOnLegacyKeyForCanonicalType(t *testing.T) {
	f := newCutoverFixture(t)
	workItemID := f.canonicalChange(t, "CHG-CUTOVER-2")
	// Canonical class but the Wave-1 key vocabulary: a matching string is not identity.
	f.instance(t, dto.RecordClassChangeRequest, fmt.Sprintf("change:%d", workItemID), workItemID, "completed")

	rep := f.inspect(t)

	require.False(t, rep.Switchable)
	require.Contains(t, blockerKinds(rep), "identity_mismatch_instances")
}

func TestWorkItemCutoverBlocksWhenProfessionalExtensionIsMissing(t *testing.T) {
	f := newCutoverFixture(t)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).
		SetTitle("cutover").SetTicketNumber("CHG-CUTOVER-3").SetRecordClass(dto.RecordClassChangeRequest).
		SetStatus("new").SetPriority("medium").SaveX(f.ctx)
	key, err := dto.WorkItemBusinessKey(dto.RecordClassChangeRequest, item.ID)
	require.NoError(t, err)
	// No Change extension row: the identity cannot be proven.
	f.instance(t, dto.RecordClassChangeRequest, key, item.ID, "completed")

	rep := f.inspect(t)

	require.False(t, rep.Switchable)
	require.Contains(t, blockerKinds(rep), "identity_mismatch_instances")
}

// History is not migrated: a finished legacy instance is reported, not blocked.
func TestWorkItemCutoverReportsHistoricalLegacyAsInformational(t *testing.T) {
	f := newCutoverFixture(t)
	f.instance(t, "ticket", "ticket:77", 77, "completed")

	rep := f.inspect(t)

	require.True(t, rep.Switchable, "finished legacy instances belong to history, blockers=%v", blockerKinds(rep))
	require.Empty(t, rep.Blockers)
	require.Len(t, rep.Informational, 1)
	require.Equal(t, "historical_legacy_instances", rep.Informational[0].Kind)
}

// Release keeps its explicit legacy identity and is neither a target nor a blocker.
func TestWorkItemCutoverIgnoresReleaseIdentity(t *testing.T) {
	f := newCutoverFixture(t)
	f.instance(t, workitemcutover.ReleaseBusinessType, "release:5", 5, "running")
	f.binding(t, workitemcutover.ReleaseBusinessType, true)

	rep := f.inspect(t)

	require.True(t, rep.Switchable, "release is not a WorkItem convergence blocker, blockers=%v", blockerKinds(rep))
}

func TestWorkItemCutoverReportsEveryBlockerTogether(t *testing.T) {
	f := newCutoverFixture(t)
	f.instance(t, "change", "change:1", 1, "running")
	f.instance(t, "ticket", "ticket:2", 2, "completed")
	f.pendingCallback(t, f.instance(t, "ticket", "ticket:2", 2, "completed"))
	f.binding(t, "service_request", true)

	rep := f.inspect(t)

	require.False(t, rep.Switchable)
	require.ElementsMatch(t, []string{"active_legacy_instances", "pending_legacy_callbacks", "active_legacy_bindings"}, blockerKinds(rep))
}

// 截断必须失败关闭：部分扫描"没发现阻塞"不等于"没有阻塞"。否则预检会给出它本该防止的
// 虚假绿色结论——对实例数超过上限的租户恰好最危险。
func TestWorkItemCutoverFailsClosedWhenScanIsTruncated(t *testing.T) {
	f := newCutoverFixture(t)
	// 已结束的旧实例本身只是信息性报告；上限降到 1 后扫描必然截断，结论就必须不可用。
	f.instance(t, "ticket", "ticket:1", 1, "completed")
	f.instance(t, "ticket", "ticket:2", 2, "completed")

	previous := workitemcutover.ScanLimit
	workitemcutover.ScanLimit = 1
	t.Cleanup(func() { workitemcutover.ScanLimit = previous })

	rep := f.inspect(t)

	require.False(t, rep.Switchable, "截断的扫描不得报告可切换，blockers=%v", blockerKinds(rep))
	require.Contains(t, blockerKinds(rep), "inconclusive_truncated_scan")
}
