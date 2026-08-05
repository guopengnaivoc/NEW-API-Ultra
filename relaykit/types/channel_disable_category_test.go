package types

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sentinels standing in for the classes of upstream-controlled material a
// provider can place into an error code. None may survive into the persisted
// channel-disable category.
const (
	categoryKeySentinel    = "SENTINEL-KEY-sk-abcdef1234567890"
	categoryHeaderSentinel = "SENTINEL-HEADER-Authorization-Bearer-xyz"
	categoryBodySentinel   = "SENTINEL-BODY-your-org-1234-has-been-suspended"
)

// WithOpenAIError and WithClaudeError copy provider-supplied strings into
// errorCode, so ChannelDisableCategory must treat that field as hostile:
// anything outside the internal allowlist collapses to a fixed constant.
func TestChannelDisableCategoryRejectsUpstreamControlledErrorCodes(t *testing.T) {
	longCode := strings.Repeat("A", 5000)

	tests := []struct {
		name     string
		err      *NewAPIError
		expected string
		hostile  string
	}{
		{
			name:     "openai code carrying an api key",
			err:      WithOpenAIError(OpenAIError{Code: categoryKeySentinel, Message: categoryBodySentinel}, http.StatusUnauthorized),
			expected: "upstream_error: status_code=401, error_code=unknown",
			hostile:  categoryKeySentinel,
		},
		{
			name:     "claude type carrying an authorization header",
			err:      WithClaudeError(ClaudeError{Type: categoryHeaderSentinel, Message: categoryBodySentinel}, http.StatusUnauthorized),
			expected: "upstream_error: status_code=401, error_code=unknown",
			hostile:  categoryHeaderSentinel,
		},
		{
			name:     "very long openai code",
			err:      WithOpenAIError(OpenAIError{Code: longCode}, http.StatusForbidden),
			expected: "upstream_error: status_code=403, error_code=unknown",
			hostile:  longCode,
		},
		{
			name:     "very long claude type",
			err:      WithClaudeError(ClaudeError{Type: longCode}, http.StatusForbidden),
			expected: "upstream_error: status_code=403, error_code=unknown",
			hostile:  longCode,
		},
		{
			name:     "non-string openai code",
			err:      WithOpenAIError(OpenAIError{Code: map[string]string{"k": categoryKeySentinel}}, http.StatusBadGateway),
			expected: "upstream_error: status_code=502, error_code=unknown",
			hostile:  categoryKeySentinel,
		},
		{
			// An upstream that spoofs the text of an allowlisted code must not
			// get to keep a suffix riding along with it.
			name:     "allowlisted code with a hostile suffix",
			err:      WithOpenAIError(OpenAIError{Code: string(ErrorCodeBadResponse) + " " + categoryKeySentinel}, http.StatusUnauthorized),
			expected: "upstream_error: status_code=401, error_code=unknown",
			hostile:  categoryKeySentinel,
		},
		{
			// Empty type is normalized to "upstream_error" by the constructor,
			// which is not an allowlisted ErrorCode, so it still collapses.
			name:     "empty claude type",
			err:      WithClaudeError(ClaudeError{Type: "", Message: categoryBodySentinel}, http.StatusUnauthorized),
			expected: "upstream_error: status_code=401, error_code=unknown",
			hostile:  categoryBodySentinel,
		},
		{
			name:     "empty openai code",
			err:      WithOpenAIError(OpenAIError{Code: "", Message: categoryBodySentinel}, http.StatusUnauthorized),
			expected: "upstream_error: status_code=401, error_code=unknown",
			hostile:  categoryBodySentinel,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			category := tc.err.ChannelDisableCategory()

			assert.Equal(t, tc.expected, category)
			assert.NotContains(t, category, tc.hostile)
			assert.NotContains(t, category, "SENTINEL")
			assert.LessOrEqual(t, len(category), MaxChannelDisableCategoryLen)
		})
	}
}

