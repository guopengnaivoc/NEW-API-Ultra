package model

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useChannelKeyRotationTestDB(t *testing.T) {
	t.Helper()

	previousDB := DB
	previousLogDB := LOG_DB
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()

	dsn := "file:" + filepath.Join(t.TempDir(), "channel-key-rotation.db") +
		"?_pragma=busy_timeout(0)&_pragma=journal_mode(WAL)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)

	DB = db
	LOG_DB = db
	common.SetDatabaseTypes(
		common.DatabaseTypeSQLite,
		common.DatabaseTypeSQLite,
	)
	initCol()
	require.NoError(t, DB.AutoMigrate(&Channel{}))

	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		DB = previousDB
		LOG_DB = previousLogDB
		common.SetDatabaseTypes(
			previousMainDatabaseType,
			previousLogDatabaseType,
		)
		initCol()
	})
}

func seedChannelKeyRotationTest(t *testing.T, id int, key string) {
	t.Helper()
	require.NoError(t, DB.Create(&Channel{
		Id:      id,
		Type:    constant.ChannelTypeCodex,
		Key:     key,
		Name:    "codex-rotation-test",
		Models:  "gpt-test",
		Group:   "default",
		Status:  common.ChannelStatusEnabled,
		Setting: common.GetPointer(`{"proxy":""}`),
	}).Error)
}

func TestRotateChannelKeySerializesSQLiteBeforeCallback(t *testing.T) {
	useChannelKeyRotationTestDB(t)
	const channelID = 63601
	seedChannelKeyRotationTest(t, channelID, "refresh-token-old")

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, _, err := RotateChannelKey(
			context.Background(),
			channelID,
			func(current *Channel) (string, bool, error) {
				close(firstEntered)
				<-releaseFirst
				return "refresh-token-winner", true, nil
			},
		)
		firstDone <- err
	}()
	<-firstEntered

	var losingCallbackCalls atomic.Int32
	_, _, losingErr := RotateChannelKey(
		context.Background(),
		channelID,
		func(current *Channel) (string, bool, error) {
			losingCallbackCalls.Add(1)
			return "refresh-token-loser", true, nil
		},
	)
	close(releaseFirst)

	require.Error(t, losingErr)
	assert.Contains(t, strings.ToLower(losingErr.Error()), "locked")
	assert.Zero(t, losingCallbackCalls.Load())
	require.NoError(t, <-firstDone)

	loaded, err := GetChannelById(channelID, true)
	require.NoError(t, err)
	assert.Equal(t, "refresh-token-winner", loaded.Key)
	assert.NotContains(t, rawChannelKey(t, channelID), "refresh-token-winner")
}

func TestRotateChannelKeyUsesAuthoritativePlaintextAndPersistsEnvelope(t *testing.T) {
	useChannelKeyRotationTestDB(t)
	const channelID = 63602
	seedChannelKeyRotationTest(t, channelID, "refresh-token-authoritative")

	before := rawChannelKey(t, channelID)
	require.True(t, common.IsDataEncryptionEnvelope(before))

	rotated, changed, err := RotateChannelKey(
		context.Background(),
		channelID,
		func(current *Channel) (string, bool, error) {
			assert.Equal(t, "refresh-token-authoritative", current.Key)
			assert.Equal(t, before, current.EncryptedKey)
			return "refresh-token-replacement", true, nil
		},
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, rotated)
	assert.Equal(t, channelID, rotated.Id)
	assert.Equal(t, "refresh-token-replacement", rotated.Key)

	after := rawChannelKey(t, channelID)
	assert.NotEqual(t, before, after)
	assert.True(t, common.IsDataEncryptionEnvelope(after))
	assert.NotContains(t, after, "refresh-token-authoritative")
	assert.NotContains(t, after, "refresh-token-replacement")
}

func TestRotateChannelKeyNoChangePreservesEnvelope(t *testing.T) {
	useChannelKeyRotationTestDB(t)
	const channelID = 63603
	seedChannelKeyRotationTest(t, channelID, "refresh-token-current")
	before := rawChannelKey(t, channelID)

	current, changed, err := RotateChannelKey(
		context.Background(),
		channelID,
		func(current *Channel) (string, bool, error) {
			return "", false, nil
		},
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.NotNil(t, current)
	assert.Equal(t, "refresh-token-current", current.Key)
	assert.Equal(t, before, rawChannelKey(t, channelID))
}

func TestRotateChannelKeyCallbackFailureRollsBack(t *testing.T) {
	useChannelKeyRotationTestDB(t)
	const channelID = 63604
	seedChannelKeyRotationTest(t, channelID, "refresh-token-before-error")
	before := rawChannelKey(t, channelID)
	callbackErr := errors.New("provider refresh rejected")

	_, changed, err := RotateChannelKey(
		context.Background(),
		channelID,
		func(current *Channel) (string, bool, error) {
			return "", false, callbackErr
		},
	)
	require.ErrorIs(t, err, callbackErr)
	assert.False(t, changed)
	assert.Equal(t, before, rawChannelKey(t, channelID))
}

func TestRotateChannelKeyPreparationModeDoesNotBindPlaintextCredential(t *testing.T) {
	useChannelKeyRotationTestDB(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"false",
	)
	const (
		channelID = 63605
		oldKey    = "refresh-token-preparation-old"
		newKey    = "refresh-token-preparation-new"
	)
	seedChannelKeyRotationTest(t, channelID, oldKey)

	callbackName := fmt.Sprintf("test:no-rotation-plaintext-predicate:%s", t.Name())
	var plaintextBound atomic.Bool
	require.NoError(t, DB.Callback().Update().After("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			for _, variable := range tx.Statement.Vars {
				if value, ok := variable.(string); ok && value == oldKey {
					plaintextBound.Store(true)
				}
			}
		}))
	t.Cleanup(func() {
		DB.Callback().Update().Remove(callbackName)
	})

	rotated, changed, err := RotateChannelKey(
		context.Background(),
		channelID,
		func(current *Channel) (string, bool, error) {
			assert.Equal(t, oldKey, current.Key)
			return newKey, true, nil
		},
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, rotated)
	assert.Equal(t, newKey, rotated.Key)
	assert.Equal(t, newKey, rawChannelKey(t, channelID))
	assert.False(t, plaintextBound.Load())
}

func TestRotateChannelKeyEncryptedModeRetainsEnvelopeCAS(t *testing.T) {
	useChannelKeyRotationTestDB(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const channelID = 63606
	seedChannelKeyRotationTest(t, channelID, "refresh-token-encrypted-old")
	originalEnvelope := rawChannelKey(t, channelID)
	require.True(t, common.IsDataEncryptionEnvelope(originalEnvelope))

	callbackName := fmt.Sprintf("test:rotation-envelope-cas:%s", t.Name())
	var envelopeBound atomic.Bool
	require.NoError(t, DB.Callback().Update().After("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			for _, variable := range tx.Statement.Vars {
				if value, ok := variable.(string); ok && value == originalEnvelope {
					envelopeBound.Store(true)
				}
			}
		}))
	t.Cleanup(func() {
		DB.Callback().Update().Remove(callbackName)
	})

	_, changed, err := RotateChannelKey(
		context.Background(),
		channelID,
		func(current *Channel) (string, bool, error) {
			return "refresh-token-encrypted-new", true, nil
		},
	)
	require.NoError(t, err)
	require.True(t, changed)
	assert.True(t, envelopeBound.Load())
	assert.True(t, common.IsDataEncryptionEnvelope(rawChannelKey(t, channelID)))
}
