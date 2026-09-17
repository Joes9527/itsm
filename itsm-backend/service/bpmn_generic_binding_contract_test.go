package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	creation "itsm-backend/handlers/common/workitemcreation"
	"testing"
)

func TestGenericCreationBindingFreezesDefinitionFlags(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	f.definition = f.definition.Update().SetBpmnXML(genericStartXML()).SetProcessVariables(map[string]interface{}{"approval_required": true, "need_escalate": true}).SaveX(context.Background())
	tx, err := f.client.Tx(context.Background())
	require.NoError(t, err)
	defer tx.Rollback()
	plan := creation.NewPlan(creation.ResolvedIntake{RecordClass: "generic", Identity: creation.Identity{TenantID: f.tenant.ID}, Command: creation.CreateWorkItemCommand{FormValues: map[string]interface{}{"detail": "ok"}}}, "open", "medium", "web")
	binding, _, err := NewProcessBindingService(f.client).ResolveCreationWorkflow(context.Background(), tx, plan, f.definition.Key)
	require.NoError(t, err)
	require.Equal(t, f.definition.ID, *binding.DefinitionID)
	require.Equal(t, true, plan.WorkflowVariables["approval_required"])
	require.Equal(t, true, plan.WorkflowVariables["need_escalate"])
}
func TestGenericCreationBindingRejectsClassAndUserFlags(t *testing.T) {
	for _, test := range []struct{ name, class, key string }{{"professional", "incident", ""}, {"flag", "generic", "approval_required"}, {"escalate", "generic", "need_escalate"}, {"approval", "generic", "approvalResult"}, {"note", "generic", "workItemCompletionNote"}} {
		t.Run(test.name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			f.definition = f.definition.Update().SetBpmnXML(genericStartXML()).SaveX(context.Background())
			tx, err := f.client.Tx(context.Background())
			require.NoError(t, err)
			defer tx.Rollback()
			input := creation.ResolvedIntake{RecordClass: test.class, Identity: creation.Identity{TenantID: f.tenant.ID}, Command: creation.CreateWorkItemCommand{FormValues: map[string]interface{}{}}}
			if test.key != "" {
				input.Command.FormValues[test.key] = false
			}
			_, _, err = NewProcessBindingService(f.client).ResolveCreationWorkflow(context.Background(), tx, creation.NewPlan(input, "open", "medium", "web"), f.definition.Key)
			require.Error(t, err)
		})
	}
}
func TestGenericPublicBindingAdmission(t *testing.T) {
	for _, class := range []string{"incident", "problem", "change_request", "service_request_item", "catalog_task", "release"} {
		t.Run(class, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			f.definition = f.definition.Update().SetBpmnXML(genericStartXML()).SaveX(context.Background())
			service := NewProcessBindingService(f.client)
			_, err := service.CreateBinding(context.Background(), &dto.ProcessBinding{BusinessType: dto.BusinessType(class), ProcessDefinitionKey: f.definition.Key, TenantID: f.tenant.ID, IsActive: true})
			require.Error(t, err)
			require.Zero(t, f.client.ProcessBinding.Query().CountX(context.Background()))
			good, err := service.CreateBinding(context.Background(), &dto.ProcessBinding{BusinessType: dto.BusinessType("generic"), ProcessDefinitionKey: f.definition.Key, TenantID: f.tenant.ID, IsActive: true})
			require.NoError(t, err)
			_, err = service.UpdateBinding(context.Background(), good.ID, &dto.ProcessBinding{BusinessType: dto.BusinessType(class), TenantID: f.tenant.ID})
			require.Error(t, err)
			require.Equal(t, "generic", f.client.ProcessBinding.GetX(context.Background(), good.ID).BusinessType)
		})
	}
	for _, key := range []string{"approval_required", "need_escalate", "approvalResult", "workItemCompletionNote"} {
		t.Run(key, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			f.definition = f.definition.Update().SetBpmnXML(genericStartXML()).SaveX(context.Background())
			_, err := NewProcessBindingService(f.client).CreateBinding(context.Background(), &dto.ProcessBinding{BusinessType: "generic", ProcessDefinitionKey: f.definition.Key, TenantID: f.tenant.ID, Overrides: map[string]interface{}{key: false}})
			require.Error(t, err)
		})
	}
}
