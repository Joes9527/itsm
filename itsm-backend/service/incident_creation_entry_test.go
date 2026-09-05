package service_test

import (
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/outboxevent"
	entticket "itsm-backend/ent/ticket"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncidentService_CreateIncident_Success(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	// 创建测试租户和用户
	testTenant, err := createIncidentTestTenant(ctx, client, "create")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "create")
	require.NoError(t, err)
	_, err = client.TicketCategory.Create().SetName("performance").SetCode("performance").SetTenantID(testTenant.ID).Save(ctx)
	require.NoError(t, err)

	// 测试创建事件
	req := &dto.CreateIncidentRequest{
		Title:       "测试事件",
		Description: "这是一个测试事件的描述",
		Priority:    "high",
		Severity:    "medium",
		Category:    "performance",
		Source:      "manual",
	}

	response, err := service.SubmitCreation(ctx, req, testTenant.ID, testUser.ID)
	require.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, req.Title, response.Title)
	assert.Equal(t, req.Priority, response.Priority)
	assert.Equal(t, req.Severity, response.Severity)
	assert.Equal(t, "new", response.Status)
	assert.Nil(t, response.AssigneeID)
	assert.Nil(t, response.ConfigurationItemID)
	assert.NotEmpty(t, response.IncidentNumber)
	assert.Regexp(t, `^TKT-[0-9]{6}-[0-9]{6}$`, response.IncidentNumber)
	assert.Equal(t, testTenant.ID, response.TenantID)
}
func TestIncidentService_CreationReturnsDurablePendingWorkflow(t *testing.T) {
	client, owner, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "pending-workflow")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "pending-workflow")
	require.NoError(t, err)
	deployEntryApproval(t, client, tenant.ID, "incident_approval", "incident")
	result, err := owner.SubmitReceipt(ctx, &dto.CreateIncidentRequest{Title: "Durable creation", Priority: "medium"}, tenant.ID, actor.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", result.WorkflowStartStatus)
	require.Zero(t, client.ProcessInstance.Query().CountX(ctx))
	require.Equal(t, 1, client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("workflow.start.requested")).CountX(ctx))
}

func TestIncidentService_CreateIncident_WithOptionalFields(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "optional")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "optional")
	require.NoError(t, err)

	assignee, err := createIncidentTestUser(ctx, client, testTenant.ID, "assignee")
	require.NoError(t, err)
	parent, err := client.TicketCategory.Create().SetName("security").SetCode("security").SetTenantID(testTenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.TicketCategory.Create().SetName("intrusion").SetCode("intrusion").SetTenantID(testTenant.ID).SetParentID(parent.ID).Save(ctx)
	require.NoError(t, err)

	detectedAt := time.Now().Add(-1 * time.Hour)

	req := &dto.CreateIncidentRequest{
		Title:       "带可选字段的事件",
		Description: "描述",
		Priority:    "critical",
		Severity:    "high",
		Category:    "security",
		Subcategory: "intrusion",
		AssigneeID:  &assignee.ID,
		Source:      "monitoring",
		DetectedAt:  &detectedAt,
		Metadata: map[string]interface{}{
			"source_ip": "192.168.1.100",
			"alert_id":  "ALT-001",
			"automated": true,
		},
	}

	response, err := service.SubmitCreation(ctx, req, testTenant.ID, testUser.ID)
	require.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, "security", response.Category)
	assert.NotNil(t, response.AssigneeID)
	assert.Equal(t, assignee.ID, *response.AssigneeID)
}
func TestIncidentService_CreateIncidentRejectsCrossTenantAssigneeAtomically(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()
	tenantA, err := createIncidentTestTenant(ctx, client, "create-boundary-a")
	require.NoError(t, err)
	tenantB, err := createIncidentTestTenant(ctx, client, "create-boundary-b")
	require.NoError(t, err)
	reporter, err := createIncidentTestUser(ctx, client, tenantA.ID, "create-boundary-a")
	require.NoError(t, err)
	foreignAssignee, err := createIncidentTestUser(ctx, client, tenantB.ID, "create-boundary-b")
	require.NoError(t, err)

	_, err = service.SubmitCreation(ctx, &dto.CreateIncidentRequest{
		Title: "Cross tenant assignment", AssigneeID: &foreignAssignee.ID,
	}, tenantA.ID, reporter.ID)
	require.ErrorContains(t, err, "assignee")
	count, err := client.Incident.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
	eventCount, err := client.IncidentEvent.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, eventCount)
	// 这个校验发生在 CreateIncident 打开事务之前（validateIncidentAssignee 是
	// tx.Begin 之前的前置校验），所以连 WorkItem 都不应该被创建——但明确断言总比
	// 依赖"没打开事务所以自然不会有"这条隐含推理更可靠，尤其是以后如果校验顺序被调整。
	ticketCount, err := client.Ticket.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, ticketCount, "校验失败必须连 WorkItem 都不留下")
}

