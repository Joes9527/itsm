package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupInstanceVariablesFixture(t *testing.T) (*ent.Client, *bpmnProcessInstanceService, int, *ent.ProcessInstance) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:piv_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("piv-1").SetDomain("piv-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("deploy-piv").
		SetDeploymentName("piv").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	definition, err := client.ProcessDefinition.Create().
		SetKey("piv-flow").
		SetName("piv-flow").
		SetBpmnXML([]byte("<bpmn/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	instance, err := client.ProcessInstance.Create().
		SetProcessInstanceID("PI-piv-1").
		SetBusinessKey("ticket:1").
		SetProcessDefinitionKey("piv-flow").
		SetProcessDefinitionID(definition.ID).
		SetStatus("running").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	svc := &bpmnProcessInstanceService{client: client, logger: zaptest.NewLogger(t).Sugar()}
	return client, svc, tenant.ID, instance
}

// TestSetProcessInstanceVariables_RejectsReservedKeys 锁定纵深防御白名单：
// 流程身份键（business_id/business_type/business_key/tenant_id）由触发方写入，
// 不允许经该端点覆盖，防止实例归属方污染自己实例的身份上下文。
func TestSetProcessInstanceVariables_RejectsReservedKeys(t *testing.T) {
	client, svc, _, instance := setupInstanceVariablesFixture(t)
	ctx := context.Background()

	for _, key := range reservedInstanceVariableKeys {
		t.Run(key, func(t *testing.T) {
			err := svc.SetProcessInstanceVariables(ctx, strconv.Itoa(instance.ID), map[string]interface{}{key: "forged"})
			assert.Error(t, err, "保留键必须被拒绝")
			assert.Contains(t, err.Error(), key)

			after, err := client.ProcessInstance.Get(ctx, instance.ID)
			require.NoError(t, err)
			assert.Empty(t, after.Variables, "被拒绝的提交不得写入实例变量")
		})
	}
}

// TestSetProcessInstanceVariables_AllowsBusinessVars 普通业务键仍可经该端点写入。
func TestSetProcessInstanceVariables_AllowsBusinessVars(t *testing.T) {
	client, svc, _, instance := setupInstanceVariablesFixture(t)
	ctx := context.Background()

	err := svc.SetProcessInstanceVariables(ctx, strconv.Itoa(instance.ID), map[string]interface{}{"custom": "x", "priority": "high"})
	require.NoError(t, err)

	after, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "x", after.Variables["custom"])
	assert.Equal(t, "high", after.Variables["priority"])
}
