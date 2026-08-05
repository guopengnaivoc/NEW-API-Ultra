package setting

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func replaceModelRequestRateLimitGroupForTest(t *testing.T, group map[string][2]int) {
	t.Helper()

	previous := ModelRequestRateLimitGroup2JSONString()
	data, err := common.Marshal(group)
	require.NoError(t, err)
	require.NoError(t, UpdateModelRequestRateLimitGroupByJSONString(string(data)))

	t.Cleanup(func() {
		require.NoError(t, UpdateModelRequestRateLimitGroupByJSONString(previous))
	})
}

func TestUpdateModelRequestRateLimitGroupRejectsInvalidJSONWithoutClearingCurrentPolicy(t *testing.T) {
	replaceModelRequestRateLimitGroupForTest(t, map[string][2]int{
		"existing": {40, 30},
	})

	err := UpdateModelRequestRateLimitGroupByJSONString(`{"replacement":`)

	require.Error(t, err)
	total, success, found := GetGroupRateLimit("existing")
	assert.True(t, found)
	assert.Equal(t, 40, total)
	assert.Equal(t, 30, success)
}

func TestUpdateModelRequestRateLimitGroupRejectsInvalidValuesWithoutClearingCurrentPolicy(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "negative total count", value: `{"bad":[-1,1]}`},
		{name: "zero success count", value: `{"bad":[1,0]}`},
		{name: "count above database bound", value: `{"bad":[2147483648,1]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replaceModelRequestRateLimitGroupForTest(t, map[string][2]int{
				"existing": {40, 30},
			})

			err := UpdateModelRequestRateLimitGroupByJSONString(test.value)

			require.Error(t, err)
			total, success, found := GetGroupRateLimit("existing")
			assert.True(t, found)
			assert.Equal(t, 40, total)
			assert.Equal(t, 30, success)
		})
	}
}

func TestModelRequestRateLimitGroupConcurrentReadAndUpdate(t *testing.T) {
	replaceModelRequestRateLimitGroupForTest(t, map[string][2]int{
		"group-a": {100, 80},
		"group-b": {200, 160},
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
			value := `{"group-a":[100,80],"group-b":[200,160]}`
			if i%2 == 0 {
				value = `{"group-a":[300,240],"group-b":[400,320]}`
			}
			if err := UpdateModelRequestRateLimitGroupByJSONString(value); err != nil {
				errs <- err
				return
			}
		}
	}()

	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for j := 0; j < iterations; j++ {
				GetGroupRateLimit("group-a")
				GetGroupRateLimit("group-b")
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

func TestGetModelRequestRateLimitConfigReturnsCoherentDefaultsAndGroupOverride(t *testing.T) {
	previousDefaults := GetModelRequestRateLimitDefaults()
	previousGroupJSON := ModelRequestRateLimitGroup2JSONString()
	t.Cleanup(func() {
		SetModelRequestRateLimitEnabled(previousDefaults.Enabled)
		require.NoError(t, SetModelRequestRateLimitDurationMinutes(previousDefaults.DurationMinutes))
		require.NoError(t, SetModelRequestRateLimitCount(previousDefaults.TotalCount))
		require.NoError(t, SetModelRequestRateLimitSuccessCount(previousDefaults.SuccessCount))
		require.NoError(t, UpdateModelRequestRateLimitGroupByJSONString(previousGroupJSON))
	})

	SetModelRequestRateLimitEnabled(true)
	require.NoError(t, SetModelRequestRateLimitDurationMinutes(5))
	require.NoError(t, SetModelRequestRateLimitCount(100))
	require.NoError(t, SetModelRequestRateLimitSuccessCount(80))
	require.NoError(t, UpdateModelRequestRateLimitGroupByJSONString(
		`{"vip":[500,400]}`,
	))

	defaults := GetModelRequestRateLimitConfig("standard")
	assert.Equal(t, ModelRequestRateLimitConfig{
		Enabled:         true,
		DurationMinutes: 5,
		TotalCount:      100,
		SuccessCount:    80,
	}, defaults)

	vip := GetModelRequestRateLimitConfig("vip")
	assert.Equal(t, ModelRequestRateLimitConfig{
		Enabled:         true,
		DurationMinutes: 5,
		TotalCount:      500,
		SuccessCount:    400,
	}, vip)
}

func TestModelRequestRateLimitScalarSettersRejectUnsafeValues(t *testing.T) {
	previousDefaults := GetModelRequestRateLimitDefaults()
	t.Cleanup(func() {
		SetModelRequestRateLimitEnabled(previousDefaults.Enabled)
		require.NoError(t, SetModelRequestRateLimitDurationMinutes(previousDefaults.DurationMinutes))
		require.NoError(t, SetModelRequestRateLimitCount(previousDefaults.TotalCount))
		require.NoError(t, SetModelRequestRateLimitSuccessCount(previousDefaults.SuccessCount))
	})

	require.Error(t, SetModelRequestRateLimitDurationMinutes(-1))
	require.Error(t, SetModelRequestRateLimitCount(-1))
	require.Error(t, SetModelRequestRateLimitSuccessCount(-1))

	assert.Equal(t, previousDefaults, GetModelRequestRateLimitDefaults())
}

func TestModelRequestRateLimitSuccessCountAllowsZeroAsUnlimited(t *testing.T) {
	previousDefaults := GetModelRequestRateLimitDefaults()
	t.Cleanup(func() {
		require.NoError(t, SetModelRequestRateLimitSuccessCount(previousDefaults.SuccessCount))
	})

	require.NoError(t, SetModelRequestRateLimitSuccessCount(0))

	assert.Zero(t, GetModelRequestRateLimitDefaults().SuccessCount)
	require.Error(t, CheckModelRequestRateLimitGroup(`{"standard":[1,0]}`))
}

func TestModelRequestRateLimitConfigConcurrentReadAndUpdate(t *testing.T) {
	previousDefaults := GetModelRequestRateLimitDefaults()
	t.Cleanup(func() {
		SetModelRequestRateLimitEnabled(previousDefaults.Enabled)
		require.NoError(t, SetModelRequestRateLimitDurationMinutes(previousDefaults.DurationMinutes))
		require.NoError(t, SetModelRequestRateLimitCount(previousDefaults.TotalCount))
		require.NoError(t, SetModelRequestRateLimitSuccessCount(previousDefaults.SuccessCount))
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
			SetModelRequestRateLimitEnabled(i%2 == 0)
			if err := SetModelRequestRateLimitDurationMinutes(i%10 + 1); err != nil {
				errs <- err
				return
			}
			if err := SetModelRequestRateLimitCount(i); err != nil {
				errs <- err
				return
			}
			if err := SetModelRequestRateLimitSuccessCount(i + 1); err != nil {
				errs <- err
				return
			}
		}
	}()

	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for j := 0; j < iterations; j++ {
				config := GetModelRequestRateLimitConfig("standard")
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
