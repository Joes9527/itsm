//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processtask"
	changedomain "itsm-backend/handlers/change"
	"os"
	"sync"
	"testing"
	"time"
)

func TestWorkItemChangeActionTaskReceipt(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
	require.NoError(t, err)
	f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
	f.apply(t, f.command("submit", "submit"))
	task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Assessment")).OnlyX(f.ctx)
	cmd := changedomain.TaskCommand{Command: f.command("assess", "assess-http"), TaskID: task.TaskID}
	result, err := f.owner.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, "completed", result.Progress)
	require.NotNil(t, result.Result)
	require.Equal(t, 3, result.Result.Version)
	require.Equal(t, "submitted", result.Result.Status)
	row := f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ProcessTaskID(task.ID)).OnlyX(f.ctx)
	require.Equal(t, row.ExecutionKey, result.ExecutionKey)
	require.Equal(t, 200, result.HTTPStatus())
	read, err := f.owner.GetTaskProgress(f.ctx, changedomain.TaskProgressQuery{Meta: cmd.Meta, ChangeID: f.c.ID, Action: "assess"})
	require.NoError(t, err)
	require.Equal(t, result.ExecutionKey, read.ExecutionKey)
	wrong := changedomain.TaskProgressQuery{Meta: cmd.Meta, ChangeID: f.c.ID, Action: "approve"}
	_, err = f.owner.GetTaskProgress(f.ctx, wrong)
	require.Error(t, err)

	replay, err := f.owner.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, result.ExecutionKey, replay.ExecutionKey)
	require.Equal(t, 1, f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ProcessTaskID(task.ID)).CountX(f.ctx))
	cmd.Evidence = "Different assessment"
	_, err = f.owner.CompleteChangeTask(f.ctx, cmd)
	require.Error(t, err)
	require.Equal(t, 3, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
	f.actor.Update().SetActive(false).ExecX(f.ctx)
	_, err = f.owner.GetTaskProgress(f.ctx, changedomain.TaskProgressQuery{Meta: cmd.Meta, ChangeID: f.c.ID, Action: "assess"})
	require.Error(t, err)
}

