package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/internal/jsonvalue"
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

func TestPersistedBPMNNumberMapCloneAndCounterThreshold(t *testing.T) {
	var reloaded jsonvalue.NumberMap
	require.NoError(t, json.Unmarshal([]byte(`{"threshold":2,"amount":9007199254740993.125,"nested":{"value":0.125}}`), &reloaded))
	cloned, err := cloneBPMNJSONValue(reloaded, 0)
	require.NoError(t, err)
	require.Equal(t, json.Number("9007199254740993.125"), cloned.(map[string]any)["amount"])
	cloned.(map[string]any)["nested"].(map[string]any)["value"] = "edited"
	require.Equal(t, json.Number("0.125"), reloaded["nested"].(map[string]any)["value"])
	threshold, ok := numericInt(reloaded["threshold"])
	require.True(t, ok)
	require.Equal(t, 2, threshold)
	_, ok = numericInt(json.Number("2.5"))
	require.False(t, ok)
}
