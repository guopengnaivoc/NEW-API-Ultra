package zhipu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	closeChannel chan bool
}

func (r *closeNotifyRecorder) CloseNotify() <-chan bool { return r.closeChannel }
func (r *closeNotifyRecorder) Flush()                   {}
func (r *closeNotifyRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack not supported")
}

// Normal EOF must deliver every queued data event and the final usage-bearing
// meta event, in upstream order, before [DONE]. The old data/meta/stop channel
// trio raced on completion and could drop both queued tail data and the meta
// frame that carries usage.
func TestZhipuStreamHandlerDeliversQueuedTailAndMetaBeforeDone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var upstream strings.Builder
	for i := 0; i < 6; i++ {
		upstream.WriteString(fmt.Sprintf("data:tail-chunk-%d\n", i))
	}
	upstream.WriteString(`meta:{"request_id":"r1","task_id":"t1","task_status":"SUCCESS","usage":{"prompt_tokens":7,"completion_tokens":13,"total_tokens":20}}` + "\n")

	recorder := &closeNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closeChannel:     make(chan bool, 1),
	}
	c, _ := gin.CreateTestContext(recorder)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/chat/completions", nil)
	require.NoError(t, err)
	c.Request = req

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(upstream.String()))}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-test"},
	}

	usage, handlerErr := zhipuStreamHandler(c, info, resp)
	require.Nil(t, handlerErr)
	require.NotNil(t, usage, "the usage-bearing meta event was dropped on normal EOF")
	assert.Equal(t, 7, usage.PromptTokens)
	assert.Equal(t, 13, usage.CompletionTokens)
	assert.Equal(t, 20, usage.TotalTokens)

	body := recorder.Body.String()
	for i := 0; i < 6; i++ {
		assert.Contains(t, body, fmt.Sprintf("tail-chunk-%d", i), "queued tail chunk %d was dropped", i)
	}
	assert.Contains(t, body, "data: [DONE]")
	doneIndex := strings.LastIndex(body, "data: [DONE]")
	finishIndex := strings.LastIndex(body, `"finish_reason"`)
	lastChunkIndex := strings.LastIndex(body, "tail-chunk-5")
	require.Greater(t, doneIndex, lastChunkIndex, "[DONE] must come after all queued content")
	require.Greater(t, doneIndex, finishIndex, "[DONE] must come after the meta/finish frame")
	require.Greater(t, finishIndex, lastChunkIndex, "meta must preserve upstream ordering after data")
}

// Cancellation must terminate the producer and release the handler without
// requiring upstream EOF.
func TestZhipuStreamHandlerClientDisconnectStopsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	bodyReader, bodyWriter := io.Pipe()
	t.Cleanup(func() { _ = bodyWriter.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	recorder := &closeNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closeChannel:     make(chan bool, 1),
	}
	c, _ := gin.CreateTestContext(recorder)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", nil)
	require.NoError(t, err)
	c.Request = req

	resp := &http.Response{Body: bodyReader}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-test"},
	}

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = zhipuStreamHandler(c, info, resp)
	}()

	_, err = io.WriteString(bodyWriter, "data:first-chunk\n")
	require.NoError(t, err)
	cancel()
	// Unblock the producer's pending Read after cancellation.
	_ = bodyWriter.CloseWithError(context.Canceled)

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		require.Fail(t, "zhipu handler did not return after client disconnect")
	}
}
