package main

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/workitemrelation"
	changedomain "itsm-backend/handlers/change"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

// testDBCounter/testDSN 为每个测试返回唯一的 SQLite 内存数据库 DSN，避免测试间数据库残留
// （与 cmd/backfill_problem_work_item/main_test.go 同一做法）。
var testDBCounter int64

func testDSN(name string) string {
	return fmt.Sprintf("file:backfill_change_wi_%s_%d?mode=memory&cache=shared&_fk=1", name, atomic.AddInt64(&testDBCounter, 1))
}

func setupTenantAndUsers(t *testing.T, client *ent.Client, ctx context.Context, code string) (tenant *ent.Tenant, requester, cmUser *ent.User) {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName("T-" + code).SetCode(code).SetDomain(code + ".example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	requester, err = client.User.Create().
		SetUsername("requester-" + code).SetEmail("requester-" + code + "@example.com").
		SetName("Requester").SetPasswordHash("hash").SetRole("agent").
		SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	cmUser, err = client.User.Create().
		SetUsername("cm-" + code).SetEmail("cm-" + code + "@example.com").
		SetName("CM").SetPasswordHash("hash").SetRole("change_manager").
		SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	return tenant, requester, cmUser
}

// TestFindCandidates_OnlyMissingWorkItemID 锁定候选口径：只有 work_item_id 为空的 Change
// 才进候选；已经回填过的、以及指定租户过滤都要生效。
func TestFindCandidates_OnlyMissingWorkItemID(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN("candidates"))
	defer client.Close()
	ctx := context.Background()

	tenantA, requesterA, _ := setupTenantAndUsers(t, client, ctx, "cand-a")
	tenantB, requesterB, _ := setupTenantAndUsers(t, client, ctx, "cand-b")

	legacyA, err := client.Change.Create().
		SetTitle("存量变更-缺WorkItem-A").SetCreatedBy(requesterA.ID).SetTenantID(tenantA.ID).
		Save(ctx)
	require.NoError(t, err)

	legacyB, err := client.Change.Create().
		SetTitle("存量变更-缺WorkItem-B").SetCreatedBy(requesterB.ID).SetTenantID(tenantB.ID).
		Save(ctx)
	require.NoError(t, err)

	// 已经迁移过（有 work_item_id）的变更——不应该出现在候选里。
	wi, err := client.Ticket.Create().
		SetTitle("已迁移变更对应的WorkItem").SetTicketNumber("TKT-CAND-ALREADY").
		SetRecordClass("change_request").SetRequesterID(requesterA.ID).SetTenantID(tenantA.ID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Change.Create().
		SetTitle("已迁移变更").SetCreatedBy(requesterA.ID).SetTenantID(tenantA.ID).SetWorkItemID(wi.ID).
		Save(ctx)
	require.NoError(t, err)

	t.Run("all tenants", func(t *testing.T) {
		candidates, err := findCandidates(ctx, client, 0)
		require.NoError(t, err)
		ids := make([]int, 0, len(candidates))
		for _, c := range candidates {
			ids = append(ids, c.ID)
		}
		assert.ElementsMatch(t, []int{legacyA.ID, legacyB.ID}, ids)
	})

	t.Run("scoped to one tenant", func(t *testing.T) {
		candidates, err := findCandidates(ctx, client, tenantB.ID)
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		assert.Equal(t, legacyB.ID, candidates[0].ID)
	})
}

// TestBackfillOne_CreatesWorkItemAndMigratesRelatedTickets 覆盖 backfillOne 的基础路径：
// 建 WorkItem、回填 work_item_id，并把 related_tickets 里能解析的编号迁移成
// WorkItemRelation；解析不到的编号跳过、计数，不阻塞整个回填。
func TestBackfillOne_CreatesWorkItemAndMigratesRelatedTickets(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN("related"))
	defer client.Close()
	ctx := context.Background()

	tenant, requester, _ := setupTenantAndUsers(t, client, ctx, "related")

	realTicket, err := client.Ticket.Create().
		SetTitle("真实关联工单").SetTicketNumber("INC-BACKFILL-REAL-1").
		SetRequesterID(requester.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	legacy, err := client.Change.Create().
		SetTitle("存量变更-带关联工单").SetCreatedBy(requester.ID).SetTenantID(tenant.ID).
		SetRelatedTickets([]string{"INC-BACKFILL-REAL-1", "INC-BACKFILL-REAL-1", "TKT-DOES-NOT-EXIST"}).
		Save(ctx)
	require.NoError(t, err)

	result, err := backfillOne(ctx, client, legacy)
	require.NoError(t, err)
	assert.False(t, result.migratedInstance, "没有任何运行中流程实例，不应该被误判为迁移成功")
	assert.Equal(t, 1, result.migratedRelations, "去重后只有一个真实存在的工单编号应该被迁移")
	assert.Equal(t, []string{"TKT-DOES-NOT-EXIST"}, result.skippedTicketNumbers)

	updated, err := client.Change.Get(ctx, legacy.ID)
	require.NoError(t, err)
	require.Positive(t, updated.WorkItemID, "work_item_id 应该已回填")

	rel, err := client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenant.ID),
			workitemrelation.SourceWorkItemID(updated.WorkItemID),
			workitemrelation.TargetWorkItemID(realTicket.ID),
			workitemrelation.RelationType(changeTicketRelationType),
			workitemrelation.DeletedAtIsNil(),
		).
		Exist(ctx)
	require.NoError(t, err)
	assert.True(t, rel, "真实存在的工单应该已经写入 WorkItemRelation")

	// 重复调用（模拟工具重跑）不应该对同一个已回填的 Change 再处理一次。
	candidatesAfter, err := findCandidates(ctx, client, tenant.ID)
	require.NoError(t, err)
	for _, c := range candidatesAfter {
		assert.NotEqual(t, legacy.ID, c.ID, "已经回填过的 Change 不应该再次出现在候选列表里")
	}
}

