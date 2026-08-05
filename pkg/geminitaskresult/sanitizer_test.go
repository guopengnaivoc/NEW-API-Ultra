package geminitaskresult

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStripExactCredentialQueryPreservesSignedBytes(t *testing.T) {
	const credential = "credential/sentinel+value"

	tests := []struct {
		name    string
		rawURI  string
		want    string
		wantErr bool
	}{
		{
			name:   "no query",
			rawURI: "https://video.example.test/path/video.mp4#fragment",
			want:   "https://video.example.test/path/video.mp4#fragment",
		},
		{
			name:   "first field",
			rawURI: "https://video.example.test/v?key=credential%2Fsentinel%2Bvalue&sig=A%2Bb",
			want:   "https://video.example.test/v?sig=A%2Bb",
		},
		{
			name:   "middle field",
			rawURI: "https://video.example.test/v?a=1&key=credential%2Fsentinel%2Bvalue&b=2",
			want:   "https://video.example.test/v?a=1&b=2",
		},
		{
			name:   "last field",
			rawURI: "https://video.example.test/v?a=1&key=credential%2Fsentinel%2Bvalue",
			want:   "https://video.example.test/v?a=1",
		},
		{
			name:   "duplicate exact fields",
			rawURI: "https://video.example.test/v?key=credential%2Fsentinel%2Bvalue&a=1&key=credential%2Fsentinel%2Bvalue",
			want:   "https://video.example.test/v?a=1",
		},
		{
			name:   "encoded field name",
			rawURI: "https://video.example.test/v?%6b%65%79=credential%2Fsentinel%2Bvalue&sig=A%2Bb",
			want:   "https://video.example.test/v?sig=A%2Bb",
		},
		{
			name:   "unrelated key value",
			rawURI: "https://video.example.test/v?key=another%2Fcredential&sig=A%2Bb",
			want:   "https://video.example.test/v?key=another%2Fcredential&sig=A%2Bb",
		},
		{
			name:   "non key field",
			rawURI: "https://video.example.test/v?monkey=credential%2Fsentinel%2Bvalue&sig=A%2Bb",
			want:   "https://video.example.test/v?monkey=credential%2Fsentinel%2Bvalue&sig=A%2Bb",
		},
		{
			name:   "signed bytes duplicates and fragment",
			rawURI: "https://video.example.test/v?sig=A%2Bb&token=signed%2Fvalue&token=signed%2Fvalue#part%2Ftwo",
			want:   "https://video.example.test/v?sig=A%2Bb&token=signed%2Fvalue&token=signed%2Fvalue#part%2Ftwo",
		},
		{
			name:   "undecodable segment remains",
			rawURI: "https://video.example.test/v?broken=%zz&key=credential%2Fsentinel%2Bvalue&sig=A%2Bb",
			want:   "https://video.example.test/v?broken=%zz&sig=A%2Bb",
		},
		{
			name:    "malformed URI",
			rawURI:  "://provider-uri-sentinel?key=credential%2Fsentinel%2Bvalue",
			wantErr: true,
		},
		{
			name:    "relative URI",
			rawURI:  "/provider-uri-sentinel?key=credential%2Fsentinel%2Bvalue",
			wantErr: true,
		},
		{
			name:    "data URI",
			rawURI:  "data:video/mp4;base64,provider-uri-sentinel",
			wantErr: true,
		},
		{
			name:    "over limit",
			rawURI:  "https://video.example.test/" + strings.Repeat("provider-uri-sentinel", MaxProviderResultURIBytes),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := StripExactCredentialQuery(test.rawURI, credential)
			if test.wantErr {
				require.Error(t, err)
				assert.Empty(t, got)
				assert.NotContains(t, err.Error(), "provider-uri-sentinel")
				assert.NotContains(t, err.Error(), credential)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}

	t.Run("empty credential", func(t *testing.T) {
		const rawURI = "https://video.example.test/provider-uri-sentinel?key=query-sentinel"

		got, err := StripExactCredentialQuery(rawURI, "")

		require.Error(t, err)
		assert.Empty(t, got)
		assert.NotContains(t, err.Error(), "provider-uri-sentinel")
		assert.NotContains(t, err.Error(), "query-sentinel")
	})
}

func TestProxyPathEscapesPublicTaskID(t *testing.T) {
	assert.Equal(t, "/v1/videos/task_alpha/content", ProxyPath("task_alpha"))
	assert.Equal(
		t,
		"/v1/videos/task%2Falpha%3F%20beta/content",
		ProxyPath("task/alpha? beta"),
	)
}

func TestEmptyPublicProjectionIsCanonical(t *testing.T) {
	assert.JSONEq(t, `{"done":false}`, string(EmptyPublicProjection(false)))
	assert.Equal(t, `{"done":false}`, string(EmptyPublicProjection(false)))
	assert.JSONEq(t, `{"done":true}`, string(EmptyPublicProjection(true)))
	assert.Equal(t, `{"done":true}`, string(EmptyPublicProjection(true)))
}

func TestSanitizeGeminiTaskResultShapes(t *testing.T) {
	const (
		credential    = "credential-secret-sentinel"
		operationName = "operation-name-secret-sentinel"
		rawURI        = "https://video.example.test/private-path-sentinel.mp4?sig=signed%2Fquery-sentinel&key=" + credential + "#fragment"
		filteredURI   = "https://video.example.test/private-path-sentinel.mp4?sig=signed%2Fquery-sentinel#fragment"
	)

	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "generated samples",
			raw: fmt.Sprintf(
				`{"name":%q,"done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":%q,"mimeType":"video/mp4"}}]}}}`,
				operationName,
				rawURI,
			),
		},
		{
			name: "generated videos",
			raw: fmt.Sprintf(
				`{"name":%q,"done":true,"response":{"generateVideoResponse":{"generatedVideos":[{"video":{"uri":%q,"mimeType":"video/mp4"}}]}}}`,
				operationName,
				rawURI,
			),
		},
		{
			name: "legacy response videos",
			raw: fmt.Sprintf(
				`{"name":%q,"done":true,"response":{"videos":[{"uri":%q,"mimeType":"video/mp4"}]}}`,
				operationName,
				rawURI,
			),
		},
		{
			name: "legacy response video",
			raw: fmt.Sprintf(
				`{"name":%q,"done":true,"response":{"video":%q,"mimeType":"video/mp4"}}`,
				operationName,
				rawURI,
			),
		},
		{
			name: "legacy response URI",
			raw: fmt.Sprintf(
				`{"name":%q,"done":true,"response":{"uri":%q,"mimeType":"video/mp4"}}`,
				operationName,
				rawURI,
			),
		},
		{
			name: "legacy top level URI",
			raw: fmt.Sprintf(
				`{"name":%q,"done":true,"uri":%q,"mimeType":"video/mp4"}`,
				operationName,
				rawURI,
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Sanitize([]byte(test.raw), Options{
				Phase:              PhasePoll,
				PublicTaskID:       "task_public",
				ResolvedCredential: credential,
				CapturePrivateURI:  true,
			})

			require.NoError(t, err)
			assert.True(t, result.Done)
			assert.Equal(t, "SUCCESS", result.Status)
			assert.Equal(t, "100%", result.Progress)
			assert.True(t, result.HadProviderURI)
			assert.Equal(t, filteredURI, result.ProviderURI)
			assert.Equal(t, "video/mp4", result.VideoMIMEType)
			assert.JSONEq(
				t,
				`{"done":true,"video":{"url":"/v1/videos/task_public/content","mime_type":"video/mp4"}}`,
				string(result.PublicData),
			)
			for _, forbidden := range []string{
				credential,
				operationName,
				"private-path-sentinel",
				"query-sentinel",
				"video.example.test",
			} {
				assert.NotContains(t, string(result.PublicData), forbidden)
			}
		})
	}
}

