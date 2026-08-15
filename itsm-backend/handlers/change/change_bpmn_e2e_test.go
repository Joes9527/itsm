package change_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processinstance"
	"itsm-backend/handlers/change"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

// newTestBPMNEngine 构造真实的 *CustomProcessEngine（通过 service.NewCustomProcessEngine
// 导出构造函数，返回值已经是 service.ProcessEngine 接口）。这是
// handlers/change/service_bpmn_bridge_test.go 里同名 internal-package helper 的一个
// 局部等价物：那个 helper 定义在 package change（未导出符号在 change_test 外部包里不可见），
// 这个文件属于外部测试包 change_test，所以在这里重新声明一份，行为完全一致。
func newTestBPMNEngine(t *testing.T, client *ent.Client, logger *zap.SugaredLogger) service.ProcessEngine {
	t.Helper()
	return service.NewCustomProcessEngine(client, logger)
}

// TestChangeApprovalE2E_FullApproveFlow 完整走一遍：提交审批 -> 触发 BPMN ->
// 完成变更评估 -> CAB 审批通过 -> 断言 Change.Status/流程实例状态/审批历史
// 全部符合预期，不留孤儿任务。
func TestChangeApprovalE2E_FullApproveFlow(t *testing.T) {
	dsn := "file:change_e2e_approve?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	defer client.Close()
	// MarkSubmittedForApproval (called by SubmitChange) uses raw database/sql, not ent —
	// 照抄 controller/problem_investigation_controller_test.go 的做法：另开一个指向
	// 同一个 sqlite 内存库（相同 DSN + mode=memory&cache=shared）的 *sql.DB 连接。
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	defer db.Close()
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("E2E Tenant").SetCode("e2e-approve").SetDomain("e2e-approve.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("requester").SetEmail("requester@example.com").SetName("Requester").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	// change_manager 是 User.Role 上的平铺字段（这个代码库没有单独的 UserRole 关联表），
	// authorizeTaskActor 靠 resolveRoleCandidates 按 tenant + role="change_manager" 查询候选人——
	// 照抄 handlers/change/service_bpmn_bridge_test.go 里 setupChangeForTransitionStatusTest 的做法。
	cmUser, err := client.User.Create().SetUsername("cm").SetEmail("cm@example.com").SetName("CM").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	deploySvc := service.NewBPMNTemplateService(client)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	trigger := service.NewProcessTriggerService(client, engine)

	repo := change.NewEntRepository(client, db)
	svc := change.NewService(repo, client, logger)
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	created, err := repo.Create(ctx, &change.Change{Title: "端到端测试变更", Type: "normal", Status: "draft", RiskLevel: "medium", ImpactScope: "low", TenantID: tenant.ID, CreatedBy: requester.ID})
	require.NoError(t, err)

	_, err = svc.SubmitChange(tenantCtx, created.ID, tenant.ID, requester.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{cmUser.ID}})
	require.NoError(t, err)

	afterSubmit, err := repo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", afterSubmit.Status)

	// SubmitChange 已经自动级联完成了 Activity_Assessment（变更评估），流程直接
	// 停在 CAB 审批节点上，不需要再手动完成评估任务。

	// CAB 审批通过
	_, err = svc.TransitionStatus(tenantCtx, created.ID, tenant.ID, cmUser.ID, "approved", "e2e 测试通过")
	require.NoError(t, err)

	final, err := repo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	// normal 类型的状态机里 approved 不是终点（approved -> {scheduled, cancelled}，没有直接
	// approved -> in_progress），scheduleChange 必须两跳推进到 scheduled，见 Finding 4。
	assert.Equal(t, "scheduled", final.Status)

	history, err := svc.GetApprovalHistory(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	// history[0].Status 是 CAB 审批决定本身的记录（approve/reject），跟 Change.Status 是两回事，
	// 不受 Finding 4 影响，依然是 "approved"。
	assert.Equal(t, "approved", history[0].Status)
	assert.Equal(t, cmUser.ID, history[0].ApproverID)

	instance, err := client.ProcessInstance.Query().Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", created.ID)), processinstance.TenantID(tenant.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "running", instance.Status, "流程实例应该还在运行，停在 Activity_Implement——这是预期，不是 bug")
	assert.Equal(t, "Activity_Implement", instance.CurrentActivityID)

	// 重复提交应该被拒绝（当前状态已经是 approved，不是 draft，SubmitChange 本身就会拒绝；
	// 这条断言同时验证了业务层的旧校验依然生效，没有被这次改动破坏）
	_, err = svc.SubmitChange(tenantCtx, created.ID, tenant.ID, requester.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{cmUser.ID}})
	require.Error(t, err)
}

