package change

import (
	"testing"

	"itsm-backend/service"

	"github.com/stretchr/testify/require"
)

func TestBuildChangeActions(t *testing.T) {
	actor := service.ActionActor{TenantID: 1, UserID: 7, Role: "super_admin"}
	change := &Change{Type: "normal", Status: "draft", CreatedBy: 23}

	actions := BuildChangeActions(actor, change)

	require.Len(t, actions, 5)
	require.True(t, actions["submitForApproval"].Allowed)
	require.False(t, actions["approve"].Allowed)
	require.Equal(t, "只有已提交待审批的变更可以批准", actions["approve"].Reason)
	require.False(t, actions["reject"].Allowed)
	require.Equal(t, "只有已提交待审批的变更可以驳回", actions["reject"].Reason)
	require.False(t, actions["startImplementation"].Allowed)
	require.Equal(t, "当前状态和变更类型不允许开始实施", actions["startImplementation"].Reason)
	require.False(t, actions["completeImplementation"].Allowed)
	require.Equal(t, "只有实施中的变更可以标记完成", actions["completeImplementation"].Reason)
}

func TestBuildChangeActionsUsesDistinctSelfApprovalAndRejectionReasons(t *testing.T) {
	actor := service.ActionActor{TenantID: 1, UserID: 11, Role: "super_admin"}
	change := &Change{Type: "standard", Status: "pending", CreatedBy: 11}

	actions := BuildChangeActions(actor, change)

	require.False(t, actions["approve"].Allowed)
	require.Equal(t, "不能审批自己提交的变更", actions["approve"].Reason)
	require.False(t, actions["reject"].Allowed)
	require.Equal(t, "不能驳回自己提交的变更", actions["reject"].Reason)
}

func TestCanStartImplementationIsTypeAware(t *testing.T) {
	actor := service.ActionActor{TenantID: 1, UserID: 7, Role: "super_admin"}

	require.False(t, CanStartImplementation(actor, &Change{Type: "normal", Status: "approved"}).Allowed)
	require.True(t, CanStartImplementation(actor, &Change{Type: "normal", Status: "scheduled"}).Allowed)
	require.True(t, CanStartImplementation(actor, &Change{Type: "standard", Status: "approved"}).Allowed)
	require.True(t, CanStartImplementation(actor, &Change{Type: "standard", Status: "scheduled"}).Allowed)
	require.True(t, CanStartImplementation(actor, &Change{Type: "emergency", Status: "approved"}).Allowed)
	require.False(t, CanStartImplementation(actor, &Change{Type: "emergency", Status: "scheduled"}).Allowed)
	require.True(t, CanStartImplementation(actor, &Change{Type: "standard", Status: "draft"}).Allowed)
	require.True(t, CanStartImplementation(actor, &Change{Type: "emergency", Status: "draft"}).Allowed)
}
