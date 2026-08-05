package constant

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetupCompletedPublishesStateAcrossConcurrentReaders(t *testing.T) {
	SetSetupCompleted(false)
	t.Cleanup(func() {
		SetSetupCompleted(false)
	})

	const readers = 16
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range readers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for range 1000 {
				_ = SetupCompleted()
			}
		}()
	}

	close(start)
	SetSetupCompleted(true)
	workers.Wait()

	assert.True(t, SetupCompleted())
}
