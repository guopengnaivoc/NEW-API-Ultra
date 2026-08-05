package model

import (
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserMetadataCacheNeverStoresOrServesQuota(t *testing.T) {
	truncateTables(t)
	server := useUserCacheMiniRedis(t)
	user := User{
		Username:    "quota-authority",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       100,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))
	assert.False(t, common.RDB.HExists(t.Context(), getUserCacheKey(user.Id), "Quota").Val())

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("quota", 40).Error)
	require.NoError(t, updateUserCache(user))

	cached, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 40, cached.Quota)
	quota, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 40, quota)
	assert.False(t, server.Exists(getUserAuthFenceKey(user.Id)))
}

func TestLegacyUserQuotaHashCannotOverrideDatabase(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	user := User{
		Username:    "legacy-quota-cache",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       55,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(user.Id), map[string]interface{}{
		"Id":          user.Id,
		"Group":       user.Group,
		"Status":      user.Status,
		"Role":        user.Role,
		"Username":    user.Username,
		"AuthVersion": user.AuthVersion,
		"CacheSchema": 2,
		"Quota":       999,
	}).Err())
	require.NoError(t, common.RDB.Expire(t.Context(), getUserCacheKey(user.Id), 60*time.Second).Err())

	cached, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 55, cached.Quota)
	assert.False(t, common.RDB.HExists(t.Context(), getUserCacheKey(user.Id), "Quota").Val())
	schema, err := common.RDB.HGet(t.Context(), getUserCacheKey(user.Id), "CacheSchema").Int()
	require.NoError(t, err)
	assert.Equal(t, userCacheSchemaVersion, schema)
}

func TestTokenCacheHitOverlaysDatabaseQuotaState(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:         1,
		Key:            "quota-cache-token",
		Name:           "quota",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		RemainQuota:    100,
		UsedQuota:      0,
		UnlimitedQuota: false,
	}
	require.NoError(t, DB.Create(&token).Error)
	require.NoError(t, cacheSetToken(token))
	cacheKey := "token:v2:" + token.KeyHash
	assert.True(t, common.RDB.Exists(t.Context(), cacheKey).Val() > 0)
	assert.Zero(t, common.RDB.Exists(t.Context(), "token:"+token.KeyHash).Val())
	assert.Zero(t, common.RDB.Exists(t.Context(), "token:"+common.GenerateHMAC(token.Key)).Val())
	cachedFields, err := common.RDB.HGetAll(t.Context(), cacheKey).Result()
	require.NoError(t, err)
	for field, value := range cachedFields {
		assert.NotContains(t, field, token.Key)
		assert.NotContains(t, value, token.Key)
	}
	for _, field := range []string{
		"Status", "ExpiredTime", "RemainQuota", "UnlimitedQuota", "UsedQuota",
	} {
		assert.False(t, common.RDB.HExists(t.Context(), cacheKey, field).Val(), field)
	}
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{
		"remain_quota":    25,
		"used_quota":      75,
		"status":          common.TokenStatusExhausted,
		"expired_time":    int64(123),
		"unlimited_quota": true,
	}).Error)

	got, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 25, got.RemainQuota)
	assert.Equal(t, 75, got.UsedQuota)
	assert.Equal(t, common.TokenStatusExhausted, got.Status)
	assert.EqualValues(t, 123, got.ExpiredTime)
	assert.True(t, got.UnlimitedQuota)
}

func TestTokenCacheRefreshReplacesLegacyAuthorizationFields(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:      1,
		Key:         "legacy-token-cache-fields",
		Name:        "legacy-cache",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
	}
	require.NoError(t, DB.Create(&token).Error)

	legacyCacheKey := "token:" + token.KeyHash
	require.NoError(t, common.RDB.HSet(t.Context(), legacyCacheKey, map[string]interface{}{
		"Id":          token.Id,
		"UserId":      token.UserId,
		"ModelLimits": "legacy-restricted-model",
		"AllowIps":    "192.0.2.10",
		"Group":       "legacy-group",
		"Status":      common.TokenStatusDisabled,
	}).Err())
	require.NoError(t, common.RDB.Expire(t.Context(), legacyCacheKey, time.Minute).Err())

	require.NoError(t, cacheSetToken(token))

	assert.Zero(t, common.RDB.Exists(t.Context(), legacyCacheKey).Val())
	currentCacheKey := "token:v2:" + token.KeyHash
	fields, err := common.RDB.HGetAll(t.Context(), currentCacheKey).Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Id": strconv.Itoa(token.Id)}, fields)
	assert.Positive(t, common.RDB.TTL(t.Context(), currentCacheKey).Val())
}

