package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/incidentalert"
	"itsm-backend/ent/incidentevent"
	"itsm-backend/ent/incidentmetric"
	entticket "itsm-backend/ent/ticket"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// ==================== 测试设置辅助函数 ====================

func setupIncidentTest(t *testing.T) (*ent.Client, *IncidentService, context.Context) {
	client := enttest.Open(t, "sqlite3", testDSN())
	logger := zaptest.NewLogger(t).Sugar()
	service := NewIncidentService(client, logger, workitemnumber.NewPostgreSQLAllocator())
	ctx := context.Background()
	return client, service, ctx
}

func createIncidentTestTenant(ctx context.Context, client *ent.Client, suffix string) (*ent.Tenant, error) {
	return client.Tenant.Create().
		SetName("Test Tenant " + suffix).
		SetCode("test" + suffix).
		SetDomain("test" + suffix + ".com").
		SetStatus("active").
		Save(ctx)
}

func createIncidentTestUser(ctx context.Context, client *ent.Client, tenantID int, suffix string) (*ent.User, error) {
	return client.User.Create().
		SetUsername("testuser" + suffix).
		SetEmail("test" + suffix + "@example.com").
		SetName("Test User").
		SetPasswordHash("hashedpassword").
		SetRole("agent").
		SetActive(true).
		SetTenantID(tenantID).
		Save(ctx)
}

func createIncidentTestWorkItem(t *testing.T, ctx context.Context, client *ent.Client, tenantID, requesterID int, title, status, priority string) *ent.Ticket {
	t.Helper()
	workItem, err := client.Ticket.Create().
		SetTitle(title).SetStatus(status).SetPriority(priority).
		SetType("incident").SetRecordClass("incident").
		SetTicketNumber(fmt.Sprintf("TKT-TEST-%d-%d", tenantID, time.Now().UnixNano())).
		SetRequesterID(requesterID).SetTenantID(tenantID).Save(ctx)
	require.NoError(t, err)
	return workItem
}

func setIncidentFixtureWorkItemFields(t *testing.T, ctx context.Context, client *ent.Client, entity *ent.Incident, title, description, status, priority string) {
	t.Helper()
	_, err := client.Ticket.UpdateOneID(entity.WorkItemID).
		SetTitle(title).SetDescription(description).SetStatus(status).SetPriority(priority).Save(ctx)
	require.NoError(t, err)
}

type blockingIncidentProcessTrigger struct {
	ProcessTriggerServiceInterface
	entered chan struct{}
	release chan struct{}
}

func (f *blockingIncidentProcessTrigger) TriggerProcess(context.Context, *dto.ProcessTriggerRequest) (*dto.ProcessTriggerResponse, error) {
	close(f.entered)
	<-f.release
	return &dto.ProcessTriggerResponse{ProcessInstanceID: 1}, nil
}

// ==================== 创建事件测试 ====================

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

	response, err := service.CreateIncident(ctx, req, testTenant.ID, testUser.ID)
	require.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, req.Title, response.Title)
	assert.Equal(t, req.Priority, response.Priority)
	assert.Equal(t, req.Severity, response.Severity)
	assert.Equal(t, "new", response.Status)
	assert.Nil(t, response.AssigneeID)
	assert.Nil(t, response.ConfigurationItemID)
	assert.NotEmpty(t, response.IncidentNumber)
	assert.Contains(t, response.IncidentNumber, "INC-")
	assert.Equal(t, testTenant.ID, response.TenantID)
}

func TestIncidentService_CreateIncidentWaitsForPostCommitWorkflow(t *testing.T) {
	client, incidentService, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "inline-workflow")
	require.NoError(t, err)
	reporter, err := createIncidentTestUser(ctx, client, tenant.ID, "inline-workflow")
	require.NoError(t, err)

	trigger := &blockingIncidentProcessTrigger{entered: make(chan struct{}), release: make(chan struct{})}
	incidentService.SetProcessTriggerService(trigger)
	result := make(chan error, 1)
	go func() {
		_, createErr := incidentService.CreateIncident(ctx, &dto.CreateIncidentRequest{Title: "Inline workflow boundary"}, tenant.ID, reporter.ID)
		result <- createErr
	}()

	select {
	case <-trigger.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("workflow trigger was not entered")
	}
	select {
	case createErr := <-result:
		t.Fatalf("CreateIncident returned before its post-commit workflow finished: %v", createErr)
	default:
	}

	close(trigger.release)
	select {
	case createErr := <-result:
		require.NoError(t, createErr)
	case <-time.After(2 * time.Second):
		t.Fatal("CreateIncident did not return after workflow completion")
	}
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

	response, err := service.CreateIncident(ctx, req, testTenant.ID, testUser.ID)
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

	_, err = service.CreateIncident(ctx, &dto.CreateIncidentRequest{
		Title: "Cross tenant assignment", AssigneeID: &foreignAssignee.ID,
	}, tenantA.ID, reporter.ID)
	require.ErrorContains(t, err, "assignee not found or inactive")
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

