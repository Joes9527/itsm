package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/handlers/shared/workitemmutation"
)

// 真实路径验证：启用完成门禁后的新 Incident。
//   - 未分类仍可 resolve（恢复服务优先，不加硬门禁）；
//   - close 被明确拒绝，且状态/版本/审计不变；
//   - 补齐完整三级分类后 close 成功，且既有 SLA 时间不被改写；
//   - 分类停用不追溯：历史合法引用仍可完成。
func TestIncidentCompletionCTIGateRealPath(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "cti-close")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "cti-close")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)

	// 先启用完成门禁：之后创建的记录属于“新单”。
	config := NewSystemConfigService(client, zap.NewNop().Sugar())
	activation, err := config.SetCTIGovernance(ctx, tenant.ID, actor.ID, "test", CTIGovernanceUpdate{CompletionEnforced: true})
	require.NoError(t, err)
	cutoff := *activation.Governance.EffectiveFrom

	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "cti-close")
	// 推进到可恢复状态：专业状态机仍由专业命令拥有。
	item := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("in_progress").SaveX(ctx)
	require.False(t, item.CreatedAt.Before(cutoff), "该单必须落在门禁截止之后")
	require.Zero(t, item.CategoryID)

	// resolve 允许（不加硬门禁）。
	item = applyIncidentAction(t, svc, ctx, tenant.ID, actor.ID, inc.ID, item.ID, item.Version, "resolve", "恢复到可用状态", "cti-resolve")
	require.Equal(t, "resolved", item.Status)
	require.False(t, item.ResolvedAt.IsZero())
	resolvedAt := item.ResolvedAt
	versionAfterResolve := item.Version

	// close 被拒绝：分类不完整，状态与版本不变。
	_, err = svc.ApplyIncidentCommand(ctx, incidentCommand(tenant.ID, actor.ID, inc.ID, versionAfterResolve, "close", "确认恢复", "cti-close-blocked"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "classification")
	blocked := client.Ticket.GetX(ctx, item.ID)
	require.Equal(t, "resolved", blocked.Status)
	require.Equal(t, versionAfterResolve, blocked.Version)
	require.Nil(t, blocked.ClosedAt)

	// 补齐完整三级分类（含一个已停用祖先：停用只禁止新选择，不追溯已完成引用）。
	tree := createCompletionTree(t, ctx, client, tenant.ID, "incident")
	disabled := false
	_, err = NewTicketCategoryService(client).UpdateCategory(ctx, tree[0], &UpdateCategoryRequest{IsActive: &disabled}, tenant.ID)
	require.NoError(t, err)
	client.Ticket.UpdateOneID(item.ID).SetCategoryID(tree[2]).ExecX(ctx)

	closed := applyIncidentAction(t, svc, ctx, tenant.ID, actor.ID, inc.ID, item.ID, versionAfterResolve, "close", "确认恢复并归档", "cti-close-allowed")
	require.Equal(t, "closed", closed.Status)
	require.NotNil(t, closed.ClosedAt)
	// 补齐分类不得改写既有 SLA/恢复时间。
	require.False(t, closed.ResolvedAt.IsZero())
	require.True(t, resolvedAt.Equal(closed.ResolvedAt), "补齐分类后 SLA/恢复时间必须保持不变")
}

// 门禁截止之前创建的在途单不被追溯要求。
func TestIncidentCompletionCTIGateIgnoresLegacyRecords(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "cti-legacy")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "cti-legacy")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)

	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "cti-legacy")
	item := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("in_progress").SaveX(ctx)
	// 把这条在途单移到截止时间之前，模拟升级前已存在的记录。
	createdAt := time.Now().UTC().Add(-48 * time.Hour)
	client.Ticket.UpdateOneID(item.ID).SetCreatedAt(createdAt).ExecX(ctx)

	config := NewSystemConfigService(client, zap.NewNop().Sugar())
	_, err = config.SetCTIGovernance(ctx, tenant.ID, actor.ID, "test", CTIGovernanceUpdate{CompletionEnforced: true})
	require.NoError(t, err)

	item = applyIncidentAction(t, svc, ctx, tenant.ID, actor.ID, inc.ID, item.ID, item.Version, "resolve", "历史在途单恢复", "cti-legacy-resolve")
	closed := applyIncidentAction(t, svc, ctx, tenant.ID, actor.ID, inc.ID, item.ID, item.Version, "close", "历史在途单关闭", "cti-legacy-close")
	require.Equal(t, "closed", closed.Status)
}

func applyIncidentAction(t *testing.T, svc *IncidentService, ctx context.Context, tenantID, actorID, incidentID, itemID, version int, action, reason, operationID string) *ent.Ticket {
	t.Helper()
	result, err := svc.ApplyIncidentCommand(ctx, incidentCommand(tenantID, actorID, incidentID, version, action, reason, operationID))
	require.NoError(t, err, "%s", action)
	require.Equal(t, version+1, result.Version)
	return svc.client.Ticket.GetX(ctx, itemID)
}

func incidentCommand(tenantID, actorID, incidentID, version int, action, reason, operationID string) dto.IncidentCommand {
	return dto.IncidentCommand{
		IncidentID: incidentID, Action: action, Reason: reason,
		// 专业原有证据要求保持不变：分类门禁是额外约束，不能替代恢复证据。
		Resolution: map[string]string{"resolve": reason}[action],
		Meta:       workitemmutation.Meta{TenantID: tenantID, ActorID: actorID, ExpectedVersion: version, Source: "http", OperationID: operationID},
	}
}

// createCompletionTree 建立一棵三级分类树，返回 [根, 二级, 三级] 的 ID。
func createCompletionTree(t *testing.T, ctx context.Context, client *ent.Client, tenantID int, prefix string) [3]int {
	t.Helper()
	categories := NewTicketCategoryService(client)
	ids := [3]int{}
	parent := 0
	for index, code := range []string{prefix + "-l1", prefix + "-l2", prefix + "-l3"} {
		record, err := categories.CreateCategory(ctx, &CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenantID})
		require.NoError(t, err)
		ids[index] = record.ID
		parent = record.ID
	}
	return ids
}
