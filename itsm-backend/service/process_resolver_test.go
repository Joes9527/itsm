package service

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newProcessResolverFixture(t *testing.T) (*ent.Client, *ProcessResolver, *ent.Tenant) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:process_resolver_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Process Resolver Tenant").
		SetCode("process-resolver-tenant").
		SetDomain("process-resolver.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	bindingSvc := NewProcessBindingService(client)
	resolver := NewProcessResolver(client, bindingSvc)
	return client, resolver, tenant
}

// TestProcessResolver_ServiceRequestBinding_MatchesTicketBusinessTypeWithSubType 复现并锁定
// config/seed/default.json 里 ProcessBinding 种子数据的 business_type 修复——种子行必须写成
// business_type="ticket" + business_sub_type="service_request"，跟 FindBestBinding 实际查询
// 方式一致，不能直接写 business_type="service_request"（那样的行永远匹配不上）。
func TestProcessResolver_ServiceRequestBinding_MatchesTicketBusinessTypeWithSubType(t *testing.T) {
	client, resolver, tenant := newProcessResolverFixture(t)
	ctx := context.Background()

	// 模拟修正后的种子数据形状
	_, err := client.ProcessBinding.Create().
		SetBusinessType("ticket").
		SetBusinessSubType("service_request").
		SetProcessDefinitionKey("service_request_flow").
		SetIsDefault(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	ticket := &ent.Ticket{Type: "service_request", Priority: "medium", TenantID: tenant.ID}
	key, err := resolver.Resolve(ctx, ticket, "")
	require.NoError(t, err)
	assert.Equal(t, "service_request_flow", key, "修正后的种子数据形状应该能被 resolver 正确匹配到，不再落到 ticket_general_flow 兜底")
}

// TestProcessResolver_ServiceRequestBinding_OldSeedShapeNeverMatches 是一条"锁定当前 bug 形状"
// 的对照测试——用旧的（错误的）business_type="service_request" 写法建绑定，证明它确实匹配不上、
// 会落到兜底默认流程。这条测试不是要修的目标，是用来证明"为什么种子数据必须改成上面那条测试
// 的形状"，两条测试合起来才是完整的回归覆盖。
func TestProcessResolver_ServiceRequestBinding_OldSeedShapeNeverMatches(t *testing.T) {
	client, resolver, tenant := newProcessResolverFixture(t)
	ctx := context.Background()

	// 模拟修正前（当前 config/seed/default.json 里）的错误种子数据形状
	_, err := client.ProcessBinding.Create().
		SetBusinessType("service_request").
		SetProcessDefinitionKey("service_request_flow").
		SetIsDefault(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	// 兜底默认绑定：business_type="ticket"，没有 business_sub_type
	_, err = client.ProcessBinding.Create().
		SetBusinessType("ticket").
		SetProcessDefinitionKey("ticket_general_flow").
		SetIsDefault(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	ticket := &ent.Ticket{Type: "service_request", Priority: "medium", TenantID: tenant.ID}
	key, err := resolver.Resolve(ctx, ticket, "")
	require.NoError(t, err)
	assert.Equal(t, "ticket_general_flow", key, "旧的错误种子数据形状会匹配不上 service_request_flow，落到通用兜底流程")
}

func TestProcessResolver_ResolveWithPriority_ServiceRequestHighPriority_RoutesToUrgentFlow(t *testing.T) {
	client, resolver, tenant := newProcessResolverFixture(t)
	ctx := context.Background()

	_, err := client.ProcessBinding.Create().
		SetBusinessType("ticket").
		SetBusinessSubType("service_request").
		SetProcessDefinitionKey("service_request_flow").
		SetIsDefault(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	highPriorityTicket := &ent.Ticket{Type: "service_request", Priority: "high", TenantID: tenant.ID}
	key, err := resolver.ResolveWithPriority(ctx, highPriorityTicket, "")
	require.NoError(t, err)
	assert.Equal(t, "service_request_urgent_flow", key, "高优先级服务请求应该路由到紧急变体")

	normalPriorityTicket := &ent.Ticket{Type: "service_request", Priority: "medium", TenantID: tenant.ID}
	key, err = resolver.ResolveWithPriority(ctx, normalPriorityTicket, "")
	require.NoError(t, err)
	assert.Equal(t, "service_request_flow", key, "普通优先级服务请求应该保持标准流程，不受这次改动影响")
}

func TestProcessResolver_ResolveWithPriority_TicketGeneralFlow_StillRoutesToUrgentFlow(t *testing.T) {
	client, resolver, tenant := newProcessResolverFixture(t)
	ctx := context.Background()

	_, err := client.ProcessBinding.Create().
		SetBusinessType("ticket").
		SetProcessDefinitionKey("ticket_general_flow").
		SetIsDefault(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	urgentTicket := &ent.Ticket{Type: "generic", Priority: "urgent", TenantID: tenant.ID}
	key, err := resolver.ResolveWithPriority(ctx, urgentTicket, "")
	require.NoError(t, err)
	assert.Equal(t, "ticket_urgent_flow", key, "既有的 ticket_general_flow 优先级路由回归——本任务只新增一条特判，不能影响这一条")
}
