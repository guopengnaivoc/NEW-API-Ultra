package cohere

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// On normal upstream EOF every queued event must reach the client before
// [DONE]: the old separate stop channel raced the buffered data channel and
// Go's select could pick stop first, dropping queued tail content and the
// finish/usage frame.
func TestCohereStreamHandlerDeliversQueuedTailBeforeDone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Enough chunks to guarantee several are still queued in the buffered
	// channel when the producer reaches EOF and (previously) fired stop.
	var upstream strings.Builder
	for i := 0; i < 6; i++ {
		upstream.WriteString(fmt.Sprintf(`{"is_finished":false,"text":"chunk-%d"}`, i))
		upstream.WriteString("\n")
	}
	upstream.WriteString(`{"is_finished":true,"finish_reason":"COMPLETE","response":{"meta":{"billed_units":{"input_tokens":11,"output_tokens":22}}}}` + "\n")

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
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "cohere-test"},
	}

	usage, handlerErr := cohereStreamHandler(c, info, resp)
	require.Nil(t, handlerErr)
	require.NotNil(t, usage)

	body := recorder.Body.String()
	for i := 0; i < 6; i++ {
		assert.Contains(t, body, fmt.Sprintf("chunk-%d", i), "queued tail chunk %d was dropped", i)
	}
	assert.Contains(t, body, `"finish_reason":"stop"`, "finish frame was dropped")
	assert.Contains(t, body, "data: [DONE]")
	doneIndex := strings.LastIndex(body, "data: [DONE]")
	lastChunkIndex := strings.LastIndex(body, "chunk-5")
	finishIndex := strings.LastIndex(body, `"finish_reason"`)
	require.Greater(t, doneIndex, lastChunkIndex, "[DONE] must come after all content")
	require.Greater(t, doneIndex, finishIndex, "[DONE] must come after the finish frame")

	assert.Equal(t, 11, usage.PromptTokens, "usage from the final frame was dropped")
	assert.Equal(t, 22, usage.CompletionTokens)
}
