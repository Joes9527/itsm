package service

import (
	"context"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"

	"go.uber.org/zap"

	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestReadCTIGovernanceNotConfiguredIsDisabled(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:cti_governance_read?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := t.Context()
	tenant := client.Tenant.Create().SetCode("gov").SetName("gov").SaveX(ctx)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	governance, err := ReadCTIGovernance(ctx, tx, tenant.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	require.False(t, governance.CatalogEnforced)
	require.False(t, governance.CompletionEnforced)
	require.Nil(t, governance.EffectiveFrom)
}

func TestReadCTIGovernanceParsesConfiguredRecord(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:cti_governance_parse?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := t.Context()
	tenant := client.Tenant.Create().SetCode("gov2").SetName("gov2").SaveX(ctx)
	client.SystemConfig.Create().SetTenantID(tenant.ID).SetKey(CTIGovernanceConfigKey).
		SetValue(`{"catalogEnforced":true,"completionEnforced":true,"effectiveFrom":"2026-09-17T00:00:00Z"}`).
		SetValueType("json").SaveX(ctx)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	governance, err := ReadCTIGovernance(ctx, tx, tenant.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	require.True(t, governance.CatalogEnforced)
	require.True(t, governance.CompletionEnforced)
	require.NotNil(t, governance.EffectiveFrom)
	require.Equal(t, "2026-09-17T00:00:00Z", governance.EffectiveFrom.Format("2006-01-02T15:04:05Z"))
}

// 非法或不一致的启用记录必须明确失败，绝不能默认为“未启用”而放开业务。
func TestReadCTIGovernanceFailsClosedOnInvalidRecords(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		values []string
		want   string
	}{
		{"invalid_json", []string{`{not json}`}, "not valid JSON"},
		{"empty_value", []string{""}, "has no value"},
		{"bad_effective_from", []string{`{"completionEnforced":true,"effectiveFrom":"not-a-time"}`}, "not RFC3339"},
		{"duplicate", []string{`{"catalogEnforced":true}`, `{"catalogEnforced":false}`}, "duplicated"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client := enttest.Open(t, "sqlite3", "file:cti_gov_invalid_"+scenario.name+"?mode=memory&cache=shared&_fk=1")
			t.Cleanup(func() { client.Close() })
			ctx := t.Context()
			tenant := client.Tenant.Create().SetCode("gov-invalid").SetName("gov").SaveX(ctx)
			for _, value := range scenario.values {
				client.SystemConfig.Create().SetTenantID(tenant.ID).SetKey(CTIGovernanceConfigKey).SetValue(value).SaveX(ctx)
			}
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			_, err = ReadCTIGovernance(ctx, tx, tenant.ID)
			require.NoError(t, tx.Rollback())
			require.ErrorContains(t, err, scenario.want)
		})
	}
}

func TestReadCTIGovernanceIsTenantScoped(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:cti_gov_scope?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := t.Context()
	tenant := client.Tenant.Create().SetCode("gov-a").SetName("a").SaveX(ctx)
	other := client.Tenant.Create().SetCode("gov-b").SetName("b").SaveX(ctx)
	client.SystemConfig.Create().SetTenantID(other.ID).SetKey(CTIGovernanceConfigKey).SetValue(`{"catalogEnforced":true}`).SaveX(ctx)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	governance, err := ReadCTIGovernance(ctx, tx, tenant.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	require.False(t, governance.CatalogEnforced, "another tenant's enforcement record must not apply")
}

// 完成门禁的确定边界：截止前 1ns 不检查、相等检查、暂停不检查、旧单不追溯。
func TestCTICutoffIncludesExactInstant(t *testing.T) {
	cut := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	policy := CTIGovernance{CompletionEnforced: true, EffectiveFrom: &cut}

	before, err := RequiresCTICompletion(policy, cut.Add(-time.Nanosecond), "incident", "close")
	require.NoError(t, err)
	require.False(t, before, "截止前 1ns 创建的在途单不得被追溯要求")

	exact, err := RequiresCTICompletion(policy, cut, "incident", "close")
	require.NoError(t, err)
	require.True(t, exact, "时间相等属于新单，必须适用门禁")

	after, err := RequiresCTICompletion(policy, cut.Add(time.Nanosecond), "incident", "close")
	require.NoError(t, err)
	require.True(t, after)
}

func TestCTICompletionActionMatrix(t *testing.T) {
	cut := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	policy := CTIGovernance{CompletionEnforced: true, EffectiveFrom: &cut}
	newRecord := cut.Add(time.Hour)

	// Incident resolve 不加硬门禁；close 检查。
	for _, scenario := range []struct {
		recordClass string
		action      string
		needed      bool
	}{
		{"generic", "resolve", true},
		{"generic", "close", true},
		{"generic", "cancel", false},
		{"generic", "reopen", false},
		{"generic", "pending", false},
		{"incident", "resolve", false},
		{"incident", "close", true},
		{"problem", "close", true},
		{"change_request", "close", true},
		{"service_request_item", "close", true},
		{"service_request_item", "delivered", false},
		{"catalog_task", "close", false},
	} {
		needed, err := RequiresCTICompletion(policy, newRecord, scenario.recordClass, scenario.action)
		require.NoError(t, err, "%s/%s", scenario.recordClass, scenario.action)
		require.Equal(t, scenario.needed, needed, "%s/%s", scenario.recordClass, scenario.action)
	}
}

// 未知 class/action 必须失败而不是放行。
func TestCTICompletionUnknownTargetsFailClosed(t *testing.T) {
	cut := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	policy := CTIGovernance{CompletionEnforced: true, EffectiveFrom: &cut}
	for _, scenario := range []struct{ recordClass, action string }{
		{"release", "close"},
		{"incident", "archive"},
		{"", "close"},
		{"generic", ""},
	} {
		needed, err := RequiresCTICompletion(policy, cut.Add(time.Hour), scenario.recordClass, scenario.action)
		require.Error(t, err, "%s/%s", scenario.recordClass, scenario.action)
		require.False(t, needed)
	}
}

// 未启用不追溯；启用但缺少截止时间属于损坏配置，必须报错而不是按“关闭”处理。
func TestCTICompletionDisabledAndBrokenPolicy(t *testing.T) {
	createdAt := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	needed, err := RequiresCTICompletion(CTIGovernance{}, createdAt, "generic", "close")
	require.NoError(t, err)
	require.False(t, needed, "未启用完成门禁时保持既有完成行为")

	needed, err = RequiresCTICompletion(CTIGovernance{CompletionEnforced: true}, createdAt, "generic", "close")
	require.ErrorContains(t, err, "without an effective cutoff")
	require.False(t, needed)
}

// ctiCompletionFixture 准备一棵三级分类树与一个启用完成门禁的租户。
func ctiCompletionFixture(t *testing.T) (*ent.Client, context.Context, int, [3]int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetCode("completion").SetName("completion").SaveX(ctx)
	categories := NewTicketCategoryService(client)
	ids := [3]int{}
	parent := 0
	for index, code := range []string{"l1", "l2", "l3"} {
		record, err := categories.CreateCategory(ctx, &CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenant.ID})
		require.NoError(t, err)
		ids[index] = record.ID
		parent = record.ID
	}
	client.SystemConfig.Create().SetTenantID(tenant.ID).SetKey(CTIGovernanceConfigKey).
		SetValue(`{"completionEnforced":true,"effectiveFrom":"2026-09-17T00:00:00Z"}`).SetValueType("json").SaveX(ctx)
	return client, ctx, tenant.ID, ids
}

func TestRequireWorkItemCTICompletionEnforcesCompleteActivePath(t *testing.T) {
	client, ctx, tenantID, ids := ctiCompletionFixture(t)
	categories := NewTicketCategoryService(client)
	after := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	before := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)

	check := func(categoryID int, createdAt time.Time, recordClass, action string) error {
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		defer func() { require.NoError(t, tx.Rollback()) }()
		return RequireWorkItemCTICompletion(ctx, tx, tenantID, recordClass, action, createdAt, categoryID)
	}

	// 完整三级、启用中 → 允许。
	require.NoError(t, check(ids[2], after, "problem", "close"))
	// 在途单（截止前创建）→ 不受门禁影响。
	require.NoError(t, check(0, before, "problem", "close"))
	// 未分类 / 部分分类 → 拒绝。
	require.ErrorIs(t, check(0, after, "problem", "close"), ErrCTICompletionRequired)
	require.ErrorIs(t, check(ids[1], after, "problem", "close"), ErrCTICompletionRequired)
	// 停用祖先不追溯：停用只禁止新选择，分类停用前已合法引用的在途单仍可完成。
	disabled := false
	_, err := categories.UpdateCategory(ctx, ids[0], &UpdateCategoryRequest{IsActive: &disabled}, tenantID)
	require.NoError(t, err)
	require.NoError(t, check(ids[2], after, "problem", "close"))
}

