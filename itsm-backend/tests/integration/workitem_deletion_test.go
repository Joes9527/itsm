package integration

import (
	"context"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	"testing"
)

// Former persistence deletion tests now exercise the sole application owner.
func deletionTestFixture(t *testing.T) (context.Context, *ent.Client, *service.TicketService, workitemmutation.Meta, []int) {
	t.Helper()
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	tenant := client.Tenant.Create().SetName("delete fixture").SetCode(t.Name()).SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("deleter").SetName("deleter").SetPasswordHash("test").SetEmail("delete@example.test").SetRole("operator").SetActive(true).SaveX(ctx)
	role := client.Role.Create().SetTenantID(tenant.ID).SetCode("operator").SetName("operator").SetIsActive(true).SaveX(ctx)
	for _, verb := range []string{"read", "delete"} {
		p := client.Permission.Create().SetTenantID(tenant.ID).SetCode(verb).SetName(verb).SetResource("incident").SetAction(verb).SaveX(ctx)
		client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(p.ID).ExecX(ctx)
	}
	ids := []int{}
	for i := 0; i < 3; i++ {
		item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetRecordClass("incident").SetTitle("Delete fixture").SetStatus("new").SetTicketNumber(fmt.Sprintf("DEL-%d", i)).SaveX(ctx)
		client.Incident.Create().SetWorkItemID(item.ID).SaveX(ctx)
		ids = append(ids, item.ID)
	}
	return ctx, client, service.NewTicketServiceForTest(client, zap.NewNop().Sugar()), workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, Source: "http"}, ids
}

func TestRepository_Delete(t *testing.T) {
	ctx, c, s, m, ids := deletionTestFixture(t)
	before := c.Ticket.GetX(ctx, ids[0]).Version
	require.NoError(t, s.DeleteTicket(ctx, ids[0], m))
	_, err := s.GetTicket(ctx, ids[0], m.TenantID)
	require.Error(t, err)
	after := c.Ticket.GetX(ctx, ids[0])
	require.NotNil(t, after.DeletedAt)
	require.Equal(t, before+1, after.Version)
}
func TestRepository_Delete_WrongTenant(t *testing.T) {
	ctx, c, s, m, ids := deletionTestFixture(t)
	m.TenantID = 99999
	require.Error(t, s.DeleteTicket(ctx, ids[0], m))
	require.Nil(t, c.Ticket.GetX(ctx, ids[0]).DeletedAt)
}
func TestRepository_BatchDelete(t *testing.T) {
	ctx, c, s, m, ids := deletionTestFixture(t)
	before := map[int]int{}
	for _, id := range ids {
		before[id] = c.Ticket.GetX(ctx, id).Version
	}
	require.NoError(t, s.BatchDeleteTickets(ctx, ids, m))
	for _, id := range ids {
		_, err := s.GetTicket(ctx, id, m.TenantID)
		require.Error(t, err)
		require.Equal(t, before[id]+1, c.Ticket.GetX(ctx, id).Version)
	}
}
func TestRepository_BatchDelete_EmptyList(t *testing.T) {
	ctx, _, s, m, _ := deletionTestFixture(t)
	require.Error(t, s.BatchDeleteTickets(ctx, []int{}, m))
}
func TestRepository_BatchDelete_TenantIsolation(t *testing.T) {
	ctx, c, s, m, ids := deletionTestFixture(t)
	m.TenantID = 99999
	require.Error(t, s.BatchDeleteTickets(ctx, ids, m))
	for _, id := range ids {
		require.Nil(t, c.Ticket.GetX(ctx, id).DeletedAt)
	}
}

func TestWorkItemDeletionPreservesTicketPreconditionsAtomically(t *testing.T) {
	for _, state := range []string{"resolved", "closed", "cancelled", "running"} {
		for _, route := range []string{"single", "batch", "subtask"} {
			t.Run(state+"/"+route, func(t *testing.T) {
				ctx, c, s, m, ids := deletionTestFixture(t)
				if state == "running" {
					deployment := c.ProcessDeployment.Create().SetTenantID(m.TenantID).SetDeploymentID("delete-test").SetDeploymentName("Delete test").SaveX(ctx)
					definition := c.ProcessDefinition.Create().SetTenantID(m.TenantID).SetDeploymentID(deployment.ID).SetKey("ticket_test").SetName("test").SetBpmnXML([]byte("<bpmn/>")).SaveX(ctx)
					c.ProcessInstance.Create().SetTenantID(m.TenantID).SetProcessInstanceID("delete-test").SetProcessDefinitionID(definition.ID).SetProcessDefinitionKey(definition.Key).SetBusinessKey(fmt.Sprintf("ticket:%d", ids[1])).SetStatus("running").SaveX(ctx)
				} else {
					c.Ticket.UpdateOneID(ids[1]).SetStatus(state).ExecX(ctx)
				}
				if route == "subtask" {
					c.Ticket.UpdateOneID(ids[1]).SetParentTicketID(ids[0]).ExecX(ctx)
				}
				before := c.Ticket.GetX(ctx, ids[1]).Version
				var err error
				switch route {
				case "single":
					err = s.DeleteTicket(ctx, ids[1], m)
				case "batch":
					err = s.BatchDeleteTickets(ctx, []int{ids[0], ids[1]}, m)
				case "subtask":
					err = s.DeleteSubtask(ctx, ids[0], ids[1], m)
				}
				require.Error(t, err, "existing ticket deletion precondition must survive the owner cutover")
				require.Nil(t, c.Ticket.GetX(ctx, ids[0]).DeletedAt)
				require.Nil(t, c.Ticket.GetX(ctx, ids[1]).DeletedAt)
				require.Equal(t, before, c.Ticket.GetX(ctx, ids[1]).Version)
			})
		}
	}
}