// TestChangeApprovalE2E_FullRejectFlow 同样结构，走驳回分支：断言 Change.Status=="rejected"，
// ProcessInstance.Status=="completed"（驳回节点走 Flow_End 直接结束，流程实例正确终止，
// 不会像 approve 分支那样停在 running）。
func TestChangeApprovalE2E_FullRejectFlow(t *testing.T) {
	dsn := "file:change_e2e_reject?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	defer client.Close()
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	defer db.Close()
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("E2E Tenant Reject").SetCode("e2e-reject").SetDomain("e2e-reject.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("requester2").SetEmail("requester2@example.com").SetName("Requester2").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm-reject").SetEmail("cm-reject@example.com").SetName("CM Reject").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	deploySvc := service.NewBPMNTemplateService(client)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	trigger := service.NewProcessTriggerService(client, engine)

	repo := change.NewEntRepository(client, db)
	svc := change.NewService(repo, client, logger)
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	created, err := repo.Create(ctx, &change.Change{Title: "端到端测试变更-驳回", Type: "normal", Status: "draft", RiskLevel: "medium", ImpactScope: "low", TenantID: tenant.ID, CreatedBy: requester.ID})
	require.NoError(t, err)

	_, err = svc.SubmitChange(tenantCtx, created.ID, tenant.ID, requester.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{cmUser.ID}})
	require.NoError(t, err)

	// SubmitChange 已经自动级联完成了 Activity_Assessment，流程直接停在 CAB 审批节点上。

	// comment 为空必须被拒绝，且不改变状态
	_, err = svc.TransitionStatus(tenantCtx, created.ID, tenant.ID, cmUser.ID, "rejected", "")
	require.Error(t, err)

	_, err = svc.TransitionStatus(tenantCtx, created.ID, tenant.ID, cmUser.ID, "rejected", "风险评估不通过")
	require.NoError(t, err)

	final, err := repo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", final.Status)

	history, err := svc.GetApprovalHistory(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "rejected", history[0].Status)

	instance, err := client.ProcessInstance.Query().Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", created.ID)), processinstance.TenantID(tenant.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "completed", instance.Status, "驳回节点走 Flow_End 直接结束，流程实例应该正确终止，不会像 approve 分支那样停在 running")
}

// TestChangeApprovalE2E_NonCMUserCannotApprove 断言非 change_manager 角色的用户
// 调用 TransitionStatus approve 会失败，Change.Status 不变。
func TestChangeApprovalE2E_NonCMUserCannotApprove(t *testing.T) {
	dsn := "file:change_e2e_wrong_actor?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	defer client.Close()
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	defer db.Close()
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("E2E Tenant WrongActor").SetCode("e2e-wrong-actor").SetDomain("e2e-wrong-actor.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("requester3").SetEmail("requester3@example.com").SetName("Requester3").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm-wa").SetEmail("cm-wa@example.com").SetName("CM WA").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	outsider, err := client.User.Create().SetUsername("outsider-e2e").SetEmail("outsider-e2e@example.com").SetName("Outsider").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	deploySvc := service.NewBPMNTemplateService(client)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	trigger := service.NewProcessTriggerService(client, engine)

	repo := change.NewEntRepository(client, db)
	svc := change.NewService(repo, client, logger)
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	created, err := repo.Create(ctx, &change.Change{Title: "端到端测试变更-越权", Type: "normal", Status: "draft", RiskLevel: "medium", ImpactScope: "low", TenantID: tenant.ID, CreatedBy: requester.ID})
	require.NoError(t, err)

	_, err = svc.SubmitChange(tenantCtx, created.ID, tenant.ID, requester.ID, &dto.SubmitChangeRequest{ApproverIDs: []int{cmUser.ID}})
	require.NoError(t, err)

	// SubmitChange 已经自动级联完成了 Activity_Assessment，流程直接停在 CAB 审批节点上。

	_, err = svc.TransitionStatus(tenantCtx, created.ID, tenant.ID, outsider.ID, "approved", "我批准")
	require.Error(t, err, "outsider 没有 change_manager 角色，authorizeTaskActor 应该拒绝")

	final, err := repo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", final.Status, "越权调用失败后状态不应该变化")
}
