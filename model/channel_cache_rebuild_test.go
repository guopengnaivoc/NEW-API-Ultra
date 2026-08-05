package model

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestInitChannelCacheSerializesLoadThroughPublication(t *testing.T) {
	useChannelKeyRotationTestDB(t)
	require.NoError(t, DB.AutoMigrate(&Ability{}))
	configureModelDataEncryption(t, "", "", "false")

	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
	})

	channelSyncLock.Lock()
	previousGroups := group2model2channels
	previousChannels := channelsIDM
	previousAdvancedConfigs := channel2advancedCustomConfig
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		group2model2channels = previousGroups
		channelsIDM = previousChannels
		channel2advancedCustomConfig = previousAdvancedConfigs
		channelSyncLock.Unlock()
	})

	const channelID = 63641
	require.NoError(t, DB.Create(&Channel{
		Id:     channelID,
		Type:   constant.ChannelTypeCodex,
		Key:    "refresh-token-cache-old",
		Name:   "codex-cache-rebuild-test",
		Models: "gpt-test",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-test",
		ChannelId: channelID,
		Enabled:   true,
	}).Error)

	firstLoaded := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseFirstOnce sync.Once
	releaseFirstLoad := func() {
		releaseFirstOnce.Do(func() {
			close(releaseFirst)
		})
	}
	t.Cleanup(releaseFirstLoad)

	callbackName := "test:block-first-channel-cache-snapshot"
	var channelQueries atomic.Int32
	require.NoError(t, DB.Callback().Query().After("gorm:after_query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Schema == nil ||
				tx.Statement.Schema.Table != "channels" {
				return
			}
			if channelQueries.Add(1) != 1 {
				return
			}
			close(firstLoaded)
			<-releaseFirst
		}))
	t.Cleanup(func() {
		DB.Callback().Query().Remove(callbackName)
	})

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- InitChannelCache()
	}()
	<-firstLoaded

	rebuildLockHeld := true
	if channelCacheRebuildLock.TryLock() {
		rebuildLockHeld = false
		channelCacheRebuildLock.Unlock()
	}

	require.NoError(t, UpdateChannelKey(channelID, "refresh-token-cache-new"))
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- InitChannelCache()
	}()

	var firstErr error
	var secondErr error
	if rebuildLockHeld {
		releaseFirstLoad()
		firstErr = <-firstDone
		secondErr = <-secondDone
	} else {
		secondErr = <-secondDone
		releaseFirstLoad()
		firstErr = <-firstDone
	}
	require.NoError(t, firstErr)
	require.NoError(t, secondErr)
	assert.True(t, rebuildLockHeld)

	published, err := CacheGetChannel(channelID)
	require.NoError(t, err)
	assert.Equal(t, "refresh-token-cache-new", published.Key)
}
