package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	domain "itsm-backend/service"
)

// 通用工单（generic）的完成门禁真实路径：resolve 与直接 close 都被覆盖，
// 拒绝时与状态写入同事务，因此不留下任何变更。
func TestTicketCompletionCTIGateRealPath(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket-cti-completion?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant := createNamedTestTenant(t, ctx, client, "ticket-cti-completion")
	actor := createNamedTestUser(t, ctx, client, tenant.ID, "ticket-cti-completion")
	svc := NewTicketServiceForTest(client, zap.NewNop().Sugar())

	config := domain.NewSystemConfigService(client, zap.NewNop().Sugar())
	activation, err := config.SetCTIGovernance(ctx, tenant.ID, actor.ID, "test", domain.CTIGovernanceUpdate{CompletionEnforced: true})
	require.NoError(t, err)
	cutoff := *activation.Governance.EffectiveFrom
	tree := createGateTree(t, ctx, client, tenant.ID, "ticket")

	// A. 未分类 → resolve 被拒绝；补齐三级后 resolve 与 close 依次成功。
	first, err := svc.SubmitCreation(ctx, &dto.CreateTicketRequest{Title: "CTI resolve", Description: "gate", Priority: "medium", RequesterID: actor.ID}, tenant.ID)
	require.NoError(t, err)
	// 推进到可恢复状态：状态机仍由通用/专业命令拥有，测试不平移规则。
	row := client.Ticket.UpdateOneID(first.ID).SetStatus("open").SaveX(ctx)
	require.False(t, row.CreatedAt.Before(cutoff))
	require.Zero(t, row.CategoryID)

	_, err = svc.ResolveTicket(ctx, first.ID, "已恢复服务", tenant.ID)
	require.ErrorContains(t, err, "classification")
	blocked := client.Ticket.GetX(ctx, first.ID)
	require.Equal(t, "open", blocked.Status)
	require.Equal(t, row.Version, blocked.Version)
	require.True(t, blocked.ResolvedAt.IsZero())

	client.Ticket.UpdateOneID(first.ID).SetCategoryID(tree[2]).ExecX(ctx)
	resolved, err := svc.ResolveTicket(ctx, first.ID, "已恢复服务", tenant.ID)
	require.NoError(t, err)
	require.Equal(t, "resolved", string(resolved.Status))
	require.NotNil(t, resolved.ResolvedAt)
	resolvedAt := *resolved.ResolvedAt

	closed, err := svc.CloseTicket(ctx, first.ID, tenant.ID, "确认关闭")
	require.NoError(t, err)
	require.Equal(t, "closed", string(closed.Status))
	require.NotNil(t, closed.ClosedAt)
	// 完成门禁不得改写既有 SLA/恢复时间。
	require.NotNil(t, closed.ResolvedAt)
	require.True(t, resolvedAt.Equal(*closed.ResolvedAt))

	// B. 已进入 resolved 但分类为空/只有二级 → close 被拒绝且状态不变；补齐三级后成功。
	second, err := svc.SubmitCreation(ctx, &dto.CreateTicketRequest{Title: "CTI close", Description: "gate", Priority: "medium", RequesterID: actor.ID}, tenant.ID)
	require.NoError(t, err)
	beforeClose := client.Ticket.UpdateOneID(second.ID).SetStatus("resolved").SaveX(ctx)

	_, err = svc.CloseTicket(ctx, second.ID, tenant.ID, "关闭")
	require.ErrorContains(t, err, "classification")
	client.Ticket.UpdateOneID(second.ID).SetCategoryID(tree[1]).ExecX(ctx)
	_, err = svc.CloseTicket(ctx, second.ID, tenant.ID, "关闭")
	require.ErrorContains(t, err, "classification")
	unchanged := client.Ticket.GetX(ctx, second.ID)
	require.Equal(t, "resolved", unchanged.Status)
	require.Equal(t, beforeClose.Version, unchanged.Version)
	require.Nil(t, unchanged.ClosedAt)

	client.Ticket.UpdateOneID(second.ID).SetCategoryID(tree[2]).ExecX(ctx)
	closedSecond, err := svc.CloseTicket(ctx, second.ID, tenant.ID, "关闭")
	require.NoError(t, err)
	require.Equal(t, "closed", string(closedSecond.Status))

	// C. 未启用门禁的租户保持既有行为：无需分类即可完成。
	other := createNamedTestTenant(t, ctx, client, "ticket-cti-plain")
	plainRequester := createNamedTestUser(t, ctx, client, other.ID, "ticket-cti-plain")
	plain, err := svc.SubmitCreation(ctx, &dto.CreateTicketRequest{Title: "no governance", Description: "legacy", Priority: "medium", RequesterID: plainRequester.ID}, other.ID)
	require.NoError(t, err)
	client.Ticket.UpdateOneID(plain.ID).SetStatus("open").ExecX(ctx)
	_, err = svc.ResolveTicket(ctx, plain.ID, "已恢复服务", other.ID)
	require.NoError(t, err)
	_, err = svc.CloseTicket(ctx, plain.ID, other.ID, "关闭")
	require.NoError(t, err)
}

// 门禁截止之前创建的在途单不被追溯要求。
func TestTicketCompletionCTIGateIgnoresLegacyRecords(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket-cti-legacy?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant := createNamedTestTenant(t, ctx, client, "ticket-cti-legacy")
	actor := createNamedTestUser(t, ctx, client, tenant.ID, "ticket-cti-legacy")
	svc := NewTicketServiceForTest(client, zap.NewNop().Sugar())

	legacy, err := svc.SubmitCreation(ctx, &dto.CreateTicketRequest{Title: "legacy", Description: "before cutoff", Priority: "medium", RequesterID: actor.ID}, tenant.ID)
	require.NoError(t, err)
	client.Ticket.UpdateOneID(legacy.ID).SetStatus("open").ExecX(ctx)

	config := domain.NewSystemConfigService(client, zap.NewNop().Sugar())
	_, err = config.SetCTIGovernance(ctx, tenant.ID, actor.ID, "test", domain.CTIGovernanceUpdate{CompletionEnforced: true})
	require.NoError(t, err)

	_, err = svc.ResolveTicket(ctx, legacy.ID, "历史在途单恢复", tenant.ID)
	require.NoError(t, err)
	_, err = svc.CloseTicket(ctx, legacy.ID, tenant.ID, "历史在途单关闭")
	require.NoError(t, err)
}

// createGateTree 建立一棵三级分类树，返回 [根, 二级, 三级] 的 ID。
func createGateTree(t *testing.T, ctx context.Context, client *ent.Client, tenantID int, prefix string) [3]int {
	t.Helper()
	categories := domain.NewTicketCategoryService(client)
	ids := [3]int{}
	parent := 0
	for index, code := range []string{prefix + "-l1", prefix + "-l2", prefix + "-l3"} {
		record, err := categories.CreateCategory(ctx, &domain.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenantID})
		require.NoError(t, err)
		ids[index] = record.ID
		parent = record.ID
	}
	return ids
}
