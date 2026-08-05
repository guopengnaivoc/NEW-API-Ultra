package model

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testCurrentTokenCachePrefix = "token:v2:"
	testLegacyTokenCachePrefix  = "token:"
)

func createTokenCacheVersionFixture(t *testing.T, rawKey string) Token {
	t.Helper()
	token := Token{
		UserId:      1,
		Key:         rawKey,
		Name:        "cache-version-fixture",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
		Group:       "current-group",
	}
	require.NoError(t, DB.Create(&token).Error)
	return token
}

func seedTokenCacheNamespaces(t *testing.T, keyHash string, id int) {
	t.Helper()
	require.NoError(t, common.RDB.HSet(t.Context(), testCurrentTokenCachePrefix+keyHash, "Id", id).Err())
	require.NoError(t, common.RDB.HSet(t.Context(), testLegacyTokenCachePrefix+keyHash, map[string]interface{}{
		"Id":     id,
		"UserId": 999,
		"Group":  "stale-legacy-group",
	}).Err())
}

func readTokenWithLegacyCacheContract(rawKey string) (*Token, error) {
	keyHash := HashTokenKey(rawKey)
	var token Token
	if err := common.RedisHGetObj(testLegacyTokenCachePrefix+keyHash, &token); err == nil {
		token.Key = rawKey
		token.KeyHash = keyHash
		return &token, nil
	}
	if err := DB.Where(commonKeyCol+" = ?", keyHash).First(&token).Error; err != nil {
		return nil, err
	}
	token.Key = rawKey
	return &token, nil
}

func requireTokenCacheNamespacesDeleted(t *testing.T, keyHash string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return common.RDB.Exists(
			t.Context(),
			testCurrentTokenCachePrefix+keyHash,
			testLegacyTokenCachePrefix+keyHash,
		).Val() == 0
	}, time.Second, 5*time.Millisecond)
}

func useTokenCacheRealRedis(t *testing.T) {
	t.Helper()
	addr := os.Getenv("NEWAPI_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("NEWAPI_TEST_REDIS_ADDR is not set")
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	require.NoError(t, client.Ping(t.Context()).Err())
	oldRedisEnabled := common.RedisEnabled
	oldRDB := common.RDB
	oldSyncFrequency := common.SyncFrequency
	common.RedisEnabled = true
	common.SyncFrequency = 2
	common.RDB = client
	t.Cleanup(func() {
		_ = client.Close()
		common.RedisEnabled = oldRedisEnabled
		common.RDB = oldRDB
		common.SyncFrequency = oldSyncFrequency
	})
}

func cleanupTokenCacheNamespaces(t *testing.T, keyHashes ...string) {
	t.Helper()
	keys := make([]string, 0, len(keyHashes)*2)
	for _, keyHash := range keyHashes {
		keys = append(keys, testCurrentTokenCachePrefix+keyHash, testLegacyTokenCachePrefix+keyHash)
	}
	t.Cleanup(func() {
		_ = common.RDB.Del(context.Background(), keys...).Err()
	})
}

func TestTokenCacheWriterKeepsLegacyReadersOnCompatibleFallback(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := createTokenCacheVersionFixture(t, "rolling-writer-token")
	legacyKey := testLegacyTokenCachePrefix + token.KeyHash
	currentKey := testCurrentTokenCachePrefix + token.KeyHash

	require.NoError(t, common.RDB.HSet(t.Context(), legacyKey, map[string]interface{}{
		"Id":                 token.Id,
		"UserId":             token.UserId,
		"Name":               token.Name,
		"ModelLimitsEnabled": false,
		"ModelLimits":        "",
		"AllowIps":           "",
		"Group":              token.Group,
		"CrossGroupRetry":    false,
	}).Err())
	require.NoError(t, common.RDB.Expire(t.Context(), legacyKey, time.Minute).Err())

	require.NoError(t, cacheSetToken(token))

	assert.Zero(t, common.RDB.Exists(t.Context(), legacyKey).Val())
	fields, err := common.RDB.HGetAll(t.Context(), currentKey).Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Id": strconv.Itoa(token.Id)}, fields)
	assert.Positive(t, common.RDB.TTL(t.Context(), currentKey).Val())

	legacyReaderToken, err := readTokenWithLegacyCacheContract(token.Key)
	require.NoError(t, err)
	assert.Equal(t, token.Id, legacyReaderToken.Id)
	assert.Equal(t, token.UserId, legacyReaderToken.UserId)
	assert.Equal(t, token.Group, legacyReaderToken.Group)
}

func TestTokenCacheReaderUsesOnlyCurrentNamespace(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := createTokenCacheVersionFixture(t, "rolling-reader-token")
	other := createTokenCacheVersionFixture(t, "rolling-reader-other-token")
	legacyKey := testLegacyTokenCachePrefix + token.KeyHash
	currentKey := testCurrentTokenCachePrefix + token.KeyHash

	require.NoError(t, cacheSetToken(token))
	// Simulate an old writer completing after the current writer. The stale
	// legacy hash must not affect a current reader while both versions overlap.
	require.NoError(t, common.RDB.HSet(t.Context(), legacyKey, map[string]interface{}{
		"Id":     other.Id,
		"UserId": 999,
		"Group":  "stale-legacy-group",
	}).Err())

	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, token.Id, cached.Id)

	require.NoError(t, common.RDB.Del(t.Context(), currentKey).Err())
	cached, err = cacheGetTokenByKey(token.Key)
	assert.Nil(t, cached)
	assert.Error(t, err)

	authoritative, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, token.Id, authoritative.Id)
	assert.Equal(t, token.UserId, authoritative.UserId)
	assert.Equal(t, token.Group, authoritative.Group)
	require.Eventually(t, func() bool {
		id, cacheErr := common.RDB.HGet(t.Context(), currentKey, "Id").Int()
		return cacheErr == nil && id == token.Id
	}, time.Second, 5*time.Millisecond)
	assert.Zero(t, common.RDB.Exists(t.Context(), legacyKey).Val())
}

