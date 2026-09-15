package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateTicketRequestDoesNotBindForceFromJSON(t *testing.T) {
	var req UpdateTicketRequest
	require.NoError(t, json.Unmarshal([]byte(`{"title":"updated","force":true,"version":1,"operationId":"edit-1"}`), &req))
	var withoutForce UpdateTicketRequest
	require.NoError(t, json.Unmarshal([]byte(`{"title":"updated","version":1,"operationId":"edit-1"}`), &withoutForce))
	require.Equal(t, withoutForce, req)
	encoded, err := json.Marshal(req)
	require.NoError(t, err)
	var fields map[string]interface{}
	require.NoError(t, json.Unmarshal(encoded, &fields))
	require.NotContains(t, fields, "force")
	require.Equal(t, 1, req.Version)
}
