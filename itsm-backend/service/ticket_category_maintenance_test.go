package service

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// newCTITestFixture 打开一个隔离的 sqlite Ent client，并准备两个租户。
// 真实的行锁/RLS 语义由 tests/integration 的 PostgreSQL 用例覆盖；这里验证业务不变量。
func newCTITestFixture(t *testing.T) (*ent.Client, *TicketCategoryService, int, int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:cti_governance?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	tenant := client.Tenant.Create().SetCode("cti-a").SetName("CTI A").SaveX(t.Context())
	other := client.Tenant.Create().SetCode("cti-b").SetName("CTI B").SaveX(t.Context())
	return client, NewTicketCategoryService(client), tenant.ID, other.ID
}

// ctiClientFixture 只需要单租户时使用。
func ctiClientFixture(t *testing.T) (*ent.Client, *TicketCategoryService, int) {
	t.Helper()
	client, service, tenant, _ := newCTITestFixture(t)
	return client, service, tenant
}

func createCTI(t *testing.T, svc *TicketCategoryService, tenantID, parentID int, code string) int {
	t.Helper()
	record, err := svc.CreateCategory(t.Context(), &CreateCategoryRequest{
		Name:     code,
		Code:     code,
		ParentID: parentID,
		IsActive: true,
		TenantID: tenantID,
	})
	require.NoError(t, err)
	return record.ID
}

// withCTITx 提供一个回滚事务，用于验证依赖调用方事务的解析契约。
func withCTITx(t *testing.T, client *ent.Client, fn func(tx *ent.Tx)) {
	t.Helper()
	tx, err := client.Tx(t.Context())
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback()) }()
	fn(tx)
}

func TestCreateCategoryEnforcesThreeLevels(t *testing.T) {
	_, svc, tenant, _ := newCTITestFixture(t)
	level1 := createCTI(t, svc, tenant, 0, "network")
	level2 := createCTI(t, svc, tenant, level1, "remote")
	level3 := createCTI(t, svc, tenant, level2, "vpn")

	_, err := svc.CreateCategory(t.Context(), &CreateCategoryRequest{Name: "deep", Code: "deep", ParentID: level3, IsActive: true, TenantID: tenant})
	require.ErrorIs(t, err, ErrCTIPathTooDeep)
}

func TestCreateCategoryKeepsCodeUniquePerTenant(t *testing.T) {
	_, svc, tenant, other := newCTITestFixture(t)
	createCTI(t, svc, tenant, 0, "shared-code")

	_, err := svc.CreateCategory(t.Context(), &CreateCategoryRequest{Name: "dup", Code: "shared-code", IsActive: true, TenantID: tenant})
	require.ErrorContains(t, err, "分类代码已存在")

	// 跨租户同码是合法业务事实：唯一范围是租户内。
	createCTI(t, svc, other, 0, "shared-code")
}

func TestCreateCategoryRejectsCrossTenantParent(t *testing.T) {
	_, svc, tenant, other := newCTITestFixture(t)
	foreign := createCTI(t, svc, other, 0, "foreign-parent")

	_, err := svc.CreateCategory(t.Context(), &CreateCategoryRequest{Name: "child", Code: "child", ParentID: foreign, IsActive: true, TenantID: tenant})
	require.ErrorIs(t, err, ErrCTICategoryNotFound)
}

func TestUpdateCategoryRejectsCodeChange(t *testing.T) {
	_, svc, tenant, _ := newCTITestFixture(t)
	id := createCTI(t, svc, tenant, 0, "stable-code")

	_, err := svc.UpdateCategory(t.Context(), id, &UpdateCategoryRequest{Code: "renamed"}, tenant)
	require.ErrorIs(t, err, ErrCTICategoryCodeImmutable)

	renamed, err := svc.UpdateCategory(t.Context(), id, &UpdateCategoryRequest{Name: "新名称", Code: "stable-code"}, tenant)
	require.NoError(t, err)
	require.Equal(t, "新名称", renamed.Name)
	require.Equal(t, "stable-code", renamed.Code)
}

func TestDeleteCategoryRejectsChildrenAndReferences(t *testing.T) {
	client, svc, tenant := ctiClientFixture(t)
	root := createCTI(t, svc, tenant, 0, "root-ref")
	child := createCTI(t, svc, tenant, root, "child-ref")

	require.ErrorIs(t, svc.DeleteCategory(t.Context(), root, tenant), ErrCTICategoryHasChildren)

	user := client.User.Create().SetTenantID(tenant).SetUsername("cti-user").SetName("User").SetRole("agent").SetEmail("cti@example.test").SetPasswordHash("x").SaveX(t.Context())
	item := client.Ticket.Create().SetTenantID(tenant).SetRequesterID(user.ID).SetTitle("ref").SetTicketNumber("CTI-REF").SetStatus("open").SetCategoryID(child).SaveX(t.Context())
	require.ErrorIs(t, svc.DeleteCategory(t.Context(), child, tenant), ErrCTICategoryReferenced)

	// 移除引用后可删除，证明保护基于当前引用而不是永久锁定。
	client.Ticket.UpdateOneID(item.ID).ClearCategoryID().ExecX(t.Context())
	require.NoError(t, svc.DeleteCategory(t.Context(), child, tenant))
}

