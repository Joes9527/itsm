package dto

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"testing"
)

func TestBPMNTaskResponseIncludesOwningWorkItemNumber(t *testing.T) {
	task := &ent.ProcessTask{TaskVariables: map[string]interface{}{}}
	instance := &ent.ProcessInstance{BusinessKey: "service_request_item:41", Variables: map[string]interface{}{"ticket_number": "TKT-202609-000021", "work_item_id": 55}}
	raw, e := json.Marshal(ToBPMNTaskResponse(task, instance))
	require.NoError(t, e)
	var view map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &view))
	require.Equal(t, "TKT-202609-000021", view["workItemNumber"])
	require.EqualValues(t, 41, view["businessId"])
	require.Equal(t, "service_request_item", view["businessType"])
}

// C1: 任务视图从规范业务键继承身份。Wave-1 旧词表与畸形键不得被解释成身份，
// 否则新运行时会把旧实例身份当成自己的。
func TestBPMNTaskResponseRefusesLegacyAndMalformedBusinessKeys(t *testing.T) {
	for _, legacyKey := range []string{"service_request:41", "ticket:41", "change:41", "", "service_request_item", "service_request_item:0", "service_request_item:abc", "unknown:41"} {
		task := &ent.ProcessTask{TaskVariables: map[string]interface{}{}}
		instance := &ent.ProcessInstance{BusinessKey: legacyKey, Variables: map[string]interface{}{"ticket_number": "TKT-202609-000021"}}
		var view map[string]interface{}
		raw, err := json.Marshal(ToBPMNTaskResponse(task, instance))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &view))
		require.Zero(t, view["businessId"], "legacy or malformed key %q must not yield an identity", legacyKey)
		require.Empty(t, view["businessType"], "legacy or malformed key %q must not yield a business type", legacyKey)
	}
}

// Release 保留其显式遗留身份，仍可被任务视图解析。
func TestBPMNTaskResponseKeepsExplicitReleaseIdentity(t *testing.T) {
	task := &ent.ProcessTask{TaskVariables: map[string]interface{}{}}
	instance := &ent.ProcessInstance{BusinessKey: "release:7", Variables: map[string]interface{}{}}
	var view map[string]interface{}
	raw, err := json.Marshal(ToBPMNTaskResponse(task, instance))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &view))
	require.EqualValues(t, 7, view["businessId"])
	require.Equal(t, "release", view["businessType"])
}
