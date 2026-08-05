package model

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const customOAuthSecretDomain = "custom_oauth_providers:client_secret"

func modelTestDataEncryptionKey(fill byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(fill), 32)))
}

func configureModelDataEncryption(
	t *testing.T,
	keys string,
	activeKeyID string,
	enabled string,
) {
	t.Helper()
	t.Cleanup(func() {
		require.NoError(t, common.InitDataEncryption())
	})
	t.Setenv("DATA_ENCRYPTION_KEYS", keys)
	t.Setenv("DATA_ENCRYPTION_ACTIVE_KEY_ID", activeKeyID)
	t.Setenv("DATA_ENCRYPTION_ENABLE", enabled)
	require.NoError(t, common.InitDataEncryption())
}

func prepareCustomOAuthSecretTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&CustomOAuthProvider{}))
	require.NoError(t, DB.Exec("DELETE FROM custom_oauth_providers").Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM custom_oauth_providers")
	})
}

func useFileBackedCustomOAuthSecretTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousMainDatabaseType := common.MainDatabaseType()
	dsn := "file:" + filepath.Join(t.TempDir(), "migration-race.db") +
		"?_pragma=busy_timeout(50)&_pragma=journal_mode(WAL)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	initCol()
	require.NoError(t, DB.AutoMigrate(&CustomOAuthProvider{}))
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousMainDatabaseType)
		initCol()
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func insertRawCustomOAuthProvider(
	t *testing.T,
	id int,
	slug string,
	storedSecret string,
) {
	t.Helper()
	require.NoError(t, DB.Table("custom_oauth_providers").Create(map[string]any{
		"id":                     id,
		"name":                   slug,
		"slug":                   slug,
		"client_id":              "client-id",
		"client_secret":          storedSecret,
		"authorization_endpoint": "https://issuer.example/authorize",
		"token_endpoint":         "https://issuer.example/token",
		"user_info_endpoint":     "https://issuer.example/userinfo",
		"scopes":                 "openid",
		"user_id_field":          "sub",
		"username_field":         "preferred_username",
		"display_name_field":     "name",
		"email_field":            "email",
		"well_known":             "",
		"access_policy":          "",
		"access_denied_message":  "",
	}).Error)
}

func rawCustomOAuthSecret(t *testing.T, id int) string {
	t.Helper()
	var stored string
	require.NoError(t, DB.Table("custom_oauth_providers").
		Select("client_secret").
		Where("id = ?", id).
		Scan(&stored).Error)
	return stored
}

func TestCustomOAuthProviderEncryptsSecretAtRest(t *testing.T) {
	prepareCustomOAuthSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)

	provider := &CustomOAuthProvider{
		Name:                  "Example",
		Slug:                  "example",
		ClientId:              "client-id",
		ClientSecret:          "oauth-client-secret",
		AuthorizationEndpoint: "https://issuer.example/authorize",
		TokenEndpoint:         "https://issuer.example/token",
		UserInfoEndpoint:      "https://issuer.example/userinfo",
	}
	require.NoError(t, DB.Create(provider).Error)

	stored := rawCustomOAuthSecret(t, provider.Id)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	assert.NotContains(t, stored, provider.ClientSecret)

	loaded, err := GetCustomOAuthProviderById(provider.Id)
	require.NoError(t, err)
	assert.Equal(t, "oauth-client-secret", loaded.ClientSecret)
}

func TestMigrateCustomOAuthProviderSecretsIsIdempotentAndNeverBindsPlaintext(t *testing.T) {
	prepareCustomOAuthSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const legacySecret = "legacy-oauth-client-secret"
	insertRawCustomOAuthProvider(t, 4101, "legacy", legacySecret)

	callbackName := fmt.Sprintf("test:no-plaintext-bind:%s", t.Name())
	var plaintextBound bool
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			for _, variable := range tx.Statement.Vars {
				if value, ok := variable.(string); ok && value == legacySecret {
					plaintextBound = true
				}
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(callbackName))
	})

	require.NoError(t, MigrateCustomOAuthProviderSecrets())
	assert.False(t, plaintextBound)
	first := rawCustomOAuthSecret(t, 4101)
	assert.True(t, common.IsDataEncryptionEnvelope(first))
	assert.NotContains(t, first, legacySecret)

	require.NoError(t, MigrateCustomOAuthProviderSecrets())
	assert.Equal(t, first, rawCustomOAuthSecret(t, 4101))

	loaded, err := GetCustomOAuthProviderById(4101)
	require.NoError(t, err)
	assert.Equal(t, legacySecret, loaded.ClientSecret)
}