func TestTokenCacheRefreshWithNonPositiveTTLPreservesIdHint(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	common.SyncFrequency = 0
	token := Token{
		UserId:      1,
		Key:         "non-positive-token-cache-ttl",
		Name:        "persistent-cache",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
	}
	require.NoError(t, DB.Create(&token).Error)

	require.NoError(t, cacheSetToken(token))

	cacheKey := "token:v2:" + token.KeyHash
	fields, err := common.RDB.HGetAll(t.Context(), cacheKey).Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Id": strconv.Itoa(token.Id)}, fields)
	assert.Equal(t, time.Duration(-1), common.RDB.TTL(t.Context(), cacheKey).Val())
	assert.Zero(t, common.RDB.Exists(t.Context(), "token:"+token.KeyHash).Val())
}

func TestLateTokenCacheWriteCannotRestoreStaleAuthorizationState(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	allowAll := ""
	token := Token{
		UserId:             1,
		Key:                "stale-authorization-cache-token",
		Name:               "stale-authorization",
		Status:             common.TokenStatusEnabled,
		ExpiredTime:        -1,
		RemainQuota:        100,
		ModelLimitsEnabled: false,
		AllowIps:           &allowAll,
		Group:              "",
		CrossGroupRetry:    false,
	}
	require.NoError(t, DB.Create(&token).Error)

	const restrictedIP = "192.0.2.10"
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{
		"user_id":              2,
		"model_limits_enabled": true,
		"model_limits":         "restricted-model",
		"allow_ips":            restrictedIP,
		"group":                "restricted-group",
		"cross_group_retry":    true,
	}).Error)

	// Reproduce an old binary publishing a stale full-metadata legacy hash
	// after a current writer has already published the v2 lookup hint.
	require.NoError(t, cacheSetToken(token))
	require.NoError(t, common.RDB.HSet(t.Context(), "token:"+token.KeyHash, map[string]interface{}{
		"Id":                 token.Id,
		"UserId":             token.UserId,
		"Name":               token.Name,
		"CreatedTime":        token.CreatedTime,
		"AccessedTime":       token.AccessedTime,
		"ModelLimitsEnabled": token.ModelLimitsEnabled,
		"ModelLimits":        token.ModelLimits,
		"AllowIps":           *token.AllowIps,
		"Group":              token.Group,
		"CrossGroupRetry":    token.CrossGroupRetry,
	}).Err())

	got, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 2, got.UserId)
	assert.Equal(t, token.KeyPrefix, got.KeyPrefix)
	assert.True(t, got.ModelLimitsEnabled)
	assert.Equal(t, "restricted-model", got.ModelLimits)
	require.NotNil(t, got.AllowIps)
	assert.Equal(t, restrictedIP, *got.AllowIps)
	assert.Equal(t, "restricted-group", got.Group)
	assert.True(t, got.CrossGroupRetry)
}

func TestDeletedTokenCacheCannotAuthenticate(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:      1,
		Key:         "deleted-cache-token",
		Name:        "deleted",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
	}
	require.NoError(t, DB.Create(&token).Error)
	require.NoError(t, cacheSetToken(token))
	require.NoError(t, DB.Delete(&token).Error)

	got, err := GetTokenByKey(token.Key, false)
	assert.Nil(t, got)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestDeletedTokenCacheCannotAuthenticateAfterIDReuse(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:      1,
		Key:         "deleted-reused-id-token",
		Name:        "deleted",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
	}
	require.NoError(t, DB.Create(&token).Error)
	require.NoError(t, cacheSetToken(token))
	require.NoError(t, DB.Unscoped().Delete(&token).Error)
	replacement := Token{
		Id:             token.Id,
		UserId:         2,
		Key:            "replacement-token",
		Name:           "replacement",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		RemainQuota:    500,
		UnlimitedQuota: true,
	}
	require.NoError(t, DB.Create(&replacement).Error)

	got, err := GetTokenByKey(token.Key, false)
	assert.Nil(t, got)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestRotateTokenKeyReplacesRedisCredentialWithoutStoringRawKeys(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:      1,
		Key:         "rotation-old-token-secret",
		Name:        "rotation-cache",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
	}
	require.NoError(t, DB.Create(&token).Error)
	require.NoError(t, cacheSetToken(token))

	oldKey := token.Key
	oldCacheKey := "token:v2:" + token.KeyHash
	require.Positive(t, common.RDB.Exists(t.Context(), oldCacheKey).Val())

	const replacementKey = "rotation-new-token-secret"
	rotated, err := RotateTokenKey(token.Id, token.UserId, replacementKey)
	require.NoError(t, err)
	assert.Equal(t, token.Id, rotated.Id)
	assert.Equal(t, replacementKey, rotated.Key)
	assert.Equal(t, HashTokenKey(replacementKey), rotated.KeyHash)

	assert.Zero(t, common.RDB.Exists(t.Context(), oldCacheKey).Val())
	newCacheKey := "token:v2:" + HashTokenKey(replacementKey)
	require.Positive(t, common.RDB.Exists(t.Context(), newCacheKey).Val())
	cachedFields, err := common.RDB.HGetAll(t.Context(), newCacheKey).Result()
	require.NoError(t, err)
	for field, value := range cachedFields {
		assert.NotContains(t, field, replacementKey)
		assert.NotContains(t, value, replacementKey)
	}

	_, err = GetTokenByKey(oldKey, false)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	authenticated, err := GetTokenByKey(replacementKey, false)
	require.NoError(t, err)
	assert.Equal(t, token.Id, authenticated.Id)
}

