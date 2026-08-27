package bpmn

import (
	"context"
	"fmt"
	"testing"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/incident"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

// dbBackedIncidentService 是仅用于本文件 fixture 测试的 IncidentDomainServiceInterface
// 实现——它直接用同一个 ent.Client 做租户范围写入，行为对齐真实 service.IncidentService
// 的 AssignIncident/UpdateStatus（找不到/跨租户返回 "incident not found"）。
// 之所以不直接用 *service.IncidentService：itsm-backend/service 包已经反向 import
// itsm-backend/service/bpmn（用于注册 handler），这里再 import itsm-backend/service
// 会形成循环依赖。
type dbBackedIncidentService struct {
	client *ent.Client
}

func (s *dbBackedIncidentService) CreateIncident(ctx context.Context, req *dto.CreateIncidentRequest, tenantID, userID int) (*dto.IncidentResponse, error) {
	inc, err := s.client.Incident.Create().
		SetTitle(req.Title).
		SetDescription(req.Description).
		SetType(req.Type).
		SetPriority(req.Priority).
		SetSeverity(req.Severity).
		SetStatus(common.IncidentStatusNew).
		SetReporterID(userID).
		SetTenantID(tenantID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return &dto.IncidentResponse{ID: inc.ID, IncidentNumber: inc.IncidentNumber}, nil
}

func (s *dbBackedIncidentService) AssignIncident(ctx context.Context, id int, assigneeID int, tenantID int) (*dto.IncidentResponse, error) {
	updated, err := s.client.Incident.Update().
		Where(incident.ID(id), incident.TenantID(tenantID)).
		SetAssigneeID(assigneeID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	if updated == 0 {
		return nil, fmt.Errorf("incident not found")
	}
	return &dto.IncidentResponse{ID: id}, nil
}

func (s *dbBackedIncidentService) UpdateStatus(ctx context.Context, id int, status string, tenantID int) (*dto.IncidentResponse, error) {
	updated, err := s.client.Incident.Update().
		Where(incident.ID(id), incident.TenantID(tenantID)).
		SetStatus(status).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	if updated == 0 {
		return nil, fmt.Errorf("incident not found")
	}
	return &dto.IncidentResponse{ID: id}, nil
}

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
	handler.SetIncidentService(&dbBackedIncidentService{client: client})
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

// TestIncidentServiceTaskHandler_TenantScopedActions 覆盖 assign 之外六个动作的
// 租户隔离三件套：同租户 Valid 生效、跨租户拒绝且零写入、无租户上下文 fail-closed。
// 这些动作的 incident_id 在 UserTask 回调路径上来自客户端提交的变量，可被伪造，
// 与 assignIncident 是同一漏洞面。
func TestIncidentServiceTaskHandler_TenantScopedActions(t *testing.T) {
	tests := []struct {
		name        string
		action      string
		extraVars   map[string]interface{}
		assertValid func(t *testing.T, client *ent.Client, incID int)
	}{
		{
			name:   "escalate",
			action: "escalate_incident",
			assertValid: func(t *testing.T, client *ent.Client, incID int) {
				after, err := client.Incident.Get(context.Background(), incID)
				require.NoError(t, err)
				assert.Equal(t, 1, after.EscalationLevel, "未显式指定级别时应递增为 1")
				assert.Equal(t, common.IncidentStatusEscalated, after.Status)
				assert.False(t, after.EscalatedAt.IsZero())
			},
		},
		{
			name:   "resolve",
			action: "resolve_incident",
			assertValid: func(t *testing.T, client *ent.Client, incID int) {
				after, err := client.Incident.Get(context.Background(), incID)
				require.NoError(t, err)
				assert.Equal(t, common.IncidentStatusResolved, after.Status)
				assert.False(t, after.ResolvedAt.IsZero())
			},
		},
		{
			name:   "close",
			action: "close_incident",
			assertValid: func(t *testing.T, client *ent.Client, incID int) {
				after, err := client.Incident.Get(context.Background(), incID)
				require.NoError(t, err)
				assert.Equal(t, common.IncidentStatusClosed, after.Status)
				assert.False(t, after.ClosedAt.IsZero())
			},
		},
		{
			name:   "acknowledge",
			action: "acknowledge_incident",
			assertValid: func(t *testing.T, client *ent.Client, incID int) {
				after, err := client.Incident.Get(context.Background(), incID)
				require.NoError(t, err)
				assert.Equal(t, common.IncidentStatusAcknowledged, after.Status)
			},
		},
		{
			name:   "categorize",
			action: "categorize_incident",
			extraVars: map[string]interface{}{
				"category": "network",
			},
			assertValid: func(t *testing.T, client *ent.Client, incID int) {
				after, err := client.Incident.Get(context.Background(), incID)
				require.NoError(t, err)
				assert.Equal(t, common.IncidentStatusTriaged, after.Status)
				assert.Equal(t, "network", after.Category)
			},
		},
		{
			name:   "update",
			action: "update_incident",
			extraVars: map[string]interface{}{
				"title": "改过的标题",
			},
			assertValid: func(t *testing.T, client *ent.Client, incID int) {
				after, err := client.Incident.Get(context.Background(), incID)
				require.NoError(t, err)
				assert.Equal(t, "改过的标题", after.Title)
				assert.Equal(t, common.IncidentStatusNew, after.Status, "update 只提交 title 时不得改状态")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("Valid", func(t *testing.T) {
				client, handler, tenantID, inc, _ := setupIncidentHandlerFixture(t)
				ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

				vars := map[string]interface{}{"action": tc.action, "incident_id": inc.ID}
				for k, v := range tc.extraVars {
					vars[k] = v
				}
				result, err := handler.Execute(ctx, nil, vars)
				require.NoError(t, err)
				assert.True(t, result.Success)
				tc.assertValid(t, client, inc.ID)
			})

			t.Run("CrossTenant", func(t *testing.T) {
				client, handler, tenantID, inc, _ := setupIncidentHandlerFixture(t)
				otherCtx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID+9999)

				before, err := client.Incident.Get(context.Background(), inc.ID)
				require.NoError(t, err)

				vars := map[string]interface{}{"action": tc.action, "incident_id": inc.ID}
				for k, v := range tc.extraVars {
					vars[k] = v
				}
				_, err = handler.Execute(otherCtx, nil, vars)
				assert.Error(t, err, "跨租户写入必须失败")

				after, err := client.Incident.Get(context.Background(), inc.ID)
				require.NoError(t, err)
				assert.Equal(t, before.Status, after.Status, "跨租户请求不得改状态")
				assert.Equal(t, before.Title, after.Title, "跨租户请求不得改标题")
				assert.Equal(t, before.EscalationLevel, after.EscalationLevel)
			})

			t.Run("NoTenant_FailClosed", func(t *testing.T) {
				_, handler, _, inc, _ := setupIncidentHandlerFixture(t)
				// ctx 无租户键、variables 无 tenant_id：必须 fail-closed 而不是裸写
				vars := map[string]interface{}{"action": tc.action, "incident_id": inc.ID}
				for k, v := range tc.extraVars {
					vars[k] = v
				}
				_, err := handler.Execute(context.Background(), nil, vars)
				require.Error(t, err, "租户未知时必须拒绝执行")
				assert.Contains(t, err.Error(), "租户")
			})
		})
	}
}

// 以下测试覆盖 create_incident / assign_incident 从裸 Ent 写入收回到
// IncidentDomainServiceInterface 之后的委派契约：未注入时 fail closed，
// 注入后把请求原样转发给领域服务，assign 额外触发一次状态更新。

func TestIncidentServiceTaskHandler_CreateIncident_RequiresInjectedService(t *testing.T) {
	handler := NewIncidentServiceTaskHandler(nil, zap.NewNop().Sugar())
	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action": "create_incident",
		"title":  "测试事件",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "未注入")
}

func TestIncidentServiceTaskHandler_CreateIncident_DelegatesToInjectedService(t *testing.T) {
	handler := NewIncidentServiceTaskHandler(nil, zap.NewNop().Sugar())
	fake := &fakeIncidentService{createResp: &dto.IncidentResponse{ID: 7, IncidentNumber: "INC-1"}}
	handler.SetIncidentService(fake)

	result, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "create_incident",
		"title":       "测试事件",
		"reporter_id": 3,
	})
	require.NoError(t, err)
	require.Equal(t, "测试事件", fake.lastCreateReq.Title)
	require.Equal(t, 3, fake.lastCreateUserID)
	require.Equal(t, 7, result.OutputVars["incident_id"])
}