func TestDeleteCategoryProtectsEveryConfiguredReference(t *testing.T) {
	client, svc, tenant := ctiClientFixture(t)
	user := client.User.Create().SetTenantID(tenant).SetUsername("cti-owner").SetName("Owner").SetRole("admin").SetEmail("owner@example.test").SetPasswordHash("x").SaveX(t.Context())

	for _, test := range []struct {
		name      string
		reference func(categoryID int)
	}{
		{"sla_definition", func(id int) {
			client.SLADefinition.Create().SetTenantID(tenant).SetName("sla").SetCategoryIds([]int{id}).SaveX(t.Context())
		}},
		{"assignment_rule", func(id int) {
			client.TicketAssignmentRule.Create().SetTenantID(tenant).SetName("assign").SetConditions([]map[string]interface{}{
				{"field": "category_id", "operator": "equals", "value": float64(id)},
			}).SaveX(t.Context())
		}},
		{"automation_rule_condition", func(id int) {
			client.TicketAutomationRule.Create().SetTenantID(tenant).SetCreatedBy(user.ID).SetName("auto-condition").SetConditions([]map[string]interface{}{
				{"field": "category_id", "operator": "in", "value": []interface{}{float64(id)}},
			}).SaveX(t.Context())
		}},
		{"automation_rule_action", func(id int) {
			client.TicketAutomationRule.Create().SetTenantID(tenant).SetCreatedBy(user.ID).SetName("auto-action").SetActions([]map[string]interface{}{
				{"type": "set_category", "category_id": float64(id)},
			}).SaveX(t.Context())
		}},
		{"template", func(id int) {
			client.TicketTemplate.Create().SetTenantID(tenant).SetName("template").SetCategory("it").SetCategoryIds([]int{id}).SaveX(t.Context())
		}},
		{"process_binding", func(id int) {
			client.ProcessBinding.Create().SetTenantID(tenant).SetBusinessType("ticket").SetProcessDefinitionKey("flow").SetCategoryID(id).SaveX(t.Context())
		}},
		{"legacy_escalation_name", func(id int) {
			category := client.TicketCategory.GetX(t.Context(), id)
			client.IncidentEscalationRule.Create().SetTenantID(tenant).SetName("escalate").SetTriggerType("sla_breach").
				SetTriggerMinutes(30).SetTargetAssigneeType("group").SetCategoryMatch(category.Name).SaveX(t.Context())
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			categoryID := createCTI(t, svc, tenant, 0, "ref-"+test.name)
			test.reference(categoryID)
			require.ErrorIs(t, svc.DeleteCategory(t.Context(), categoryID, tenant), ErrCTICategoryReferenced)
		})
	}
}

func TestMoveCategoryRejectsReferencedSubtree(t *testing.T) {
	client, svc, tenant := ctiClientFixture(t)
	root := createCTI(t, svc, tenant, 0, "move-root")
	child := createCTI(t, svc, tenant, root, "move-child")
	other := createCTI(t, svc, tenant, 0, "move-target")

	// 引用最深节点时，被引用节点自身与其祖先都不能移动
	// （移动祖先会改变被引用后代的完整路径）。
	sla := client.SLADefinition.Create().SetTenantID(tenant).SetName("sla-move").SetCategoryIds([]int{child}).SaveX(t.Context())
	newParent := other
	_, err := svc.MoveCategory(t.Context(), root, &MoveCategoryRequest{NewParentID: &newParent}, tenant)
	require.ErrorIs(t, err, ErrCTICategoryReferenced)
	_, err = svc.MoveCategory(t.Context(), child, &MoveCategoryRequest{NewParentID: &newParent}, tenant)
	require.ErrorIs(t, err, ErrCTICategoryReferenced)

	// 引用解除后，同一节点仍可移动（保护基于当前引用，不是永久锁定）。
	client.SLADefinition.DeleteOneID(sla.ID).ExecX(t.Context())
	moved, err := svc.MoveCategory(t.Context(), child, &MoveCategoryRequest{NewParentID: &newParent}, tenant)
	require.NoError(t, err)
	require.Equal(t, other, moved.ParentID)
	require.Equal(t, 2, moved.Level)
}

