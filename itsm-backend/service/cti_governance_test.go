package service

import (
	"testing"
	"time"

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