// TestIncidentService_CreateIncident_CreatesWorkItemInSameTransaction 覆盖统一 WorkItem
// 领域模型宪章 §3.2 的事务边界要求：CreateIncident 必须在同一个事务内同时建好 tickets
// 行（record_class="incident"，创建后不可变）和 incidents 行，且 incidents.work_item_id
// 指回那条 tickets 行；公共字段（标题/描述/优先级/请求人/租户）以 WorkItem 为权威来源。
func TestIncidentService_CreateIncident_CreatesWorkItemInSameTransaction(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "workitem")
	require.NoError(t, err)
	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "workitem")
	require.NoError(t, err)

	req := &dto.CreateIncidentRequest{
		Title:       "数据库连接池耗尽",
		Description: "生产库连接池被打满",
		Priority:    "critical",
	}

	response, err := service.SubmitCreation(ctx, req, testTenant.ID, testUser.ID)
	require.NoError(t, err)
	require.NotNil(t, response.WorkItemID, "CreateIncident 响应必须携带新建的 WorkItem ID")

	workItem, err := client.Ticket.Get(ctx, *response.WorkItemID)
	require.NoError(t, err, "incidents.work_item_id 指向的 tickets 行必须真实存在")
	assert.Equal(t, "incident", workItem.RecordClass)
	assert.Equal(t, req.Title, workItem.Title)
	assert.Equal(t, req.Description, workItem.Description)
	assert.Equal(t, req.Priority, workItem.Priority)
	assert.Equal(t, testUser.ID, workItem.RequesterID)
	assert.Equal(t, testTenant.ID, workItem.TenantID)
	assert.NotEmpty(t, workItem.TicketNumber)

	persistedIncident, err := client.Incident.Get(ctx, response.ID)
	require.NoError(t, err)
	assert.Equal(t, workItem.ID, persistedIncident.WorkItemID, "incidents.work_item_id 必须指回新建的 WorkItem")
}
func TestIncidentService_CreateIncidentAllocatesSequentialWorkItemNumbers(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	tenant, err := createIncidentTestTenant(ctx, client, "allocator")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "allocator")
	require.NoError(t, err)

	first, err := service.SubmitCreation(ctx, &dto.CreateIncidentRequest{Title: "First allocated incident"}, tenant.ID, user.ID)
	require.NoError(t, err)
	second, err := service.SubmitCreation(ctx, &dto.CreateIncidentRequest{Title: "Second allocated incident"}, tenant.ID, user.ID)
	require.NoError(t, err)

	firstWorkItem, err := client.Ticket.Get(ctx, *first.WorkItemID)
	require.NoError(t, err)
	secondWorkItem, err := client.Ticket.Get(ctx, *second.WorkItemID)
	require.NoError(t, err)
	require.Regexp(t, `^TKT-\d{6}-000001$`, firstWorkItem.TicketNumber)
	require.Regexp(t, `^TKT-\d{6}-000002$`, secondWorkItem.TicketNumber)
}

