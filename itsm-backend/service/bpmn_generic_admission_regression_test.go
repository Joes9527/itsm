package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/service/bpmn"
)

func TestGenericStartPublicReplayCannotReuseTrustedInputs(t *testing.T) {
	f, event := genericStartFixture(t, nil)
	require.NoError(t, NewWorkflowStartOutboxHandler(f.client, f.engine, f.client).Deliver(context.Background(), event))
	var payload workflowStartPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	ctx := WithTrustedBPMNTenantContext(context.Background(), f.tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.actor.ID)
	_, err := f.engine.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), fmt.Sprintf("generic:%d", payload.WorkItemID), "generic", payload.WorkItemID, payload.Variables, payload.DedupeKey)
	require.ErrorContains(t, err, "approval_required")
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(context.Background()))
}
func TestGenericStartRejectsChangedDefinitionConfig(t *testing.T) {
	f, event := genericStartFixture(t, nil)
	f.definition.Update().SetProcessVariables(map[string]interface{}{"approval_required": true, "need_escalate": false}).SaveX(context.Background())
	require.ErrorContains(t, NewWorkflowStartOutboxHandler(f.client, f.engine, f.client).Deliver(context.Background(), event), "approval_required")
	require.Zero(t, f.client.ProcessInstance.Query().CountX(context.Background()))
}
func TestGenericBindingUpdateRejectsOverrideAndTargetChange(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	service := NewProcessBindingService(f.client)
	old, err := service.CreateBinding(context.Background(), &dto.ProcessBinding{BusinessType: "incident", ProcessDefinitionKey: f.definition.Key, TenantID: f.tenant.ID, IsActive: true})
	require.NoError(t, err)
	f.definition = f.definition.Update().SetBpmnXML(genericStartXML()).SaveX(context.Background())
	_, err = service.UpdateBinding(context.Background(), old.ID, &dto.ProcessBinding{TenantID: f.tenant.ID, ProcessDefinitionKey: f.definition.Key})
	require.Error(t, err)
	generic, err := service.CreateBinding(context.Background(), &dto.ProcessBinding{BusinessType: "generic", ProcessDefinitionKey: f.definition.Key, TenantID: f.tenant.ID, IsActive: true})
	require.NoError(t, err)
	for _, key := range []string{"approval_required", "need_escalate", "approvalResult", "workItemCompletionNote"} {
		_, err = service.UpdateBinding(context.Background(), generic.ID, &dto.ProcessBinding{TenantID: f.tenant.ID, Overrides: map[string]interface{}{key: false}})
		require.ErrorContains(t, err, key)
	}
}
func TestLegacyStartAndBindingKeepVariableCompatibility(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	vars := map[string]interface{}{"approval_required": "legacy", "need_escalate": nil, "approvalResult": "approved", "workItemCompletionNote": "legacy"}
	item := f.workItem(t, 111)
	instance, err := f.engine.StartProcess(startProcessContext(f), f.definition.Key, fmt.Sprintf("generic:%d", item.ID), "generic", item.ID, vars)
	require.NoError(t, err)
	require.Equal(t, "legacy", instance.Variables["approval_required"])
	binding, err := NewProcessBindingService(f.client).CreateBinding(context.Background(), &dto.ProcessBinding{BusinessType: "incident", ProcessDefinitionKey: f.definition.Key, TenantID: f.tenant.ID, Overrides: vars})
	require.NoError(t, err)
	require.Equal(t, "incident", string(binding.BusinessType))
}
