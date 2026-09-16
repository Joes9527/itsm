package service

import (
	"encoding/json"
	"testing"

	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/require"
)

func ticketCallbackContractForTest(t *testing.T, action string) bpmn.CallbackActionContract {
	t.Helper()
	contract, ok := (&bpmn.TicketServiceTaskHandler{}).CallbackContract(action)
	require.True(t, ok, action)
	return contract
}

// The observed Dev failure: Activity_Assign completed with no actor input, the
// contract accepted the payload, and the durable callback retried to
// handler_error. The declared value rule must reject every unusable form.
func TestTicketAssignPayloadRejectsUnusableAssigneeID(t *testing.T) {
	contract := ticketCallbackContractForTest(t, "assign")

	_, err := normalizeBPMNCallbackContractPayload(contract, map[string]any{"notify_content": "please take it"})
	require.Error(t, err, "a completion without assignee_id must not produce an assign payload")

	for _, testCase := range []struct {
		name  string
		value any
	}{
		{"null", nil},
		{"empty string", ""},
		{"blank string", "   "},
		{"zero", 0},
		{"negative", -3},
		{"fractional", 1.5},
		{"non numeric string", "not-a-user"},
		{"padded numeric string", " 42 "},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := normalizeBPMNCallbackContractPayload(contract, map[string]any{"assignee_id": testCase.value})
			require.Error(t, err)
		})
	}

	for _, valid := range []any{7, int64(7), json.Number("7"), "7"} {
		normalized, err := normalizeBPMNCallbackContractPayload(contract, map[string]any{"assignee_id": valid})
		require.NoError(t, err, "%v must stay accepted", valid)
		require.Equal(t, "7", normalized["assignee_id"])
	}
}

// update_status keeps a fixed fallback, so an absent new_status must stay valid
// (updateTicketStatus maps it to "in_progress") while an explicitly supplied
// unusable value must be rejected.
func TestTicketUpdateStatusPayloadBindsOnlySuppliedNewStatus(t *testing.T) {
	contract := ticketCallbackContractForTest(t, "update_status")

	normalized, err := normalizeBPMNCallbackContractPayload(contract, map[string]any{})
	require.NoError(t, err, "the handler owns an in_progress fallback, so absence stays valid")
	require.NotContains(t, normalized, "new_status")

	for _, testCase := range []struct {
		name  string
		value any
	}{
		{"null", nil},
		{"empty string", ""},
		{"whitespace only", "   "},
		{"tab and newline", "\t\n"},
		{"number", 12},
		{"boolean", true},
		{"object", map[string]any{"status": "in_progress"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := normalizeBPMNCallbackContractPayload(contract, map[string]any{"new_status": testCase.value})
			require.Error(t, err)
		})
	}

	normalized, err = normalizeBPMNCallbackContractPayload(contract, map[string]any{"new_status": "in_progress"})
	require.NoError(t, err)
	require.Equal(t, "in_progress", normalized["new_status"])
}

// A user-fixable value error on an opted-in action must abort the completion
// command instead of persisting a callback that can never succeed.
func TestCallbackEnqueuePlanRejectsUserInputErrorForOptedInAction(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{
		PayloadFields:          []string{"assignee_id"},
		RequiredFields:         []string{"assignee_id"},
		PositiveIntegerFields:  []string{"assignee_id"},
		RejectInvalidUserInput: true,
	}

	plan, err := BuildCallbackEnqueuePlanForActorCompletion(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply"},
		map[string]interface{}{"unrelated": "value"},
		false,
		callbackEnqueuePlanRegistry(handler),
	)

	require.Error(t, err, "missing actor input must fail the completion instead of enqueueing")
	require.Empty(t, plan.Payload)
	require.Empty(t, plan.BlockCode, "a fixable input error is not a definition block")
}

// The same opted-in action reached from a frozen payload (workflow start,
// service-task auto-dispatch, claimed callback) must keep the visible blocked
// plan: the frozen value came from configuration, not from the completing actor.
func TestFrozenEnqueueKeepsBlockedPlanForOptedInAction(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{
		PayloadFields:          []string{"assignee_id"},
		RequiredFields:         []string{"assignee_id"},
		PositiveIntegerFields:  []string{"assignee_id"},
		RejectInvalidUserInput: true,
	}

	plan, err := BuildCallbackEnqueuePlan(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply"},
		map[string]interface{}{"assignee_id": "malformed"},
		false,
		callbackEnqueuePlanRegistry(handler),
	)

	require.NoError(t, err)
	require.Equal(t, bpmn.CallbackBlockHandlerContract, plan.BlockCode)
	require.Empty(t, plan.Payload)
}

// Handlers outside the Dev restoration keep the pre-existing visible blocked
// plan: this task must not change every handler's required-field semantics.
func TestCallbackEnqueuePlanKeepsBlockedPlanForNonOptedInAction(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{
		PayloadFields:  []string{"assignee_id"},
		RequiredFields: []string{"assignee_id"},
	}

	plan, err := BuildCallbackEnqueuePlan(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply"},
		map[string]interface{}{"unrelated": "value"},
		false,
		callbackEnqueuePlanRegistry(handler),
	)

	require.NoError(t, err)
	require.Equal(t, bpmn.CallbackBlockHandlerContract, plan.BlockCode)
	require.Empty(t, plan.Payload)
}

// A declared-contract defect stays a blocked plan even for an opted-in action:
// only the actor-correctable value errors are promoted to a rejection.
func TestCallbackEnqueuePlanKeepsDefinitionDefectsBlockedForOptedInAction(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{
		PayloadFields:          []string{"title"},
		RequiredFields:         []string{"undeclared_required"},
		RejectInvalidUserInput: true,
	}

	plan, err := BuildCallbackEnqueuePlan(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply"},
		map[string]interface{}{"title": "declared"},
		false,
		callbackEnqueuePlanRegistry(handler),
	)

	require.NoError(t, err)
	require.Equal(t, bpmn.CallbackBlockHandlerContract, plan.BlockCode)
	require.Empty(t, plan.Payload)
}

// An unknown action on an opted-in handler is a definition defect, not actor input.
func TestCallbackEnqueuePlanKeepsUnknownActionBlockedForOptedInHandler(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{
		PayloadFields:          []string{"assignee_id"},
		RequiredFields:         []string{"assignee_id"},
		RejectInvalidUserInput: true,
	}

	plan, err := BuildCallbackEnqueuePlan(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "undeclared_action"},
		map[string]interface{}{"assignee_id": 7},
		false,
		callbackEnqueuePlanRegistry(handler),
	)

	require.NoError(t, err)
	require.Equal(t, bpmn.CallbackBlockHandlerContract, plan.BlockCode)
}
