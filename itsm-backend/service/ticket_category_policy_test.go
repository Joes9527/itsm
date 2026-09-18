package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// ctiPath builds a root→leaf ordered candidate path for the test tenant.
func ctiPath(ids ...int) []CTINode {
	nodes := make([]CTINode, 0, len(ids))
	for index, id := range ids {
		parent := 0
		if index > 0 {
			parent = ids[index-1]
		}
		nodes = append(nodes, CTINode{ID: id, ParentID: parent, Level: index + 1, TenantID: 7, Active: true})
	}
	return nodes
}

func TestCTIRejectsIncompleteRequiredPath(t *testing.T) {
	path := []CTINode{{ID: 1, TenantID: 7, Level: 1, Active: true}}
	if ValidateCTIPath(path, 7, true, true) == nil {
		t.Fatal("required CTI accepted only level 1")
	}
}

func TestCTIAllowsUnclassifiedReport(t *testing.T) {
	if err := ValidateCTIPath(nil, 7, false, true); err != nil {
		t.Fatal(err)
	}
}

func TestCTIAllowsPartialPathWhenNotRequired(t *testing.T) {
	require.NoError(t, ValidateCTIPath(ctiPath(1), 7, false, true))
	require.NoError(t, ValidateCTIPath(ctiPath(1, 2), 7, false, true))
}

func TestCTIAcceptsCompletePath(t *testing.T) {
	require.NoError(t, ValidateCTIPath(ctiPath(1, 2, 3), 7, true, true))
}

func TestCTIRejectsOverDepthPath(t *testing.T) {
	err := ValidateCTIPath(ctiPath(1, 2, 3, 4), 7, false, true)
	require.ErrorIs(t, err, ErrCTIPathTooDeep)
}

func TestCTIRejectsDiscontinuousLevel(t *testing.T) {
	path := ctiPath(1, 2, 3)
	path[2].Level = 4
	require.ErrorIs(t, ValidateCTIPath(path, 7, false, true), ErrCTIPathHierarchy)
}

func TestCTIRejectsMissingRootParent(t *testing.T) {
	path := ctiPath(1, 2)
	path[0].ParentID = 9
	require.ErrorIs(t, ValidateCTIPath(path, 7, false, true), ErrCTIPathHierarchy)
}

func TestCTIRejectsBrokenParentChain(t *testing.T) {
	path := ctiPath(1, 2, 3)
	path[2].ParentID = 99
	require.ErrorIs(t, ValidateCTIPath(path, 7, false, true), ErrCTIPathHierarchy)
}

func TestCTIRejectsCrossTenantNode(t *testing.T) {
	path := ctiPath(1, 2, 3)
	path[1].TenantID = 8
	require.ErrorIs(t, ValidateCTIPath(path, 7, false, true), ErrCTIPathOutsideTenant)
}

func TestCTIRejectsDuplicateNode(t *testing.T) {
	path := ctiPath(1, 2, 3)
	path[1].ID = 1
	path[1].ParentID = 1
	require.ErrorIs(t, ValidateCTIPath(path, 7, false, true), ErrCTIPathHierarchy)
}

func TestCTIRejectsInvalidTenantScope(t *testing.T) {
	require.ErrorIs(t, ValidateCTIPath(ctiPath(1), 0, false, true), ErrCTIPathOutsideTenant)
}

func TestCTIRequiresActiveAncestorsWhenRequested(t *testing.T) {
	path := ctiPath(1, 2, 3)
	path[0].Active = false
	require.ErrorIs(t, ValidateCTIPath(path, 7, false, true), ErrCTIPathInactive)
	require.NoError(t, ValidateCTIPath(path, 7, false, false), "historical disabled ancestors stay valid for quality checks")
}

func TestCTIMatchScopeDistinguishesAncestor(t *testing.T) {
	path := ctiPath(1, 2, 3)
	exact, err := MatchCTI(path, 1, CTIExact)
	require.NoError(t, err)
	require.False(t, exact, "exact must not match an ancestor")
	exact, err = MatchCTI(path, 3, CTIExact)
	require.NoError(t, err)
	require.True(t, exact, "exact matches the selected deepest node")
	subtree, err := MatchCTI(path, 1, CTISubtree)
	require.NoError(t, err)
	require.True(t, subtree, "subtree matches any ancestor")
	subtree, err = MatchCTI(path, 3, CTISubtree)
	require.NoError(t, err)
	require.True(t, subtree)
	subtree, err = MatchCTI(path, 42, CTISubtree)
	require.NoError(t, err)
	require.False(t, subtree)
}

func TestCTIRejectsUnknownMatchScope(t *testing.T) {
	_, err := MatchCTI(ctiPath(1), 1, CTIMatchScope("descendants"))
	require.True(t, errors.Is(err, ErrCTIUnknownMatchScope))
}

func TestCTIMatchRejectsEmptyPath(t *testing.T) {
	_, err := MatchCTI(nil, 1, CTIExact)
	require.ErrorIs(t, err, ErrCTIPathIncomplete)
}