// ==================== WorkItem 迁移测试（Wave 2） ====================

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

	response, err := service.CreateIncident(ctx, req, testTenant.ID, testUser.ID)
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

	first, err := service.CreateIncident(ctx, &dto.CreateIncidentRequest{Title: "First allocated incident"}, tenant.ID, user.ID)
	require.NoError(t, err)
	second, err := service.CreateIncident(ctx, &dto.CreateIncidentRequest{Title: "Second allocated incident"}, tenant.ID, user.ID)
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
	response, err := service.CreateIncident(ctx, &dto.CreateIncidentRequest{
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

	response, err := service.CreateIncident(ctx, &dto.CreateIncidentRequest{
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

// ==================== 获取事件测试 ====================

func TestIncidentService_GetIncident_Success(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "get")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "get")
	require.NoError(t, err)

	// 创建测试事件
	workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "测试事件", "new", "medium")
	testIncident, err := client.Incident.Create().
		SetSeverity("medium").
		SetIncidentNumber("INC-202401-000001").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// 测试获取事件
	response, err := service.GetIncident(ctx, testIncident.ID, testTenant.ID)
	require.NoError(t, err)
	assert.Equal(t, testIncident.ID, response.ID)
	assert.Equal(t, workItem.Title, response.Title)
	assert.Equal(t, workItem.Status, response.Status)
}

func TestIncidentService_GetIncident_NotFound(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "notfound")
	require.NoError(t, err)

	// 测试获取不存在的事件
	_, err = service.GetIncident(ctx, 99999, testTenant.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incident not found")
}

func TestIncidentService_GetIncident_TenantMismatch(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant1, err := createIncidentTestTenant(ctx, client, "tenant1")
	require.NoError(t, err)

	testTenant2, err := createIncidentTestTenant(ctx, client, "tenant2")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant1.ID, "tenant")
	require.NoError(t, err)
	workItem := createIncidentTestWorkItem(t, ctx, client, testTenant1.ID, testUser.ID, "Tenant scoped incident", common.IncidentStatusNew, "medium")

	// 在 tenant1 下创建事件
	testIncident, err := client.Incident.Create().
		SetSeverity("medium").
		SetIncidentNumber("INC-TENANT-001").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// 尝试用 tenant2 获取事件，应该失败
	_, err = service.GetIncident(ctx, testIncident.ID, testTenant2.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incident not found")
}

func TestIncidentService_AssignIncident_ValidatesAssigneeAndReturnsUpdatedIncident(t *testing.T) {
	client, incidentService, ctx := setupIncidentTest(t)
	defer client.Close()

	tenant, err := createIncidentTestTenant(ctx, client, "assign")
	require.NoError(t, err)
	reporter, err := createIncidentTestUser(ctx, client, tenant.ID, "assign-reporter")
	require.NoError(t, err)
	assignee, err := createIncidentTestUser(ctx, client, tenant.ID, "assign-agent")
	require.NoError(t, err)
	workItem := createIncidentTestWorkItem(t, ctx, client, tenant.ID, reporter.ID, "Assign incident", common.IncidentStatusNew, "high")

	incidentEntity, err := client.Incident.Create().
		SetSeverity("medium").
		SetIncidentNumber("INC-ASSIGN-001").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Ticket.UpdateOneID(workItem.ID).SetDescription("desc").Save(ctx)
	require.NoError(t, err)

	response, err := incidentService.AssignIncident(ctx, incidentEntity.ID, assignee.ID, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, response.AssigneeID)
	assert.Equal(t, assignee.ID, *response.AssigneeID)
	assert.Equal(t, workItem.Version+1, response.Version)

	otherTenant, err := createIncidentTestTenant(ctx, client, "assign-other")
	require.NoError(t, err)
	otherUser, err := createIncidentTestUser(ctx, client, otherTenant.ID, "assign-other")
	require.NoError(t, err)
	_, err = incidentService.AssignIncident(ctx, incidentEntity.ID, otherUser.ID, tenant.ID)
	require.ErrorContains(t, err, "assignee not found or inactive")

	inactive, err := createIncidentTestUser(ctx, client, tenant.ID, "assign-inactive")
	require.NoError(t, err)
	_, err = inactive.Update().SetActive(false).Save(ctx)
	require.NoError(t, err)
	_, err = incidentService.AssignIncident(ctx, incidentEntity.ID, inactive.ID, tenant.ID)
	require.ErrorContains(t, err, "assignee not found or inactive")
}

func TestAssignIncidentRejectsTerminalStatuses(t *testing.T) {
	for _, status := range []string{common.IncidentStatusResolved, common.IncidentStatusClosed, common.IncidentStatusCancelled} {
		t.Run(status, func(t *testing.T) {
			client, incidentService, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "assign-"+status)
			require.NoError(t, err)
			reporter, err := createIncidentTestUser(ctx, client, tenant.ID, "assign-reporter-"+status)
			require.NoError(t, err)
			assignee, err := createIncidentTestUser(ctx, client, tenant.ID, "assign-target-"+status)
			require.NoError(t, err)
			workItem := createIncidentTestWorkItem(t, ctx, client, tenant.ID, reporter.ID, "Lifecycle guarded assignment", status, "medium")
			incidentEntity, err := client.Incident.Create().
				SetIncidentNumber("INC-ASSIGN-" + status).
				SetWorkItemID(workItem.ID).
				Save(ctx)
			require.NoError(t, err)

			_, err = incidentService.AssignIncident(ctx, incidentEntity.ID, assignee.ID, tenant.ID)
			require.ErrorContains(t, err, "cannot be reassigned")

			persisted, err := client.Ticket.Get(ctx, workItem.ID)
			require.NoError(t, err)
			require.Zero(t, persisted.AssigneeID)
		})
	}
}

func TestAssignIncidentRejectsConcurrentSnapshotChange(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		mutateRace func(context.Context, *ent.Client, int) error
		assertErr  func(*testing.T, error)
	}{
		{
			name: "terminal status without version bump",
			mutateRace: func(ctx context.Context, racer *ent.Client, incidentID int) error {
				entity, err := racer.Incident.Get(ctx, incidentID)
				if err != nil {
					return err
				}
				return racer.Ticket.UpdateOneID(entity.WorkItemID).SetStatus(common.IncidentStatusResolved).Exec(ctx)
			},
			assertErr: func(t *testing.T, err error) {
				require.ErrorContains(t, err, "resolved or closed incidents cannot be reassigned")
			},
		},
		{
			name: "version change while status remains eligible",
			mutateRace: func(ctx context.Context, racer *ent.Client, incidentID int) error {
				entity, err := racer.Incident.Get(ctx, incidentID)
				if err != nil {
					return err
				}
				return racer.Ticket.UpdateOneID(entity.WorkItemID).AddVersion(1).Exec(ctx)
			},
			assertErr: func(t *testing.T, err error) {
				var conflict *common.VersionConflictError
				require.ErrorAs(t, err, &conflict)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dsn := testDSN()
			client := enttest.Open(t, "sqlite3", dsn)
			defer client.Close()
			racer, err := ent.Open("sqlite3", dsn)
			require.NoError(t, err)
			defer racer.Close()
			ctx := context.Background()
			tenant, err := createIncidentTestTenant(ctx, client, "assign-race-"+testCase.name)
			require.NoError(t, err)
			reporter, err := createIncidentTestUser(ctx, client, tenant.ID, "assign-race-reporter-"+testCase.name)
			require.NoError(t, err)
			assignee, err := createIncidentTestUser(ctx, client, tenant.ID, "assign-race-target-"+testCase.name)
			require.NoError(t, err)
			workItem := createIncidentTestWorkItem(t, ctx, client, tenant.ID, reporter.ID, "Concurrent assignment", common.IncidentStatusNew, "medium")
			incidentEntity, err := client.Incident.Create().
				SetIncidentNumber("INC-ASSIGN-RACE-" + testCase.name).
				SetWorkItemID(workItem.ID).
				Save(ctx)
			require.NoError(t, err)

			raced := false
			client.Ticket.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					if !raced {
						raced = true
						require.NoError(t, testCase.mutateRace(ctx, racer, incidentEntity.ID))
					}
					return next.Mutate(ctx, mutation)
				})
			})

			incidentService := NewIncidentService(client, zaptest.NewLogger(t).Sugar(), workitemnumber.NewPostgreSQLAllocator())
			_, err = incidentService.AssignIncident(ctx, incidentEntity.ID, assignee.ID, tenant.ID)
			require.Error(t, err)
			testCase.assertErr(t, err)
			require.True(t, raced)

			persisted, err := client.Ticket.Get(ctx, workItem.ID)
			require.NoError(t, err)
			require.Zero(t, persisted.AssigneeID)
			eventCount, err := client.IncidentEvent.Query().
				Where(incidentevent.IncidentIDEQ(incidentEntity.ID), incidentevent.EventTypeEQ("assignment")).
				Count(ctx)
			require.NoError(t, err)
			require.Zero(t, eventCount)
		})
	}
}

