package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	_ "github.com/mattn/go-sqlite3"
)

// 默认审批路径的夹具。
//
// 背景：引擎默认只查部门的负责人（departments.manager_id），而生产库里这条线
// **0/7975 全空**；个人汇报线（users.manager_id）却已填 7045/7872。于是"绝大多数
// 单子解析不到审批人"的根因不是数据没迁完，而是代码走了一条空的数据线。
type approvalChainFixture struct {
	engine    *CustomProcessEngine
	client    *ent.Client
	ctx       context.Context
	tenant    int
	requester *ent.User
}

func newApprovalChainFixture(t *testing.T, dsn string) *approvalChainFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("t-" + dsn).SetStatus("active").SaveX(ctx)

	f := &approvalChainFixture{
		engine: &CustomProcessEngine{client: client, logger: zap.NewNop().Sugar()},
		client: client, ctx: ctx, tenant: tenant.ID,
	}
	return f
}

func (f *approvalChainFixture) user(username string, managerID int, deptID int) *ent.User {
	create := f.client.User.Create().
		SetUsername(username).SetEmail(username + "@example.test").SetName(username).
		SetPasswordHash("hash").SetTenantID(f.tenant).SetActive(true)
	if managerID > 0 {
		create = create.SetManagerID(managerID)
	}
	if deptID > 0 {
		create = create.SetDepartmentID(deptID)
	}
	return create.SaveX(f.ctx)
}

func (f *approvalChainFixture) dept(code string, managerID int) *ent.Department {
	create := f.client.Department.Create().SetName(code).SetCode(code).SetTenantID(f.tenant)
	if managerID > 0 {
		create = create.SetManagerID(managerID)
	}
	return create.SaveX(f.ctx)
}

func (f *approvalChainFixture) resolve() string {
	return f.engine.resolveApprovalAssignee(f.ctx, &ent.ProcessInstance{TenantID: f.tenant}, f.requester)
}

// 核心用例：提单人有自己的上级时，**必须派给他的上级**——
// 这正是"审批不断流"要的结果，也是最初报告里"first-hop personal manager 未被消费"的修复点。
func TestResolveApprovalAssigneePrefersTheRequestersOwnManager(t *testing.T) {
	f := newApprovalChainFixture(t, "file:raa_own?mode=memory&cache=shared&_fk=1")
	deptManager := f.user("D70001", 0, 0)
	ownManager := f.user("D70002", 0, 0)
	dept := f.dept("D-DEPT-1", deptManager.ID)
	f.requester = f.user("D70003", ownManager.ID, dept.ID)

	require.Equal(t, strconv.Itoa(ownManager.ID), f.resolve(),
		"必须先走提单人自己的上级，而不是部门负责人")
}

// 没有个人上级时，保留原有行为：回落到部门负责人。
func TestResolveApprovalAssigneeFallsBackToTheDepartmentManager(t *testing.T) {
	f := newApprovalChainFixture(t, "file:raa_dept?mode=memory&cache=shared&_fk=1")
	deptManager := f.user("D70011", 0, 0)
	dept := f.dept("D-DEPT-2", deptManager.ID)
	f.requester = f.user("D70012", 0, dept.ID) // 没有个人上级

	require.Equal(t, strconv.Itoa(deptManager.ID), f.resolve())
}

// 两条线都解析不到人 → 返回空，由调用方落兜底组（兜底必留痕，已单独实现）。
func TestResolveApprovalAssigneeReturnsEmptyWhenNeitherChainResolves(t *testing.T) {
	f := newApprovalChainFixture(t, "file:raa_none?mode=memory&cache=shared&_fk=1")
	dept := f.dept("D-DEPT-3", 0) // 部门也没负责人
	f.requester = f.user("D70021", 0, dept.ID)

	require.Empty(t, f.resolve())
}

// 自己的上级解析出来是自己时不得派给自己，应继续往下走（部门负责人）。
func TestResolveApprovalAssigneeSkipsWhenTheOwnManagerIsTheRequestersThemselves(t *testing.T) {
	f := newApprovalChainFixture(t, "file:raa_self?mode=memory&cache=shared&_fk=1")
	deptManager := f.user("D70031", 0, 0)
	dept := f.dept("D-DEPT-4", deptManager.ID)
	requester := f.user("D70032", 0, dept.ID)
	// 造自引用：自己的上级是自己
	f.client.User.UpdateOneID(requester.ID).SetManagerID(requester.ID).SaveX(f.ctx)
	f.requester = requester

	require.Equal(t, strconv.Itoa(deptManager.ID), f.resolve(),
		"自己的上级是自己时不得派给自己，应继续走部门负责人")
}

// 部门负责人的上级链非在职时，不得把他派成审批人。
func TestResolveApprovalAssigneeSkipsAnInactiveOwnManager(t *testing.T) {
	f := newApprovalChainFixture(t, "file:raa_inactive?mode=memory&cache=shared&_fk=1")
	deptManager := f.user("D70041", 0, 0)
	ownManager := f.user("D70042", 0, 0)
	f.client.User.UpdateOneID(ownManager.ID).SetActive(false).SaveX(f.ctx)
	dept := f.dept("D-DEPT-5", deptManager.ID)
	f.requester = f.user("D70043", ownManager.ID, dept.ID)

	require.Equal(t, strconv.Itoa(deptManager.ID), f.resolve(),
		"离职的上级不能当审批人，应继续走部门负责人")
}

// 跨租户不得解析到别人租户的人。
func TestResolveApprovalAssigneeIsTenantScoped(t *testing.T) {
	f := newApprovalChainFixture(t, "file:raa_tenant?mode=memory&cache=shared&_fk=1")
	otherTenant := f.client.Tenant.Create().SetName("Other").SetCode("t-other-raa").SetStatus("active").SaveX(f.ctx)
	foreign := f.client.User.Create().SetUsername("D70051").SetEmail("D70051@example.test").
		SetName("外租户").SetPasswordHash("hash").SetTenantID(otherTenant.ID).SetActive(true).SaveX(f.ctx)
	dept := f.dept("D-DEPT-6", 0)
	f.requester = f.user("D70052", foreign.ID, dept.ID)

	require.Empty(t, f.resolve(), "跨租户的上级不得被解析出来")
}