func TestCustomOAuthPlaintextMigrationLocksReadAndWriteInOneTransaction(
	t *testing.T,
) {
	prepareCustomOAuthSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	insertRawCustomOAuthProvider(
		t,
		4151,
		"locked-plaintext-migration",
		"locked-legacy-client-secret",
	)

	var readPool any
	var writePool any
	queryCallback := fmt.Sprintf("test:capture-migration-read-tx:%s", t.Name())
	updateCallback := fmt.Sprintf("test:capture-migration-write-tx:%s", t.Name())
	require.NoError(t, DB.Callback().Row().Before("gorm:row").
		Register(queryCallback, func(tx *gorm.DB) {
			if readPool == nil &&
				tx.Statement.Table == "custom_oauth_providers" {
				readPool = tx.Statement.ConnPool
			}
		}))
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			if writePool == nil &&
				tx.Statement.Table == "custom_oauth_providers" {
				writePool = tx.Statement.ConnPool
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Row().Remove(queryCallback))
		require.NoError(t, DB.Callback().Update().Remove(updateCallback))
	})

	require.NoError(t, MigrateCustomOAuthProviderSecrets())
	require.NotNil(t, readPool)
	require.NotNil(t, writePool)
	assert.Equal(t, readPool, writePool)
	_, readUsesTransaction := readPool.(gorm.TxCommitter)
	_, writeUsesTransaction := writePool.(gorm.TxCommitter)
	assert.True(t, readUsesTransaction)
	assert.True(t, writeUsesTransaction)

	loaded, err := GetCustomOAuthProviderById(4151)
	require.NoError(t, err)
	assert.Equal(t, "locked-legacy-client-secret", loaded.ClientSecret)
}

func TestCustomOAuthPlaintextMigrationNeverOverwritesConcurrentCommittedWrite(
	t *testing.T,
) {
	useFileBackedCustomOAuthSecretTestDB(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const (
		rowID         = 4161
		staleSecret   = "stale-legacy-client-secret"
		currentSecret = "concurrent-legacy-client-secret"
	)
	insertRawCustomOAuthProvider(
		t,
		rowID,
		"concurrent-plaintext-migration",
		staleSecret,
	)

	readLocked := make(chan struct{})
	writerDone := make(chan struct{})
	var writerErr error
	var signalRead sync.Once
	queryCallback := fmt.Sprintf("test:pause-after-locked-read:%s", t.Name())
	updateCallback := fmt.Sprintf("test:wait-for-concurrent-write:%s", t.Name())
	require.NoError(t, DB.Callback().Row().After("gorm:row").
		Register(queryCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == "custom_oauth_providers" {
				signalRead.Do(func() { close(readLocked) })
			}
		}))
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == "custom_oauth_providers" {
				<-writerDone
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Row().Remove(queryCallback))
		require.NoError(t, DB.Callback().Update().Remove(updateCallback))
	})

	go func() {
		<-readLocked
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		writerErr = DB.WithContext(ctx).Exec(
			"UPDATE custom_oauth_providers SET client_secret = ? WHERE id = ?",
			currentSecret,
			rowID,
		).Error
		close(writerDone)
	}()

	migrationErr := MigrateCustomOAuthProviderSecrets()
	<-writerDone
	require.NoError(t, writerErr)
	require.Error(t, migrationErr)
	stored := rawCustomOAuthSecret(t, rowID)
	assert.Equal(t, currentSecret, stored)

	require.NoError(t, MigrateCustomOAuthProviderSecrets())
	stored = rawCustomOAuthSecret(t, rowID)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	plaintext, _, err := common.OpenDataEncryptionValue(
		customOAuthSecretDomain,
		stored,
	)
	require.NoError(t, err)
	assert.Equal(t, currentSecret, plaintext)
}

