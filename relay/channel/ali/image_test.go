package ali

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAsyncTaskWaitRespectsRequestDeadline(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be called when context cancels before first poll", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: server.URL,
			ChannelSetting: dto.ChannelSettings{},
			ApiKey:         "test-api-key",
		},
	}

	ctx := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = ctx

	requestCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	t.Cleanup(cancel)

	_, _, err := asyncTaskWait(ginCtx, info, "task-id", requestCtx)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded))
}
