package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type expireNotificationCounterBeforeEval struct {
	server *miniredis.Miniredis
	key    string
}

type synchronizeNotificationReads struct {
	participants int32
	arrived      atomic.Int32
	release      chan struct{}
}

type failNotificationLimitEval struct {
	err error
}

func (hook *expireNotificationCounterBeforeEval) BeforeProcess(
	ctx context.Context,
	cmd redis.Cmder,
) (context.Context, error) {
	if cmd.Name() == "eval" {
		hook.server.Del(hook.key)
	}
	return ctx, nil
}

func (*expireNotificationCounterBeforeEval) AfterProcess(context.Context, redis.Cmder) error {
	return nil
}

func (hook *synchronizeNotificationReads) AfterProcess(
	_ context.Context,
	cmd redis.Cmder,
) error {
	if cmd.Name() != "get" {
		return nil
	}
	if hook.arrived.Add(1) == hook.participants {
		close(hook.release)
	}
	<-hook.release
	return nil
}

func (*expireNotificationCounterBeforeEval) BeforeProcessPipeline(
	ctx context.Context,
	_ []redis.Cmder,
) (context.Context, error) {
	return ctx, nil
}

func (*expireNotificationCounterBeforeEval) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func (*synchronizeNotificationReads) BeforeProcess(
	ctx context.Context,
	_ redis.Cmder,
) (context.Context, error) {
	return ctx, nil
}

func (*synchronizeNotificationReads) BeforeProcessPipeline(
	ctx context.Context,
	_ []redis.Cmder,
) (context.Context, error) {
	return ctx, nil
}

func (*synchronizeNotificationReads) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func (hook *failNotificationLimitEval) BeforeProcess(
	ctx context.Context,
	cmd redis.Cmder,
) (context.Context, error) {
	if cmd.Name() == "eval" {
		return ctx, hook.err
	}
	return ctx, nil
}

func (*failNotificationLimitEval) AfterProcess(context.Context, redis.Cmder) error {
	return nil
}

func (*failNotificationLimitEval) BeforeProcessPipeline(
	ctx context.Context,
	_ []redis.Cmder,
) (context.Context, error) {
	return ctx, nil
}

func (*failNotificationLimitEval) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func useNotificationLimitRedis(
	t *testing.T,
	limit int,
	durationMinutes int,
	poolSize int,
) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()

	server := miniredis.RunT(t)
	previousRDB := common.RDB
	previousRedisEnabled := common.RedisEnabled
	previousLimit := constant.NotifyLimitCount
	previousDuration := constant.NotificationLimitDurationMinute
	client := redis.NewClient(&redis.Options{
		Addr:     server.Addr(),
		PoolSize: poolSize,
	})
	common.RDB = client
	common.RedisEnabled = true
	constant.NotifyLimitCount = limit
	constant.NotificationLimitDurationMinute = durationMinutes
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		common.RDB = previousRDB
		common.RedisEnabled = previousRedisEnabled
		constant.NotifyLimitCount = previousLimit
		constant.NotificationLimitDurationMinute = previousDuration
	})
	return server, client
}

func TestRedisNotificationLimitConcurrentAdmissionHonorsExactCap(t *testing.T) {
	const (
		participants = 12
		limit        = 3
		userID       = 9876
		notifyType   = "quota"
	)

	server, client := useNotificationLimitRedis(t, limit, 10, participants)

	readBarrier := &synchronizeNotificationReads{
		participants: participants,
		release:      make(chan struct{}),
	}
	client.AddHook(readBarrier)

	start := make(chan struct{})
	results := make(chan bool, participants)
	errs := make(chan error, participants)
	var workers sync.WaitGroup
	workers.Add(participants)
	for range participants {
		go func() {
			defer workers.Done()
			<-start
			allowed, err := checkRedisLimit(userID, notifyType)
			results <- allowed
			errs <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	allowedCount := 0
	for allowed := range results {
		if allowed {
			allowedCount++
		}
	}
	assert.Equal(t, limit, allowedCount)

	key := "notify_limit:9876:quota"
	value, err := server.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "3", value)
	assert.Equal(t, 10*time.Minute, server.TTL(key))
}

func TestRedisNotificationLimitReseedsCounterThatExpiresBeforeIncrement(t *testing.T) {
	server, client := useNotificationLimitRedis(t, 10, 60, 0)

	const userID = 4321
	const notifyType = "quota"
	key := "notify_limit:4321:quota"
	require.NoError(t, client.Set(t.Context(), key, "2", 60*time.Minute).Err())
	client.AddHook(&expireNotificationCounterBeforeEval{server: server, key: key})

	allowed, err := checkRedisLimit(userID, notifyType)
	require.NoError(t, err)
	assert.True(t, allowed)
	value, err := client.Get(t.Context(), key).Result()
	require.NoError(t, err)
	assert.Equal(t, "1", value)
	assert.Positive(t, server.TTL(key))
}

func TestRedisNotificationLimitRejectsPersistentCounter(t *testing.T) {
	server, client := useNotificationLimitRedis(t, 10, 60, 0)

	const userID = 5432
	const notifyType = "quota"
	key := "notify_limit:5432:quota"
	require.NoError(t, client.Set(t.Context(), key, "2", 0).Err())

	allowed, err := checkRedisLimit(userID, notifyType)
	assert.False(t, allowed)
	assert.ErrorIs(t, err, common.ErrRedisKeyPersistent)
	value, err := server.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "2", value)
	assert.Equal(t, time.Duration(0), server.TTL(key))
}

