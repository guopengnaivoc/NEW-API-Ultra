package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareUserSettingSecretTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(
		&User{},
		&Channel{},
		&CustomOAuthProvider{},
		&Option{},
		&TwoFA{},
	))
	require.NoError(t, DB.Exec("DELETE FROM users").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	require.NoError(t, DB.Exec("DELETE FROM custom_oauth_providers").Error)
	require.NoError(t, DB.Exec("DELETE FROM options").Error)
	require.NoError(t, DB.Exec("DELETE FROM two_fas").Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM users")
		DB.Exec("DELETE FROM channels")
		DB.Exec("DELETE FROM custom_oauth_providers")
		DB.Exec("DELETE FROM options")
		DB.Exec("DELETE FROM two_fas")
	})
}

func insertRawUserSetting(
	t *testing.T,
	id int,
	username string,
	storedSetting string,
	deletedAt *time.Time,
) {
	t.Helper()
	require.NoError(t, DB.Table("users").Create(map[string]any{
		"id":           id,
		"username":     username,
		"password":     "test-password-hash",
		"setting":      storedSetting,
		"auth_version": 1,
		"deleted_at":   deletedAt,
	}).Error)
}

func rawUserSetting(t *testing.T, id int) string {
	t.Helper()
	var stored string
	require.NoError(t, DB.Table("users").
		Select("setting").
		Where("id = ?", id).
		Scan(&stored).Error)
	return stored
}

func marshalUserSetting(t *testing.T, setting dto.UserSetting) string {
	t.Helper()
	encoded, err := common.Marshal(setting)
	require.NoError(t, err)
	return string(encoded)
}

func TestUserSettingConditionallyEncryptsCredentialsAndReturnsToPlaintext(
	t *testing.T,
) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	user := &User{
		Username:    "conditional-setting",
		Password:    "test-password-hash",
		AuthVersion: 1,
	}
	user.SetSetting(dto.UserSetting{
		Language:      "en",
		WebhookSecret: "webhook-signing-secret",
		GotifyToken:   "gotify-application-token",
	})
	require.NoError(t, DB.Create(user).Error)

	stored := rawUserSetting(t, user.Id)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	assert.NotContains(t, stored, "webhook-signing-secret")
	assert.NotContains(t, stored, "gotify-application-token")

	var loaded User
	require.NoError(t, DB.First(&loaded, user.Id).Error)
	setting := loaded.GetSetting()
	assert.Equal(t, "webhook-signing-secret", setting.WebhookSecret)
	assert.Equal(t, "gotify-application-token", setting.GotifyToken)
	assert.Equal(t, "en", setting.Language)

	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{Language: "fr"}))
	stored = rawUserSetting(t, user.Id)
	assert.False(t, common.IsDataEncryptionEnvelope(stored))
	assert.JSONEq(t, `{"gotify_priority":0,"language":"fr"}`, stored)
}

func TestOrdinaryUserSettingRemainsPlaintextWithoutKeyring(t *testing.T) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(t, "", "", "true")
	user := &User{
		Username:    "ordinary-setting",
		Password:    "test-password-hash",
		AuthVersion: 1,
	}
	user.SetSetting(dto.UserSetting{Language: "vi"})

	require.NoError(t, DB.Create(user).Error)
	stored := rawUserSetting(t, user.Id)
	assert.False(t, common.IsDataEncryptionEnvelope(stored))
	assert.JSONEq(t, `{"gotify_priority":0,"language":"vi"}`, stored)

	var loaded User
	require.NoError(t, DB.First(&loaded, user.Id).Error)
	assert.Equal(t, "vi", loaded.GetSetting().Language)
}

