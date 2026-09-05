package intake

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/handlers/common/workitemcreation"
	cataloghandler "itsm-backend/handlers/service_catalog"
	"itsm-backend/service"
	"testing"
)

func TestCreationCatalogRevisionAndWorkflowResolution(t *testing.T) {
	client, _, identity, _, _, _ := intakeFixture(t)
	ctx := context.Background()
	catalog := client.ServiceCatalog.Create().SetTenantID(identity.TenantID).SetName("VPN").SetTargetClass("service_request_item").SaveX(ctx)
	client.ProcessBinding.Create().SetTenantID(identity.TenantID).SetBusinessType("service_request").SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	field := client.FieldDefinition.Create().SetTenantID(identity.TenantID).SetEntityType("service_catalog").SetEntityID(catalog.ID).SetName("device_count").SetLabel("Devices").SetFieldType("number").SetRequired(true).SaveX(ctx)
	owner := cataloghandler.NewService(nil, client, zap.NewNop().Sugar())
	port, ok := any(owner).(workitemcreation.CatalogResolver)
	require.True(t, ok, "catalog owner must provide a real transaction-aware revision")
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	first, defs, err := port.ResolveCreationCatalog(ctx, tx, identity, catalog.ID)
	require.NoError(t, err)
	require.NotEmpty(t, first.Version)
	require.NotEmpty(t, first.FormSchemaVersion)
	require.Len(t, defs, 1)
	binding, _, err := service.NewProcessBindingService(tx.Client()).ResolveCreationWorkflow(ctx, tx, workitemcreation.ResolvedIntake{Identity: identity, RecordClass: catalog.TargetClass}, "")
	require.NoError(t, err)
	require.True(t, binding.NoProcess)
	require.NoError(t, tx.Rollback())
	client.FieldDefinition.UpdateOneID(field.ID).SetRequired(false).ExecX(ctx)
	tx, err = client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	second, _, err := port.ResolveCreationCatalog(ctx, tx, identity, catalog.ID)
	require.NoError(t, err)
	require.NotEqual(t, first.Version, second.Version)
	require.NotEqual(t, first.FormSchemaVersion, second.FormSchemaVersion)
}
func TestCreationWorkflowMissingBindingFailsClosed(t *testing.T) {
	client, _, identity, command, _, _ := intakeFixture(t)
	owner := service.NewProcessBindingService(client)
	port, ok := any(owner).(workitemcreation.WorkflowResolver)
	require.True(t, ok, "workflow owner must resolve creation bindings")
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	defer tx.Rollback()
	_, _, err = port.ResolveCreationWorkflow(context.Background(), tx, workitemcreation.ResolvedIntake{Identity: identity, Command: command, RecordClass: "generic"}, "")
	require.ErrorIs(t, err, workitemcreation.ErrWorkflowBindingRequired)
}

func TestCreationReferencesUseOwningServices(t *testing.T) {
	client, _, _, _, _, _ := intakeFixture(t)
	_, ok := any(service.NewConfigurationItemService(client, zap.NewNop().Sugar(), nil, nil)).(workitemcreation.ConfigurationItemResolver)
	require.True(t, ok, "CMDB owner must resolve authorized creation references")
	_, ok = any(&service.TicketCategoryService{}).(workitemcreation.ClassificationResolver)
	require.True(t, ok, "classification owner must resolve creation hierarchy")
}

func TestCreationCIReferenceFailsAcrossTenants(t *testing.T) {
	client, _, identity, _, _, _ := intakeFixture(t)
	ctx := context.Background()
	other := client.Tenant.Create().SetName("Other").SetCode("other").SaveX(ctx)
	ciType := client.CIType.Create().SetTenantID(other.ID).SetName("Server").SetDescription("Server").SaveX(ctx)
	ci := client.ConfigurationItem.Create().SetTenantID(other.ID).SetName("Private").SetCiTypeID(ciType.ID).SaveX(ctx)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = service.NewConfigurationItemService(client, zap.NewNop().Sugar(), nil, nil).ResolveCreationCIs(ctx, tx, identity, []int{ci.ID}, nil)
	require.ErrorIs(t, err, workitemcreation.ErrReferenceNotFound)
}

func TestCreationWorkflowResolvesMajorVersionToExactDefinition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		newer   bool
		invalid bool
	}{
		{name: "semantic latest", version: "1.0.0"},
		{name: "explicit older major", version: "v1.2.3", newer: true},
		{name: "malformed version", version: "1garbage", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _, identity, command, _, _ := intakeFixture(t)
			ctx := context.Background()
			deployment := client.ProcessDeployment.Create().SetTenantID(identity.TenantID).SetDeploymentID("semantic").SetDeploymentName("Semantic").SaveX(ctx)
			definition := client.ProcessDefinition.Create().SetTenantID(identity.TenantID).SetDeploymentID(deployment.ID).SetKey("semantic").SetName("Semantic").SetVersion(tc.version).SetIsActive(true).SetIsLatest(!tc.newer).SetBpmnXML([]byte("<definitions/>")).SaveX(ctx)
			if tc.newer {
				client.ProcessDefinition.Create().SetTenantID(identity.TenantID).SetDeploymentID(deployment.ID).SetKey("semantic").SetName("Semantic").SetVersion("2.0.0").SetIsActive(true).SetIsLatest(true).SetBpmnXML([]byte("<definitions/>")).SaveX(ctx)
			}
			client.ProcessBinding.Create().SetTenantID(identity.TenantID).SetBusinessType("ticket").SetIsDefault(true).SetProcessDefinitionKey("semantic").SetProcessVersion(1).SaveX(ctx)
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			resolved, _, err := service.NewProcessBindingService(client).ResolveCreationWorkflow(ctx, tx, workitemcreation.ResolvedIntake{Identity: identity, Command: command, RecordClass: "generic"}, "")
			if tc.invalid {
				require.ErrorIs(t, err, workitemcreation.ErrDomainValidationFailed)
				return
			}
			require.NoError(t, err)
			require.Equal(t, definition.ID, *resolved.DefinitionID)
			require.Equal(t, tc.version, resolved.DefinitionVersion)
		})
	}
}
