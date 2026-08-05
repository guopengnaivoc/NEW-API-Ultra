package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGeminiVideoURLTestChannel(baseURL string, apiKey string) *model.Channel {
	return &model.Channel{
		Type:    constant.ChannelTypeGemini,
		Key:     apiKey,
		BaseURL: &baseURL,
	}
}

func newGeminiVideoURLTestTask(taskID string) *model.Task {
	return &model.Task{
		TaskID: taskID,
		Action: "generate",
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: taskcommon.EncodeLocalTaskID(
				"models/veo-3.1/operations/" + taskID,
			),
		},
	}
}

func requireGeminiVideoURLUnavailable(
	t *testing.T,
	got string,
	err error,
	forbidden ...string,
) {
	t.Helper()
	assert.Empty(t, got)
	require.EqualError(t, err, "Gemini video URL is unavailable")
	for _, value := range forbidden {
		assert.NotContains(t, err.Error(), value)
	}
}

func TestGetGeminiVideoURLPrefersEncryptedPrivateResult(t *testing.T) {
	type requestObservation struct {
		header     string
		requestURI string
	}
	privateRequests := make(chan requestObservation, 1)
	privateTarget := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			privateRequests <- requestObservation{
				header:     r.Header.Get(geminiVideoAPIKeyHeader),
				requestURI: r.RequestURI,
			}
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = io.WriteString(w, "private-result")
		},
	))
	t.Cleanup(privateTarget.Close)

	var legacyRequests atomic.Int32
	legacyTarget := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			legacyRequests.Add(1)
			_, _ = io.WriteString(w, "legacy-result")
		},
	))
	t.Cleanup(legacyTarget.Close)

	privateURI := privateTarget.URL + "/content?key=" +
		url.QueryEscape(geminiVideoProxyTestAPIKey) +
		"&key=unrelated-key&sig=A%2Bb"
	fixture := setupGeminiVideoProxyFixture(t, privateURI)

	var task model.Task
	require.NoError(t, model.DB.Where("task_id = ?", fixture.taskID).First(&task).Error)
	task.SetData(map[string]any{
		"uri": legacyTarget.URL + "/legacy?legacy-query-sentinel=1",
	})
	require.NoError(t, model.DB.Model(&task).UpdateColumn("data", task.Data).Error)

	response := runGeminiVideoProxy(t, fixture)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "private-result", response.Body.String())
	assert.Zero(t, legacyRequests.Load())
	select {
	case observation := <-privateRequests:
		assert.Equal(t, geminiVideoProxyTestAPIKey, observation.header)
		assert.Equal(
			t,
			"/content?key=unrelated-key&sig=A%2Bb",
			observation.requestURI,
		)
		assert.NotContains(
			t,
			observation.requestURI,
			geminiVideoProxyTestAPIKey,
		)
	default:
		t.Fatal("encrypted private target was not requested")
	}
}

func TestGetGeminiVideoURLRejectsEncryptedSelfProxy(t *testing.T) {
	const apiKey = "self-proxy-credential"

	t.Run("encrypted", func(t *testing.T) {
		var pollRequests atomic.Int32
		pollServer := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				pollRequests.Add(1)
				_, _ = io.WriteString(
					w,
					`{"done":true,"uri":"https://video.example/content"}`,
				)
			},
		))
		t.Cleanup(pollServer.Close)

		task := newGeminiVideoURLTestTask("task/encrypted self proxy")
		selfProxyURI := pollServer.URL + geminitaskresult.ProxyPath(task.TaskID)
		_, err := task.SetProviderResultURI(selfProxyURI)
		require.NoError(t, err)

		got, err := getGeminiVideoURL(
			newGeminiVideoURLTestChannel(pollServer.URL, apiKey),
			task,
			apiKey,
		)

		requireGeminiVideoURLUnavailable(t, got, err, selfProxyURI)
		assert.Zero(t, pollRequests.Load())
	})

	t.Run("legacy", func(t *testing.T) {
		var pollRequests atomic.Int32
		pollServer := httptest.NewServer(http.HandlerFunc(
			func(http.ResponseWriter, *http.Request) {
				pollRequests.Add(1)
			},
		))
		t.Cleanup(pollServer.Close)

		task := newGeminiVideoURLTestTask("task/legacy self proxy")
		selfProxyURI := "https://local.example" +
			geminitaskresult.ProxyPath(task.TaskID)
		task.SetData(map[string]any{"uri": selfProxyURI})

		got, err := getGeminiVideoURL(
			newGeminiVideoURLTestChannel(pollServer.URL, apiKey),
			task,
			apiKey,
		)

		requireGeminiVideoURLUnavailable(t, got, err, selfProxyURI)
		assert.Zero(t, pollRequests.Load())
	})

	t.Run("provider re-poll", func(t *testing.T) {
		var pollRequests atomic.Int32
		task := newGeminiVideoURLTestTask("task/re-poll self proxy")
		selfProxyURI := "https://local.example" +
			geminitaskresult.ProxyPath(task.TaskID)
		pollServer := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				pollRequests.Add(1)
				_, _ = io.WriteString(
					w,
					`{"done":true,"response":{"generateVideoResponse":{`+
						`"generatedVideos":[{"video":{"uri":"`+
						selfProxyURI+`"}}]}}}`,
				)
			},
		))
		t.Cleanup(pollServer.Close)

		got, err := getGeminiVideoURL(
			newGeminiVideoURLTestChannel(pollServer.URL, apiKey),
			task,
			apiKey,
		)

		requireGeminiVideoURLUnavailable(t, got, err, selfProxyURI)
		assert.EqualValues(t, 1, pollRequests.Load())
	})

	t.Run("similar path is not exact self proxy", func(t *testing.T) {
		task := newGeminiVideoURLTestTask("task-similar-self-proxy-path")
		similarURI := "https://video.example/prefix" +
			geminitaskresult.ProxyPath(task.TaskID) + "/suffix"
		_, err := task.SetProviderResultURI(similarURI)
		require.NoError(t, err)

		got, err := getGeminiVideoURL(
			newGeminiVideoURLTestChannel("https://poll.invalid", apiKey),
			task,
			apiKey,
		)

		require.NoError(t, err)
		assert.Equal(t, similarURI, got)
	})
}