func TestSanitizeGeminiTaskResultStateAndErrorPolicy(t *testing.T) {
	const (
		credential       = "credential-secret-sentinel"
		operationName    = "operation-name-secret-sentinel"
		providerMessage  = "provider-error-message-secret-sentinel"
		providerVideoURI = "https://video.example.test/private-path-sentinel?key=" + credential
	)

	tests := []struct {
		name            string
		phase           Phase
		raw             string
		wantPublic      string
		wantStatus      string
		wantProgress    string
		wantDone        bool
		wantFailed      bool
		wantRetryable   bool
		wantErrorCode   int
		wantErrorStatus string
		wantMIME        string
		wantOperation   string
		capturePrivate  bool
		resolvedKey     string
		wantProviderURI string
		wantHadProvider bool
	}{
		{
			name:          "submit operation",
			phase:         PhaseSubmit,
			raw:           fmt.Sprintf(`{"name":%q}`, operationName),
			wantPublic:    `{"done":false}`,
			wantStatus:    "IN_PROGRESS",
			wantProgress:  "50%",
			wantOperation: operationName,
		},
		{
			name:         "in progress",
			phase:        PhasePoll,
			raw:          `{"done":false}`,
			wantPublic:   `{"done":false}`,
			wantStatus:   "IN_PROGRESS",
			wantProgress: "50%",
		},
		{
			name:         "success without video",
			phase:        PhasePoll,
			raw:          `{"done":true}`,
			wantPublic:   `{"done":true}`,
			wantStatus:   "SUCCESS",
			wantProgress: "100%",
			wantDone:     true,
		},
		{
			name:  "provider failure",
			phase: PhasePoll,
			raw: fmt.Sprintf(
				`{"done":true,"error":{"code":13,"status":"INTERNAL","message":%q}}`,
				providerMessage,
			),
			wantPublic:      `{"done":true,"error":{"code":13,"status":"INTERNAL"}}`,
			wantStatus:      "FAILURE",
			wantProgress:    "100%",
			wantDone:        true,
			wantFailed:      true,
			wantErrorCode:   13,
			wantErrorStatus: "INTERNAL",
		},
		{
			name:  "retryable provider failure",
			phase: PhasePoll,
			raw: fmt.Sprintf(
				`{"done":true,"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":%q}}`,
				providerMessage,
			),
			wantPublic:      `{"done":true,"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`,
			wantStatus:      "FAILURE",
			wantProgress:    "100%",
			wantDone:        true,
			wantFailed:      true,
			wantRetryable:   true,
			wantErrorCode:   429,
			wantErrorStatus: "RESOURCE_EXHAUSTED",
		},
		{
			name:  "invalid optional values omitted",
			phase: PhasePoll,
			raw: fmt.Sprintf(
				`{"done":true,"response":{"generateVideoResponse":{"generatedVideos":[{"video":{"uri":%q,"mimeType":"text/plain"}}]}},"error":{"code":13,"status":"invalid status","message":%q}}`,
				providerVideoURI,
				providerMessage,
			),
			wantPublic:      `{"done":true,"video":{"url":"/v1/videos/task_public/content"},"error":{"code":13}}`,
			wantStatus:      "FAILURE",
			wantProgress:    "100%",
			wantDone:        true,
			wantFailed:      true,
			wantErrorCode:   13,
			capturePrivate:  true,
			resolvedKey:     credential,
			wantProviderURI: "https://video.example.test/private-path-sentinel",
			wantHadProvider: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Sanitize([]byte(test.raw), Options{
				Phase:              test.phase,
				PublicTaskID:       "task_public",
				ResolvedCredential: test.resolvedKey,
				CapturePrivateURI:  test.capturePrivate,
			})

			require.NoError(t, err)
			assert.JSONEq(t, test.wantPublic, string(result.PublicData))
			assert.Equal(t, test.wantStatus, result.Status)
			assert.Equal(t, test.wantProgress, result.Progress)
			assert.Equal(t, test.wantDone, result.Done)
			assert.Equal(t, test.wantFailed, result.Failed)
			assert.Equal(t, test.wantRetryable, result.Retryable)
			assert.Equal(t, test.wantErrorCode, result.ErrorCode)
			assert.Equal(t, test.wantErrorStatus, result.ErrorStatus)
			assert.Equal(t, test.wantMIME, result.VideoMIMEType)
			assert.Equal(t, test.wantOperation, result.OperationName)
			assert.Equal(t, test.wantProviderURI, result.ProviderURI)
			assert.Equal(t, test.wantHadProvider, result.HadProviderURI)
			assert.NotContains(t, string(result.PublicData), operationName)
			assert.NotContains(t, string(result.PublicData), providerMessage)
			assert.NotContains(t, string(result.PublicData), credential)
			assert.NotContains(t, string(result.PublicData), "private-path-sentinel")
		})
	}
}

