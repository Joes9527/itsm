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

// 一个节点可以同时声明多个固定范围（部门/团队/项目/临时团队）。
// 当前实现只取 sources[0]，其余被**静默忽略**——第一个来源解析不出人时，
// 明明还有第二个能解析出人的来源，任务却会落进兜底甚至派错人。
//
// 本用例构造：部门没有负责人，团队有 leader → 必须解析到 team leader。
func TestResolveFixedScopeAssigneeTriesEveryDeclaredScope(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:fixscope_multi?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	tenant := client.Tenant.Create().SetName("T").SetCode("t-fixscope").SetStatus("active").SaveX(ctx)

	// 部门：故意不设负责人
	dept := client.Department.Create().
		SetName("无负责人部门").SetCode("D-NOMGR").SetTenantID(tenant.ID).SaveX(ctx)

	// 团队：有 leader
	leader := client.User.Create().
		SetUsername("D60001").SetEmail("D60001@example.test").SetName("服务台组长").
		SetPasswordHash("hash").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	team := client.Team.Create().
		SetName("服务台-L1").SetCode("T-L1").SetTenantID(tenant.ID).SetManagerID(leader.ID).SaveX(ctx)

	engine := &CustomProcessEngine{client: client, logger: zap.NewNop().Sugar()}
	instance := &ent.ProcessInstance{TenantID: tenant.ID}
	task := &BPMNUserTask{ID: "Task_Assign", AssigneeDeptId: dept.ID, AssigneeTeamId: team.ID}

	got := engine.resolveFixedScopeAssignee(ctx, instance, nil, task)
	require.Equal(t, strconv.Itoa(leader.ID), got,
		"第一个声明的范围解析不出人时，必须继续尝试其余声明的范围")
}

// 所有来源都解析不出人时返回空，由调用方决定是保持未指派还是落兜底。
func TestResolveFixedScopeAssigneeReturnsEmptyWhenNoScopeResolves(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:fixscope_none?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	tenant := client.Tenant.Create().SetName("T").SetCode("t-fixscope2").SetStatus("active").SaveX(ctx)
	dept := client.Department.Create().
		SetName("无负责人部门").SetCode("D-NOMGR").SetTenantID(tenant.ID).SaveX(ctx)

	engine := &CustomProcessEngine{client: client, logger: zap.NewNop().Sugar()}
	instance := &ent.ProcessInstance{TenantID: tenant.ID}
	task := &BPMNUserTask{ID: "Task_Assign", AssigneeDeptId: dept.ID}

	require.Empty(t, engine.resolveFixedScopeAssignee(ctx, instance, nil, task))
}

// 申请人本人不能被解析成自己的审批人：命中本人时继续尝试其余来源，全部命中本人才返回空。
func TestResolveFixedScopeAssigneeSkipsTheRequesterThemselves(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:fixscope_self?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	tenant := client.Tenant.Create().SetName("T").SetCode("t-fixscope3").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().
		SetUsername("D60010").SetEmail("D60010@example.test").SetName("申请人").
		SetPasswordHash("hash").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	dept := client.Department.Create().
		SetName("部门").SetCode("D-SELF").SetTenantID(tenant.ID).SetManagerID(requester.ID).SaveX(ctx)

	engine := &CustomProcessEngine{client: client, logger: zap.NewNop().Sugar()}
	instance := &ent.ProcessInstance{TenantID: tenant.ID}
	task := &BPMNUserTask{ID: "Task_Assign", AssigneeDeptId: dept.ID}

	require.Empty(t, engine.resolveFixedScopeAssignee(ctx, instance, requester, task),
		"解析出的负责人是申请人本人时不得派给自己")
}
