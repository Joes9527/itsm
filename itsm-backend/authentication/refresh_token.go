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

type RefreshTokenIdentity struct {
	UserID   int
	Username string
	Role     string
	TenantID int
}

// ValidatedRefreshToken is an opaque, signed refresh token that may be
// authorized by the application before its one-time consumption.
type ValidatedRefreshToken struct {
	credential *VerifiedCredential
	consumer   *RefreshTokenConsumer
	token      string
	expiresAt  time.Time
	identity   RefreshTokenIdentity
}

func (t *ValidatedRefreshToken) Credential() *VerifiedCredential {
	if t == nil {
		return nil
	}
	return t.credential
}

func (t *ValidatedRefreshToken) Identity() RefreshTokenIdentity {
	if t == nil {
		return RefreshTokenIdentity{}
	}
	return t.identity
}

func NewRefreshTokenConsumer(jwtSecret string, store RefreshTokenStore) *RefreshTokenConsumer {
	return &RefreshTokenConsumer{jwtSecret: jwtSecret, store: store}
}

func (c *RefreshTokenConsumer) Validate(token string) (*ValidatedRefreshToken, error) {
	if c == nil {
		return nil, errors.New("refresh token consumer is not configured")
	}
	claims, err := validateToken(token, c.jwtSecret, "refresh")
	if err != nil {
		return nil, err
	}
	if claims.ExpiresAt == nil {
		return nil, errors.New("refresh token has no expiry")
	}
	if claims.UserID <= 0 || claims.TenantID <= 0 || claims.Username == "" || claims.Role == "" {
		return nil, errors.New("refresh token session identity is incomplete")
	}
	return &ValidatedRefreshToken{
		credential: claims.credential,
		consumer:   c,
		token:      token,
		expiresAt:  claims.ExpiresAt.Time,
		identity: RefreshTokenIdentity{
			UserID: claims.UserID, Username: claims.Username, Role: claims.Role, TenantID: claims.TenantID,
		},
	}, nil
}

func (c *RefreshTokenConsumer) Consume(ctx context.Context, token *ValidatedRefreshToken) error {
	if c == nil || c.store == nil {
		return &RefreshTokenStoreUnavailableError{}
	}
	if token == nil || token.consumer != c || token.token == "" {
		return errors.New("refresh token was not validated by this consumer")
	}
	scoped, err := credentialContext(ctx, token.credential, "refresh")
	if err != nil {
		return err
	}
	return c.store.Consume(scoped, token.token, token.expiresAt)
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
	result, err := s.client.SetArgs(ctx, refreshTokenConsumptionKey(token), "1", redis.SetArgs{Mode: "NX", TTL: ttl}).Result()
	if errors.Is(err, redis.Nil) {
		return ErrRefreshTokenConsumed
	}
	if err != nil {
		return &RefreshTokenStoreUnavailableError{Cause: err}
	}
	if result != "OK" {
		return ErrRefreshTokenConsumed
	}
	return nil
}

func refreshTokenConsumptionKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return refreshTokenConsumptionPrefix + hex.EncodeToString(sum[:])
}
