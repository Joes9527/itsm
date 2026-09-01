package service

import (
	"context"
	"testing"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/repository/ticket"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// seedRolePermission 给测试角色授予一条 resource:action 权限，供 hasPermission 相关断言使用。
func seedRolePermission(t *testing.T, client *ent.Client, tenantID int, roleCode, resource, action string) {
	t.Helper()
	ctx := context.Background()
	role, err := client.Role.Create().
		SetCode(roleCode).
		SetName(roleCode).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	perm, err := client.Permission.Create().
		SetCode(resource + ":" + action).
		SetName(resource + ":" + action).
		SetResource(resource).
		SetAction(action).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.RolePermission.Create().
		SetRoleID(role.ID).
		SetPermissionID(perm.ID).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
}

func TestCanAssign_NotExcludedForRequester(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	authorization.InvalidateAllPermissionCaches() // 每个测试独立的权限缓存视图，避免跨测试用例的租户ID复用造成缓存串号
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("t").SetDomain("t.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	seedRolePermission(t, client, tenant.ID, "sd_manager", "ticket", "assign")

	tk := &ticket.Ticket{ID: 1, RequesterID: 42, Status: ticket.StatusOpen}
	actor := ActionActor{Client: client, TenantID: tenant.ID, UserID: 42, Role: "sd_manager"}

	perm := CanAssign(actor, tk)
	require.True(t, perm.Allowed, "assign 不应排除本人，理由: %s", perm.Reason)
}

func TestCanCC_AllowedForRequester(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	authorization.InvalidateAllPermissionCaches() // 每个测试独立的权限缓存视图，避免跨测试用例的租户ID复用造成缓存串号
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("t").SetDomain("t.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester").SetEmail("r@example.com").SetName("Requester").
		SetPasswordHash("x").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	entTk, err := client.Ticket.Create().
		SetTitle("t").SetDescription("d").SetPriority("medium").SetStatus("open").
		SetTicketNumber("TKT-1").SetRequesterID(requester.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	actor := ActionActor{Client: client, TenantID: tenant.ID, UserID: requester.ID, Role: "end_user"}
	perm := CanCC(ctx, actor, entTk.ID)
	require.True(t, perm.Allowed, "申请人本人应该可以抄送，理由: %s", perm.Reason)
}

func TestCanCC_BlockedForUnrelatedUser(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	authorization.InvalidateAllPermissionCaches() // 每个测试独立的权限缓存视图，避免跨测试用例的租户ID复用造成缓存串号
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("t").SetDomain("t.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester").SetEmail("r@example.com").SetName("Requester").
		SetPasswordHash("x").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	bystander, err := client.User.Create().
		SetUsername("bystander").SetEmail("b@example.com").SetName("Bystander").
		SetPasswordHash("x").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	entTk, err := client.Ticket.Create().
		SetTitle("t").SetDescription("d").SetPriority("medium").SetStatus("open").
		SetTicketNumber("TKT-2").SetRequesterID(requester.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	actor := ActionActor{Client: client, TenantID: tenant.ID, UserID: bystander.ID, Role: "end_user"}
	perm := CanCC(ctx, actor, entTk.ID)
	require.False(t, perm.Allowed)
}

func TestCanDelete_BlockedByRunningProcessInstance(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	authorization.InvalidateAllPermissionCaches() // 每个测试独立的权限缓存视图，避免跨测试用例的租户ID复用造成缓存串号
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("t").SetDomain("t.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	seedRolePermission(t, client, tenant.ID, "sysadmin_test", "ticket", "delete")

	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("deploy-1").
		SetDeploymentName("测试部署").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	def, err := client.ProcessDefinition.Create().
		SetKey("ticket_general_flow").
		SetName("通用工单流程").
		SetBpmnXML([]byte("<bpmn/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.ProcessInstance.Create().
		SetProcessInstanceID("proc-1").
		SetBusinessKey("ticket:7").
		SetProcessDefinitionKey("ticket_general_flow").
		SetProcessDefinitionID(def.ID).
		SetStatus("running").
		SetTenantID(tenant.ID).
		SetStartTime(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	tk := &ticket.Ticket{ID: 7, RequesterID: 42, Status: ticket.StatusOpen}
	actor := ActionActor{Client: client, TenantID: tenant.ID, UserID: 99, Role: "sysadmin_test"}

	perm := CanDelete(ctx, actor, tk)
	require.False(t, perm.Allowed)
	require.Equal(t, "工单流程流转中，不可删除", perm.Reason)
}

func TestCanProvision_RequesterExcludedEvenWithPermission(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	authorization.InvalidateAllPermissionCaches() // 每个测试独立的权限缓存视图，避免跨测试用例的租户ID复用造成缓存串号
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("t").SetDomain("t.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	seedRolePermission(t, client, tenant.ID, "l1_support", "service_request", "provision")

	perm := CanProvision(client, tenant.ID, 42, "l1_support", 42)
	require.False(t, perm.Allowed)
	require.Equal(t, "申请人不能交付自己提交的服务请求", perm.Reason)
}

func TestCanProvision_AllowedForFulfillerNotRequester(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	authorization.InvalidateAllPermissionCaches() // 每个测试独立的权限缓存视图，避免跨测试用例的租户ID复用造成缓存串号
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("t").SetDomain("t.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	seedRolePermission(t, client, tenant.ID, "l1_support", "service_request", "provision")

	perm := CanProvision(client, tenant.ID, 99, "l1_support", 42)
	require.True(t, perm.Allowed)
}

func TestCanProvision_DeniedForEndUserEvenNotRequester(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	authorization.InvalidateAllPermissionCaches() // 每个测试独立的权限缓存视图，避免跨测试用例的租户ID复用造成缓存串号
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("t").SetDomain("t.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	// end_user 没有 service_request:provision

	perm := CanProvision(client, tenant.ID, 99, "end_user", 42)
	require.False(t, perm.Allowed)
	require.Equal(t, "无交付权限", perm.Reason)
}
