package common

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useAtomicMutationMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	server := miniredis.RunT(t)
	previous := RDB
	RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		require.NoError(t, RDB.Close())
		RDB = previous
	})
	return server
}

func TestRedisTTLMutationRejectsMissingAndPersistentKeys(t *testing.T) {
	server := useAtomicMutationMiniRedis(t)
	ctx := context.Background()

	require.ErrorIs(t, RedisIncr("missing", 1), ErrRedisKeyMissing)
	require.ErrorIs(t, RedisHIncrBy("missing-hash", "Quota", 1), ErrRedisKeyMissing)
	require.ErrorIs(t, RedisHSetField("missing-set", "Quota", 1), ErrRedisKeyMissing)

	require.NoError(t, RDB.Set(ctx, "persistent", "3", 0).Err())
	require.ErrorIs(t, RedisIncr("persistent", 1), ErrRedisKeyPersistent)
	value, err := RDB.Get(ctx, "persistent").Result()
	require.NoError(t, err)
	assert.Equal(t, "3", value)

	require.NoError(t, RDB.HSet(ctx, "persistent-hash", "Quota", 3).Err())
	require.ErrorIs(t, RedisHIncrBy("persistent-hash", "Quota", 1), ErrRedisKeyPersistent)
	require.ErrorIs(t, RedisHSetField("persistent-hash", "Status", 2), ErrRedisKeyPersistent)
	assert.Equal(t, "3", server.HGet("persistent-hash", "Quota"))
	assert.False(t, RDB.HExists(ctx, "persistent-hash", "Status").Val())
}

func TestRedisTTLMutationPreservesRemainingLifetime(t *testing.T) {
	server := useAtomicMutationMiniRedis(t)
	ctx := context.Background()

	require.NoError(t, RDB.Set(ctx, "expiring", "3", 10*time.Second).Err())
	require.NoError(t, RDB.HSet(ctx, "expiring-hash", "Quota", 3).Err())
	require.NoError(t, RDB.Expire(ctx, "expiring-hash", 10*time.Second).Err())
	server.FastForward(3 * time.Second)

	stringTTL := server.TTL("expiring")
	hashTTL := server.TTL("expiring-hash")
	require.Positive(t, stringTTL)
	require.Positive(t, hashTTL)

	require.NoError(t, RedisIncr("expiring", 2))
	require.NoError(t, RedisHIncrBy("expiring-hash", "Quota", 2))
	require.NoError(t, RedisHSetField("expiring-hash", "Status", 2))

	value, err := server.Get("expiring")
	require.NoError(t, err)
	assert.Equal(t, "5", value)
	assert.Equal(t, "5", server.HGet("expiring-hash", "Quota"))
	assert.Equal(t, "2", server.HGet("expiring-hash", "Status"))
	assert.LessOrEqual(t, server.TTL("expiring"), stringTTL)
	assert.LessOrEqual(t, server.TTL("expiring-hash"), hashTTL)
	assert.Positive(t, server.TTL("expiring"))
	assert.Positive(t, server.TTL("expiring-hash"))
}
