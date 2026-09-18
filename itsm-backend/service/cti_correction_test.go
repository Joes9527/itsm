package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
)

func ctiCorrectionFixture(t *testing.T) (*ent.Client, context.Context, *TicketCategoryService, int, [3]int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetCode("cti-correction").SetName("correction").SaveX(ctx)
	categories := NewTicketCategoryService(client)
	ids := [3]int{}
	parent := 0
	for index, code := range []string{"l1", "l2", "l3"} {
		record, err := categories.CreateCategory(ctx, &CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenant.ID})
		require.NoError(t, err)
		ids[index] = record.ID
		parent = record.ID
	}
	return client, ctx, categories, tenant.ID, ids
}

func validateTarget(t *testing.T, client *ent.Client, ctx context.Context, tenantID, target int, policy CTICorrectionTargetPolicy) ([]CTINode, error) {
	t.Helper()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback()) }()
	return ValidateCTICorrectionTargetTx(ctx, tx, tenantID, target, policy)
}

func TestRequireCTICorrectionReasonOnlyWhenChanged(t *testing.T) {
	require.NoError(t, RequireCTICorrectionReason("", false), "no change needs no reason")
	require.NoError(t, RequireCTICorrectionReason("  reclassified  ", true))
	err := RequireCTICorrectionReason("   ", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "reason is required")
}

func TestValidateCTICorrectionTargetRespectsPolicy(t *testing.T) {
	client, ctx, categories, tenantID, ids := ctiCorrectionFixture(t)

	// 专业纠正（RequireComplete=false）：二级目标合法，租户可能只维护到二级。
	partial, err := validateTarget(t, client, ctx, tenantID, ids[1], CTICorrectionTargetPolicy{})
	require.NoError(t, err)
	require.Len(t, partial, 2)

	// 目录申请项（RequireComplete=true）：二级目标必须拒绝，三级目标通过。
	_, err = validateTarget(t, client, ctx, tenantID, ids[1], CTICorrectionTargetPolicy{RequireComplete: true})
	require.ErrorContains(t, err, "complete three-level path")
	complete, err := validateTarget(t, client, ctx, tenantID, ids[2], CTICorrectionTargetPolicy{RequireComplete: true})
	require.NoError(t, err)
	require.Len(t, complete, 3)

	// 清除：按策略允许或拒绝。
	cleared, err := validateTarget(t, client, ctx, tenantID, 0, CTICorrectionTargetPolicy{AllowClear: true})
	require.NoError(t, err)
	require.Nil(t, cleared)
	_, err = validateTarget(t, client, ctx, tenantID, 0, CTICorrectionTargetPolicy{})
	require.Error(t, err)

	// 停用节点、跨租户节点、不存在的节点都必须拒绝，且跨租户不泄露存在性。
	disabled := false
	_, err = categories.UpdateCategory(ctx, ids[2], &UpdateCategoryRequest{IsActive: &disabled}, tenantID)
	require.NoError(t, err)
	_, err = validateTarget(t, client, ctx, tenantID, ids[2], CTICorrectionTargetPolicy{})
	require.ErrorContains(t, err, "not a usable active path")

	other := client.Tenant.Create().SetCode("cti-correction-other").SetName("other").SaveX(ctx)
	foreign := client.TicketCategory.Create().SetName("foreign").SetCode("foreign").SetTenantID(other.ID).SaveX(ctx)
	_, err = validateTarget(t, client, ctx, tenantID, foreign.ID, CTICorrectionTargetPolicy{})
	require.ErrorContains(t, err, "not available in this tenant")
	_, err = validateTarget(t, client, ctx, tenantID, 999999, CTICorrectionTargetPolicy{})
	require.ErrorContains(t, err, "not available in this tenant")
}

func TestCTICorrectionEvidenceMetadataShape(t *testing.T) {
	client, ctx, _, tenantID, ids := ctiCorrectionFixture(t)
	path, err := validateTarget(t, client, ctx, tenantID, ids[2], CTICorrectionTargetPolicy{RequireComplete: true})
	require.NoError(t, err)
	before, err := validateTarget(t, client, ctx, tenantID, ids[0], CTICorrectionTargetPolicy{})
	require.NoError(t, err)

	metadata := CTICorrectionEvidence{Before: before, After: path}.Metadata("  misrouted at intake  ")
	require.Equal(t, "misrouted at intake", metadata["classificationReason"])
	after := metadata["classificationAfter"].([]map[string]any)
	require.Len(t, after, 3)
	require.Equal(t, ids[2], after[2]["id"])
	require.Equal(t, "l3", after[2]["name"])
	require.Equal(t, true, after[2]["isActive"])
	// 清除场景没有 after 路径，但仍保留原因与前值。
	cleared := CTICorrectionEvidence{Before: path}.Metadata("cleared by mistake")
	require.Nil(t, cleared["classificationAfter"])
	require.Len(t, cleared["classificationBefore"].([]map[string]any), 3)
}

func TestValidateCTICorrectionTargetRejectsBadInputs(t *testing.T) {
	client, ctx, _, tenantID, ids := ctiCorrectionFixture(t)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback()) }()

	_, err = ValidateCTICorrectionTargetTx(ctx, nil, tenantID, ids[2], CTICorrectionTargetPolicy{})
	require.ErrorContains(t, err, "owning transaction")
	_, err = ValidateCTICorrectionTargetTx(ctx, tx, 0, ids[2], CTICorrectionTargetPolicy{})
	require.ErrorIs(t, err, ErrCTIPathOutsideTenant)
}

// 原因长度上限在服务层校验（覆盖工具/队列等非 HTTP 调用方）；按字符数计，边界包含 500。
func TestRequireCTICorrectionReasonEnforcesServiceLevelLength(t *testing.T) {
	require.NoError(t, RequireCTICorrectionReason(strings.Repeat("a", 500), true))
	require.NoError(t, RequireCTICorrectionReason(strings.Repeat("归", 500), true))
	require.ErrorContains(t, RequireCTICorrectionReason(strings.Repeat("a", 501), true), "at most 500")
	require.ErrorContains(t, RequireCTICorrectionReason(strings.Repeat("归", 501), true), "at most 500")
	// 只有空白等价于未填写
	require.ErrorContains(t, RequireCTICorrectionReason("   \t ", true), "reason is required")
	// 未发生变更时不校验
	require.NoError(t, RequireCTICorrectionReason(strings.Repeat("a", 501), false))
}