func TestTokenCacheWriterPreservesNonPositiveTTLInCurrentNamespace(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	common.SyncFrequency = 0
	token := createTokenCacheVersionFixture(t, "persistent-versioned-token")
	legacyKey := testLegacyTokenCachePrefix + token.KeyHash
	currentKey := testCurrentTokenCachePrefix + token.KeyHash

	require.NoError(t, common.RDB.HSet(t.Context(), legacyKey, "Id", token.Id).Err())
	require.NoError(t, cacheSetToken(token))

	assert.Zero(t, common.RDB.Exists(t.Context(), legacyKey).Val())
	fields, err := common.RDB.HGetAll(t.Context(), currentKey).Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Id": strconv.Itoa(token.Id)}, fields)
	assert.Equal(t, time.Duration(-1), common.RDB.TTL(t.Context(), currentKey).Val())
}

func TestTokenCacheInvalidationDeletesCurrentAndLegacyNamespaces(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := createTokenCacheVersionFixture(t, "dual-invalidation-token")
	seedTokenCacheNamespaces(t, token.KeyHash, token.Id)

	require.NoError(t, cacheDeleteTokenHash(token.KeyHash))

	assert.Zero(t, common.RDB.Exists(t.Context(), testCurrentTokenCachePrefix+token.KeyHash).Val())
	assert.Zero(t, common.RDB.Exists(t.Context(), testLegacyTokenCachePrefix+token.KeyHash).Val())
}

func TestUserTokenCacheInvalidationDeletesCurrentAndLegacyNamespaces(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := createTokenCacheVersionFixture(t, "user-invalidation-token")
	seedTokenCacheNamespaces(t, token.KeyHash, token.Id)

	require.NoError(t, InvalidateUserTokensCache(token.UserId))

	assert.Zero(t, common.RDB.Exists(t.Context(), testCurrentTokenCachePrefix+token.KeyHash).Val())
	assert.Zero(t, common.RDB.Exists(t.Context(), testLegacyTokenCachePrefix+token.KeyHash).Val())
}

func TestTokenMutationRefreshesCurrentNamespaceAndClearsLegacyNamespace(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Token) error
	}{
		{
			name: "full token update",
			mutate: func(token *Token) error {
				token.Name = "updated-cache-version-fixture"
				return token.Update()
			},
		},
		{
			name: "selected status update",
			mutate: func(token *Token) error {
				token.Status = common.TokenStatusDisabled
				return token.SelectUpdate()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncateTables(t)
			useUserCacheMiniRedis(t)
			token := createTokenCacheVersionFixture(t, "versioned-mutation-"+tt.name)
			seedTokenCacheNamespaces(t, token.KeyHash, token.Id+1000)

			require.NoError(t, tt.mutate(&token))

			require.Eventually(t, func() bool {
				id, cacheErr := common.RDB.HGet(
					t.Context(),
					testCurrentTokenCachePrefix+token.KeyHash,
					"Id",
				).Int()
				legacyExists := common.RDB.Exists(
					t.Context(),
					testLegacyTokenCachePrefix+token.KeyHash,
				).Val()
				return cacheErr == nil && id == token.Id && legacyExists == 0
			}, time.Second, 5*time.Millisecond)
		})
	}
}