func TestMoveCategoryRejectsSubtreeDeeperThanThreeLevels(t *testing.T) {
	_, svc, tenant, _ := newCTITestFixture(t)
	target := createCTI(t, svc, tenant, 0, "deep-root")
	deepParent := createCTI(t, svc, tenant, target, "deep-level2")
	moving := createCTI(t, svc, tenant, 0, "moving-root")
	createCTI(t, svc, tenant, moving, "moving-level2")

	newParent := deepParent
	_, err := svc.MoveCategory(t.Context(), moving, &MoveCategoryRequest{NewParentID: &newParent}, tenant)
	require.ErrorIs(t, err, ErrCTIPathTooDeep)
}

func TestMoveCategoryRejectsCycle(t *testing.T) {
	_, svc, tenant, _ := newCTITestFixture(t)
	root := createCTI(t, svc, tenant, 0, "cycle-root")
	child := createCTI(t, svc, tenant, root, "cycle-child")

	newParent := child
	_, err := svc.MoveCategory(t.Context(), root, &MoveCategoryRequest{NewParentID: &newParent}, tenant)
	require.ErrorContains(t, err, "子分类")
}

func TestDisableCategoryRequiresNoPublishedCatalog(t *testing.T) {
	client, svc, tenant := ctiClientFixture(t)
	root := createCTI(t, svc, tenant, 0, "disable-root")
	leaf := createCTI(t, svc, tenant, root, "disable-leaf")

	// 草稿目录（非发布状态）不阻止停用。
	draft := client.ServiceCatalog.Create().SetTenantID(tenant).SetName("draft").SetTargetClass("service_request_item").
		SetStatus("disabled").SetDefaultTicketCategoryID(leaf).SaveX(t.Context())
	disabled := false
	_, err := svc.UpdateCategory(t.Context(), leaf, &UpdateCategoryRequest{IsActive: &disabled}, tenant)
	require.NoError(t, err)

	enabled := true
	_, err = svc.UpdateCategory(t.Context(), leaf, &UpdateCategoryRequest{IsActive: &enabled}, tenant)
	require.NoError(t, err)

	// 已发布目录引用最深节点时，其祖先也不能停用。
	client.ServiceCatalog.UpdateOneID(draft.ID).SetStatus("enabled").SetIsActive(true).ExecX(t.Context())
	_, err = svc.UpdateCategory(t.Context(), root, &UpdateCategoryRequest{IsActive: &disabled}, tenant)
	require.ErrorIs(t, err, ErrCTICategoryPublishedCatalog)

	client.ServiceCatalog.UpdateOneID(draft.ID).SetStatus("disabled").SetIsActive(false).ExecX(t.Context())
	_, err = svc.UpdateCategory(t.Context(), root, &UpdateCategoryRequest{IsActive: &disabled}, tenant)
	require.NoError(t, err)
}

func TestResolveCTIPathPersistsDeepestNodeContract(t *testing.T) {
	client, svc, tenant := ctiClientFixture(t)
	level1 := createCTI(t, svc, tenant, 0, "resolve-l1")
	level2 := createCTI(t, svc, tenant, level1, "resolve-l2")
	level3 := createCTI(t, svc, tenant, level2, "resolve-l3")

	withCTITx(t, client, func(tx *ent.Tx) {
		path, err := svc.ResolveCTIPath(t.Context(), tx, tenant, level3, true, true)
		require.NoError(t, err)
		require.Len(t, path, 3)
		require.Equal(t, []int{level1, level2, level3}, []int{path[0].ID, path[1].ID, path[2].ID})
		require.Equal(t, []int{1, 2, 3}, []int{path[0].Level, path[1].Level, path[2].Level})

		// 未分类的普通报障合法；要求完整三级时被拒绝。
		empty, err := svc.ResolveCTIPath(t.Context(), tx, tenant, 0, false, true)
		require.NoError(t, err)
		require.Empty(t, empty)
		_, err = svc.ResolveCTIPath(t.Context(), tx, tenant, 0, true, true)
		require.ErrorIs(t, err, ErrCTIPathIncomplete)
		_, err = svc.ResolveCTIPath(t.Context(), tx, tenant, level2, true, true)
		require.ErrorIs(t, err, ErrCTIPathIncomplete)
	})

	// 停用后：历史质量校验（requireActive=false）仍认可原路径；重新选择必须用启用节点。
	client.TicketCategory.UpdateOneID(level1).SetIsActive(false).ExecX(t.Context())
	withCTITx(t, client, func(tx *ent.Tx) {
		path, err := svc.ResolveCTIPath(t.Context(), tx, tenant, level3, true, false)
		require.NoError(t, err)
		require.Len(t, path, 3)
		_, err = svc.ResolveCTIPath(t.Context(), tx, tenant, level3, true, true)
		require.ErrorIs(t, err, ErrCTIPathInactive)
	})
}

