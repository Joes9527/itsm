package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processauditlog"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupInstanceVariablesFixture(t *testing.T) (*ent.Client, *bpmnProcessInstanceService, int, int, *ent.ProcessInstance) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:piv_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("piv-1").SetDomain("piv-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	actor, err := client.User.Create().
		SetUsername("piv-actor").
		SetEmail("piv-actor@example.test").
		SetName("PIV Actor").
		SetPasswordHash("test").
		SetTenantID(tenant.ID).
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

	logger := zaptest.NewLogger(t).Sugar()
	svc := &bpmnProcessInstanceService{
		client:       client,
		logger:       logger,
		auditService: NewBPMNAuditService(client, logger),
	}
	return client, svc, tenant.ID, actor.ID, instance
}

// TestSetProcessInstanceVariables_RejectsReservedKeys 锁定纵深防御白名单：
// 流程身份键（business_id/business_type/business_key/tenant_id）由触发方写入，
// 不允许经该端点覆盖，防止实例归属方污染自己实例的身份上下文。
func TestSetProcessInstanceVariables_RejectsReservedKeys(t *testing.T) {
	client, svc, tenantID, actorID, instance := setupInstanceVariablesFixture(t)
	ctx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID: actorID, TenantID: tenantID, CanUpdateAllInstances: true,
	})

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
	client, svc, tenantID, actorID, instance := setupInstanceVariablesFixture(t)
	ctx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID: actorID, TenantID: tenantID, CanUpdateAllInstances: true,
	})

	err := svc.SetProcessInstanceVariables(ctx, strconv.Itoa(instance.ID), map[string]interface{}{"custom": "x", "priority": "high"})
	require.NoError(t, err)

	after, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "x", after.Variables["custom"])
	assert.Equal(t, "high", after.Variables["priority"])
	assert.Equal(t, instance.Version+1, after.Version)
	assert.Equal(t, 1, client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionVariableChanged),
	).CountX(ctx))
}

func TestSetProcessInstanceVariables_RejectsTerminalLifecycle(t *testing.T) {
	for _, status := range []string{"completed", "terminated"} {
		t.Run(status, func(t *testing.T) {
			client, svc, tenantID, actorID, instance := setupInstanceVariablesFixture(t)
			ctx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
				UserID: actorID, TenantID: tenantID, CanUpdateAllInstances: true,
			})
			beforeVariables := map[string]interface{}{"preserved": true}
			instance, err := client.ProcessInstance.UpdateOne(instance).
				SetStatus(status).
				SetVariables(beforeVariables).
				Save(ctx)
			require.NoError(t, err)

			err = svc.SetProcessInstanceVariables(ctx, instance.ProcessInstanceID, map[string]interface{}{"late": true})
			requireBPMNLifecycleConflict(t, err)
			after := client.ProcessInstance.GetX(ctx, instance.ID)
			assert.Equal(t, status, after.Status)
			assert.Equal(t, instance.Version, after.Version)
			assert.Equal(t, beforeVariables, after.Variables)
			assert.Zero(t, client.ProcessAuditLog.Query().Where(
				processauditlog.ProcessInstanceID(instance.ID),
				processauditlog.Action(AuditActionVariableChanged),
			).CountX(ctx))
		})
	}
}

func TestSetProcessInstanceVariables_RejectsUnknownLifecycle(t *testing.T) {
	client, svc, tenantID, actorID, instance := setupInstanceVariablesFixture(t)
	ctx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID: actorID, TenantID: tenantID, CanUpdateAllInstances: true,
	})
	instance, err := client.ProcessInstance.UpdateOne(instance).SetStatus("unknown").Save(ctx)
	require.NoError(t, err)

	err = svc.SetProcessInstanceVariables(ctx, instance.ProcessInstanceID, map[string]interface{}{"late": true})
	requireBPMNLifecycleConflict(t, err)
	after := client.ProcessInstance.GetX(ctx, instance.ID)
	assert.Equal(t, "unknown", after.Status)
	assert.Equal(t, instance.Version, after.Version)
	assert.Empty(t, after.Variables)
	assert.Zero(t, client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionVariableChanged),
	).CountX(ctx))
}
