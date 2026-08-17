//go:build integration

package service

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"itsm-backend/database"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestCloseChangeApprovalChains_ClosesPendingRows 回归覆盖：CloseChangeApprovalChains
// 是 handlers/change 在变更进入终态时用来收口残留 pending 审批链节点的独立函数
// （ChangeService.UpdateChangeStatus 删除前也共享同一段逻辑）。此前没有任何测试直接
// 覆盖它。本测试锁定其核心行为：只收口指定 change_id + tenant_id 下状态为 pending 的
// 行，不影响已是其他状态的行，也不影响其他 change_id 的 pending 行。
//
// 运行方式（需要真实 Postgres，本地默认跳过）：
//
//	ITSM_TEST_DB='host=127.0.0.1 port=5432 user=itsm_user dbname=itsm sslmode=disable password=dev123' \
//	  go test -tags integration ./service/ -run TestCloseChangeApprovalChains -v
func TestCloseChangeApprovalChains_ClosesPendingRows(t *testing.T) {
	dsn := os.Getenv("ITSM_TEST_DB")
	if dsn == "" {
		t.Skip("set ITSM_TEST_DB (postgres DSN) to run integration tests")
	}

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()

	// 创建裸表（非 Ent 管理，与生产迁移保持一致，字段定义参照原 change_approval_service_test.go）
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS change_approval_chains (
			id BIGSERIAL PRIMARY KEY,
			change_id BIGINT NOT NULL,
			tenant_id BIGINT NOT NULL DEFAULT 0,
			level INT NOT NULL,
			approver_id BIGINT NOT NULL,
			role TEXT DEFAULT 'approver',
			status TEXT DEFAULT 'pending',
			is_required BOOLEAN DEFAULT true,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		);
	`)
	require.NoError(t, err)

	// 注入测试用原始连接：CloseChangeApprovalChains 通过 database.GetRawDB() 读取全局连接。
	prev := database.GetRawDB()
	database.SetRawDBForTest(db)
	t.Cleanup(func() { database.SetRawDBForTest(prev) })

	// 使用不太可能与真实数据冲突的大 ID，并在测试前后清理，保证可重复运行。
	const changeID, otherChangeID, tenantID = 900001, 900002, 900001

	cleanup := func() {
		_, _ = db.ExecContext(context.Background(),
			`DELETE FROM change_approval_chains WHERE change_id IN ($1, $2) AND tenant_id = $3`,
			changeID, otherChangeID, tenantID)
	}
	cleanup()
	t.Cleanup(cleanup)

	// 目标 change：2 条 pending + 1 条已 approved（不应被本函数改动）
	_, err = db.ExecContext(ctx,
		`INSERT INTO change_approval_chains (change_id, tenant_id, level, approver_id, role, status, is_required) VALUES ($1,$2,1,101,'approver','pending',true)`,
		changeID, tenantID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO change_approval_chains (change_id, tenant_id, level, approver_id, role, status, is_required) VALUES ($1,$2,2,102,'approver','pending',true)`,
		changeID, tenantID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO change_approval_chains (change_id, tenant_id, level, approver_id, role, status, is_required) VALUES ($1,$2,3,103,'approver','approved',true)`,
		changeID, tenantID)
	require.NoError(t, err)

	// 另一个 change_id 下也有 pending 行，用于验证收口只作用于目标 change_id/tenant_id
	_, err = db.ExecContext(ctx,
		`INSERT INTO change_approval_chains (change_id, tenant_id, level, approver_id, role, status, is_required) VALUES ($1,$2,1,201,'approver','pending',true)`,
		otherChangeID, tenantID)
	require.NoError(t, err)

	err = CloseChangeApprovalChains(ctx, changeID, tenantID)
	require.NoError(t, err)

	var pendingCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM change_approval_chains WHERE change_id = $1 AND tenant_id = $2 AND status = 'pending'`,
		changeID, tenantID).Scan(&pendingCount))
	require.Equal(t, 0, pendingCount, "目标 change 的两条 pending 行都应被收口为 obsolete")

	var obsoleteCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM change_approval_chains WHERE change_id = $1 AND tenant_id = $2 AND status = 'obsolete'`,
		changeID, tenantID).Scan(&obsoleteCount))
	require.Equal(t, 2, obsoleteCount)

	var approvedStatus string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT status FROM change_approval_chains WHERE change_id = $1 AND tenant_id = $2 AND level = 3`,
		changeID, tenantID).Scan(&approvedStatus))
	require.Equal(t, "approved", approvedStatus, "已是 approved 的行不应被本函数改动")

	var otherPendingCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM change_approval_chains WHERE change_id = $1 AND tenant_id = $2 AND status = 'pending'`,
		otherChangeID, tenantID).Scan(&otherPendingCount))
	require.Equal(t, 1, otherPendingCount, "其他 change_id 的 pending 行不应被本次收口影响")
}
