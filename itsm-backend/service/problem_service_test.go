package service

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/knownerror"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// ==================== 测试设置辅助函数 ====================
//
// 统一 WorkItem 领域模型迁移（Wave 2 · Problem 域）核实后删除了 ProblemService 里确认
// 死掉的 CreateProblem/GetProblem/ListProblems/UpdateProblem/DeleteProblem/
// GetProblemStats/triggerWorkflowForProblem/GetWorkflowStatus 方法（见 problem_service.go
// 顶部注释的核实证据），本文件同步删除了这些方法对应的测试。唯一保留的方法
// CreateKnownErrorFromProblem 真的在跑（handlers/known_error/handler.go 的
// CreateFromProblem），下面这条测试锁定它的跨租户隔离行为。

func setupProblemTest(t *testing.T) (*ent.Client, *ProblemService, context.Context) {
	client := enttest.Open(t, "sqlite3", testDSN())
	logger := zaptest.NewLogger(t).Sugar()
	service := NewProblemService(client, logger)
	ctx := context.Background()
	return client, service, ctx
}

func createProblemTestTenant(ctx context.Context, client *ent.Client, suffix string) (*ent.Tenant, error) {
	return client.Tenant.Create().
		SetName("Test Tenant " + suffix).
		SetCode("test" + suffix).
		SetDomain("test" + suffix + ".com").
		SetStatus("active").
		Save(ctx)
}

func createProblemTestUser(ctx context.Context, client *ent.Client, tenantID int, suffix string) (*ent.User, error) {
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

func TestProblemService_CreateKnownErrorFromProblemTenantIsolation(t *testing.T) {
	client, service, ctx := setupProblemTest(t)
	defer client.Close()
	tenantA, err := createProblemTestTenant(ctx, client, "kedb-a")
	require.NoError(t, err)
	tenantB, err := createProblemTestTenant(ctx, client, "kedb-b")
	require.NoError(t, err)
	userA, err := createProblemTestUser(ctx, client, tenantA.ID, "kedb-a")
	require.NoError(t, err)
	userB, err := createProblemTestUser(ctx, client, tenantB.ID, "kedb-b")
	require.NoError(t, err)
	p, err := client.Problem.Create().
		SetTitle("Known database issue").
		SetDescription("Repeated database connection exhaustion").
		SetPriority("high").
		SetRootCause("Connection pool leak").
		SetCreatedBy(userA.ID).
		SetTenantID(tenantA.ID).
		Save(ctx)
	require.NoError(t, err)
	service.SetKnownErrorService(NewKnownErrorService(client, service.logger))

	_, err = service.CreateKnownErrorFromProblem(ctx, p.ID, userB.ID, nil)
	require.ErrorContains(t, err, "problem not found")
	count, err := client.KnownError.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count)

	response, err := service.CreateKnownErrorFromProblem(ctx, p.ID, userA.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, tenantA.ID, response.TenantID)
	linked, err := client.KnownError.Query().Where(knownerror.IDEQ(response.ID)).QueryProblem().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, p.ID, linked.ID)
}