func TestGetIncidentWithActionsUsesOneEntitySnapshot(t *testing.T) {
	var incidentSelects int
	countIncidentSelects := func(args ...any) {
		statement := fmt.Sprint(args...)
		if strings.Contains(statement, "SELECT") && strings.Contains(statement, "FROM `incidents`") {
			incidentSelects++
		}
	}
	client := enttest.Open(t, "sqlite3", testDSN(), enttest.WithOptions(ent.Log(countIncidentSelects), ent.Debug()))
	defer client.Close()
	ctx := context.Background()
	tenant, err := createIncidentTestTenant(ctx, client, "detail-snapshot")
	require.NoError(t, err)
	reporter, err := createIncidentTestUser(ctx, client, tenant.ID, "detail-snapshot")
	require.NoError(t, err)
	workItem, err := client.Ticket.Create().
		SetTitle("Snapshot WorkItem").
		SetTicketNumber("TKT-SNAPSHOT").
		SetStatus(common.IncidentStatusInProgress).
		SetPriority("high").
		SetRequesterID(reporter.ID).
		SetTenantID(tenant.ID).
		SetRecordClass("incident").
		Save(ctx)
	require.NoError(t, err)
	incidentEntity, err := client.Incident.Create().
		SetIncidentNumber("INC-SNAPSHOT").
		SetWorkItemID(workItem.ID).
		Save(ctx)
	require.NoError(t, err)

	incidentSelects = 0
	incidentService := NewIncidentService(client, zaptest.NewLogger(t).Sugar(), workitemnumber.NewPostgreSQLAllocator())
	response, err := incidentService.GetIncidentWithActions(ctx, incidentEntity.ID, ActionActor{
		Client: client, TenantID: tenant.ID, UserID: reporter.ID, Role: "super_admin",
	})
	require.NoError(t, err)
	require.Equal(t, common.IncidentStatusInProgress, response.Status)
	require.True(t, response.Actions["resolve"].Allowed)
	require.Equal(t, 1, incidentSelects, "detail DTO and actions must derive from one Incident entity read")
}

// ==================== 列出事件测试 ====================

func TestIncidentService_ListIncidents_Pagination(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "list")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "list")
	require.NoError(t, err)

	// 创建多个测试事件
	for i := 0; i < 15; i++ {
		workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, fmt.Sprintf("Test Incident %d", i+1), common.IncidentStatusNew, "medium")
		_, err := client.Ticket.UpdateOneID(workItem.ID).SetDescription("Test description").Save(ctx)
		require.NoError(t, err)
		_, err = client.Incident.Create().
			SetSeverity("low").
			SetIncidentNumber(fmt.Sprintf("INC-LIST-%03d", i+1)).
			SetWorkItemID(workItem.ID).
			SetDetectedAt(time.Now()).
			Save(ctx)
		require.NoError(t, err)
	}

	// 测试第一页
	responses, total, err := service.ListIncidents(ctx, testTenant.ID, 1, 10, map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, 15, total)
	assert.Len(t, responses, 10)

	// 测试第二页
	responses, total, err = service.ListIncidents(ctx, testTenant.ID, 2, 10, map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, 15, total)
	assert.Len(t, responses, 5)
}

func TestIncidentService_ListIncidents_Filters(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "filter")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "filter")
	require.NoError(t, err)

	// 创建不同状态和优先级的事件
	statuses := []string{"new", "in_progress", "resolved"}
	priorities := []string{"low", "medium", "high"}

	for i, status := range statuses {
		for j, priority := range priorities {
			workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, fmt.Sprintf("filter-%d-%d", i, j), status, priority)
			_, err := client.Incident.Create().
				SetSeverity("medium").
				SetIncidentNumber(fmt.Sprintf("INC-FLT-%d%d", i, j)).
				SetWorkItemID(workItem.ID).
				SetDetectedAt(time.Now()).
				Save(ctx)
			require.NoError(t, err)
		}
	}

	// 测试状态过滤
	responses, total, err := service.ListIncidents(ctx, testTenant.ID, 1, 10, map[string]interface{}{
		"status": "new",
	})
	require.NoError(t, err)
	assert.Equal(t, 3, total) // 3 个优先级 × 1 个状态
	assert.Len(t, responses, 3)

	// 测试优先级过滤
	_, total, err = service.ListIncidents(ctx, testTenant.ID, 1, 10, map[string]interface{}{
		"priority": "high",
	})
	require.NoError(t, err)
	assert.Equal(t, 3, total) // 3 个状态 × 1 个优先级

	// 测试组合过滤
	_, total, err = service.ListIncidents(ctx, testTenant.ID, 1, 10, map[string]interface{}{
		"status":   "in_progress",
		"priority": "high",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
}

func TestIncidentService_ListIncidents_KeywordSearch(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "search")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "search")
	require.NoError(t, err)

	// 创建带有关键词的事件
	databaseWorkItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "数据库连接失败", common.IncidentStatusNew, "critical")
	_, err = client.Ticket.UpdateOneID(databaseWorkItem.ID).SetDescription("生产环境数据库无法连接").Save(ctx)
	require.NoError(t, err)
	_, err = client.Incident.Create().
		SetSeverity("high").
		SetIncidentNumber("INC-DB-001").
		SetWorkItemID(databaseWorkItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	networkWorkItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "网络延迟问题", common.IncidentStatusNew, "medium")
	_, err = client.Ticket.UpdateOneID(networkWorkItem.ID).SetDescription("用户反馈网络响应缓慢").Save(ctx)
	require.NoError(t, err)
	_, err = client.Incident.Create().
		SetSeverity("medium").
		SetIncidentNumber("INC-NET-001").
		SetWorkItemID(networkWorkItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// 搜索关键词 "数据库"
	responses, total, err := service.ListIncidents(ctx, testTenant.ID, 1, 10, map[string]interface{}{
		"keyword": "数据库",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Contains(t, responses[0].Title, "数据库")

	// 搜索关键词 "网络"
	responses, total, err = service.ListIncidents(ctx, testTenant.ID, 1, 10, map[string]interface{}{
		"keyword": "网络",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Contains(t, responses[0].Title, "网络")
}

// ==================== 更新事件测试 ====================

func TestIncidentService_UpdateIncident_Success(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "update")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "update")
	require.NoError(t, err)

	workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Original Title", common.IncidentStatusNew, "low")
	_, err = client.Ticket.UpdateOneID(workItem.ID).SetDescription("Original description").Save(ctx)
	require.NoError(t, err)
	testIncident, err := client.Incident.Create().
		SetSeverity("low").
		SetIncidentNumber("INC-UPD-001").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// 测试更新
	newTitle := "Updated Title"
	newPriority := "high"

	response, err := service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
		Title:    &newTitle,
		Priority: &newPriority,
		Version:  0, // 跳过版本检查
	}, testTenant.ID)

	require.NoError(t, err)
	assert.Equal(t, newTitle, response.Title)
	assert.Equal(t, newPriority, response.Priority)
	assert.Equal(t, 2, response.Version) // 版本号自动 +1
}

