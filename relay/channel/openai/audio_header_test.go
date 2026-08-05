package openai

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenaiTTSHandlerFiltersUpstreamHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":        {"audio/mpeg"},
			"Content-Length":      {"999"},
			"Content-Disposition": {`inline; filename="speech.mp3"`},
			"ETag":                {`"speech-etag"`},
			"Last-Modified":       {"Wed, 21 Oct 2015 07:28:00 GMT"},
			"Set-Cookie":          {"session=attacker"},
			"X-Secret":            {"audio-secret-sentinel"},
		},
		Body: io.NopCloser(strings.NewReader("audio")),
	}
	info := &relaycommon.RelayInfo{
		Request: &dto.AudioRequest{ResponseFormat: "pcm"},
	}

	OpenaiTTSHandler(c, resp, info)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "audio", recorder.Body.String())
	assert.Equal(t, "audio/mpeg", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "5", recorder.Header().Get("Content-Length"))
	assert.Equal(t, `inline; filename="speech.mp3"`, recorder.Header().Get("Content-Disposition"))
	assert.Equal(t, `"speech-etag"`, recorder.Header().Get("ETag"))
	assert.Equal(t, "Wed, 21 Oct 2015 07:28:00 GMT", recorder.Header().Get("Last-Modified"))
	assert.Empty(t, recorder.Header().Values("Set-Cookie"))
	assert.Empty(t, recorder.Header().Values("X-Secret"))
	assert.NotContains(t, recorder.Result().Header.Values("Set-Cookie"), "session=attacker")
	assert.NotContains(t, recorder.Result().Header.Values("X-Secret"), "audio-secret-sentinel")
}

func TestOpenaiTTSHandlerStreamingUsesRelayHeaders(t *testing.T) {
	const upstreamSSE = "" +
		"data: {\"id\":\"chunk-1\",\"text\":\"hello\"}\n" +
		"data: {\"id\":\"chunk-2\",\"text\":\"world\"}\n" +
		"data: [DONE]\n"
	originalStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = originalStreamingTimeout
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":        {"application/x-upstream-marker"},
			"Content-Length":      {"5"},
			"Content-Disposition": {`inline; filename="speech.mp3"`},
			"ETag":                {`"speech-etag"`},
			"Last-Modified":       {"Wed, 21 Oct 2015 07:28:00 GMT"},
			"Set-Cookie":          {"session=attacker"},
			"Retry-After":         {"2"},
			"X-Secret":            {"audio-secret-sentinel"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamSSE)),
	}
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		DisablePing: true,
	}

	usage := OpenaiTTSHandler(c, resp, info)

	require.NotNil(t, usage)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(
		t,
		"data: {\"id\":\"chunk-1\",\"text\":\"hello\"}\n\n"+
			"data: {\"id\":\"chunk-2\",\"text\":\"world\"}\n\n",
		recorder.Body.String(),
	)
	assert.Equal(t, "text/event-stream", recorder.Result().Header.Get("Content-Type"))
	assert.Equal(t, "2", recorder.Header().Get("Retry-After"))
	for _, name := range []string{
		"Content-Length",
		"Content-Disposition",
		"ETag",
		"Last-Modified",
		"Set-Cookie",
		"X-Secret",
	} {
		assert.Empty(t, recorder.Header().Values(name), name)
	}
}

func TestOpenaiTTSHandlerReadFailureDoesNotCommitUpstreamContentLength(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	resp := &http.Response{
		StatusCode: http.StatusAccepted,
		Header: http.Header{
			"Content-Type":        {"audio/mpeg"},
			"Content-Length":      {"999"},
			"Content-Disposition": {`inline; filename="speech.mp3"`},
		},
		Body: io.NopCloser(io.MultiReader(
			strings.NewReader("partial"),
			iotest.ErrReader(errors.New("deterministic read failure")),
		)),
	}
	info := &relaycommon.RelayInfo{
		Request: &dto.AudioRequest{ResponseFormat: "pcm"},
	}

	usage := OpenaiTTSHandler(c, resp, info)

	require.NotNil(t, usage)
	assert.Equal(t, http.StatusAccepted, recorder.Code)
	assert.Empty(t, recorder.Body.String())
	assert.Equal(t, "audio/mpeg", recorder.Header().Get("Content-Type"))
	assert.Empty(t, recorder.Header().Values("Content-Length"))
	assert.Empty(t, recorder.Header().Values("Content-Disposition"))
}
