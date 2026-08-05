package model

import (
	"math"
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOptionValueRejectsInvalidModelRequestRateLimitScalars(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{key: "ModelRequestRateLimitDurationMinutes", value: "-1"},
		{key: "ModelRequestRateLimitDurationMinutes", value: strconv.FormatInt(math.MaxInt64, 10)},
		{key: "ModelRequestRateLimitCount", value: "-1"},
		{key: "ModelRequestRateLimitCount", value: "2147483648"},
		{key: "ModelRequestRateLimitSuccessCount", value: "-1"},
		{key: "ModelRequestRateLimitSuccessCount", value: "2147483648"},
		{key: "ModelRequestRateLimitSuccessCount", value: "not-a-number"},
	}

	for _, test := range tests {
		t.Run(test.key+"="+test.value, func(t *testing.T) {
			require.Error(t, validateOptionValue(test.key, test.value))
		})
	}
}

func TestUpdateOptionDoesNotPersistInvalidModelRequestRateLimitScalar(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	err := UpdateOption("ModelRequestRateLimitSuccessCount", "-1")

	require.Error(t, err)
	requireOptionMissing(t, db, "ModelRequestRateLimitSuccessCount")
}

func TestUpdateOptionAppliesUnlimitedModelRequestRateLimitSuccessCount(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	previousDefaults := setting.GetModelRequestRateLimitDefaults()
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		require.NoError(t, setting.SetModelRequestRateLimitSuccessCount(previousDefaults.SuccessCount))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	require.NoError(t, UpdateOption("ModelRequestRateLimitSuccessCount", "0"))

	assert.Equal(t, "0", requireOptionValue(t, db, "ModelRequestRateLimitSuccessCount"))
	common.OptionMapRWMutex.RLock()
	published := common.OptionMap["ModelRequestRateLimitSuccessCount"]
	common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, "0", published)
	assert.Zero(t, setting.GetModelRequestRateLimitDefaults().SuccessCount)
}

func TestUpdateOptionDoesNotPersistInvalidModelRequestRateLimitGroup(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	err := UpdateOption("ModelRequestRateLimitGroup", `{"unsafe":[-1,1]}`)

	require.Error(t, err)
	requireOptionMissing(t, db, "ModelRequestRateLimitGroup")
}

func TestUpdateOptionMapRejectsInvalidModelRequestRateLimitGroupWithoutPublishingIt(t *testing.T) {
	previousGroupJSON := setting.ModelRequestRateLimitGroup2JSONString()
	require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(
		`{"existing":[40,30]}`,
	))
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = map[string]string{
		"ModelRequestRateLimitGroup": `{"existing":[40,30]}`,
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(previousGroupJSON))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	err := updateOptionMap("ModelRequestRateLimitGroup", `{"unsafe":[-1,1]}`)

	require.Error(t, err)
	common.OptionMapRWMutex.RLock()
	published := common.OptionMap["ModelRequestRateLimitGroup"]
	common.OptionMapRWMutex.RUnlock()
	assert.JSONEq(t, `{"existing":[40,30]}`, published)
	total, success, found := setting.GetGroupRateLimit("existing")
	assert.True(t, found)
	assert.Equal(t, 40, total)
	assert.Equal(t, 30, success)
}

func TestModelRequestRateLimitOptionUpdatesAreSynchronizedWithRequestSnapshots(t *testing.T) {
	previousDefaults := setting.GetModelRequestRateLimitDefaults()
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		setting.SetModelRequestRateLimitEnabled(previousDefaults.Enabled)
		require.NoError(t, setting.SetModelRequestRateLimitDurationMinutes(previousDefaults.DurationMinutes))
		require.NoError(t, setting.SetModelRequestRateLimitCount(previousDefaults.TotalCount))
		require.NoError(t, setting.SetModelRequestRateLimitSuccessCount(previousDefaults.SuccessCount))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	const iterations = 100
	start := make(chan struct{})
	errs := make(chan error, 1)
	var workers sync.WaitGroup

	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < iterations; i++ {
			updates := []struct {
				key   string
				value string
			}{
				{key: "ModelRequestRateLimitEnabled", value: strconv.FormatBool(i%2 == 0)},
				{key: "ModelRequestRateLimitDurationMinutes", value: strconv.Itoa(i%10 + 1)},
				{key: "ModelRequestRateLimitCount", value: strconv.Itoa(i)},
				{key: "ModelRequestRateLimitSuccessCount", value: strconv.Itoa(i + 1)},
			}
			for _, update := range updates {
				if err := updateOptionMap(update.key, update.value); err != nil {
					errs <- err
					return
				}
			}
		}
	}()

	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for j := 0; j < iterations; j++ {
				config := setting.GetModelRequestRateLimitConfig("standard")
				assert.GreaterOrEqual(t, config.DurationMinutes, 1)
				assert.GreaterOrEqual(t, config.TotalCount, 0)
				assert.GreaterOrEqual(t, config.SuccessCount, 1)
			}
		}()
	}

	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}
