package change

import (
	"context"
	"database/sql"
	"fmt"
	executionfixture "itsm-backend/tests/fixtures/execution"

	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/enttest"

	"itsm-backend/ent/ticket"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

var testTicketNumberSeq int64

func nextTestTicketNumber() string {
	n := atomic.AddInt64(&testTicketNumberSeq, 1)
	return fmt.Sprintf("TKT-TEST-%08d", n)
}

func newTestChangeRepository(client *ent.Client, db *sql.DB) *EntRepository {
	return NewEntRepository(client, db)
}
func createChangeWorkItemFixture(t *testing.T, client *ent.Client, tenantID, requesterID int, title string, statuses ...string) *ent.Ticket {
	t.Helper()
	status := "draft"
	if len(statuses) > 0 {
		status = statuses[0]
	}
	workItem, err := client.Ticket.Create().
		SetTitle(title).
		SetStatus(status).
		SetRecordClass("change_request").
		SetPriority("medium").
		SetTicketNumber(nextTestTicketNumber()).
		SetRequesterID(requesterID).
		SetOpenedByID(requesterID).
		SetTenantID(tenantID).
		Save(context.Background())
	require.NoError(t, err)
	return workItem
}

func newChangeBPMNEntClient(t *testing.T, dbName string) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", dbName))
	t.Cleanup(func() { client.Close() })
	return client
}
func openChangeBPMNRawDB(t *testing.T, dbName string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", dbName))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func setupChangeBPMNActor(t *testing.T, client *ent.Client, code string) (tenantID, actorID int) {
	t.Helper()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Change BPMN Tenant " + code).
		SetCode("chg-bridge-" + code).
		SetDomain("chg-bridge-" + code + ".example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	actor, err := client.User.Create().
		SetUsername("chg-approver-" + code).
		SetEmail("chg-approver-" + code + "@example.com").
		SetName("Change Approver " + code).
		SetPasswordHash("hash").
		SetRole("agent").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	return tenant.ID, actor.ID
}
func TestCompleteChangeApprovalTask_ApproveCompletesScheduleNode(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	cmd := f.taskCommand(t, "approve", f.approver)
	result, err := f.svc.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	require.Equal(t, "approved", result.Result.Status)
	require.Equal(t, "Activity_Schedule", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))

}
func TestCompleteChangeApprovalTask_RejectEndsProcess(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	result, err := f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, "reject", f.approver))
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	require.Equal(t, "rejected", result.Result.Status)
	require.Equal(t, "completed", f.client.ProcessInstance.Query().OnlyX(f.ctx).Status)

}
func TestCompleteChangeApprovalTask_WrongActorRejected(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	outsider := f.client.User.Create().SetTenantID(f.tenant).SetUsername("outsider").SetName("Outsider").SetEmail("outsider@example.test").SetPasswordHash("test").SetRole("agent").SetActive(true).SaveX(f.ctx)
	_, err := f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, "approve", outsider.ID))
	require.Error(t, err)
	require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))

}
func TestCompleteChangeApprovalTask_FiltersDecoyTaskByDefinitionKey(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	assessment := f.taskCommand(t, "assess", f.requester)
	bad := assessment
	bad.Action = "approve"
	bad.Meta.ActorID = f.approver
	_, err := f.svc.CompleteChangeTask(f.ctx, bad)
	require.Error(t, err)
	require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
	require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
	result, err := f.svc.CompleteChangeTask(f.ctx, assessment)
	require.NoError(t, err)
	require.NotNil(t, result.Result)

}
func TestCompleteChangeApprovalTask_ResumesCascadeAfterInterruptedCall(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	fail := true
	f.client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if p, ok := m.(*ent.ProcessInstanceMutation); ok {
				if activity, exists := p.CurrentActivityID(); exists && activity == "Activity_Schedule" && fail {
					return nil, fmt.Errorf("continuation unavailable")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	cmd := f.taskCommand(t, "approve", f.approver)
	result, err := f.svc.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	require.Equal(t, "pending", result.Progress)
	require.Equal(t, "approved", result.Result.Status)
	fail = false
	f.client.ProcessCallbackOutbox.Update().SetNextAttemptAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
	_, err = f.engine.ProcessPendingCallbacks(f.ctx, "retry", 10)
	require.NoError(t, err)
	again, err := f.svc.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, "completed", again.Progress)
	require.Equal(t, result.Result.Version, again.Result.Version)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))

}
func TestCompleteChangeApprovalTask_RetryAfterFullSuccessIsNoop(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	cmd := f.taskCommand(t, "approve", f.approver)
	result, err := f.svc.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	require.Equal(t, "approved", result.Result.Status)
	require.Equal(t, "Activity_Schedule", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
	again, err := f.svc.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.True(t, again.Result.Replayed)
	require.Equal(t, result.Result.Version, again.Result.Version)
	require.Equal(t, result.ExecutionKey, again.ExecutionKey)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))

}
func TestCompleteChangeApprovalTask_RetryWithMismatchedActionRejected(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	cmd := f.taskCommand(t, "approve", f.approver)
	result, err := f.svc.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	require.Equal(t, "approved", result.Result.Status)
	require.Equal(t, "Activity_Schedule", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
	cmd.Action = "reject"
	_, err = f.svc.CompleteChangeTask(f.ctx, cmd)
	require.Error(t, err)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
}

func TestTransitionStatus_Approve_UsesCompleteChangeApprovalTask(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	cmd := f.taskCommand(t, "approve", f.approver)
	result, err := f.svc.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	require.Equal(t, "approved", result.Result.Status)
	require.Equal(t, "Activity_Schedule", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))

}

