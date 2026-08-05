package model

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	currentTokenCachePrefix = "token:v2:"
	legacyTokenCachePrefix  = "token:"
)

func currentTokenCacheKey(keyHash string) string {
	return currentTokenCachePrefix + keyHash
}

func legacyTokenCacheKey(keyHash string) string {
	return legacyTokenCachePrefix + keyHash
}

func cacheSetToken(token Token) error {
	if token.KeyHash == "" {
		return fmt.Errorf("token hash is empty")
	}
	cacheKey := currentTokenCacheKey(token.KeyHash)
	legacyCacheKey := legacyTokenCacheKey(token.KeyHash)
	cacheTTL := time.Duration(common.RedisKeyCacheSeconds()) * time.Second
	txn := common.RDB.TxPipeline()
	txn.Del(context.Background(), legacyCacheKey, cacheKey)
	txn.HSet(context.Background(), cacheKey, "Id", token.Id)
	if cacheTTL > 0 {
		txn.Expire(context.Background(), cacheKey, cacheTTL)
	}
	_, err := txn.Exec(context.Background())
	if err != nil {
		return fmt.Errorf("failed to replace token cache entry: %w", err)
	}
	return nil
}

func cacheDeleteTokenHash(keyHash string) error {
	err := common.RDB.Del(
		context.Background(),
		currentTokenCacheKey(keyHash),
		legacyTokenCacheKey(keyHash),
	).Err()
	if err != nil {
		return fmt.Errorf("failed to delete token cache entries: %w", err)
	}
	return nil
}

// CacheGetTokenByKey 从缓存中获取 token，如果缓存中不存在，则从数据库中获取
func cacheGetTokenByKey(key string) (*Token, error) {
	keyHash := HashTokenKey(key)
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var token Token
	err := common.RedisHGetObj(currentTokenCacheKey(keyHash), &token)
	if err != nil {
		return nil, err
	}
	token.Key = key
	token.KeyHash = keyHash
	return &token, nil
}