func TestUserUpdatePersistsProtectedSettingEnvelope(t *testing.T) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	user := &User{
		Username:    "model-update-setting",
		Password:    "test-password-hash",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	user.SetSetting(dto.UserSetting{Language: "en"})
	require.NoError(t, DB.Create(user).Error)

	loaded, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	loaded.SetSetting(dto.UserSetting{
		Language:      "en",
		WebhookSecret: "model-update-webhook-secret",
	})
	require.NoError(t, loaded.Update(false))

	stored := rawUserSetting(t, user.Id)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	assert.NotContains(t, stored, "model-update-webhook-secret")
	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(
		t,
		"model-update-webhook-secret",
		reloaded.GetSetting().WebhookSecret,
	)
}

func TestMigrateUserSettingSecretsIncludesSoftDeletedAndNeverBindsPlaintext(
	t *testing.T,
) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	activeSetting := marshalUserSetting(t, dto.UserSetting{
		WebhookSecret: "legacy-webhook-secret",
	})
	deletedSetting := marshalUserSetting(t, dto.UserSetting{
		GotifyToken: "legacy-gotify-token",
	})
	ordinarySetting := marshalUserSetting(t, dto.UserSetting{Language: "ja"})
	deletedAt := time.Now().UTC()
	insertRawUserSetting(t, 7101, "legacy-active-setting", activeSetting, nil)
	insertRawUserSetting(t, 7102, "legacy-deleted-setting", deletedSetting, &deletedAt)
	insertRawUserSetting(t, 7103, "legacy-ordinary-setting", ordinarySetting, nil)

	callbackName := fmt.Sprintf("test:no-user-setting-plaintext-bind:%s", t.Name())
	var plaintextBound bool
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			for _, variable := range tx.Statement.Vars {
				value, ok := variable.(string)
				if ok && (value == activeSetting || value == deletedSetting) {
					plaintextBound = true
				}
			}
		}))
	t.Cleanup(func() {
		DB.Callback().Update().Remove(callbackName)
	})

	require.NoError(t, MigrateUserSettingSecrets())
	firstActive := rawUserSetting(t, 7101)
	firstDeleted := rawUserSetting(t, 7102)
	assert.True(t, common.IsDataEncryptionEnvelope(firstActive))
	assert.True(t, common.IsDataEncryptionEnvelope(firstDeleted))
	assert.Equal(t, ordinarySetting, rawUserSetting(t, 7103))
	assert.False(t, plaintextBound)

	require.NoError(t, MigrateUserSettingSecrets())
	assert.Equal(t, firstActive, rawUserSetting(t, 7101))
	assert.Equal(t, firstDeleted, rawUserSetting(t, 7102))

	var active User
	require.NoError(t, DB.First(&active, 7101).Error)
	assert.Equal(t, "legacy-webhook-secret", active.GetSetting().WebhookSecret)
	var deleted User
	require.NoError(t, DB.Unscoped().First(&deleted, 7102).Error)
	assert.Equal(t, "legacy-gotify-token", deleted.GetSetting().GotifyToken)
}

func TestMigrateUserSettingSecretsAllowsOrdinarySettingWithoutKeyring(
	t *testing.T,
) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(t, "", "", "true")
	ordinarySetting := marshalUserSetting(t, dto.UserSetting{
		Language:       "fr",
		SidebarModules: `{"personal":{"profile":true}}`,
	})
	insertRawUserSetting(
		t,
		7151,
		"keyless-ordinary-migration",
		ordinarySetting,
		nil,
	)

	require.NoError(t, MigrateUserSettingSecrets())
	assert.Equal(t, ordinarySetting, rawUserSetting(t, 7151))
}

func TestMigrateUserSettingSecretsRejectsProtectedSettingWithoutKeyring(
	t *testing.T,
) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(t, "", "", "true")
	protectedSetting := marshalUserSetting(t, dto.UserSetting{
		WebhookSecret: "keyless-migration-secret-must-not-leak",
	})
	insertRawUserSetting(
		t,
		7152,
		"keyless-protected-migration",
		protectedSetting,
		nil,
	)

	err := MigrateUserSettingSecrets()
	require.Error(t, err)
	assert.Contains(t, err.Error(), userSettingSecretDomain)
	assert.NotContains(t, err.Error(), "keyless-migration-secret-must-not-leak")
	assert.NotContains(t, err.Error(), protectedSetting)
	assert.Equal(t, protectedSetting, rawUserSetting(t, 7152))
}

