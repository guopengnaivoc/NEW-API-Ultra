package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/bytedance/gopkg/util/gopool"
)

const notificationLimitReservationScript = `
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
if not limit or limit <= 0 or limit ~= math.floor(limit) or
   not window or window <= 0 or window ~= math.floor(window) then
  return -3
end

local value = redis.call('GET', KEYS[1])
if not value then
  redis.call('PSETEX', KEYS[1], window, '1')
  return 1
end

local ttl = redis.call('PTTL', KEYS[1])
if ttl == -1 then
  return -1
end
if ttl <= 0 then
  return -2
end
if not string.match(value, '^[1-9][0-9]*$') then
  return -2
end

local count = tonumber(value)
if not count then
  return -2
end
if count >= limit then
  return 0
end

redis.call('INCR', KEYS[1])
return 1
`

var (
	errNotificationLimitCounterCorrupt = errors.New("notification limit counter is corrupt")
	notifyLimitStore                   memoryNotificationLimitStore
	cleanupOnce                        sync.Once
)

type limitCount struct {
	Count     int
	Timestamp time.Time
}

type memoryNotificationLimitStore struct {
	mutex   sync.Mutex
	entries map[string]limitCount
}

func validateNotificationLimitConfiguration(limit int, duration time.Duration) error {
	if limit <= 0 || duration <= 0 {
		return fmt.Errorf(
			"invalid notification limit configuration: limit=%d duration=%s",
			limit,
			duration,
		)
	}
	return nil
}

func notificationLimitConfiguration() (int, time.Duration, error) {
	limit := constant.NotifyLimitCount
	minutes := constant.NotificationLimitDurationMinute
	if minutes <= 0 {
		return 0, 0, fmt.Errorf(
			"invalid notification limit configuration: limit=%d duration_minutes=%d",
			limit,
			minutes,
		)
	}
	maxDurationMinutes := int64(time.Duration(1<<63-1) / time.Minute)
	if int64(minutes) > maxDurationMinutes {
		return 0, 0, fmt.Errorf(
			"invalid notification limit configuration: limit=%d duration_minutes=%d",
			limit,
			minutes,
		)
	}
	duration := time.Duration(minutes) * time.Minute
	if err := validateNotificationLimitConfiguration(limit, duration); err != nil {
		return 0, 0, err
	}
	return limit, duration, nil
}

func (store *memoryNotificationLimitStore) admit(
	userId int,
	notifyType string,
	now time.Time,
	limit int,
	duration time.Duration,
) (bool, error) {
	if err := validateNotificationLimitConfiguration(limit, duration); err != nil {
		return false, err
	}

	key := fmt.Sprintf("%d:%s", userId, notifyType)
	store.mutex.Lock()
	defer store.mutex.Unlock()

	if store.entries == nil {
		store.entries = make(map[string]limitCount)
	}
	current, ok := store.entries[key]
	if !ok || !now.Before(current.Timestamp.Add(duration)) {
		store.entries[key] = limitCount{Count: 1, Timestamp: now}
		return true, nil
	}
	if current.Count <= 0 {
		return false, errNotificationLimitCounterCorrupt
	}
	if current.Count >= limit {
		return false, nil
	}

	current.Count++
	store.entries[key] = current
	return true, nil
}

func (store *memoryNotificationLimitStore) cleanup(now time.Time, duration time.Duration) {
	if duration <= 0 {
		return
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	for key, current := range store.entries {
		if !now.Before(current.Timestamp.Add(duration)) {
			delete(store.entries, key)
		}
	}
}

// startCleanupTask starts a background task to clean up expired entries.
func startCleanupTask() {
	gopool.Go(func() {
		for {
			time.Sleep(time.Hour)
			_, duration, err := notificationLimitConfiguration()
			if err != nil {
				common.SysError(err.Error())
				continue
			}
			notifyLimitStore.cleanup(time.Now(), duration)
		}
	})
}

// CheckNotificationLimit checks if the user has exceeded their notification limit.
// It returns true if the user can send a notification and false if the limit is exceeded.
func CheckNotificationLimit(userId int, notifyType string) (bool, error) {
	if common.RedisEnabled {
		return checkRedisLimit(userId, notifyType)
	}
	return checkMemoryLimit(userId, notifyType)
}

func checkRedisLimit(userId int, notifyType string) (bool, error) {
	limit, duration, err := notificationLimitConfiguration()
	if err != nil {
		return false, err
	}
	if common.RDB == nil {
		return false, errors.New("notification limit Redis backend is unavailable")
	}

	key := fmt.Sprintf("notify_limit:%d:%s", userId, notifyType)
	result, err := common.RDB.Eval(
		context.Background(),
		notificationLimitReservationScript,
		[]string{key},
		strconv.Itoa(limit),
		strconv.FormatInt(duration.Milliseconds(), 10),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("failed to reserve notification limit: %w", err)
	}

	switch result {
	case 1:
		return true, nil
	case 0:
		return false, nil
	case -1:
		return false, fmt.Errorf(
			"notification limit counter %q has no expiration: %w",
			key,
			common.ErrRedisKeyPersistent,
		)
	case -2:
		return false, fmt.Errorf("%w: %q", errNotificationLimitCounterCorrupt, key)
	case -3:
		return false, fmt.Errorf(
			"invalid notification limit configuration: limit=%d duration=%s",
			limit,
			duration,
		)
	default:
		return false, fmt.Errorf(
			"unexpected notification limit reservation result %d for %q",
			result,
			key,
		)
	}
}

func checkMemoryLimit(userId int, notifyType string) (bool, error) {
	limit, duration, err := notificationLimitConfiguration()
	if err != nil {
		return false, err
	}
	cleanupOnce.Do(startCleanupTask)
	return notifyLimitStore.admit(userId, notifyType, time.Now(), limit, duration)
}
