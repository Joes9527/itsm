package bpmn

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketcategory"

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
	workItem, err := s.client.Ticket.Create().
		SetTitle(req.Title).
		SetDescription(req.Description).
		SetStatus(common.IncidentStatusNew).
		SetPriority(req.Priority).
		SetType("incident").
		SetRecordClass("incident").
		SetTicketNumber("TKT-BPMN-INCIDENT-FIXTURE").
		SetRequesterID(userID).
		SetOpenedByID(userID).
		SetTenantID(tenantID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	inc, err := s.client.Incident.Create().
		SetType(req.Type).
		SetSeverity(req.Severity).
		SetWorkItemID(workItem.ID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return &dto.IncidentResponse{ID: inc.ID, IncidentNumber: inc.IncidentNumber}, nil
}

func (s *dbBackedIncidentService) AssignIncident(ctx context.Context, id int, assigneeID int, tenantID int) (*dto.IncidentResponse, error) {
	entity, err := s.client.Incident.Query().Where(incident.ID(id), incident.HasWorkItemWith(ticket.TenantID(tenantID))).Only(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.client.Ticket.UpdateOneID(entity.WorkItemID).Where(ticket.TenantID(tenantID)).SetAssigneeID(assigneeID).SetStatus(common.IncidentStatusAssigned).Save(ctx); err != nil {
		return nil, err
	}
	return &dto.IncidentResponse{ID: id}, nil
}

func (s *dbBackedIncidentService) UpdateStatus(ctx context.Context, id int, status string, tenantID int) (*dto.IncidentResponse, error) {
	entity, err := s.client.Incident.Query().Where(incident.ID(id), incident.HasWorkItemWith(ticket.TenantID(tenantID))).Only(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.client.Ticket.UpdateOneID(entity.WorkItemID).Where(ticket.TenantID(tenantID)).SetStatus(status).Save(ctx); err != nil {
		return nil, err
	}
	return &dto.IncidentResponse{ID: id}, nil
}

// EscalateIncidentLevel/ResolveIncidentForWorkflow/CloseIncidentForWorkflow/
// AcknowledgeIncidentForWorkflow/UpdateIncidentForWorkflow/CategorizeIncidentForWorkflow
// 镜像 service.IncidentService 里同名方法的写入语义（见该文件"BPMN 工作流专用写入方法"
// 一节的注释），保持这个 fixture 与真实实现行为一致。

func (s *dbBackedIncidentService) EscalateIncidentLevel(ctx context.Context, id, tenantID, level int) (*dto.IncidentResponse, error) {
	current, err := s.client.Incident.Query().Where(incident.ID(id), incident.HasWorkItemWith(ticket.TenantID(tenantID))).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found")
		}
		return nil, err
	}
	if level <= 0 {
		level = current.EscalationLevel + 1
	}
	updated, err := s.client.Incident.UpdateOneID(id).
		Where(incident.HasWorkItemWith(ticket.TenantID(tenantID))).
		SetEscalationLevel(level).
		SetEscalatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.client.Ticket.UpdateOneID(current.WorkItemID).Where(ticket.TenantID(tenantID)).SetStatus(common.IncidentStatusEscalated).Save(ctx); err != nil {
		return nil, err
	}
	return &dto.IncidentResponse{ID: id, EscalationLevel: updated.EscalationLevel}, nil
}

func (s *dbBackedIncidentService) ResolveIncidentForWorkflow(ctx context.Context, id, tenantID int, resolution string) (*dto.IncidentResponse, error) {
	entity, err := s.client.Incident.Query().Where(incident.ID(id), incident.HasWorkItemWith(ticket.TenantID(tenantID))).Only(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.client.Ticket.UpdateOneID(entity.WorkItemID).Where(ticket.TenantID(tenantID)).SetStatus(common.IncidentStatusResolved).SetResolvedAt(time.Now()).Save(ctx); err != nil {
		return nil, err
	}
	return &dto.IncidentResponse{ID: id}, nil
}

func (s *dbBackedIncidentService) CloseIncidentForWorkflow(ctx context.Context, id, tenantID int, feedback string) (*dto.IncidentResponse, error) {
	entity, err := s.client.Incident.Query().Where(incident.ID(id), incident.HasWorkItemWith(ticket.TenantID(tenantID))).Only(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.client.Ticket.UpdateOneID(entity.WorkItemID).Where(ticket.TenantID(tenantID)).SetStatus(common.IncidentStatusClosed).SetClosedAt(time.Now()).Save(ctx); err != nil {
		return nil, err
	}
	return &dto.IncidentResponse{ID: id}, nil
}

func (s *dbBackedIncidentService) AcknowledgeIncidentForWorkflow(ctx context.Context, id, tenantID int) (*dto.IncidentResponse, error) {
	entity, err := s.client.Incident.Query().Where(incident.ID(id), incident.HasWorkItemWith(ticket.TenantID(tenantID))).Only(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.client.Ticket.UpdateOneID(entity.WorkItemID).Where(ticket.TenantID(tenantID)).SetStatus(common.IncidentStatusAcknowledged).Save(ctx); err != nil {
		return nil, err
	}
	return &dto.IncidentResponse{ID: id}, nil
}

func (s *dbBackedIncidentService) UpdateIncidentForWorkflow(ctx context.Context, id, tenantID int, title, description, priority, severity, status string) (*dto.IncidentResponse, error) {
	entity, err := s.client.Incident.Query().Where(incident.ID(id), incident.HasWorkItemWith(ticket.TenantID(tenantID))).Only(ctx)
	if err != nil {
		return nil, err
	}
	if severity != "" {
		if _, err := s.client.Incident.UpdateOneID(id).
			Where(incident.HasWorkItemWith(ticket.TenantID(tenantID))).
			SetSeverity(severity).
			Save(ctx); err != nil {
			return nil, err
		}
	}
	workItemUpdate := s.client.Ticket.UpdateOneID(entity.WorkItemID).Where(ticket.TenantID(tenantID))
	if title != "" {
		workItemUpdate.SetTitle(title)
	}
	if description != "" {
		workItemUpdate.SetDescription(description)
	}
	if priority != "" {
		workItemUpdate.SetPriority(priority)
	}
	if status != "" {
		workItemUpdate.SetStatus(status)
	}
	if _, err := workItemUpdate.Save(ctx); err != nil {
		return nil, err
	}
	return &dto.IncidentResponse{ID: id}, nil
}

func (s *dbBackedIncidentService) CategorizeIncidentForWorkflow(ctx context.Context, id, tenantID int, category, subcategory string) (*dto.IncidentResponse, error) {
	entity, err := s.client.Incident.Query().Where(incident.ID(id), incident.HasWorkItemWith(ticket.TenantID(tenantID))).Only(ctx)
	if err != nil {
		return nil, err
	}
	workItemUpdate := s.client.Ticket.UpdateOneID(entity.WorkItemID).Where(ticket.TenantID(tenantID))
	if category != "" {
		categoryEntity, categoryErr := s.client.TicketCategory.Query().Where(ticketcategory.TenantID(tenantID), ticketcategory.Code(category)).Only(ctx)
		if categoryErr != nil {
			return nil, categoryErr
		}
		workItemUpdate.SetCategoryID(categoryEntity.ID)
	}
	if _, err := workItemUpdate.SetStatus(common.IncidentStatusTriaged).Save(ctx); err != nil {
		return nil, err
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
	workItem, err := client.Ticket.Create().SetTitle("测试事件").SetStatus(common.IncidentStatusNew).SetPriority("medium").SetType("incident").SetRecordClass("incident").SetTicketNumber("TKT-INCIDENT-HANDLER").SetRequesterID(reporter.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.TicketCategory.Create().SetName("Network").SetCode("network").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	inc, err := client.Incident.Create().
		SetIncidentNumber("INC-IH-1").
		SetWorkItemID(workItem.ID).
		Save(ctx)
	require.NoError(t, err)

	handler := NewIncidentServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	handler.SetIncidentService(&dbBackedIncidentService{client: client})
	return client, handler, tenant.ID, inc, assignee.ID
}

func requireHandlerIncidentWorkItem(t *testing.T, client *ent.Client, entity *ent.Incident) *ent.Ticket {
	t.Helper()
	workItem, err := client.Ticket.Get(context.Background(), entity.WorkItemID)
	require.NoError(t, err)
	return workItem
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
			assert.Equal(t, common.IncidentStatusNew, requireHandlerIncidentWorkItem(t, client, after).Status, "空态跳过时不应该改状态")
			assert.Zero(t, requireHandlerIncidentWorkItem(t, client, after).AssigneeID, "空态跳过时不应该写处理人")
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
	assert.Equal(t, assigneeID, requireHandlerIncidentWorkItem(t, client, after).AssigneeID)
	assert.Equal(t, common.IncidentStatusAssigned, requireHandlerIncidentWorkItem(t, client, after).Status)
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

// TestIncidentServiceTaskHandler_AssignIncident_CrossTenant 证明分配写入带租户过滤。
// 回调业务身份必须来自流程实例，handler 仍需对权威租户做目标行过滤。
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
	assert.Zero(t, requireHandlerIncidentWorkItem(t, client, after).AssigneeID, "跨租户请求不得写入处理人")
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
				assert.Equal(t, common.IncidentStatusEscalated, requireHandlerIncidentWorkItem(t, client, after).Status)
				assert.False(t, after.EscalatedAt.IsZero())
			},
		},
		{
			name:   "resolve",
			action: "resolve_incident",
			assertValid: func(t *testing.T, client *ent.Client, incID int) {
				after, err := client.Incident.Get(context.Background(), incID)
				require.NoError(t, err)
				assert.Equal(t, common.IncidentStatusResolved, requireHandlerIncidentWorkItem(t, client, after).Status)
				assert.NotNil(t, requireHandlerIncidentWorkItem(t, client, after).ResolvedAt)
			},
		},
		{
			name:   "close",
			action: "close_incident",
			assertValid: func(t *testing.T, client *ent.Client, incID int) {
				after, err := client.Incident.Get(context.Background(), incID)
				require.NoError(t, err)
				assert.Equal(t, common.IncidentStatusClosed, requireHandlerIncidentWorkItem(t, client, after).Status)
				assert.NotNil(t, requireHandlerIncidentWorkItem(t, client, after).ClosedAt)
			},
		},
		{
			name:   "acknowledge",
			action: "acknowledge_incident",
			assertValid: func(t *testing.T, client *ent.Client, incID int) {
				after, err := client.Incident.Get(context.Background(), incID)
				require.NoError(t, err)
				assert.Equal(t, common.IncidentStatusAcknowledged, requireHandlerIncidentWorkItem(t, client, after).Status)
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
				assert.Equal(t, common.IncidentStatusTriaged, requireHandlerIncidentWorkItem(t, client, after).Status)
				workItem := requireHandlerIncidentWorkItem(t, client, after)
				categoryEntity, err := workItem.QueryCategory().Only(context.Background())
				require.NoError(t, err)
				assert.Equal(t, "network", categoryEntity.Code)
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
				workItem := requireHandlerIncidentWorkItem(t, client, after)
				assert.Equal(t, "改过的标题", workItem.Title)
				assert.Equal(t, common.IncidentStatusNew, workItem.Status, "update 只提交 title 时不得改状态")
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
				beforeWorkItem := requireHandlerIncidentWorkItem(t, client, before)

				vars := map[string]interface{}{"action": tc.action, "incident_id": inc.ID}
				for k, v := range tc.extraVars {
					vars[k] = v
				}
				_, err = handler.Execute(otherCtx, nil, vars)
				assert.Error(t, err, "跨租户写入必须失败")

				after, err := client.Incident.Get(context.Background(), inc.ID)
				require.NoError(t, err)
				afterWorkItem := requireHandlerIncidentWorkItem(t, client, after)
				assert.Equal(t, beforeWorkItem.Status, afterWorkItem.Status, "跨租户请求不得改状态")
				assert.Equal(t, beforeWorkItem.Title, afterWorkItem.Title, "跨租户请求不得改标题")
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
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, 1)
	_, err := handler.Execute(ctx, nil, map[string]interface{}{
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

	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, 1)
	result, err := handler.Execute(ctx, nil, map[string]interface{}{
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

	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, 1)
	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "assign_incident",
		"incident_id": 9,
		"assignee_id": 4,
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

func (f *fakeIncidentService) EscalateIncidentLevel(ctx context.Context, id, tenantID, level int) (*dto.IncidentResponse, error) {
	if level <= 0 {
		level = 1
	}
	return &dto.IncidentResponse{ID: id, EscalationLevel: level}, nil
}

func (f *fakeIncidentService) ResolveIncidentForWorkflow(ctx context.Context, id, tenantID int, resolution string) (*dto.IncidentResponse, error) {
	return &dto.IncidentResponse{ID: id, Status: common.IncidentStatusResolved}, nil
}

func (f *fakeIncidentService) CloseIncidentForWorkflow(ctx context.Context, id, tenantID int, feedback string) (*dto.IncidentResponse, error) {
	return &dto.IncidentResponse{ID: id, Status: common.IncidentStatusClosed}, nil
}

func (f *fakeIncidentService) AcknowledgeIncidentForWorkflow(ctx context.Context, id, tenantID int) (*dto.IncidentResponse, error) {
	return &dto.IncidentResponse{ID: id, Status: common.IncidentStatusAcknowledged}, nil
}

func (f *fakeIncidentService) UpdateIncidentForWorkflow(ctx context.Context, id, tenantID int, title, description, priority, severity, status string) (*dto.IncidentResponse, error) {
	return &dto.IncidentResponse{ID: id, Title: title}, nil
}

func (f *fakeIncidentService) CategorizeIncidentForWorkflow(ctx context.Context, id, tenantID int, category, subcategory string) (*dto.IncidentResponse, error) {
	return &dto.IncidentResponse{ID: id, Category: category, Subcategory: subcategory}, nil
}