func TestIncidentServiceTaskHandler_AssignIncident_DelegatesAndUpdatesStatus(t *testing.T) {
	handler := NewIncidentServiceTaskHandler(nil, zap.NewNop().Sugar())
	fake := &fakeIncidentService{}
	handler.SetIncidentService(fake)

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{
		"action":      "assign_incident",
		"incident_id": 9,
		"assignee_id": 4,
		"tenant_id":   1,
	})
	require.NoError(t, err)
	require.Equal(t, 9, fake.lastAssignID)
	require.Equal(t, 4, fake.lastAssigneeID)
	require.Equal(t, 9, fake.lastStatusID)
	require.Equal(t, "assigned", fake.lastStatus)
}

type fakeIncidentService struct {
	createResp       *dto.IncidentResponse
	lastCreateReq    *dto.CreateIncidentRequest
	lastCreateUserID int
	lastAssignID     int
	lastAssigneeID   int
	lastStatusID     int
	lastStatus       string
}

func (f *fakeIncidentService) CreateIncident(ctx context.Context, req *dto.CreateIncidentRequest, tenantID, userID int) (*dto.IncidentResponse, error) {
	f.lastCreateReq = req
	f.lastCreateUserID = userID
	if f.createResp != nil {
		return f.createResp, nil
	}
	return &dto.IncidentResponse{ID: 1}, nil
}

func (f *fakeIncidentService) AssignIncident(ctx context.Context, id int, assigneeID int, tenantID int) (*dto.IncidentResponse, error) {
	f.lastAssignID = id
	f.lastAssigneeID = assigneeID
	return &dto.IncidentResponse{ID: id}, nil
}

func (f *fakeIncidentService) UpdateStatus(ctx context.Context, id int, status string, tenantID int) (*dto.IncidentResponse, error) {
	f.lastStatusID = id
	f.lastStatus = status
	return &dto.IncidentResponse{ID: id}, nil
}