func TestWorkItemChangeTaskCABEntryPermission(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
	require.NoError(t, err)
	f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)

	for _, phase := range []string{"draft", "submitted"} {
		if phase == "submitted" {
			f.apply(t, f.command("submit", "submit"))
		}
		plan := phase + " reviewed deployment plan"
		_, err := f.owner.ApplyMetadata(f.ctx, changedomain.MetadataCommand{Meta: f.command("metadata", "preassessment-"+phase).Meta, ChangeID: f.c.ID, Patch: dto.UpdateChangeRequest{ImplementationPlan: &plan}})
		require.NoError(t, err, "unassessed facts remain editable")
	}

	f.client.Change.UpdateOneID(f.c.ID).SetAffectedCis([]string{"ci-z", "ci-a"}).ExecX(f.ctx)
	task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Assessment")).OnlyX(f.ctx)
	_, err = f.owner.CompleteChangeTask(f.ctx, changedomain.TaskCommand{Command: f.command("assess", "assess-http"), TaskID: task.TaskID})
	require.NoError(t, err)

	// The actual default assessment advances to CAB while the Change remains submitted.
	assessed := f.client.Change.GetX(f.ctx, f.c.ID)
	require.NotEmpty(t, assessed.AssessmentDigest)
	require.False(t, assessed.AssessedAt.IsZero())
	before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	audits := f.client.AuditLog.Query().CountX(f.ctx)
	text := "changed assessed fact"
	typ := dto.ChangeTypeEmergency
	risk := dto.ChangeRiskHigh
	impact := dto.ChangeImpactHigh
	for name, patch := range map[string]dto.UpdateChangeRequest{
		"type": {Type: &typ}, "justification": {Justification: &text},
		"risk": {RiskLevel: &risk}, "impact": {ImpactScope: &impact},
		"implementation": {ImplementationPlan: &text}, "rollback": {RollbackPlan: &text},
		"cis": {AffectedCIs: []string{"changed-ci"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.owner.ApplyMetadata(f.ctx, changedomain.MetadataCommand{Meta: f.command("metadata", "assessed-edit-"+name).Meta, ChangeID: f.c.ID, Patch: patch})
			require.Error(t, err)
			after := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, before.UpdatedAt, after.UpdatedAt)
			require.Equal(t, audits, f.client.AuditLog.Query().CountX(f.ctx))
			current := f.client.Change.GetX(f.ctx, f.c.ID)
			require.Equal(t, assessed.AssessmentDigest, current.AssessmentDigest)
			require.Equal(t, assessed.ImplementationPlan, current.ImplementationPlan)
			require.Equal(t, assessed.RollbackPlan, current.RollbackPlan)
			require.Equal(t, assessed.Justification, current.Justification)
			require.Equal(t, assessed.Type, current.Type)
			require.Equal(t, assessed.RiskLevel, current.RiskLevel)
			require.Equal(t, assessed.ImpactScope, current.ImpactScope)
			require.Equal(t, assessed.AffectedCis, current.AffectedCis)
		})
	}
	title := "  CAB title correction  "
	metadata := changedomain.MetadataCommand{Meta: f.command("metadata", "cab-title").Meta, ChangeID: f.c.ID, Patch: dto.UpdateChangeRequest{Title: &title, ImplementationPlan: &assessed.ImplementationPlan, AffectedCIs: []string{"ci-a", "ci-z"}}}
	edited, err := f.owner.ApplyMetadata(f.ctx, metadata)
	require.NoError(t, err, "unchanged assessed facts may accompany a harmless title correction")
	require.Equal(t, before.Version+1, edited.Version)
	title = "CAB title correction"
	replayed, err := f.owner.ApplyMetadata(f.ctx, metadata)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, edited.Version, replayed.Version)

	metadataActor := f.actor
	actor := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("writer").SetName("writer").SetEmail("writer@example.test").SetPasswordHash("test").SetRole("agent").SetActive(true).SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("agent").SetName("Writer").SetIsActive(true).SaveX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("change:write").SetName("Write").SetResource("change").SetAction("write").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	task = f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_CABApproval")).OnlyX(f.ctx)
	task.Update().SetAssignee(fmt.Sprint(actor.ID)).ExecX(f.ctx)
	f.actor = actor
	_, err = f.owner.CompleteChangeTask(f.ctx, changedomain.TaskCommand{Command: f.command("approve", "cab-http"), TaskID: task.TaskID})
	require.Error(t, err, "approval authority must reject before accepting task")
	require.NotEqual(t, "completed", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
	require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
	approvePermission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("change:approve").SetName("Approve").SetResource("change").SetAction("approve").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(approvePermission.ID).SaveX(f.ctx)
	cmd := changedomain.TaskCommand{Command: f.command("approve", "cab-http"), TaskID: task.TaskID}
	result, err := f.owner.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, 200, result.HTTPStatus())
	require.Equal(t, "approved", result.Result.Status)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
	decision := f.client.ProcessApprovalDecision.Query().OnlyX(f.ctx)
	require.Equal(t, actor.ID, decision.ActorID)
	require.Equal(t, task.ID, decision.ProcessTaskID)
	require.Equal(t, 1, f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Schedule"), processtask.Status("created")).CountX(f.ctx))
	metadataActor.Update().SetActive(false).ExecX(f.ctx)
	_, err = f.owner.ApplyMetadata(f.ctx, metadata)
	require.Error(t, err, "normalized immutable replay still requires current authorization")

	role.Update().SetIsActive(false).ExecX(f.ctx)
	_, err = f.owner.CompleteChangeTask(f.ctx, cmd)
	require.Error(t, err)
	_, err = f.owner.GetTaskProgress(f.ctx, changedomain.TaskProgressQuery{Meta: cmd.Meta, ChangeID: f.c.ID, Action: "approve"})
	require.Error(t, err)
}

