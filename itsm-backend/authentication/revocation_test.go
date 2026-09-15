package authentication

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccessTokenRevocationKeyDoesNotExposeToken(t *testing.T) {
	const token = "header.payload.signature"
	key := accessTokenRevocationKey(token)
	require.NotContains(t, key, token)
	require.Contains(t, key, accessTokenRevocationPrefix)
}

func TestMemoryAccessTokenRevocationStore(t *testing.T) {
	store := newMemoryAccessTokenRevocationStore()
	const token = "one-time-access-token"
	require.NoError(t, store.Revoke(context.Background(), token, time.Now().Add(time.Hour)))
	revoked, err := store.IsRevoked(context.Background(), token)
	require.NoError(t, err)
	require.True(t, revoked)
}