func TestTokenDeletionDeletesCurrentAndLegacyNamespaces(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := createTokenCacheVersionFixture(t, "versioned-deletion-token")
	seedTokenCacheNamespaces(t, token.KeyHash, token.Id)

	require.NoError(t, token.Delete())

	requireTokenCacheNamespacesDeleted(t, token.KeyHash)
}

func TestBatchTokenDeletionDeletesCurrentAndLegacyNamespaces(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := createTokenCacheVersionFixture(t, "versioned-batch-deletion-token")
	seedTokenCacheNamespaces(t, token.KeyHash, token.Id)

	deleted, err := BatchDeleteTokens([]int{token.Id}, token.UserId)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	requireTokenCacheNamespacesDeleted(t, token.KeyHash)
}

func TestTokenRotationDeletesBothOldNamespacesAndClearsNewLegacyNamespace(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := createTokenCacheVersionFixture(t, "versioned-rotation-old")
	seedTokenCacheNamespaces(t, token.KeyHash, token.Id)

	const replacementKey = "versioned-rotation-new"
	replacementHash := HashTokenKey(replacementKey)
	seedTokenCacheNamespaces(t, replacementHash, token.Id+1000)

	rotated, err := RotateTokenKey(token.Id, token.UserId, replacementKey)
	require.NoError(t, err)
	assert.Equal(t, replacementHash, rotated.KeyHash)

	requireTokenCacheNamespacesDeleted(t, token.KeyHash)
	assert.Zero(t, common.RDB.Exists(t.Context(), testLegacyTokenCachePrefix+replacementHash).Val())
	fields, err := common.RDB.HGetAll(t.Context(), testCurrentTokenCachePrefix+replacementHash).Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Id": strconv.Itoa(token.Id)}, fields)
}

