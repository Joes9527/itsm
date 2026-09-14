package change

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApprovalHistoryHTTPUsesCamelCaseContract(t *testing.T) {
	router, handler, repo := setupTestHandler(t)
	router.GET("/api/v1/changes/:id/approvals", handler.GetApprovals)
	now := time.Date(2026, 9, 14, 5, 0, 0, 0, time.UTC)
	comment := "CAB approved"
	repo.approvals[1] = &ApprovalRecord{ID: 1, ChangeID: 7, TenantID: 1, ApproverID: 9, ApproverName: "CAB reviewer", Status: "approved", Comment: &comment, ApprovedAt: &now, CreatedAt: now}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/v1/changes/7/approvals", nil))
	require.Equal(t, 200, recorder.Code)
	var response struct {
		Code int                      `json:"code"`
		Data []map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Zero(t, response.Code)
	require.Len(t, response.Data, 1)
	row := response.Data[0]
	require.Equal(t, "approved", row["status"])
	require.Equal(t, float64(9), row["approverId"])
	require.Equal(t, "CAB reviewer", row["approverName"])
	require.Equal(t, "CAB approved", row["comment"])
	require.Equal(t, "2026-09-14T05:00:00Z", row["approvedAt"])
	for _, field := range []string{"Status", "ApproverID", "TenantID", "tenantId"} {
		require.NotContains(t, row, field)
	}
	empty := httptest.NewRecorder()
	router.ServeHTTP(empty, httptest.NewRequest("GET", "/api/v1/changes/8/approvals", nil))
	require.Equal(t, 200, empty.Code)
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(empty.Body.Bytes(), &payload))
	require.Equal(t, []interface{}{}, payload["data"])
}
