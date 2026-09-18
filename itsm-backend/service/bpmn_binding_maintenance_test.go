package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
)

func TestBindingMaintenanceAllowsInactiveDefinition(t *testing.T) {
	for _, contract := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "contract"}[contract], func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			if contract {
				f.definition = f.definition.Update().SetBpmnXML(genericStartXML()).SaveX(context.Background())
			}
			service := NewProcessBindingService(f.client)
			binding, err := service.CreateBinding(context.Background(), &dto.ProcessBinding{TenantID: f.tenant.ID, BusinessType: "generic", ProcessDefinitionKey: f.definition.Key, IsActive: true})
			require.NoError(t, err)
			f.definition.Update().SetIsActive(false).SaveX(context.Background())
			updated, err := service.UpdateBinding(context.Background(), binding.ID, &dto.ProcessBinding{TenantID: f.tenant.ID, IsActive: false, Priority: 42})
			require.NoError(t, err)
			require.False(t, updated.IsActive)
			require.Equal(t, 42, updated.Priority)
			_, err = service.UpdateBinding(context.Background(), binding.ID, &dto.ProcessBinding{TenantID: f.tenant.ID, IsActive: true})
			require.Error(t, err)
			require.False(t, f.client.ProcessBinding.GetX(context.Background(), binding.ID).IsActive)
			// Non-reserved settings remain maintainable against the inactive definition.
			_, err = service.UpdateBinding(context.Background(), binding.ID, &dto.ProcessBinding{TenantID: f.tenant.ID, Overrides: map[string]interface{}{"display": "ok"}})
			require.NoError(t, err)
			if contract {
				_, err = service.UpdateBinding(context.Background(), binding.ID, &dto.ProcessBinding{TenantID: f.tenant.ID, BusinessType: "incident"})
				require.Error(t, err)
				_, err = service.UpdateBinding(context.Background(), binding.ID, &dto.ProcessBinding{TenantID: f.tenant.ID, Overrides: map[string]interface{}{"approval_required": false}})
				require.Error(t, err)
			}
		})
	}
}

func TestBindingTargetChangeStillRequiresExecutableDefinition(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	service := NewProcessBindingService(f.client)
	binding, err := service.CreateBinding(context.Background(), &dto.ProcessBinding{TenantID: f.tenant.ID, BusinessType: "generic", ProcessDefinitionKey: f.definition.Key, IsActive: true})
	require.NoError(t, err)
	f.client.ProcessDefinition.Create().SetKey("inactive-target").SetName("Inactive target").SetVersion("1").SetTenantID(f.tenant.ID).SetDeploymentID(f.definition.DeploymentID).SetBpmnXML(f.definition.BpmnXML).SetIsActive(false).SaveX(context.Background())
	_, err = service.UpdateBinding(context.Background(), binding.ID, &dto.ProcessBinding{TenantID: f.tenant.ID, IsActive: false, ProcessDefinitionKey: "inactive-target"})
	require.Error(t, err)
	require.Equal(t, f.definition.Key, f.client.ProcessBinding.GetX(context.Background(), binding.ID).ProcessDefinitionKey)
}
