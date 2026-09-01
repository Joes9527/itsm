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
	refreshToken, err := GenerateRefreshToken(41, secret, time.Hour)
	require.NoError(t, err)

	_, err = ValidateAccessToken(context.Background(), refreshToken, secret)
	require.Error(t, err)
}
