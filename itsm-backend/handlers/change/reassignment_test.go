package change

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processtask"
	"testing"
	"time"
)

func reassignmentPatch(t *testing.T, target int, reason string) dto.UpdateChangeRequest {
	t.Helper()
	var patch dto.UpdateChangeRequest
	data, err := json.Marshal(map[string]any{"assigneeId": target, "assignmentReason": reason})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &patch))
	return patch
}

func TestChangeReassignmentRequiresReason(t *testing.T) {
	for _, reason := range []string{"", "  \n "} {
		t.Run(fmt.Sprintf("reason-%q", reason), func(t *testing.T) {
			f := newGovernedChangeFixture(t, "normal")
			before := f.client.Ticket.UpdateOneID(f.record.WorkItemID).SetAssigneeID(f.requester).SaveX(f.ctx)
			_, err := f.svc.ApplyMetadata(f.ctx, MetadataCommand{Meta: f.command("metadata", f.requester).Meta, ChangeID: f.record.ID, Patch: reassignmentPatch(t, f.approver, reason)})
			require.ErrorContains(t, err, "assignment reason required")
			after := f.client.Ticket.GetX(f.ctx, before.ID)
			require.Equal(t, before.AssigneeID, after.AssigneeID)
			require.Equal(t, before.Version, after.Version)
			require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
		})
	}
}

func TestChangeReassignmentPreservesApproval(t *testing.T) {
	for _, stage := range []string{"approved", "scheduled", "in_progress"} {
		t.Run(stage, func(t *testing.T) {
			f := newGovernedChangeFixture(t, "normal")
			f.client.Ticket.UpdateOneID(f.record.WorkItemID).SetAssigneeID(f.requester).ExecX(f.ctx)
			f.submit(t)
			f.assess(t)
			_, err := f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, "approve", f.approver))
			require.NoError(t, err)
			if stage != "approved" {
				schedule := f.taskCommand(t, "schedule", f.requester)
				start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
				schedule.PlannedStart = &start
				schedule.PlannedEnd = &end
				_, err = f.svc.CompleteChangeTask(f.ctx, schedule)
				require.NoError(t, err)
			}
			if stage == "in_progress" {
				_, err = f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, "implement", f.requester))
				require.NoError(t, err)
			}
			before := f.client.Ticket.GetX(f.ctx, f.record.WorkItemID)
			assessed := f.client.Change.GetX(f.ctx, f.record.ID)
			decisions := f.client.ProcessApprovalDecision.Query().IDsX(f.ctx)
			require.NotEmpty(t, decisions)
			tasks := f.client.ProcessTask.Query().Order(processtask.ByID()).AllX(f.ctx)
			require.NotEmpty(t, tasks)
			cmd := MetadataCommand{Meta: f.command("metadata", f.requester).Meta, ChangeID: f.record.ID, Patch: reassignmentPatch(t, f.approver, "  application support handover  ")}
			result, err := f.svc.ApplyMetadata(f.ctx, cmd)
			require.NoError(t, err)
			after := f.client.Ticket.GetX(f.ctx, before.ID)
			require.Equal(t, before.Status, after.Status)
			require.Equal(t, f.approver, after.AssigneeID)
			require.Equal(t, before.Version+1, result.Version)
			current := f.client.Change.GetX(f.ctx, f.record.ID)
			require.Equal(t, assessed.AssessmentDigest, current.AssessmentDigest)
			require.Equal(t, assessed.AssessmentEvidence, current.AssessmentEvidence)
			require.Equal(t, assessed.AssessedBy, current.AssessedBy)
			require.Equal(t, assessed.AssessedAt, current.AssessedAt)
			require.Equal(t, decisions, f.client.ProcessApprovalDecision.Query().IDsX(f.ctx))
			afterTasks := f.client.ProcessTask.Query().Order(processtask.ByID()).AllX(f.ctx)
			require.Len(t, afterTasks, len(tasks))
			for i, task := range tasks {
				require.Equal(t, task.ID, afterTasks[i].ID)
				require.Equal(t, task.Assignee, afterTasks[i].Assignee)
				require.Equal(t, task.Status, afterTasks[i].Status)
			}
			audit := f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).OnlyX(f.ctx)
			require.Contains(t, *audit.RequestBody, `"assignmentReason":"application support handover"`)
			require.Contains(t, *audit.RequestBody, fmt.Sprintf(`"previousAssigneeId":%d`, before.AssigneeID))
			replay, err := f.svc.ApplyMetadata(f.ctx, cmd)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, result.Version, replay.Version)
			cmd.Patch = reassignmentPatch(t, f.approver, "different intent")
			_, err = f.svc.ApplyMetadata(f.ctx, cmd)
			require.Error(t, err)
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
		})
	}
}