// TestBackfillOne_MigratesRunningProcessInstanceBusinessKey_EndToEnd 是本次 Change 迁移
// 任务风险最高的一步的正确性证明：模拟一条"迁移前创建、正在走 Track4 审批流程"的存量
// Change——它的运行中 ProcessInstance.business_key 是旧格式 "change:{changeID}"（迁移前的
// SubmitChange 用 Change 自己的主键触发流程）。跑 backfillOne 之后：
//  1. business_key/business_id 应该原地改写成新格式 "change:{workItemID}"；
//  2. 用真实的 handlers/change.Service.TransitionStatus（内部调用
//     completeChangeApprovalTask，通过 resolveWorkItemID 用新格式查找运行中实例）完成 CAB
//     审批，必须能找到并正确推进这条被迁移过的实例——这证明迁移后 Track4 的审批链路径
//     真的能续接上，不是只改了字符串但查询对不上。
func TestBackfillOne_MigratesRunningProcessInstanceBusinessKey_EndToEnd(t *testing.T) {
	dsn := testDSN("e2e_running")
	client := enttest.Open(t, "sqlite3", dsn)
	defer client.Close()
	rawDB, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	defer rawDB.Close()

	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()
	tenant, requester, cmUser := setupTenantAndUsers(t, client, ctx, "e2e-running")

	// 建两条陪跑的 Change，把 legacy.ID 往后推——在一个全新的内存库里，Change 和 Ticket
	// 各自的自增序列都从 1 开始，如果 legacy 恰好是第一条 Change 而 backfillOne 建出来的
	// WorkItem 也恰好是第一条 Ticket，两者的数字 ID 会意外相等（都是 1），导致旧格式
	// "change:1" 和新格式 "change:1" 变成同一个字符串，掩盖掉本该被发现的"没有真的原地
	// 改写 businessKey"这类 bug。故意制造 legacy.ID != workItem.ID，让下面"旧 key 查不到"
	// 的断言具备真实的区分力。
	for i := 0; i < 2; i++ {
		_, decoyErr := client.Change.Create().
			SetTitle(fmt.Sprintf("陪跑变更-%d", i)).SetType("normal").SetStatus("draft").
			SetRiskLevel("medium").SetImpactScope("low").
			SetCreatedBy(requester.ID).SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, decoyErr)
	}

	// 1. 建一条"迁移前创建"的存量 Change：没有 work_item_id，状态已经是 pending
	//    （模拟旧版 SubmitChange 已经把它从 draft 推进到 pending）。
	legacy, err := client.Change.Create().
		SetTitle("迁移前创建的存量变更").SetType("normal").SetStatus("pending").
		SetRiskLevel("medium").SetImpactScope("low").
		SetCreatedBy(requester.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 2. 部署真实 BPMN 模板，用旧格式（Change 自己的主键）手工触发流程——完全复现迁移前
	//    SubmitChange 的行为：BusinessID: legacy.ID，不是 workItemID（那时候还不存在）。
	engine := service.NewCustomProcessEngine(client, logger)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	tenantCtx = service.WithBPMNAccessScope(tenantCtx, service.BPMNAccessScope{UserID: requester.ID, TenantID: tenant.ID, CanReadAllTasks: true})
	_, err = service.NewBPMNTemplateService(client).LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := service.NewProcessTriggerService(client, engine)
	_, err = trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType:         dto.BusinessTypeChange,
		BusinessID:           legacy.ID, // 旧格式：Change 自己的主键
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": true, "requester_id": float64(requester.ID)},
		TenantID:             tenant.ID,
	})
	require.NoError(t, err)

	// 推进到 CAB 审批节点（完成变更评估）。
	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	// 前置条件确认：运行中实例确实是旧格式 businessKey。
	oldBusinessKey := fmt.Sprintf("change:%d", legacy.ID)
	before, err := client.ProcessInstance.Query().
		Where(processinstance.BusinessKey(oldBusinessKey), processinstance.TenantID(tenant.ID), processinstance.Status("running")).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Activity_CABApproval", before.CurrentActivityID)

	// 3. 跑本工具的核心回填逻辑。
	result, err := backfillOne(ctx, client, legacy)
	require.NoError(t, err)
	require.True(t, result.migratedInstance, "应该识别出旧格式的运行中实例并完成迁移")
	require.NotEqual(t, legacy.ID, result.workItemID,
		"测试前置条件：changeID 和 workItemID 必须不同，否则下面新旧 businessKey 的对比断言没有区分力")

	// 4. 验证 businessKey/business_id 已经原地改写成新格式，旧格式查不到了。
	newBusinessKey := fmt.Sprintf("change:%d", result.workItemID)
	migratedInstance, err := client.ProcessInstance.Query().
		Where(processinstance.BusinessKey(newBusinessKey), processinstance.TenantID(tenant.ID), processinstance.Status("running")).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, before.ID, migratedInstance.ID, "应该是原地更新同一条实例，不是新建了一条")
	assert.Equal(t, result.workItemID, migratedInstance.BusinessID, "结构化的 business_id 列也要同步改写")

	stillOld, err := client.ProcessInstance.Query().
		Where(processinstance.BusinessKey(oldBusinessKey), processinstance.TenantID(tenant.ID)).
		Exist(ctx)
	require.NoError(t, err)
	assert.False(t, stillOld, "旧格式的 businessKey 不应该再查得到任何实例")

	// 5. 核心正确性证明：用真实的 handlers/change.Service.TransitionStatus 完成 CAB 审批，
	//    必须能通过新 businessKey 找到并推进这条被迁移过的实例。
	repo := changedomain.NewEntRepository(client, rawDB)
	svc := changedomain.NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	approverCtx := service.WithBPMNAccessScope(tenantCtx, service.BPMNAccessScope{
		UserID:   cmUser.ID,
		TenantID: tenant.ID,
	})
	updated, err := svc.TransitionStatus(approverCtx, legacy.ID, tenant.ID, cmUser.ID, "approved", "迁移后审批验证")
	require.NoError(t, err, "迁移后，Track4 的 CAB 审批必须能通过新 businessKey 找到并推进这条实例")
	assert.Equal(t, "scheduled", updated.Status, "normal 类型两跳推进到 scheduled")

	finalInstance, err := client.ProcessInstance.Query().
		Where(processinstance.BusinessKey(newBusinessKey), processinstance.TenantID(tenant.ID)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Implement", finalInstance.CurrentActivityID)
}

