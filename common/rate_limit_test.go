package common

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryRateLimiterConcurrentInitialization(t *testing.T) {
	var rateLimiter InMemoryRateLimiter
	start := make(chan struct{})
	var waitGroup sync.WaitGroup

	const workerCount = 4
	waitGroup.Add(workerCount)
	for worker := range workerCount {
		go func() {
			defer waitGroup.Done()
			<-start
			rateLimiter.Init(0)
			rateLimiter.Request(fmt.Sprintf("worker-%d", worker), 1, 60)
		}()
	}

	close(start)
	waitGroup.Wait()
}

func TestInMemoryRateLimiterCleanupUsesEachKeyWindow(t *testing.T) {
	currentUnix := int64(1_000)
	rateLimiter := InMemoryRateLimiter{
		nowUnix: func() int64 {
			return currentUnix
		},
	}
	rateLimiter.Init(0)

	require.True(t, rateLimiter.Request("short-window", 1, 10))
	require.False(t, rateLimiter.Request("short-window", 1, 10))
	require.True(t, rateLimiter.Request("long-window", 1, 60))
	require.False(t, rateLimiter.Request("long-window", 1, 60))

	currentUnix += 21
	rateLimiter.clearExpiredItemsAt(currentUnix)

	assert.True(t, rateLimiter.Request("short-window", 1, 10))
	assert.False(t, rateLimiter.Request("long-window", 1, 60))
}
