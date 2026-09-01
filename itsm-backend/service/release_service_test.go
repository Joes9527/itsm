package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type failingReleaseProcessTrigger struct {
	ProcessTriggerServiceInterface
	err error
}

type emptyReleaseProcessTrigger struct {
	ProcessTriggerServiceInterface
}

type mismatchedReleaseProcessTrigger struct {
	ProcessTriggerServiceInterface
}

func (*mismatchedReleaseProcessTrigger) TriggerByBusinessTypeWithClient(context.Context, *ent.Client, dto.BusinessType, int, map[string]interface{}, string, int) (*TransactionalProcessStart, error) {
	return newTransactionalProcessStart(&dto.ProcessTriggerResponse{
		ProcessInstanceID: 99,
		BusinessKey:       "release:999",
	}, dto.BusinessTypeRelease, 999, 1, nil), nil
}

func (*emptyReleaseProcessTrigger) TriggerByBusinessTypeWithClient(context.Context, *ent.Client, dto.BusinessType, int, map[string]interface{}, string, int) (*TransactionalProcessStart, error) {
	return &TransactionalProcessStart{}, nil
}

func (f *failingReleaseProcessTrigger) TriggerByBusinessType(context.Context, dto.BusinessType, int, map[string]interface{}, string, int) (*dto.ProcessTriggerResponse, error) {
	return nil, f.err
}

func (f *failingReleaseProcessTrigger) TriggerByBusinessTypeWithClient(context.Context, *ent.Client, dto.BusinessType, int, map[string]interface{}, string, int) (*TransactionalProcessStart, error) {
	return nil, f.err
}

type commitObservingReleaseProcessTrigger struct {
	ProcessTriggerServiceInterface
	client     *ent.Client
	observed   bool
	businessID int
}

func (f *commitObservingReleaseProcessTrigger) TriggerByBusinessTypeWithClient(_ context.Context, _ *ent.Client, businessType dto.BusinessType, businessID int, _ map[string]interface{}, _ string, tenantID int) (*TransactionalProcessStart, error) {
	f.businessID = businessID
	return newTransactionalProcessStart(&dto.ProcessTriggerResponse{ProcessInstanceID: 1, BusinessKey: fmt.Sprintf("%s:%d", businessType, businessID)}, businessType, businessID, tenantID, func(ctx context.Context) {
		f.observed = f.client.Release.GetX(ctx, businessID) != nil
	}), nil
}

func TestReleaseService_CreateReleaseFailsClosedWithoutWorkflowDependencies(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("No workflow").SetCode("no-workflow").SetDomain("no-workflow.test").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetUsername("no-workflow").SetEmail("no-workflow@test").SetName("No workflow").SetPasswordHash("x").SetActive(true).SetTenantID(tenant.ID).SaveX(ctx)

	request := &dto.CreateReleaseRequest{ReleaseNumber: "REL-NO-WORKFLOW", Title: "must not orphan", Type: "minor"}
	result, err := NewReleaseService(client, zaptest.NewLogger(t).Sugar()).CreateRelease(ctx, request, actor.ID, tenant.ID)
	require.Nil(t, result)
	require.ErrorContains(t, err, "workflow")
	require.Zero(t, client.Release.Query().CountX(ctx), "failed creation must not leave an orphan release")
	require.Zero(t, client.ProcessInstance.Query().CountX(ctx), "failed creation must not leave a partial workflow")
}

func TestReleaseService_CreateReleaseRollsBackWhenWorkflowStartFails(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("Failing workflow").SetCode("failing-workflow").SetDomain("failing-workflow.test").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetUsername("failing-workflow").SetEmail("failing-workflow@test").SetName("Failing workflow").SetPasswordHash("x").SetActive(true).SetTenantID(tenant.ID).SaveX(ctx)

	svc := NewReleaseService(client, zaptest.NewLogger(t).Sugar())
	svc.SetProcessEngine(NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar()))
	svc.SetProcessTriggerService(&failingReleaseProcessTrigger{err: errors.New("binding unavailable")})
	result, err := svc.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-START-FAIL", Title: "rollback release", Type: "minor",
	}, actor.ID, tenant.ID)

	require.Nil(t, result)
	require.ErrorContains(t, err, "binding unavailable")
	require.Zero(t, client.Release.Query().CountX(ctx), "trigger failure must roll back the release")
	require.Zero(t, client.ProcessInstance.Query().CountX(ctx), "trigger failure must not leave a process instance")
}

