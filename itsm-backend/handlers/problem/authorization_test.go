package problem

import (
	"testing"

	"itsm-backend/common"
	"itsm-backend/service"

	"github.com/stretchr/testify/require"
)

func TestBuildProblemActionsUsesCanonicalStatuses(t *testing.T) {
	actor := service.ActionActor{TenantID: 1, UserID: 7, Role: "super_admin"}

	openProblem := &Problem{Status: common.TicketStatusOpen}
	investigatingProblem := &Problem{Status: "investigating"}
	identifiedProblem := &Problem{Status: "identified"}
	legacyInProgressProblem := &Problem{Status: "in_progress"}
	resolvedProblem := &Problem{Status: "resolved"}

	openActions := BuildProblemActions(actor, openProblem)
	require.Len(t, openActions, 7)
	require.True(t, openActions["edit"].Allowed)
	require.True(t, openActions["startInvestigation"].Allowed)
	require.False(t, openActions["resolve"].Allowed)
	require.False(t, openActions["close"].Allowed)
	require.NotEmpty(t, openActions["close"].Reason)

	require.True(t, CanStartInvestigation(actor, legacyInProgressProblem).Allowed)
	require.Empty(t, CanStartInvestigation(actor, legacyInProgressProblem).Reason)

	require.False(t, CanResolveProblem(actor, investigatingProblem).Allowed)
	require.False(t, CanResolveProblem(actor, identifiedProblem).Allowed)
	require.False(t, CanResolveProblem(actor, legacyInProgressProblem).Allowed)
	require.False(t, CanResolveProblem(actor, resolvedProblem).Allowed)
	require.True(t, CanStartInvestigation(actor, identifiedProblem).Allowed)
	require.False(t, CanStartInvestigation(actor, resolvedProblem).Allowed)
	require.True(t, CanReopenProblem(actor, resolvedProblem).Allowed)

	require.False(t, CanCloseProblem(actor, resolvedProblem).Allowed)
	require.False(t, CanCloseProblem(actor, investigatingProblem).Allowed)
}
