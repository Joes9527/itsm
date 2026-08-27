package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

// testDSN 为每个测试返回唯一的 SQLite 内存数据库 DSN，避免测试间数据库残留
// （与 cmd/check_work_item_integrity/main_test.go 同一做法）。
var testDBCounter int64

func testDSN() string {
	return fmt.Sprintf("file:backfill_pi_business_identity_test_%d?mode=memory&cache=shared&_fk=1", atomic.AddInt64(&testDBCounter, 1))
}

func TestDeriveBusinessIdentity(t *testing.T) {
	cases := []struct {
		name         string
		instance     *ent.ProcessInstance
		expectType   string
		expectID     int
		expectSource string
	}{
		{
			name: "variables 完整时优先用 variables",
			instance: &ent.ProcessInstance{
				BusinessKey: "ticket:99",
				Variables:   map[string]interface{}{"business_type": "change", "business_id": 7},
			},
			expectType: "change", expectID: 7, expectSource: "variables",
		},
		{
			name: "variables 的 business_id 是 JSON 反序列化后的 float64",
			instance: &ent.ProcessInstance{
				BusinessKey: "ticket:99",
				Variables:   map[string]interface{}{"business_type": "ticket", "business_id": float64(42)},
			},
			expectType: "ticket", expectID: 42, expectSource: "variables",
		},
		{
			name: "variables 缺失时回退解析 business_key",
			instance: &ent.ProcessInstance{
				BusinessKey: "service_request:15",
				Variables:   map[string]interface{}{},
			},
			expectType: "service_request", expectID: 15, expectSource: "business_key",
		},
		{
			name: "variables 只有 business_type，ID 由 business_key 补齐",
			instance: &ent.ProcessInstance{
				BusinessKey: "incident:31",
				Variables:   map[string]interface{}{"business_type": "incident"},
			},
			expectType: "incident", expectID: 31, expectSource: "variables+business_key",
		},
		{
			name: "business_key 大小写归一到小写，与 dto.BusinessType 取值一致",
			instance: &ent.ProcessInstance{
				BusinessKey: "Release:8",
				Variables:   nil,
			},
			expectType: "release", expectID: 8, expectSource: "business_key",
		},
		{
			name: "business_key 无法解析且 variables 为空时推导失败",
			instance: &ent.ProcessInstance{
				BusinessKey: "legacy-no-colon-key",
				Variables:   nil,
			},
			expectType: "", expectID: 0, expectSource: "",
		},
		{
			name: "business_key 的 ID 段不是数字时推导失败，不能瞎猜",
			instance: &ent.ProcessInstance{
				BusinessKey: "ticket:abc",
				Variables:   nil,
			},
			expectType: "", expectID: 0, expectSource: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bType, bID, source := deriveBusinessIdentity(tc.instance)
			require.Equal(t, tc.expectType, bType)
			require.Equal(t, tc.expectID, bID)
			require.Equal(t, tc.expectSource, source)
		})
	}
}

// TestFindCandidates 锁定候选筛选口径：只挑 running 且身份不全的行，
// 已经写好两列的行、已经结束的实例都不能进候选。
func TestFindCandidates(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Backfill Tenant").
		SetCode("backfill-pi-tenant").
		SetDomain("backfill-pi.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("deploy-backfill-test").
		SetDeploymentName("backfill-test").
		SetDeploymentSource("test").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	definition, err := client.ProcessDefinition.Create().
		SetKey("ticket_general_flow").
		SetName("通用工单流程").
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	newInstance := func(key, businessKey, status string, businessType string, businessID int, vars map[string]interface{}) *ent.ProcessInstance {
		create := client.ProcessInstance.Create().
			SetProcessInstanceID(key).
			SetBusinessKey(businessKey).
			SetProcessDefinitionKey("ticket_general_flow").
			SetProcessDefinitionID(definition.ID).
			SetStatus(status).
			SetTenantID(tenant.ID)
		if vars != nil {
			create = create.SetVariables(vars)
		}
		if businessType != "" {
			create = create.SetBusinessType(businessType)
		}
		if businessID > 0 {
			create = create.SetBusinessID(businessID)
		}
		inst, createErr := create.Save(ctx)
		require.NoError(t, createErr)
		return inst
	}

	legacy := newInstance("PI-legacy-1", "ticket:11", "running", "", 0,
		map[string]interface{}{"business_type": "ticket", "business_id": 11})
	halfFilled := newInstance("PI-legacy-2", "change:22", "running", "change", 0, nil)
	unresolvable := newInstance("PI-legacy-3", "no-colon", "running", "", 0, nil)
	newInstance("PI-modern-1", "ticket:33", "running", "ticket", 33, nil)
	newInstance("PI-done-1", "ticket:44", "completed", "", 0, nil)

	resolved, skipped, err := findCandidates(ctx, client, tenant.ID)
	require.NoError(t, err)

	byID := map[int]candidate{}
	for _, c := range resolved {
		byID[c.id] = c
	}
	require.Len(t, resolved, 2, "只有两条 running 且能推导出身份的老实例进候选")

	require.Equal(t, "ticket", byID[legacy.ID].businessType)
	require.Equal(t, 11, byID[legacy.ID].businessID)

	require.Equal(t, "change", byID[halfFilled.ID].businessType,
		"已写好的 business_type 必须保持原值，不能被推导值覆盖")
	require.Equal(t, 22, byID[halfFilled.ID].businessID,
		"只有缺失的 business_id 由 business_key 补齐")

	require.Len(t, skipped, 1)
	require.Equal(t, unresolvable.ID, skipped[0].id,
		"推导不出身份的行要单独报出来供人工处理，不能被静默丢弃")
}

// TestFindCandidates_TenantScoped 锁定租户过滤：-tenant-id 必须真的收敛查询范围。
func TestFindCandidates_TenantScoped(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	mk := func(code, domain string) int {
		tenant, err := client.Tenant.Create().
			SetName(code).SetCode(code).SetDomain(domain).SetStatus("active").Save(ctx)
		require.NoError(t, err)
		deployment, err := client.ProcessDeployment.Create().
			SetDeploymentID("deploy-" + code).SetDeploymentName(code).
			SetDeploymentSource("test").SetTenantID(tenant.ID).Save(ctx)
		require.NoError(t, err)
		definition, err := client.ProcessDefinition.Create().
			SetKey("ticket_general_flow").SetName("通用工单流程").
			SetBpmnXML([]byte("<definitions/>")).
			SetDeploymentID(deployment.ID).SetTenantID(tenant.ID).Save(ctx)
		require.NoError(t, err)
		_, err = client.ProcessInstance.Create().
			SetProcessInstanceID("PI-" + code).
			SetBusinessKey("ticket:1").
			SetProcessDefinitionKey("ticket_general_flow").
			SetProcessDefinitionID(definition.ID).
			SetStatus("running").
			SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, err)
		return tenant.ID
	}

	tenantA := mk("tenant-a", "a.example.com")
	mk("tenant-b", "b.example.com")

	scoped, skipped, err := findCandidates(ctx, client, tenantA)
	require.NoError(t, err)
	require.Empty(t, skipped)
	require.Len(t, scoped, 1)
	require.Equal(t, tenantA, scoped[0].tenantID)

	all, _, err := findCandidates(ctx, client, 0)
	require.NoError(t, err)
	require.Len(t, all, 2, "tenant-id<=0 时处理所有租户")
}
