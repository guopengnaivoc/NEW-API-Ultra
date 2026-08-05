package coze

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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCozeDoRequestNonStreamCompletesWithPolling(t *testing.T) {
	t.Parallel()

	var retrieveCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/chat":
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"id":"chat-id","conversation_id":"conv-id","status":"created","usage":{"token_count":0}}}`))
		case "/v3/chat/retrieve":
			count := retrieveCount.Add(1)
			if count == 1 {
				_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"status":"running","usage":{"token_count":0,"output_count":0,"input_count":0}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"status":"completed","usage":{"token_count":10,"output_count":4,"input_count":6}}}`))
		case "/v3/chat/message/list":
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":[{"type":"answer","content":"\"hello\"","created_at":1000}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = req
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: server.URL,
		},
	}

	result, err := (&Adaptor{}).DoRequest(ctx, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("DoRequest failed: %T %v", err, err)
	}

	resp, ok := result.(*http.Response)
	require.True(t, ok)
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	_, apiErr := (&Adaptor{}).DoResponse(ctx, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(string(responseBody))),
	}, info)
	if apiErr != nil {
		t.Fatalf("DoResponse failed: %T %v", apiErr, apiErr)
	}
	require.GreaterOrEqual(t, retrieveCount.Load(), int32(2))
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestCozeDoRequestPollingRespectsRequestDeadline(t *testing.T) {
	t.Parallel()

	var retrieveCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/chat":
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"id":"chat-id","conversation_id":"conv-id","status":"created"}}`))
		case "/v3/chat/retrieve":
			retrieveCount.Add(1)
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"status":"running","usage":{"token_count":0}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	baseCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	t.Cleanup(cancel)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	req = req.WithContext(baseCtx)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = req
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: server.URL,
		},
	}

	start := time.Now()
	result, err := (&Adaptor{}).DoRequest(ctx, info, strings.NewReader(`{}`))
	require.Error(t, err)
	require.Nil(t, result)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Greater(t, time.Since(start), 0*time.Millisecond)
	require.Less(t, time.Since(start), 800*time.Millisecond)
	require.GreaterOrEqual(t, retrieveCount.Load(), int32(1))
}

func TestDoRequestContextHelpers(t *testing.T) {
	t.Parallel()

	ctx := &gin.Context{}
	base, cancel := context.WithCancel(context.Background())
	cancel()
	if ctx.Request == nil {
		req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		ctx.Request = req
	}
	ctx.Request = ctx.Request.WithContext(base)

	requestCtx, _ := requestContextForPolling(ctx)
	require.Error(t, requestCtx.Err())
	require.ErrorIs(t, requestCtx.Err(), context.Canceled)
}