func TestSanitizeGeminiTaskResultDropsAdditionalVideos(t *testing.T) {
	const credential = "credential-secret-sentinel"
	raw := fmt.Sprintf(
		`{"done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":"https://video.example.test/first?key=%s"}},{"video":{"uri":"https://video.example.test/second?key=%s"}}],"generatedVideos":[{"video":{"uri":"https://video.example.test/third?key=%s"}}]}}}`,
		credential,
		credential,
		credential,
	)

	result, err := Sanitize([]byte(raw), Options{
		Phase:              PhasePoll,
		PublicTaskID:       "task_public",
		ResolvedCredential: credential,
		CapturePrivateURI:  true,
	})

	require.NoError(t, err)
	assert.Equal(t, "https://video.example.test/first", result.ProviderURI)
	assert.True(t, result.ExtraVideoResults)
	assert.NotContains(t, string(result.PublicData), "first")
	assert.NotContains(t, string(result.PublicData), "second")
	assert.NotContains(t, string(result.PublicData), "third")
}

func TestSanitizeGeminiTaskResultPublicReadNeverReturnsPrivateURI(t *testing.T) {
	const rawURI = "https://video.example.test/private-path-sentinel?sig=query-sentinel&key=credential-secret-sentinel"
	raw := fmt.Sprintf(
		`{"done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":%q}}]}}}`,
		rawURI,
	)

	result, err := Sanitize([]byte(raw), Options{
		Phase:             PhasePublicRead,
		PublicTaskID:      "task_public",
		CapturePrivateURI: false,
	})

	require.NoError(t, err)
	assert.True(t, result.HadProviderURI)
	assert.Empty(t, result.ProviderURI)
	assert.JSONEq(
		t,
		`{"done":true,"video":{"url":"/v1/videos/task_public/content"}}`,
		string(result.PublicData),
	)
	assert.NotContains(t, string(result.PublicData), "private-path-sentinel")
	assert.NotContains(t, string(result.PublicData), "query-sentinel")
	assert.NotContains(t, string(result.PublicData), "credential-secret-sentinel")
}

