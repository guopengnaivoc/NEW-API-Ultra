package channel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type responseCommitTracker struct {
	header    http.Header
	committed atomic.Bool
}

func (w *responseCommitTracker) Header() http.Header {
	return w.header
}

func (w *responseCommitTracker) Write(data []byte) (int, error) {
	w.committed.Store(true)
	return len(data), nil
}

func (w *responseCommitTracker) WriteHeader(int) {
	w.committed.Store(true)
}

func (w *responseCommitTracker) Flush() {
	w.committed.Store(true)
}

type upstreamResult struct {
	response *http.Response
	err      error
}

func init() {
	gin.SetMode(gin.TestMode)
}

func TestDoRequestDoesNotCommitStreamBeforeUpstreamHeaders(t *testing.T) {
	originalSettings, err := config.ConfigToMap(operation_setting.GetGeneralSetting())
	require.NoError(t, err)
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("general_setting", originalSettings)
		require.NoError(t, updateErr)
		require.True(t, updated)
	})
	updated, err := config.GlobalConfig.Update("general_setting", map[string]string{
		"ping_interval_enabled": "true",
		"ping_interval_seconds": "1",
	})
	require.NoError(t, err)
	require.True(t, updated)

	requestStarted := make(chan struct{}, 1)
	releaseUpstream := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestStarted <- struct{}{}
		<-releaseUpstream
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(proxy.Close)
	t.Cleanup(func() { service.InvalidateProxyClient(proxy.URL) })

	writer := &responseCommitTracker{header: make(http.Header)}
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	upstreamRequest, err := http.NewRequest(http.MethodPost, "http://upstream.invalid/v1/chat/completions", http.NoBody)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		IsStream: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{Proxy: proxy.URL},
		},
	}

	result := make(chan upstreamResult, 1)
	go func() {
		response, requestErr := doRequest(ctx, upstreamRequest, info)
		result <- upstreamResult{response: response, err: requestErr}
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		require.Fail(t, "upstream proxy did not receive the request")
	}
	<-time.After(1200 * time.Millisecond)
	committedBeforeHeaders := writer.committed.Load()
	close(releaseUpstream)

	var requestResult upstreamResult
	select {
	case requestResult = <-result:
	case <-time.After(2 * time.Second):
		require.Fail(t, "request did not return after upstream headers were released")
	}
	require.NoError(t, requestResult.err)
	require.NotNil(t, requestResult.response)
	t.Cleanup(func() { _ = requestResult.response.Body.Close() })
	require.Equal(t, http.StatusUnauthorized, requestResult.response.StatusCode)
	require.False(t, committedBeforeHeaders, "SSE keepalive committed downstream HTTP 200 before the upstream status was known")
}

func TestProcessHeaderOverride_ChannelTestSkipsPassthroughRules(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Empty(t, headers)
}

func TestProcessHeaderOverride_ChannelTestSkipsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	_, ok := headers["x-upstream-trace"]
	require.False(t, ok)
}

func TestProcessHeaderOverride_NonTestKeepsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-upstream-trace"])
}

func TestProcessHeaderOverrideRejectsSensitiveClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	protectedNames := []string{
		"Authorization",
		"COOKIE",
		"Proxy_Authorization",
		"Api-Key",
		"X_API_KEY",
		"x-Goog-Api-Key",
		"Mj_Api_Secret",
		"X_Auth_Session",
		"X-Security-Proof",
		"X_Turnstile_Token",
		"Sec_WebSocket_Key",
		"Sec-WebSocket-Protocol",
	}
	for _, protectedName := range protectedNames {
		protectedName := protectedName
		t.Run(protectedName, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			ctx.Request.Header.Set(protectedName, "caller-secret")

			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					HeadersOverride: map[string]any{
						"X-Stolen": "{client_header:" + protectedName + "}",
					},
				},
			}

			headers, err := processHeaderOverride(info, ctx)
			require.Nil(t, headers)
			var apiErr *relaytypes.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, relaytypes.ErrorCodeChannelHeaderOverrideInvalid, apiErr.GetErrorCode())
			require.NotContains(t, err.Error(), "caller-secret")
		})
	}
}

