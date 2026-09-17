//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/migration"
	"itsm-backend/service"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// CTI 结构治理的真实 PostgreSQL 证据。目标指纹与身份证明由 migrationEntryTarget 的
// 显式 DSN 校验提供（默认 127.0.0.1:36444/sslvpn_test）；未配置 DSN 时测试失败，
// 不会以 skip 冒充通过。
type ctiStructureFixture struct {
	schema string
	db     *sql.DB
	client *ent.Client
	ctx    context.Context
}

func newCTIStructureFixture(t *testing.T) *ctiStructureFixture {
	t.Helper()
	parsed := migrationEntryTarget(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	bootstrap, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, bootstrap.Close()) })

	schema := fmt.Sprintf("cti_structure_%d", time.Now().UnixNano())
	_, err = bootstrap.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := bootstrap.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
		var remaining int
		require.NoError(t, bootstrap.QueryRowContext(context.Background(), "SELECT count(*) FROM pg_namespace WHERE nspname=$1", schema).Scan(&remaining))
		require.Zero(t, remaining)
		t.Logf("isolated CTI schema %s removed; remaining=%d", schema, remaining)
	})

	scopedURL := withSearchPath(parsed, schema)
	scoped, err := sql.Open("postgres", scopedURL.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scoped.Close()) })

	client, err := ent.Open("postgres", scopedURL.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, migration.NewMigrator(scoped, zap.NewNop().Sugar()).EnsureMigrationsTable(ctx))
	require.NoError(t, client.Schema.Create(ctx))
	_, err = scoped.ExecContext(ctx, migration.GetMigrationSQL("009_enable_rls_tenant_isolation"))
	require.NoError(t, err)
	_, err = scoped.ExecContext(ctx, migration.GetMigrationSQL(migration.CTIGovernanceVersion))
	require.NoError(t, err)

	var current string
	require.NoError(t, scoped.QueryRowContext(ctx, "SELECT current_schema()").Scan(&current))
	require.Equal(t, schema, current, "DDL must target the isolated schema")

	return &ctiStructureFixture{schema: schema, db: scoped, client: client, ctx: ctx}
}

func withSearchPath(parsed *url.URL, schema string) url.URL {
	scoped := *parsed
	query := scoped.Query()
	query.Set("search_path", schema)
	scoped.RawQuery = query.Encode()
	return scoped
}

func (f *ctiStructureFixture) tenant(t *testing.T, code string) int {
	t.Helper()
	return f.client.Tenant.Create().SetCode(code).SetName(code).SaveX(f.ctx).ID
}

func (f *ctiStructureFixture) category(t *testing.T, svc *service.TicketCategoryService, tenantID, parentID int, code string) int {
	t.Helper()
	record, err := svc.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parentID, IsActive: true, TenantID: tenantID})
	require.NoError(t, err)
	return record.ID
}

