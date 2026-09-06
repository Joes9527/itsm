package intake

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"testing"
)

func TestIdentityRepositoryExactMappingAndCurrentUserRole(t *testing.T) {
	client, _, i, _, _, _ := intakeFixture(t)
	ctx := tenantctx.WithTenantID(context.Background(), i.TenantID)
	_, a, _ := assertionFixture()
	m := client.ExternalIdentity.Create().SetTenantID(i.TenantID).SetUserID(i.ActorID).SetProvider(a.Provider).SetWorkspace(a.Workspace).SetSubject(a.Subject).SaveX(ctx)
	repo := NewIdentityRepository(client, client, authorization.NewSessionReader(client, sameTransactionDirectory{}))
	_, resolved, err := repo.Resolve(ctx, a.Provider, a.Workspace, a.Subject)
	require.NoError(t, err)
	require.Equal(t, i.ActorID, resolved.ActorID)
	for _, parts := range [][]string{{"other", a.Workspace, a.Subject}, {a.Provider, "other", a.Subject}, {a.Provider, a.Workspace, "u@example.test"}, {a.Provider, a.Workspace, "forged-subject"}} {
		_, _, err = repo.Resolve(ctx, parts[0], parts[1], parts[2])
		require.ErrorIs(t, err, creation.ErrAuthenticationRequired)
	}
	c := &authentication.IntakeClaims{UserID: i.ActorID, TenantID: i.TenantID, Role: i.Role, Provider: a.Provider, MappingID: m.ID, MappingVersion: 1}
	for _, mutate := range []func(*authentication.IntakeClaims){func(c *authentication.IntakeClaims) { c.UserID++ }, func(c *authentication.IntakeClaims) { c.TenantID++ }, func(c *authentication.IntakeClaims) { c.Role = "admin" }, func(c *authentication.IntakeClaims) { c.Provider = "other" }, func(c *authentication.IntakeClaims) { c.MappingVersion++ }} {
		changed := *c
		mutate(&changed)
		_, err = repo.Validate(tenantctx.WithTenantID(ctx, changed.TenantID), &changed)
		require.Error(t, err)
	}
	client.User.UpdateOneID(i.ActorID).SetActive(false).SaveX(ctx)
	_, err = repo.Validate(ctx, c)
	require.Error(t, err)
}
func TestIdentityRepositoryStorageFailureIsNotUnmapped(t *testing.T) {
	client, _, _, _, _, _ := intakeFixture(t)
	client.ExternalIdentity.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) { return nil, errors.New("storage unavailable") })
	}))
	repo := NewIdentityRepository(client, client, authorization.NewSessionReader(client, sameTransactionDirectory{}))
	_, _, err := repo.Resolve(context.Background(), "kaf", "workspace", "subject")
	require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
}
func TestIdentityMappingAuditFailureRollsBackMutation(t *testing.T) {
	client, _, i, _, _, _ := intakeFixture(t)
	ctx := tenantctx.WithTenantID(context.Background(), i.TenantID)
	cfg, a, _ := assertionFixture()
	s := NewIdentityMappingService(authorization.NewSessionReader(client, sameTransactionDirectory{}), cfg.config.Providers)
	client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, errors.New("audit unavailable") })
	})
	_, err := s.Create(ctx, i, CreateIdentityMapping{Provider: a.Provider, Workspace: a.Workspace, Subject: a.Subject, UserID: i.ActorID})
	require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
	require.Zero(t, client.ExternalIdentity.Query().CountX(ctx))
}
