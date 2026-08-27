package schema_test

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/workitemrelation"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestWorkItemRelation_UniqueConstraint(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:workitemrelation?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	_, err := client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("related_to").
		SetCreatedByID(1).
		Save(ctx)
	require.NoError(t, err)

	// Same (tenant, source, target, relation_type) tuple must be rejected.
	_, err = client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("related_to").
		SetCreatedByID(1).
		Save(ctx)
	require.Error(t, err)

	// A different relation_type between the same two WorkItems is allowed.
	_, err = client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("duplicate_of").
		SetCreatedByID(1).
		Save(ctx)
	require.NoError(t, err)
}

// TestWorkItemRelation_RelinkAfterSoftDelete 锁定唯一索引必须是"只约束未软删除行"的
// 部分索引：解除关联（软删除，写 deleted_at）之后再把同一对 WorkItem 用同一
// relation_type 关联回来，必须能成功。
//
// 若唯一索引退回全表覆盖，软删除行会一直占着那个唯一键，重新关联永久报唯一冲突——
// 而关系行是纯链接记录，用户在 UI 上"取消关联 → 再关联"是完全正常的操作。
func TestWorkItemRelation_RelinkAfterSoftDelete(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:workitemrelation_relink?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	first, err := client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("investigated_by").
		SetCreatedByID(1).
		Save(ctx)
	require.NoError(t, err)

	// 解除关联 = 软删除。
	_, err = client.WorkItemRelation.UpdateOneID(first.ID).
		SetDeletedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// 重新关联同一对 WorkItem、同一 relation_type。
	second, err := client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("investigated_by").
		SetCreatedByID(1).
		Save(ctx)
	require.NoError(t, err, "软删除后重新建立同一条关系必须成功；唯一索引应带 WHERE deleted_at IS NULL")
	require.NotEqual(t, first.ID, second.ID)

	// 但两条"活着"的同键关系仍然必须被拒绝——部分索引不能把唯一性整个放开。
	_, err = client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("investigated_by").
		SetCreatedByID(1).
		Save(ctx)
	require.Error(t, err, "未软删除的重复关系仍然必须被唯一约束拒绝")

	alive, err := client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(1),
			workitemrelation.SourceWorkItemID(10),
			workitemrelation.TargetWorkItemID(20),
			workitemrelation.RelationType("investigated_by"),
			workitemrelation.DeletedAtIsNil(),
		).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, alive, 1, "任一时刻同键只允许存在一条未软删除的关系")
	require.Equal(t, second.ID, alive[0].ID)
}