func TestChangeReassignmentPreservesGovernedGuards(t *testing.T) {
	for _, mode := range []string{"assessed_plan", "authorized_plan", "window", "pending_callback", "stale", "terminal", "invalid_target", "same_owner", "first_assignment", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			f := newGovernedChangeFixture(t, "normal")
			if mode != "first_assignment" {
				f.client.Ticket.UpdateOneID(f.record.WorkItemID).SetAssigneeID(f.requester).ExecX(f.ctx)
			}
			if mode == "assessed_plan" || mode == "authorized_plan" || mode == "window" || mode == "pending_callback" {
				f.submit(t)
				f.assess(t)
			}
			if mode == "authorized_plan" {
				_, err := f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, "approve", f.approver))
				require.NoError(t, err)
			}
			if mode == "pending_callback" {
				f.client.ProcessCallbackOutbox.Update().SetStatus("pending").ExecX(f.ctx)
			}
			if mode == "terminal" {
				f.client.Ticket.UpdateOneID(f.record.WorkItemID).SetStatus("completed").ExecX(f.ctx)
			}
			before := f.client.Ticket.GetX(f.ctx, f.record.WorkItemID)
			audits := f.client.AuditLog.Query().CountX(f.ctx)
			cmd := MetadataCommand{Meta: f.command("metadata", f.requester).Meta, ChangeID: f.record.ID, Patch: reassignmentPatch(t, f.approver, "handover")}
			text := "changed plan"
			switch mode {
			case "assessed_plan", "authorized_plan":
				cmd.Patch.ImplementationPlan = &text
			case "window":
				now := time.Now()
				cmd.Patch.PlannedStartDate = &now
			case "stale":
				cmd.Meta.ExpectedVersion++
			case "invalid_target":
				target := 99999
				cmd.Patch.AssigneeID = &target
			case "same_owner":
				cmd.Patch = reassignmentPatch(t, f.requester, "")
				cmd.Patch.Title = &text
			case "first_assignment":
				cmd.Patch = reassignmentPatch(t, f.approver, "")
			case "revoked":
				f.client.User.UpdateOneID(f.requester).SetActive(false).ExecX(f.ctx)
			}
			_, err := f.svc.ApplyMetadata(f.ctx, cmd)
			if mode == "same_owner" || mode == "first_assignment" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			after := f.client.Ticket.GetX(f.ctx, before.ID)
			require.Equal(t, before.AssigneeID, after.AssigneeID)
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, audits, f.client.AuditLog.Query().CountX(f.ctx))
		})
	}
}

func TestChangeReassignmentHTTP(t *testing.T) {
	for _, route := range []string{"assign", "metadata"} {
		t.Run(route, func(t *testing.T) {
			f, r := newGovernedHandlerFixture(t)
			f.client.Ticket.UpdateOneID(f.record.WorkItemID).SetAssigneeID(f.requester).ExecX(f.ctx)
			endpoint := fmt.Sprintf("/api/v1/changes/%d", f.record.ID)
			method := "PUT"
			if route == "assign" {
				endpoint += "/assign"
				method = "POST"
			}
			missing := fmt.Sprintf(`{"expectedVersion":1,"operationId":"missing","assigneeId":%d}`, f.approver)
			w := governedHTTP(r, method, endpoint, missing, nil)
			require.Equal(t, 400, w.Code, w.Body.String())
			body := fmt.Sprintf(`{"expectedVersion":1,"operationId":"handover","assigneeId":%d,"assignmentReason":"  real handover  "}`, f.approver)
			w = governedHTTP(r, method, endpoint, body, nil)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Equal(t, f.approver, f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).AssigneeID)
			w = governedHTTP(r, method, endpoint, body, nil)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), `"replayed":true`)
			wrongVersion := fmt.Sprintf(`{"version":2,"operationId":"wrong-version-field","assigneeId":%d,"assignmentReason":"handover"}`, f.requester)
			w = governedHTTP(r, method, endpoint, wrongVersion, nil)
			require.Equal(t, 400, w.Code, w.Body.String())
		})
	}
}
