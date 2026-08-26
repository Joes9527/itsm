package service

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// setupPlatformTenantEnv 部署内置模板，返回不带租户键的平台级 ctx——模拟 controller 的
// getBPMNTenantContext 对 tenant_id=0 不注入 BPMNTenantIDContextKey 的行为。
func setupPlatformTenantEnv(t *testing.T) (*ent.Client, ProcessEngine, context.Context, int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Platform Tenant").
		SetCode("platform-tenant").
		SetDomain("platform.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	engine := NewCustomProcessEngine(client, zap.NewNop().Sugar())
	_, err = NewBPMNTemplateService(client).LoadAndDeployTemplates(ctx, tenant.ID)
	require.NoError(t, err)

	return client, engine, ctx, tenant.ID
}

// TestStartProcess_PlatformNoTenant_ServiceTaskRunsWithInstanceTenant 锁定 P2.2 修复：
// 平台级（ctx 无租户键、variables 无 tenant_id）启动流程时，StartProcess 把定义租户
// 注入 ctx，带 RequireTenantID 的 ServiceTask（incident_emergency_flow 的
// Activity_AutoAssign）以实例租户执行，不再硬失败。
func TestStartProcess_PlatformNoTenant_ServiceTaskRunsWithInstanceTenant(t *testing.T) {
	client, engine, platformCtx, tenantID := setupPlatformTenantEnv(t)

	assignee, err := client.User.Create().
		SetUsername("platform-assignee").SetEmail("platform-assignee@test.com").SetPasswordHash("x").
		SetName("处理人").SetTenantID(tenantID).SetActive(true).
		Save(platformCtx)
	require.NoError(t, err)

	inc, err := client.Incident.Create().
		SetTitle("平台级启动测试事件").
		SetIncidentNumber("INC-PLATFORM-1").
		SetStatus("new").
		SetReporterID(assignee.ID).
		SetTenantID(tenantID).
		Save(platformCtx)
	require.NoError(t, err)

	instance, err := engine.StartProcess(platformCtx, "incident_emergency_flow", "incident:platform-1", map[string]interface{}{
		"incident_id": inc.ID,
		"assignee_id": assignee.ID,
	})
	require.NoError(t, err, "平台级启动流程不应再在 ServiceTask 上硬失败")

	assigned, err := client.Incident.Get(platformCtx, inc.ID)
	require.NoError(t, err)
	assert.Equal(t, assignee.ID, assigned.AssigneeID, "assign_incident 应以实例（定义）租户执行")
	assert.Equal(t, "assigned", assigned.Status)

	started, err := client.ProcessInstance.Get(platformCtx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_ManagerApproval", started.CurrentActivityID, "流程应推进到第一个用户任务")
}

// TestCompleteTask_PlatformNoTenant_CallbackSideEffectFires 锁定 CompleteTask 的注入：
// 平台级完成用户任务时，此前 dispatchUserTaskCallback 里的 RequireTenantID 失败导致
// 业务副作用被静默跳过（只 Warn）；注入实例租户后副作用应真实执行。
func TestCompleteTask_PlatformNoTenant_CallbackSideEffectFires(t *testing.T) {
	client, engine, platformCtx, tenantID := setupPlatformTenantEnv(t)

	tenantCtx := context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, tenantID)
	ch := client.Change.Create().
		SetTitle("平台回调测试变更").
		SetCreatedBy(1).
		SetTenantID(tenantID).
		SaveX(tenantCtx)

	instance, err := engine.StartProcess(tenantCtx, "change_normal_flow", "change:platform-1", map[string]interface{}{
		"approval_required": true,
		"change_id":         ch.ID,
	})
	require.NoError(t, err)

	task := findTaskByDefinitionKey(t, client, tenantCtx, instance.ID, "Activity_Assessment")
	// System caller: this test drives CompleteTask directly to exercise
	// platform-level tenant injection, not to simulate a specific end user
	// acting on the task — authorizeTaskActor now denies by default without
	// either a user ID or this explicit declaration.
	systemPlatformCtx := context.WithValue(platformCtx, bpmn.BPMNSystemCallerContextKey, true)
	require.NoError(t, engine.CompleteTask(systemPlatformCtx, task.TaskID, map[string]interface{}{
		"change_id": ch.ID,
		"title":     "平台改过的标题",
	}))

	updated, err := client.Change.Get(platformCtx, ch.ID)
	require.NoError(t, err)
	assert.Equal(t, "平台改过的标题", updated.Title, "平台完成任务的 update_change 副作用应以实例租户执行")

	advanced, err := client.ProcessInstance.Get(platformCtx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_CABApproval", advanced.CurrentActivityID, "流程应正常推进到 CAB 审批")
}

// TestCompleteTask_PlatformNoTenant_TaskStillBoundToInstanceTenant 锁定注入不放松租户
// 边界：平台 ctx 伪造 change_id 指向别家租户时，注入的实例租户 + 写侧 Where(TenantID)
// 使写入落空，别家租户数据不受影响。
func TestCompleteTask_PlatformNoTenant_TaskStillBoundToInstanceTenant(t *testing.T) {
	client, engine, platformCtx, tenantID := setupPlatformTenantEnv(t)

	otherTenant, err := client.Tenant.Create().
		SetName("Other Tenant").SetCode("platform-other").SetDomain("other.example.com").SetStatus("active").
		Save(platformCtx)
	require.NoError(t, err)

	otherChange := client.Change.Create().
		SetTitle("别家租户的变更").
		SetCreatedBy(1).
		SetTenantID(otherTenant.ID).
		SaveX(platformCtx)

	tenantCtx := context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, tenantID)
	ch := client.Change.Create().
		SetTitle("本租户变更").
		SetCreatedBy(1).
		SetTenantID(tenantID).
		SaveX(tenantCtx)

	instance, err := engine.StartProcess(tenantCtx, "change_normal_flow", "change:platform-2", map[string]interface{}{
		"approval_required": true,
		"change_id":         ch.ID,
	})
	require.NoError(t, err)

	task := findTaskByDefinitionKey(t, client, tenantCtx, instance.ID, "Activity_Assessment")
	// System caller: this test drives CompleteTask directly to exercise
	// platform-level tenant boundary enforcement, not to simulate a specific
	// end user acting on the task — authorizeTaskActor now denies by default
	// without either a user ID or this explicit declaration.
	systemPlatformCtx := context.WithValue(platformCtx, bpmn.BPMNSystemCallerContextKey, true)
	require.NoError(t, engine.CompleteTask(systemPlatformCtx, task.TaskID, map[string]interface{}{
		"change_id": otherChange.ID,
		"title":     "越权尝试",
	}))

	after, err := client.Change.Get(platformCtx, otherChange.ID)
	require.NoError(t, err)
	assert.Equal(t, "别家租户的变更", after.Title, "跨租户伪造 change_id 不得写入别家租户数据")
}