func TestSanitizeGeminiTaskResultRejectsUnsafeInputsWithoutRawFallback(t *testing.T) {
	const (
		credential = "credential-secret-sentinel"
		sentinel   = "raw-provider-secret-sentinel"
	)

	tests := []struct {
		name       string
		raw        string
		credential string
		capture    bool
	}{
		{
			name:       "malformed JSON",
			raw:        `{"done":true,"uri":"raw-provider-secret-sentinel"`,
			credential: credential,
			capture:    true,
		},
		{
			name:       "unknown shape",
			raw:        `{"unknown":"raw-provider-secret-sentinel"}`,
			credential: credential,
			capture:    true,
		},
		{
			name:       "unsafe URI",
			raw:        `{"done":true,"uri":"data:video/mp4;base64,raw-provider-secret-sentinel"}`,
			credential: credential,
			capture:    true,
		},
		{
			name: "oversized URI",
			raw: fmt.Sprintf(
				`{"done":true,"uri":"https://video.example.test/%s"}`,
				strings.Repeat(sentinel, MaxProviderResultURIBytes),
			),
			credential: credential,
			capture:    true,
		},
		{
			name:    "missing capture credential",
			raw:     `{"done":true,"uri":"https://video.example.test/raw-provider-secret-sentinel?key=credential-secret-sentinel"}`,
			capture: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Sanitize([]byte(test.raw), Options{
				Phase:              PhasePoll,
				PublicTaskID:       "task_public",
				ResolvedCredential: test.credential,
				CapturePrivateURI:  test.capture,
			})

			require.Error(t, err)
			assert.JSONEq(t, `{"done":false}`, string(result.PublicData))
			assert.Empty(t, result.ProviderURI)
			for _, forbidden := range []string{sentinel, credential, "video.example.test"} {
				assert.NotContains(t, string(result.PublicData), forbidden)
				assert.NotContains(t, err.Error(), forbidden)
			}
		})
	}

	t.Run("base64 and unknown fields are dropped", func(t *testing.T) {
		raw := `{"done":true,"response":{"bytesBase64Encoded":"raw-provider-secret-sentinel","metadata":{"token":"credential-secret-sentinel"}}}`

		result, err := Sanitize([]byte(raw), Options{
			Phase:        PhasePoll,
			PublicTaskID: "task_public",
		})

		require.NoError(t, err)
		assert.JSONEq(t, `{"done":true}`, string(result.PublicData))
		assert.NotContains(t, string(result.PublicData), sentinel)
		assert.NotContains(t, string(result.PublicData), credential)
	})
}

func TestSanitizeGeminiCanonicalProjectionIsIdempotent(t *testing.T) {
	canonical := []byte(
		`{"done":true,"video":{"url":"/v1/videos/task_public/content","mime_type":"video/mp4"}}`,
	)

	result, err := Sanitize(canonical, Options{
		Phase:             PhasePublicRead,
		PublicTaskID:      "task_public",
		CapturePrivateURI: false,
	})

	require.NoError(t, err)
	assert.Equal(t, canonical, result.PublicData)
	assert.Empty(t, result.ProviderURI)
	assert.False(t, result.HadProviderURI)
	assert.Equal(t, "SUCCESS", result.Status)
}