// TestBackfillOne_TenantIsolation 确认 migrateRunningProcessInstance 只会迁移同一个租户内
// 匹配 businessKey 的运行中实例——即使另一个租户碰巧存在一条 business_key 字符串完全相同的
// running 实例（理论上不应该发生，但如果查询漏了 tenant_id 过滤就会跨租户误迁移别的租户的
// 流程实例，这是需要显式锁定的安全边界）。
func TestBackfillOne_TenantIsolation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN("tenant_iso"))
	defer client.Close()
	ctx := context.Background()

	tenantA, requesterA, _ := setupTenantAndUsers(t, client, ctx, "iso-a")
	tenantB, requesterB, _ := setupTenantAndUsers(t, client, ctx, "iso-b")

	// 陪跑记录，避免 legacyA.ID 和回填出来的 workItem.ID 意外相等（两张表在全新内存库里的
	// 自增序列都从 1 开始），理由同上一个测试。
	for i := 0; i < 2; i++ {
		_, decoyErr := client.Change.Create().
			SetTitle(fmt.Sprintf("陪跑变更-%d", i)).SetCreatedBy(requesterA.ID).SetTenantID(tenantA.ID).
			Save(ctx)
		require.NoError(t, decoyErr)
	}

	legacyA, err := client.Change.Create().
		SetTitle("租户A的存量变更").SetCreatedBy(requesterA.ID).SetTenantID(tenantA.ID).
		Save(ctx)
	require.NoError(t, err)

	// 故意在两个租户下各建一条 business_key 字符串完全相同（"change:<legacyA.ID>"）的
	// running 实例——租户 B 的这条是用来验证隔离的诱饵，不应该被 tenantA 的回填碰到。
	collidingKey := fmt.Sprintf("change:%d", legacyA.ID)
	instanceA := createBareRunningInstance(t, client, tenantA.ID, collidingKey, "PI-ISO-A")
	instanceB := createBareRunningInstance(t, client, tenantB.ID, collidingKey, "PI-ISO-B")
	_ = requesterB

	result, err := backfillOne(ctx, client, legacyA)
	require.NoError(t, err)
	require.True(t, result.migratedInstance)
	require.NotEqual(t, legacyA.ID, result.workItemID,
		"测试前置条件：changeID 和 workItemID 必须不同，否则下面的新旧 key 对比没有区分力")

	afterA, err := client.ProcessInstance.Get(ctx, instanceA.ID)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("change:%d", result.workItemID), afterA.BusinessKey, "租户 A 的实例应该被迁移")
	assert.Equal(t, result.workItemID, afterA.BusinessID)

	afterB, err := client.ProcessInstance.Get(ctx, instanceB.ID)
	require.NoError(t, err)
	assert.Equal(t, collidingKey, afterB.BusinessKey, "租户 B 的实例即使 businessKey 字符串相同也绝不能被跨租户迁移")
	assert.Zero(t, afterB.BusinessID, "租户 B 的实例不应该被回填工具动过，business_id 应该保持初始值")
}