func TestGetGeminiVideoURLRejectsCorruptEnvelopeWithoutFallback(t *testing.T) {
	const (
		apiKey         = "corrupt-envelope-credential"
		legacySentinel = "legacy-fallback-uri-sentinel"
	)
	legacyURI := "https://video.example/" + legacySentinel

	wrongDomainEnvelope, err := common.SealDataEncryptionValueRequired(
		"tasks:wrong_provider_result_domain",
		"https://video.example/wrong-domain-plaintext-sentinel",
	)
	require.NoError(t, err)

	tests := []struct {
		name   string
		stored string
	}{
		{
			name:   "non-envelope",
			stored: "plaintext-provider-result-envelope-sentinel",
		},
		{
			name: "corrupt envelope",
			stored: "naenc:v1:test:corrupt-wrapped-key-sentinel:" +
				"corrupt-payload-sentinel",
		},
		{
			name:   "wrong domain",
			stored: wrongDomainEnvelope,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var pollRequests atomic.Int32
			pollServer := httptest.NewServer(http.HandlerFunc(
				func(http.ResponseWriter, *http.Request) {
					pollRequests.Add(1)
				},
			))
			t.Cleanup(pollServer.Close)
			task := newGeminiVideoURLTestTask("task-corrupt-envelope")
			task.EncryptedProviderResultURI = &test.stored
			task.SetData(map[string]any{"uri": legacyURI})

			got, err := getGeminiVideoURL(
				newGeminiVideoURLTestChannel(pollServer.URL, apiKey),
				task,
				apiKey,
			)

			requireGeminiVideoURLUnavailable(
				t,
				got,
				err,
				test.stored,
				legacyURI,
				legacySentinel,
				apiKey,
				"wrong-domain-plaintext-sentinel",
			)
			assert.Zero(t, pollRequests.Load())
		})
	}
}

func TestGetGeminiVideoURLSanitizesLegacyGeneratedSamples(t *testing.T) {
	const apiKey = "legacy-generated-samples-credential"
	task := newGeminiVideoURLTestTask("task-legacy-generated-samples")
	task.SetData(map[string]any{
		"done": true,
		"response": map[string]any{
			"generateVideoResponse": map[string]any{
				"generatedSamples": []any{map[string]any{
					"video": map[string]any{
						"uri": "https://video.example/content?key=" +
							url.QueryEscape(apiKey) +
							"&sig=A%2Bb&key=unrelated",
					},
				}},
			},
		},
	})

	got, err := getGeminiVideoURL(
		newGeminiVideoURLTestChannel("https://poll.invalid", apiKey),
		task,
		apiKey,
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		"https://video.example/content?sig=A%2Bb&key=unrelated",
		got,
	)
	assert.NotContains(t, got, apiKey)
}