func TestRedisNotificationLimitUsesFirstAdmissionFixedWindow(t *testing.T) {
	server, _ := useNotificationLimitRedis(t, 2, 10, 0)

	const (
		userID     = 6543
		notifyType = "quota"
	)
	key := "notify_limit:6543:quota"

	allowed, err := checkRedisLimit(userID, notifyType)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, 10*time.Minute, server.TTL(key))

	server.FastForward(3 * time.Minute)
	allowed, err = checkRedisLimit(userID, notifyType)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, 7*time.Minute, server.TTL(key))

	allowed, err = checkRedisLimit(userID, notifyType)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, 7*time.Minute, server.TTL(key))

	server.FastForward(7 * time.Minute)
	allowed, err = checkRedisLimit(userID, notifyType)
	require.NoError(t, err)
	assert.True(t, allowed)
	value, err := server.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "1", value)
	assert.Equal(t, 10*time.Minute, server.TTL(key))
}

func TestRedisNotificationLimitRejectsCorruptCounter(t *testing.T) {
	server, client := useNotificationLimitRedis(t, 3, 10, 0)

	const (
		userID     = 7654
		notifyType = "quota"
	)
	key := "notify_limit:7654:quota"
	require.NoError(t, client.Set(t.Context(), key, "not-a-count", 10*time.Minute).Err())

	allowed, err := checkRedisLimit(userID, notifyType)
	assert.False(t, allowed)
	require.Error(t, err)
	assert.ErrorContains(t, err, "corrupt")
	value, getErr := server.Get(key)
	require.NoError(t, getErr)
	assert.Equal(t, "not-a-count", value)
	assert.Equal(t, 10*time.Minute, server.TTL(key))
}

func TestRedisNotificationLimitFailsClosedOnBackendError(t *testing.T) {
	_, client := useNotificationLimitRedis(t, 3, 10, 0)
	backendErr := errors.New("forced notification limiter backend failure")
	client.AddHook(&failNotificationLimitEval{err: backendErr})

	allowed, err := checkRedisLimit(8765, "quota")
	assert.False(t, allowed)
	assert.ErrorIs(t, err, backendErr)
}

func TestRedisNotificationLimitRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name            string
		limit           int
		durationMinutes int
	}{
		{name: "zero limit", limit: 0, durationMinutes: 10},
		{name: "negative limit", limit: -1, durationMinutes: 10},
		{name: "zero duration", limit: 3, durationMinutes: 0},
		{name: "negative duration", limit: 3, durationMinutes: -1},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useNotificationLimitRedis(t, test.limit, test.durationMinutes, 0)

			allowed, err := checkRedisLimit(9000+index, "quota")
			assert.False(t, allowed)
			require.Error(t, err)
			assert.ErrorContains(t, err, "invalid notification limit configuration")
		})
	}
}

func TestMemoryNotificationLimitConcurrentAdmissionHonorsExactCap(t *testing.T) {
	const (
		participants = 64
		limit        = 5
		userID       = 2468
		notifyType   = "quota"
	)

	var store memoryNotificationLimitStore
	now := time.Date(2026, time.July, 29, 10, 58, 0, 0, time.UTC)
	start := make(chan struct{})
	results := make(chan bool, participants)
	errs := make(chan error, participants)
	var workers sync.WaitGroup
	workers.Add(participants)
	for range participants {
		go func() {
			defer workers.Done()
			<-start
			allowed, err := store.admit(
				userID,
				notifyType,
				now,
				limit,
				10*time.Minute,
			)
			results <- allowed
			errs <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	allowedCount := 0
	for allowed := range results {
		if allowed {
			allowedCount++
		}
	}
	assert.Equal(t, limit, allowedCount)
}

func TestMemoryNotificationLimitWindowDoesNotResetAtNaturalHour(t *testing.T) {
	var store memoryNotificationLimitStore
	firstAdmission := time.Date(2026, time.July, 29, 10, 58, 0, 0, time.UTC)

	allowed, err := store.admit(1357, "quota", firstAdmission, 1, 10*time.Minute)
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = store.admit(
		1357,
		"quota",
		time.Date(2026, time.July, 29, 11, 0, 0, 0, time.UTC),
		1,
		10*time.Minute,
	)
	require.NoError(t, err)
	assert.False(t, allowed)

	allowed, err = store.admit(
		1357,
		"quota",
		firstAdmission.Add(10*time.Minute),
		1,
		10*time.Minute,
	)
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestMemoryNotificationLimitRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		duration time.Duration
	}{
		{name: "zero limit", limit: 0, duration: 10 * time.Minute},
		{name: "negative limit", limit: -1, duration: 10 * time.Minute},
		{name: "zero duration", limit: 3, duration: 0},
		{name: "negative duration", limit: 3, duration: -time.Minute},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var store memoryNotificationLimitStore
			allowed, err := store.admit(
				1111,
				"quota",
				time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC),
				test.limit,
				test.duration,
			)
			assert.False(t, allowed)
			require.Error(t, err)
			assert.ErrorContains(t, err, "invalid notification limit configuration")
		})
	}
}