func TestUserCacheStoresOnlyEncryptedCredentialSetting(t *testing.T) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	useUserCacheMiniRedis(t)
	user := User{
		Username:    "encrypted-cache-setting",
		Password:    "test-password-hash",
		Group:       "default",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	user.SetSetting(dto.UserSetting{
		WebhookSecret: "redis-webhook-secret",
		GotifyToken:   "redis-gotify-token",
	})
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))

	stored, err := common.RDB.HGet(
		t.Context(),
		getUserCacheKey(user.Id),
		"Setting",
	).Result()
	require.NoError(t, err)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	assert.NotContains(t, stored, "redis-webhook-secret")
	assert.NotContains(t, stored, "redis-gotify-token")

	cached, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "redis-webhook-secret", cached.GetSetting().WebhookSecret)
	assert.Equal(t, "redis-gotify-token", cached.GetSetting().GotifyToken)

	fromCache, err := GetUserSetting(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "redis-webhook-secret", fromCache.WebhookSecret)
	assert.Equal(t, "redis-gotify-token", fromCache.GotifyToken)

	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{
		GotifyToken: "updated-redis-gotify-token",
	}))
	stored, err = common.RDB.HGet(
		t.Context(),
		getUserCacheKey(user.Id),
		"Setting",
	).Result()
	require.NoError(t, err)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	assert.NotContains(t, stored, "updated-redis-gotify-token")
	fromCache, err = GetUserSetting(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "updated-redis-gotify-token", fromCache.GotifyToken)
}

func TestPreparationGateEncryptsCredentialSettingOnlyInRedis(t *testing.T) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"false",
	)
	useUserCacheMiniRedis(t)
	user := User{
		Username:    "preparation-cache-setting",
		Password:    "test-password-hash",
		Group:       "default",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	user.SetSetting(dto.UserSetting{WebhookSecret: "staged-webhook-secret"})
	require.NoError(t, DB.Create(&user).Error)
	assert.False(t, common.IsDataEncryptionEnvelope(rawUserSetting(t, user.Id)))

	require.NoError(t, populateUserCache(user))
	stored, err := common.RDB.HGet(
		t.Context(),
		getUserCacheKey(user.Id),
		"Setting",
	).Result()
	require.NoError(t, err)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	assert.NotContains(t, stored, "staged-webhook-secret")
}

func TestPreparationGateWithoutKeyringLeavesCredentialSettingOutOfRedis(
	t *testing.T,
) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(t, "", "", "false")
	server := useUserCacheMiniRedis(t)
	user := User{
		Username:    "keyless-preparation-setting",
		Password:    "test-password-hash",
		Group:       "default",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	user.SetSetting(dto.UserSetting{GotifyToken: "keyless-staged-token"})
	require.NoError(t, DB.Create(&user).Error)
	assert.Contains(t, rawUserSetting(t, user.Id), "keyless-staged-token")

	require.NoError(t, populateUserCache(user))
	assert.False(t, server.Exists(getUserCacheKey(user.Id)))
}

func TestProtectedUserSettingFailsWithoutKeyringAndDoesNotLeak(t *testing.T) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(t, "", "", "true")
	user := &User{
		Username:    "keyless-protected-setting",
		Password:    "test-password-hash",
		AuthVersion: 1,
	}
	user.SetSetting(dto.UserSetting{WebhookSecret: "must-not-leak"})

	err := DB.Create(user).Error
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "must-not-leak")

	legacy := marshalUserSetting(t, dto.UserSetting{
		GotifyToken: "legacy-must-not-leak",
	})
	insertRawUserSetting(t, 7201, "keyless-legacy-setting", legacy, nil)
	err = ValidateReversibleSecretStorage()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "users:setting")
	assert.NotContains(t, err.Error(), "legacy-must-not-leak")
	assert.NotContains(t, err.Error(), legacy)
}
