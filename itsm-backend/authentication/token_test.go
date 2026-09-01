package authentication

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateAccessTokenRejectsRevokedToken(t *testing.T) {
	ctx := context.Background()
	const secret = "authoritative-access-validation-secret"
	token, err := GenerateAccessToken(41, "operator", "admin", 7, secret, time.Hour)
	require.NoError(t, err)

	claims, err := ValidateAccessToken(ctx, token, secret)
	require.NoError(t, err)
	require.Equal(t, 41, claims.UserID)
	require.NotNil(t, claims.ExpiresAt)

	require.NoError(t, RevokeAccessToken(ctx, token, claims.ExpiresAt.Time))
	_, err = ValidateAccessToken(ctx, token, secret)
	require.ErrorIs(t, err, ErrAccessTokenRevoked)
}

func TestValidateAccessTokenRejectsRefreshToken(t *testing.T) {
	const secret = "authoritative-token-type-secret"
	refreshToken, err := GenerateRefreshToken(41, "operator", "admin", 7, secret, time.Hour)
	require.NoError(t, err)

	_, err = ValidateAccessToken(context.Background(), refreshToken, secret)
	require.Error(t, err)
}

func TestIssueSessionTokensUsesCanonicalTTLAndTenantIdentity(t *testing.T) {
	before := time.Now()
	tokens, err := IssueSessionTokens(SessionIdentity{UserID: 51, Username: "session-user", Role: "manager", TenantID: 9}, "session-secret")
	require.NoError(t, err)

	access, err := validateToken(tokens.AccessToken, "session-secret", "access")
	require.NoError(t, err)
	refresh, err := validateToken(tokens.RefreshToken, "session-secret", "refresh")
	require.NoError(t, err)
	for _, claims := range []*Claims{access, refresh} {
		require.Equal(t, 51, claims.UserID)
		require.Equal(t, 9, claims.TenantID)
		require.Equal(t, "manager", claims.Role)
	}
	require.WithinDuration(t, before.Add(AccessTokenTTL), access.ExpiresAt.Time, 2*time.Second)
	require.WithinDuration(t, before.Add(RefreshTokenTTL), refresh.ExpiresAt.Time, 2*time.Second)
}
