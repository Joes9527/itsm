package authentication

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"testing"
	"time"
)

type credentialScopeProbe struct {
	calls, tenant int
	bypass        bool
}

func (s *credentialScopeProbe) IsRevoked(ctx context.Context, _ string) (bool, error) {
	s.calls++
	s.tenant, _ = tenantctx.TenantID(ctx)
	s.bypass = tenantctx.IsSystemBypass(ctx)
	return false, nil
}
func (*credentialScopeProbe) Revoke(context.Context, string, time.Time) error { return nil }

func TestAccessValidationDerivesTenantFromVerifiedClaims(t *testing.T) {
	const secret = "private-verified-scope-secret"
	raw, err := GenerateAccessToken(7, "operator", "agent", 3, secret, time.Hour)
	require.NoError(t, err)
	original := currentAccessTokenRevocationStore()
	t.Cleanup(func() { setAccessTokenRevocationStore(original) })
	for _, mode := range []string{"empty", "matching", "conflict", "system-bypass"} {
		t.Run(mode, func(t *testing.T) {
			probe := &credentialScopeProbe{}
			setAccessTokenRevocationStore(probe)
			ctx := context.Background()
			switch mode {
			case "matching":
				ctx = tenantctx.WithTenantID(ctx, 3)
			case "conflict":
				ctx = tenantctx.WithTenantID(ctx, 4)
			case "system-bypass":
				ctx = tenantctx.WithSystemBypass(ctx)
			}
			_, err := ValidateAccessToken(ctx, raw, secret)
			if mode == "conflict" {
				require.Error(t, err)
				require.Zero(t, probe.calls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, probe.calls)
			require.Equal(t, 3, probe.tenant)
			require.False(t, probe.bypass)
		})
	}
}

func TestVerifiedCredentialCannotBeCreatedFromUnverifiedClaims(t *testing.T) {
	require.Nil(t, (&Claims{UserID: 7, TenantID: 3}).Credential())
	require.Nil(t, (*Claims)(nil).Credential())
	for _, credential := range []*VerifiedCredential{nil, {}} {
		_, err := credentialContext(context.Background(), credential, "access")
		require.Error(t, err)
	}
	raw, err := GenerateAccessToken(7, "operator", "agent", 3, "private-immutable-identity", time.Hour)
	require.NoError(t, err)
	claims, err := validateToken(raw, "private-immutable-identity", "access")
	require.NoError(t, err)
	identity := claims.Credential()
	require.NotNil(t, identity)
	require.NotContains(t, identity.digest, raw)
	require.Len(t, identity.digest, 64)
	expires := identity.expiresAt
	claims.UserID, claims.TenantID = 99, 99
	claims.ExpiresAt.Time = time.Now().Add(24 * time.Hour)
	scoped, err := credentialContext(context.Background(), identity, "access")
	require.NoError(t, err)
	require.Equal(t, 3, tenantctx.MustTenantID(scoped))
	require.Equal(t, 7, identity.actorID)
	require.Equal(t, expires, identity.expiresAt)
	_, err = credentialContext(context.Background(), identity, "refresh")
	require.Error(t, err)
	identity.expiresAt = time.Now().Add(-time.Second)
	_, err = credentialContext(context.Background(), identity, "access")
	require.Error(t, err)
}

type refreshScopeProbe struct {
	calls, tenant int
	bypass        bool
}

func (s *refreshScopeProbe) Consume(ctx context.Context, _ string, _ time.Time) error {
	s.calls++
	s.tenant, _ = tenantctx.TenantID(ctx)
	s.bypass = tenantctx.IsSystemBypass(ctx)
	return nil
}
func TestRefreshConsumptionUsesVerifiedTenantAndPurpose(t *testing.T) {
	const secret = "private-refresh-context"
	raw, err := GenerateRefreshToken(7, "operator", "agent", 3, secret, time.Hour)
	require.NoError(t, err)
	probe := &refreshScopeProbe{}
	consumer := NewRefreshTokenConsumer(secret, probe)
	verified, err := consumer.Validate(raw)
	require.NoError(t, err)
	require.Error(t, consumer.Consume(tenantctx.WithTenantID(context.Background(), 4), verified))
	require.Zero(t, probe.calls)
	require.NoError(t, consumer.Consume(tenantctx.WithSystemBypass(context.Background()), verified))
	require.Equal(t, 3, probe.tenant)
	require.False(t, probe.bypass)
	verified.credential.expiresAt = time.Now().Add(-time.Second)
	require.Error(t, consumer.Consume(context.Background(), verified))
	require.Equal(t, 1, probe.calls)
}
