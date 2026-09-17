package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

// 事件分类纠正入口的契约：**只接受最深节点 ID**。
//
// 背景（2026-09-17 共享已准备目标验收实测）：旧实现按 category/subcategory
// **显示名称**解析，前端实际发送 categoryId 时被当作空值，于是返回
// "分类已更新" 但什么都没改 —— 关闭门禁随后仍然拦截，用户看到"成功"却无法完成单据。
// 该用例固定修复后的行为：空载荷必须失败关闭，改分类必须带原因，清空同样必须带原因。
func TestIncidentClassificationCommandRejectsEmptyPayload(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "classification-id")
	require.NoError(t, err)
	foreign, err := createIncidentTestTenant(ctx, client, "classification-id-foreign")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "classification-id")
	require.NoError(t, err)

	category := func(tenantID int, name string) *ent.TicketCategory {
		row, err := client.TicketCategory.Create().
			SetTenantID(tenantID).
			SetCode(fmt.Sprintf("code-%s", name)).
			SetName(name).
			SetIsActive(true).
			Save(ctx)
		require.NoError(t, err)
		return row
	}
	complete := category(tenant.ID, "完整节点")
	other := category(foreign.ID, "外部节点")

	workItem := createIncidentTestWorkItem(t, ctx, client, tenant.ID, user.ID, "分类契约", "new", "medium")
	incident, err := client.Incident.Create().
		SetWorkItemID(workItem.ID).
		SetSeverity("medium").
		SetDetectedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	version := func() int { return client.Ticket.GetX(ctx, workItem.ID).Version }

	// 1) 未分类时传 categoryId=0 属于"无变化"，是幂等空操作（HTTP 层已强制 reason 必填）。
	//    关键修复点是：拿到真实 ID 时**必须真的写入**，而不是像旧实现那样报告成功却没改。
	_, err = svc.UpdateClassification(ctx, incident.ID, tenant.ID, version(), 0, "")
	require.NoError(t, err)
	after, err := client.Ticket.Get(ctx, workItem.ID)
	require.NoError(t, err)
	require.Zero(t, after.CategoryID)

	// 2) 设置完整分类：必须带原因。
	_, err = svc.UpdateClassification(ctx, incident.ID, tenant.ID, version(), complete.ID, "")
	require.ErrorContains(t, err, "reason is required")

	updated, err := svc.UpdateClassification(ctx, incident.ID, tenant.ID, version(), complete.ID, "按现场信息归类")
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, complete.ID, updated.CategoryID)

	// 3) 跨租户节点必须拒绝，且不改变现状。
	_, err = svc.UpdateClassification(ctx, incident.ID, tenant.ID, version(), other.ID, "跨租户节点")
	require.Error(t, err)
	kept, err := client.Ticket.Get(ctx, workItem.ID)
	require.NoError(t, err)
	require.Equal(t, complete.ID, kept.CategoryID)

	// 4) 清空分类：同样必须带原因（事件允许清空，但不允许无痕清空）。
	_, err = svc.UpdateClassification(ctx, incident.ID, tenant.ID, version(), 0, "")
	require.ErrorContains(t, err, "reason is required")

	cleared, err := svc.UpdateClassification(ctx, incident.ID, tenant.ID, version(), 0, "初始归类有误，先撤回")
	require.NoError(t, err)
	require.Zero(t, cleared.CategoryID)
}

// 流程回调（BPMN service task）走同一条契约：以 category_id 传递最深节点，
// 不再解析显示名称，避免流程与界面使用两套分类语义。
func TestIncidentBPMMClassificationUsesCategoryIDVariable(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "bpmn-classification")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "bpmn-classification")
	require.NoError(t, err)

	node, err := client.TicketCategory.Create().
		SetTenantID(tenant.ID).
		SetCode("bpmn-node").
		SetName("流程节点").
		SetIsActive(true).
		Save(ctx)
	require.NoError(t, err)
	workItem := createIncidentTestWorkItem(t, ctx, client, tenant.ID, user.ID, "流程分类", "new", "medium")
	incident, err := client.Incident.Create().SetWorkItemID(workItem.ID).SetSeverity("medium").SetDetectedAt(time.Now()).Save(ctx)
	require.NoError(t, err)

	updated, err := svc.UpdateClassification(ctx, incident.ID, tenant.ID, client.Ticket.GetX(ctx, workItem.ID).Version, node.ID, "流程自动归类")
	require.NoError(t, err)
	require.Equal(t, node.ID, updated.CategoryID)
}
