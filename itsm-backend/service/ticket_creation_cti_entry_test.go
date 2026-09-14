package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func TestTicketCreationCTIEntry(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket-cti-entry?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant := createNamedTestTenant(t, ctx, client, "ticket-cti")
	actor := createNamedTestUser(t, ctx, client, tenant.ID, "ticket-cti")
	svc := NewTicketServiceForTest(client, zaptest.NewLogger(t).Sugar())
	root := client.TicketCategory.Create().SetName("Duplicate").SetCode("root").SetTenantID(tenant.ID).SaveX(ctx)
	child := client.TicketCategory.Create().SetName("Duplicate").SetCode("child").SetTenantID(tenant.ID).SetParentID(root.ID).SaveX(ctx)
	req := &dto.CreateTicketRequest{Title: "Ticket CTI", Description: "classification", Priority: "medium", RequesterID: actor.ID, CTI: &creation.CTIInput{CategoryID: &root.ID, TypeID: &child.ID}}
	item, err := svc.SubmitCreation(ctx, req, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, item.CategoryID)
	require.Equal(t, child.ID, *item.CategoryID)
	for _, scenario := range []string{"inactive", "foreign", "hierarchy", "missing-parent"} {
		child.Update().SetIsActive(true).SetTenantID(tenant.ID).SetParentID(root.ID).SaveX(ctx)
		req.CTI.CategoryID = &root.ID
		switch scenario {
		case "inactive":
			child.Update().SetIsActive(false).SaveX(ctx)
		case "foreign":
			child.Update().SetTenantID(tenant.ID + 999).SaveX(ctx)
		case "hierarchy":
			child.Update().ClearParentID().SaveX(ctx)
		case "missing-parent":
			req.CTI.CategoryID = nil
		}
		_, err = svc.SubmitCreation(ctx, req, tenant.ID)
		require.Error(t, err, scenario)
		require.Equal(t, 1, client.Ticket.Query().CountX(ctx))
	}
}