func TestMigrateCustomOAuthProviderSecretsRewrapsOldKeyWithoutPayloadRewrite(t *testing.T) {
	prepareCustomOAuthSecretTest(t)
	configureModelDataEncryption(
		t,
		"old="+modelTestDataEncryptionKey('a'),
		"old",
		"true",
	)

	provider := &CustomOAuthProvider{
		Name:                  "Rotation",
		Slug:                  "rotation",
		ClientId:              "client-id",
		ClientSecret:          "rotation-secret",
		AuthorizationEndpoint: "https://issuer.example/authorize",
		TokenEndpoint:         "https://issuer.example/token",
		UserInfoEndpoint:      "https://issuer.example/userinfo",
	}
	require.NoError(t, DB.Create(provider).Error)
	before := rawCustomOAuthSecret(t, provider.Id)
	beforeParts := strings.Split(before, ":")
	require.Len(t, beforeParts, 5)

	configureModelDataEncryption(
		t,
		"old="+modelTestDataEncryptionKey('a')+",new="+modelTestDataEncryptionKey('b'),
		"new",
		"true",
	)
	require.NoError(t, MigrateCustomOAuthProviderSecrets())
	after := rawCustomOAuthSecret(t, provider.Id)
	afterParts := strings.Split(after, ":")
	require.Len(t, afterParts, 5)
	assert.Equal(t, "new", afterParts[2])
	assert.Equal(t, beforeParts[4], afterParts[4])

	loaded, err := GetCustomOAuthProviderById(provider.Id)
	require.NoError(t, err)
	assert.Equal(t, "rotation-secret", loaded.ClientSecret)
}

func TestCustomOAuthRotationDoesNotOverwriteConcurrentOldKeyWrite(t *testing.T) {
	useFileBackedCustomOAuthSecretTestDB(t)
	keyring := "old=" + modelTestDataEncryptionKey('a') +
		",new=" + modelTestDataEncryptionKey('b')
	configureModelDataEncryption(t, keyring, "old", "true")
	staleEnvelope, err := common.SealDataEncryptionValue(
		customOAuthSecretDomain,
		"stale-client-secret",
	)
	require.NoError(t, err)
	concurrentEnvelope, err := common.SealDataEncryptionValue(
		customOAuthSecretDomain,
		"concurrent-client-secret",
	)
	require.NoError(t, err)
	insertRawCustomOAuthProvider(t, 4301, "rotation-race", staleEnvelope)

	configureModelDataEncryption(t, keyring, "new", "true")
	readLocked := make(chan struct{})
	writerDone := make(chan struct{})
	var writerErr error
	var signalRead sync.Once
	queryCallback := fmt.Sprintf("test:pause-after-rotation-read:%s", t.Name())
	updateCallback := fmt.Sprintf("test:wait-for-rotation-write:%s", t.Name())
	require.NoError(t, DB.Callback().Row().After("gorm:row").
		Register(queryCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == "custom_oauth_providers" {
				signalRead.Do(func() { close(readLocked) })
			}
		}))
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == "custom_oauth_providers" {
				<-writerDone
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Row().Remove(queryCallback))
		require.NoError(t, DB.Callback().Update().Remove(updateCallback))
	})

	go func() {
		<-readLocked
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		writerErr = DB.WithContext(ctx).Exec(
			"UPDATE custom_oauth_providers SET client_secret = ? WHERE id = ?",
			concurrentEnvelope,
			4301,
		).Error
		close(writerDone)
	}()

	migrationErr := MigrateCustomOAuthProviderSecrets()
	<-writerDone
	require.NoError(t, writerErr)
	require.Error(t, migrationErr)
	assert.Equal(t, concurrentEnvelope, rawCustomOAuthSecret(t, 4301))

	require.NoError(t, MigrateCustomOAuthProviderSecrets())
	loaded, err := GetCustomOAuthProviderById(4301)
	require.NoError(t, err)
	assert.Equal(t, "concurrent-client-secret", loaded.ClientSecret)
}

func TestValidateReversibleSecretStorageFailsClosedWithoutLeakingStoredValue(t *testing.T) {
	prepareCustomOAuthSecretTest(t)
	configureModelDataEncryption(t, "", "", "true")
	const legacySecret = "startup-plaintext-oauth-secret"
	insertRawCustomOAuthProvider(t, 4102, "startup-failure", legacySecret)

	err := ValidateReversibleSecretStorage()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), legacySecret)
	assert.Contains(t, err.Error(), "DATA_ENCRYPTION_KEYS")
	assert.Contains(t, err.Error(), "DATA_ENCRYPTION_ACTIVE_KEY_ID")
	assert.Contains(t, err.Error(), "DATA_ENCRYPTION_ENABLE=false")
}

func TestValidateReversibleSecretStorageRejectsCorruptEnvelope(t *testing.T) {
	prepareCustomOAuthSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const corrupt = "naenc:v1:k1:invalid:invalid"
	insertRawCustomOAuthProvider(t, 4103, "corrupt", corrupt)

	err := ValidateReversibleSecretStorage()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), corrupt)
	assert.Contains(t, err.Error(), customOAuthSecretDomain)
}
