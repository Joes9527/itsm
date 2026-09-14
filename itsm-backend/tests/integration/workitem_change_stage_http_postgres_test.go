//go:build integration_postgres

package integration

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processtask"
	changedomain "itsm-backend/handlers/change"
	"testing"
	"time"
)

// Moved from handlers/change: actual review/close use PostgreSQL row locks.
func TestTransitionStatus_StageCompletion_AdvanceProcessEndToEnd(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	task := prepareChangeDefaultTask(t, f)
	r, h := changeHTTPFixture(f)
	for _, action := range []string{"assess", "approve", "schedule", "implement", "record-outcome", "review", "close"} {
		r.POST("/changes/:id/"+action, h.ExecuteAction)
	}
	r.POST("/changes/:id/pir", h.CreatePIR)
	base := fmt.Sprintf("/changes/%d", f.c.ID)
	invoke := func(action string, fields map[string]any) changedomain.TaskProgress {
		t.Helper()
		fields["expectedVersion"] = f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version
		fields["operationId"] = "http-" + action
		fields["taskId"] = task.TaskID
		fields["evidence"] = "verified " + action
		body, err := json.Marshal(fields)
		require.NoError(t, err)
		w := changeHTTPCall(r, "POST", base+"/"+action, string(body))
		require.Equal(t, 200, w.Code, w.Body.String())
		var reply struct {
			Code int                       `json:"code"`
			Data changedomain.TaskProgress `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &reply))
		require.Zero(t, reply.Code)
		require.NotNil(t, reply.Data.Result)
		require.Equal(t, "completed", reply.Data.Progress)
		return reply.Data
	}
	initiator := f.actor
	approver := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("stage-cab").SetName("CAB").SetEmail("stage@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("change_manager").SetName("CAB").SaveX(f.ctx)
	approver.Update().AddRoleIDs(role.ID).ExecX(f.ctx)
	invoke("assess", map[string]any{})
	f.actor = approver
	task = f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_CABApproval")).OnlyX(f.ctx)
	approved := invoke("approve", map[string]any{})
	require.Equal(t, "approved", approved.Result.Status)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
	f.actor = initiator
	task = f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Schedule")).OnlyX(f.ctx)
	invoke("schedule", map[string]any{"plannedStartDate": time.Now().Add(-time.Hour), "plannedEndDate": time.Now().Add(time.Hour)})
	task = f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Implement")).OnlyX(f.ctx)
	invoke("implement", map[string]any{})
	task = f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Verify")).OnlyX(f.ctx)
	outcome := invoke("record-outcome", map[string]any{"outcome": "failed", "actualEndDate": time.Now()})
	require.Equal(t, "in_progress", outcome.Result.Status)
	body, _ := json.Marshal(map[string]any{"expectedVersion": outcome.Result.Version, "operationId": "http-pir", "overallResult": "failed", "successSummary": "observed failure"})
	w := changeHTTPCall(r, "POST", base+"/pir", string(body))
	require.Equal(t, 200, w.Code, w.Body.String())
	var pir struct {
		Data struct {
			PIRID int `json:"pirId"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &pir))
	require.Positive(t, pir.Data.PIRID)
	task = f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Review")).OnlyX(f.ctx)
	invoke("review", map[string]any{"pirId": pir.Data.PIRID})
	task = f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Close")).OnlyX(f.ctx)
	closed := invoke("close", map[string]any{"pirId": pir.Data.PIRID})
	require.Equal(t, "completed", closed.Result.Status)
	require.Equal(t, "failed", f.client.Change.GetX(f.ctx, f.c.ID).Outcome)
	require.Equal(t, "completed", f.client.ProcessInstance.Query().OnlyX(f.ctx).Status)
	require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.close")).CountX(f.ctx))
	rewrite := "rewrite"
	_, err := f.pirOwner.UpdatePIR(f.ctx, pir.Data.PIRID, &dto.UpdateChangePIRRequest{ChangeID: f.c.ID, SuccessSummary: &rewrite}, f.command("pir", "terminal").Meta)
	require.Error(t, err)
}
