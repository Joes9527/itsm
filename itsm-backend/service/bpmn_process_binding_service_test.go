package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func newIntakeBindingFixture(t *testing.T) (*ent.Client, *ent.Tenant) {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", name))
	t.Cleanup(func() { client.Close() })
	tenant, err := client.Tenant.Create().SetName(name).SetCode(name).SetStatus("active").Save(context.Background())
	require.NoError(t, err)
	return client, tenant
}

func seedIntakeProcessDefinition(t *testing.T, client *ent.Client, tenantID int, key, version string) *ent.ProcessDefinition {
	t.Helper()
	ctx := context.Background()
	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID(key + "-" + version + fmt.Sprint(tenantID)).
		SetDeploymentName(key).SetTenantID(tenantID).Save(ctx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().
		SetKey(key).SetName(key).SetVersion(version).SetBpmnXML([]byte("<definitions/>")).
		SetIsActive(true).SetIsLatest(true).SetDeploymentID(deployment.ID).SetTenantID(tenantID).Save(ctx)
	require.NoError(t, err)
	return definition
}

func TestResolveIntakeBindingCatalogKeyWinsAndFreezesDefinition(t *testing.T) {
	client, tenant := newIntakeBindingFixture(t)
	ctx := context.Background()
	catalogDef := seedIntakeProcessDefinition(t, client, tenant.ID, "catalog-flow", "7")
	seedIntakeProcessDefinition(t, client, tenant.ID, "binding-flow", "3")
	_, err := client.ProcessBinding.Create().SetBusinessType("ticket").SetBusinessSubType("incident").
		SetProcessDefinitionKey("binding-flow").SetPriority(100).SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	resolved, err := NewProcessBindingService(client).ResolveIntakeBinding(ctx, tx, tenant.ID, "incident", "catalog-flow")
	require.NoError(t, err)
	require.Equal(t, catalogDef.ID, resolved.DefinitionID)
	require.Equal(t, "catalog-flow", resolved.DefinitionKey)
	require.Equal(t, "7", resolved.DefinitionVersion)
	require.False(t, resolved.NoProcess)
	require.NoError(t, tx.Rollback())
}

func TestResolveIntakeBindingSupportsExplicitNoProcessAndFailsWhenMissing(t *testing.T) {
	client, tenant := newIntakeBindingFixture(t)
	ctx := context.Background()
	_, err := client.ProcessBinding.Create().SetBusinessType("ticket").SetBusinessSubType("incident").
		SetProcessDefinitionKey("unused").SetConditions(map[string]any{"no_process": true}).
		SetPriority(100).SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	resolved, err := NewProcessBindingService(client).ResolveIntakeBinding(ctx, tx, tenant.ID, "incident", "")
	require.NoError(t, err)
	require.True(t, resolved.NoProcess)
	require.Zero(t, resolved.DefinitionID)
	require.NoError(t, tx.Rollback())

	tx2, err := client.Tx(ctx)
	require.NoError(t, err)
	_, err = NewProcessBindingService(client).ResolveIntakeBinding(ctx, tx2, tenant.ID, "service_request_item", "")
	require.ErrorIs(t, err, ErrIntakeWorkflowBindingRequired)
	require.NoError(t, tx2.Rollback())
}

func TestResolveIntakeBindingHonorsConfiguredBindingVersion(t *testing.T) {
	client, tenant := newIntakeBindingFixture(t)
	ctx := context.Background()
	v3 := seedIntakeProcessDefinition(t, client, tenant.ID, "versioned-flow", "3")
	_, err := v3.Update().SetIsLatest(false).Save(ctx)
	require.NoError(t, err)
	seedIntakeProcessDefinition(t, client, tenant.ID, "versioned-flow", "4")
	_, err = client.ProcessBinding.Create().SetBusinessType("ticket").SetBusinessSubType("incident").
		SetProcessDefinitionKey("versioned-flow").SetProcessVersion(3).
		SetPriority(100).SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	resolved, err := NewProcessBindingService(client).ResolveIntakeBinding(ctx, tx, tenant.ID, "incident", "")
	require.NoError(t, err)
	require.Equal(t, v3.ID, resolved.DefinitionID)
	require.Equal(t, "3", resolved.DefinitionVersion)
	require.NoError(t, tx.Rollback())
}
