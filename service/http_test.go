package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyUpstreamResponseHeadersPolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  UpstreamHeaderPolicy
		allowed []string
		denied  []string
	}{
		{
			name:   "relay",
			policy: UpstreamHeaderPolicyRelay,
			allowed: []string{
				"Content-Type",
				"Content-Encoding",
				"Content-Language",
				"Retry-After",
				"Request-Id",
				"X-Request-Id",
				"X-Reasoning-Included",
				"X-Codex-Turn-State",
			},
			denied: []string{
				"Content-Length",
				"Content-Disposition",
				"ETag",
				"Last-Modified",
			},
		},
		{
			name:   "media",
			policy: UpstreamHeaderPolicyMedia,
			allowed: []string{
				"Content-Type",
				"Content-Encoding",
				"Content-Language",
				"Retry-After",
				"Request-Id",
				"X-Request-Id",
				"X-Reasoning-Included",
				"X-Codex-Turn-State",
				"Content-Length",
				"Content-Disposition",
				"ETag",
				"Last-Modified",
			},
			denied: []string{
				"Accept-Ranges",
				"Content-Range",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			source := make(http.Header)
			for _, name := range test.allowed {
				source.Add(name, "allowed-first")
				source.Add(name, "allowed-second")
			}
			for _, name := range test.denied {
				source.Set(name, "denied")
			}

			CopyUpstreamResponseHeaders(context, source, test.policy)

			for _, name := range test.allowed {
				assert.Equal(
					t,
					[]string{"allowed-first", "allowed-second"},
					recorder.Header().Values(name),
					name,
				)
			}
			for _, name := range test.denied {
				assert.Empty(t, recorder.Header().Values(name), name)
			}
		})
	}
}

func TestCopyUpstreamResponseHeadersRejectsSecurityAndConnectionFields(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	source := http.Header{
		"Connection":                  {"Content-Type, Request-Id, X-Request-Id"},
		"Content-Type":                {"video/mp4"},
		"Request-Id":                  {"anthropic-provider-request"},
		"X-Request-Id":                {"provider-request"},
		"Set-Cookie":                  {"session=attacker"},
		"Authorization":               {"Bearer sentinel"},
		"Proxy-Authorization":         {"Basic sentinel"},
		"Www-Authenticate":            {"Basic"},
		"Proxy-Authenticate":          {"Basic"},
		"X-Goog-Api-Key":              {"credential-sentinel"},
		"Location":                    {"https://example.invalid/?key=sentinel"},
		"Content-Location":            {"https://example.invalid/?key=sentinel"},
		"Link":                        {"<https://example.invalid/?key=sentinel>"},
		"Refresh":                     {"0;url=https://example.invalid/?key=sentinel"},
		"Cache-Control":               {"public"},
		"Expires":                     {"Wed, 21 Oct 2030 07:28:00 GMT"},
		"Vary":                        {"Authorization"},
		"Access-Control-Allow-Origin": {"*"},
		"Content-Security-Policy":     {"default-src *"},
		"Strict-Transport-Security":   {"max-age=31536000"},
		"X-Frame-Options":             {"ALLOWALL"},
		"X-Secret":                    {"credential-sentinel"},
		"Keep-Alive":                  {"timeout=5"},
		"Transfer-Encoding":           {"chunked"},
		"Upgrade":                     {"websocket"},
	}

	CopyUpstreamResponseHeaders(context, source, UpstreamHeaderPolicyMedia)

	assert.Empty(t, recorder.Header())
}

func TestCopyUpstreamResponseHeadersCapturesButDoesNotForwardGatewayRequestID(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	source := http.Header{
		common.RequestIdKey: {"upstream-correlation"},
	}

	CopyUpstreamResponseHeaders(context, source, UpstreamHeaderPolicyRelay)

	value, exists := context.Get(common.UpstreamRequestIdKey)
	require.True(t, exists)
	assert.Equal(t, "upstream-correlation", value)
	assert.Empty(t, recorder.Header().Values(common.RequestIdKey))
}

func TestCopyUpstreamResponseHeadersUnknownPolicyFailsClosed(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	source := http.Header{"Content-Type": {"application/json"}}

	CopyUpstreamResponseHeaders(context, source, UpstreamHeaderPolicy(255))

	assert.Empty(t, recorder.Header())
}

func TestIOCopyBytesGracefullyUsesRelayHeaderPolicy(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	source := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   {"application/json"},
			"Content-Length": {"999"},
			"Set-Cookie":     {"session=attacker"},
		},
	}

	IOCopyBytesGracefully(context, source, []byte(`{"ok":true}`))

	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "11", recorder.Header().Get("Content-Length"))
	assert.Empty(t, recorder.Header().Values("Set-Cookie"))
}
