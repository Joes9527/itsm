package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Automation rules evaluate the same subtree semantics as assignment rules, so
// their path resolution must be identical.
func TestTicketAutomationRuleCategoryMatchPathResolvesFullHierarchy(t *testing.T) {
	client, categories, tenant := ctiClientFixture(t)
	root := createCTI(t, categories, tenant, 0, "auto-root")
	child := createCTI(t, categories, tenant, root, "auto-child")
	leaf := createCTI(t, categories, tenant, child, "auto-leaf")
	item := ctiTicketWithCategory(t, client, tenant, leaf, "AUTO-SCOPE")

	path, err := NewTicketAutomationRuleService(client, zap.NewNop().Sugar()).categoryMatchPath(t.Context(), item)

	require.NoError(t, err)
	require.Len(t, path, 3, "subtree matching needs the complete path, not just the selected node")
	assert.Equal(t, []int{root, child, leaf}, []int{path[0].ID, path[1].ID, path[2].ID})
}

func TestTicketAutomationRuleCategoryMatchPathIsEmptyWithoutClassification(t *testing.T) {
	client, _, tenant := ctiClientFixture(t)
	service := NewTicketAutomationRuleService(client, zap.NewNop().Sugar())

	path, err := service.categoryMatchPath(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, path)

	unclassified := ctiTicketWithCategory(t, client, tenant, 0, "AUTO-NO-CATEGORY")
	path, err = service.categoryMatchPath(t.Context(), unclassified)
	require.NoError(t, err)
	assert.Empty(t, path)
}
