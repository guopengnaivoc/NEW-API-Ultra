package palm

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// PaLM streams a single converted payload. The old separate stop channel could
// win the select against the queued payload on normal completion, dropping the
// entire response and emitting only [DONE].
func TestPalmStreamHandlerDeliversQueuedPayloadBeforeDone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamJSON := `{"candidates":[{"author":"1","content":"palm-tail-content"}]}`
	recorder := &closeNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closeChannel:     make(chan bool, 1),
	}
	c, _ := gin.CreateTestContext(recorder)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/chat/completions", nil)
	require.NoError(t, err)
	c.Request = req

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(upstreamJSON))}

	apiErr, responseText := palmStreamHandler(c, resp)
	require.Nil(t, apiErr)
	assert.Equal(t, "palm-tail-content", responseText)

	body := recorder.Body.String()
	assert.Contains(t, body, "palm-tail-content", "the single queued payload was dropped before [DONE]")
	assert.Contains(t, body, "data: [DONE]")
	require.Greater(t,
		strings.LastIndex(body, "data: [DONE]"),
		strings.LastIndex(body, "palm-tail-content"),
		"[DONE] must come after the payload")
}

// Cancellation terminates the handler without upstream EOF.
func TestPalmStreamHandlerClientDisconnectStopsHandler(t *testing.T) {
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

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = palmStreamHandler(c, resp)
	}()

	cancel()
	_ = bodyWriter.CloseWithError(context.Canceled)

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		require.Fail(t, "palm handler did not return after client disconnect")
	}
}
