package intake

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"testing"
	"time"
)

func TestMSPIntakeUsesNativeActorAndCustomerRequester(t *testing.T) {
	client, app, identity, command, _, _ := intakeFixture(t)
	ctx := context.Background()
	provider := client.Tenant.Create().SetName("Provider").SetCode("provider").SetType("msp_provider").SaveX(ctx)
	client.Tenant.UpdateOneID(identity.TenantID).SetType("msp_customer").ExecX(ctx)
	actor := client.User.Create().SetTenantID(provider.ID).SetUsername("operator").SetName("Operator").SetEmail("operator@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(ctx)
	allocation := client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(identity.TenantID).SetRole("primary").SaveX(ctx)
	seedCreationPermission(t, client, identity.TenantID, "msp_tech")
	identity.ActorID, identity.Role = actor.ID, "msp_tech"
	result, err := app.Create(ctx, identity, command)
	require.NoError(t, err)
	item := client.Ticket.GetX(ctx, result.WorkItemID)
	require.Equal(t, actor.ID, item.OpenedByID)
	require.Equal(t, identity.RequesterID, item.RequesterID)
	replay, err := app.Create(ctx, identity, command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).ExecX(ctx)
	_, err = app.Create(ctx, identity, command)
	require.ErrorIs(t, err, creation.ErrPermissionDenied)
	require.Equal(t, 1, client.IntakeRequest.Query().CountX(ctx))
}

func TestMSPIntakeCurrentAuthorizationDenialMatrix(t *testing.T) {
	for _, scenario := range []string{"missing_directory", "self_requester", "foreign_requester", "inactive_requester", "inactive_actor", "missing_actor", "inactive_target", "expired_target", "unallocated", "deassigned", "role_changed", "missing_role", "inactive_role", "missing_permission", "no_create_on_behalf"} {
		t.Run(scenario, func(t *testing.T) {
			client, app, identity, command, _, _ := intakeFixture(t)
			ctx := context.Background()
			provider := client.Tenant.Create().SetCode("provider").SetName("Provider").SetType("msp_provider").SaveX(ctx)
			client.Tenant.UpdateOneID(identity.TenantID).SetType("msp_customer").ExecX(ctx)
			actor := client.User.Create().SetTenantID(provider.ID).SetUsername("operator").SetName("Operator").SetEmail("operator@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(ctx)
			allocation := client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(identity.TenantID).SetRole("primary").SaveX(ctx)
			role := client.Role.Create().SetTenantID(identity.TenantID).SetCode("msp_tech").SetName("MSP").SaveX(ctx)
			for _, action := range []string{"read", "write", "create_on_behalf"} {
				if scenario == "no_create_on_behalf" && action == "create_on_behalf" {
					continue
				}
				p := client.Permission.Create().SetTenantID(identity.TenantID).SetCode("msp:" + action).SetName(action).SetResource("ticket").SetAction(action).SaveX(ctx)
				client.RolePermission.Create().SetTenantID(identity.TenantID).SetRoleID(role.ID).SetPermissionID(p.ID).SaveX(ctx)
			}
			identity.ActorID, identity.Role = actor.ID, "msp_tech"
			switch scenario {
			case "missing_directory":
				app.directory = nil
			case "self_requester":
				identity.RequesterID = actor.ID
			case "foreign_requester":
				client.User.UpdateOneID(identity.RequesterID).SetTenantID(provider.ID).ExecX(ctx)
			case "inactive_requester":
				client.User.UpdateOneID(identity.RequesterID).SetActive(false).ExecX(ctx)
			case "inactive_actor":
				client.User.UpdateOneID(actor.ID).SetActive(false).ExecX(ctx)
			case "missing_actor":
				client.MSPAllocation.DeleteOneID(allocation.ID).ExecX(ctx)
				client.User.DeleteOneID(actor.ID).ExecX(ctx)
			case "inactive_target":
				client.Tenant.UpdateOneID(identity.TenantID).SetStatus("suspended").ExecX(ctx)
			case "expired_target":
				client.Tenant.UpdateOneID(identity.TenantID).SetExpiresAt(time.Now().Add(-time.Hour)).ExecX(ctx)
			case "unallocated":
				client.MSPAllocation.DeleteOneID(allocation.ID).ExecX(ctx)
			case "deassigned":
				client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).ExecX(ctx)
			case "role_changed":
				client.User.UpdateOneID(actor.ID).SetMspRole("provider_admin").ExecX(ctx)
			case "missing_role":
				client.RolePermission.Delete().ExecX(ctx)
				client.Role.DeleteOneID(role.ID).ExecX(ctx)
			case "inactive_role":
				client.Role.UpdateOneID(role.ID).SetIsActive(false).ExecX(ctx)
			case "missing_permission":
				client.RolePermission.Delete().ExecX(ctx)
			}
			_, err := app.Create(ctx, identity, command)
			require.Error(t, err)
			if scenario == "self_requester" {
				require.ErrorIs(t, err, creation.ErrInvalidCommand)
			}
			assertNoIntakeGraph(t, client)
		})
	}
}

func TestMSPReceiptRejectsChangedNativeProvenance(t *testing.T) {
	client, app, identity, command, _, _ := intakeFixture(t)
	ctx := context.Background()
	provider := client.Tenant.Create().SetCode("provider").SetName("Provider").SetType("msp_provider").SaveX(ctx)
	next := client.Tenant.Create().SetCode("next").SetName("Next").SetType("msp_provider").SaveX(ctx)
	client.Tenant.UpdateOneID(identity.TenantID).SetType("msp_customer").ExecX(ctx)
	actor := client.User.Create().SetTenantID(provider.ID).SetUsername("operator").SetName("Operator").SetEmail("operator@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(ctx)
	client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(identity.TenantID).SetRole("primary").SaveX(ctx)
	seedCreationPermission(t, client, identity.TenantID, "msp_tech")
	identity.ActorID, identity.Role = actor.ID, "msp_tech"
	_, err := app.Create(ctx, identity, command)
	require.NoError(t, err)
	client.User.UpdateOneID(actor.ID).SetTenantID(next.ID).ExecX(ctx)
	_, err = app.Create(ctx, identity, command)
	require.ErrorIs(t, err, creation.ErrIdempotencyConflict)
	require.Equal(t, provider.ID, client.IntakeRequest.Query().OnlyX(ctx).ActorTenantID)
}

type failingCloseDirectory struct{ closes *int }

func (p failingCloseDirectory) Open(_ context.Context, tx *ent.Tx, _ int) (*ent.Client, func() error, error) {
	return tx.Client(), func() error { *p.closes++; return errors.New("directory close unavailable") }, nil
}
func TestDirectoryCloseFailurePreventsReceiptClaim(t *testing.T) {
	client, app, identity, command, _, _ := intakeFixture(t)
	closes := 0
	app.directory = failingCloseDirectory{&closes}
	_, err := app.Create(context.Background(), identity, command)
	require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
	require.Equal(t, 1, closes)
	assertNoIntakeGraph(t, client)
}
