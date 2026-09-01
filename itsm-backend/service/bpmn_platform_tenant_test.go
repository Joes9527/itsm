package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// setupPlatformTenantEnv deploys the built-in templates and returns an
// untrusted base context. Tests must opt into a typed actor scope or the narrow
// trusted-tenant start capability before invoking public mutations.
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

	// incident_emergency_flow 的 Activity_AutoAssign（assign_incident）现在委托给注入的
	// IncidentService（Task 6：不再绕过领域服务直接写 Ent），跟生产环境 bootstrap 里的
	// SetIncidentService 装配是同一个模式，测试里也要同样装配，否则 ServiceTask 会因为
	// "incident service 未注入" 硬失败。
	if cpe, ok := engine.(*CustomProcessEngine); ok {
		if h, ok := cpe.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler); ok {
			h.SetIncidentService(NewIncidentService(client, zap.NewNop().Sugar()))
		}
	}

	return client, engine, ctx, tenant.ID
}

func TestStartProcess_TrustedTenant_ServiceTaskUsesInstanceIdentity(t *testing.T) {
	client, engine, platformCtx, tenantID := setupPlatformTenantEnv(t)

	assignee, err := client.User.Create().
		SetUsername("platform-assignee").SetEmail("platform-assignee@test.com").SetPasswordHash("x").
		SetName("处理人").SetTenantID(tenantID).SetActive(true).
		Save(platformCtx)
	require.NoError(t, err)

	workItem := client.Ticket.Create().
		SetTitle("平台级启动测试事件").
		SetTicketNumber("T-PLATFORM-INCIDENT-1").
		SetType("incident").
		SetRecordClass("incident").
		SetStatus("new").
		SetRequesterID(assignee.ID).
		SetTenantID(tenantID).
		SaveX(platformCtx)
	inc, err := client.Incident.Create().
		SetTitle("平台级启动测试事件").
		SetIncidentNumber("INC-PLATFORM-1").
		SetStatus("new").
		SetReporterID(assignee.ID).
		SetWorkItemID(workItem.ID).
		SetTenantID(tenantID).
		Save(platformCtx)
	require.NoError(t, err)

	trustedCtx := WithTrustedBPMNTenantContext(platformCtx, tenantID)
	instance, err := engine.StartProcess(trustedCtx, "incident_emergency_flow", "incident:platform-1", "incident", workItem.ID, map[string]interface{}{
		"assignee_id":  assignee.ID,
		"requester_id": assignee.ID,
		"triggered_by": strconv.Itoa(assignee.ID),
	})
	require.NoError(t, err)

	assigned, err := client.Incident.Get(platformCtx, inc.ID)
	require.NoError(t, err)
	assert.Equal(t, assignee.ID, assigned.AssigneeID, "assign_incident 应以实例（定义）租户执行")
	assert.Equal(t, "assigned", assigned.Status)

	started, err := client.ProcessInstance.Get(platformCtx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_ManagerApproval", started.CurrentActivityID, "流程应推进到第一个用户任务")
}

func TestCompleteTask_TypedScope_CallbackUsesAuthoritativeBusinessIdentity(t *testing.T) {
	client, engine, platformCtx, tenantID := setupPlatformTenantEnv(t)

	actor := client.User.Create().
		SetUsername("platform-change-actor").SetEmail("platform-change-actor@test.com").SetPasswordHash("x").
		SetName("变更处理人").SetTenantID(tenantID).SetActive(true).
		SaveX(platformCtx)
	workItem := client.Ticket.Create().
		SetTitle("平台回调测试变更").
		SetTicketNumber("T-PLATFORM-CHANGE-1").
		SetType("change").
		SetRecordClass("change_request").
		SetRequesterID(actor.ID).
		SetTenantID(tenantID).
		SaveX(platformCtx)
	ch := client.Change.Create().
		SetTitle("平台回调测试变更").
		SetCreatedBy(actor.ID).
		SetWorkItemID(workItem.ID).
		SetTenantID(tenantID).
		SaveX(platformCtx)

	trustedCtx := WithTrustedBPMNTenantContext(platformCtx, tenantID)
	instance, err := engine.StartProcess(trustedCtx, "change_normal_flow", "change:platform-1", "change", workItem.ID, map[string]interface{}{
		"approval_required": true,
		"requester_id":      actor.ID,
		"triggered_by":      strconv.Itoa(actor.ID),
	})
	require.NoError(t, err)

	task := findTaskByDefinitionKey(t, client, platformCtx, instance.ID, "Activity_Assessment")
	actorCtx := WithBPMNAccessScope(platformCtx, BPMNAccessScope{
		UserID: actor.ID, TenantID: tenantID, CanUpdateAllTasks: true,
	})
	require.NoError(t, engine.CompleteTask(actorCtx, task.TaskID, map[string]interface{}{
		"title": "平台改过的标题",
	}))

	updated, err := client.Change.Get(platformCtx, ch.ID)
	require.NoError(t, err)
	assert.Equal(t, "平台改过的标题", updated.Title, "平台完成任务的 update_change 副作用应以实例租户执行")

	advanced, err := client.ProcessInstance.Get(platformCtx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_CABApproval", advanced.CurrentActivityID, "流程应正常推进到 CAB 审批")
}

func TestCompleteTask_ParticipantBusinessIDCannotRetargetCallback(t *testing.T) {
	client, engine, platformCtx, tenantID := setupPlatformTenantEnv(t)

	otherTenant, err := client.Tenant.Create().
		SetName("Other Tenant").SetCode("platform-other").SetDomain("other.example.com").SetStatus("active").
		Save(platformCtx)
	require.NoError(t, err)

	otherActor := client.User.Create().
		SetUsername("platform-other-actor").SetEmail("platform-other-actor@test.com").SetPasswordHash("x").
		SetName("其他租户处理人").SetTenantID(otherTenant.ID).SetActive(true).
		SaveX(platformCtx)
	otherWorkItem := client.Ticket.Create().
		SetTitle("别家租户的变更").SetTicketNumber("T-PLATFORM-OTHER-CHANGE-1").
		SetType("change").SetRecordClass("change_request").SetRequesterID(otherActor.ID).
		SetTenantID(otherTenant.ID).SaveX(platformCtx)
	otherChange := client.Change.Create().
		SetTitle("别家租户的变更").
		SetCreatedBy(otherActor.ID).
		SetWorkItemID(otherWorkItem.ID).
		SetTenantID(otherTenant.ID).
		SaveX(platformCtx)

	actor := client.User.Create().
		SetUsername("platform-own-actor").SetEmail("platform-own-actor@test.com").SetPasswordHash("x").
		SetName("本租户处理人").SetTenantID(tenantID).SetActive(true).
		SaveX(platformCtx)
	workItem := client.Ticket.Create().
		SetTitle("本租户变更").SetTicketNumber("T-PLATFORM-OWN-CHANGE-1").
		SetType("change").SetRecordClass("change_request").SetRequesterID(actor.ID).
		SetTenantID(tenantID).SaveX(platformCtx)
	ch := client.Change.Create().
		SetTitle("本租户变更").
		SetCreatedBy(actor.ID).
		SetWorkItemID(workItem.ID).
		SetTenantID(tenantID).
		SaveX(platformCtx)

	trustedCtx := WithTrustedBPMNTenantContext(platformCtx, tenantID)
	instance, err := engine.StartProcess(trustedCtx, "change_normal_flow", "change:platform-2", "change", workItem.ID, map[string]interface{}{
		"approval_required": true,
		"requester_id":      actor.ID,
		"triggered_by":      strconv.Itoa(actor.ID),
	})
	require.NoError(t, err)

	task := findTaskByDefinitionKey(t, client, platformCtx, instance.ID, "Activity_Assessment")
	actorCtx := WithBPMNAccessScope(platformCtx, BPMNAccessScope{
		UserID: actor.ID, TenantID: tenantID, CanUpdateAllTasks: true,
	})
	require.NoError(t, engine.CompleteTask(actorCtx, task.TaskID, map[string]interface{}{
		"change_id": otherChange.ID,
		"title":     "越权尝试",
	}))

	after, err := client.Change.Get(platformCtx, otherChange.ID)
	require.NoError(t, err)
	assert.Equal(t, "别家租户的变更", after.Title, "跨租户伪造 change_id 不得写入别家租户数据")
	assert.Equal(t, "越权尝试", client.Change.GetX(platformCtx, ch.ID).Title, "回调只能写权威流程目标")
}