// An upstream that supplies exactly an allowlisted code keeps the operator
// detail: the allowlist is a projection onto internal vocabulary, not a
// blanket erase.
func TestChannelDisableCategoryPreservesAllowlistedErrorCodes(t *testing.T) {
	tests := []struct {
		name     string
		err      *NewAPIError
		expected string
	}{
		{
			name:     "internal constructor with a known code",
			err:      NewErrorWithStatusCode(errors.New(categoryBodySentinel), ErrorCodeBadResponseStatusCode, http.StatusUnauthorized),
			expected: "upstream_error: status_code=401, error_code=bad_response_status_code",
		},
		{
			name:     "internal channel error code",
			err:      NewErrorWithStatusCode(errors.New(categoryBodySentinel), ErrorCodeChannelInvalidKey, http.StatusForbidden),
			expected: "upstream_error: status_code=403, error_code=channel:invalid_key",
		},
		{
			// NewError defaults the status to 500.
			name:     "code-only constructor defaults the status",
			err:      NewError(errors.New(categoryBodySentinel), ErrorCodeDoRequestFailed),
			expected: "upstream_error: status_code=500, error_code=do_request_failed",
		},
		{
			// An upstream echoing exactly an internal code is indistinguishable
			// from the internal value and is allowed through as that constant.
			name:     "upstream echoing exactly an allowlisted code",
			err:      WithOpenAIError(OpenAIError{Code: string(ErrorCodeBadResponse)}, http.StatusUnauthorized),
			expected: "upstream_error: status_code=401, error_code=bad_response",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			category := tc.err.ChannelDisableCategory()

			assert.Equal(t, tc.expected, category)
			assert.NotContains(t, category, categoryBodySentinel)
			assert.LessOrEqual(t, len(category), MaxChannelDisableCategoryLen)
		})
	}
}

// The status half of the category is also a bounded internal value: only a
// syntactically valid HTTP status is named.
func TestChannelDisableCategoryStatusCodeContract(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		expected string
	}{
		{name: "zero status is omitted", status: 0, expected: "upstream_error: error_code=bad_response"},
		{name: "negative status is omitted", status: -1, expected: "upstream_error: error_code=bad_response"},
		{name: "below-range status is omitted", status: 99, expected: "upstream_error: error_code=bad_response"},
		{name: "above-range status is omitted", status: 600, expected: "upstream_error: error_code=bad_response"},
		{name: "absurd status is omitted", status: 999999999, expected: "upstream_error: error_code=bad_response"},
		{name: "lowest valid status", status: 100, expected: "upstream_error: status_code=100, error_code=bad_response"},
		{name: "ordinary status", status: http.StatusUnauthorized, expected: "upstream_error: status_code=401, error_code=bad_response"},
		{name: "highest valid status", status: 599, expected: "upstream_error: status_code=599, error_code=bad_response"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := NewErrorWithStatusCode(errors.New(categoryBodySentinel), ErrorCodeBadResponse, tc.status)
			category := err.ChannelDisableCategory()

			assert.Equal(t, tc.expected, category)
			assert.LessOrEqual(t, len(category), MaxChannelDisableCategoryLen)
		})
	}
}

func TestChannelDisableCategoryNilError(t *testing.T) {
	var err *NewAPIError

	category := err.ChannelDisableCategory()

	assert.Equal(t, "upstream_error", category)
	assert.LessOrEqual(t, len(category), MaxChannelDisableCategoryLen)
}

// Every declared ErrorCode must be allowlisted, otherwise operators silently
// lose detail. Unknown values must still fail closed.
func TestChannelDisableCategoryAllowlistCoversEveryDeclaredErrorCode(t *testing.T) {
	require.NotEmpty(t, channelDisableErrorCodes)

	for code := range channelDisableErrorCodes {
		err := NewErrorWithStatusCode(errors.New(categoryBodySentinel), code, http.StatusUnauthorized)
		category := err.ChannelDisableCategory()

		assert.Contains(t, category, string(code), "allowlisted code should survive")
		assert.LessOrEqual(t, len(category), MaxChannelDisableCategoryLen,
			"allowlisted code %q must fit the declared bound", code)
	}

	unknown := NewErrorWithStatusCode(errors.New("x"), ErrorCode("not_a_declared_code"), http.StatusUnauthorized)
	assert.Equal(t, "upstream_error: status_code=401, error_code=unknown", unknown.ChannelDisableCategory())
}
