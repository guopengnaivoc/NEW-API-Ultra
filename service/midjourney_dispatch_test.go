package service

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMidjourneyDispatchTestContext(body string) *gin.Context {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"http://gateway.example/mj/submit/imagine",
		strings.NewReader(body),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	return context
}

func TestDoMidjourneyHttpRequestDispatchClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousHTTPClient := httpClient
	httpClient = &http.Client{}
	t.Cleanup(func() {
		httpClient = previousHTTPClient
	})

	t.Run("local body error was not dispatched", func(t *testing.T) {
		context := newMidjourneyDispatchTestContext("{")

		_, _, err := DoMidjourneyHttpRequest(
			context,
			time.Second,
			"http://upstream.example/mj/submit/imagine",
		)
		require.Error(t, err)
		assert.False(t, MidjourneyRequestWasDispatched(err))
	})

	t.Run("transport error is delivery ambiguous", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		address := listener.Addr().String()
		require.NoError(t, listener.Close())
		context := newMidjourneyDispatchTestContext(`{"prompt":"test"}`)

		_, _, err = DoMidjourneyHttpRequest(
			context,
			time.Second,
			"http://"+address+"/mj/submit/imagine",
		)
		require.Error(t, err)
		assert.True(t, MidjourneyRequestWasDispatched(err))
	})

	t.Run("response parse error was dispatched", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("{not-json"))
		}))
		t.Cleanup(upstream.Close)
		context := newMidjourneyDispatchTestContext(`{"prompt":"test"}`)

		_, _, err := DoMidjourneyHttpRequest(
			context,
			time.Second,
			upstream.URL+"/mj/submit/imagine",
		)
		require.Error(t, err)
		assert.True(t, MidjourneyRequestWasDispatched(err))
	})

	t.Run("empty response was dispatched", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(upstream.Close)
		context := newMidjourneyDispatchTestContext(`{"prompt":"test"}`)

		_, _, err := DoMidjourneyHttpRequest(
			context,
			time.Second,
			upstream.URL+"/mj/submit/imagine",
		)
		require.Error(t, err)
		assert.True(t, MidjourneyRequestWasDispatched(err))
	})
}

func TestDoMidjourneyHttpRequestRejectsOversizedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	responseBody := newTrackingResponseBody(`{"code":1,"description":"queued","result":"task-id"}`)
	previousHTTPClient := httpClient
	httpClient = oversizedResponseClient(responseBody, MidjourneyResponseMaxBytes+1)
	t.Cleanup(func() {
		httpClient = previousHTTPClient
	})
	context := newMidjourneyDispatchTestContext(`{"prompt":"test"}`)

	_, body, err := DoMidjourneyHttpRequest(
		context,
		time.Second,
		"http://upstream.example/mj/submit/imagine",
	)

	require.ErrorIs(t, err, errServiceResponseTooLarge)
	assert.Nil(t, body)
	assert.True(t, MidjourneyRequestWasDispatched(err))
	assert.True(t, responseBody.closed)
	assert.Zero(t, responseBody.reads)
}