// ==================== 乐观锁版本控制测试 ====================

func TestIncidentService_UpdateIncident_VersionControl(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "version")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "version")
	require.NoError(t, err)

	workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Version Test", common.IncidentStatusNew, "medium")
	_, err = client.Ticket.UpdateOneID(workItem.ID).SetDescription("Test description").Save(ctx)
	require.NoError(t, err)
	testIncident, err := client.Incident.Create().
		SetSeverity("medium").
		SetIncidentNumber("INC-VER-001").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	t.Run("版本匹配时更新成功", func(t *testing.T) {
		newTitle := "Updated with correct version"
		response, err := service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Title:   &newTitle,
			Version: 1, // 匹配当前版本
			Force:   false,
		}, testTenant.ID)

		require.NoError(t, err)
		assert.Equal(t, newTitle, response.Title)
		assert.Equal(t, 2, response.Version) // 版本号自动 +1
	})

	t.Run("版本不匹配时返回冲突错误", func(t *testing.T) {
		// 当前版本应该是 2
		newTitle := "Should fail"
		_, err := service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Title:   &newTitle,
			Version: 1, // 使用旧版本号
			Force:   false,
		}, testTenant.ID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "版本冲突")

		// 检查是否是 VersionConflictError 类型
		var conflictErr *common.VersionConflictError
		assert.ErrorAs(t, err, &conflictErr)
		assert.Equal(t, testIncident.ID, conflictErr.ResourceID)
		assert.Equal(t, 1, conflictErr.CurrentVersion)
		assert.Equal(t, 2, conflictErr.ServerVersion)
	})

	t.Run("Force=true 忽略版本检查", func(t *testing.T) {
		newTitle := "Force Update"
		response, err := service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Title:   &newTitle,
			Version: 1,    // 旧版本号
			Force:   true, // 强制更新
		}, testTenant.ID)

		require.NoError(t, err)
		assert.Equal(t, newTitle, response.Title)
	})

	t.Run("Version=0 跳过版本检查", func(t *testing.T) {
		newTitle := "No Version Check"
		response, err := service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Title:   &newTitle,
			Version: 0, // 跳过版本检查
			Force:   false,
		}, testTenant.ID)

		require.NoError(t, err)
		assert.Equal(t, newTitle, response.Title)
	})
}

// ==================== 状态转换测试 ====================

func TestIncidentService_UpdateIncident_StatusTransition(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "status")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "status")
	require.NoError(t, err)

	t.Run("有效状态转换 new -> in_progress", func(t *testing.T) {
		workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Status Test 1", common.IncidentStatusNew, "medium")
		testIncident, err := client.Incident.Create().
			SetSeverity("medium").
			SetIncidentNumber("INC-ST-001").
			SetWorkItemID(workItem.ID).
			SetDetectedAt(time.Now()).
			Save(ctx)
		require.NoError(t, err)

		newStatus := "in_progress"
		response, err := service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Status:  &newStatus,
			Version: 0,
		}, testTenant.ID)

		require.NoError(t, err)
		assert.Equal(t, newStatus, response.Status)
	})

	// resolved/closed 不能再通过通用 UpdateIncident 直接设置——必须走 ResolveIncident/
	// CloseIncident 专用动作，确保解决说明、关闭备注和审计事件不可被绕过
	// （见 service/incident_service.go 的 UpdateIncident 守卫）。
	// 专用动作路径本身的行为由 TestIncidentService_DedicatedLifecyclePersistsAuditAndTimestamps 覆盖。
	t.Run("通用更新拒绝直接转到 resolved，必须走专用动作", func(t *testing.T) {
		workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Status Test 2", common.IncidentStatusInProgress, "medium")
		testIncident, err := client.Incident.Create().
			SetSeverity("medium").
			SetIncidentNumber("INC-ST-002").
			SetWorkItemID(workItem.ID).
			SetDetectedAt(time.Now()).
			Save(ctx)
		require.NoError(t, err)

		newStatus := "resolved"
		_, err = service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Status:  &newStatus,
			Version: 0,
		}, testTenant.ID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "dedicated resolve or close action")
	})

	t.Run("通用更新拒绝直接转到 closed，必须走专用动作", func(t *testing.T) {
		resolvedAt := time.Now().Add(-1 * time.Hour)
		workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Status Test 3", common.IncidentStatusResolved, "medium")
		_, err := client.Ticket.UpdateOneID(workItem.ID).SetResolvedAt(resolvedAt).Save(ctx)
		require.NoError(t, err)
		testIncident, err := client.Incident.Create().
			SetSeverity("medium").
			SetIncidentNumber("INC-ST-003").
			SetWorkItemID(workItem.ID).
			SetDetectedAt(time.Now()).
			Save(ctx)
		require.NoError(t, err)

		newStatus := "closed"
		_, err = service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Status:  &newStatus,
			Version: 0,
		}, testTenant.ID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "dedicated resolve or close action")
	})

	t.Run("无效状态转换", func(t *testing.T) {
		workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Status Test 4", common.IncidentStatusNew, "medium")
		testIncident, err := client.Incident.Create().
			SetSeverity("medium").
			SetIncidentNumber("INC-ST-004").
			SetWorkItemID(workItem.ID).
			SetDetectedAt(time.Now()).
			Save(ctx)
		require.NoError(t, err)

		newStatus := "closed" // new 不能直接到 closed
		_, err = service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Status:  &newStatus,
			Version: 0,
		}, testTenant.ID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid status transition")
	})
}

