package intake

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRedisNonceStoreClaimIsAtomicAndExpires(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	store := &RedisNonceStore{client: client}
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := store.Claim(context.Background(), "test-submission", time.Minute)
			if err != nil {
				t.Errorf("claim failed: %v", err)
				return
			}
			if ok {
				claimed.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, claimed.Load())
	require.Len(t, server.Keys(), 1)
	require.NotContains(t, server.Keys()[0], "test-submission")
	server.FastForward(time.Minute)
	ok, err := store.Claim(context.Background(), "test-submission", time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	server.Close()
	ok, err = store.Claim(context.Background(), "another-submission", time.Minute)
	require.Error(t, err)
	require.False(t, ok)
}
