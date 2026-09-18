//go:build integration_postgres

package integration

import (
	"testing"

	"itsm-backend/ent"
	"itsm-backend/service"

	"github.com/stretchr/testify/require"
)

// ruleScopePath 在拥有者事务内解析分类路径（与生产调用方一致：同一事务内解析）。
func ruleScopePath(t *testing.T, f *ctiStructureFixture, tenant, categoryID int) []service.CTINode {
	t.Helper()
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback()) }()
	path, err := service.ResolveRuleMatchPath(f.ctx, tx, tenant, categoryID)
	require.NoError(t, err)
	return path
}

func ruleScopeMatched(t *testing.T, item *ent.Ticket, path []service.CTINode, condition map[string]interface{}) bool {
	t.Helper()
	matched, err := service.EvaluateTicketRuleConditions(service.TicketRuleMatch{Item: item, CategoryPath: path}, []map[string]interface{}{condition})
	require.NoError(t, err)
	return matched
}

// B3 的真实 PostgreSQL 证据：规则分类条件的精确/子树语义与**旧规则命中集合不变**。
// 旧规则（条件里没有 scope 标记）必须继续按精确语义命中，新增的子树范围只能由显式声明获得；
// 未知 scope 必须失败关闭。分类路径由拥有者在**同一事务内**解析。
func TestCTIRuleScopePostgres(t *testing.T) {
	f := newCTIStructureFixture(t)
	categories := service.NewTicketCategoryService(f.client)
	tenant := f.tenant(t, "cti-rule-scope")

	tree := func(prefix string) [3]int {
		ids := [3]int{}
		parent := 0
		for index, code := range []string{prefix + "-l1", prefix + "-l2", prefix + "-l3"} {
			record, err := categories.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenant})
			require.NoError(t, err)
			ids[index] = record.ID
			parent = record.ID
		}
		return ids
	}
	primary := tree("scope")
	sibling := tree("scope-sibling")

	requester := f.client.User.Create().SetTenantID(tenant).SetUsername("cti-scope-requester").SetName("Requester").
		SetRole("end_user").SetEmail("cti-scope-requester@example.test").SetPasswordHash("x").SaveX(f.ctx)
	item := f.client.Ticket.Create().SetTenantID(tenant).SetRequesterID(requester.ID).SetTitle("Rule scope").SetTicketNumber("CTI-SCOPE-1").
		SetRecordClass("generic").SetStatus("new").SetPriority("medium").SetCategoryID(primary[2]).SaveX(f.ctx)

	path := ruleScopePath(t, f, tenant, item.CategoryID)
	require.Len(t, path, 3, "the owner resolves the full root path inside its transaction")

	evaluate := func(condition map[string]interface{}) (bool, error) {
		return service.EvaluateTicketRuleConditions(service.TicketRuleMatch{Item: item, CategoryPath: path}, []map[string]interface{}{condition})
	}

	t.Run("legacy_exact_conditions_keep_their_hit_set", func(t *testing.T) {
		// 旧规则形态：没有 scope 标记。
		matched, err := evaluate(map[string]interface{}{"field": "category_id", "operator": "equals", "value": primary[2]})
		require.NoError(t, err)
		require.True(t, matched, "the deepest node still matches")

		matched, err = evaluate(map[string]interface{}{"field": "category_id", "operator": "equals", "value": primary[1]})
		require.NoError(t, err)
		require.False(t, matched, "an ancestor must NOT match without an explicit subtree scope")

		matched, err = evaluate(map[string]interface{}{"field": "category_id", "operator": "in", "value": []interface{}{primary[0], primary[1]}})
		require.NoError(t, err)
		require.False(t, matched, "legacy membership must not widen to ancestors")

		matched, err = evaluate(map[string]interface{}{"field": "category_id", "operator": "equals", "value": sibling[2]})
		require.NoError(t, err)
		require.False(t, matched)
	})

	t.Run("subtree_scope_matches_ancestors_only_when_declared", func(t *testing.T) {
		for _, id := range []int{primary[0], primary[1], primary[2]} {
			matched, err := evaluate(map[string]interface{}{"field": "category_id", "operator": "equals", "value": id, "scope": "subtree"})
			require.NoError(t, err)
			require.True(t, matched, "ancestor %d must match under subtree scope", id)
		}
		matched, err := evaluate(map[string]interface{}{"field": "category_id", "operator": "equals", "value": sibling[0], "scope": "subtree"})
		require.NoError(t, err)
		require.False(t, matched, "an unrelated branch must not match")
	})

	t.Run("unknown_scope_fails_closed", func(t *testing.T) {
		_, err := evaluate(map[string]interface{}{"field": "category_id", "operator": "equals", "value": primary[2], "scope": "descendants"})
		require.ErrorIs(t, err, service.ErrCTIUnknownMatchScope)
	})

	t.Run("unclassified_item_matches_no_classification_condition", func(t *testing.T) {
		unclassified := f.client.Ticket.Create().SetTenantID(tenant).SetRequesterID(requester.ID).SetTitle("Unclassified").SetTicketNumber("CTI-SCOPE-2").
			SetRecordClass("generic").SetStatus("new").SetPriority("medium").SaveX(f.ctx)
		emptyPath := ruleScopePath(t, f, tenant, unclassified.CategoryID)
		require.Empty(t, emptyPath)
		matched, err := service.EvaluateTicketRuleConditions(
			service.TicketRuleMatch{Item: unclassified, CategoryPath: emptyPath},
			[]map[string]interface{}{{"field": "category_id", "operator": "equals", "value": primary[0], "scope": "subtree"}})
		require.NoError(t, err, "an unclassified item must not be an evaluation error")
		require.False(t, matched)
	})

	// 分类被移动后，规则命中随新路径重算；规则本身不被改写（无自动再执行）。
	// 注意：被工单引用的分类按既有引用保护**不允许移动**，因此移动场景使用未被引用的子树。
	t.Run("moving_a_category_recomputes_hits_without_rewriting_rules", func(t *testing.T) {
		conditions := []map[string]interface{}{{"field": "category_id", "operator": "equals", "value": 0, "scope": "subtree"}}
		rule := f.client.TicketAssignmentRule.Create().SetTenantID(tenant).SetName("scope rule").
			SetConditions(conditions).SetIsActive(true).SetPriority(1).SaveX(f.ctx)

		moveTarget := tree("scope-target")[0]
		movable := tree("scope-movable")
		_, err := categories.MoveCategory(f.ctx, movable[1], &service.MoveCategoryRequest{NewParentID: &moveTarget}, tenant)
		require.NoError(t, err, "an unreferenced subtree must still be movable")

		movedItem := f.client.Ticket.Create().SetTenantID(tenant).SetRequesterID(requester.ID).
			SetTitle("Moved branch").SetTicketNumber("CTI-SCOPE-3").SetRecordClass("generic").
			SetStatus("new").SetPriority("medium").SetCategoryID(movable[2]).SaveX(f.ctx)
		newPath := ruleScopePath(t, f, tenant, movedItem.CategoryID)
		require.Equal(t, moveTarget, newPath[len(newPath)-3].ID, "the parent chain reflects the move")
		require.Equal(t, movable[2], newPath[len(newPath)-1].ID)

		require.True(t, ruleScopeMatched(t, movedItem, newPath,
			map[string]interface{}{"field": "category_id", "operator": "equals", "value": moveTarget, "scope": "subtree"}),
			"subtree scope follows the moved branch")

		// 规则定义本身没有被改写：改分类不触发任何自动再执行。
		stored := f.client.TicketAssignmentRule.GetX(f.ctx, rule.ID)
		require.Len(t, stored.Conditions, 1)
		require.Equal(t, "subtree", stored.Conditions[0]["scope"])
	})
}
