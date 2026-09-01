package authentication

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const refreshTestSecret = "refresh-token-consumer-test-secret"

func TestRedisRefreshTokenConsumerConsumesTokenExactlyOnce(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	consumer := NewRefreshTokenConsumer(refreshTestSecret, NewRedisRefreshTokenStore(client))
	token, err := GenerateRefreshToken(73, "operator", "admin", 8, refreshTestSecret, time.Hour)
	require.NoError(t, err)

	validated, err := consumer.Validate(token)
	require.NoError(t, err)
	identity := validated.Identity()
	require.Equal(t, 73, identity.UserID)
	require.Equal(t, "operator", identity.Username)
	require.Equal(t, "admin", identity.Role)
	require.Equal(t, 8, identity.TenantID)
	require.NoError(t, consumer.Consume(context.Background(), validated))

	validated, err = consumer.Validate(token)
	require.NoError(t, err)
	err = consumer.Consume(context.Background(), validated)
	require.ErrorIs(t, err, ErrRefreshTokenConsumed)

	keys := server.Keys()
	require.Len(t, keys, 1)
	require.Equal(t, refreshTokenConsumptionKey(token), keys[0])
	require.NotContains(t, keys[0], token)
	require.Positive(t, server.TTL(keys[0]))
}

func TestRedisRefreshTokenConsumerAllowsOneConcurrentUse(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	consumer := NewRefreshTokenConsumer(refreshTestSecret, NewRedisRefreshTokenStore(client))
	token, err := GenerateRefreshToken(91, "concurrent", "end_user", 3, refreshTestSecret, time.Hour)
	require.NoError(t, err)

	const attempts = 24
	var successes atomic.Int32
	var consumed atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			validated, validateErr := consumer.Validate(token)
			if validateErr != nil {
				t.Errorf("unexpected validate error: %v", validateErr)
				return
			}
			consumeErr := consumer.Consume(context.Background(), validated)
			switch {
			case consumeErr == nil:
				successes.Add(1)
			case errors.Is(consumeErr, ErrRefreshTokenConsumed):
				consumed.Add(1)
			default:
				t.Errorf("unexpected consume error: %v", consumeErr)
			}
		}()
	}
	close(start)
	wg.Wait()

	require.EqualValues(t, 1, successes.Load())
	require.EqualValues(t, attempts-1, consumed.Load())
}

func TestRefreshTokenConsumerFailsClosedWithoutStore(t *testing.T) {
	t.Parallel()
	consumer := NewRefreshTokenConsumer(refreshTestSecret, nil)
	token, err := GenerateRefreshToken(12, "missing-store", "end_user", 1, refreshTestSecret, time.Hour)
	require.NoError(t, err)

	validated, err := consumer.Validate(token)
	require.NoError(t, err)
	err = consumer.Consume(context.Background(), validated)
	var unavailable *RefreshTokenStoreUnavailableError
	require.ErrorAs(t, err, &unavailable)
}

func TestRedisRefreshTokenConsumerFailsClosedWhenRedisIsUnavailable(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr:         server.Addr(),
		DialTimeout:  20 * time.Millisecond,
		ReadTimeout:  20 * time.Millisecond,
		WriteTimeout: 20 * time.Millisecond,
		MaxRetries:   -1,
	})
	t.Cleanup(func() { _ = client.Close() })
	consumer := NewRefreshTokenConsumer(refreshTestSecret, NewRedisRefreshTokenStore(client))
	token, err := GenerateRefreshToken(44, "redis-down", "admin", 2, refreshTestSecret, time.Hour)
	require.NoError(t, err)
	server.Close()

	const attempts = 8
	var successes atomic.Int32
	var unavailableCount atomic.Int32
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			validated, validateErr := consumer.Validate(token)
			if validateErr != nil {
				t.Errorf("unexpected validate error: %v", validateErr)
				return
			}
			consumeErr := consumer.Consume(context.Background(), validated)
			if consumeErr == nil {
				successes.Add(1)
				return
			}
			var unavailable *RefreshTokenStoreUnavailableError
			if errors.As(consumeErr, &unavailable) {
				unavailableCount.Add(1)
				return
			}
			t.Errorf("unexpected consume error: %v", consumeErr)
		}()
	}
	wg.Wait()

	require.Zero(t, successes.Load())
	require.EqualValues(t, attempts, unavailableCount.Load())
}
