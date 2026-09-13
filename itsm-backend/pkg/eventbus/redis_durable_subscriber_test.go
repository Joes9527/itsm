package eventbus

import (
	"context"
	"github.com/ThreeDotsLabs/watermill-redisstream/pkg/redisstream"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/config"
	"testing"
	"time"
)

func TestDurableEntryRejectsMalformedWireWithoutPanic(t *testing.T) {
	for _, fields := range []map[string]interface{}{
		{}, {redisstream.UUIDHeaderKey: 1, "payload": "{}"}, {redisstream.UUIDHeaderKey: "", "payload": "{}"},
		{redisstream.UUIDHeaderKey: "id", "payload": 1}, {redisstream.UUIDHeaderKey: "id", "payload": "{}", "metadata": 1},
		{redisstream.UUIDHeaderKey: "id", "payload": "{}", "metadata": "invalid-msgpack"},
	} {
		require.NotPanics(t, func() { _, err := decodeDurableEntry(redis.XMessage{ID: "1-0", Values: fields}); require.Error(t, err) })
	}
}

func TestDurableConcurrentCloseSharesCompletionAndClosesClient(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	subscriber, err := newRedisDurableSubscriber(client, "itsm:test", config.EventStreamConfig{}, zap.NewNop().Sugar())
	require.NoError(t, err)
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { results <- subscriber.Close() }()
	}
	for i := 0; i < 8; i++ {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("concurrent close did not finish")
		}
	}
	require.ErrorIs(t, client.Ping(context.Background()).Err(), redis.ErrClosed)
	require.NoError(t, subscriber.Close())
}
