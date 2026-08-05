package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareChannelSecretTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(
		&Channel{},
		&Ability{},
		&CustomOAuthProvider{},
		&Option{},
		&TwoFA{},
	))
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	require.NoError(t, DB.Exec("DELETE FROM custom_oauth_providers").Error)
	require.NoError(t, DB.Exec("DELETE FROM options").Error)
	require.NoError(t, DB.Exec("DELETE FROM two_fas").Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM abilities")
		DB.Exec("DELETE FROM channels")
		DB.Exec("DELETE FROM custom_oauth_providers")
		DB.Exec("DELETE FROM options")
		DB.Exec("DELETE FROM two_fas")
	})
}

func insertRawChannel(t *testing.T, id int, name string, storedKey string) {
	t.Helper()
	require.NoError(t, DB.Table("channels").Create(map[string]any{
		"id":     id,
		"name":   name,
		"key":    storedKey,
		"models": "gpt-test",
		"group":  "default",
		"status": common.ChannelStatusEnabled,
	}).Error)
}

func rawChannelKey(t *testing.T, id int) string {
	t.Helper()
	var stored string
	require.NoError(t, DB.Table("channels").
		Select(commonKeyCol).
		Where("id = ?", id).
		Scan(&stored).Error)
	return stored
}

func TestChannelEncryptsKeyAtRestAndRoundTrips(t *testing.T) {
	prepareChannelSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const key = "provider-key-one\nprovider-key-two"
	channel := &Channel{
		Name:   "encrypted-channel",
		Key:    key,
		Models: "gpt-test",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, DB.Create(channel).Error)

	stored := rawChannelKey(t, channel.Id)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	assert.NotContains(t, stored, "provider-key-one")
	assert.NotContains(t, stored, "provider-key-two")

	loaded, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, key, loaded.Key)
	assert.Equal(t, []string{"provider-key-one", "provider-key-two"}, loaded.GetKeys())
}

func TestChannelQueriesThatOmitKeyDoNotDecryptOrExposeCredential(t *testing.T) {
	prepareChannelSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	channel := &Channel{
		Name:   "omitted-key-channel",
		Key:    "omitted-provider-credential",
		Models: "gpt-test",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, DB.Create(channel).Error)

	loaded, err := GetChannelById(channel.Id, false)
	require.NoError(t, err)
	assert.Empty(t, loaded.Key)
	assert.Empty(t, loaded.EncryptedKey)

	channels, err := GetAllChannels(0, 10, false, false)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	assert.Empty(t, channels[0].Key)
	assert.Empty(t, channels[0].EncryptedKey)

	before := rawChannelKey(t, channel.Id)
	full, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	full.Status = common.ChannelStatusManuallyDisabled
	require.NoError(t, full.SaveWithoutKey())
	assert.Equal(t, before, rawChannelKey(t, channel.Id))
}

func TestMigrateChannelSecretsIsIdempotentAndNeverBindsPlaintext(t *testing.T) {
	prepareChannelSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const legacyKey = "legacy-provider-key"
	insertRawChannel(t, 6101, "legacy-channel", legacyKey)

	callbackName := fmt.Sprintf("test:no-channel-plaintext-bind:%s", t.Name())
	var plaintextBound bool
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			for _, variable := range tx.Statement.Vars {
				value, ok := variable.(string)
				if ok && value == legacyKey {
					plaintextBound = true
				}
			}
		}))
	t.Cleanup(func() {
		DB.Callback().Update().Remove(callbackName)
	})

	require.NoError(t, MigrateChannelSecrets())
	first := rawChannelKey(t, 6101)
	assert.True(t, common.IsDataEncryptionEnvelope(first))
	assert.False(t, plaintextBound)

	require.NoError(t, MigrateChannelSecrets())
	assert.Equal(t, first, rawChannelKey(t, 6101))
	loaded, err := GetChannelById(6101, true)
	require.NoError(t, err)
	assert.Equal(t, legacyKey, loaded.Key)
}

func TestUpdateChannelKeyPersistsEnvelope(t *testing.T) {
	prepareChannelSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	insertRawChannel(t, 6201, "refresh-channel", "")

	require.NoError(t, UpdateChannelKey(6201, "refreshed-provider-key"))
	stored := rawChannelKey(t, 6201)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	assert.NotContains(t, stored, "refreshed-provider-key")

	loaded, err := GetChannelById(6201, true)
	require.NoError(t, err)
	assert.Equal(t, "refreshed-provider-key", loaded.Key)
}

