package change

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/processtask"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	"os"
	"strings"
	"testing"
)

type governedChangeFixture struct {
	ctx                         context.Context
	client                      *ent.Client
	svc                         *Service
	engine                      *service.CustomProcessEngine
	tenant, requester, approver int
	record                      *ent.Change
	seq                         int
}

func newGovernedChangeFixture(t *testing.T, kind string) *governedChangeFixture {
	t.Helper()
	client := newChangeBPMNEntClient(t, "governed_"+strings.ReplaceAll(t.Name(), "/", "_"))
	tenant, requester := setupChangeBPMNActor(t, client, strings.ReplaceAll(t.Name(), "/", "_"))
	ctx := tenantctx.WithTenantID(context.Background(), tenant)
	// Existing production raw risk-detail schema, declared explicitly for SQLite fixtures.
	_, schemaErr := client.ExecContext(ctx, `CREATE TABLE change_risk_assessments(id INTEGER PRIMARY KEY,change_id INTEGER,tenant_id INTEGER,risk_level TEXT NOT NULL DEFAULT 'medium',risk_description TEXT,impact_analysis TEXT,mitigation_measures TEXT,contingency_plan TEXT,risk_owner TEXT,risk_review_date DATETIME,created_at DATETIME,updated_at DATETIME)`)
	require.NoError(t, schemaErr)
	client.User.UpdateOneID(requester).SetRole("super_admin").ExecX(ctx)
	approver := client.User.Create().SetTenantID(tenant).SetUsername("governed-cab").SetName("CAB").SetEmail("cab@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(ctx)
	role := client.Role.Create().SetTenantID(tenant).SetCode("change_manager").SetName("CAB").SaveX(ctx)
	approver.Update().AddRoleIDs(role.ID).ExecX(ctx)
	engine := service.NewCustomProcessEngine(client, zap.NewNop().Sugar()).(*service.CustomProcessEngine)
	svc := NewService(NewEntRepository(client, nil), client, zap.NewNop().Sugar())
	svc.SetProcessEngine(engine)
	deployment := client.ProcessDeployment.Create().SetTenantID(tenant).SetDeploymentID("governed").SetDeploymentName("Governed").SaveX(ctx)
	for _, key := range []string{"change_normal_flow", "change_emergency_flow"} {
		xml, err := os.ReadFile("../../service/bpmn/" + key + ".bpmn")
		require.NoError(t, err)
		client.ProcessDefinition.Create().SetTenantID(tenant).SetDeploymentID(deployment.ID).SetKey(key).SetName(key).SetBpmnXML(xml).SetIsActive(true).SaveX(ctx)
	}
	item := createChangeWorkItemFixture(t, client, tenant, requester, "Governed change")
	record := client.Change.Create().SetWorkItemID(item.ID).SetType(kind).SetImplementationPlan("deploy").SetRollbackPlan("restore").SaveX(ctx)
	return &governedChangeFixture{ctx: ctx, client: client, svc: svc, engine: engine, tenant: tenant, requester: requester, approver: approver.ID, record: record}
}
func (f *governedChangeFixture) command(action string, actor int) Command {
	f.seq++
	return Command{Meta: workitemmutation.Meta{TenantID: f.tenant, ActorID: actor, ExpectedVersion: f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Version, OperationID: fmt.Sprintf("%s-%d", action, f.seq), Source: "http"}, ChangeID: f.record.ID, Action: action, Evidence: "Observed evidence"}
}
func (f *governedChangeFixture) submit(t *testing.T) {
	t.Helper()
	_, err := f.svc.ApplyCommand(f.ctx, f.command("submit", f.requester))
	require.NoError(t, err)
}
func (f *governedChangeFixture) taskCommand(t *testing.T, action string, actor int) TaskCommand {
	t.Helper()
	task := f.client.ProcessTask.Query().Where(processtask.CallbackAction(changeTaskActions[action]), processtask.StatusNEQ("completed"), processtask.StatusNEQ("cancelled")).OnlyX(f.ctx)
	return TaskCommand{Command: f.command(action, actor), TaskID: task.TaskID}
}
func (f *governedChangeFixture) assess(t *testing.T) {
	t.Helper()
	result, err := f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, "assess", f.requester))
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	require.Equal(t, "completed", result.Progress)
}
