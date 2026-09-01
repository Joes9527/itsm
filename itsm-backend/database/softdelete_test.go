package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestSoftDeleteInterceptorScopesChangeThroughWorkItem(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:change-soft-delete-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Change soft-delete tenant").
		SetCode("change-soft-delete").
		SetDomain("change-soft-delete.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().
		SetUsername("change-soft-delete-user").
		SetEmail("change-soft-delete@example.com").
		SetName("Change soft-delete user").
		SetPasswordHash("hash").
		SetRole("agent").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	workItem, err := client.Ticket.Create().
		SetTitle("Soft-deleted change").
		SetTicketNumber("CHG-SOFT-DELETE").
		SetType("change").
		SetRecordClass("change_request").
		SetRequesterID(user.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Change.Create().
		SetType("normal").
		SetRiskLevel("medium").
		SetImpactScope("low").
		SetWorkItemID(workItem.ID).
		Save(ctx)
	require.NoError(t, err)

	RegisterSoftDeleteInterceptors(client)
	require.Equal(t, 1, client.Change.Query().CountX(ctx))
	_, err = client.Ticket.UpdateOneID(workItem.ID).SetDeletedAt(time.Now()).Save(ctx)
	require.NoError(t, err)
	require.Zero(t, client.Change.Query().CountX(ctx))
}
