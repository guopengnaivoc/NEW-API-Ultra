package oaichat

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIChatRequestToClaudeMessagesRejectsInvalidToolSchemaType(t *testing.T) {
	maxTokens := uint(128)
	request := dto.GeneralOpenAIRequest{
		Model:     "claude-test",
		MaxTokens: &maxTokens,
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name: "lookup",
					Parameters: map[string]any{
						"type": 7,
					},
				},
			},
		},
	}
	var (
		converted *dto.ClaudeRequest
		err       error
	)

	assert.NotPanics(t, func() {
		converted, err = OpenAIChatRequestToClaudeMessages(
			context.Background(),
			nil,
			request,
		)
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, `tool "lookup" parameters.type must be a string`)
	assert.Nil(t, converted)
}

func TestOpenAIChatRequestToClaudeMessagesRejectsMixedStopArray(t *testing.T) {
	maxTokens := uint(128)
	request := dto.GeneralOpenAIRequest{
		Model:     "claude-test",
		MaxTokens: &maxTokens,
		Stop:      []any{"done", 7},
	}
	var (
		converted *dto.ClaudeRequest
		err       error
	)

	assert.NotPanics(t, func() {
		converted, err = OpenAIChatRequestToClaudeMessages(
			context.Background(),
			nil,
			request,
		)
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "stop[1] must be a string")
	assert.Nil(t, converted)
}
