package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	executionfixture "itsm-backend/tests/fixtures/execution"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/incidentalert"
	"itsm-backend/ent/incidentevent"
	"itsm-backend/ent/incidentmetric"
	"itsm-backend/handlers/shared/workitemmutation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// ==================== 测试设置辅助函数 ====================

func setupIncidentTest(t *testing.T) (*ent.Client, *IncidentService, context.Context) {
	client := enttest.Open(t, "sqlite3", testDSN())
	logger := zaptest.NewLogger(t).Sugar()
	service := NewIncidentService(client, logger, executionfixture.Standard())
	service.RuleEngine().SetActorDirectory(client)
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
		SetRecordClass("incident").
		SetTicketNumber(fmt.Sprintf("TKT-TEST-%d-%d", tenantID, time.Now().UnixNano())).
		SetRequesterID(requesterID).SetTenantID(tenantID).Save(ctx)
	require.NoError(t, err)
	return workItem
}

// ==================== 创建事件测试 ====================

// ==================== WorkItem 迁移测试（Wave 2） ====================

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
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Ticket.UpdateOneID(workItem.ID).SetDescription("desc").Save(ctx)
	require.NoError(t, err)

	response, err := assignIncidentForTest(t, incidentService, ctx, incidentEntity.ID, assignee.ID, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, response.AssigneeID)
	assert.Equal(t, assignee.ID, *response.AssigneeID)
	assert.Equal(t, workItem.Version+1, response.Version)

	otherTenant, err := createIncidentTestTenant(ctx, client, "assign-other")
	require.NoError(t, err)
	otherUser, err := createIncidentTestUser(ctx, client, otherTenant.ID, "assign-other")
	require.NoError(t, err)
	_, err = assignIncidentForTest(t, incidentService, ctx, incidentEntity.ID, otherUser.ID, tenant.ID)
	require.ErrorContains(t, err, "assignee not found or inactive")

	inactive, err := createIncidentTestUser(ctx, client, tenant.ID, "assign-inactive")
	require.NoError(t, err)
	_, err = inactive.Update().SetActive(false).Save(ctx)
	require.NoError(t, err)
	_, err = assignIncidentForTest(t, incidentService, ctx, incidentEntity.ID, inactive.ID, tenant.ID)
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
				SetWorkItemID(workItem.ID).
				Save(ctx)
			require.NoError(t, err)

			_, err = assignIncidentForTest(t, incidentService, ctx, incidentEntity.ID, assignee.ID, tenant.ID)
			require.ErrorContains(t, err, "cannot be reassigned")

			persisted, err := client.Ticket.Get(ctx, workItem.ID)
			require.NoError(t, err)
			require.Zero(t, persisted.AssigneeID)
		})
	}
}