func TestChannelUpdateEncryptsNewKeyAndPreservesOmittedKey(t *testing.T) {
	prepareChannelSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	channel := &Channel{
		Name:   "update-channel",
		Key:    "original-provider-key",
		Models: "gpt-test",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, channel.Insert())

	update := &Channel{
		Id:   channel.Id,
		Name: "updated-channel",
		Key:  "rotated-provider-key",
	}
	require.NoError(t, update.Update())
	stored := rawChannelKey(t, channel.Id)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	assert.NotContains(t, stored, "rotated-provider-key")
	assert.Equal(t, "rotated-provider-key", update.Key)

	withoutKey := &Channel{Id: channel.Id, Name: "renamed-channel"}
	require.NoError(t, withoutKey.Update())
	assert.Equal(t, stored, rawChannelKey(t, channel.Id))
	assert.Equal(t, "rotated-provider-key", withoutKey.Key)
}

func TestBatchInsertChannelsEncryptsEveryKey(t *testing.T) {
	prepareChannelSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	channels := []Channel{
		{
			Name:   "batch-one",
			Key:    "batch-provider-key-one",
			Models: "gpt-test",
			Group:  "default",
			Status: common.ChannelStatusEnabled,
		},
		{
			Name:   "batch-two",
			Key:    "batch-provider-key-two",
			Models: "gpt-test",
			Group:  "default",
			Status: common.ChannelStatusEnabled,
		},
	}
	require.NoError(t, BatchInsertChannels(channels))

	var stored []storedChannelSecret
	require.NoError(t, DB.Table("channels").
		Select("id, "+commonKeyCol+" AS stored_key").
		Order("id").
		Scan(&stored).Error)
	require.Len(t, stored, 2)
	for _, row := range stored {
		assert.True(t, common.IsDataEncryptionEnvelope(row.StoredKey))
		assert.NotContains(t, row.StoredKey, "batch-provider-key")
	}
}

func TestChannelSearchNeverBindsCredential(t *testing.T) {
	prepareChannelSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const key = "credential-must-not-enter-search-sql"
	tag := "search-tag"
	channel := &Channel{
		Name:   "unrelated-name",
		Key:    key,
		Models: "gpt-test",
		Group:  "default",
		Tag:    &tag,
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, DB.Create(channel).Error)

	callbackName := fmt.Sprintf("test:no-channel-search-key-bind:%s", t.Name())
	var credentialBound bool
	require.NoError(t, DB.Callback().Query().Before("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			for _, variable := range tx.Statement.Vars {
				value, ok := variable.(string)
				if ok && value == key {
					credentialBound = true
				}
			}
		}))
	t.Cleanup(func() {
		DB.Callback().Query().Remove(callbackName)
	})

	channels, err := SearchChannels(key, "", "gpt-test", false)
	require.NoError(t, err)
	assert.Empty(t, channels)
	tags, err := SearchTags(key, "", "gpt-test", false)
	require.NoError(t, err)
	assert.Empty(t, tags)
	assert.False(t, credentialBound)
}

func TestInitChannelCacheRejectsCorruptEnvelopeWithoutPartialPublication(
	t *testing.T,
) {
	prepareChannelSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
	})

	good := &Channel{
		Id:     6301,
		Name:   "known-good-channel",
		Key:    "known-good-key",
		Models: "gpt-test",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, DB.Create(good).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-test",
		ChannelId: good.Id,
		Enabled:   true,
	}).Error)
	require.NoError(t, InitChannelCache())
	published, err := CacheGetChannel(good.Id)
	require.NoError(t, err)
	assert.Equal(t, "known-good-key", published.Key)

	const corrupt = "naenc:v1:k1:not-a-valid-wrap:not-a-valid-payload"
	insertRawChannel(t, 6302, "corrupt-channel", corrupt)
	err = InitChannelCache()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), corrupt)

	published, err = CacheGetChannel(good.Id)
	require.NoError(t, err)
	assert.Equal(t, "known-good-key", published.Key)
	_, err = CacheGetChannel(6302)
	require.Error(t, err)
}
