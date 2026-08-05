package logger

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogRotationStateClaimsSingleRotation(t *testing.T) {
	state := newLogRotationState(0)
	start := make(chan struct{})
	claims := make(chan bool, 8)

	var workers sync.WaitGroup
	workers.Add(cap(claims))
	for range cap(claims) {
		go func() {
			defer workers.Done()
			<-start
			claims <- state.recordLog()
		}()
	}

	close(start)
	workers.Wait()
	close(claims)

	claimCount := 0
	for claimed := range claims {
		if claimed {
			claimCount++
		}
	}
	assert.Equal(t, 1, claimCount)
	assert.False(t, state.tryStartSetup())

	state.finishSetup()
	assert.True(t, state.tryStartSetup())
	assert.False(t, state.tryStartSetup())
	state.finishSetup()
}

func TestLogRotationStateResetsCounterWhenRotationStarts(t *testing.T) {
	state := newLogRotationState(2)

	assert.False(t, state.recordLog())
	assert.False(t, state.recordLog())
	assert.True(t, state.recordLog())
	assert.False(t, state.recordLog())

	state.finishSetup()
	assert.False(t, state.recordLog())
	assert.True(t, state.recordLog())
	state.finishSetup()
}

func TestLogInfoWritesOrdinaryOutput(t *testing.T) {
	var output bytes.Buffer

	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	gin.DefaultWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		common.LogWriterMu.Unlock()
	})

	LogInfo(context.WithValue(context.Background(), common.RequestIdKey, "request-123"), "ordinary output")

	require.NotEmpty(t, output.String())
	assert.Contains(t, output.String(), "[INFO]")
	assert.Contains(t, output.String(), "request-123")
	assert.Contains(t, output.String(), "ordinary output")
}