// 未知动作/类必须失败，而不是静默放行（fail closed）。
func TestRequireWorkItemCTICompletionFailsClosedOnUnknownTargets(t *testing.T) {
	client, ctx, tenantID, ids := ctiCompletionFixture(t)
	after := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	for _, scenario := range []struct{ recordClass, action string }{
		{"release", "close"},
		{"problem", "obliterate"},
		{"", "close"},
	} {
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		err = RequireWorkItemCTICompletion(ctx, tx, tenantID, scenario.recordClass, scenario.action, after, ids[2])
		require.NoError(t, tx.Rollback())
		require.Error(t, err, "%s/%s", scenario.recordClass, scenario.action)
		require.NotErrorIs(t, err, ErrCTICompletionRequired, "policy errors are not business rejections")
	}
}

// 跨租户或已删除的分类按“分类不可用”拒绝，且不泄露对象是否存在。
func TestRequireWorkItemCTICompletionRejectsForeignClassification(t *testing.T) {
	client, ctx, tenantID, _ := ctiCompletionFixture(t)
	after := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	other := client.Tenant.Create().SetCode("completion-other").SetName("other").SaveX(ctx)
	foreign := client.TicketCategory.Create().SetName("foreign").SetCode("foreign").SetTenantID(other.ID).SaveX(ctx)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	err = RequireWorkItemCTICompletion(ctx, tx, tenantID, "problem", "close", after, foreign.ID)
	require.NoError(t, tx.Rollback())
	require.ErrorIs(t, err, ErrCTICompletionRequired)
}

