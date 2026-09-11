package bpmn

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"itsm-backend/ent/enttest"
	"testing"
)

func TestTicketBPMNAssignmentRejectsIncident(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:bpmn-incident-boundary?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("boundary").SetCode("boundary").SetDomain("boundary.test").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetUsername("owner").SetEmail("owner@boundary.test").SetName("Owner").SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).SaveX(ctx)
	item := client.Ticket.Create().SetTitle("Incident").SetDescription("boundary").SetPriority("medium").SetStatus("new").SetRecordClass("incident").SetTicketNumber("INC-B").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SaveX(ctx)
	ctx = context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)
	handler := NewTicketServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	handler.SetNotificationService(&ticketNotificationStub{})
	_, err := handler.assignTicket(ctx, item.ID, map[string]interface{}{"assignee_id": actor.ID})
	require.ErrorContains(t, err, "Incident assignment requires the Incident command")
	after := client.Ticket.GetX(ctx, item.ID)
	require.Equal(t, item.AssigneeID, after.AssigneeID)
	require.Equal(t, item.Status, after.Status)
	require.Equal(t, item.Version, after.Version)
}
