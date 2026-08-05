package controller

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type midjourneyPollingResponseBody struct {
	reader    io.Reader
	bytesRead int64
	closed    bool
}

func (body *midjourneyPollingResponseBody) Read(p []byte) (int, error) {
	n, err := body.reader.Read(p)
	body.bytesRead += int64(n)
	return n, err
}

func (body *midjourneyPollingResponseBody) Close() error {
	body.closed = true
	return nil
}

type midjourneyPollingRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn midjourneyPollingRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func useMidjourneyPollingTransport(t *testing.T, transport http.RoundTripper) {
	t.Helper()

	client := service.GetHttpClient()
	require.NotNil(t, client)
	previousTransport := client.Transport
	client.Transport = transport
	t.Cleanup(func() {
		client.Transport = previousTransport
	})
}

func captureMidjourneyPollingLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	logs := &bytes.Buffer{}
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultWriter = logs
	gin.DefaultErrorWriter = logs
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		gin.DefaultErrorWriter = previousErrorWriter
		common.LogWriterMu.Unlock()
	})
	return logs
}

func TestMidjourneyScheduledPollingBoundsSuccessAndErrorResponses(t *testing.T) {
	t.Run("oversized successful response", func(t *testing.T) {
		fixture := setupMidjourneyPollingTest(t, "polling-oversized", `[]`)
		logs := captureMidjourneyPollingLogs(t)
		const rawMarker = "MIDJOURNEY_OVERSIZED_BODY_MARKER"
		responseContent := `{"secret":"` + fixture.channel.Key + `","marker":"` + rawMarker +
			`","payload":"` + strings.Repeat("x", int(service.MidjourneyResponseMaxBytes))
		responseBody := &midjourneyPollingResponseBody{reader: strings.NewReader(responseContent)}
		useMidjourneyPollingTransport(t, midjourneyPollingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          responseBody,
				ContentLength: -1,
				Header:        make(http.Header),
				Request:       request,
			}, nil
		}))

		summary := runMidjourneyTaskUpdateOnce(context.Background(), nil)

		assert.Equal(t, 1, summary.UnfinishedTasks)
		assert.Equal(t, 1, summary.ChannelsScanned)
		assert.True(t, responseBody.closed)
		assert.LessOrEqual(
			t,
			responseBody.bytesRead,
			service.MidjourneyResponseMaxBytes+1,
		)
		logOutput := logs.String()
		assert.Contains(t, logOutput, "service response body exceeds limit")
		assert.False(t, strings.Contains(logOutput, rawMarker), "logs exposed the oversized response body")
		assert.False(t, strings.Contains(logOutput, fixture.channel.Key), "logs exposed the channel secret")

		_, _, task, reservation := fixture.loadBillingState(t)
		assert.Equal(t, "SUBMITTED", task.Status)
		assert.Equal(t, model.MidjourneyQuotaReservationStatusReserved, reservation.Status)
	})

	t.Run("non-success response", func(t *testing.T) {
		fixture := setupMidjourneyPollingTest(t, "polling-error-status", `[]`)
		logs := captureMidjourneyPollingLogs(t)
		const rawMarker = "MIDJOURNEY_ERROR_BODY_MARKER"
		responseContent := rawMarker + fixture.channel.Key +
			strings.Repeat("x", int(service.MidjourneyResponseMaxBytes))
		responseBody := &midjourneyPollingResponseBody{reader: strings.NewReader(responseContent)}
		useMidjourneyPollingTransport(t, midjourneyPollingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusBadGateway,
				Body:          responseBody,
				ContentLength: int64(len(responseContent)),
				Header:        make(http.Header),
				Request:       request,
			}, nil
		}))

		summary := runMidjourneyTaskUpdateOnce(context.Background(), nil)

		assert.Equal(t, 1, summary.UnfinishedTasks)
		assert.Equal(t, 1, summary.ChannelsScanned)
		assert.True(t, responseBody.closed)
		assert.Zero(t, responseBody.bytesRead)
		logOutput := logs.String()
		assert.Contains(t, logOutput, "Get Task status code: 502")
		assert.False(t, strings.Contains(logOutput, rawMarker), "logs exposed the error response body")
		assert.False(t, strings.Contains(logOutput, fixture.channel.Key), "logs exposed the channel secret")
	})
}

