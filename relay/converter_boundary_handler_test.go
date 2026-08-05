package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const converterBoundarySecret = "converter-boundary-private-marker"

func newConverterBoundaryContext(t *testing.T, channelType int, path string) *gin.Context {
	t.Helper()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, path, nil)
	common.SetContextKey(context, constant.ContextKeyChannelType, channelType)
	common.SetContextKey(context, constant.ContextKeyChannelBaseUrl, "https://unused.invalid")
	common.SetContextKey(context, constant.ContextKeyChannelKey, converterBoundarySecret)
	common.SetContextKey(context, constant.ContextKeyOriginalModel, "converter-boundary-model")
	return context
}

func assertClientConversionError(t *testing.T, relayErr *types.NewAPIError) {
	t.Helper()

	require.NotNil(t, relayErr)
	assert.Equal(t, http.StatusBadRequest, relayErr.StatusCode)
	assert.Equal(t, types.ErrorCodeInvalidRequest, relayErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(relayErr))
	assert.NotContains(t, relayErr.ToOpenAIError().Message, converterBoundarySecret)
}

func TestTextHelperRejectsMalformedClientConversionAsBadRequest(t *testing.T) {
	maxTokens := uint(128)
	tests := []struct {
		name    string
		request *dto.GeneralOpenAIRequest
	}{
		{
			name: "non-string tool schema type",
			request: &dto.GeneralOpenAIRequest{
				Model:     "converter-boundary-model",
				MaxTokens: &maxTokens,
				Tools: []dto.ToolCallRequest{
					{
						Type: "function",
						Function: dto.FunctionRequest{
							Name: "lookup",
							Parameters: map[string]any{
								"type":        7,
								"description": converterBoundarySecret,
							},
						},
					},
				},
			},
		},
		{
			name: "mixed stop array",
			request: &dto.GeneralOpenAIRequest{
				Model:     "converter-boundary-model",
				MaxTokens: &maxTokens,
				Stop: []any{
					"done",
					map[string]any{"secret": converterBoundarySecret},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context := newConverterBoundaryContext(
				t,
				constant.ChannelTypeAnthropic,
				"/v1/chat/completions",
			)
			info := &relaycommon.RelayInfo{
				OriginModelName: "converter-boundary-model",
				RelayMode:       relayconstant.RelayModeChatCompletions,
				RelayFormat:     types.RelayFormatOpenAI,
				Request:         test.request,
			}

			var relayErr *types.NewAPIError
			assert.NotPanics(t, func() {
				relayErr = TextHelper(context, info)
			})

			assertClientConversionError(t, relayErr)
		})
	}
}

func TestTextHelperPreservesUnclassifiedConversionFailure(t *testing.T) {
	maxTokens := uint(128)
	message := dto.Message{Role: "user"}
	message.SetStringContent(converterBoundarySecret)
	request := &dto.GeneralOpenAIRequest{
		Model:     "converter-boundary-model",
		MaxTokens: &maxTokens,
		Messages:  []dto.Message{message},
	}
	context := newConverterBoundaryContext(
		t,
		constant.ChannelTypeReplicate,
		"/v1/completions",
	)
	info := &relaycommon.RelayInfo{
		OriginModelName: "converter-boundary-model",
		RelayMode:       relayconstant.RelayModeCompletions,
		RelayFormat:     types.RelayFormatOpenAI,
		Request:         request,
	}

	var relayErr *types.NewAPIError
	assert.NotPanics(t, func() {
		relayErr = TextHelper(context, info)
	})

	require.NotNil(t, relayErr)
	assert.Equal(t, http.StatusInternalServerError, relayErr.StatusCode)
	assert.Equal(t, types.ErrorCodeConvertRequestFailed, relayErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(relayErr))
	assert.Equal(
		t,
		"replicate adaptor: ConvertOpenAIRequest is not implemented",
		relayErr.ToOpenAIError().Message,
	)
	assert.NotContains(t, relayErr.ToOpenAIError().Message, converterBoundarySecret)
}

func TestClaudeHelperRejectsMissingImageSourceAsBadRequest(t *testing.T) {
	maxTokens := uint(128)
	request := &dto.ClaudeRequest{
		Model:     "converter-boundary-model",
		MaxTokens: &maxTokens,
		Messages: []dto.ClaudeMessage{
			{
				Role: "user",
				Content: []dto.ClaudeMediaMessage{
					{Type: "text", Text: common.GetPointer(converterBoundarySecret)},
					{Type: "image"},
				},
			},
		},
	}
	context := newConverterBoundaryContext(
		t,
		constant.ChannelTypeOpenAI,
		"/v1/messages",
	)
	info := &relaycommon.RelayInfo{
		OriginModelName: "converter-boundary-model",
		RelayFormat:     types.RelayFormatClaude,
		Request:         request,
	}

	var relayErr *types.NewAPIError
	assert.NotPanics(t, func() {
		relayErr = ClaudeHelper(context, info)
	})

	assertClientConversionError(t, relayErr)
}
