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
	require.Len(t, openActions, 4)
	require.True(t, openActions["edit"].Allowed)
	require.True(t, openActions["startInvestigation"].Allowed)
	require.True(t, openActions["resolve"].Allowed)
	require.False(t, openActions["close"].Allowed)
	require.NotEmpty(t, openActions["close"].Reason)

	require.False(t, CanStartInvestigation(actor, legacyInProgressProblem).Allowed)
	require.NotEmpty(t, CanStartInvestigation(actor, legacyInProgressProblem).Reason)

	require.True(t, CanResolveProblem(actor, investigatingProblem).Allowed)
	require.True(t, CanResolveProblem(actor, identifiedProblem).Allowed)
	require.True(t, CanResolveProblem(actor, legacyInProgressProblem).Allowed)
	require.False(t, CanResolveProblem(actor, resolvedProblem).Allowed)
	require.True(t, CanStartInvestigation(actor, identifiedProblem).Allowed)
	require.True(t, CanStartInvestigation(actor, resolvedProblem).Allowed)

	require.True(t, CanCloseProblem(actor, resolvedProblem).Allowed)
	require.False(t, CanCloseProblem(actor, investigatingProblem).Allowed)
}
