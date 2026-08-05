package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareOptionSecretTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Option{}, &CustomOAuthProvider{}))
	require.NoError(t, DB.Exec("DELETE FROM options").Error)
	require.NoError(t, DB.Exec("DELETE FROM custom_oauth_providers").Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM options")
		DB.Exec("DELETE FROM custom_oauth_providers")
	})
}

func rawOptionValue(t *testing.T, key string) string {
	t.Helper()
	var stored string
	require.NoError(t, DB.Table("options").
		Select("value").
		Where(commonKeyCol+" = ?", key).
		Scan(&stored).Error)
	return stored
}

func TestProtectedOptionAllowlistEncryptsEveryCurrentSecret(t *testing.T) {
	prepareOptionSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)

	protectedKeys := []string{
		"SMTPToken",
		"WorkerValidKey",
		"EpayKey",
		"StripeApiSecret",
		"StripeWebhookSecret",
		"CreemApiKey",
		"CreemWebhookSecret",
		"WaffoApiKey",
		"WaffoPrivateKey",
		"WaffoSandboxApiKey",
		"WaffoSandboxPrivateKey",
		"WaffoPancakePrivateKey",
		"GitHubClientSecret",
		"LinuxDOClientSecret",
		"TelegramBotToken",
		"WeChatServerToken",
		"TurnstileSecretKey",
		"discord.client_secret",
		"oidc.client_secret",
		"model_deployment.ionet.api_key",
	}
	for _, key := range protectedKeys {
		plaintext := "secret-for-" + key
		require.NoError(t, DB.Create(&Option{Key: key, Value: plaintext}).Error)
		stored := rawOptionValue(t, key)
		assert.True(t, common.IsDataEncryptionEnvelope(stored), key)
		assert.NotContains(t, stored, plaintext, key)
	}

	options, err := AllOption()
	require.NoError(t, err)
	require.Len(t, options, len(protectedKeys))
	for _, option := range options {
		assert.Equal(t, "secret-for-"+option.Key, option.Value)
	}
}

func TestOptionSecretClassificationKeepsKnownPublicKeyPlainAndRejectsUnknownMatch(t *testing.T) {
	prepareOptionSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)

	require.NoError(t, DB.Create(&Option{
		Key:   "TurnstileSiteKey",
		Value: "public-site-key",
	}).Error)
	assert.Equal(t, "public-site-key", rawOptionValue(t, "TurnstileSiteKey"))

	err := DB.Create(&Option{
		Key:   "FutureProviderSecret",
		Value: "unclassified-secret",
	}).Error
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "unclassified-secret")
}

func TestMigrateOptionSecretsIsIdempotentAndNeverBindsPlaintext(t *testing.T) {
	prepareOptionSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const key = "SMTPToken"
	const legacySecret = "legacy-smtp-token"
	require.NoError(t, DB.Table("options").Create(map[string]any{
		"key":   key,
		"value": legacySecret,
	}).Error)

	callbackName := fmt.Sprintf("test:no-option-plaintext-bind:%s", t.Name())
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

	require.NoError(t, MigrateOptionSecrets())
	assert.False(t, plaintextBound)
	first := rawOptionValue(t, key)
	assert.True(t, common.IsDataEncryptionEnvelope(first))
	assert.NotContains(t, first, legacySecret)

	require.NoError(t, MigrateOptionSecrets())
	assert.Equal(t, first, rawOptionValue(t, key))

	options, err := AllOption()
	require.NoError(t, err)
	require.Len(t, options, 1)
	assert.Equal(t, legacySecret, options[0].Value)
}

func TestUpdateOptionPublishesPlaintextButPersistsEnvelope(t *testing.T) {
	prepareOptionSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()

	require.NoError(t, UpdateOption("SMTPToken", "runtime-smtp-token"))
	assert.True(t, common.IsDataEncryptionEnvelope(rawOptionValue(t, "SMTPToken")))

	common.OptionMapRWMutex.RLock()
	published := common.OptionMap["SMTPToken"]
	common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, "runtime-smtp-token", published)
}

func TestInitOptionMapPropagatesEncryptedOptionFailure(t *testing.T) {
	prepareOptionSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const corrupt = "naenc:v1:k1:invalid:invalid"
	require.NoError(t, DB.Table("options").Create(map[string]any{
		"key":   "SMTPToken",
		"value": corrupt,
	}).Error)

	err := InitOptionMap()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), corrupt)
	assert.Contains(t, err.Error(), "options:SMTPToken")
}

func TestValidateReversibleSecretStorageRejectsUnclassifiedOptionKey(t *testing.T) {
	prepareOptionSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	require.NoError(t, DB.Table("options").Create(map[string]any{
		"key":   "FutureProviderToken",
		"value": "future-secret",
	}).Error)

	err := ValidateReversibleSecretStorage()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "future-secret")
	assert.Contains(t, err.Error(), "FutureProviderToken")
}
