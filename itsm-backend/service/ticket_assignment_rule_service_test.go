package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Subtree rule matching resolves against the complete classification path, so
// the resolved path must be ordered root -> selected node.
func TestTicketAssignmentRuleCategoryMatchPathResolvesFullHierarchy(t *testing.T) {
	client, categories, tenant := ctiClientFixture(t)
	root := createCTI(t, categories, tenant, 0, "assign-root")
	child := createCTI(t, categories, tenant, root, "assign-child")
	leaf := createCTI(t, categories, tenant, child, "assign-leaf")
	item := ctiTicketWithCategory(t, client, tenant, leaf, "ASSIGN-SCOPE")

	path, err := NewTicketAssignmentRuleService(client, zap.NewNop().Sugar()).categoryMatchPath(t.Context(), item)

	require.NoError(t, err)
	require.Len(t, path, 3, "subtree matching needs the complete path, not just the selected node")
	assert.Equal(t, []int{root, child, leaf}, []int{path[0].ID, path[1].ID, path[2].ID})
}

// An unclassified item (or a missing item) must not fabricate a path, otherwise
// subtree conditions would match by accident.
func TestTicketAssignmentRuleCategoryMatchPathIsEmptyWithoutClassification(t *testing.T) {
	client, categories, tenant := ctiClientFixture(t)
	service := NewTicketAssignmentRuleService(client, zap.NewNop().Sugar())

	path, err := service.categoryMatchPath(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, path)

	unclassified := ctiTicketWithCategory(t, client, tenant, 0, "ASSIGN-NO-CATEGORY")
	path, err = service.categoryMatchPath(t.Context(), unclassified)
	require.NoError(t, err)
	assert.Empty(t, path)
	_ = categories
}