func TestGetGeminiVideoURLRepollSanitizesGeneratedVideos(t *testing.T) {
	const apiKey = "repoll-generated-videos-credential"

	t.Run("empty legacy data", func(t *testing.T) {
		var receivedHeader string
		pollServer := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				receivedHeader = r.Header.Get(geminiVideoAPIKeyHeader)
				_, _ = io.WriteString(
					w,
					`{"done":true,"response":{"generateVideoResponse":{`+
						`"generatedVideos":[{"video":{"uri":`+
						`"https://video.example/content?key=`+
						apiKey+`&sig=A%2Bb"}}]}}}`,
				)
			},
		))
		t.Cleanup(pollServer.Close)
		task := newGeminiVideoURLTestTask("task-repoll-generated-videos")

		got, err := getGeminiVideoURL(
			newGeminiVideoURLTestChannel(pollServer.URL, apiKey),
			task,
			apiKey,
		)

		require.NoError(t, err)
		assert.Equal(t, apiKey, receivedHeader)
		assert.Equal(t, "https://video.example/content?sig=A%2Bb", got)
		assert.NotContains(t, got, apiKey)
	})

	t.Run("canonical public legacy projection", func(t *testing.T) {
		var pollRequests atomic.Int32
		pollServer := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				pollRequests.Add(1)
				assert.Equal(t, apiKey, r.Header.Get(geminiVideoAPIKeyHeader))
				_, _ = io.WriteString(
					w,
					`{"done":true,"uri":`+
						`"https://video.example/canonical-poll-content"}`,
				)
			},
		))
		t.Cleanup(pollServer.Close)
		task := newGeminiVideoURLTestTask("task-canonical-public-legacy")
		task.SetData(map[string]any{
			"done": false,
			"video": map[string]any{
				"url": geminitaskresult.ProxyPath(task.TaskID),
			},
		})

		got, err := getGeminiVideoURL(
			newGeminiVideoURLTestChannel(pollServer.URL, apiKey),
			task,
			apiKey,
		)

		require.NoError(t, err)
		assert.Equal(
			t,
			"https://video.example/canonical-poll-content",
			got,
		)
		assert.EqualValues(t, 1, pollRequests.Load())
	})
}

func TestGetGeminiVideoURLNeverFallsBackToRawBody(t *testing.T) {
	const (
		apiKey      = "raw-fallback-credential"
		rawSentinel = "raw-fallback-uri-sentinel"
	)
	typeDriftBody := `{"done":true,"response":{"videos":[` +
		`{"uri":17},{"uri":"https://video.example/` + rawSentinel + `"` +
		`}]}}`

	t.Run("legacy type drift", func(t *testing.T) {
		var pollRequests atomic.Int32
		pollServer := httptest.NewServer(http.HandlerFunc(
			func(http.ResponseWriter, *http.Request) {
				pollRequests.Add(1)
			},
		))
		t.Cleanup(pollServer.Close)
		task := newGeminiVideoURLTestTask("task-legacy-type-drift")
		task.Data = []byte(typeDriftBody)

		got, err := getGeminiVideoURL(
			newGeminiVideoURLTestChannel(pollServer.URL, apiKey),
			task,
			apiKey,
		)

		requireGeminiVideoURLUnavailable(
			t,
			got,
			err,
			rawSentinel,
			typeDriftBody,
			apiKey,
		)
		assert.Zero(t, pollRequests.Load())
	})

	t.Run("provider re-poll type drift", func(t *testing.T) {
		var pollRequests atomic.Int32
		pollServer := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				pollRequests.Add(1)
				_, _ = io.WriteString(w, typeDriftBody)
			},
		))
		t.Cleanup(pollServer.Close)
		task := newGeminiVideoURLTestTask("task-repoll-type-drift")

		got, err := getGeminiVideoURL(
			newGeminiVideoURLTestChannel(pollServer.URL, apiKey),
			task,
			apiKey,
		)

		requireGeminiVideoURLUnavailable(
			t,
			got,
			err,
			rawSentinel,
			typeDriftBody,
			apiKey,
		)
		assert.EqualValues(t, 1, pollRequests.Load())
	})
}

func TestGetGeminiVideoURLRequiresExactResolvedCredential(t *testing.T) {
	const envelopeSentinel = "credential-resolution-envelope-sentinel"
	stored := envelopeSentinel
	task := newGeminiVideoURLTestTask("task-missing-resolved-credential")
	task.EncryptedProviderResultURI = &stored

	got, err := getGeminiVideoURL(
		newGeminiVideoURLTestChannel("https://poll.invalid", ""),
		task,
		"",
	)

	requireGeminiVideoURLUnavailable(t, got, err, envelopeSentinel)
}

func TestGetGeminiVideoURLPreservesUndecodableUnrelatedQueryBytes(t *testing.T) {
	const apiKey = "query-preservation-credential"
	task := newGeminiVideoURLTestTask("task-query-preservation")
	privateURI := "https://video.example/content?broken=%zz&key=" +
		url.QueryEscape(apiKey) + "&sig=A%2Bb#fragment"
	_, err := task.SetProviderResultURI(privateURI)
	require.NoError(t, err)

	got, err := getGeminiVideoURL(
		newGeminiVideoURLTestChannel("https://poll.invalid", apiKey),
		task,
		apiKey,
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		"https://video.example/content?broken=%zz&sig=A%2Bb#fragment",
		got,
	)
	assert.NotContains(t, got, apiKey)
	assert.True(t, strings.Contains(got, "broken=%zz"))
}