func TestWorkItemChangeTaskPendingAndRollback(t *testing.T) {
	for _, mode := range []string{"pending", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
			require.NoError(t, err)
			f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
			f.apply(t, f.command("submit", "submit"))
			task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Assessment")).OnlyX(f.ctx)
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			f.runtime.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if mutation, ok := m.(*ent.AuditLogMutation); ok {
						action, _ := mutation.Action()
						if (mode == "pending" && action == "change.assess") || (mode == "rollback" && action == "change.task_completion") {
							return nil, errors.New("injected audit failure")
						}
					}
					return next.Mutate(ctx, m)
				})
			})
			cmd := changedomain.TaskCommand{Command: f.command("assess", "task-http"), TaskID: task.TaskID}
			result, err := f.owner.CompleteChangeTask(f.ctx, cmd)
			if mode == "rollback" {
				require.Error(t, err)
				require.NotEqual(t, "completed", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
				require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
				return
			}
			require.NoError(t, err)
			require.Nil(t, result.Result)
			require.Equal(t, "pending", result.Progress)
			after := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			require.Equal(t, before.Version, after.Version)
			require.True(t, before.UpdatedAt.Equal(after.UpdatedAt), "acceptance must not alter public updatedAt")
			require.Equal(t, before.Status, after.Status)
			require.Equal(t, 202, result.HTTPStatus())
			title := "edit during callback"
			_, err = f.owner.ApplyMetadata(f.ctx, changedomain.MetadataCommand{Meta: f.command("metadata", "blocked-edit").Meta, ChangeID: f.c.ID, Patch: dto.UpdateChangeRequest{Title: &title}})
			require.Error(t, err, "metadata cannot invalidate accepted callback")
			another := cmd
			another.Meta.OperationID += "-new"
			_, err = f.owner.CompleteChangeTask(f.ctx, another)
			require.ErrorContains(t, err, "prior callback")
			row := f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx)
			row.Update().SetStatus("processing").ExecX(f.ctx)
			reading, err := f.owner.GetTaskProgress(f.ctx, changedomain.TaskProgressQuery{Meta: cmd.Meta, ChangeID: f.c.ID, Action: "assess"})
			require.NoError(t, err)
			require.Equal(t, 202, reading.HTTPStatus())
			require.Equal(t, "processing", reading.Progress)
			row.Update().SetStatus("blocked").SetLastErrorClass("handler_contract").ExecX(f.ctx)
			result, err = f.owner.CompleteChangeTask(f.ctx, cmd)
			require.NoError(t, err)
			require.Equal(t, 409, result.HTTPStatus())
			require.Equal(t, "handler_contract", result.Reason)
		})
	}
}

func TestWorkItemChangeTaskConcurrentReceipt(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
			require.NoError(t, err)
			f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
			f.apply(t, f.command("submit", "submit"))
			task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Assessment")).OnlyX(f.ctx)
			cmd := changedomain.TaskCommand{Command: f.command("assess", "concurrent"), TaskID: task.TaskID}
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
					_, err := f.owner.CompleteChangeTask(f.ctx, copy)
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
			if same {
				require.Equal(t, 2, success, "same-key contender returns existing progress")
			} else {
				require.Equal(t, 1, success)
			}
			require.Equal(t, 3, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
			require.Equal(t, 1, f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ProcessTaskID(task.ID)).CountX(f.ctx))
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.task_completion")).CountX(f.ctx))
		})
	}
}

