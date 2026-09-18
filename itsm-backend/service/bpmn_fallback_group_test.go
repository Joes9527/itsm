package service

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	_ "github.com/mattn/go-sqlite3"
)

func fallbackFixture(t *testing.T, dsn string) (*CustomProcessEngine, *ent.Client, context.Context, int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("t-" + dsn).SetStatus("active").SaveX(ctx)
	return &CustomProcessEngine{client: client, logger: zap.NewNop().Sugar()}, client, ctx, tenant.ID
}

// 没配置时回落默认组——绝不能返回空串，否则审批任务无人可领。
func TestApprovalFallbackGroupDefaultsWhenUnconfigured(t *testing.T) {
	engine, _, ctx, tenantID := fallbackFixture(t, "file:fb_default?mode=memory&cache=shared&_fk=1")

	require.Equal(t, approvalFallbackCandidateGroup, engine.approvalFallbackGroup(ctx, tenantID))
}

// 配了就用配的：这是"可配置"的实证，配置面是既有的 systemconfigs 表。
func TestApprovalFallbackGroupHonoursTenantConfiguration(t *testing.T) {
	engine, client, ctx, tenantID := fallbackFixture(t, "file:fb_custom?mode=memory&cache=shared&_fk=1")
	client.SystemConfig.Create().
		SetKey(ApprovalFallbackGroupConfigKey).SetValue("分公司审批组").
		SetValueType("string").SetCategory("bpmn").SetTenantID(tenantID).SaveX(ctx)

	require.Equal(t, "分公司审批组", engine.approvalFallbackGroup(ctx, tenantID))
}

// 配置成空白等于没配：回落默认组而不是返回空。
func TestApprovalFallbackGroupFallsBackOnBlankValue(t *testing.T) {
	engine, client, ctx, tenantID := fallbackFixture(t, "file:fb_blank?mode=memory&cache=shared&_fk=1")
	client.SystemConfig.Create().
		SetKey(ApprovalFallbackGroupConfigKey).SetValue("   ").
		SetValueType("string").SetCategory("bpmn").SetTenantID(tenantID).SaveX(ctx)

	require.Equal(t, approvalFallbackCandidateGroup, engine.approvalFallbackGroup(ctx, tenantID))
}

// 别的租户配了不算数：配置必须按租户隔离。
func TestApprovalFallbackGroupIsTenantScoped(t *testing.T) {
	engine, client, ctx, tenantID := fallbackFixture(t, "file:fb_tenant?mode=memory&cache=shared&_fk=1")
	other := client.Tenant.Create().SetName("Other").SetCode("t-other-fb").SetStatus("active").SaveX(ctx)
	client.SystemConfig.Create().
		SetKey(ApprovalFallbackGroupConfigKey).SetValue("别家的组").
		SetValueType("string").SetCategory("bpmn").SetTenantID(other.ID).SaveX(ctx)

	require.Equal(t, approvalFallbackCandidateGroup, engine.approvalFallbackGroup(ctx, tenantID),
		"别的租户的配置不得泄漏到本租户")
}

// 软删除的配置视为没配。
func TestApprovalFallbackGroupIgnoresDeletedConfiguration(t *testing.T) {
	engine, client, ctx, tenantID := fallbackFixture(t, "file:fb_deleted?mode=memory&cache=shared&_fk=1")
	client.SystemConfig.Create().
		SetKey(ApprovalFallbackGroupConfigKey).SetValue("已删除的组").
		SetValueType("string").SetCategory("bpmn").SetTenantID(tenantID).
		SetDeletedAt(time.Now()).SaveX(ctx)

	require.Equal(t, approvalFallbackCandidateGroup, engine.approvalFallbackGroup(ctx, tenantID))
}

// tenantID 为 0（异常上下文）也要安全回落，不得 panic 或返回空。
func TestApprovalFallbackGroupIsSafeWithoutATenant(t *testing.T) {
	engine, _, ctx, _ := fallbackFixture(t, "file:fb_notenant?mode=memory&cache=shared&_fk=1")

	require.Equal(t, approvalFallbackCandidateGroup, engine.approvalFallbackGroup(ctx, 0))
}
