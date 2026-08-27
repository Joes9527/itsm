package fixtures_test

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/fielddefinition"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
	"itsm-backend/tests/fixtures"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestEnsureSSLVPNMetadata(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sslvpn_fixtures_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("SSLVPN Test Tenant").
		SetCode("sslvpn-tenant").
		SetDomain("sslvpn.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	// 1. 首次初始化
	res, err := fixtures.EnsureSSLVPNMetadata(ctx, client, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, res)

	// 验证分类
	assert.Equal(t, "网络与远程访问服务", res.Category.Name)
	assert.Equal(t, "network_and_remote_access", res.Category.Code)

	// 验证服务目录项
	assert.Equal(t, "SSL-VPN 远程办公访问权限申请", res.CatalogItem.Name)
	assert.Equal(t, "sslvpn_approval_flow", res.CatalogItem.ProcessDefinitionKey)
	assert.True(t, res.CatalogItem.RequiresApproval)
	assert.Equal(t, 2, res.CatalogItem.ApprovalLevel)

	// 验证 8 个自定义字段
	require.Len(t, res.FieldDefs, 8)
	expectedFieldNames := []string{
		"applicant_name",
		"applicant_upn",
		"employee_id",
		"department",
		"vpn_level",
		"target_systems",
		"access_duration",
		"access_reason",
	}
	actualFieldNames := make([]string, 0, len(res.FieldDefs))
	for _, fd := range res.FieldDefs {
		actualFieldNames = append(actualFieldNames, fd.Name)
		assert.Equal(t, "service_catalog", fd.EntityType)
		assert.Equal(t, res.CatalogItem.ID, fd.EntityID)
		assert.True(t, fd.Required)
	}
	assert.ElementsMatch(t, expectedFieldNames, actualFieldNames)

	// 验证 3 个用户
	assert.Equal(t, "end_user_test", res.Users.EndUser.Username)
	assert.Equal(t, "end_user", res.Users.EndUser.Role)
	assert.Equal(t, "supervisor_test", res.Users.Supervisor.Username)
	assert.Equal(t, "dept_manager", res.Users.Supervisor.Role)
	assert.Equal(t, "lixin_test", res.Users.Lixin.Username)
	assert.Equal(t, "network_eng", res.Users.Lixin.Role)

	// 验证审批组
	assert.Equal(t, "dept_manager", res.DeptManagerGrp.Name)
	assert.Equal(t, "network_eng", res.NetworkEngGrp.Name)

	// 验证 BPMN 流程定义已成功部署
	procDef, err := client.ProcessDefinition.Query().
		Where(
			processdefinition.KeyEQ("sslvpn_approval_flow"),
			processdefinition.TenantIDEQ(tenant.ID),
			processdefinition.IsLatest(true),
		).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "SSL-VPN 申请与双级审批流", procDef.Name)

	// 2. 幂等性测试（再次调用不报错，不产生重复字段）
	res2, err := fixtures.EnsureSSLVPNMetadata(ctx, client, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, res2)

	count, err := client.FieldDefinition.Query().
		Where(
			fielddefinition.TenantIDEQ(tenant.ID),
			fielddefinition.EntityTypeEQ("service_catalog"),
			fielddefinition.EntityIDEQ(res.CatalogItem.ID),
		).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 8, count, "幂等调用后自定义字段数量仍应为 8")

	// 3. 验证通过 CustomProcessEngine 能够成功启动 sslvpn_approval_flow 流程
	logger := zaptest.NewLogger(t).Sugar()
	engineIface := service.NewCustomProcessEngine(client, logger)
	engine, ok := engineIface.(*service.CustomProcessEngine)
	require.True(t, ok)

	runCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	instance, err := engine.StartProcess(runCtx, "sslvpn_approval_flow", "TICKET-VPN-TEST-1", map[string]interface{}{
		"requester_id": float64(res.Users.EndUser.ID),
	})
	require.NoError(t, err)
	assert.NotNil(t, instance)
	assert.Equal(t, "running", instance.Status)
}