// 动作矩阵完整性：各专业命令的动作集合已在拥有者中定义，任何新增动作都必须先归类，
// 否则门禁会在运行时 fail closed（这是刻意的），本测试让这种遗漏在 CI 阶段就暴露。
func TestCTICompletionMatrixCoversOwningCommandActions(t *testing.T) {
	owners := map[string][]string{
		// handlers/change/commands.go: applyCommandTx 的动作分支
		"change_request": {"submit", "assess", "authorize", "schedule", "implement", "record_outcome", "review", "close", "cancel"},
		// handlers/problem/lifecycle.go: applyCommandTx 的动作分支
		"problem": {"investigate", "verify_resolution", "select_resolution", "resolve", "close", "reopen"},
		// service/incident_commands.go: applyIncidentCommandTx 的动作分支
		"incident": {"assign", "escalate", "acknowledge", "start", "resolve", "close", "reopen"},
		// service/ticket_service.go: generic 工单命令入口
		"generic": {"resolve", "close", "cancel", "reopen", "pending"},
	}
	for recordClass, actions := range owners {
		matrix, known := ctiCompletionActions[recordClass]
		require.True(t, known, "record class %s must be classified", recordClass)
		for _, action := range actions {
			_, classified := matrix[action]
			require.True(t, classified, "%s/%s must be classified as gated or not gated", recordClass, action)
		}
	}
}

