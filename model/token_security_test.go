package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenInsertPersistsOnlyOneWayDigest(t *testing.T) {
	truncateTables(t)

	const rawKey = "relay-token-raw-secret-material-1234567890abcdef"
	token := Token{
		UserId:         17,
		Key:            rawKey,
		Name:           "hashed-at-rest",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		RemainQuota:    100,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.Insert())

	var storedKey string
	require.NoError(t, DB.Raw("SELECT "+commonKeyCol+" FROM tokens WHERE id = ?", token.Id).Scan(&storedKey).Error)
	assert.NotEqual(t, rawKey, storedKey)
	assert.Len(t, storedKey, 64)
	assert.NotContains(t, storedKey, rawKey[:8])

	authenticated, err := GetTokenByKey(rawKey, true)
	require.NoError(t, err)
	assert.Equal(t, token.Id, authenticated.Id)
	assert.Equal(t, rawKey, authenticated.Key)
}

func TestTokenJSONNeverSerializesCredentialMaterial(t *testing.T) {
	const rawKey = "relay-token-json-secret"
	token := Token{
		Id:   1,
		Key:  rawKey,
		Name: "safe-response",
	}

	payload, err := common.Marshal(token)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), rawKey)
	assert.False(t, strings.Contains(string(payload), `"key"`), string(payload))
}

func TestMigrateTokenKeysHashesSafeRowsAndInvalidatesUnsafeRows(t *testing.T) {
	truncateTables(t)

	safeKey := strings.Repeat("safe-token-", 5)[:48]
	weakKey := "short-token"
	ambiguousDigest := strings.Repeat("a", sha256DigestHexLength)
	for index, key := range []string{safeKey, weakKey, ambiguousDigest} {
		require.NoError(t, DB.Exec(
			"INSERT INTO tokens (user_id, "+commonKeyCol+", key_prefix, status, name, expired_time) VALUES (?, ?, '', ?, ?, -1)",
			99,
			key,
			common.TokenStatusEnabled,
			fmt.Sprintf("legacy-%d", index),
		).Error)
	}

	require.NoError(t, MigrateTokenKeys())

	var tokens []Token
	require.NoError(t, DB.Order("id ASC").Find(&tokens).Error)
	require.Len(t, tokens, 3)
	assert.Equal(t, HashTokenKey(safeKey), tokens[0].KeyHash)
	assert.Equal(t, TokenKeyPrefix(safeKey), tokens[0].KeyPrefix)
	assert.Equal(t, common.TokenStatusEnabled, tokens[0].Status)

	for _, token := range tokens[1:] {
		assert.Equal(t, common.TokenStatusDisabled, token.Status)
		assert.Equal(t, invalidTokenKeyPrefix, token.KeyPrefix)
		assert.NotEmpty(t, token.KeyHash)
		assert.NotEqual(
			t,
			HashTokenKey(fmt.Sprintf("invalid-relay-token:%d", token.Id)),
			token.KeyHash,
			"invalidated credentials must not have a predictable preimage",
		)
	}

	authenticated, err := GetTokenByKey(safeKey, true)
	require.NoError(t, err)
	assert.Equal(t, tokens[0].Id, authenticated.Id)
	_, err = GetTokenByKey(weakKey, true)
	require.Error(t, err)
	_, err = GetTokenByKey(ambiguousDigest, true)
	require.Error(t, err)

	firstHashes := []string{tokens[0].KeyHash, tokens[1].KeyHash, tokens[2].KeyHash}
	require.NoError(t, MigrateTokenKeys())
	require.NoError(t, DB.Order("id ASC").Find(&tokens).Error)
	assert.Equal(t, firstHashes, []string{tokens[0].KeyHash, tokens[1].KeyHash, tokens[2].KeyHash})
}

func TestMigrateTokenKeysRemovesPlaintextFromSoftDeletedRows(t *testing.T) {
	truncateTables(t)

	rawKey := strings.Repeat("deleted-legacy-token-", 3)[:48]
	token := Token{
		UserId:      99,
		Key:         rawKey,
		Name:        "deleted-legacy",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
	}
	require.NoError(t, DB.Create(&token).Error)
	require.NoError(t, DB.Model(&Token{}).
		Where("id = ?", token.Id).
		Updates(map[string]any{"key": rawKey, "key_prefix": ""}).Error)
	require.NoError(t, DB.Delete(&token).Error)

	require.NoError(t, MigrateTokenKeys())

	var migrated Token
	require.NoError(t, DB.Unscoped().First(&migrated, token.Id).Error)
	assert.True(t, migrated.DeletedAt.Valid)
	assert.Equal(t, HashTokenKey(rawKey), migrated.KeyHash)
	assert.Equal(t, TokenKeyPrefix(rawKey), migrated.KeyPrefix)
	assert.NotEqual(t, rawKey, migrated.KeyHash)
}

func TestMigrateTokenKeysNeverBindsLegacyPlaintextInUpdates(t *testing.T) {
	truncateTables(t)

	rawKey := strings.Repeat("migration-log-secret-", 3)[:48]
	require.NoError(t, DB.Exec(
		"INSERT INTO tokens (user_id, "+commonKeyCol+", key_prefix, status, name, expired_time) VALUES (?, ?, '', ?, ?, -1)",
		99,
		rawKey,
		common.TokenStatusEnabled,
		"migration-log-safety",
	).Error)

	boundPlaintext := false
	const callbackName = "test:detect_token_migration_plaintext_bind"
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		for _, variable := range tx.Statement.Vars {
			if value, ok := variable.(string); ok && value == rawKey {
				boundPlaintext = true
			}
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(callbackName))
	})

	require.NoError(t, MigrateTokenKeys())
	assert.False(t, boundPlaintext, "migration SQL must not expose legacy plaintext through bound query values")
}