func TestIncidentService_DedicatedLifecyclePersistsAuditAndTimestamps(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "dedicated-lifecycle")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "dedicated-lifecycle")
	require.NoError(t, err)
	workItem := createIncidentTestWorkItem(t, ctx, client, tenant.ID, user.ID, "lifecycle", common.IncidentStatusInProgress, "high")
	entity, err := client.Incident.Create().
		SetSeverity("high").
		SetIncidentNumber("INC-LIFECYCLE-001").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now().Add(-time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, service.ResolveIncident(ctx, entity.ID, user.ID, tenant.ID, "Restarted affected service", "Memory leak"))
	resolved, err := client.Incident.Get(ctx, entity.ID)
	require.NoError(t, err)
	resolvedWorkItem := requireIncidentWorkItem(t, client, resolved)
	assert.Equal(t, "resolved", resolvedWorkItem.Status)
	assert.NotNil(t, resolvedWorkItem.ResolvedAt)
	assert.Equal(t, "Memory leak", resolved.RootCause["rootCause"])
	require.NotEmpty(t, resolved.ResolutionSteps)
	assert.Equal(t, workItem.Version+1, resolvedWorkItem.Version)

	require.NoError(t, service.CloseIncident(ctx, entity.ID, user.ID, tenant.ID, "Observed stable for 30 minutes"))
	closed, err := client.Incident.Get(ctx, entity.ID)
	require.NoError(t, err)
	closedWorkItem := requireIncidentWorkItem(t, client, closed)
	assert.Equal(t, "closed", closedWorkItem.Status)
	assert.NotNil(t, closedWorkItem.ClosedAt)
	assert.Equal(t, resolvedWorkItem.Version+1, closedWorkItem.Version)
	events, err := client.IncidentEvent.Query().Where(incidentevent.IncidentIDEQ(entity.ID)).All(ctx)
	require.NoError(t, err)
	assert.Len(t, events, 2)
}

func TestIncidentService_ResolveRequiresResolutionAndStatusMachineFailsClosed(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "resolution-required")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "resolution-required")
	require.NoError(t, err)
	workItem := createIncidentTestWorkItem(t, ctx, client, tenant.ID, user.ID, "resolution required", "new", "medium")
	entity, err := client.Incident.Create().
		SetSeverity("medium").
		SetIncidentNumber("INC-RESOLUTION-REQUIRED").
		SetWorkItemID(workItem.ID).
		Save(ctx)
	require.NoError(t, err)

	err = service.ResolveIncident(ctx, entity.ID, user.ID, tenant.ID, "  ", "")
	require.ErrorContains(t, err, "resolution is required")
	assert.False(t, isValidIncidentStatusTransition("legacy", "resolved"))
	assert.False(t, isValidIncidentStatusTransition("in_progress", "closed"))
}

// ==================== BPMN 工作流专用写入方法测试 ====================
//
// service/bpmn.IncidentServiceTaskHandler 的测试用的是自己的 fakeIncidentService/
// dbBackedIncidentService（避免 service/bpmn 反向 import service 造成循环依赖），
// 不会真正跑到下面这几个方法本身——这里直接测 *IncidentService 上的真实实现，
// 覆盖 escalate/resolve/close/acknowledge/update/categorize 六个 BPMN 动作从裸 Ent
// 写收回领域服务之后的实际写入语义 + 审计事件。

func newLifecycleIncidentFixture(t *testing.T, client *ent.Client, ctx context.Context, tenantID, userID int, number string) *ent.Incident {
	t.Helper()
	workItem := createIncidentTestWorkItem(t, ctx, client, tenantID, userID, "BPMN workflow lifecycle incident", "new", "medium")
	entity, err := client.Incident.Create().
		SetSeverity("medium").
		SetIncidentNumber(number).
		SetWorkItemID(workItem.ID).
		Save(ctx)
	require.NoError(t, err)
	return entity
}

func TestIncidentService_EscalateIncidentLevel_AutoIncrementsAndAudits(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "wf-escalate")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "wf-escalate")
	require.NoError(t, err)
	entity := newLifecycleIncidentFixture(t, client, ctx, tenant.ID, user.ID, "INC-WF-ESCALATE-1")

	resp, err := service.EscalateIncidentLevel(ctx, entity.ID, tenant.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Incident.EscalationLevel)

	after, err := client.Incident.Get(ctx, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, after.EscalationLevel)
	assert.Equal(t, common.IncidentStatusEscalated, requireIncidentWorkItem(t, client, after).Status)
	assert.False(t, after.EscalatedAt.IsZero())

	events, err := client.IncidentEvent.Query().Where(incidentevent.IncidentIDEQ(entity.ID)).All(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "escalation", events[0].EventType)

	// 显式指定级别时不再自动递增。
	resp2, err := service.EscalateIncidentLevel(ctx, entity.ID, tenant.ID, 3)
	require.NoError(t, err)
	assert.Equal(t, 3, resp2.Incident.EscalationLevel)
}

func TestIncidentService_ResolveIncidentForWorkflow_SetsStatusAndAudits(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "wf-resolve")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "wf-resolve")
	require.NoError(t, err)
	entity := newLifecycleIncidentFixture(t, client, ctx, tenant.ID, user.ID, "INC-WF-RESOLVE-1")

	_, err = service.ResolveIncidentForWorkflow(ctx, entity.ID, tenant.ID, "自动诊断已恢复")
	require.NoError(t, err)

	after, err := client.Incident.Get(ctx, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, common.IncidentStatusResolved, requireIncidentWorkItem(t, client, after).Status)
	assert.NotNil(t, requireIncidentWorkItem(t, client, after).ResolvedAt)

	events, err := client.IncidentEvent.Query().Where(incidentevent.IncidentIDEQ(entity.ID)).All(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "resolution", events[0].EventType)
}

func TestIncidentService_CloseIncidentForWorkflow_SetsStatusAndAudits(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "wf-close")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "wf-close")
	require.NoError(t, err)
	entity := newLifecycleIncidentFixture(t, client, ctx, tenant.ID, user.ID, "INC-WF-CLOSE-1")

	_, err = service.CloseIncidentForWorkflow(ctx, entity.ID, tenant.ID, "流程自动关闭")
	require.NoError(t, err)

	after, err := client.Incident.Get(ctx, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, common.IncidentStatusClosed, requireIncidentWorkItem(t, client, after).Status)
	assert.NotNil(t, requireIncidentWorkItem(t, client, after).ClosedAt)

	events, err := client.IncidentEvent.Query().Where(incidentevent.IncidentIDEQ(entity.ID)).All(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "closure", events[0].EventType)
}

func TestIncidentService_AcknowledgeIncidentForWorkflow_SetsStatusAndAudits(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "wf-ack")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "wf-ack")
	require.NoError(t, err)
	entity := newLifecycleIncidentFixture(t, client, ctx, tenant.ID, user.ID, "INC-WF-ACK-1")

	_, err = service.AcknowledgeIncidentForWorkflow(ctx, entity.ID, tenant.ID)
	require.NoError(t, err)

	after, err := client.Incident.Get(ctx, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, common.IncidentStatusAcknowledged, requireIncidentWorkItem(t, client, after).Status)

	events, err := client.IncidentEvent.Query().Where(incidentevent.IncidentIDEQ(entity.ID)).All(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "acknowledgement", events[0].EventType)
}

