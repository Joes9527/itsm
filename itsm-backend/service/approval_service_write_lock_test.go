package service

import (
	"context"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupWriteLockTest(t *testing.T) (*ent.Client, *ApprovalService, context.Context) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1&_busy_timeout=5000")
	logger := zaptest.NewLogger(t).Sugar()
	svc := NewApprovalService(client, logger)
	ctx := context.Background()
	return client, svc, ctx
}

func lockTenant(t *testing.T, client *ent.Client, ctx context.Context, tenantID int) {
	t.Helper()
	_, err := client.SystemConfig.Create().
		SetKey("legacyApprovalWriteLocked").
		SetValue("true").
		SetValueType("boolean").
		SetCategory("approval").
		SetTenantID(tenantID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)
}

func TestApprovalService_CreateWorkflow_LockedTenantAborts(t *testing.T) {
	client, svc, ctx := setupWriteLockTest(t)
	defer client.Close()

	tenant, err := createApprovalTestTenant(ctx, client, "locked-create")
	require.NoError(t, err)
	lockTenant(t, client, ctx, tenant.ID)

	req := &dto.CreateApprovalWorkflowRequest{
		Name:     "应该被拒绝的工作流",
		IsActive: true,
		Nodes: []dto.ApprovalNodeRequest{
			{Level: 1, Name: "审批", ApproverType: "user", ApproverIDs: []int{1}, ApprovalMode: "any", RejectAction: "end"},
		},
	}
	resp, err := svc.CreateWorkflow(ctx, req, tenant.ID)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.True(t, errors.Is(err, ErrLegacyApprovalWriteLocked))

	count, err := client.ApprovalWorkflow.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count, "锁定状态下不应该真的创建出工作流记录")
}

func TestApprovalService_CreateWorkflow_UnlockedTenantSucceeds(t *testing.T) {
	client, svc, ctx := setupWriteLockTest(t)
	defer client.Close()

	tenant, err := createApprovalTestTenant(ctx, client, "unlocked-create")
	require.NoError(t, err)
	// 不调用 lockTenant——没有对应的 SystemConfig 行，必须视为未锁定（安全默认值）。

	req := &dto.CreateApprovalWorkflowRequest{
		Name:     "正常应该成功的工作流",
		IsActive: true,
		Nodes: []dto.ApprovalNodeRequest{
			{Level: 1, Name: "审批", ApproverType: "user", ApproverIDs: []int{1}, ApprovalMode: "any", RejectAction: "end"},
		},
	}
	resp, err := svc.CreateWorkflow(ctx, req, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestApprovalService_UpdateWorkflow_LockedTenantAborts(t *testing.T) {
	client, svc, ctx := setupWriteLockTest(t)
	defer client.Close()

	tenant, err := createApprovalTestTenant(ctx, client, "locked-update")
	require.NoError(t, err)

	// 先在未锁定状态下创建一条，再锁定，确认锁定只挡"新的写操作"，不影响已存在的数据。
	req := &dto.CreateApprovalWorkflowRequest{
		Name:     "先创建后锁定",
		IsActive: true,
		Nodes: []dto.ApprovalNodeRequest{
			{Level: 1, Name: "审批", ApproverType: "user", ApproverIDs: []int{1}, ApprovalMode: "any", RejectAction: "end"},
		},
	}
	created, err := svc.CreateWorkflow(ctx, req, tenant.ID)
	require.NoError(t, err)

	lockTenant(t, client, ctx, tenant.ID)

	newName := "锁定后不应该改名成功"
	updateReq := &dto.UpdateApprovalWorkflowRequest{Name: &newName}
	resp, err := svc.UpdateWorkflow(ctx, created.ID, updateReq, tenant.ID)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.True(t, errors.Is(err, ErrLegacyApprovalWriteLocked))

	// 确认真的没改成功。
	stillOldName, err := client.ApprovalWorkflow.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "先创建后锁定", stillOldName.Name)
}

func TestApprovalService_DeleteWorkflow_LockedTenantAborts(t *testing.T) {
	client, svc, ctx := setupWriteLockTest(t)
	defer client.Close()

	tenant, err := createApprovalTestTenant(ctx, client, "locked-delete")
	require.NoError(t, err)

	req := &dto.CreateApprovalWorkflowRequest{
		Name:     "不应该被删除的工作流",
		IsActive: true,
		Nodes: []dto.ApprovalNodeRequest{
			{Level: 1, Name: "审批", ApproverType: "user", ApproverIDs: []int{1}, ApprovalMode: "any", RejectAction: "end"},
		},
	}
	created, err := svc.CreateWorkflow(ctx, req, tenant.ID)
	require.NoError(t, err)

	lockTenant(t, client, ctx, tenant.ID)

	err = svc.DeleteWorkflow(ctx, created.ID, tenant.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrLegacyApprovalWriteLocked))

	stillExists, err := client.ApprovalWorkflow.Query().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, created.ID, stillExists.ID)
}

func TestApprovalService_LockIsPerTenant(t *testing.T) {
	client, svc, ctx := setupWriteLockTest(t)
	defer client.Close()

	lockedTenant, err := createApprovalTestTenant(ctx, client, "lock-scope-locked")
	require.NoError(t, err)
	otherTenant, err := createApprovalTestTenant(ctx, client, "lock-scope-other")
	require.NoError(t, err)
	lockTenant(t, client, ctx, lockedTenant.ID)

	req := &dto.CreateApprovalWorkflowRequest{
		Name:     "别的租户不应该被连带锁定",
		IsActive: true,
		Nodes: []dto.ApprovalNodeRequest{
			{Level: 1, Name: "审批", ApproverType: "user", ApproverIDs: []int{1}, ApprovalMode: "any", RejectAction: "end"},
		},
	}
	resp, err := svc.CreateWorkflow(ctx, req, otherTenant.ID)
	require.NoError(t, err, "锁定是租户级的，不应该影响别的租户")
	require.NotNil(t, resp)
}
