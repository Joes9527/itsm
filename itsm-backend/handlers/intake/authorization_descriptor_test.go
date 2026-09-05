package intake

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	creation "itsm-backend/handlers/common/workitemcreation"
	"testing"
)

func TestCreationAuthorizationCannotOutliveTransaction(t *testing.T) {
	client, _, identity, command, _, _ := intakeFixture(t)
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	descriptor, err := authorization.AuthorizeWorkItemCreation(ctx, tx, tx.Client(), identity, command)
	require.NoError(t, err)
	identity = descriptor.Identity()
	require.NoError(t, descriptor.Validate(tx, identity))
	changed := identity
	changed.ActorID++
	require.ErrorIs(t, descriptor.Validate(tx, changed), creation.ErrPermissionDenied)
	require.NoError(t, tx.Rollback())
	require.ErrorIs(t, descriptor.Validate(tx, identity), creation.ErrPermissionDenied)
}