func TestIncidentService_UpdateIncidentForWorkflow_PartialUpdateDoesNotTouchOtherFields(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "wf-update")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "wf-update")
	require.NoError(t, err)
	entity := newLifecycleIncidentFixture(t, client, ctx, tenant.ID, user.ID, "INC-WF-UPDATE-1")

	// 只提交 title，其它字段留空——空字符串表示"不修改"，不能被误当成"清空该字段"。
	_, err = service.UpdateIncidentForWorkflow(ctx, entity.ID, tenant.ID, "初步诊断：数据库连接超时", "", "", "", "")
	require.NoError(t, err)

	after, err := client.Incident.Get(ctx, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, "初步诊断：数据库连接超时", requireIncidentWorkItem(t, client, after).Title)
	assert.Equal(t, "new", requireIncidentWorkItem(t, client, after).Status, "update 只提交 title 时不得改状态")
	assert.Equal(t, "medium", requireIncidentWorkItem(t, client, after).Priority, "未提交的字段不应该被清空/改变")
}

func TestIncidentService_CategorizeIncidentForWorkflow_SetsTriagedAndAudits(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "wf-categorize")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "wf-categorize")
	require.NoError(t, err)
	parent, err := client.TicketCategory.Create().SetName("network").SetCode("network").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.TicketCategory.Create().SetName("dns").SetCode("dns").SetTenantID(tenant.ID).SetParentID(parent.ID).Save(ctx)
	require.NoError(t, err)
	entity := newLifecycleIncidentFixture(t, client, ctx, tenant.ID, user.ID, "INC-WF-CATEGORIZE-1")

	_, err = service.CategorizeIncidentForWorkflow(ctx, entity.ID, tenant.ID, "network", "dns")
	require.NoError(t, err)

	after, err := client.Incident.Get(ctx, entity.ID)
	require.NoError(t, err)
	workItem := requireIncidentWorkItem(t, client, after)
	assert.Equal(t, common.IncidentStatusTriaged, workItem.Status)
	categoryEntity, err := workItem.QueryCategory().WithParent().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "network", categoryEntity.Edges.Parent.Name)
	assert.Equal(t, "dns", categoryEntity.Name)

	events, err := client.IncidentEvent.Query().Where(incidentevent.IncidentIDEQ(entity.ID)).All(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "categorization", events[0].EventType)
}

func TestIncidentService_WorkflowRetryPreservesFirstBusinessEffect(t *testing.T) {
	client, incidentService, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "wf-retry")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "wf-retry")
	require.NoError(t, err)

	t.Run("escalation", func(t *testing.T) {
		entity := newLifecycleIncidentFixture(t, client, ctx, tenant.ID, user.ID, "INC-WF-RETRY-ESC")
		_, err := incidentService.EscalateIncidentLevel(ctx, entity.ID, tenant.ID, 0)
		require.NoError(t, err)
		first := client.Incident.GetX(ctx, entity.ID)
		firstWorkItem := requireIncidentWorkItem(t, client, first)

		_, err = incidentService.EscalateIncidentLevel(ctx, entity.ID, tenant.ID, 0)
		require.NoError(t, err)
		after := client.Incident.GetX(ctx, entity.ID)
		assert.Equal(t, 1, after.EscalationLevel)
		assert.Equal(t, firstWorkItem.Version, requireIncidentWorkItem(t, client, after).Version)
		assert.Equal(t, first.EscalatedAt, after.EscalatedAt)
		assert.Equal(t, 1, client.IncidentEvent.Query().Where(incidentevent.IncidentIDEQ(entity.ID)).CountX(ctx))
	})

	t.Run("resolution", func(t *testing.T) {
		entity := newLifecycleIncidentFixture(t, client, ctx, tenant.ID, user.ID, "INC-WF-RETRY-RES")
		_, err := incidentService.ResolveIncidentForWorkflow(ctx, entity.ID, tenant.ID, "restored")
		require.NoError(t, err)
		first := client.Incident.GetX(ctx, entity.ID)
		firstWorkItem := requireIncidentWorkItem(t, client, first)

		_, err = incidentService.ResolveIncidentForWorkflow(ctx, entity.ID, tenant.ID, "restored")
		require.NoError(t, err)
		after := client.Incident.GetX(ctx, entity.ID)
		afterWorkItem := requireIncidentWorkItem(t, client, after)
		assert.Equal(t, firstWorkItem.Version, afterWorkItem.Version)
		assert.Equal(t, firstWorkItem.ResolvedAt, afterWorkItem.ResolvedAt)
		assert.Equal(t, 1, client.IncidentEvent.Query().Where(incidentevent.IncidentIDEQ(entity.ID)).CountX(ctx))
	})
}

func TestIncidentServiceTaskHandler_RetryNoWriteIsIdempotent(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars func(incidentID, assigneeID int) map[string]interface{}
	}{
		{name: "assign", vars: func(id, assigneeID int) map[string]interface{} {
			return map[string]interface{}{"action": "assign_incident", "incident_id": id, "assignee_id": assigneeID}
		}},
		{name: "escalate", vars: func(id, _ int) map[string]interface{} {
			return map[string]interface{}{"action": "escalate_incident", "incident_id": id, "escalation_level": 1}
		}},
		{name: "resolve", vars: func(id, _ int) map[string]interface{} {
			return map[string]interface{}{"action": "resolve_incident", "incident_id": id, "resolution": "restored"}
		}},
		{name: "close", vars: func(id, _ int) map[string]interface{} {
			return map[string]interface{}{"action": "close_incident", "incident_id": id, "feedback": "done"}
		}},
		{name: "acknowledge", vars: func(id, _ int) map[string]interface{} {
			return map[string]interface{}{"action": "acknowledge_incident", "incident_id": id}
		}},
		{name: "update", vars: func(id, _ int) map[string]interface{} {
			return map[string]interface{}{"action": "update_incident", "incident_id": id, "title": "updated once"}
		}},
		{name: "categorize", vars: func(id, _ int) map[string]interface{} {
			return map[string]interface{}{"action": "categorize_incident", "incident_id": id, "category": "network", "subcategory": "dns"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, incidentService, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "handler-retry-"+tc.name)
			require.NoError(t, err)
			user, err := createIncidentTestUser(ctx, client, tenant.ID, "handler-retry-"+tc.name)
			require.NoError(t, err)
			if tc.name == "categorize" {
				parent := client.TicketCategory.Create().SetName("network").SetCode("network").SetTenantID(tenant.ID).SaveX(ctx)
				client.TicketCategory.Create().SetName("dns").SetCode("dns").SetTenantID(tenant.ID).SetParentID(parent.ID).SaveX(ctx)
			}
			entity := newLifecycleIncidentFixture(t, client, ctx, tenant.ID, user.ID, "INC-HANDLER-RETRY-"+tc.name)
			handler := bpmn.NewIncidentServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
			handler.SetIncidentService(incidentService)
			workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
			variables := tc.vars(entity.ID, user.ID)

			first, err := handler.Execute(workflowCtx, nil, variables)
			require.NoError(t, err)
			require.Equal(t, bpmn.CallbackEffectApplied, first.Status)
			retried, err := handler.Execute(workflowCtx, nil, variables)
			require.NoError(t, err)
			require.Equal(t, bpmn.CallbackEffectIdempotent, retried.Status)
		})
	}
}

