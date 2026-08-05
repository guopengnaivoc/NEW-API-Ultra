package channel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBodyLifetimeGinContext(t *testing.T) *gin.Context {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return ctx
}

func newBodyLifetimeRelayInfo(proxyURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{Proxy: proxyURL},
		},
	}
}

// doRequest returns when upstream headers arrive, but callers stream the body
// afterwards. The request context must stay alive until body EOF/Close: the
// upstream here sends headers, blocks until doRequest has provably returned,
// and only then sends the body. Every byte must remain readable.
func TestDoRequestBodyReadableAfterReturn(t *testing.T) {
	doRequestReturned := make(chan struct{})
	const tailPayload = "tail-payload-after-return"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-doRequestReturned:
		case <-time.After(5 * time.Second):
			return
		}
		_, _ = io.WriteString(w, tailPayload)
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { service.InvalidateProxyClient(upstream.URL) })

	req, err := http.NewRequest(http.MethodPost, "http://upstream.invalid/v1/chat/completions", http.NoBody)
	require.NoError(t, err)

	resp, err := doRequest(newBodyLifetimeGinContext(t), req, newBodyLifetimeRelayInfo(upstream.URL))
	require.NoError(t, err)
	require.NotNil(t, resp)
	close(doRequestReturned)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "body read failed after doRequest returned; the request context was canceled too early")
	assert.Equal(t, tailPayload, string(body))
	require.NoError(t, resp.Body.Close())
}

// countingCancelBody wraps cancelOnCloseBody's cancel func to count firings.
func TestCancelOnCloseBodyCancelsExactlyOnce(t *testing.T) {
	testCases := []struct {
		name    string
		consume func(t *testing.T, rc io.ReadCloser)
	}{
		{
			name: "read to EOF then close",
			consume: func(t *testing.T, rc io.ReadCloser) {
				_, err := io.ReadAll(rc)
				require.NoError(t, err)
				require.NoError(t, rc.Close())
				require.NoError(t, rc.Close())
			},
		},
		{
			name: "close without reading",
			consume: func(t *testing.T, rc io.ReadCloser) {
				require.NoError(t, rc.Close())
			},
		},
		{
			name: "terminal read error",
			consume: func(t *testing.T, rc io.ReadCloser) {
				buf := make([]byte, 4)
				for {
					if _, err := rc.Read(buf); err != nil {
						break
					}
				}
				_ = rc.Close()
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cancelCount atomic.Int64
			rc := newCancelOnCloseBody(
				io.NopCloser(strings.NewReader("stream-data")),
				func() { cancelCount.Add(1) },
			)
			tc.consume(t, rc)
			assert.Equal(t, int64(1), cancelCount.Load(), "cancel must fire exactly once")
		})
	}
}

func TestCancelOnCloseBodyNilBodyCancelsImmediately(t *testing.T) {
	var cancelCount atomic.Int64
	rc := newCancelOnCloseBody(nil, func() { cancelCount.Add(1) })
	require.Equal(t, int64(1), cancelCount.Load(), "nil body must release the context immediately")

	// The replacement body behaves as an empty, closable stream.
	n, err := rc.Read(make([]byte, 1))
	assert.Zero(t, n)
	assert.ErrorIs(t, err, io.EOF)
	assert.NoError(t, rc.Close())
	assert.Equal(t, int64(1), cancelCount.Load())
}

// Early error exits must release the context themselves: a canceled inbound
// context surfaces as an error, not a leak.
func TestDoRequestCancelsContextOnEarlyErrors(t *testing.T) {
	t.Run("request failure", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/unreachable", http.NoBody)
		require.NoError(t, err)
		resp, err := doRequest(newBodyLifetimeGinContext(t), req, newBodyLifetimeRelayInfo(""))
		require.Error(t, err)
		require.Nil(t, resp)
	})

	t.Run("caller context already canceled", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		req, err := http.NewRequestWithContext(canceledCtx, http.MethodPost, "http://upstream.invalid/", http.NoBody)
		require.NoError(t, err)
		resp, err := doRequest(newBodyLifetimeGinContext(t), req, newBodyLifetimeRelayInfo(""))
		// The relay layer intentionally replaces upstream transport errors
		// with a hidden generic message (ErrOptionWithHideErrMsg), so the
		// contract here is error + nil response, not a context.Canceled
		// unwrap.
		require.Error(t, err)
		require.Nil(t, resp)
	})
}

// Caller cancellation and the relay timeout still interrupt an in-flight body:
// transferring cancel ownership to the body must not detach the body from the
// merged inbound/timeout context.
func TestDoRequestBodyStillHonorsCallerCancellation(t *testing.T) {
	upstreamBlocked := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-upstreamBlocked:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { close(upstreamBlocked) })
	t.Cleanup(func() { service.InvalidateProxyClient(upstream.URL) })

	// The realistic caller-cancel signal is the inbound (gin) request context:
	// mergeRequestContexts prefers it when neither context carries a deadline,
	// and a client disconnect cancels exactly this context in production.
	callerCtx, callerCancel := context.WithCancel(context.Background())
	ginCtx := newBodyLifetimeGinContext(t)
	ginCtx.Request = ginCtx.Request.WithContext(callerCtx)
	req, err := http.NewRequest(http.MethodPost, "http://upstream.invalid/", http.NoBody)
	require.NoError(t, err)

	resp, err := doRequest(ginCtx, req, newBodyLifetimeRelayInfo(upstream.URL))
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	readResult := make(chan error, 1)
	go func() {
		_, readErr := io.ReadAll(resp.Body)
		readResult <- readErr
	}()

	callerCancel()
	select {
	case readErr := <-readResult:
		require.Error(t, readErr, "body read must be interrupted by caller cancellation")
	case <-time.After(5 * time.Second):
		require.Fail(t, "body read was not interrupted after caller cancellation")
	}
}