func TestReleaseService_CreateReleaseRollsBackWhenWorkflowReturnsEmptySuccess(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("Empty workflow").SetCode("empty-workflow").SetDomain("empty-workflow.test").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetUsername("empty-workflow").SetEmail("empty-workflow@test").SetName("Empty workflow").SetPasswordHash("x").SetActive(true).SetTenantID(tenant.ID).SaveX(ctx)

	svc := NewReleaseService(client, zaptest.NewLogger(t).Sugar())
	svc.SetProcessEngine(NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar()))
	svc.SetProcessTriggerService(&emptyReleaseProcessTrigger{})
	result, err := svc.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-EMPTY-START", Title: "rollback empty workflow start", Type: "minor",
	}, actor.ID, tenant.ID)

	require.Nil(t, result)
	require.ErrorContains(t, err, "invalid workflow start")
	require.Zero(t, client.Release.Query().CountX(ctx), "empty workflow success must roll back the release")
	require.Zero(t, client.ProcessInstance.Query().CountX(ctx), "empty workflow success must not leave a process instance")
}

func TestReleaseService_CreateReleaseRollsBackWhenWorkflowIdentityMismatches(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("Wrong workflow").SetCode("wrong-workflow").SetDomain("wrong-workflow.test").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetUsername("wrong-workflow").SetEmail("wrong-workflow@test").SetName("Wrong workflow").SetPasswordHash("x").SetActive(true).SetTenantID(tenant.ID).SaveX(ctx)

	svc := NewReleaseService(client, zaptest.NewLogger(t).Sugar())
	svc.SetProcessEngine(NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar()))
	svc.SetProcessTriggerService(&mismatchedReleaseProcessTrigger{})
	result, err := svc.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-WRONG-START", Title: "rollback mismatched workflow start", Type: "minor",
	}, actor.ID, tenant.ID)

	require.Nil(t, result)
	require.ErrorContains(t, err, "invalid workflow start")
	require.Zero(t, client.Release.Query().CountX(ctx))
}

func TestReleaseService_DeliversTransactionalWorkflowCallbacksOnlyAfterCommit(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("Post commit").SetCode("release-post-commit").SetDomain("release-post-commit.test").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetUsername("release-post-commit").SetEmail("release-post-commit@test").SetName("Post commit").SetPasswordHash("x").SetActive(true).SetTenantID(tenant.ID).SaveX(ctx)
	trigger := &commitObservingReleaseProcessTrigger{client: client}
	svc := NewReleaseService(client, zaptest.NewLogger(t).Sugar())
	svc.SetProcessEngine(NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar()))
	svc.SetProcessTriggerService(trigger)

	created, err := svc.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-POST-COMMIT", Title: "deliver after commit", Type: "minor",
	}, actor.ID, tenant.ID)

	require.NoError(t, err)
	require.Equal(t, created.ID, trigger.businessID)
	require.True(t, trigger.observed, "post-commit callback must observe the committed release")
}

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
	requireReleaseCreationWorkflow(t, client, releaseService, testTenant.ID)

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

	// 此测试只验证读取，不经由创建命令制造无关的流程实例。
	release := client.Release.Create().
		SetReleaseNumber("REL-001").SetTitle("测试发布").SetType("minor").
		SetStatus(string(dto.ReleaseStatusDraft)).SetCreatedBy(testUser.ID).SetTenantID(testTenant.ID).
		SaveX(ctx)

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

	// 直接建立聚合，保留本测试对“无权威流程时状态变更失败”的精确前置条件。
	release := client.Release.Create().
		SetReleaseNumber("REL-001").SetTitle("测试发布").SetType("minor").
		SetStatus(string(dto.ReleaseStatusDraft)).SetCreatedBy(testUser.ID).SetTenantID(testTenant.ID).
		SaveX(ctx)

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

	// 统计查询测试直接准备持久化 fixture，不旁路生产创建命令的工作流不变量。
	client.Release.Create().SetReleaseNumber("REL-001").SetTitle("发布1").SetType("minor").
		SetStatus(string(dto.ReleaseStatusDraft)).SetCreatedBy(testUser.ID).SetTenantID(testTenant.ID).SaveX(ctx)
	client.Release.Create().SetReleaseNumber("REL-002").SetTitle("发布2").SetType("patch").
		SetStatus(string(dto.ReleaseStatusDraft)).SetCreatedBy(testUser.ID).SetTenantID(testTenant.ID).SaveX(ctx)

	// 测试获取统计
	stats, err := releaseService.GetReleaseStats(ctx, testTenant.ID)
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Equal(t, 2, stats.Total)
	assert.Equal(t, 2, stats.Draft)
}

func requireReleaseCreationWorkflow(t *testing.T, client *ent.Client, releaseService *ReleaseService, tenantID int) {
	t.Helper()
	ctx := context.Background()
	_, err := NewBPMNTemplateService(client).LoadAndDeployTemplates(ctx, tenantID)
	require.NoError(t, err)
	require.NoError(t, NewProcessBindingService(client).InitDefaultBindings(ctx, tenantID))
	engine := NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar())
	releaseService.SetProcessEngine(engine)
	releaseService.SetProcessTriggerService(NewProcessTriggerService(client, engine))
}