// TestIncidentService_WorkflowMethods_CrossTenantFailClosed 覆盖六个 BPMN 工作流方法
// 共同的租户边界：跨租户调用必须失败且不产生任何写入。
func TestIncidentService_WorkflowMethods_CrossTenantFailClosed(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "wf-cross")
	require.NoError(t, err)
	other, err := createIncidentTestTenant(ctx, client, "wf-cross-other")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "wf-cross")
	require.NoError(t, err)
	entity := newLifecycleIncidentFixture(t, client, ctx, tenant.ID, user.ID, "INC-WF-CROSS-1")

	_, err = service.EscalateIncidentLevel(ctx, entity.ID, other.ID, 0)
	assert.Error(t, err)
	_, err = service.ResolveIncidentForWorkflow(ctx, entity.ID, other.ID, "x")
	assert.Error(t, err)
	_, err = service.CloseIncidentForWorkflow(ctx, entity.ID, other.ID, "x")
	assert.Error(t, err)
	_, err = service.AcknowledgeIncidentForWorkflow(ctx, entity.ID, other.ID)
	assert.Error(t, err)
	_, err = service.UpdateIncidentForWorkflow(ctx, entity.ID, other.ID, "改过的标题", "", "", "", "")
	assert.Error(t, err)
	_, err = service.CategorizeIncidentForWorkflow(ctx, entity.ID, other.ID, "x", "y")
	assert.Error(t, err)

	after, err := client.Incident.Get(ctx, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, "new", requireIncidentWorkItem(t, client, after).Status, "跨租户写入必须全部失败，状态不能被改动")
	assert.Equal(t, "BPMN workflow lifecycle incident", requireIncidentWorkItem(t, client, after).Title)
}

// ==================== 删除事件测试 ====================

func TestIncidentService_DeleteIncident_Success(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "delete")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "delete")
	require.NoError(t, err)
	workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Delete incident", common.IncidentStatusNew, "medium")

	testIncident, err := client.Incident.Create().
		SetSeverity("medium").
		SetIncidentNumber("INC-DEL-001").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// 创建关联的事件记录
	_, err = client.IncidentEvent.Create().
		SetIncidentID(testIncident.ID).
		SetEventType("creation").
		SetEventName("事件创建").
		SetDescription("Test event created").
		SetStatus("active").
		SetSeverity("info").
		SetTenantID(testTenant.ID).
		SetOccurredAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// 测试删除
	err = service.DeleteIncident(ctx, testIncident.ID, testTenant.ID)
	require.NoError(t, err)

	// 验证已软删除，标准查询不可见但审计数据仍保留
	stored, err := client.Ticket.Get(ctx, workItem.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.DeletedAt)
	_, err = service.GetIncident(ctx, testIncident.ID, testTenant.ID)
	require.ErrorContains(t, err, "incident not found")

	// 审计事件必须保留
	events, err := client.IncidentEvent.Query().
		Where(incidentevent.IncidentIDEQ(testIncident.ID)).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, events, 1)
}

func TestIncidentService_DeleteIncident_NotFound(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "delnotfound")
	require.NoError(t, err)

	// 测试删除不存在的事件
	err = service.DeleteIncident(ctx, 99999, testTenant.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incident not found")
}

// TestIncidentService_DeleteIncident_CascadeTenantIsolation verifies that
// tenant 2 cannot delete an incident belonging to tenant 1 (cross-tenant access denied)
func TestIncidentService_DeleteIncident_CascadeTenantIsolation(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant1, err := createIncidentTestTenant(ctx, client, "cascade1")
	require.NoError(t, err)

	testTenant2, err := createIncidentTestTenant(ctx, client, "cascade2")
	require.NoError(t, err)

	testUser1, err := createIncidentTestUser(ctx, client, testTenant1.ID, "cascade1")
	require.NoError(t, err)
	workItem := createIncidentTestWorkItem(t, ctx, client, testTenant1.ID, testUser1.ID, "Cascade incident", common.IncidentStatusNew, "medium")

	testIncident, err := client.Incident.Create().
		SetSeverity("medium").
		SetIncidentNumber("INC-CASCADE-001").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// Create cascade records (IncidentEvent, IncidentAlert, IncidentMetric)
	_, err = client.IncidentEvent.Create().
		SetIncidentID(testIncident.ID).
		SetEventType("creation").
		SetEventName("事件创建").
		SetDescription("Test event").
		SetStatus("active").
		SetSeverity("info").
		SetTenantID(testTenant1.ID).
		SetOccurredAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.IncidentAlert.Create().
		SetIncidentID(testIncident.ID).
		SetAlertType("warning").
		SetAlertName("Test Alert").
		SetMessage("Test alert message").
		SetStatus("triggered").
		SetSeverity("medium").
		SetTenantID(testTenant1.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.IncidentMetric.Create().
		SetIncidentID(testIncident.ID).
		SetMetricType("test").
		SetMetricName("test_metric").
		SetMetricValue(100.0).
		SetTenantID(testTenant1.ID).
		Save(ctx)
	require.NoError(t, err)

	// Tenant 2 tries to delete Tenant 1's incident - should fail with cross-tenant error
	err = service.DeleteIncident(ctx, testIncident.ID, testTenant2.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cross-tenant access denied", "Expected cross-tenant access denied error")

	// Verify incident still exists (not deleted)
	incident, err := client.Incident.Get(ctx, testIncident.ID)
	require.NoError(t, err)
	assert.Equal(t, testTenant1.ID, requireIncidentWorkItem(t, client, incident).TenantID, "Incident should still belong to Tenant 1")

	// Verify cascade records still exist
	events, err := client.IncidentEvent.Query().Where(incidentevent.IncidentIDEQ(testIncident.ID)).All(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, events, "IncidentEvent should not be deleted")

	alerts, err := client.IncidentAlert.Query().Where(incidentalert.IncidentIDEQ(testIncident.ID)).All(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, alerts, "IncidentAlert should not be deleted")

	metrics, err := client.IncidentMetric.Query().Where(incidentmetric.IncidentIDEQ(testIncident.ID)).All(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, metrics, "IncidentMetric should not be deleted")
}

// ==================== 事件活动记录测试 ====================

func TestIncidentService_CreateIncidentEvent_Success(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "event")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "event")
	require.NoError(t, err)
	workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Event incident", common.IncidentStatusNew, "medium")

	testIncident, err := client.Incident.Create().
		SetSeverity("medium").
		SetIncidentNumber("INC-EVT-001").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// 创建事件记录
	response, err := service.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID:  testIncident.ID,
		EventType:   "status_change",
		EventName:   "状态变更",
		Description: "事件状态从 new 变更为 in_progress",
		Status:      "active",
		Severity:    "info",
		Source:      "system",
		Data: map[string]interface{}{
			"old_status": "new",
			"new_status": "in_progress",
		},
	}, testTenant.ID)

	require.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, testIncident.ID, response.IncidentID)
	assert.Equal(t, "status_change", response.EventType)
	assert.Equal(t, "状态变更", response.EventName)
}

