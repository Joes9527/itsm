package common

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"itsm-backend/authentication"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type refreshTestRepository struct {
	Repository
	user *User
}

func (r *refreshTestRepository) GetUserByID(_ context.Context, id int) (*User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, errors.New("user not found")
	}
	return r.user, nil
}

type atomicRefreshTokenStore struct {
	mu       sync.Mutex
	consumed map[string]struct{}
	err      error
}

func (s *atomicRefreshTokenStore) Consume(_ context.Context, token string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if _, exists := s.consumed[token]; exists {
		return authentication.ErrRefreshTokenConsumed
	}
	s.consumed[token] = struct{}{}
	return nil
}

func TestServiceRefreshTokenRotatesAndConsumesPreviousToken(t *testing.T) {
	t.Parallel()
	const secret = "common-refresh-rotation-secret"
	store := &atomicRefreshTokenStore{consumed: make(map[string]struct{})}
	consumer := authentication.NewRefreshTokenConsumer(secret, store)
	user := &User{ID: 27, Username: "operator", Role: "admin", TenantID: 5, Active: true}
	svc := NewService(&refreshTestRepository{user: user}, secret, zap.NewNop().Sugar(), nil, consumer)
	original, err := authentication.GenerateRefreshToken(user.ID, secret, time.Hour)
	require.NoError(t, err)

	first, err := svc.RefreshToken(context.Background(), original)
	require.NoError(t, err)
	require.NotEmpty(t, first.AccessToken)
	require.NotEmpty(t, first.RefreshToken)
	require.NotEqual(t, original, first.RefreshToken)

	_, err = svc.RefreshToken(context.Background(), original)
	require.ErrorIs(t, err, authentication.ErrRefreshTokenConsumed)

	second, err := svc.RefreshToken(context.Background(), first.RefreshToken)
	require.NoError(t, err)
	require.NotEqual(t, first.RefreshToken, second.RefreshToken)
}

func TestServiceRefreshTokenFailsClosedWhenConsumerStoreUnavailable(t *testing.T) {
	t.Parallel()
	const secret = "common-refresh-unavailable-secret"
	consumer := authentication.NewRefreshTokenConsumer(secret, nil)
	user := &User{ID: 28, Username: "operator", Role: "admin", TenantID: 5, Active: true}
	svc := NewService(&refreshTestRepository{user: user}, secret, zap.NewNop().Sugar(), nil, consumer)
	token, err := authentication.GenerateRefreshToken(user.ID, secret, time.Hour)
	require.NoError(t, err)

	result, err := svc.RefreshToken(context.Background(), token)
	require.Nil(t, result)
	var unavailable *authentication.RefreshTokenStoreUnavailableError
	require.ErrorAs(t, err, &unavailable)
}
