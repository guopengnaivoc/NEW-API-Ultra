package common

import (
	"sync"
	"time"
)

type rateLimitEntry struct {
	timestamps []int64
	duration   int64
}

type InMemoryRateLimiter struct {
	store           map[string]*rateLimitEntry
	mutex           sync.Mutex
	cleanupInterval time.Duration
	nowUnix         func() int64
}

func (l *InMemoryRateLimiter) Init(cleanupInterval time.Duration) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.store != nil {
		return
	}

	l.store = make(map[string]*rateLimitEntry)
	l.cleanupInterval = cleanupInterval
	if cleanupInterval > 0 {
		go l.clearExpiredItems()
	}
}

func (l *InMemoryRateLimiter) clearExpiredItems() {
	for {
		time.Sleep(l.cleanupInterval)
		l.clearExpiredItemsAt(l.currentUnix())
	}
}

func (l *InMemoryRateLimiter) clearExpiredItemsAt(now int64) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	for key, entry := range l.store {
		size := len(entry.timestamps)
		if size == 0 || now-entry.timestamps[size-1] >= entry.duration {
			delete(l.store, key)
		}
	}
}

func (l *InMemoryRateLimiter) currentUnix() int64 {
	if l.nowUnix != nil {
		return l.nowUnix()
	}
	return time.Now().Unix()
}

// Request parameter duration's unit is seconds
func (l *InMemoryRateLimiter) Request(key string, maxRequestNum int, duration int64) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	// [old <-- new]
	entry, ok := l.store[key]
	now := l.currentUnix()
	if ok {
		entry.duration = duration
		if len(entry.timestamps) < maxRequestNum {
			entry.timestamps = append(entry.timestamps, now)
			return true
		} else {
			if now-entry.timestamps[0] >= duration {
				entry.timestamps = entry.timestamps[1:]
				entry.timestamps = append(entry.timestamps, now)
				return true
			} else {
				return false
			}
		}
	} else {
		l.store[key] = &rateLimitEntry{
			timestamps: []int64{now},
			duration:   duration,
		}
	}
	return true
}