// ==================== 事件统计测试 ====================

func TestIncidentService_GetIncidentStats(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "stats")
	require.NoError(t, err)

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "stats")
	require.NoError(t, err)

	// 创建不同状态的事件
	statuses := []struct {
		status   string
		priority string
		severity string
		count    int
	}{
		{"new", "critical", "critical", 2},
		{"in_progress", "high", "high", 3},
		{"resolved", "medium", "medium", 4},
		{"closed", "low", "low", 1},
	}

	for _, s := range statuses {
		for i := 0; i < s.count; i++ {
			workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, fmt.Sprintf("Stats Test %s %d", s.status, i), s.status, s.priority)
			workItemUpdate := client.Ticket.UpdateOneID(workItem.ID).SetDescription("Test description")
			if s.status == "resolved" {
				workItemUpdate.SetResolvedAt(time.Now())
			} else if s.status == "closed" {
				workItemUpdate.SetResolvedAt(time.Now().Add(-time.Hour)).SetClosedAt(time.Now())
			}
			_, err := workItemUpdate.Save(ctx)
			require.NoError(t, err)
			incidentBuilder := client.Incident.Create().
				SetSeverity(s.severity).
				SetIncidentNumber(fmt.Sprintf("INC-STATS-%s-%d", s.status, i)).
				SetWorkItemID(workItem.ID).
				SetDetectedAt(time.Now())
			_, err = incidentBuilder.Save(ctx)
			require.NoError(t, err)
		}
	}

	// 获取统计
	stats, err := service.GetIncidentStats(ctx, testTenant.ID)
	require.NoError(t, err)
	assert.NotNil(t, stats)

	// 验证统计数据
	totalExpected := 2 + 3 + 4 + 1 // 10
	assert.Equal(t, totalExpected, stats.TotalIncidents)

	// open incidents = new + in_progress
	openExpected := 2 + 3 // 5
	assert.Equal(t, openExpected, stats.OpenIncidents)

	// critical incidents
	assert.Equal(t, 2, stats.CriticalIncidents)
}

// ==================== 升级为重大事件测试 ====================

func TestIncidentService_EscalateToMajorIncident_Success(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "major")
	require.NoError(t, err)
	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "major")
	require.NoError(t, err)
	workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "数据库主从切换失败", common.IncidentStatusInProgress, "high")

	inc, err := client.Incident.Create().
		SetSeverity("high").
		SetIncidentNumber("INC-MAJOR-001").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	req := &dto.EscalateMajorIncidentRequest{
		ImpactScope:       "critical",
		BusinessImpact:    "核心交易链路不可用，影响全部线上用户",
		CommunicationPlan: "拉通应急群，每30分钟同步进展",
	}
	err = service.EscalateToMajorIncident(ctx, inc.ID, testUser.ID, testTenant.ID, req)
	require.NoError(t, err)

	updated, err := client.Incident.Get(ctx, inc.ID)
	require.NoError(t, err)
	assert.True(t, updated.IsMajorIncident)
	assert.Equal(t, "critical", updated.Severity)
	assert.Equal(t, 1, updated.EscalationLevel)
	assert.False(t, updated.EscalatedAt.IsZero())
	assert.Equal(t, workItem.Version+1, requireIncidentWorkItem(t, client, updated).Version)

	majorInfo, ok := updated.ImpactAnalysis["majorIncident"].(map[string]interface{})
	require.True(t, ok, "impact_analysis 应包含 majorIncident 评估信息")
	assert.Equal(t, "critical", majorInfo["impactScope"])
	assert.Equal(t, req.BusinessImpact, majorInfo["businessImpact"])

	// 审计事件已记录
	eventCount, err := client.IncidentEvent.Query().
		Where(incidentevent.EventTypeEQ("major_incident_escalation")).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, eventCount)
}

func TestIncidentService_EscalateToMajorIncident_Rejections(t *testing.T) {
	client, service, ctx := setupIncidentTest(t)
	defer client.Close()

	testTenant, err := createIncidentTestTenant(ctx, client, "majorrej")
	require.NoError(t, err)
	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "majorrej")
	require.NoError(t, err)

	req := &dto.EscalateMajorIncidentRequest{
		ImpactScope:    "high",
		BusinessImpact: "影响评估描述足够长度",
	}

	tests := []struct {
		name    string
		status  string
		isMajor bool
		wantErr string
	}{
		{"已是重大事件", "in_progress", true, "already a major incident"},
		{"已解决事件", "resolved", false, "cannot be escalated"},
		{"已关闭事件", "closed", false, "cannot be escalated"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "拒绝场景 "+tt.name, tt.status, "high")
			inc, err := client.Incident.Create().
				SetSeverity("high").
				SetIncidentNumber(fmt.Sprintf("INC-MAJOR-REJ-%d", i)).
				SetWorkItemID(workItem.ID).
				SetIsMajorIncident(tt.isMajor).
				SetDetectedAt(time.Now()).
				Save(ctx)
			require.NoError(t, err)

			err = service.EscalateToMajorIncident(ctx, inc.ID, testUser.ID, testTenant.ID, req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}

	// 跨租户访问必须失败（fail closed）
	otherTenant, err := createIncidentTestTenant(ctx, client, "majorother")
	require.NoError(t, err)
	workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "跨租户事件", common.IncidentStatusInProgress, "high")
	inc, err := client.Incident.Create().
		SetSeverity("high").
		SetIncidentNumber("INC-MAJOR-CROSS").
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)
	err = service.EscalateToMajorIncident(ctx, inc.ID, testUser.ID, otherTenant.ID, req)
	require.Error(t, err)
}
