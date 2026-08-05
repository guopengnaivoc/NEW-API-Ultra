package dto

import (
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeneralOpenAIRequestPreserveExplicitZeroValues(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-4.1",
		"stream":false,
		"max_tokens":0,
		"max_completion_tokens":0,
		"top_p":0,
		"top_k":0,
		"n":0,
		"frequency_penalty":0,
		"presence_penalty":0,
		"seed":0,
		"logprobs":false,
		"top_logprobs":0,
		"dimensions":0,
		"return_images":false,
		"return_related_questions":false
	}`)

	var req GeneralOpenAIRequest
	err := kitutil.Unmarshal(raw, &req)
	require.NoError(t, err)

	encoded, err := kitutil.Marshal(req)
	require.NoError(t, err)

	require.True(t, gjson.GetBytes(encoded, "stream").Exists())
	require.True(t, gjson.GetBytes(encoded, "max_tokens").Exists())
	require.True(t, gjson.GetBytes(encoded, "max_completion_tokens").Exists())
	require.True(t, gjson.GetBytes(encoded, "top_p").Exists())
	require.True(t, gjson.GetBytes(encoded, "top_k").Exists())
	require.True(t, gjson.GetBytes(encoded, "n").Exists())
	require.True(t, gjson.GetBytes(encoded, "frequency_penalty").Exists())
	require.True(t, gjson.GetBytes(encoded, "presence_penalty").Exists())
	require.True(t, gjson.GetBytes(encoded, "seed").Exists())
	require.True(t, gjson.GetBytes(encoded, "logprobs").Exists())
	require.True(t, gjson.GetBytes(encoded, "top_logprobs").Exists())
	require.True(t, gjson.GetBytes(encoded, "dimensions").Exists())
	require.True(t, gjson.GetBytes(encoded, "return_images").Exists())
	require.True(t, gjson.GetBytes(encoded, "return_related_questions").Exists())
}

func TestOpenAIResponsesRequestPreserveExplicitZeroValues(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-4.1",
		"max_output_tokens":0,
		"max_tool_calls":0,
		"stream":false,
		"top_p":0
	}`)

	var req OpenAIResponsesRequest
	err := kitutil.Unmarshal(raw, &req)
	require.NoError(t, err)

	encoded, err := kitutil.Marshal(req)
	require.NoError(t, err)

	require.True(t, gjson.GetBytes(encoded, "max_output_tokens").Exists())
	require.True(t, gjson.GetBytes(encoded, "max_tool_calls").Exists())
	require.True(t, gjson.GetBytes(encoded, "stream").Exists())
	require.True(t, gjson.GetBytes(encoded, "top_p").Exists())
}

func TestStreamOptionsPreserveExplicitBooleanValues(t *testing.T) {
	tests := []struct {
		name                   string
		raw                    string
		wantStreamOptions      bool
		wantIncludeUsage       bool
		wantIncludeUsageValue  bool
		wantIncludeObfuscation bool
		wantObfuscationValue   bool
	}{
		{
			name: "absent options stay absent",
			raw:  `{"model":"gpt-4.1"}`,
		},
		{
			name:              "explicit usage false is preserved",
			raw:               `{"model":"gpt-4.1","stream_options":{"include_usage":false}}`,
			wantStreamOptions: true,
			wantIncludeUsage:  true,
		},
		{
			name:                  "explicit usage true is preserved",
			raw:                   `{"model":"gpt-4.1","stream_options":{"include_usage":true}}`,
			wantStreamOptions:     true,
			wantIncludeUsage:      true,
			wantIncludeUsageValue: true,
		},
		{
			name:                   "explicit obfuscation false is preserved",
			raw:                    `{"model":"gpt-4.1","stream_options":{"include_obfuscation":false}}`,
			wantStreamOptions:      true,
			wantIncludeObfuscation: true,
		},
		{
			name:                   "explicit obfuscation true is preserved",
			raw:                    `{"model":"gpt-4.1","stream_options":{"include_obfuscation":true}}`,
			wantStreamOptions:      true,
			wantIncludeObfuscation: true,
			wantObfuscationValue:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var request GeneralOpenAIRequest
			require.NoError(t, kitutil.Unmarshal([]byte(test.raw), &request))

			encoded, err := kitutil.Marshal(request)
			require.NoError(t, err)

			streamOptions := gjson.GetBytes(encoded, "stream_options")
			require.Equal(t, test.wantStreamOptions, streamOptions.Exists())
			require.Equal(
				t,
				test.wantIncludeUsage,
				gjson.GetBytes(encoded, "stream_options.include_usage").Exists(),
			)
			if test.wantIncludeUsage {
				require.Equal(
					t,
					test.wantIncludeUsageValue,
					gjson.GetBytes(encoded, "stream_options.include_usage").Bool(),
				)
			}
			require.Equal(
				t,
				test.wantIncludeObfuscation,
				gjson.GetBytes(encoded, "stream_options.include_obfuscation").Exists(),
			)
			if test.wantIncludeObfuscation {
				require.Equal(
					t,
					test.wantObfuscationValue,
					gjson.GetBytes(encoded, "stream_options.include_obfuscation").Bool(),
				)
			}
		})
	}
}

func TestGeneralOpenAIRequestGetSystemRoleName(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "o1 uses developer", model: "o1", want: "developer"},
		{name: "o3 family uses developer", model: "o3-mini-high", want: "developer"},
		{name: "o4 family uses developer", model: "o4-mini", want: "developer"},
		{name: "o1 mini stays system", model: "o1-mini", want: "system"},
		{name: "o1 preview stays system", model: "o1-preview", want: "system"},
		{name: "gpt 5 uses developer", model: "gpt-5", want: "developer"},
		{name: "omni is not o series", model: "omni-moderation-latest", want: "system"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := GeneralOpenAIRequest{Model: tt.model}

			require.Equal(t, tt.want, req.GetSystemRoleName())
		})
	}
}