func TestQuotaMutationsBypassBatchAggregation(t *testing.T) {
	truncateTables(t)
	previousBatchUpdate := common.BatchUpdateEnabled
	previousRedisEnabled := common.RedisEnabled
	common.BatchUpdateEnabled = true
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.BatchUpdateEnabled = previousBatchUpdate
		common.RedisEnabled = previousRedisEnabled
	})

	user := User{Username: "batch-wallet", Quota: 100}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{
		UserId:      user.Id,
		Key:         "batch-token",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
	}
	require.NoError(t, DB.Create(&token).Error)

	require.NoError(t, DecreaseUserQuota(user.Id, 30, false))
	require.NoError(t, DecreaseTokenQuota(token.Id, token.Key, 30))

	require.NoError(t, DB.First(&user, user.Id).Error)
	require.NoError(t, DB.First(&token, token.Id).Error)
	assert.Equal(t, 70, user.Quota)
	assert.Equal(t, 70, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)

	require.NoError(t, IncreaseUserQuota(user.Id, 20, false))
	require.NoError(t, IncreaseTokenQuota(token.Id, token.Key, 20))
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.NoError(t, DB.First(&token, token.Id).Error)
	assert.Equal(t, 90, user.Quota)
	assert.Equal(t, 90, token.RemainQuota)
	assert.Equal(t, 10, token.UsedQuota)
}

func TestLegacyBatchFlagCannotDeferAccountingPersistence(t *testing.T) {
	truncateTables(t)
	previousBatchUpdate := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() {
		common.BatchUpdateEnabled = previousBatchUpdate
	})

	const userID = 9182
	const channelID = 8271
	user := User{Id: userID, Username: "synchronous-accounting"}
	channel := Channel{Id: channelID, Name: "synchronous-accounting"}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&channel).Error)

	UpdateUserUsedQuotaAndRequestCount(userID, 45)
	UpdateChannelUsedQuota(channelID, 67)

	require.NoError(t, DB.Select("used_quota", "request_count").First(&user, userID).Error)
	require.NoError(t, DB.Select("used_quota").First(&channel, channelID).Error)
	assert.Equal(t, 45, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	assert.Equal(t, int64(67), channel.UsedQuota)
}

func TestUserMetadataAccessorsDoNotReadQuotaFromDatabase(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	user := User{
		Username:    "metadata-only-cache",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       100,
		Setting:     `{"language":"en"}`,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))

	var queryCount atomic.Int32
	const callbackName = "test:count_metadata_accessor_queries"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount.Add(1)
	}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Query().Remove(callbackName))
	})

	group, err := getUserGroupCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "default", group)
	status, err := getUserStatusCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.UserStatusEnabled, status)
	name, err := getUserNameCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, user.Username, name)
	setting, err := getUserSettingCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "en", setting.Language)
	assert.Equal(t, "en", GetUserLanguage(user.Id))
	assert.Zero(t, queryCount.Load())
}

func TestMissingUserQuotaReturnsNotFound(t *testing.T) {
	truncateTables(t)

	quota, err := GetUserQuota(987654321, false)
	assert.Zero(t, quota)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