func TestAssignIncidentRejectsStaleSnapshot(t *testing.T) {
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
				require.ErrorContains(t, err, "cannot be reassigned")
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
				SetWorkItemID(workItem.ID).
				Save(ctx)
			require.NoError(t, err)

			// The caller observed this version before another writer changed it.
			// Actual overlapping transactions are verified by PostgreSQL tests.
			reporter.Update().SetRole("super_admin").ExecX(ctx)
			require.NoError(t, testCase.mutateRace(ctx, racer, incidentEntity.ID))
			incidentService := NewIncidentService(client, zaptest.NewLogger(t).Sugar(), executionfixture.Standard())
			_, err = incidentService.ApplyIncidentCommand(ctx, dto.IncidentCommand{
				IncidentID: incidentEntity.ID, Action: "assign", AssigneeID: assignee.ID,
				Meta: workitemmutation.Meta{
					TenantID: tenant.ID, ActorID: reporter.ID,
					ExpectedVersion: workItem.Version, Source: "http", OperationID: "stale-assign",
				},
			})
			require.Error(t, err)
			testCase.assertErr(t, err)

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
		SetWorkItemID(workItem.ID).
		Save(ctx)
	require.NoError(t, err)

	incidentSelects = 0
	incidentService := NewIncidentService(client, zaptest.NewLogger(t).Sugar(), executionfixture.Standard())
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
		SetWorkItemID(databaseWorkItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	networkWorkItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "网络延迟问题", common.IncidentStatusNew, "medium")
	_, err = client.Ticket.UpdateOneID(networkWorkItem.ID).SetDescription("用户反馈网络响应缓慢").Save(ctx)
	require.NoError(t, err)
	_, err = client.Incident.Create().
		SetSeverity("medium").
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
		Version:  1, // expected version
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

	t.Run("Force=true 被拒绝", func(t *testing.T) {
		newTitle := "Force Update"
		_, err := service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Title:   &newTitle,
			Version: 1,    // 旧版本号
			Force:   true, // 强制更新
		}, testTenant.ID)

		require.Error(t, err)
	})

	t.Run("Version=0 被拒绝", func(t *testing.T) {
		newTitle := "No Version Check"
		_, err := service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Title:   &newTitle,
			Version: 0, // 跳过版本检查
			Force:   false,
		}, testTenant.ID)

		require.Error(t, err)
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
			SetWorkItemID(workItem.ID).
			SetDetectedAt(time.Now()).
			Save(ctx)
		require.NoError(t, err)

		newStatus := "in_progress"
		_, err = service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Status:  &newStatus,
			Version: 1,
		}, testTenant.ID)

		require.ErrorContains(t, err, "Incident command")
	})

	// resolved/closed 不能再通过通用 UpdateIncident 直接设置——必须走 ResolveIncident/
	// CloseIncident 专用动作，确保解决说明、关闭备注和审计事件不可被绕过
	// （见 service/incident_service.go 的 UpdateIncident 守卫）。
	// 专用动作路径本身的行为由 TestIncidentService_DedicatedLifecyclePersistsAuditAndTimestamps 覆盖。
	t.Run("通用更新拒绝直接转到 resolved，必须走专用动作", func(t *testing.T) {
		workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Status Test 2", common.IncidentStatusInProgress, "medium")
		testIncident, err := client.Incident.Create().
			SetSeverity("medium").
			SetWorkItemID(workItem.ID).
			SetDetectedAt(time.Now()).
			Save(ctx)
		require.NoError(t, err)

		newStatus := "resolved"
		_, err = service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Status:  &newStatus,
			Version: 1,
		}, testTenant.ID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "Incident command")
	})

	t.Run("通用更新拒绝直接转到 closed，必须走专用动作", func(t *testing.T) {
		resolvedAt := time.Now().Add(-1 * time.Hour)
		workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Status Test 3", common.IncidentStatusResolved, "medium")
		_, err := client.Ticket.UpdateOneID(workItem.ID).SetResolvedAt(resolvedAt).Save(ctx)
		require.NoError(t, err)
		testIncident, err := client.Incident.Create().
			SetSeverity("medium").
			SetWorkItemID(workItem.ID).
			SetDetectedAt(time.Now()).
			Save(ctx)
		require.NoError(t, err)

		newStatus := "closed"
		_, err = service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Status:  &newStatus,
			Version: 1,
		}, testTenant.ID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "Incident command")
	})

	t.Run("无效状态转换", func(t *testing.T) {
		workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "Status Test 4", common.IncidentStatusNew, "medium")
		testIncident, err := client.Incident.Create().
			SetSeverity("medium").
			SetWorkItemID(workItem.ID).
			SetDetectedAt(time.Now()).
			Save(ctx)
		require.NoError(t, err)

		newStatus := "closed" // new 不能直接到 closed
		_, err = service.UpdateIncident(ctx, testIncident.ID, &dto.UpdateIncidentRequest{
			Status:  &newStatus,
			Version: 1,
		}, testTenant.ID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "Incident command")
	})
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
		SetWorkItemID(workItem.ID).
		Save(ctx)
	require.NoError(t, err)
	return entity
}

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

	_, err = service.ApplyIncidentCommand(ctx, dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: other.ID, ActorID: user.ID, ExpectedVersion: 1, OperationID: "cross", Source: "workflow"}, IncidentID: entity.ID, Action: "escalate"})
	assert.Error(t, err)

	_, err = service.UpdateIncident(ctx, entity.ID, &dto.UpdateIncidentRequest{Version: 1}, other.ID)
	assert.Error(t, err)
	_, err = service.UpdateClassification(ctx, entity.ID, other.ID, 1, "x", "y")
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

	deletionRole := client.Role.Create().SetTenantID(testTenant.ID).SetCode("agent").SetName("delete fixture").SetIsActive(true).SaveX(ctx)
	for _, verb := range []string{"read", "delete"} {
		perm := client.Permission.Create().SetTenantID(testTenant.ID).SetCode("deletion_" + verb).SetName(verb).SetResource("incident").SetAction(verb).SaveX(ctx)
		client.RolePermission.Create().SetTenantID(testTenant.ID).SetRoleID(deletionRole.ID).SetPermissionID(perm.ID).ExecX(ctx)
	}

	// 测试删除
	err = service.DeleteIncident(ctx, testIncident.ID, workitemmutation.Meta{TenantID: testTenant.ID, ActorID: testUser.ID})
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

	testUser, err := createIncidentTestUser(ctx, client, testTenant.ID, "delnotfound")
	require.NoError(t, err)
	// 测试删除不存在的事件
	err = service.DeleteIncident(ctx, 99999, workitemmutation.Meta{TenantID: testTenant.ID, ActorID: testUser.ID})
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err))
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
	err = service.DeleteIncident(ctx, testIncident.ID, workitemmutation.Meta{TenantID: testTenant2.ID, ActorID: testUser1.ID})
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err))

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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workItem := createIncidentTestWorkItem(t, ctx, client, testTenant.ID, testUser.ID, "拒绝场景 "+tt.name, tt.status, "high")
			inc, err := client.Incident.Create().
				SetSeverity("high").
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
		SetWorkItemID(workItem.ID).
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)
	err = service.EscalateToMajorIncident(ctx, inc.ID, testUser.ID, otherTenant.ID, req)
	require.Error(t, err)
}
