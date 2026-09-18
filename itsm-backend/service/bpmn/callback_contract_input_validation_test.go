package bpmn

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The Dev restoration incident (ticket29 / process27 / task33 / callback2) showed
// that the ticket `assign` action declared only a positive-integer rule. A missing
// assignee_id therefore passed the contract, was persisted as a durable callback,
// and retried until it reached handler_error. The declaration must carry
// required-ness and must not degrade a user-fixable input error into a silently
// unusable callback.
func TestTicketAssignCallbackRejectsMissingAssigneeID(t *testing.T) {
	contract, ok := (&TicketServiceTaskHandler{}).CallbackContract("assign")
	require.True(t, ok)

	require.Contains(t, contract.PayloadFields, "assignee_id")
	require.Contains(t, contract.RequiredFields, "assignee_id")
	require.Contains(t, contract.PositiveIntegerFields, "assignee_id")
	require.True(t, contract.RejectInvalidUserInput)
}

// Activity_Resolve needs the new status supplied by the actor. A key-presence-only
// required check would accept null, an empty string, whitespace, or a number, so
// update_status additionally declares an owned non-empty string rule.
func TestTicketUpdateStatusCallbackBindsOnlySuppliedNewStatus(t *testing.T) {
	contract, ok := (&TicketServiceTaskHandler{}).CallbackContract("update_status")
	require.True(t, ok)

	require.Contains(t, contract.PayloadFields, "new_status")
	require.Contains(t, contract.NonEmptyStringFields, "new_status")
	// updateTicketStatus maps a missing new_status to "in_progress", so the fixed
	// configuration satisfies the input and absence must stay valid. The stricter
	// "must be supplied" rule belongs to the gated generic lifecycle contract.
	require.NotContains(t, contract.RequiredFields, "new_status")
	require.False(t, contract.RejectInvalidUserInput)
}

// Handlers that were not part of the Dev restoration must keep the existing
// definition-defect behaviour instead of gaining a global rejection semantics.
func TestUnrelatedCallbackActionsKeepExistingInputSemantics(t *testing.T) {
	for _, action := range []string{"notify_requester", "notify_handler", "escalate"} {
		contract, ok := (&TicketServiceTaskHandler{}).CallbackContract(action)
		require.True(t, ok, action)
		require.False(t, contract.RejectInvalidUserInput, action)
		require.NotContains(t, contract.NonEmptyStringFields, "new_status", action)
	}

	incidentAssign, ok := (&IncidentServiceTaskHandler{}).CallbackContract("assign_incident")
	require.True(t, ok)
	require.False(t, incidentAssign.RejectInvalidUserInput)
}
