package service

import (
	"context"
	executionfixture "itsm-backend/tests/fixtures/execution"
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

	engine := NewCustomProcessEngine(client, zap.NewNop().Sugar(), executionfixture.Standard())
	_, err = NewBPMNTemplateService(client).LoadAndDeployTemplates(ctx, tenant.ID)
	require.NoError(t, err)

	// incident_emergency_flow 的 Activity_AutoAssign（assign_incident）现在委托给注入的
	// IncidentService（Task 6：不再绕过领域服务直接写 Ent），跟生产环境 bootstrap 里的
	// SetIncidentService 装配是同一个模式，测试里也要同样装配，否则 ServiceTask 会因为
	// "incident service 未注入" 硬失败。
	if cpe, ok := engine.(*CustomProcessEngine); ok {
		if h, ok := cpe.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler); ok {
			h.SetIncidentService(NewIncidentService(client, zap.NewNop().Sugar(), executionfixture.Standard()))
		}
	}

	return client, engine, ctx, tenant.ID
}

func TestStartProcess_TrustedTenant_ServiceTaskUsesInstanceIdentity(t *testing.T) {
	client, engine, platformCtx, tenantID := setupPlatformTenantEnv(t)

	assignee, err := client.User.Create().
		SetUsername("platform-assignee").SetEmail("platform-assignee@test.com").SetPasswordHash("x").
		SetName("处理人").SetTenantID(tenantID).SetActive(true).SetRole("super_admin").
		Save(platformCtx)
	require.NoError(t, err)

	workItem := client.Ticket.Create().
		SetTitle("平台级启动测试事件").
		SetTicketNumber("T-PLATFORM-INCIDENT-1").
		SetRecordClass("incident").
		SetStatus("new").
		SetRequesterID(assignee.ID).
		SetTenantID(tenantID).
		SaveX(platformCtx)
	inc, err := client.Incident.Create().
		SetWorkItemID(workItem.ID).
		Save(platformCtx)
	require.NoError(t, err)

	trustedCtx := WithTrustedBPMNTenantContext(platformCtx, tenantID)
	instance, err := engine.StartProcess(trustedCtx, "incident_emergency_flow", "incident:platform-1", "incident", workItem.ID, map[string]interface{}{
		"version":      workItem.Version,
		"assignee_id":  assignee.ID,
		"requester_id": assignee.ID,
		"triggered_by": strconv.Itoa(assignee.ID),
	})
	require.NoError(t, err)

	assigned, err := client.Incident.Get(platformCtx, inc.ID)
	require.NoError(t, err)
	assert.Equal(t, assignee.ID, requireIncidentWorkItem(t, client, assigned).AssigneeID, "assign_incident 应以实例（定义）租户执行")
	assert.Equal(t, "assigned", requireIncidentWorkItem(t, client, assigned).Status)

	started, err := client.ProcessInstance.Get(platformCtx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_ManagerApproval", started.CurrentActivityID, "流程应推进到第一个用户任务")
}

// TestCompleteTask_TypedScope_CallbackUsesAuthoritativeBusinessIdentity moved to tests/integration/workitem_change_consumers_postgres_test.go.

// TestCompleteTask_ParticipantBusinessIDCannotRetargetCallback moved to tests/integration/workitem_change_consumers_postgres_test.go.