// TestSanitizeGeminiRejectsCanonicalVideoWithForeignURL pins the canonical
// public-projection guard: a top-level video object is only accepted when its
// url is exactly this task's proxy path. An upstream-supplied absolute URL must
// be rejected rather than echoed into the public projection, otherwise a
// provider (or attacker-influenced) URL could be surfaced to end users.
func TestSanitizeGeminiRejectsCanonicalVideoWithForeignURL(t *testing.T) {
	const foreignURL = "https://evil.example.test/leak-sentinel.mp4"
	raw := fmt.Sprintf(
		`{"done":true,"video":{"url":%q,"mime_type":"video/mp4"}}`,
		foreignURL,
	)

	result, err := Sanitize([]byte(raw), Options{
		Phase:              PhasePoll,
		PublicTaskID:       "task_public",
		ResolvedCredential: "credential-secret-sentinel",
		CapturePrivateURI:  true,
	})

	require.Error(t, err)
	assert.JSONEq(t, `{"done":false}`, string(result.PublicData))
	assert.Empty(t, result.ProviderURI)
	for _, forbidden := range []string{foreignURL, "evil.example.test", "leak-sentinel"} {
		assert.NotContains(t, string(result.PublicData), forbidden)
		assert.NotContains(t, err.Error(), forbidden)
	}
}

// TestSanitizeGeminiRejectsCanonicalVideoWithProxyPathOfOtherTask ensures the
// proxy-path comparison is task-scoped: a canonical projection minted for a
// different task's proxy path must not be accepted for this task.
func TestSanitizeGeminiRejectsCanonicalVideoWithProxyPathOfOtherTask(t *testing.T) {
	raw := fmt.Sprintf(
		`{"done":true,"video":{"url":%q}}`,
		ProxyPath("some_other_task"),
	)

	result, err := Sanitize([]byte(raw), Options{
		Phase:              PhasePoll,
		PublicTaskID:       "task_public",
		ResolvedCredential: "credential-secret-sentinel",
		CapturePrivateURI:  true,
	})

	require.Error(t, err)
	assert.JSONEq(t, `{"done":false}`, string(result.PublicData))
	assert.NotContains(t, string(result.PublicData), "some_other_task")
}

// TestSanitizeGeminiRejectsNonHTTPProviderURIScheme pins the provider-URI
// scheme allowlist: only absolute http(s) URIs may be captured. Other schemes
// (file, ftp, gs, javascript, ...) must be rejected at the sanitizer boundary
// so they can never become a captured ProviderURI or reach a fetch/redirect.
func TestSanitizeGeminiRejectsNonHTTPProviderURIScheme(t *testing.T) {
	schemes := []string{
		"ftp://video.example.test/leak-sentinel.mp4",
		"file:///etc/leak-sentinel",
		"gs://bucket/leak-sentinel.mp4",
		"javascript:alert('leak-sentinel')",
	}

	for _, uri := range schemes {
		t.Run(uri, func(t *testing.T) {
			raw := fmt.Sprintf(`{"done":true,"uri":%q}`, uri)

			result, err := Sanitize([]byte(raw), Options{
				Phase:              PhasePoll,
				PublicTaskID:       "task_public",
				ResolvedCredential: "credential-secret-sentinel",
				CapturePrivateURI:  true,
			})

			require.Error(t, err)
			assert.Empty(t, result.ProviderURI)
			assert.JSONEq(t, `{"done":false}`, string(result.PublicData))
			assert.NotContains(t, string(result.PublicData), "leak-sentinel")
		})
	}
}

// TestParseProviderResultURIRejectsNonHTTPScheme locks the same scheme
// allowlist directly at StripExactCredentialQuery, which parses the stored
// private URI before the controller re-validates it.
func TestParseProviderResultURIRejectsNonHTTPScheme(t *testing.T) {
	for _, uri := range []string{
		"ftp://video.example.test/leak-sentinel.mp4?key=credential-secret-sentinel",
		"file:///etc/leak-sentinel?key=credential-secret-sentinel",
	} {
		t.Run(uri, func(t *testing.T) {
			filtered, err := StripExactCredentialQuery(uri, "credential-secret-sentinel")
			require.Error(t, err)
			assert.Empty(t, filtered)
			assert.NotContains(t, err.Error(), "leak-sentinel")
			assert.NotContains(t, err.Error(), "credential-secret-sentinel")
		})
	}
}
