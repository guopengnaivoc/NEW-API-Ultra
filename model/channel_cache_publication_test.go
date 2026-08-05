package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useChannelCachePublicationFixture seeds one channel plus its ability and
// publishes the cache, restoring the previous global cache state afterwards.
func useChannelCachePublicationFixture(t *testing.T, channelID int, seed func(*Channel)) {
	t.Helper()
	useChannelKeyRotationTestDB(t)
	require.NoError(t, DB.AutoMigrate(&Ability{}))
	configureModelDataEncryption(t, "", "", "false")

	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemoryCacheEnabled })

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
		channelPollingCursors.Delete(channelID)
	})

	channel := &Channel{
		Id:     channelID,
		Type:   constant.ChannelTypeCodex,
		Key:    "cache-publication-key",
		Name:   "cache-publication",
		Models: "gpt-test",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}
	if seed != nil {
		seed(channel)
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-test",
		ChannelId: channelID,
		Enabled:   true,
	}).Error)
	require.NoError(t, InitChannelCache())
}

// A channel selected for a request is read field by field without holding
// channelSyncLock for the whole request, so a status flip must publish a new
// snapshot rather than write through the pointer the reader already holds.
func TestCacheUpdateChannelStatusRepublishesInsteadOfMutatingSharedChannel(t *testing.T) {
	const channelID = 91021
	useChannelCachePublicationFixture(t, channelID, nil)

	selected, err := CacheGetChannel(channelID)
	require.NoError(t, err)
	require.Equal(t, common.ChannelStatusEnabled, selected.Status)

	CacheUpdateChannelStatus(channelID, common.ChannelStatusAutoDisabled)

	assert.Equal(t, common.ChannelStatusEnabled, selected.Status,
		"the snapshot an in-flight request already holds must not be rewritten underneath it")

	republished, err := CacheGetChannel(channelID)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, republished.Status)
	assert.NotSame(t, selected, republished)

	// A disabled channel must also leave the routing table, which is the other
	// half of what CacheUpdateChannelStatus is responsible for.
	assert.False(t, IsChannelEnabledForGroupModel("default", "gpt-test", channelID))
}

// Republication must deep-copy ChannelInfo: a shallow struct copy still aliases
// the per-key maps, so a key-status write would remain visible through the old
// pointer and could be observed mid-write by a concurrent reader.
func TestCacheMutateChannelDoesNotAliasMultiKeyMaps(t *testing.T) {
	const channelID = 91022
	useChannelCachePublicationFixture(t, channelID, func(channel *Channel) {
		channel.Key = "key-a\nkey-b"
		channel.ChannelInfo = ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       2,
			MultiKeyMode:       constant.MultiKeyModePolling,
			MultiKeyStatusList: map[int]int{},
		}
	})

	selected, err := CacheGetChannel(channelID)
	require.NoError(t, err)

	require.True(t, CacheMutateChannel(channelID, func(channel *Channel) {
		channel.ChannelInfo.MultiKeyStatusList[1] = common.ChannelStatusAutoDisabled
		channel.ChannelInfo.MultiKeyDisabledReason = map[int]string{1: "quota exhausted"}
	}))

	assert.Empty(t, selected.ChannelInfo.MultiKeyStatusList,
		"the previously published ChannelInfo maps must not be shared with the clone")
	assert.Nil(t, selected.ChannelInfo.MultiKeyDisabledReason)

	republished, err := CacheGetChannel(channelID)
	require.NoError(t, err)
	assert.Equal(t, map[int]int{1: common.ChannelStatusAutoDisabled}, republished.ChannelInfo.MultiKeyStatusList)
	assert.Equal(t, map[int]string{1: "quota exhausted"}, republished.ChannelInfo.MultiKeyDisabledReason)

	assert.False(t, CacheMutateChannel(channelID+1, func(*Channel) {
		t.Fatal("mutate must not run for an unknown channel")
	}))
}

// GetNextEnabledKey used to advance the rotation cursor by writing to the very
// struct published in the cache, which raced every reader of that channel. The
// cursor now lives outside the shared object and still rotates.
func TestGetNextEnabledKeyRotatesWithoutWritingTheSharedChannel(t *testing.T) {
	const channelID = 91023
	useChannelCachePublicationFixture(t, channelID, func(channel *Channel) {
		channel.Key = "key-a\nkey-b\nkey-c"
		channel.ChannelInfo = ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 3,
			MultiKeyMode: constant.MultiKeyModePolling,
		}
	})

	selected, err := CacheGetChannel(channelID)
	require.NoError(t, err)

	var picked []string
	for i := 0; i < 4; i++ {
		key, index, apiErr := selected.GetNextEnabledKey()
		require.Nil(t, apiErr)
		assert.Equal(t, i%3, index)
		picked = append(picked, key)
	}
	assert.Equal(t, []string{"key-a", "key-b", "key-c", "key-a"}, picked)
	assert.Zero(t, selected.ChannelInfo.MultiKeyPollingIndex,
		"the published channel must stay immutable while the cursor advances")
}

// Reproduces NA-ISSUE-0120 under -race: a status flip concurrent with the
// lock-free field reads the relay path performs on an already-selected channel.
func TestCachedChannelStaysRaceFreeUnderConcurrentStatusUpdates(t *testing.T) {
	const channelID = 91024
	useChannelCachePublicationFixture(t, channelID, func(channel *Channel) {
		channel.Key = "key-a\nkey-b"
		channel.ChannelInfo = ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       2,
			MultiKeyMode:       constant.MultiKeyModePolling,
			MultiKeyStatusList: map[int]int{},
		}
	})

	const iterations = 500
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			CacheUpdateChannelStatus(channelID, common.ChannelStatusAutoDisabled)
			CacheUpdateChannelStatus(channelID, common.ChannelStatusEnabled)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			CacheMutateChannel(channelID, func(channel *Channel) {
				channel.ChannelInfo.MultiKeyStatusList[i%2] = common.ChannelStatusAutoDisabled
				delete(channel.ChannelInfo.MultiKeyStatusList, i%2)
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			channel, err := CacheGetChannel(channelID)
			if err != nil || channel == nil {
				continue
			}
			// Mirrors the relay path: the selected channel is read field by
			// field long after channelSyncLock was released.
			_ = channel.Status
			_ = channel.GetPriority()
			_, _, _ = channel.GetNextEnabledKey()
			for range channel.ChannelInfo.MultiKeyStatusList {
			}
		}
	}()
	wg.Wait()
}
