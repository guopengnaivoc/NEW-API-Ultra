package claudemessages

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeMessagesRequestToOpenAIChatRejectsImageWithoutSource(t *testing.T) {
	request := dto.ClaudeRequest{
		Messages: []dto.ClaudeMessage{
			{
				Role: "user",
				Content: []dto.ClaudeMediaMessage{
					{Type: "image"},
				},
			},
		},
	}
	var (
		converted *dto.GeneralOpenAIRequest
		err       error
	)

	assert.NotPanics(t, func() {
		converted, err = ClaudeMessagesRequestToOpenAIChat(request, nil)
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "claude message 0 content 0 image source is required")
	assert.Nil(t, converted)
}

func TestStreamResponseClaude2OpenAIDropsInputJSONDeltaWithoutPartialJSON(t *testing.T) {
	response := &dto.ClaudeResponse{
		Type: "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{
			Type: "input_json_delta",
		},
	}
	var converted *dto.ChatCompletionsStreamResponse

	assert.NotPanics(t, func() {
		converted = StreamResponseClaude2OpenAI(response)
	})

	assert.Nil(t, converted)
}
