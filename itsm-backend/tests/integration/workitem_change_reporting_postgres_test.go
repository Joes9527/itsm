//go:build integration_postgres

package integration

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	changedomain "itsm-backend/handlers/change"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

func TestWorkItemChangeHTTPIdentityAndOutcomeStats(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	r, h := changeHTTPFixture(f)
	statsHandler := changedomain.NewHandler(changedomain.NewService(changedomain.NewEntRepository(f.runtime, f.db), f.runtime, zap.NewNop().Sugar(), executionfixture.Standard()))
	r.GET("/changes/stats", statsHandler.GetStats)
	r.GET("/changes/:id", h.GetChange)
	r.POST("/changes/:id/submit", h.ExecuteAction)
	w := changeHTTPCall(r, "GET", fmt.Sprintf("/changes/%d", f.c.ID), "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var detail struct {
		Data struct {
			Number     string `json:"number"`
			WorkItemID int    `json:"workItemId"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.Equal(t, "CHG-CORE", detail.Data.Number)
	require.Equal(t, f.c.WorkItemID, detail.Data.WorkItemID)
	var dates struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &dates))
	for _, field := range []string{"plannedStartDate", "plannedEndDate", "actualStartDate", "actualEndDate"} {
		require.Nil(t, dates.Data[field], field)
	}
	w = changeHTTPCall(r, "POST", fmt.Sprintf("/changes/%d/submit", f.c.ID), `{"expectedVersion":1,"operationId":"report-submit"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	for i, outcome := range []string{"successful", "failed", "rolled_back", ""} {
		item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetTitle("outcome fixture").SetTicketNumber(fmt.Sprintf("CHG-REPORT-%d", i)).SetRecordClass("change_request").SetStatus("completed").SaveX(f.ctx)
		f.client.Change.Create().SetWorkItemID(item.ID).SetOutcome(outcome).SaveX(f.ctx)
	}
	deleted := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetTitle("deleted").SetTicketNumber("CHG-DELETED").SetRecordClass("change_request").SetStatus("completed").SetDeletedAt(time.Now()).SaveX(f.ctx)
	f.client.Change.Create().SetWorkItemID(deleted.ID).SetOutcome("successful").SaveX(f.ctx)
	foreign := f.client.Tenant.Create().SetName("report foreign").SetCode("report-foreign").SaveX(f.ctx)
	item := f.client.Ticket.Create().SetTenantID(foreign.ID).SetRequesterID(f.actor.ID).SetTitle("foreign").SetTicketNumber("CHG-FOREIGN").SetRecordClass("change_request").SetStatus("completed").SaveX(f.ctx)
	f.client.Change.Create().SetWorkItemID(item.ID).SetOutcome("successful").SaveX(f.ctx)
	w = changeHTTPCall(r, "GET", "/changes/stats", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var stats struct {
		Data map[string]int `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &stats))
	require.Equal(t, 5, stats.Data["total"])
	require.Equal(t, 4, stats.Data["completed"])
	require.Equal(t, 1, stats.Data["pending"], "canonical submitted contributes to pending summary")
	require.Equal(t, 1, stats.Data["successfulOutcomes"])
	require.Equal(t, 1, stats.Data["failedOutcomes"])
	require.Equal(t, 1, stats.Data["rolledBackOutcomes"])
}
