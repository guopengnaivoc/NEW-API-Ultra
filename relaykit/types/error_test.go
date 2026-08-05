package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCountTokenErrorMasksSensitiveDetailsInEveryClientRepresentation(t *testing.T) {
	const sensitiveMessage = "count token failed for " +
		"https://files.example.com/private/customer-123/image.png" +
		"?X-Amz-Signature=TOPSECRETSIGNATURE"
	newAPIError := NewError(errors.New(sensitiveMessage), ErrorCodeCountTokenFailed)

	messages := map[string]string{
		"masked error": newAPIError.MaskSensitiveError(),
		"OpenAI":       newAPIError.ToOpenAIError().Message,
		"Claude":       newAPIError.ToClaudeError().Message,
	}

	for name, message := range messages {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, message, "count token failed")
			assert.NotContains(t, message, "files.example.com")
			assert.NotContains(t, message, "private")
			assert.NotContains(t, message, "customer-123")
			assert.NotContains(t, message, "image.png")
			assert.NotContains(t, message, "TOPSECRETSIGNATURE")
		})
	}
}
