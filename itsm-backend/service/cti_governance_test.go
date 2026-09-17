package service

import (
	"testing"

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
