package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/service/bpmn"
)

func TestCallbackIntegerContractPersistsExactIdentity(t *testing.T) {
	contract, _ := (&bpmn.TicketServiceTaskHandler{}).CallbackContract("assign")
	normalized, err := normalizeBPMNCallbackContractPayload(contract, map[string]any{"assignee_id": json.Number("9007199254740993"), "tenant_id": 999})
	require.NoError(t, err)
	require.NotContains(t, normalized, "tenant_id")
	raw, err := json.Marshal(normalized)
	require.NoError(t, err)
	var reloaded map[string]any
	require.NoError(t, json.Unmarshal(raw, &reloaded))
	require.Equal(t, 9007199254740993, bpmn.GetIntFromVars(reloaded, "assignee_id"))
}

func TestCallbackJSONNumberValidationPreservesNestedPrecision(t *testing.T) {
	input := map[string]any{"amount": json.Number("9007199254740993.125"), "items": []any{json.Number("0.125")}}
	cloned, err := cloneBPMNJSONValue(input, 0)
	require.NoError(t, err)
	raw, err := json.Marshal(cloned)
	require.NoError(t, err)
	require.Contains(t, string(raw), "9007199254740993.125")
	for _, number := range []json.Number{"NaN", "1/2", "", "+1", "1e99999999"} {
		_, err := cloneBPMNJSONValue(number, 0)
		require.Error(t, err)
	}
	_, err = normalizeBPMNCallbackContractPayload(bpmn.CallbackActionContract{PayloadFields: []string{"tenant_id"}, PositiveIntegerFields: []string{"tenant_id"}}, map[string]any{"tenant_id": json.Number("2")})
	require.Error(t, err, "integer metadata must not bypass reserved identity filtering")
}