// 受控启用：第一次启用写入截止时间，之后只能启停布尔值，暂停不撤销截止时间。
func TestSetCTIGovernanceControlledActivation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:cti_gov_activation?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetCode("gov-activation").SetName("gov").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("gov-admin").SetName("admin").
		SetEmail("gov-admin@example.test").SetPasswordHash("x").SetRole("super_admin").SaveX(ctx)
	svc := NewSystemConfigService(client, zap.NewNop().Sugar())

	// 第一次启用：写入截止时间并审计。
	first, err := svc.SetCTIGovernance(ctx, tenant.ID, actor.ID, "admin_ui", CTIGovernanceUpdate{CompletionEnforced: true, CatalogEnforced: true})
	require.NoError(t, err)
	require.True(t, first.Applied)
	require.True(t, first.Governance.CompletionEnforced)
	require.NotNil(t, first.Governance.EffectiveFrom)
	cutoff := *first.Governance.EffectiveFrom
	require.Equal(t, 1, client.AuditLog.Query().Where(auditlog.ResourceEQ("cti_governance")).CountX(ctx))

	// 再次启用（幂等）：不改变截止时间，也不重复写审计。
	again, err := svc.SetCTIGovernance(ctx, tenant.ID, actor.ID, "admin_ui", CTIGovernanceUpdate{CompletionEnforced: true, CatalogEnforced: true})
	require.NoError(t, err)
	require.False(t, again.Applied)
	require.Equal(t, cutoff, *again.Governance.EffectiveFrom)
	require.Equal(t, 1, client.AuditLog.Query().Where(auditlog.ResourceEQ("cti_governance")).CountX(ctx))

	// 暂停：只关布尔值，保留截止时间（恢复不把在途单变成新单）。
	paused, err := svc.SetCTIGovernance(ctx, tenant.ID, actor.ID, "admin_ui", CTIGovernanceUpdate{})
	require.NoError(t, err)
	require.True(t, paused.Applied)
	require.False(t, paused.Governance.CompletionEnforced)
	require.NotNil(t, paused.Governance.EffectiveFrom)
	require.Equal(t, cutoff, *paused.Governance.EffectiveFrom)

	// 恢复：截止时间仍是第一次启用的时间，并给出暂停期在途未分类单的数量下界。
	client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetTitle("in-flight").
		SetTicketNumber("CTI-GOV-1").SetStatus("open").SetCreatedAt(cutoff.Add(time.Minute)).SaveX(ctx)
	resumed, err := svc.SetCTIGovernance(ctx, tenant.ID, actor.ID, "admin_ui", CTIGovernanceUpdate{CompletionEnforced: true})
	require.NoError(t, err)
	require.Equal(t, cutoff, *resumed.Governance.EffectiveFrom)
	require.Equal(t, 1, resumed.InFlightWithoutClassification)
	require.Equal(t, 3, client.AuditLog.Query().Where(auditlog.ResourceEQ("cti_governance")).CountX(ctx))
}

// 通用配置接口不得读写保留键，避免绕过受控启用的锁与审计。
func TestReservedGovernanceKeyRejectedByGenericConfigAPI(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:cti_gov_reserved?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetCode("gov-reserved").SetName("gov").SaveX(ctx)
	svc := NewSystemConfigService(client, zap.NewNop().Sugar())

	_, err := svc.CreateSystemConfig(ctx, &dto.SystemConfigRequest{Key: CTIGovernanceConfigKey, Value: `{"completionEnforced":false}`}, tenant.ID)
	require.ErrorIs(t, err, ErrReservedSystemConfigKey)

	_, err = svc.BatchUpdateSystemConfigs(ctx, []dto.UpdateSystemConfigRequest{{Key: CTIGovernanceConfigKey, Value: `{"completionEnforced":false}`}}, tenant.ID)
	require.ErrorIs(t, err, ErrReservedSystemConfigKey)
	require.Zero(t, client.SystemConfig.Query().CountX(ctx), "批量请求整笔拒绝，不能部分写入")

	row := client.SystemConfig.Create().SetTenantID(tenant.ID).SetKey(CTIGovernanceConfigKey).
		SetValue(`{"completionEnforced":true}`).SetValueType("json").SaveX(ctx)
	_, err = svc.UpdateSystemConfig(ctx, row.ID, &dto.UpdateSystemConfigRequest{Value: `{"completionEnforced":false}`}, tenant.ID)
	require.ErrorIs(t, err, ErrReservedSystemConfigKey)
	err = svc.DeleteSystemConfig(ctx, row.ID, tenant.ID)
	require.ErrorIs(t, err, ErrReservedSystemConfigKey)

	// 受控路径仍然可以写入同一租户的治理记录。
	require.NoError(t, func() error {
		_, err := svc.SetCTIGovernance(ctx, tenant.ID, 1, "admin_ui", CTIGovernanceUpdate{CatalogEnforced: true})
		return err
	}())
}
