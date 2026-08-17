package bpmn

import (
	"context"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// setupIncidentHandlerFixture 建一个"刚创建、还没有处理人"的事件——这正是
// incident_emergency_flow 的 Activity_AutoAssign（起始事件后的第一个 serviceTask）
// 在生产里遇到的常态：Incident.assignee_id 是 Optional，新事件默认为 0。
func setupIncidentHandlerFixture(t *testing.T) (*ent.Client, *IncidentServiceTaskHandler, int, *ent.Incident, int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:incident_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("ih-1").SetDomain("ih-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	reporter, err := client.User.Create().
		SetUsername("reporter-ih").SetEmail("reporter-ih@test.com").SetPasswordHash("x").
		SetName("报告人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	assignee, err := client.User.Create().
		SetUsername("assignee-ih").SetEmail("assignee-ih@test.com").SetPasswordHash("x").
		SetName("处理人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	inc, err := client.Incident.Create().
		SetTitle("自动分配测试事件").
		SetIncidentNumber("INC-IH-1").
		SetStatus(common.IncidentStatusNew).
		SetReporterID(reporter.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	handler := NewIncidentServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	return client, handler, tenant.ID, inc, assignee.ID
}

// TestIncidentServiceTaskHandler_AssignIncident_NoAssignee_IsNoOp 是 Finding 1 的核心回归：
// "自动分配但当前没有任何可用处理人"是正常空态，不是失败。返回 error 会让 handleElement
// 把整条 StartProcess 打断，流程实例永久卡在起始事件上。
func TestIncidentServiceTaskHandler_AssignIncident_NoAssignee_IsNoOp(t *testing.T) {
	client, handler, tenantID, inc, _ := setupIncidentHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	cases := []struct {
		name      string
		variables map[string]interface{}
	}{
		{
			name:      "assignee_id 完全缺失",
			variables: map[string]interface{}{"action": "assign_incident", "incident_id": inc.ID},
		},
		{
			name:      "assignee_id 为 0（Optional 字段的默认值）",
			variables: map[string]interface{}{"action": "assign_incident", "incident_id": inc.ID, "assignee_id": 0},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := handler.Execute(ctx, nil, tc.variables)
			require.NoError(t, err, "无处理人是正常空态，不应该返回错误")
			require.NotNil(t, result)
			assert.True(t, result.Success)

			after, err := client.Incident.Get(ctx, inc.ID)
			require.NoError(t, err)
			assert.Equal(t, common.IncidentStatusNew, after.Status, "空态跳过时不应该改状态")
			assert.Zero(t, after.AssigneeID, "空态跳过时不应该写处理人")
		})
	}
}

// TestIncidentServiceTaskHandler_AssignIncident_ValidAssignee 是上面那条修复的回归护栏：
// 有合法处理人时，原有行为必须一字不变。
func TestIncidentServiceTaskHandler_AssignIncident_ValidAssignee(t *testing.T) {
	client, handler, tenantID, inc, assigneeID := setupIncidentHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "assign_incident",
		"incident_id": inc.ID,
		"assignee_id": assigneeID,
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	after, err := client.Incident.Get(ctx, inc.ID)
	require.NoError(t, err)
	assert.Equal(t, assigneeID, after.AssigneeID)
	assert.Equal(t, common.IncidentStatusAssigned, after.Status)
}

// TestIncidentServiceTaskHandler_AssignIncident_InvalidIncidentID_StillErrors：
// incident_id 缺失/非法说明是接线错误（谁也没告诉这个节点该操作哪条事件），
// 必须继续硬失败，不能跟"没有处理人"混为一谈。
func TestIncidentServiceTaskHandler_AssignIncident_InvalidIncidentID_StillErrors(t *testing.T) {
	_, handler, tenantID, _, assigneeID := setupIncidentHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "assign_incident",
		"assignee_id": assigneeID,
	})
	assert.Error(t, err, "无效的事件ID是真实接线错误，必须继续报错")
}

// TestIncidentServiceTaskHandler_AssignIncident_CrossTenant 证明分配写入带租户过滤：
// incident_task 的动作也会经 dispatchUserTaskCallback 收到调用方提交的变量
// （Activity_Diagnosis/Resolve/Close 都是 incident_task 的 userTask），
// 所以 incident_id 属于可被伪造的输入。
func TestIncidentServiceTaskHandler_AssignIncident_CrossTenant(t *testing.T) {
	client, handler, tenantID, inc, assigneeID := setupIncidentHandlerFixture(t)
	otherCtx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID+9999)

	_, err := handler.Execute(otherCtx, nil, map[string]interface{}{
		"action":      "assign_incident",
		"incident_id": inc.ID,
		"assignee_id": assigneeID,
	})
	assert.Error(t, err, "跨租户分配必须失败")

	after, err := client.Incident.Get(context.Background(), inc.ID)
	require.NoError(t, err)
	assert.Zero(t, after.AssigneeID, "跨租户请求不得写入处理人")
}
