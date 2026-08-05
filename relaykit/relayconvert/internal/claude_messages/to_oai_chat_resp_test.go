package claudemessages

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }

// Multiple Claude text blocks must all survive conversion; keeping only
// the last block silently truncated responses (NA-ISSUE-0093).
func TestResponseClaude2OpenAIConcatenatesAllTextBlocks(t *testing.T) {
	claudeResponse := &dto.ClaudeResponse{
		Id:         "msg_1",
		Model:      "claude-test",
		StopReason: "end_turn",
		Content: []dto.ClaudeMediaMessage{
			{Type: "text", Text: strPtr("first block ")},
			{Type: "thinking", Thinking: strPtr("thought A ")},
			{Type: "text", Text: strPtr("second block")},
			{Type: "thinking", Thinking: strPtr("thought B")},
		},
	}

	converted := ResponseClaude2OpenAI(claudeResponse)
	require.Len(t, converted.Choices, 1)
	require.Equal(t, "first block second block", converted.Choices[0].Message.StringContent())
	require.NotNil(t, converted.Choices[0].Message.ReasoningContent)
	require.Equal(t, "thought A thought B", *converted.Choices[0].Message.ReasoningContent)
}
