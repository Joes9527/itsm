package service

import (
	"context"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestTriggerProcess_PopulatesStructuredBusinessIdentity(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:trigger_business_identity?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Trigger Identity Tenant").
		SetCode("trigger-identity").
		SetDomain("trigger-identity.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	logger := zaptest.NewLogger(t).Sugar()
	engine := NewCustomProcessEngine(client, logger)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	tenantCtx = WithTrustedBPMNTenantContext(tenantCtx, tenant.ID)

	deploySvc := NewBPMNTemplateService(client)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := NewProcessTriggerService(client, engine)
	resp, err := trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType:         dto.BusinessTypeChange,
		BusinessID:           42,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": false},
		TenantID:             tenant.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "change:42", resp.BusinessKey)

	// dto.ProcessTriggerResponse.ProcessInstanceID is the ent row's integer primary
	// key (instance.ID), not the string BPMN engine id (instance.ProcessInstanceID) —
	// confirmed against the response construction in TriggerProcess itself
	// (service/bpmn_process_trigger_service.go: "ProcessInstanceID: instance.ID").
	instance, err := client.ProcessInstance.Get(ctx, resp.ProcessInstanceID)
	require.NoError(t, err)
	require.Equal(t, "change", instance.BusinessType)
	require.Equal(t, 42, instance.BusinessID)
}
