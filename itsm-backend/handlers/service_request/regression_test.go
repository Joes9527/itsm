package service_request

import (
	"context"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/service_catalog"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestService_Update_ForbiddenForNonOwnerWithoutPermission(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_update_forbidden?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-update-forbidden").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("owner").SetEmail("owner@test.com").SetName("Owner").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	otherUser, err := client.User.Create().
		SetUsername("other").SetEmail("other@test.com").SetName("Other").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-权限", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "", service_catalog.TargetClassServiceRequestItem)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	svc := NewService(srRepo, client, logger)

	created := createServiceRequestFixture(t, client, tenant.ID, requester.ID, catalog.ID, "申请一台云主机-权限", "")

	// otherUser 既不是申请人，也没有配置 service_request:write 权限（一个干净的
	// enttest 库没有种任何 permissions 行）——canManageServiceRequest 必须判 false。
	_, err = svc.Update(ctx, created.ID, tenant.ID, otherUser.ID, "end_user", &ServiceRequest{CostCenter: "CC-HIJACK"})
	require.Error(t, err)
	appErr, ok := common.AsAppError(err)
	require.True(t, ok)
	assert.Equal(t, common.ErrCodeForbidden, appErr.Code)

	err = svc.Delete(ctx, created.ID, tenant.ID, otherUser.ID, "end_user")
	require.Error(t, err)
	appErr, ok = common.AsAppError(err)
	require.True(t, ok)
	assert.Equal(t, common.ErrCodeForbidden, appErr.Code)
}

func TestService_Update_AllowedForNonOwnerWithSuperAdminRole(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_update_super_admin?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-update-admin").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("owner2").SetEmail("owner2@test.com").SetName("Owner2").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	admin, err := client.User.Create().
		SetUsername("admin").SetEmail("admin@test.com").SetName("Admin").
		SetPasswordHash("hash").SetRole("super_admin").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-管理员", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "", service_catalog.TargetClassServiceRequestItem)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	svc := NewService(srRepo, client, logger)

	created := createServiceRequestFixture(t, client, tenant.ID, requester.ID, catalog.ID, "申请一台云主机-管理员", "")

	updated, err := svc.Update(ctx, created.ID, tenant.ID, admin.ID, "super_admin", &ServiceRequest{CostCenter: "CC-ADMIN-EDIT"})
	require.NoError(t, err, "super_admin 即使不是申请人也应该能编辑他人的服务请求")
	assert.Equal(t, "CC-ADMIN-EDIT", updated.CostCenter)

	err = svc.Delete(ctx, created.ID, tenant.ID, admin.ID, "super_admin")
	require.NoError(t, err, "super_admin 即使不是申请人也应该能删除他人的服务请求")
}

// TestService_CrossTenantIsolation_GetUpdateDelete 覆盖场景 5：租户 A 创建的 ServiceRequest，
// 租户 B 不能通过 Get/Update/Delete 读取或修改——即使租户 B 的调用方精确知道租户 A 那条记录的
// ID。仓储层已经有 GetByTicketID 的跨租户隔离测试（repository_impl_test.go），这里补齐
// Service 层 Get/Update/Delete 三个直接暴露给 handler 的入口，理由同 CLAUDE.md 对新增/改动的
// tenant-scoped 查询的强制测试要求。
func TestService_CrossTenantIsolation_GetUpdateDelete(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_cross_tenant_gud?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenantA, err := client.Tenant.Create().SetName("Tenant A").SetCode("sr-cross-a").SetDomain("cross-a.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	tenantB, err := client.Tenant.Create().SetName("Tenant B").SetCode("sr-cross-b").SetDomain("cross-b.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	requesterA, err := client.User.Create().
		SetUsername("cross-requester-a").SetEmail("cross-requester-a@test.com").SetName("Requester A").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenantA.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalogA, err := scService.Create(ctx, "云主机申请-跨租户", "云服务", "desc", 1, tenantA.ID, "enabled", 0, 0, nil, "", "", service_catalog.TargetClassServiceRequestItem)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	svc := NewService(srRepo, client, logger)

	created := createServiceRequestFixture(t, client, tenantA.ID, requesterA.ID, catalogA.ID, "申请一台云主机-跨租户隔离基线", "CC-TENANT-A")

	// 同租户能正常读到——先证明这不是配置错误导致的假阳性。
	sameTenant, err := svc.Get(ctx, created.ID, tenantA.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, sameTenant.ID)

	t.Run("Get", func(t *testing.T) {
		_, err := svc.Get(ctx, created.ID, tenantB.ID)
		require.Error(t, err)
		assert.True(t, ent.IsNotFound(err), "租户 B 不能用租户 A 的 ServiceRequest ID 读取到数据，got: %v", err)
	})

	t.Run("Update", func(t *testing.T) {
		_, err := svc.Update(ctx, created.ID, tenantB.ID, 0, "manager", &ServiceRequest{
			CostCenter: "CC-HIJACKED-BY-TENANT-B",
		})
		require.Error(t, err)
		appErr, ok := common.AsAppError(err)
		require.True(t, ok, "跨租户 Update 必须返回结构化 AppError，got: %v", err)
		assert.Equal(t, common.ErrCodeNotFound, appErr.Code)

		// 确认数据真的没有被改动。
		unchanged, err := svc.Get(ctx, created.ID, tenantA.ID)
		require.NoError(t, err)
		assert.Equal(t, "CC-TENANT-A", unchanged.CostCenter, "跨租户 Update 必须完全不生效")
	})

	t.Run("Delete", func(t *testing.T) {
		err := svc.Delete(ctx, created.ID, tenantB.ID, 0, "manager")
		require.Error(t, err)
		appErr, ok := common.AsAppError(err)
		require.True(t, ok, "跨租户 Delete 必须返回结构化 AppError，got: %v", err)
		assert.Equal(t, common.ErrCodeNotFound, appErr.Code)

		// 确认记录真的没有被软删除。
		stillThere, err := svc.Get(ctx, created.ID, tenantA.ID)
		require.NoError(t, err)
		assert.Equal(t, created.ID, stillThere.ID, "跨租户 Delete 必须完全不生效，记录对租户 A 仍然可见")
	})
}
