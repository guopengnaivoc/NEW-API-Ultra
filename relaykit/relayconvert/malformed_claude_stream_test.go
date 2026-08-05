package relayconvert

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertStreamResponseRejectsInputJSONDeltaWithoutPartialJSON(t *testing.T) {
	response := &dto.ClaudeResponse{
		Type: "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{
			Type: "input_json_delta",
		},
	}
	var (
		result *ResponseResult
		err    error
	)

	assert.NotPanics(t, func() {
		result, err = ConvertStreamResponse(
			context.Background(),
			nil,
			types.RelayFormatOpenAI,
			response,
		)
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "partial_json is required for input_json_delta")
	assert.Nil(t, result)
}