func TestProcessHeaderOverrideAllowsChannelOwnedAuthorization(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("Authorization", "Bearer caller-secret")

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: "provider-secret",
			HeadersOverride: map[string]any{
				"Authorization": "Bearer {api_key}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "Bearer provider-secret", headers["authorization"])
	require.NotContains(t, headers["authorization"], "caller-secret")
}

func TestProcessHeaderOverridePassthroughRejectsSensitiveClientHeaders(t *testing.T) {
	t.Parallel()

	rules := map[string]map[string]any{
		"wildcard": {
			"*": "",
		},
		"regex": {
			`re:(?i)^(x[-_]trace[-_]id|authorization|proxy[-_]authorization|x[-_]api[-_]key|x[-_]auth[-_]session|sec[-_]websocket[-_]protocol)$`: "",
		},
	}
	for name, headersOverride := range rules {
		name := name
		headersOverride := headersOverride
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			ctx.Request.Header.Set("X-Trace-Id", "trace-123")
			ctx.Request.Header.Set("Authorization", "Bearer caller-token")
			ctx.Request.Header.Set("Proxy_Authorization", "Basic caller-proxy-token")
			ctx.Request.Header.Set("X_API_KEY", "caller-api-key")
			ctx.Request.Header.Set("X_Auth_Session", "caller-session-id")
			ctx.Request.Header.Set("Sec_WebSocket_Protocol", "openai-insecure-api-key.caller-key")

			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					HeadersOverride: headersOverride,
				},
			}

			headers, err := processHeaderOverride(info, ctx)
			require.NoError(t, err)
			require.Equal(t, "trace-123", headers["x-trace-id"])
			for _, forbidden := range []string{
				"authorization",
				"proxy_authorization",
				"x_api_key",
				"x_auth_session",
				"sec_websocket_protocol",
			} {
				require.NotContains(t, headers, forbidden)
			}
			for headerName, value := range headers {
				require.False(t, relaycommon.IsSensitiveClientHeader(headerName), "protected header %q was forwarded with value %q", headerName, value)
			}
		})
	}
}

func TestProcessHeaderOverride_RuntimeOverrideIsFinalHeaderMap(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		IsChannelTest:             false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"x-static":  "runtime-value",
			"x-runtime": "runtime-only",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
				"X-Legacy": "legacy-only",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "runtime-value", headers["x-static"])
	require.Equal(t, "runtime-only", headers["x-runtime"])
	_, exists := headers["x-legacy"]
	require.False(t, exists)
}

func TestProcessHeaderOverride_PassthroughSkipsAcceptEncoding(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")
	ctx.Request.Header.Set("Accept-Encoding", "gzip")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-trace-id"])

	_, hasAcceptEncoding := headers["accept-encoding"]
	require.False(t, hasAcceptEncoding)
}

func TestProcessHeaderOverride_PassHeadersTemplateSetsRuntimeHeaders(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("Originator", "Codex CLI")
	ctx.Request.Header.Set("Session_id", "sess-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{
					map[string]any{
						"mode":  "pass_headers",
						"value": []any{"Originator", "Session_id", "X-Codex-Beta-Features"},
					},
				},
			},
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4.1"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	require.Equal(t, "legacy-value", info.RuntimeHeadersOverride["x-static"])

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "Codex CLI", headers["originator"])
	require.Equal(t, "sess-123", headers["session_id"])
	_, exists = headers["x-codex-beta-features"]
	require.False(t, exists)

	upstreamReq := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyHeaderOverrideToRequest(upstreamReq, headers)
	require.Equal(t, "Codex CLI", upstreamReq.Header.Get("Originator"))
	require.Equal(t, "sess-123", upstreamReq.Header.Get("Session_id"))
	require.Empty(t, upstreamReq.Header.Get("X-Codex-Beta-Features"))
}

func TestDoRequestReturnsErrorForNilRequest(t *testing.T) {
	t.Parallel()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := doRequest(ctx, nil, &relaycommon.RelayInfo{})
	require.Error(t, err)
	require.EqualError(t, err, "request is nil")
}

func TestDoRequestPropagatesCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Keep the request pending to force propagation of the upstream context cancellation.
		select {}
	}))
	t.Cleanup(server.Close)

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	req = req.WithContext(canceledCtx)

	start := time.Now()
	_, err = doRequest(ctx, req, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "upstream error: do request failed")
	if time.Since(start) > 300*time.Millisecond {
		t.Fatalf("request did not respect upstream context in time: %s", time.Since(start))
	}
	require.NotNil(t, err)
	if !strings.Contains(strings.ToLower(err.Error()), "upstream error: do request failed") {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
}
