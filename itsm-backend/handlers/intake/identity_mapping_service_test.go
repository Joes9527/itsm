package intake

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strings"
	"testing"
)

func TestIdentityMappingPermissionCASAuditAndRevocation(t *testing.T) {
	client, _, i, _, _, _ := intakeFixture(t)
	ctx := tenantctx.WithTenantID(context.Background(), i.TenantID)
	cfg, a, _ := assertionFixture()
	s := NewIdentityMappingService(authorization.NewSessionReader(client, sameTransactionDirectory{}), cfg.config.Providers)
	input := CreateIdentityMapping{Provider: a.Provider, Workspace: a.Workspace, Subject: a.Subject, UserID: i.ActorID}
	m, err := s.Create(ctx, i, input)
	require.NoError(t, err)
	require.Equal(t, 1, m.Version)
	require.True(t, m.Active)
	_, err = s.Create(ctx, i, input)
	require.ErrorIs(t, err, creation.ErrIdempotencyConflict)
	m, err = s.Update(ctx, i, m.ID, 1, false)
	require.NoError(t, err)
	require.Equal(t, 2, m.Version)
	require.False(t, m.Active)
	_, err = s.Update(ctx, i, m.ID, 1, true)
	require.ErrorIs(t, err, creation.ErrIdempotencyConflict)
	logs := client.AuditLog.Query().AllX(ctx)
	require.Len(t, logs, 2)
	for _, log := range logs {
		require.False(t, strings.Contains(*log.RequestBody, a.Subject))
		require.False(t, strings.Contains(*log.RequestBody, a.Workspace))
	}
	client.RolePermission.Delete().ExecX(ctx)
	_, err = s.Update(ctx, i, m.ID, 2, true)
	require.ErrorIs(t, err, creation.ErrPermissionDenied)
}
