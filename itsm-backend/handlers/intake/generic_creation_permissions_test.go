package intake

import (
	"context"
	"github.com/stretchr/testify/require"
	creation "itsm-backend/handlers/common/workitemcreation"
	"testing"
)

func TestGenericRequesterCreateAndReplayWithoutWriteGrant(t *testing.T) {
	client, app, identity, command, _, _ := intakeFixture(t)
	ctx := context.Background()
	role := client.Role.Query().OnlyX(ctx)
	client.RolePermission.Delete().ExecX(ctx)
	for _, action := range []string{"read", "create"} {
		grant := client.Permission.Create().SetTenantID(identity.TenantID).SetName(action).SetCode("ticket:" + action).SetResource("ticket").SetAction(action).SaveX(ctx)
		client.RolePermission.Create().SetTenantID(identity.TenantID).SetRoleID(role.ID).SetPermissionID(grant.ID).SaveX(ctx)
	}
	first, err := app.Create(ctx, identity, command)
	require.NoError(t, err)
	require.Positive(t, first.WorkItemID)
	require.False(t, first.Replayed)
	item := client.Ticket.GetX(ctx, first.WorkItemID)
	require.Equal(t, identity.RequesterID, item.RequesterID)
	require.Equal(t, "generic", item.RecordClass)
	replay, err := app.Create(ctx, identity, command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
	client.RolePermission.Delete().ExecX(ctx)
	_, err = app.Create(ctx, identity, command)
	require.ErrorIs(t, err, creation.ErrPermissionDenied)
	require.Equal(t, 1, client.Ticket.Query().CountX(ctx))
	require.Equal(t, 1, client.IntakeRequest.Query().CountX(ctx))
}
