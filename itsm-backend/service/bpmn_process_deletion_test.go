package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processversionchangelog"
)

func newProcessDeletionFixture(t *testing.T, name string) (*ent.Client, context.Context, *ent.ProcessDefinition) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:"+name+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode(name).SetDomain(name + ".com").SetStatus("active").SaveX(ctx)
	deployment := client.ProcessDeployment.Create().SetDeploymentID("dep-" + name).SetDeploymentName("dep").SetTenantID(tenant.ID).SaveX(ctx)
	definition := client.ProcessDefinition.Create().
		SetKey(name).
		SetName("flow").
		SetVersion("1.0.0").
		SetBpmnXML([]byte("<bpmn:definitions/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		SaveX(ctx)
	return client, newTenantCtx(ctx, tenant.ID), definition
}

func newProcessDeletionService(client *ent.Client) *bpmnProcessDefinitionService {
	return &bpmnProcessDefinitionService{client: client, logger: zap.NewNop().Sugar()}
}

// Deleting a definition whose version was activated must remove its version
// change-log rows instead of failing on the foreign key.
func TestDeleteProcessDefinitionRemovesVersionChangeLogs(t *testing.T) {
	client, ctx, definition := newProcessDeletionFixture(t, "del_changelog")
	_, err := client.ProcessVersionChangelog.Create().
		SetProcessDefinitionID(definition.ID).
		SetVersion(definition.Version).
		SetChangeLog("激活流程版本 1.0.0").
		SetTenantID(definition.TenantID).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, newProcessDeletionService(client).DeleteProcessDefinition(ctx, definition.Key, definition.Version))

	require.False(t, client.ProcessDefinition.Query().Where(processdefinition.IDEQ(definition.ID)).ExistX(ctx))
	require.False(t, client.ProcessVersionChangelog.Query().Where(processversionchangelog.ProcessDefinitionIDEQ(definition.ID)).ExistX(ctx))
}

func TestDeleteProcessDefinitionRefusesUnfinishedInstance(t *testing.T) {
	client, ctx, definition := newProcessDeletionFixture(t, "del_running")
	_, err := client.ProcessInstance.Create().
		SetProcessInstanceID("pi-running").
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetStatus("running").
		SetTenantID(definition.TenantID).
		Save(ctx)
	require.NoError(t, err)

	err = newProcessDeletionService(client).DeleteProcessDefinition(ctx, definition.Key, definition.Version)
	require.Error(t, err)
	require.Contains(t, err.Error(), "未结束")
	require.True(t, client.ProcessDefinition.Query().Where(processdefinition.IDEQ(definition.ID)).ExistX(ctx))
}

func TestDeleteProcessDefinitionRefusesHistoricalInstances(t *testing.T) {
	client, ctx, definition := newProcessDeletionFixture(t, "del_completed")
	_, err := client.ProcessInstance.Create().
		SetProcessInstanceID("pi-completed").
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetStatus("completed").
		SetTenantID(definition.TenantID).
		Save(ctx)
	require.NoError(t, err)

	err = newProcessDeletionService(client).DeleteProcessDefinition(ctx, definition.Key, definition.Version)
	require.Error(t, err)
	require.Contains(t, err.Error(), "历史实例")
	require.True(t, client.ProcessDefinition.Query().Where(processdefinition.IDEQ(definition.ID)).ExistX(ctx))
}
