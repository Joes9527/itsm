//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processtask"
	changedomain "itsm-backend/handlers/change"
)

func changeAssignmentCommand(t *testing.T, f *changeLifecycleFixture, key string) changedomain.MetadataCommand {
	t.Helper()
	target := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("handover-" + key).SetName("Handover").SetEmail(key + "@handover.test").SetPasswordHash("test").SetRole("agent").SetActive(true).SaveX(f.ctx)
	f.client.Ticket.UpdateOneID(f.c.WorkItemID).SetAssigneeID(f.actor.ID).ExecX(f.ctx)
	return changedomain.MetadataCommand{Meta: f.command("metadata", key).Meta, ChangeID: f.c.ID, Patch: dto.UpdateChangeRequest{AssigneeID: &target.ID, AssignmentReason: "application support handover"}}
}

func TestWorkItemAssignmentChangePreservesApproval(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
	require.NoError(t, err)
	f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
	f.apply(t, f.command("submit", "submit"))
	task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Assessment")).OnlyX(f.ctx)
	_, err = f.owner.CompleteChangeTask(f.ctx, changedomain.TaskCommand{Command: f.command("assess", "assess"), TaskID: task.TaskID})
	require.NoError(t, err)
	approver := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("approval-owner").SetName("CAB").SetEmail("cab@handover.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
	task = f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_CABApproval")).OnlyX(f.ctx)
	task.Update().SetAssignee(fmt.Sprint(approver.ID)).ExecX(f.ctx)
	f.actor = approver
	_, err = f.owner.CompleteChangeTask(f.ctx, changedomain.TaskCommand{Command: f.command("approve", "approve"), TaskID: task.TaskID})
	require.NoError(t, err)
	cmd := changeAssignmentCommand(t, f, "preserve")
	before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	professional := f.client.Change.GetX(f.ctx, f.c.ID)
	decisions := f.client.ProcessApprovalDecision.Query().IDsX(f.ctx)
	require.NotEmpty(t, decisions)
	tasks := f.client.ProcessTask.Query().Order(processtask.ByID()).AllX(f.ctx)
	result, err := f.owner.ApplyMetadata(f.ctx, cmd)
	require.NoError(t, err)
	after := f.client.Ticket.GetX(f.ctx, before.ID)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, *cmd.Patch.AssigneeID, after.AssigneeID)
	require.Equal(t, before.Version+1, result.Version)
	current := f.client.Change.GetX(f.ctx, f.c.ID)
	require.Equal(t, professional.AssessmentDigest, current.AssessmentDigest)
	require.Equal(t, professional.AssessmentEvidence, current.AssessmentEvidence)
	require.Equal(t, professional.AssessedBy, current.AssessedBy)
	require.Equal(t, professional.AssessedAt, current.AssessedAt)
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
	replay, err := f.owner.ApplyMetadata(f.ctx, cmd)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, result.Version, replay.Version)
}

func TestWorkItemAssignmentChangeRejectsAndRollsBack(t *testing.T) {
	for _, mode := range []string{"reason", "stale", "audit_failure", "changed_reason", "revoked_replay"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			cmd := changeAssignmentCommand(t, f, mode)
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			switch mode {
			case "reason":
				cmd.Patch.AssignmentReason = " \n "
			case "stale":
				cmd.Meta.ExpectedVersion++
			case "audit_failure":
				f.runtime.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if _, ok := m.(*ent.AuditLogMutation); ok {
							return nil, errors.New("assignment audit unavailable")
						}
						return next.Mutate(ctx, m)
					})
				})
			case "changed_reason", "revoked_replay":
				_, err := f.owner.ApplyMetadata(f.ctx, cmd)
				require.NoError(t, err)
				before = f.client.Ticket.GetX(f.ctx, before.ID)
				if mode == "changed_reason" {
					cmd.Patch.AssignmentReason = "another intention"
				} else {
					f.actor.Update().SetActive(false).ExecX(f.ctx)
				}
			}
			_, err := f.owner.ApplyMetadata(f.ctx, cmd)
			require.Error(t, err)
			after := f.client.Ticket.GetX(f.ctx, before.ID)
			require.Equal(t, before.AssigneeID, after.AssigneeID)
			require.Equal(t, before.Version, after.Version)
			expectedAudits := 0
			if mode == "changed_reason" || mode == "revoked_replay" {
				expectedAudits = 1
			}
			require.Equal(t, expectedAudits, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
		})
	}
}

func TestWorkItemAssignmentChangeConcurrentReceipt(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			cmd := changeAssignmentCommand(t, f, "concurrent")
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			synchronizeChangeOwnerReads(t, f)
			start := make(chan struct{})
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for i := range 2 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					copy := cmd
					if !same {
						copy.Meta.OperationID += fmt.Sprint(i)
					}
					_, err := f.owner.ApplyMetadata(f.ctx, copy)
					results <- err
				}(i)
			}
			close(start)
			wg.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				}
			}
			expected := 1
			if same {
				expected = 2
			}
			require.Equal(t, expected, success)
			after := f.client.Ticket.GetX(f.ctx, before.ID)
			require.Equal(t, before.Version+1, after.Version)
			require.Equal(t, *cmd.Patch.AssigneeID, after.AssigneeID)
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
		})
	}
}
