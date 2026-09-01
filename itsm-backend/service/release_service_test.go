package service

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestReleaseService_CreateRelease(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	releaseService := NewReleaseService(client, logger)

	ctx := context.Background()

	// 创建测试租户
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	// 创建测试用户
	testUser, err := client.User.Create().
		SetUsername("testuser").
		SetEmail("test@example.com").
		SetName("Test User").
		SetPasswordHash("hashedpassword").
		SetRole("admin").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	tests := []struct {
		name          string
		request       *dto.CreateReleaseRequest
		tenantID      int
		createdBy     int
		expectedError bool
	}{
		{
			name: "成功创建发布",
			request: &dto.CreateReleaseRequest{
				ReleaseNumber: "REL-20260222-001",
				Title:         "测试发布",
				Description:   "这是一个测试发布",
				Type:          "minor",
				Environment:   "staging",
				Severity:      "medium",
			},
			tenantID:      testTenant.ID,
			createdBy:     testUser.ID,
			expectedError: false,
		},
		{
			name: "发布编号为空",
			request: &dto.CreateReleaseRequest{
				ReleaseNumber: "",
				Title:         "测试发布",
			},
			tenantID:      testTenant.ID,
			createdBy:     testUser.ID,
			expectedError: true,
		},
		{
			name: "标题为空",
			request: &dto.CreateReleaseRequest{
				ReleaseNumber: "REL-001",
				Title:         "",
			},
			tenantID:      testTenant.ID,
			createdBy:     testUser.ID,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release, err := releaseService.CreateRelease(ctx, tt.request, tt.createdBy, tt.tenantID)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, release)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, release)
				assert.Equal(t, tt.request.Title, release.Title)
				assert.Equal(t, tt.request.ReleaseNumber, release.ReleaseNumber)
			}
		})
	}
}

func TestReleaseService_GetReleaseByID(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	releaseService := NewReleaseService(client, logger)

	ctx := context.Background()

	// 创建测试数据
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	testUser, err := client.User.Create().
		SetUsername("testuser").
		SetEmail("test@example.com").
		SetName("Test User").
		SetPasswordHash("hashedpassword").
		SetRole("admin").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 创建测试发布
	release, err := releaseService.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-001",
		Title:         "测试发布",
		Type:          "minor",
	}, testUser.ID, testTenant.ID)
	require.NoError(t, err)

	// 测试获取发布
	t.Run("获取存在的发布", func(t *testing.T) {
		result, err := releaseService.GetReleaseByID(ctx, release.ID, testTenant.ID)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "REL-001", result.ReleaseNumber)
	})

	// 测试获取不存在的发布
	t.Run("获取不存在的发布", func(t *testing.T) {
		result, err := releaseService.GetReleaseByID(ctx, 9999, testTenant.ID)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})
}

func TestReleaseService_UpdateReleaseStatusFailsClosedWithoutAuthoritativeWorkflow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	releaseService := NewReleaseService(client, logger)

	ctx := context.Background()

	// 创建测试数据
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	testUser, err := client.User.Create().
		SetUsername("testuser").
		SetEmail("test@example.com").
		SetName("Test User").
		SetPasswordHash("hashedpassword").
		SetRole("admin").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 创建测试发布
	release, err := releaseService.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-001",
		Title:         "测试发布",
		Type:          "minor",
	}, testUser.ID, testTenant.ID)
	require.NoError(t, err)

	_, err = releaseService.UpdateReleaseStatus(ctx, release.ID, testTenant.ID, testUser.ID, "scheduled")
	require.ErrorContains(t, err, "workflow engine is unavailable")
	require.Equal(t, "draft", client.Release.GetX(ctx, release.ID).Status)

	engine := NewCustomProcessEngine(client, logger)
	releaseService.SetProcessEngine(engine)
	_, err = releaseService.UpdateReleaseStatus(ctx, release.ID, testTenant.ID, testUser.ID, "scheduled")
	require.ErrorContains(t, err, "workflow instance not found")
	require.Equal(t, "draft", client.Release.GetX(ctx, release.ID).Status)

	_, err = NewBPMNTemplateService(client).LoadAndDeployTemplates(ctx, testTenant.ID)
	require.NoError(t, err)
	workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, testTenant.ID)
	workflowCtx = WithTrustedBPMNTenantContext(workflowCtx, testTenant.ID)
	_, err = engine.StartProcess(
		workflowCtx, "release_approval_flow", "release:"+strconv.Itoa(release.ID),
		string(dto.BusinessTypeRelease), release.ID, map[string]interface{}{"triggered_by": strconv.Itoa(testUser.ID)},
	)
	require.NoError(t, err)
	_, err = releaseService.UpdateReleaseStatus(ctx, release.ID, testTenant.ID, testUser.ID, "scheduled")
	require.ErrorContains(t, err, "workflow task Activity_Schedule not found")
	require.Equal(t, "draft", client.Release.GetX(ctx, release.ID).Status)
}