func TestTransitionStatus_Reject_RequiresComment(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	cmd := f.taskCommand(t, "reject", f.approver)
	cmd.Evidence = ""
	_, err := f.svc.CompleteChangeTask(f.ctx, cmd)
	require.Error(t, err)
	require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
}

func TestTransitionStatus_Approve_WrongActorRejected(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	outsider := f.client.User.Create().SetTenantID(f.tenant).SetUsername("outsider").SetName("Outsider").SetEmail("outsider@example.test").SetPasswordHash("test").SetRole("agent").SetActive(true).SaveX(f.ctx)
	_, err := f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, "approve", outsider.ID))
	require.Error(t, err)
	require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))

}
func TestTransitionStatus_Approve_NoRunningProcessInstanceFailsClosed(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	_, err := f.svc.CompleteChangeTask(f.ctx, TaskCommand{Command: f.command("implement", f.requester), TaskID: "missing"})
	require.Error(t, err)
	require.Equal(t, "draft", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))

}

func TestSubmitChange_AutoCompletesAssessmentTask(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	require.Equal(t, "change_normal_flow", instance.ProcessDefinitionKey)
	require.Equal(t, "Activity_Assessment", instance.CurrentActivityID)
	require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))

}
func TestChangeServiceTaskHandler_CreateChange_DelegatesToRealServiceAndCreatesWorkItem(t *testing.T) {
	client := newChangeBPMNEntClient(t, "change_bpmn_handler_create_real")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()
	tenantID, actorID := setupChangeBPMNActor(t, client, "bpmn-handler-create")

	repo := newTestChangeRepository(client, openChangeBPMNRawDB(t, "change_bpmn_handler_create_real"))
	svc := NewService(repo, client, logger, executionfixture.Standard())
	ConfigureChangeIntakeFixture(ctx, client, tenantID, "agent")
	app := NewChangeIntakeApp(client, svc, logger)

	engine := service.NewCustomProcessEngine(client, logger).(*service.CustomProcessEngine)
	engine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler).SetCreationApplication(app, client)
	source := client.Ticket.Create().SetTenantID(tenantID).SetRequesterID(actorID).SetOpenedByID(actorID).SetTitle("BPMN 源工单").SetTicketNumber("SOURCE-CREATE").SetRecordClass("generic").SetStatus("open").SetPriority("medium").SaveX(ctx)

	deployment := client.ProcessDeployment.Create().SetTenantID(tenantID).SetDeploymentID("change-creation").SetDeploymentName("Change Creation").SaveX(ctx)
	xml := []byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="change-creation" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:serviceTask id="create"><bpmn:extensionElements><bpmn:metaData name="service_task_type">change_task</bpmn:metaData><bpmn:metaData name="action">create_change</bpmn:metaData></bpmn:extensionElements></bpmn:serviceTask><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="a" sourceRef="start" targetRef="create"/><bpmn:sequenceFlow id="b" sourceRef="create" targetRef="end"/></bpmn:process></bpmn:definitions>`)
	definition := client.ProcessDefinition.Create().SetTenantID(tenantID).SetDeploymentID(deployment.ID).SetKey("change-creation").SetName("Change Creation").SetBpmnXML(xml).SetIsActive(true).SaveX(ctx)

	runCtx := service.WithTrustedBPMNTenantContext(ctx, tenantID)
	runCtx = context.WithValue(runCtx, bpmn.BPMNUserIDContextKey, actorID)
	_, err := engine.StartProcessByDefinitionID(runCtx, service.FreezeProcessDefinition(definition), fmt.Sprintf("generic:%d", source.ID), "generic", source.ID, map[string]any{
		"title":         "BPMN 自动创建的变更",
		"description":   "验证委托到真实领域服务后同步建好 WorkItem",
		"type":          "normal",
		"priority":      "medium",
		"created_by":    actorID,
		"justification": "Apply approved service configuration", "impact_scope": "low", "risk_level": "medium", "implementation_plan": "Save configuration, apply update and verify", "rollback_plan": "Restore saved configuration",
	}, "source-start")
	require.NoError(t, err)
	require.Equal(t, 2, client.Ticket.Query().CountX(ctx))

	changeEntity := client.Change.Query().Where(change.HasWorkItemWith(ticket.TenantID(tenantID))).OnlyX(ctx)
	require.NotNil(t, changeEntity.WorkItemID,
		"通过 BPMN create_change 路径自动创建的 Change 也必须有关联的 WorkItem，不能绕过事务化建表")

	workItem, err := client.Ticket.Get(ctx, changeEntity.WorkItemID)
	require.NoError(t, err)
	assert.Equal(t, "change_request", workItem.RecordClass, "recordClass 必须是 change_request，不是 change")
	assert.Equal(t, "BPMN 自动创建的变更", workItem.Title)
	assert.Equal(t, tenantID, workItem.TenantID)
}