// TestIncidentService_CreateIncident_ServiceCatalogDivertedPath_AlsoCreatesWorkItem
// 覆盖服务目录 itsm_type=Incident 的报障分流路径（handlers/service_request/service.go 的
// isIncidentCatalog + createIncidentFromCatalog）。该路径通过 IncidentCreator 接口
// （生产环境由 internal/bootstrap/app.go 的 srIncidentBridge 适配）最终调用的正是
// IncidentService.CreateIncident——这里用与 srIncidentBridge.CreateIncident 完全相同的
// 请求形状直接调用同一个函数，验证服务目录分流路径同样会产生 WorkItem，不再像 Wave 2
// 之前那样绕开 Ticket。跨包集成的另一半（Service.Create 确实会在 isIncidentCatalog
// 命中时委托给 IncidentCreator、且不产生 ServiceRequest 行）由
// handlers/service_request/regression_test.go 的
// TestService_Create_IncidentCatalog_NoServiceRequestRowCreated 覆盖，该测试的注释
// 明确把"Incident 侧完整行为"这一半留给了本任务包。
func TestIncidentService_CreateIncident_ServiceCatalogDivertedPath_AlsoCreatesWorkItem(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "catalog")
	require.NoError(t, err)
	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "catalog")
	require.NoError(t, err)

	// 镜像 srIncidentBridge.CreateIncident 传给 IncidentService.CreateIncident 的请求形状。
	response, err := service.SubmitCreation(ctx, &dto.CreateIncidentRequest{
		Title:       "服务目录报障：VPN无法连接",
		Description: "员工反馈无法连接VPN",
		Type:        "incident",
		Priority:    "medium",
	}, testTenant.ID, testUser.ID)
	require.NoError(t, err)
	require.NotNil(t, response.WorkItemID)

	workItem, err := client.Ticket.Get(ctx, *response.WorkItemID)
	require.NoError(t, err)
	assert.Equal(t, "incident", workItem.RecordClass)
	assert.Equal(t, testTenant.ID, workItem.TenantID)
}

// TestIncidentService_CreateIncident_TenantIsolation_FailClosed 覆盖 AGENTS.md 的租户强
// 闭合约束：跨租户既不能读到别的租户的 Incident，也不能读到它关联的 WorkItem。
func TestIncidentService_CreateIncident_TenantIsolation_FailClosed(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	tenantA, err := createIncidentTestTenant(ctx, client, "iso-a")
	require.NoError(t, err)
	tenantB, err := createIncidentTestTenant(ctx, client, "iso-b")
	require.NoError(t, err)
	userA, err := createIncidentTestUser(ctx, client, tenantA.ID, "iso-a")
	require.NoError(t, err)

	response, err := service.SubmitCreation(ctx, &dto.CreateIncidentRequest{
		Title: "租户A的机密事件",
	}, tenantA.ID, userA.ID)
	require.NoError(t, err)
	require.NotNil(t, response.WorkItemID)

	// 用租户 B 的身份读取租户 A 的 Incident：必须 Fail Closed（"not found"），不能静默
	// 放行或返回空集合伪装成功。
	_, err = service.GetIncident(ctx, response.ID, tenantB.ID)
	require.Error(t, err, "跨租户读取 Incident 必须失败")

	// WorkItem 本身也要遵守同样的租户边界——直接用 ent 查询验证底层数据没有跨租户可见。
	_, err = client.Ticket.Query().
		Where(entticket.IDEQ(*response.WorkItemID), entticket.TenantIDEQ(tenantB.ID)).
		Only(ctx)
	require.Error(t, err, "WorkItem 不能被其它租户查到")
	require.True(t, ent.IsNotFound(err))

	// 用正确的租户仍然能读到，证明上面的失败确实是租户过滤生效，不是数据本身就有问题。
	workItem, err := client.Ticket.Query().
		Where(entticket.IDEQ(*response.WorkItemID), entticket.TenantIDEQ(tenantA.ID)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "incident", workItem.RecordClass)
}
