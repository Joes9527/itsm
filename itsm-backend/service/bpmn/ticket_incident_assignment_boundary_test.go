package bpmn

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	"testing"
)

func TestTicketBPMNAssignmentRejectsProfessionalClasses(t *testing.T) {
	for _, class := range []string{"incident", "problem", "change_request"} {
		for _, action := range []string{"assign", "escalate", "status", "same_status"} {
			t.Run(class+"/"+action, func(t *testing.T) {
				client := enttest.Open(t, "sqlite3", "file:bpmn-incident-boundary?mode=memory&cache=shared&_fk=1")
				defer client.Close()
				ctx := context.Background()
				tenant := client.Tenant.Create().SetName("boundary").SetCode("boundary").SetDomain("boundary.test").SetStatus("active").SaveX(ctx)
				actor := client.User.Create().SetUsername("owner").SetEmail("owner@boundary.test").SetName("Owner").SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).SaveX(ctx)
				item := client.Ticket.Create().SetTitle("Incident").SetDescription("boundary").SetPriority("medium").SetStatus("new").SetRecordClass(class).SetTicketNumber("INC-B").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SaveX(ctx)
				ctx = context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)
				handler := NewTicketServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
				spy := &professionalBoundaryNotificationSpy{}
				handler.SetNotificationService(spy)
				handler.SetTicketService(&ticketStatusServiceEntStub{client: client})
				var err error
				if action == "assign" {
					_, err = handler.assignTicket(ctx, item.ID, map[string]interface{}{"assignee_id": actor.ID})
				} else if action == "status" || action == "same_status" {
					status := "in_progress"
					if action == "same_status" {
						status = item.Status
					}
					_, err = handler.updateTicketStatus(ctx, item.ID, map[string]interface{}{"new_status": status})
				} else {
					_, err = handler.escalateTicket(ctx, item.ID, map[string]interface{}{"escalate_to": "high", "notify_admin_ids": []int{actor.ID}})
				}
				if action == "escalate" {
					require.ErrorContains(t, err, "workflow escalation service unavailable")
				} else {
					require.ErrorContains(t, err, "owning domain command")
				}
				after := client.Ticket.GetX(ctx, item.ID)
				require.Equal(t, item.AssigneeID, after.AssigneeID)
				require.Equal(t, item.Status, after.Status)
				require.Equal(t, item.Version, after.Version)
				require.Equal(t, item.Priority, after.Priority)
				require.Zero(t, spy.calls)
			})
		}
	}
}

type professionalBoundaryNotificationSpy struct{ calls int }

func (s *professionalBoundaryNotificationSpy) SendNotification(context.Context, int, *dto.SendTicketNotificationRequest, int) (*dto.SendTicketNotificationResult, error) {
	s.calls++
	return &dto.SendTicketNotificationResult{Effect: dto.TicketNotificationEffectApplied, AppliedCount: 1, DeliveryCount: 1}, nil
}