// createBareRunningInstance 建一条最小化的 running ProcessInstance，不依赖真实 BPMN
// 模板部署——只用来验证 migrateRunningProcessInstance 的查询/更新是否正确按 tenant_id 过滤，
// 不需要真的推进流程。
func createBareRunningInstance(t *testing.T, client *ent.Client, tenantID int, businessKey, instanceKey string) *ent.ProcessInstance {
	t.Helper()
	ctx := context.Background()
	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("DEP-" + instanceKey).
		SetDeploymentName("Deployment " + instanceKey).
		SetDeploymentTime(time.Now()).
		SetDeployedBy("test").
		SetIsActive(true).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	def, err := client.ProcessDefinition.Create().
		SetKey("bare_flow_" + instanceKey).
		SetName("Bare Flow " + instanceKey).
		SetVersion("1").
		SetIsLatest(true).
		SetBpmnXML([]byte("<bpmn/>")).
		SetDeploymentID(deployment.ID).
		SetDeployedAt(time.Now()).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	instance, err := client.ProcessInstance.Create().
		SetProcessInstanceID(instanceKey).
		SetProcessDefinitionKey(def.Key).
		SetProcessDefinitionID(def.ID).
		SetBusinessKey(businessKey).
		SetStatus("running").
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	return instance
}
