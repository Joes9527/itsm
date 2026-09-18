package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/approver"
	"itsm-backend/service/bpmn"
	executionfixture "itsm-backend/tests/fixtures/execution"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	_ "github.com/mattn/go-sqlite3"
)

// 端到端验证 A（审批不断流）：一张单走完"起流程 → 技术评审 → 到达审批节点"，
// 断言审批任务**真的落到提单人自己的上级**，而不是兜底组。
//
// 链路上每一环都是真实组件：真实模板部署、真实流程绑定、真实引擎、真实 ProcessTask
// 落库。单测只证明解析函数的行为，这里证明它接在流程上确实生效。
//
// 场景一：提单人有在职的直属上级。
func TestApprovalTaskGoesToTheRequestersOwnManagerEndToEnd(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:e2e_own_manager?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("审批直连上级租户").SetCode("e2e-own-manager").
		SetDomain("e2e-own-manager.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	manager, err := client.User.Create().
		SetUsername("e2e-manager").SetEmail("e2e-manager@test.com").SetPasswordHash("x").
		SetName("直属上级").SetTenantID(tenant.ID).SetActive(true).Save(ctx)
	require.NoError(t, err)

	// 兜底组的成员是**另一个人**：只有这样才能证明任务没走兜底路径。
	backup, err := client.User.Create().
		SetUsername("e2e-backup").SetEmail("e2e-backup@test.com").SetPasswordHash("x").
		SetName("兜底审批人").SetTenantID(tenant.ID).SetActive(true).Save(ctx)
	require.NoError(t, err)

	creator, err := client.User.Create().
		SetUsername("e2e-creator").SetEmail("e2e-creator@test.com").SetPasswordHash("x").
		SetName("提单人").SetTenantID(tenant.ID).SetActive(true).
		SetManagerID(manager.ID). // ← 关键：提单人有一个在职的直属上级
		Save(ctx)
	require.NoError(t, err)

	_, err = client.Group.Create().
		SetName(approvalFallbackCandidateGroup).
		SetTenantID(tenant.ID).
		AddMemberIDs(backup.ID).
		Save(ctx)
	require.NoError(t, err)

	approvalTask := driveReleaseToApprovalNode(t, client, ctx, tenant.ID, creator.ID, creator.ID)

	require.Equal(t, strconv.Itoa(manager.ID), approvalTask.Assignee,
		"审批任务必须派给提单人自己的上级")
	require.NotEqual(t, strconv.Itoa(backup.ID), approvalTask.Assignee,
		"审批任务不得落到兜底组（落到那里说明直连上级没生效）")

	// 没走兜底路径，就不该有兜底留痕。
	fallbackAudits := client.ProcessAuditLog.Query().Where(
		processauditlog.Action(approvalFallbackAuditAction),
		processauditlog.ProcessInstanceID(approvalTask.ProcessInstanceID),
	).CountX(ctx)
	require.Equal(t, 0, fallbackAudits, "派给了直属上级就不应留下兜底记录")
}

// 场景二：提单人没有可用的上级 → 落到兜底组，且**必须留下可查的审计记录**。
//
// 这条验证设计里"先往上找，最后兜底组（留痕）"的另一半：兜底不是静默兜底。
func TestApprovalTaskFallsBackToTheGroupAndIsAudited(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:e2e_fallback?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("审批兜底租户").SetCode("e2e-fallback").
		SetDomain("e2e-fallback.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	backup, err := client.User.Create().
		SetUsername("e2e-backup2").SetEmail("e2e-backup2@test.com").SetPasswordHash("x").
		SetName("兜底审批人").SetTenantID(tenant.ID).SetActive(true).Save(ctx)
	require.NoError(t, err)

	// 提单人既没有直属上级，也没有部门负责人。
	creator, err := client.User.Create().
		SetUsername("e2e-creator2").SetEmail("e2e-creator2@test.com").SetPasswordHash("x").
		SetName("无上级提单人").SetTenantID(tenant.ID).SetActive(true).Save(ctx)
	require.NoError(t, err)

	_, err = client.Group.Create().
		SetName(approvalFallbackCandidateGroup).
		SetTenantID(tenant.ID).
		AddMemberIDs(backup.ID).
		Save(ctx)
	require.NoError(t, err)

	approvalTask := driveReleaseToApprovalNode(t, client, ctx, tenant.ID, creator.ID, creator.ID)

	require.Empty(t, approvalTask.Assignee, "解析不到人时不应随便指派具体人")

	// 兜底必须留痕：审计里要能回答"当时为什么派给了兜底组"。
	audits := client.ProcessAuditLog.Query().Where(
		processauditlog.Action(approvalFallbackAuditAction),
		processauditlog.ProcessInstanceID(approvalTask.ProcessInstanceID),
	).AllX(ctx)
	require.Len(t, audits, 1, "落到兜底组必须留下恰好一条审计记录")
	require.Equal(t, approvalFallbackCandidateGroup, audits[0].Metadata["group"])
	require.Equal(t, string(approver.ReasonFallbackGroup), audits[0].Metadata["reason"])

	// 兜底组的成员应被展开进候选人，否则任务无人可领。
	assert.Contains(t, approvalTask.CandidateUsers, "e2e-backup2")
}

// 场景三：流程变量里没有 `requester_id`，但实例有 `initiator`（release 就是这样）
// → 必须回落到 initiator，仍然派给提单人的上级。
//
// 背景（本次查明）：引擎里有**两个不同的人概念**——
//   - `instance.Initiator`：谁触发了流程，由 resolveProcessInitiator 从请求上下文 /
//     triggered_by 推出，覆盖很广（权限、事件/变更命令都在用）；
//   - `variables.requester_id`：谁**提单**，只有工单进单等少数路径写入。
//
// 审批归属解析原**只**读后者，于是像 release 这种"有 initiator、没有 requester_id"的
// 流程一律落兜底组。这不是数据缺失，而是解析用错了来源。
func TestApprovalFallsBackToTheInstanceInitiatorWhenRequesterVariableIsMissing(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:e2e_no_requester?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("无提单人变量租户").SetCode("e2e-no-requester").
		SetDomain("e2e-no-requester.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	backup, err := client.User.Create().
		SetUsername("e2e-backup3").SetEmail("e2e-backup3@test.com").SetPasswordHash("x").
		SetName("兜底审批人").SetTenantID(tenant.ID).SetActive(true).Save(ctx)
	require.NoError(t, err)

	_, err = client.Group.Create().
		SetName(approvalFallbackCandidateGroup).
		SetTenantID(tenant.ID).
		AddMemberIDs(backup.ID).
		Save(ctx)
	require.NoError(t, err)

	manager, err := client.User.Create().
		SetUsername("e2e-manager3").SetEmail("e2e-manager3@test.com").SetPasswordHash("x").
		SetName("直属上级").SetTenantID(tenant.ID).SetActive(true).Save(ctx)
	require.NoError(t, err)

	// 提单人有在职上级；流程变量不写 requester_id，但实例的 initiator 就是他。
	creator, err := client.User.Create().
		SetUsername("e2e-creator3").SetEmail("e2e-creator3@test.com").SetPasswordHash("x").
		SetName("靠 initiator 可见").SetTenantID(tenant.ID).SetActive(true).
		SetManagerID(manager.ID).Save(ctx)
	require.NoError(t, err)

	approvalTask := driveReleaseToApprovalNode(t, client, ctx, tenant.ID, creator.ID, 0)

	require.Equal(t, strconv.Itoa(manager.ID), approvalTask.Assignee,
		"没有 requester_id 时必须回落到 instance.Initiator，仍然派给提单人的上级")
	require.NotEqual(t, strconv.Itoa(backup.ID), approvalTask.Assignee, "不得落到兜底组")
}

// 场景四：**代提单**。客服（initiator）替用户（requester_id）提单时，
// 审批必须派给**用户**的上级，而不是客服的上级。
//
// 这是"回落 initiator"不能反过来做的理由：`requester_id` 优先，不能被子级覆盖。
func TestApprovalPrefersTheRequesterOverTheInitiatorWhenTheyDiffer(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:e2e_on_behalf?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("代提单租户").SetCode("e2e-on-behalf").
		SetDomain("e2e-on-behalf.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	requesterManager := client.User.Create().
		SetUsername("e2e-req-manager").SetEmail("e2e-req-manager@test.com").SetPasswordHash("x").
		SetName("提单人上级").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)

	agentManager := client.User.Create().
		SetUsername("e2e-agent-manager").SetEmail("e2e-agent-manager@test.com").SetPasswordHash("x").
		SetName("客服上级").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)

	// 真正的提单人（工单是替他提的）
	requester := client.User.Create().
		SetUsername("e2e-requester").SetEmail("e2e-requester@test.com").SetPasswordHash("x").
		SetName("真实提单人").SetTenantID(tenant.ID).SetActive(true).
		SetManagerID(requesterManager.ID).SaveX(ctx)

	// 代提单的客服（流程的 initiator）
	agent := client.User.Create().
		SetUsername("e2e-agent").SetEmail("e2e-agent@test.com").SetPasswordHash("x").
		SetName("客服").SetTenantID(tenant.ID).SetActive(true).
		SetManagerID(agentManager.ID).SaveX(ctx)

	approvalTask := driveReleaseToApprovalNode(t, client, ctx, tenant.ID, agent.ID, requester.ID)

	require.Equal(t, strconv.Itoa(requesterManager.ID), approvalTask.Assignee,
		"代提单时必须派给**提单人**的上级")
	require.NotEqual(t, strconv.Itoa(agentManager.ID), approvalTask.Assignee,
		"不得派给代提单人的上级")
}

// 场景五：initiator 是 `system`（系统发起的流程，没有真实的人）→ 不能瞎派。
func TestApprovalDoesNotResolveWhenTheInitiatorIsSystem(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:e2e_system_initiator?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("系统发起租户").SetCode("e2e-system").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	backup := client.User.Create().
		SetUsername("e2e-backup5").SetEmail("e2e-backup5@test.com").SetPasswordHash("x").
		SetName("兜底审批人").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	_, err = client.Group.Create().
		SetName(approvalFallbackCandidateGroup).
		SetTenantID(tenant.ID).AddMemberIDs(backup.ID).Save(ctx)
	require.NoError(t, err)

	manager := client.User.Create().
		SetUsername("e2e-manager5").SetEmail("e2e-manager5@test.com").SetPasswordHash("x").
		SetName("直属上级").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	creator := client.User.Create().
		SetUsername("e2e-creator5").SetEmail("e2e-creator5@test.com").SetPasswordHash("x").
		SetName("触发者").SetTenantID(tenant.ID).SetActive(true).
		SetManagerID(manager.ID).SaveX(ctx)

	approvalTask := driveReleaseToApprovalNodeWithInitiator(t, client, ctx, tenant.ID, creator.ID, "system")

	require.Empty(t, approvalTask.Assignee, "system 不是真实的人，不得据此指派")
	require.NotEqual(t, strconv.Itoa(manager.ID), approvalTask.Assignee)
}

// driveReleaseToApprovalNodeWithInitiator 与 driveReleaseToApprovalNode 相同，
// 但把实例的 initiator 覆盖为指定值（用于模拟"系统发起"这类非人的发起者）。
func driveReleaseToApprovalNodeWithInitiator(t *testing.T, client *ent.Client, ctx context.Context, tenantID, creatorID int, initiator string) *ent.ProcessTask {
	t.Helper()
	return driveReleaseToApprovalNodeOpts(t, client, ctx, tenantID, creatorID, 0, initiator)
}

// driveReleaseToApprovalNode 用真实模板与绑定把一张单推到审批节点，返回该审批任务。
//
// requesterInVariables > 0 时把 requester_id 写进流程变量——这正是**工单进单**所做的
// （handlers/intake/service.go），也是"为谁提单"的权威来源；不注入时靠实例的 initiator
// 定位人（release 流程的实情）。
func driveReleaseToApprovalNode(t *testing.T, client *ent.Client, ctx context.Context, tenantID, creatorID, requesterInVariables int) *ent.ProcessTask {
	t.Helper()
	return driveReleaseToApprovalNodeOpts(t, client, ctx, tenantID, creatorID, requesterInVariables, "")
}

func driveReleaseToApprovalNodeOpts(t *testing.T, client *ent.Client, ctx context.Context, tenantID, creatorID, requesterInVariables int, initiatorOverride string) *ent.ProcessTask {
	t.Helper()

	_, err := NewBPMNTemplateService(client).LoadAndDeployTemplates(ctx, tenantID)
	require.NoError(t, err)
	require.NoError(t, NewProcessBindingService(client).InitDefaultBindings(ctx, tenantID))

	releaseService := NewReleaseService(client, zap.NewNop().Sugar())
	engine := NewCustomProcessEngine(client, zap.NewNop().Sugar(), executionfixture.Standard())
	releaseService.SetProcessEngine(engine)
	releaseService.SetProcessTriggerService(NewProcessTriggerService(client, engine))
	engine.(*CustomProcessEngine).CallbackRegistry().
		GetHandler("release_service_handler").(*bpmn.ReleaseServiceTaskHandler).
		SetReleaseService(releaseService)

	releaseEntity, err := releaseService.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-E2E-1",
		Title:         "端到端审批归属验证",
		Type:          "minor",
	}, creatorID, tenantID)
	require.NoError(t, err)

	instance := client.ProcessInstance.Query().Where(
		processinstance.BusinessType("release"), processinstance.BusinessID(releaseEntity.ID),
	).OnlyX(ctx)

	if requesterInVariables > 0 {
		vars := map[string]interface{}{}
		for k, v := range instance.Variables {
			vars[k] = v
		}
		vars["requester_id"] = requesterInVariables // 与 intake 写入的形式一致
		require.NoError(t, client.ProcessInstance.UpdateOneID(instance.ID).
			SetVariables(vars).Exec(ctx))
	}
	if initiatorOverride != "" {
		require.NoError(t, client.ProcessInstance.UpdateOneID(instance.ID).
			SetInitiator(initiatorOverride).Exec(ctx))
	}

	// 技术评审 → 网关按 tech_review_pass 路由到 Activity_Approval
	_, err = releaseService.ApplyReleaseTechReview(ctx, releaseEntity.ID, tenantID, creatorID, "架构评审通过")
	require.NoError(t, err)

	afterReview, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, "Activity_Approval", afterReview.CurrentActivityID, "评审后应推进到审批节点")

	return client.ProcessTask.Query().Where(
		processtask.ProcessInstanceID(instance.ID),
		processtask.TaskDefinitionKey("Activity_Approval"),
	).OnlyX(ctx)
}