func TestWorkItemChangeOwnersMSPAndVersionRefresh(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
	require.NoError(t, err)
	f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
	f.apply(t, f.command("submit", "submit"))
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("owner-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("provider").SetName("provider").SetEmail("provider@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP tech").SetIsActive(true).SaveX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("change:write").SetName("Write").SetResource("change").SetAction("write").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	f.actor = actor
	title := "MSP metadata"
	meta := changedomain.MetadataCommand{Meta: f.command("metadata", "msp-meta").Meta, ChangeID: f.c.ID, Patch: dto.UpdateChangeRequest{Title: &title}}
	result, err := f.owner.ApplyMetadata(f.ctx, meta)
	require.NoError(t, err)
	require.Equal(t, 3, result.Version)
	task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Assessment")).OnlyX(f.ctx)
	task.Update().SetAssignee(fmt.Sprint(actor.ID)).ExecX(f.ctx)
	cmd := changedomain.TaskCommand{Command: f.command("assess", "msp-task"), TaskID: task.TaskID}
	stale := cmd
	stale.Meta.ExpectedVersion = 2
	_, err = f.owner.CompleteChangeTask(f.ctx, stale)
	require.Error(t, err)
	require.NotEqual(t, "completed", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
	progress, err := f.owner.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, progress.Result)
	require.Equal(t, 4, progress.Result.Version)
	row := f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ProcessTaskID(task.ID)).OnlyX(f.ctx)
	require.Equal(t, actor.ID, row.ActorID)
	require.Equal(t, actor.ID, f.client.Change.GetX(f.ctx, f.c.ID).AssessedBy)
	for _, mode := range []string{"allocation", "permission", "foreign", "forged"} {
		allocation.Update().ClearDeassignedAt().ExecX(f.ctx)
		role.Update().SetIsActive(true).ExecX(f.ctx)
		actor.Update().SetMspRole("provider_agent").ExecX(f.ctx)
		invalidMeta := meta
		invalidTask := cmd
		switch mode {
		case "allocation":
			allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
		case "permission":
			role.Update().SetIsActive(false).ExecX(f.ctx)
		case "foreign":
			actor.Update().ClearMspRole().ExecX(f.ctx)
		case "forged":
			invalidMeta.Meta.ActorID = 999999
			invalidTask.Meta.ActorID = 999999
		}
		_, err = f.owner.ApplyMetadata(f.ctx, invalidMeta)
		require.Error(t, err, mode)
		_, err = f.owner.CompleteChangeTask(f.ctx, invalidTask)
		require.Error(t, err, mode)
		_, err = f.owner.GetTaskProgress(f.ctx, changedomain.TaskProgressQuery{Meta: invalidTask.Meta, ChangeID: f.c.ID, Action: "assess"})
		require.Error(t, err, mode)
	}
	require.Equal(t, 4, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
}

func TestWorkItemChangeTaskInputAndReceiptIdentity(t *testing.T) {
	for _, mode := range []string{"self_approval", "decision_id", "empty_evidence", "wrong_receipt", "wrong_instance", "foreign_task", "unrelated_instance", "wrong_action"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
			require.NoError(t, err)
			f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
			f.apply(t, f.command("submit", "submit"))
			task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Assessment")).OnlyX(f.ctx)
			cmd := changedomain.TaskCommand{Command: f.command("assess", "task-http"), TaskID: task.TaskID}
			if mode == "foreign_task" {
				tenant := f.client.Tenant.Create().SetCode("foreign-task").SetName("foreign").SaveX(f.ctx)
				task.Update().SetTenantID(tenant.ID).ExecX(f.ctx)
			}
			if mode == "unrelated_instance" {
				f.client.ProcessInstance.UpdateOneID(task.ProcessInstanceID).SetBusinessID(f.c.WorkItemID + 999).ExecX(f.ctx)
			}
			if mode == "wrong_action" {
				cmd.Action = "implement"
			}
			if mode == "decision_id" {
				cmd.ApprovalDecisionID = 999
			}
			if mode == "empty_evidence" {
				cmd.Evidence = "   "
			}
			if mode == "self_approval" {
				_, err = f.owner.CompleteChangeTask(f.ctx, cmd)
				require.NoError(t, err)
				task = f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_CABApproval")).OnlyX(f.ctx)
				task.Update().SetAssignee(fmt.Sprint(f.actor.ID)).ExecX(f.ctx)
				cmd = changedomain.TaskCommand{Command: f.command("approve", "cab-http"), TaskID: task.TaskID}
			}
			if mode == "wrong_receipt" || mode == "wrong_instance" {
				result, err := f.owner.CompleteChangeTask(f.ctx, cmd)
				require.NoError(t, err)
				if mode == "wrong_receipt" {
					require.ErrorContains(t, f.client.AuditLog.Update().Where(auditlog.OperationID(result.ExecutionKey)).SetResultVersion(99).Exec(f.ctx), "immutable work item audit fact")
					return
				} else {
					f.client.ProcessCallbackOutbox.Update().Where(processcallbackoutbox.ExecutionKey(result.ExecutionKey)).SetProcessInstanceID(999999).ExecX(f.ctx)
				}
				_, err = f.owner.GetTaskProgress(f.ctx, changedomain.TaskProgressQuery{Meta: cmd.Meta, ChangeID: f.c.ID, Action: "assess"})
				require.Error(t, err, "invalid durable identity cannot be projected as success")
				return
			}
			_, err = f.owner.CompleteChangeTask(f.ctx, cmd)
			require.Error(t, err, mode)
			require.NotEqual(t, "completed", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
			require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
		})
	}
}

func TestWorkItemChangeTaskMetadataRace(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
	require.NoError(t, err)
	f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
	f.apply(t, f.command("submit", "submit"))
	task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Assessment")).OnlyX(f.ctx)
	cmd := changedomain.TaskCommand{Command: f.command("assess", "task-race"), TaskID: task.TaskID}
	title := "concurrent edit"
	meta := changedomain.MetadataCommand{Meta: f.command("metadata", "edit-race").Meta, ChangeID: f.c.ID, Patch: dto.UpdateChangeRequest{Title: &title}}
	synchronizeChangeOwnerReads(t, f)
	taskResult := make(chan error, 1)
	editResult := make(chan error, 1)
	go func() { _, err := f.owner.CompleteChangeTask(f.ctx, cmd); taskResult <- err }()
	go func() { _, err := f.owner.ApplyMetadata(f.ctx, meta); editResult <- err }()
	taskErr, editErr := <-taskResult, <-editResult
	require.NotEqual(t, taskErr == nil, editErr == nil, "acceptance and metadata using the same version cannot both commit")
	require.Equal(t, 3, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
	if taskErr == nil {
		require.Equal(t, "completed", f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ProcessTaskID(task.ID)).OnlyX(f.ctx).Status)
	} else {
		require.NotEqual(t, "completed", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
		require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
	}
}
