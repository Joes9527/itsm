package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

func ctiScopePath() []CTINode {
	return []CTINode{
		{ID: 1, Name: "root", Level: 1, ParentID: 0, Active: true, TenantID: 1},
		{ID: 2, Name: "type", Level: 2, ParentID: 1, Active: true, TenantID: 1},
		{ID: 3, Name: "item", Level: 3, ParentID: 2, Active: true, TenantID: 1},
	}
}

func ctiCondition(scope interface{}, operator string, value interface{}) []map[string]interface{} {
	condition := map[string]interface{}{"field": "category_id", "operator": operator, "value": value}
	if scope != nil {
		condition["scope"] = scope
	}
	return []map[string]interface{}{condition}
}

// 默认（无 scope 标记）必须保持既有精确语义，否则历史规则的命中集会被静默扩大。
func TestCTIRuleConditionDefaultsToExactScope(t *testing.T) {
	item := &ent.Ticket{ID: 9, TenantID: 1, CategoryID: 3, Status: "new", Priority: "medium"}
	match := TicketRuleMatch{Item: item, CategoryPath: ctiScopePath()}

	matched, err := EvaluateTicketRuleConditions(match, ctiCondition(nil, "equals", float64(3)))
	require.NoError(t, err)
	require.True(t, matched)

	// 祖先不是"当前分类"：精确语义下不得命中。
	matched, err = EvaluateTicketRuleConditions(match, ctiCondition(nil, "equals", float64(1)))
	require.NoError(t, err)
	require.False(t, matched, "exact scope must not match an ancestor")

	matched, err = EvaluateTicketRuleConditions(match, ctiCondition(nil, "in", []interface{}{float64(1), float64(2)}))
	require.NoError(t, err)
	require.False(t, matched, "exact membership must not match ancestors")
}

func TestCTIRuleConditionSubtreeMatchesAncestors(t *testing.T) {
	item := &ent.Ticket{ID: 9, TenantID: 1, CategoryID: 3, Status: "new", Priority: "medium"}
	match := TicketRuleMatch{Item: item, CategoryPath: ctiScopePath()}

	for _, parameter := range []struct {
		name      string
		operator  string
		value     interface{}
		wantMatch bool
	}{
		{"ancestor-equals", "equals", float64(1), true},
		{"self-equals", "equals", float64(3), true},
		{"sibling-not-in-path", "equals", float64(7), false},
		{"ancestor-in-set", "in", []interface{}{float64(2)}, true},
		{"not-in-set", "not_in", []interface{}{float64(1)}, false},
	} {
		t.Run(parameter.name, func(t *testing.T) {
			matched, err := EvaluateTicketRuleConditions(match, ctiCondition("subtree", parameter.operator, parameter.value))
			require.NoError(t, err)
			require.Equal(t, parameter.wantMatch, matched)
		})
	}
}

func TestCTIRuleConditionRejectsUnknownScope(t *testing.T) {
	item := &ent.Ticket{ID: 9, TenantID: 1, CategoryID: 3, Status: "new", Priority: "medium"}
	match := TicketRuleMatch{Item: item, CategoryPath: ctiScopePath()}

	for _, scope := range []interface{}{"descendants", "", 7, true} {
		matched, err := EvaluateTicketRuleConditions(match, ctiCondition(scope, "equals", float64(3)))
		if scope == "" {
			// 空字符串按缺省处理：既有数据可能显式写入空值。
			require.NoError(t, err)
			require.True(t, matched)
			continue
		}
		require.ErrorIs(t, err, ErrCTIUnknownMatchScope, "scope=%v", scope)
		require.False(t, matched)
	}
}

// 未分类工单：分类条件不命中，但不是错误；有分类却拿不到路径才是错误（失败关闭）。
func TestCTIRuleConditionFailsClosedWithoutResolvedPath(t *testing.T) {
	unclassified := &ent.Ticket{ID: 9, TenantID: 1, Status: "new", Priority: "medium"}
	matched, err := EvaluateTicketRuleConditions(TicketRuleMatch{Item: unclassified}, ctiCondition("subtree", "equals", float64(1)))
	require.NoError(t, err)
	require.False(t, matched, "an unclassified item cannot match a classification condition")

	classified := &ent.Ticket{ID: 10, TenantID: 1, CategoryID: 3, Status: "new", Priority: "medium"}
	_, err = EvaluateTicketRuleConditions(TicketRuleMatch{Item: classified}, ctiCondition("subtree", "equals", float64(1)))
	require.ErrorContains(t, err, "classification path")

	_, err = EvaluateTicketRuleConditions(TicketRuleMatch{Item: classified}, ctiCondition(nil, "equals", float64(3)))
	require.ErrorContains(t, err, "classification path", "missing path must fail closed even for exact scope")
}

func TestCTIRuleConditionRejectsUndefinedCategoryOperators(t *testing.T) {
	item := &ent.Ticket{ID: 9, TenantID: 1, CategoryID: 3, Status: "new", Priority: "medium"}
	match := TicketRuleMatch{Item: item, CategoryPath: ctiScopePath()}

	for _, operator := range []string{"contains", "greater_than", "like"} {
		_, err := EvaluateTicketRuleConditions(match, ctiCondition(nil, operator, float64(3)))
		require.ErrorContains(t, err, "unsupported category condition operator", "operator=%s", operator)
	}
	_, err := EvaluateTicketRuleConditions(match, ctiCondition(nil, "equals", "not-a-number"))
	require.ErrorContains(t, err, "numeric ids")
}

// 非分类字段的既有行为不得改变。
func TestCTIRuleConditionKeepsNonCategoryFields(t *testing.T) {
	item := &ent.Ticket{ID: 9, TenantID: 1, CategoryID: 3, Status: "in_progress", Priority: "high"}
	match := TicketRuleMatch{Item: item, CategoryPath: ctiScopePath()}

	matched, err := EvaluateTicketRuleConditions(match, []map[string]interface{}{
		{"field": "status", "operator": "equals", "value": "in_progress"},
		{"field": "priority", "operator": "in", "value": []interface{}{"high", "critical"}},
		{"field": "category_id", "operator": "equals", "value": float64(2), "scope": "subtree"},
	})
	require.NoError(t, err)
	require.True(t, matched)

	matched, err = EvaluateTicketRuleConditions(match, []map[string]interface{}{
		{"field": "status", "operator": "equals", "value": "closed"},
	})
	require.NoError(t, err)
	require.False(t, matched)
}
