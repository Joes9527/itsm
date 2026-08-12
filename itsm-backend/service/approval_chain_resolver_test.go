package service

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/schema"

	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalChainResolver_NoChainConfigured(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:chain_resolver_none?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	resolver := NewApprovalChainResolver(client, zaptest.NewLogger(t).Sugar())

	chain, err := resolver.ResolveForServiceRequest(context.Background(), 1, 0, "", 0)
	require.NoError(t, err)
	assert.Nil(t, chain, "no chain configured => nil result")
}

func TestApprovalChainResolver_BasicChain(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:chain_resolver_basic?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Chain Test").SetCode("chain-test").
		SetDomain("chain-test.example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	steps := []schema.ApprovalChainStep{
		{Level: 1, Name: "部门主管审批", Role: "manager", ApprovalType: "serial", IsRequired: true},
		{Level: 2, Name: "IT审批", Role: "it_admin", ApprovalType: "serial", IsRequired: true, AmountThreshold: 50000},
		{Level: 3, Name: "安全审批", Role: "security_admin", ApprovalType: "parallel", IsRequired: true, GroupControlled: true},
	}
	_, err = client.ApprovalChain.Create().
		SetName("服务请求审批链").
		SetEntityType("service_request").
		SetTenantID(tenant.ID).
		SetChain(steps).
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	resolver := NewApprovalChainResolver(client, zaptest.NewLogger(t).Sugar())

	t.Run("all steps when amount exceeds thresholds", func(t *testing.T) {
		chain, err := resolver.ResolveForServiceRequest(ctx, tenant.ID, 80000, "", 0)
		require.NoError(t, err)
		require.NotNil(t, chain)
		require.Len(t, chain.Steps, 3)
	})

	t.Run("IT step filtered below threshold", func(t *testing.T) {
		chain, err := resolver.ResolveForServiceRequest(ctx, tenant.ID, 10000, "", 0)
		require.NoError(t, err)
		require.NotNil(t, chain)
		require.Len(t, chain.Steps, 2)
	})

	t.Run("group_controlled always included", func(t *testing.T) {
		chain, err := resolver.ResolveForServiceRequest(ctx, tenant.ID, 0, "", 0)
		require.NoError(t, err)
		require.NotNil(t, chain)
		assert.True(t, chain.Steps[1].GroupControlled)
	})
}

func TestApprovalChainResolver_TenantIsolation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:chain_resolver_isolation?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenantA, err := client.Tenant.Create().
		SetName("Tenant A").SetCode("chain-tenant-a").
		SetDomain("chain-a.example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	tenantB, err := client.Tenant.Create().
		SetName("Tenant B").SetCode("chain-tenant-b").
		SetDomain("chain-b.example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.ApprovalChain.Create().
		SetName("TenantA Chain").SetEntityType("service_request").
		SetTenantID(tenantA.ID).SetChain([]schema.ApprovalChainStep{
			{Level: 1, Name: "TenantA审批", Role: "manager", ApprovalType: "serial", IsRequired: true},
		}).SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	resolver := NewApprovalChainResolver(client, zaptest.NewLogger(t).Sugar())
	chain, err := resolver.ResolveForServiceRequest(ctx, tenantB.ID, 0, "", 0)
	require.NoError(t, err)
	assert.Nil(t, chain, "tenant B should not see tenant A's chain")
}

func TestParseChainSteps(t *testing.T) {
	steps, err := parseChainSteps(nil)
	require.NoError(t, err)
	assert.Nil(t, steps)

	raw := []interface{}{
		map[string]interface{}{
			"level":            float64(1),
			"role":             "manager",
			"name":             "经理审批",
			"org_scope":        "subsidiary",
			"amount_threshold": float64(30000),
			"group_controlled": true,
		},
	}
	steps, err = parseChainSteps(raw)
	require.NoError(t, err)
	require.Len(t, steps, 1)
	assert.Equal(t, "subsidiary", steps[0].OrgScope)
	assert.Equal(t, 30000.0, steps[0].AmountThreshold)
	assert.True(t, steps[0].GroupControlled)
}

var _ *ent.Client
