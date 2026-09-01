package authentication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const refreshTokenConsumptionPrefix = "jwt:refresh:consumed:"

var ErrRefreshTokenConsumed = errors.New("refresh token has already been consumed")

// RefreshTokenStoreUnavailableError means the authoritative one-time token
// store could not establish whether a refresh token was already consumed.
// Callers must fail closed when this error is returned.
type RefreshTokenStoreUnavailableError struct {
	Cause error
}

func (e *RefreshTokenStoreUnavailableError) Error() string {
	if e == nil || e.Cause == nil {
		return "refresh token store unavailable"
	}
	return fmt.Sprintf("refresh token store unavailable: %v", e.Cause)
}

func (e *RefreshTokenStoreUnavailableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// RefreshTokenStore atomically records the first use of a refresh token.
// Implementations return ErrRefreshTokenConsumed when a token was recorded by
// an earlier request and RefreshTokenStoreUnavailableError when they cannot
// make that determination.
type RefreshTokenStore interface {
	Consume(ctx context.Context, token string, expiresAt time.Time) error
}

// RefreshTokenConsumer is the sole authority for validating and consuming a
// refresh token. A refresh succeeds only after its token is atomically marked
// as consumed.
type RefreshTokenConsumer struct {
	jwtSecret string
	store     RefreshTokenStore
}

func NewRefreshTokenConsumer(jwtSecret string, store RefreshTokenStore) *RefreshTokenConsumer {
	return &RefreshTokenConsumer{jwtSecret: jwtSecret, store: store}
}

func (c *RefreshTokenConsumer) Consume(ctx context.Context, token string) (*Claims, error) {
	if c == nil || c.store == nil {
		return nil, &RefreshTokenStoreUnavailableError{}
	}
	claims, err := validateToken(token, c.jwtSecret, "refresh")
	if err != nil {
		return nil, err
	}
	if claims.ExpiresAt == nil {
		return nil, errors.New("refresh token has no expiry")
	}
	if err := c.store.Consume(ctx, token, claims.ExpiresAt.Time); err != nil {
		return nil, err
	}
	return claims, nil
}

type redisRefreshTokenStore struct {
	client *redis.Client
}

func NewRedisRefreshTokenStore(client *redis.Client) RefreshTokenStore {
	return &redisRefreshTokenStore{client: client}
}

func (s *redisRefreshTokenStore) Consume(ctx context.Context, token string, expiresAt time.Time) error {
	if s == nil || s.client == nil {
		return &RefreshTokenStoreUnavailableError{}
	}
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return ErrRefreshTokenConsumed
	}
	consumed, err := s.client.SetNX(ctx, refreshTokenConsumptionKey(token), "1", ttl).Result()
	if err != nil {
		return &RefreshTokenStoreUnavailableError{Cause: err}
	}
	if !consumed {
		return ErrRefreshTokenConsumed
	}
	return nil
}

func refreshTokenConsumptionKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return refreshTokenConsumptionPrefix + hex.EncodeToString(sum[:])
}
