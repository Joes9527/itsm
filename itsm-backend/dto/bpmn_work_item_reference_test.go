package dto

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"testing"
)

func TestBPMNTaskResponseIncludesOwningWorkItemNumber(t *testing.T) {
	task := &ent.ProcessTask{TaskVariables: map[string]interface{}{}}
	instance := &ent.ProcessInstance{BusinessKey: "service_request:41", Variables: map[string]interface{}{"ticket_number": "TKT-202609-000021", "work_item_id": 55}}
	raw, e := json.Marshal(ToBPMNTaskResponse(task, instance))
	require.NoError(t, e)
	var view map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &view))
	require.Equal(t, "TKT-202609-000021", view["workItemNumber"])
	require.EqualValues(t, 41, view["businessId"])
}
