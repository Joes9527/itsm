package authentication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const accessTokenRevocationPrefix = "jwt:revoked:"

type accessTokenRevocationStore interface {
	IsRevoked(context.Context, string) (bool, error)
	Revoke(context.Context, string, time.Time) error
}

var (
	revocationStoreMu sync.RWMutex
	revocationStore   accessTokenRevocationStore = newMemoryAccessTokenRevocationStore()
)

func ConfigureAccessTokenRevocationRedis(client *redis.Client) {
	if client != nil {
		setAccessTokenRevocationStore(&redisAccessTokenRevocationStore{client: client})
	}
}

func setAccessTokenRevocationStore(store accessTokenRevocationStore) {
	revocationStoreMu.Lock()
	defer revocationStoreMu.Unlock()
	revocationStore = store
}

func currentAccessTokenRevocationStore() accessTokenRevocationStore {
	revocationStoreMu.RLock()
	defer revocationStoreMu.RUnlock()
	return revocationStore
}

func RevokeAccessToken(ctx context.Context, token string, expiresAt time.Time) error {
	if err := currentAccessTokenRevocationStore().Revoke(ctx, token, expiresAt); err != nil {
		return fmt.Errorf("%w: %v", ErrAccessTokenRevocationCheck, err)
	}
	return nil
}

func IsAccessTokenRevoked(ctx context.Context, token string) (bool, error) {
	return currentAccessTokenRevocationStore().IsRevoked(ctx, token)
}

func accessTokenRevocationKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return accessTokenRevocationPrefix + hex.EncodeToString(sum[:])
}

type redisAccessTokenRevocationStore struct{ client *redis.Client }

func (s *redisAccessTokenRevocationStore) IsRevoked(ctx context.Context, token string) (bool, error) {
	count, err := s.client.Exists(ctx, accessTokenRevocationKey(token)).Result()
	return count > 0, err
}

func (s *redisAccessTokenRevocationStore) Revoke(ctx context.Context, token string, expiresAt time.Time) error {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil
	}
	return s.client.Set(ctx, accessTokenRevocationKey(token), "1", ttl).Err()
}

type memoryAccessTokenRevocationStore struct {
	mu      sync.Mutex
	expires map[string]time.Time
}

func newMemoryAccessTokenRevocationStore() *memoryAccessTokenRevocationStore {
	return &memoryAccessTokenRevocationStore{expires: make(map[string]time.Time)}
}

func (s *memoryAccessTokenRevocationStore) IsRevoked(_ context.Context, token string) (bool, error) {
	key := accessTokenRevocationKey(token)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt, ok := s.expires[key]
	if ok && !now.Before(expiresAt) {
		delete(s.expires, key)
		return false, nil
	}
	return ok, nil
}

func (s *memoryAccessTokenRevocationStore) Revoke(_ context.Context, token string, expiresAt time.Time) error {
	if token == "" || !time.Now().Before(expiresAt) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expires[accessTokenRevocationKey(token)] = expiresAt
	return nil
}
