package service

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
)

func TestTriggerRejectsReservedIdentityVariables(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := WithTrustedBPMNTenantContext(context.Background(), f.tenant.ID)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetRecordClass("generic").SetTitle("trigger identity").SetTicketNumber("TKT-TRIGGER-RESERVED").SaveX(ctx)
	trigger := NewProcessTriggerService(f.client, f.engine)
	request := func(vars map[string]any) *dto.ProcessTriggerRequest {
		return &dto.ProcessTriggerRequest{BusinessType: dto.BusinessTypeGeneric, BusinessID: item.ID, TenantID: f.tenant.ID, TriggeredBy: strconv.Itoa(f.actor.ID), ProcessDefinitionKey: f.definition.Key, Variables: vars}
	}
	for key := range reservedBPMNParticipantVariableKeys {
		t.Run(key, func(t *testing.T) {
			_, err := trigger.TriggerProcess(ctx, request(map[string]any{key: 999}))
			require.Error(t, err)
			require.Zero(t, f.client.ProcessInstance.Query().CountX(ctx))
		})
	}
	_, err := trigger.TriggerProcess(ctx, request(map[string]any{" Business_Type ": "ticket"}))
	require.Error(t, err)
	require.Zero(t, f.client.ProcessInstance.Query().CountX(ctx))
	result, err := trigger.TriggerProcess(ctx, request(map[string]any{"quantity": 2}))
	require.NoError(t, err)
	instance := f.client.ProcessInstance.GetX(ctx, result.ProcessInstanceID)
	require.Equal(t, "generic", instance.BusinessType)
	require.Equal(t, "generic", instance.Variables["business_type"])
	require.EqualValues(t, item.ID, instance.BusinessID)
}