// TestCTIStructurePostgres 覆盖租户隔离、三级约束、引用保护与迁移原子性。
func TestCTIStructurePostgres(t *testing.T) {
	f := newCTIStructureFixture(t)
	svc := service.NewTicketCategoryService(f.client)
	tenantA := f.tenant(t, "cti-pg-a")
	tenantB := f.tenant(t, "cti-pg-b")

	t.Run("code_uniqueness_is_tenant_scoped", func(t *testing.T) {
		a := f.category(t, svc, tenantA, 0, "shared")
		b := f.category(t, svc, tenantB, 0, "shared")
		require.NotEqual(t, a, b)
		_, err := svc.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: "dup", Code: "shared", TenantID: tenantA})
		require.ErrorContains(t, err, "分类代码已存在")
		// 全局唯一索引必须已被迁移移除，否则第二个租户无法使用同一编码。
		var legacy int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=$1 AND indexname='ticketcategory_code'`, f.schema).Scan(&legacy))
		require.Zero(t, legacy)
		var scoped int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=$1 AND indexname='ticketcategory_tenant_id_code'`, f.schema).Scan(&scoped))
		require.Equal(t, 1, scoped)
	})

	t.Run("three_level_limit_is_enforced", func(t *testing.T) {
		l1 := f.category(t, svc, tenantA, 0, "limit-l1")
		l2 := f.category(t, svc, tenantA, l1, "limit-l2")
		l3 := f.category(t, svc, tenantA, l2, "limit-l3")
		_, err := svc.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: "l4", Code: "limit-l4", ParentID: l3, TenantID: tenantA})
		require.ErrorIs(t, err, service.ErrCTIPathTooDeep)

		target := f.category(t, svc, tenantA, 0, "limit-target")
		moving := f.category(t, svc, tenantA, 0, "limit-moving")
		f.category(t, svc, tenantA, moving, "limit-moving-l2")
		parent := target
		_, err = svc.MoveCategory(f.ctx, moving, &service.MoveCategoryRequest{NewParentID: &parent}, tenantA)
		require.ErrorIs(t, err, service.ErrCTIPathTooDeep)
	})

	t.Run("string_reference_blocks_delete", func(t *testing.T) {
		category := f.category(t, svc, tenantA, 0, "legacy-name-ref")
		node := f.client.TicketCategory.GetX(f.ctx, category)
		f.client.IncidentEscalationRule.Create().SetTenantID(tenantA).SetName("legacy").SetTriggerType("sla_breach").
			SetTriggerMinutes(30).SetTargetAssigneeType("group").SetCategoryMatch(node.Name).SaveX(f.ctx)
		require.ErrorIs(t, svc.DeleteCategory(f.ctx, category, tenantA), service.ErrCTICategoryReferenced)
	})

	t.Run("concurrent_work_item_creation_and_category_removal_serialize_on_the_row", func(t *testing.T) {
		victim := f.category(t, svc, tenantA, 0, "race-victim")
		requester := f.client.User.Create().SetTenantID(tenantA).SetUsername("cti-racer").SetName("Racer").
			SetRole("agent").SetEmail("racer@example.test").SetPasswordHash("x").SaveX(f.ctx)

		start := make(chan struct{})
		var ready sync.WaitGroup
		ready.Add(2)
		created := make(chan error, 1)
		removed := make(chan error, 1)
		go func() {
			ready.Done()
			<-start
			// 工单创建：在同一事务内解析并锁定路径，然后写入最深节点。
			created <- svcWithTransaction(f, func(tx *ent.Tx) error {
				if _, err := svc.ResolveCTIPath(f.ctx, tx, tenantA, victim, false, true); err != nil {
					return err
				}
				_, err := tx.Ticket.Create().SetTenantID(tenantA).SetRequesterID(requester.ID).
					SetTitle("race").SetTicketNumber("CTI-RACE-1").SetStatus("open").SetRecordClass("generic").
					SetCategoryID(victim).Save(f.ctx)
				return err
			})
		}()
		go func() {
			ready.Done()
			<-start
			removed <- svc.DeleteCategory(f.ctx, victim, tenantA)
		}()
		ready.Wait()
		close(start)
		createErr, deleteErr := <-created, <-removed

		// 两者在同一行锁上串行化：先建单则删除被引用保护拒绝，先删除则创建解析失败。
		if createErr == nil {
			require.ErrorIs(t, deleteErr, service.ErrCTICategoryReferenced)
		} else {
			require.NoError(t, deleteErr)
		}

		// 最终状态必须自洽：不存在引用已删除分类的工单。
		var dangling int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `
			SELECT count(*) FROM tickets t
			WHERE t.tenant_id=$1 AND t.category_id IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM ticket_categories c WHERE c.id = t.category_id)`, tenantA).Scan(&dangling))
		require.Zero(t, dangling, "no committed WorkItem may reference a removed category")
	})

	t.Run("migration_preflight_failure_rolls_back_without_touching_ledger", func(t *testing.T) {
		// 还原 048 之前的形态：旧的全表唯一索引存在、结构列不存在。
		_, err := f.db.ExecContext(f.ctx, `DROP INDEX IF EXISTS ticketcategory_tenant_id_code`)
		require.NoError(t, err)
		_, err = f.db.ExecContext(f.ctx, `CREATE UNIQUE INDEX IF NOT EXISTS ticketcategory_code ON ticket_categories (code)`)
		require.NoError(t, err)
		_, err = f.db.ExecContext(f.ctx, `ALTER TABLE service_catalogs DROP CONSTRAINT IF EXISTS service_catalogs_ticket_categories_default_catalogs`)
		require.NoError(t, err)
		_, err = f.db.ExecContext(f.ctx, `ALTER TABLE service_catalogs DROP COLUMN IF EXISTS default_ticket_category_id`)
		require.NoError(t, err)

		// 造出超三级的历史脏数据：迁移必须预检失败，而不是截断数据。
		l1 := f.category(t, svc, tenantA, 0, "deep-l1")
		l2 := f.category(t, svc, tenantA, l1, "deep-l2")
		l3 := f.category(t, svc, tenantA, l2, "deep-l3")
		_, err = f.db.ExecContext(f.ctx, `INSERT INTO ticket_categories (name, code, level, sort_order, is_active, tenant_id, parent_id, created_at, updated_at)
			VALUES ('deep-l4', 'deep-l4', 4, 0, true, $1, $2, now(), now())`, tenantA, l3)
		require.NoError(t, err)

		tx, err := f.db.BeginTx(f.ctx, nil)
		require.NoError(t, err)
		_, execErr := tx.ExecContext(f.ctx, migration.GetMigrationSQL(migration.CTIGovernanceVersion))
		require.ErrorContains(t, execErr, "CTI preflight")
		require.NoError(t, tx.Rollback())

		// 失败后必须是完整旧形态：没有半写入的新结构，也没有丢列。
		var index int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=$1 AND indexname='ticketcategory_code'`, f.schema).Scan(&index))
		require.Equal(t, 1, index, "legacy index must survive a failed migration")
		var scoped int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=$1 AND indexname='ticketcategory_tenant_id_code'`, f.schema).Scan(&scoped))
		require.Zero(t, scoped)
		var column int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='service_catalogs' AND column_name='default_ticket_category_id'`, f.schema).Scan(&column))
		require.Zero(t, column)
		var receipts int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM schema_migrations WHERE version=$1`, migration.CTIGovernanceVersion).Scan(&receipts))
		require.Zero(t, receipts, "a failed migration must not write a ledger receipt")

		// 清理脏数据后同一迁移必须成功，证明失败来自预检而不是脚本损坏。
		_, err = f.db.ExecContext(f.ctx, `DELETE FROM ticket_categories WHERE code='deep-l4'`)
		require.NoError(t, err)
		_, err = f.db.ExecContext(f.ctx, migration.GetMigrationSQL(migration.CTIGovernanceVersion))
		require.NoError(t, err)
		var restored int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=$1 AND indexname='ticketcategory_tenant_id_code'`, f.schema).Scan(&restored))
		require.Equal(t, 1, restored)
	})

	// 诊断输出必须证明真实执行身份与目标，便于审阅者核对不是 skip。
	var identity string
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT current_user").Scan(&identity))
	t.Logf("CTI structure PostgreSQL evidence: schema=%s user=%s tenant_scope=two-tenant", f.schema, identity)
}

func svcWithTransaction(f *ctiStructureFixture, fn func(tx *ent.Tx) error) error {
	tx, err := f.client.Tx(f.ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
