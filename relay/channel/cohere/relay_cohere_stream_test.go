package cohere

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	closeChannel chan bool
}

func (r *closeNotifyRecorder) CloseNotify() <-chan bool {
	return r.closeChannel
}

func (r *closeNotifyRecorder) Flush() {}

func (r *closeNotifyRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack not supported")
}

type blockingStreamBody struct {
	mu     sync.Mutex
	sent   bool
	chunk  []byte
	closed chan struct{}
	ready  chan struct{}
}

func (b *blockingStreamBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sent {
		<-b.closed
		return 0, io.EOF
	}
	b.sent = true
	close(b.ready)
	n := copy(p, b.chunk)
	return n, nil
}

func (b *blockingStreamBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

func TestCohereStreamHandlerClientDisconnectClosesUpstreamBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", nil)
	require.NoError(t, err)

	recorder := &closeNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closeChannel:     make(chan bool, 1),
	}
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req

	body := &blockingStreamBody{
		chunk:  []byte(`{"is_finished":false,"text":"hello"}` + "\n"),
		closed: make(chan struct{}),
		ready:  make(chan struct{}),
	}
	resp := &http.Response{
		Body: body,
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "cohere-test",
		},
	}

	finished := make(chan *types.NewAPIError, 1)
	go func() {
		_, err := cohereStreamHandler(c, info, resp)
		finished <- err
	}()

	require.Eventually(t, func() bool {
		select {
		case <-body.ready:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	cancel()

	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("cohereStreamHandler returned unexpected error: %s", err.Error())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cohere stream handler did not return after request context cancel")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("cohere upstream body was not closed after client disconnect")
	}
}
