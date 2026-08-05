package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClaudeStreamBoundaryContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	if constant.StreamingTimeout == 0 {
		constant.StreamingTimeout = 30
		t.Cleanup(func() {
			constant.StreamingTimeout = 0
		})
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return context, recorder
}

func TestClaudeStreamHandlerRejectsMalformedInputJSONDeltaAndStops(t *testing.T) {
	const shouldNotAppear = "valid-event-after-malformed-boundary"
	body := strings.Join([]string{
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"valid-event-after-malformed-boundary"}}`,
		"data: [DONE]",
		"",
	}, "\n")
	context, recorder := newClaudeStreamBoundaryContext(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-test",
		},
		RelayFormat: types.RelayFormatOpenAI,
		IsStream:    true,
	}
	response := &http.Response{
		Body:   io.NopCloser(strings.NewReader(body)),
		Header: make(http.Header),
	}

	var (
		usage    any
		relayErr *types.NewAPIError
	)
	assert.NotPanics(t, func() {
		usage, relayErr = ClaudeStreamHandler(context, response, info)
	})

	assert.Nil(t, usage)
	if assert.NotNil(t, relayErr) {
		assert.Equal(t, http.StatusBadGateway, relayErr.StatusCode)
		assert.Equal(t, types.ErrorCodeBadResponse, relayErr.GetErrorCode())
	}
	require.NotNil(t, info.StreamStatus)
	assert.True(t, info.StreamStatus.HasErrors())
	assert.NotContains(t, recorder.Body.String(), shouldNotAppear)
}

func TestClaudeStreamHandlerPreservesValidInputJSONDelta(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Paris\"}"}}`,
		"data: [DONE]",
		"",
	}, "\n")
	context, recorder := newClaudeStreamBoundaryContext(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-test",
		},
		RelayFormat: types.RelayFormatOpenAI,
		IsStream:    true,
	}
	response := &http.Response{
		Body:   io.NopCloser(strings.NewReader(body)),
		Header: make(http.Header),
	}

	usage, relayErr := ClaudeStreamHandler(context, response, info)

	require.Nil(t, relayErr)
	require.NotNil(t, usage)
	assert.Contains(t, recorder.Body.String(), `\"city\":\"Paris\"`)
}