func TestMidjourneyScheduledPollingSanitizesMalformedResponseLog(t *testing.T) {
	fixture := setupMidjourneyPollingTest(t, "polling-malformed", `[]`)
	logs := captureMidjourneyPollingLogs(t)
	const rawMarker = "MIDJOURNEY_MALFORMED_BODY_MARKER"
	responseContent := `{"secret":"` + fixture.channel.Key + `","marker":"` + rawMarker + `"`
	responseBody := &midjourneyPollingResponseBody{reader: strings.NewReader(responseContent)}
	useMidjourneyPollingTransport(t, midjourneyPollingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          responseBody,
			ContentLength: int64(len(responseContent)),
			Header:        make(http.Header),
			Request:       request,
		}, nil
	}))

	summary := runMidjourneyTaskUpdateOnce(context.Background(), nil)

	assert.Equal(t, 1, summary.UnfinishedTasks)
	assert.Equal(t, 1, summary.ChannelsScanned)
	assert.True(t, responseBody.closed)
	assert.LessOrEqual(t, responseBody.bytesRead, service.MidjourneyResponseMaxBytes+1)
	logOutput := logs.String()
	assert.Contains(t, logOutput, "Get Mjp Task parse body error2:")
	assert.False(t, strings.Contains(logOutput, rawMarker), "logs exposed the malformed response body")
	assert.False(t, strings.Contains(logOutput, fixture.channel.Key), "logs exposed the channel secret")
}

func TestMidjourneyScheduledPollingPreservesValidResponseAndTimeout(t *testing.T) {
	fixture := setupMidjourneyPollingTest(t, "polling-valid-boundary", `[]`)
	logs := captureMidjourneyPollingLogs(t)
	responseContent := `[{"id":"polling-valid-boundary","progress":"100%","status":"SUCCESS"}]`
	responseBody := &midjourneyPollingResponseBody{reader: strings.NewReader(responseContent)}
	type contextKey struct{}
	parentContext := context.WithValue(context.Background(), contextKey{}, "preserved")
	var requestContext context.Context
	var requestDeadlineRemaining time.Duration
	var hasDeadline bool
	var inheritedValue any
	useMidjourneyPollingTransport(t, midjourneyPollingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestContext = request.Context()
		requestDeadline, deadlineSet := request.Context().Deadline()
		hasDeadline = deadlineSet
		requestDeadlineRemaining = time.Until(requestDeadline)
		inheritedValue = request.Context().Value(contextKey{})
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          responseBody,
			ContentLength: int64(len(responseContent)),
			Header:        make(http.Header),
			Request:       request,
		}, nil
	}))

	summary := runMidjourneyTaskUpdateOnce(parentContext, nil)

	assert.Equal(t, 1, summary.UnfinishedTasks)
	assert.Equal(t, 1, summary.ChannelsScanned)
	require.True(t, hasDeadline)
	assert.Greater(t, requestDeadlineRemaining, 14*time.Second)
	assert.LessOrEqual(t, requestDeadlineRemaining, 15*time.Second)
	assert.Equal(t, "preserved", inheritedValue)
	require.NotNil(t, requestContext)
	assert.ErrorIs(t, requestContext.Err(), context.Canceled)
	assert.NoError(t, parentContext.Err())
	assert.True(t, responseBody.closed)
	assert.Equal(t, int64(len(responseContent)), responseBody.bytesRead)
	assert.False(t, strings.Contains(logs.String(), fixture.channel.Key), "logs exposed the channel secret")

	user, token, task, reservation := fixture.loadBillingState(t)
	assert.Equal(t, 70, user.Quota)
	assert.Equal(t, 70, token.RemainQuota)
	assert.Equal(t, "SUCCESS", task.Status)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusSettled, reservation.Status)
}
