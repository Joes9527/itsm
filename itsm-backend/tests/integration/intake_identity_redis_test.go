package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"itsm-backend/handlers/intake"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIdentityRedisNonceAtomicReplayAndUnavailable(t *testing.T) {
	address := os.Getenv("INTAKE_REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("explicit owned Redis required")
	}
	require.Equal(t, "127.0.0.1:36445", address)
	client := redis.NewClient(&redis.Options{Addr: address, DB: 0, DialTimeout: time.Second})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())
	key := uuid.NewString()
	sum := sha256.Sum256([]byte(key))
	storedKey := "intake:identity-exchange:nonce:" + hex.EncodeToString(sum[:])
	defer client.Del(context.Background(), storedKey)
	n := intake.NewRedisNonceStore(client)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for index := 0; index < 20; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := n.Claim(ctx, key, 2*time.Second)
			require.NoError(t, err)
			if ok {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), winners.Load())
	ttl, err := client.PTTL(ctx, storedKey).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Duration(0))
	require.LessOrEqual(t, ttl, 2*time.Second)
	unavailable := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DB: 0, DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer unavailable.Close()
	_, err = intake.NewRedisNonceStore(unavailable).Claim(ctx, uuid.NewString(), time.Second)
	require.Error(t, err)
}