func TestReleaseService_ApplyReleaseWorkflowCallbackOwnsLifecycleMutation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().
		SetName("Release callback tenant").SetCode("release-callback").
		SetDomain("release-callback.example.com").SetStatus("active").SaveX(ctx)
	user := client.User.Create().
		SetUsername("release-callback-user").SetEmail("release-callback@example.com").
		SetName("Release callback user").SetPasswordHash("x").SetActive(true).
		SetTenantID(tenant.ID).SaveX(ctx)
	entity := client.Release.Create().
		SetReleaseNumber("REL-CALLBACK-1").SetTitle("Callback authority").
		SetStatus(string(dto.ReleaseStatusDraft)).SetCreatedBy(user.ID).SetTenantID(tenant.ID).
		SaveX(ctx)
	svc := NewReleaseService(client, zaptest.NewLogger(t).Sugar())

	first, err := svc.ApplyReleaseWorkflowCallback(ctx, bpmn.ReleaseWorkflowCommand{
		ReleaseID: entity.ID, TenantID: tenant.ID,
		Action: bpmn.ReleaseWorkflowActionTechReview, Comment: "通过",
	})
	require.NoError(t, err)
	assert.True(t, first.Changed)

	retry, err := svc.ApplyReleaseWorkflowCallback(ctx, bpmn.ReleaseWorkflowCommand{
		ReleaseID: entity.ID, TenantID: tenant.ID,
		Action: bpmn.ReleaseWorkflowActionTechReview, Comment: "通过",
	})
	require.NoError(t, err)
	assert.False(t, retry.Changed)
	assert.Equal(t, 1, strings.Count(client.Release.GetX(ctx, entity.ID).ReleaseNotes, "[技术评审] 通过"))

	_, err = svc.ApplyReleaseWorkflowCallback(ctx, bpmn.ReleaseWorkflowCommand{
		ReleaseID: entity.ID, TenantID: tenant.ID,
		Action: bpmn.ReleaseWorkflowActionStatus, TargetStatus: string(dto.ReleaseStatusCompleted),
	})
	require.ErrorContains(t, err, "非法的发布状态转换")
	assert.Equal(t, string(dto.ReleaseStatusDraft), client.Release.GetX(ctx, entity.ID).Status)
}

func TestReleaseService_ApplyReleaseWorkflowCallbackConcurrentTerminalTransitionsUseCAS(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().
		SetName("Release CAS tenant").SetCode("release-cas").
		SetDomain("release-cas.example.com").SetStatus("active").SaveX(ctx)
	user := client.User.Create().
		SetUsername("release-cas-user").SetEmail("release-cas@example.com").
		SetName("Release CAS user").SetPasswordHash("x").SetActive(true).
		SetTenantID(tenant.ID).SaveX(ctx)
	entity := client.Release.Create().
		SetReleaseNumber("REL-CAS-1").SetTitle("CAS authority").
		SetStatus(string(dto.ReleaseStatusInProgress)).SetCreatedBy(user.ID).SetTenantID(tenant.ID).
		SaveX(ctx)
	svc := NewReleaseService(client, zaptest.NewLogger(t).Sugar())

	statuses := []string{string(dto.ReleaseStatusCompleted), string(dto.ReleaseStatusFailed)}
	errs := make(chan error, len(statuses))
	var start sync.WaitGroup
	start.Add(1)
	for _, status := range statuses {
		status := status
		go func() {
			start.Wait()
			_, err := svc.ApplyReleaseWorkflowCallback(ctx, bpmn.ReleaseWorkflowCommand{
				ReleaseID: entity.ID, TenantID: tenant.ID,
				Action: bpmn.ReleaseWorkflowActionStatus, TargetStatus: status,
			})
			errs <- err
		}()
	}
	start.Done()

	successes := 0
	for range statuses {
		if err := <-errs; err == nil {
			successes++
		}
	}
	assert.Equal(t, 1, successes, "competing terminal transitions must not both commit")
	assert.Contains(t, statuses, client.Release.GetX(ctx, entity.ID).Status)
}

func TestReleaseService_GetReleaseStats(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	releaseService := NewReleaseService(client, logger)

	ctx := context.Background()

	// 创建测试数据
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	testUser, err := client.User.Create().
		SetUsername("testuser").
		SetEmail("test@example.com").
		SetName("Test User").
		SetPasswordHash("hashedpassword").
		SetRole("admin").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 创建多个测试发布
	_, _ = releaseService.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-001",
		Title:         "发布1",
		Type:          "minor",
	}, testUser.ID, testTenant.ID)

	_, _ = releaseService.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-002",
		Title:         "发布2",
		Type:          "patch",
	}, testUser.ID, testTenant.ID)

	// 测试获取统计
	stats, err := releaseService.GetReleaseStats(ctx, testTenant.ID)
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Equal(t, 2, stats.Total)
	assert.Equal(t, 2, stats.Draft)
}