func TestTokenCacheVersioningAgainstRealRedis(t *testing.T) {
	useTokenCacheRealRedis(t)

	t.Run("ttl semantics", func(t *testing.T) {
		truncateTables(t)
		oldSyncFrequency := common.SyncFrequency
		t.Cleanup(func() {
			common.SyncFrequency = oldSyncFrequency
		})
		expiring := createTokenCacheVersionFixture(t, "real-redis-expiring")
		persistent := createTokenCacheVersionFixture(t, "real-redis-persistent")
		cleanupTokenCacheNamespaces(t, expiring.KeyHash, persistent.KeyHash)

		common.SyncFrequency = 2
		require.NoError(t, cacheSetToken(expiring))
		assert.Positive(t, common.RDB.TTL(
			t.Context(),
			testCurrentTokenCachePrefix+expiring.KeyHash,
		).Val())

		common.SyncFrequency = 0
		require.NoError(t, cacheSetToken(persistent))
		assert.Equal(t, time.Duration(-1), common.RDB.TTL(
			t.Context(),
			testCurrentTokenCachePrefix+persistent.KeyHash,
		).Val())
	})

	t.Run("old writer after current writer", func(t *testing.T) {
		truncateTables(t)
		token := createTokenCacheVersionFixture(t, "real-redis-rolling-overlap")
		cleanupTokenCacheNamespaces(t, token.KeyHash)
		require.NoError(t, cacheSetToken(token))
		require.NoError(t, common.RDB.HSet(t.Context(), testLegacyTokenCachePrefix+token.KeyHash, map[string]interface{}{
			"Id":     token.Id + 1000,
			"UserId": token.UserId + 1000,
			"Group":  "stale-real-redis-group",
		}).Err())

		got, err := GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		assert.Equal(t, token.Id, got.Id)
		assert.Equal(t, token.UserId, got.UserId)
		assert.Equal(t, token.Group, got.Group)

		require.NoError(t, cacheSetToken(token))
		assert.Zero(t, common.RDB.Exists(t.Context(), testLegacyTokenCachePrefix+token.KeyHash).Val())
	})

	t.Run("mutation refresh", func(t *testing.T) {
		truncateTables(t)
		token := createTokenCacheVersionFixture(t, "real-redis-mutation")
		cleanupTokenCacheNamespaces(t, token.KeyHash)
		seedTokenCacheNamespaces(t, token.KeyHash, token.Id+1000)

		token.Name = "real-redis-updated"
		require.NoError(t, token.Update())
		require.Eventually(t, func() bool {
			id, err := common.RDB.HGet(t.Context(), testCurrentTokenCachePrefix+token.KeyHash, "Id").Int()
			return err == nil &&
				id == token.Id &&
				common.RDB.Exists(t.Context(), testLegacyTokenCachePrefix+token.KeyHash).Val() == 0
		}, time.Second, 5*time.Millisecond)

		seedTokenCacheNamespaces(t, token.KeyHash, token.Id+1000)
		token.Status = common.TokenStatusDisabled
		require.NoError(t, token.SelectUpdate())
		require.Eventually(t, func() bool {
			id, err := common.RDB.HGet(t.Context(), testCurrentTokenCachePrefix+token.KeyHash, "Id").Int()
			return err == nil &&
				id == token.Id &&
				common.RDB.Exists(t.Context(), testLegacyTokenCachePrefix+token.KeyHash).Val() == 0
		}, time.Second, 5*time.Millisecond)
	})

	t.Run("direct and user invalidation", func(t *testing.T) {
		truncateTables(t)
		token := createTokenCacheVersionFixture(t, "real-redis-invalidation")
		cleanupTokenCacheNamespaces(t, token.KeyHash)
		seedTokenCacheNamespaces(t, token.KeyHash, token.Id)
		require.NoError(t, cacheDeleteTokenHash(token.KeyHash))
		requireTokenCacheNamespacesDeleted(t, token.KeyHash)

		seedTokenCacheNamespaces(t, token.KeyHash, token.Id)
		require.NoError(t, InvalidateUserTokensCache(token.UserId))
		requireTokenCacheNamespacesDeleted(t, token.KeyHash)
	})

	t.Run("single and batch deletion", func(t *testing.T) {
		truncateTables(t)
		single := createTokenCacheVersionFixture(t, "real-redis-single-deletion")
		batch := createTokenCacheVersionFixture(t, "real-redis-batch-deletion")
		cleanupTokenCacheNamespaces(t, single.KeyHash, batch.KeyHash)
		seedTokenCacheNamespaces(t, single.KeyHash, single.Id)
		seedTokenCacheNamespaces(t, batch.KeyHash, batch.Id)

		require.NoError(t, single.Delete())
		requireTokenCacheNamespacesDeleted(t, single.KeyHash)
		deleted, err := BatchDeleteTokens([]int{batch.Id}, batch.UserId)
		require.NoError(t, err)
		assert.Equal(t, 1, deleted)
		requireTokenCacheNamespacesDeleted(t, batch.KeyHash)
	})

	t.Run("rotation", func(t *testing.T) {
		truncateTables(t)
		token := createTokenCacheVersionFixture(t, "real-redis-rotation-old")
		const replacementKey = "real-redis-rotation-new"
		replacementHash := HashTokenKey(replacementKey)
		cleanupTokenCacheNamespaces(t, token.KeyHash, replacementHash)
		seedTokenCacheNamespaces(t, token.KeyHash, token.Id)
		seedTokenCacheNamespaces(t, replacementHash, token.Id+1000)

		rotated, err := RotateTokenKey(token.Id, token.UserId, replacementKey)
		require.NoError(t, err)
		assert.Equal(t, replacementHash, rotated.KeyHash)
		requireTokenCacheNamespacesDeleted(t, token.KeyHash)
		assert.Zero(t, common.RDB.Exists(t.Context(), testLegacyTokenCachePrefix+replacementHash).Val())
		id, err := common.RDB.HGet(t.Context(), testCurrentTokenCachePrefix+replacementHash, "Id").Int()
		require.NoError(t, err)
		assert.Equal(t, token.Id, id)
	})
}

func TestValidateUserTokenPersistsExpiredOrExhaustedStatusWithRedisEnabled(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	expiredToken := Token{
		UserId:         1,
		Key:            "validate-expired-token",
		Name:           "expired-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    common.GetTimestamp() - 10,
		RemainQuota:    100,
		UnlimitedQuota: true,
	}
	require.NoError(t, DB.Create(&expiredToken).Error)

	_, err := ValidateUserToken(expiredToken.Key)
	assert.ErrorIs(t, err, ErrTokenInvalid)

	var refreshed Token
	require.NoError(t, DB.First(&refreshed, "id = ?", expiredToken.Id).Error)
	assert.Equal(t, common.TokenStatusExpired, refreshed.Status)

	exhaustedToken := Token{
		UserId:         1,
		Key:            "validate-exhausted-token",
		Name:           "exhausted-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		RemainQuota:    0,
		UnlimitedQuota: false,
	}
	require.NoError(t, DB.Create(&exhaustedToken).Error)

	_, err = ValidateUserToken(exhaustedToken.Key)
	assert.ErrorIs(t, err, ErrTokenInvalid)

	var consumed Token
	require.NoError(t, DB.First(&consumed, "id = ?", exhaustedToken.Id).Error)
	assert.Equal(t, common.TokenStatusExhausted, consumed.Status)
}
