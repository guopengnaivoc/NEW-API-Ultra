package xunfei

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

func newXunfeiTestServer(t *testing.T, frames []string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Consume the request payload the client writes first.
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for _, frame := range frames {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func xunfeiFrame(index int, status int, content string, prompt, completion, total int) string {
	return fmt.Sprintf(
		`{"header":{"code":0,"status":%d},"payload":{"choices":{"status":%d,"seq":%d,"text":[{"content":"%s","role":"assistant","index":0}]},"usage":{"text":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}}}`,
		status, status, index, content, prompt, completion, total,
	)
}

// The producer must deliver every frame — including the final usage-bearing
// status==2 frame — and then close the channel as the completion signal. The
// old separate stop channel was signaled while frames could still sit queued
// in the buffered data channel, so the consumer's select could drop them.
func TestXunfeiMakeRequestDeliversAllFramesThenCloses(t *testing.T) {
	frames := []string{
		xunfeiFrame(0, 0, "tail-a", 0, 0, 0),
		xunfeiFrame(1, 1, "tail-b", 0, 0, 0),
		xunfeiFrame(2, 2, "tail-final", 9, 17, 26),
	}
	wsURL := newXunfeiTestServer(t, frames)

	dataChan, err := xunfeiMakeRequest(context.Background(), dto.GeneralOpenAIRequest{Model: "spark-test"}, "general", wsURL, "app-id")
	require.NoError(t, err)

	var received []XunfeiChatResponse
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame, ok := <-dataChan:
			if !ok {
				// Channel closed: the deterministic completion signal.
				require.Len(t, received, 3, "all queued frames must be drained before close is observed")
				assert.Equal(t, "tail-a", received[0].Payload.Choices.Text[0].Content)
				assert.Equal(t, "tail-b", received[1].Payload.Choices.Text[0].Content)
				assert.Equal(t, "tail-final", received[2].Payload.Choices.Text[0].Content)
				assert.Equal(t, 9, received[2].Payload.Usage.Text.PromptTokens, "final frame carries usage")
				assert.Equal(t, 17, received[2].Payload.Usage.Text.CompletionTokens)
				assert.Equal(t, 26, received[2].Payload.Usage.Text.TotalTokens)
				return
			}
			received = append(received, frame)
		case <-deadline:
			require.Fail(t, "producer did not close the channel after the final frame")
		}
	}
}

// Cancellation must close the websocket and terminate the producer: the
// channel closes without delivering further frames and without leaking the
// reader goroutine.
func TestXunfeiMakeRequestCancellationClosesConnection(t *testing.T) {
	holdServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(xunfeiFrame(0, 0, "first", 0, 0, 0))); err != nil {
			return
		}
		<-holdServer // hold the stream open: only cancellation can end the client side
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(holdServer) })
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithCancel(context.Background())
	dataChan, err := xunfeiMakeRequest(ctx, dto.GeneralOpenAIRequest{Model: "spark-test"}, "general", wsURL, "app-id")
	require.NoError(t, err)

	select {
	case frame, ok := <-dataChan:
		require.True(t, ok)
		require.Equal(t, "first", frame.Payload.Choices.Text[0].Content)
	case <-time.After(5 * time.Second):
		require.Fail(t, "first frame never arrived")
	}

	cancel()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-dataChan:
			if !ok {
				return // producer terminated and closed the channel
			}
		case <-deadline:
			require.Fail(t, "producer did not terminate after cancellation")
		}
	}
}