func TestResolveCTIPathRequiresOwningTransaction(t *testing.T) {
	_, svc, tenant, _ := newCTITestFixture(t)
	_, err := svc.ResolveCTIPath(t.Context(), nil, tenant, 1, false, true)
	require.ErrorContains(t, err, "owning transaction")
}

func TestGetCategoryTreeProjectsPathAndIncludesInactive(t *testing.T) {
	client, svc, tenant := ctiClientFixture(t)
	level1 := createCTI(t, svc, tenant, 0, "tree-l1")
	level2 := createCTI(t, svc, tenant, level1, "tree-l2")
	level3 := createCTI(t, svc, tenant, level2, "tree-l3")
	client.TicketCategory.UpdateOneID(level2).SetIsActive(false).ExecX(t.Context())

	activeTree, err := svc.GetCategoryTree(t.Context(), tenant, false)
	require.NoError(t, err)
	require.Len(t, activeTree, 1)
	require.Equal(t, level1, activeTree[0].ID)
	// 停用的中间节点被过滤后，其后代不能悄悄挂到错误的层级。
	require.Empty(t, activeTree[0].Children)

	tree, err := svc.GetCategoryTree(t.Context(), tenant, true)
	require.NoError(t, err)
	require.Len(t, tree, 1)
	require.Equal(t, level1, tree[0].ID)
	require.Len(t, tree[0].Children, 1)
	require.Equal(t, level2, tree[0].Children[0].ID)
	require.False(t, tree[0].Children[0].IsActive)
	require.Len(t, tree[0].Children[0].Children, 1)
	require.Equal(t, "tree-l1 / tree-l2 / tree-l3", tree[0].Children[0].Children[0].Path)
	require.Equal(t, []int{level1, level2, level3}, tree[0].Children[0].Children[0].PathIDs)

	path, err := svc.GetCategoryPath(t.Context(), tenant, level3)
	require.NoError(t, err)
	require.Len(t, path, 3)
	require.Equal(t, level1, path[0].ID)
}

// 跨租户父链（历史错误数据）必须阻止在其之上继续加深，且不泄露对方租户对象。
func TestCreateCategoryRejectsCrossTenantParentChain(t *testing.T) {
	client, svc, tenant, other := newCTITestFixture(t)
	foreign := createCTI(t, svc, other, 0, "foreign-chain-root")
	child := createCTI(t, svc, tenant, 0, "chain-child")
	// 绕过服务直接写入跨租户父级，模拟历史脏数据。
	client.TicketCategory.UpdateOneID(child).SetParentID(foreign).SetLevel(2).ExecX(t.Context())

	_, err := svc.CreateCategory(t.Context(), &CreateCategoryRequest{Name: "grand", Code: "grand", ParentID: child, IsActive: true, TenantID: tenant})
	require.ErrorIs(t, err, ErrCTIPathHierarchy)

	// 也不能把分类移动到这条损坏链下。
	newParent := child
	_, err = svc.MoveCategory(t.Context(), createCTI(t, svc, tenant, 0, "chain-moving"), &MoveCategoryRequest{NewParentID: &newParent}, tenant)
	require.ErrorIs(t, err, ErrCTIPathHierarchy)
}

func TestGetCategoryByCodeScopesToTenant(t *testing.T) {
	_, svc, tenant, other := newCTITestFixture(t)
	createCTI(t, svc, tenant, 0, "scoped-code")
	createCTI(t, svc, other, 0, "scoped-code")

	found, err := svc.GetCategoryByCode(t.Context(), tenant, "scoped-code")
	require.NoError(t, err)
	require.Equal(t, tenant, found.TenantID)

	_, err = svc.GetCategoryByCode(t.Context(), tenant, "missing-code")
	require.ErrorIs(t, err, ErrCTICategoryNotFound)
}

func TestSubtreeHeightAndDepths(t *testing.T) {
	client, svc, tenant := ctiClientFixture(t)
	root := createCTI(t, svc, tenant, 0, "height-root")
	child := createCTI(t, svc, tenant, root, "height-child")
	grand := createCTI(t, svc, tenant, child, "height-grand")

	all := client.TicketCategory.Query().AllX(t.Context())
	nodes, depths, ids := subtreeOf(all, root)
	require.Len(t, nodes, 3)
	require.Len(t, ids, 3)
	require.Equal(t, 3, subtreeHeight(depths, root))
	require.Equal(t, 2, subtreeHeight(depths, child))
	require.Equal(t, 1, subtreeHeight(depths, grand))
}

func TestMaintenanceHonoursCallerContextDeadline(t *testing.T) {
	_, svc, tenant, _ := newCTITestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := svc.CreateCategory(ctx, &CreateCategoryRequest{Name: "ctx", Code: "ctx", IsActive: true, TenantID: tenant})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteCategory(ctx, id.ID, tenant))
}
