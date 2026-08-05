package setting

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type ModelRequestRateLimitConfig struct {
	Enabled         bool
	DurationMinutes int
	TotalCount      int
	SuccessCount    int
}

var modelRequestRateLimitEnabled = false
var modelRequestRateLimitDurationMinutes = 1
var modelRequestRateLimitCount = 0
var modelRequestRateLimitSuccessCount = 1000
var modelRequestRateLimitGroup = map[string][2]int{}
var modelRequestRateLimitMutex sync.RWMutex

const maxModelRequestRateLimitDurationMinutes = int64(math.MaxInt64) / int64(time.Minute)

func GetModelRequestRateLimitDefaults() ModelRequestRateLimitConfig {
	modelRequestRateLimitMutex.RLock()
	defer modelRequestRateLimitMutex.RUnlock()

	return ModelRequestRateLimitConfig{
		Enabled:         modelRequestRateLimitEnabled,
		DurationMinutes: modelRequestRateLimitDurationMinutes,
		TotalCount:      modelRequestRateLimitCount,
		SuccessCount:    modelRequestRateLimitSuccessCount,
	}
}

func GetModelRequestRateLimitConfig(group string) ModelRequestRateLimitConfig {
	modelRequestRateLimitMutex.RLock()
	defer modelRequestRateLimitMutex.RUnlock()

	config := ModelRequestRateLimitConfig{
		Enabled:         modelRequestRateLimitEnabled,
		DurationMinutes: modelRequestRateLimitDurationMinutes,
		TotalCount:      modelRequestRateLimitCount,
		SuccessCount:    modelRequestRateLimitSuccessCount,
	}
	if limits, found := modelRequestRateLimitGroup[group]; found {
		config.TotalCount = limits[0]
		config.SuccessCount = limits[1]
	}
	return config
}

func SetModelRequestRateLimitEnabled(enabled bool) {
	modelRequestRateLimitMutex.Lock()
	modelRequestRateLimitEnabled = enabled
	modelRequestRateLimitMutex.Unlock()
}

func validateModelRequestRateLimitDurationMinutes(durationMinutes int64) error {
	if durationMinutes < 0 || durationMinutes > maxModelRequestRateLimitDurationMinutes {
		return fmt.Errorf(
			"model request rate limit duration must be between 0 and %d minutes",
			maxModelRequestRateLimitDurationMinutes,
		)
	}
	return nil
}

func SetModelRequestRateLimitDurationMinutes(durationMinutes int) error {
	if err := validateModelRequestRateLimitDurationMinutes(int64(durationMinutes)); err != nil {
		return err
	}
	modelRequestRateLimitMutex.Lock()
	modelRequestRateLimitDurationMinutes = durationMinutes
	modelRequestRateLimitMutex.Unlock()
	return nil
}

func validateModelRequestRateLimitCount(count int64) error {
	if count < 0 || count > math.MaxInt32 {
		return fmt.Errorf("model request rate limit count must be between 0 and %d", math.MaxInt32)
	}
	return nil
}

func SetModelRequestRateLimitCount(count int) error {
	if err := validateModelRequestRateLimitCount(int64(count)); err != nil {
		return err
	}
	modelRequestRateLimitMutex.Lock()
	modelRequestRateLimitCount = count
	modelRequestRateLimitMutex.Unlock()
	return nil
}

func validateModelRequestRateLimitSuccessCount(count int64) error {
	if count < 0 || count > math.MaxInt32 {
		return fmt.Errorf("model request rate limit success count must be between 0 and %d", math.MaxInt32)
	}
	return nil
}

func SetModelRequestRateLimitSuccessCount(count int) error {
	if err := validateModelRequestRateLimitSuccessCount(int64(count)); err != nil {
		return err
	}
	modelRequestRateLimitMutex.Lock()
	modelRequestRateLimitSuccessCount = count
	modelRequestRateLimitMutex.Unlock()
	return nil
}

func ModelRequestRateLimitGroup2JSONString() string {
	modelRequestRateLimitMutex.RLock()
	defer modelRequestRateLimitMutex.RUnlock()

	jsonBytes, err := common.Marshal(modelRequestRateLimitGroup)
	if err != nil {
		common.SysLog("error marshalling model request rate limit group: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateModelRequestRateLimitGroupByJSONString(jsonStr string) error {
	next, err := parseModelRequestRateLimitGroup(jsonStr)
	if err != nil {
		return err
	}

	modelRequestRateLimitMutex.Lock()
	modelRequestRateLimitGroup = next
	modelRequestRateLimitMutex.Unlock()
	return nil
}

func GetGroupRateLimit(group string) (totalCount, successCount int, found bool) {
	modelRequestRateLimitMutex.RLock()
	defer modelRequestRateLimitMutex.RUnlock()

	if modelRequestRateLimitGroup == nil {
		return 0, 0, false
	}

	limits, found := modelRequestRateLimitGroup[group]
	if !found {
		return 0, 0, false
	}
	return limits[0], limits[1], true
}

func parseModelRequestRateLimitGroup(jsonStr string) (map[string][2]int, error) {
	groupRateLimits := make(map[string][2]int)
	err := common.UnmarshalJsonStr(jsonStr, &groupRateLimits)
	if err != nil {
		return nil, err
	}
	for group, limits := range groupRateLimits {
		if limits[0] < 0 || limits[1] < 1 {
			return nil, fmt.Errorf("group %s has invalid rate limit values: [%d, %d]", group, limits[0], limits[1])
		}
		if limits[0] > math.MaxInt32 || limits[1] > math.MaxInt32 {
			return nil, fmt.Errorf("group %s [%d, %d] has max rate limits value 2147483647", group, limits[0], limits[1])
		}
	}

	return groupRateLimits, nil
}

func CheckModelRequestRateLimitGroup(jsonStr string) error {
	_, err := parseModelRequestRateLimitGroup(jsonStr)
	return err
}

func ValidateModelRequestRateLimitOption(key, value string) (bool, error) {
	switch key {
	case "ModelRequestRateLimitDurationMinutes":
		durationMinutes, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return true, fmt.Errorf("invalid model request rate limit duration: %w", err)
		}
		return true, validateModelRequestRateLimitDurationMinutes(durationMinutes)
	case "ModelRequestRateLimitCount":
		count, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return true, fmt.Errorf("invalid model request rate limit count: %w", err)
		}
		return true, validateModelRequestRateLimitCount(count)
	case "ModelRequestRateLimitSuccessCount":
		count, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return true, fmt.Errorf("invalid model request rate limit success count: %w", err)
		}
		return true, validateModelRequestRateLimitSuccessCount(count)
	default:
		return false, nil
	}
}
