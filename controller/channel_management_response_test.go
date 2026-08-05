package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	expectedChannelBalanceResponseMaxBytes  int64 = 1 << 20
	expectedChannelModelResponseMaxBytes    int64 = 4 << 20
	expectedChannelManagementRequestTimeout       = 20 * time.Second
)

type channelManagementRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip channelManagementRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

type trackingChannelManagementBody struct {
	reader    io.Reader
	bytesRead int64
	closed    bool
}

func (body *trackingChannelManagementBody) Read(data []byte) (int, error) {
	count, err := body.reader.Read(data)
	body.bytesRead += int64(count)
	return count, err
}

func (body *trackingChannelManagementBody) Close() error {
	body.closed = true
	return nil
}

func newChannelManagementTestClient(
	roundTrip channelManagementRoundTripper,
) *http.Client {
	return &http.Client{Transport: roundTrip}
}

func newChannelManagementTestRequest(
	t *testing.T,
	ctx context.Context,
) *http.Request {
	t.Helper()

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://management.example/models?api_key=credential-sentinel",
		nil,
	)
	require.NoError(t, err)
	return request
}

func assertChannelManagementError(
	t *testing.T,
	err error,
	expected string,
	forbidden ...string,
) {
	t.Helper()

	require.EqualError(t, err, expected)
	for _, sentinel := range forbidden {
		assert.NotContains(t, err.Error(), sentinel)
	}
}

func TestReadChannelManagementResponsePreservesExactBytesAndCloses(
	t *testing.T,
) {
	const maxBytes int64 = 64
	tests := []struct {
		name          string
		payload       []byte
		contentLength int64
	}{
		{
			name:          "smaller declared response",
			payload:       []byte("exact response bytes"),
			contentLength: int64(len("exact response bytes")),
		},
		{
			name:          "exact limit with unknown length",
			payload:       bytes.Repeat([]byte("x"), int(maxBytes)),
			contentLength: -1,
		},
		{
			name:          "exact limit with declared length",
			payload:       bytes.Repeat([]byte("x"), int(maxBytes)),
			contentLength: maxBytes,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &trackingChannelManagementBody{
				reader: bytes.NewReader(test.payload),
			}
			client := newChannelManagementTestClient(
				func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode:    http.StatusOK,
						ContentLength: test.contentLength,
						Body:          body,
					}, nil
				},
			)

			result, err := readChannelManagementResponse(
				client,
				newChannelManagementTestRequest(t, context.Background()),
				maxBytes,
			)

			require.NoError(t, err)
			assert.Equal(t, test.payload, result)
			assert.True(t, body.closed)
			assert.Equal(t, int64(len(test.payload)), body.bytesRead)
		})
	}
}

func TestReadChannelManagementResponseRejectsDeclaredOversizeWithoutReading(
	t *testing.T,
) {
	const maxBytes int64 = 32
	body := &trackingChannelManagementBody{
		reader: strings.NewReader("body-sentinel"),
	}
	client := newChannelManagementTestClient(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: maxBytes + 1,
				Body:          body,
			}, nil
		},
	)

	result, err := readChannelManagementResponse(
		client,
		newChannelManagementTestRequest(t, context.Background()),
		maxBytes,
	)

	assert.Nil(t, result)
	assertChannelManagementError(
		t,
		err,
		"channel management response exceeds configured limit",
		"body-sentinel",
		"credential-sentinel",
	)
	assert.Zero(t, body.bytesRead)
	assert.True(t, body.closed)
}

func TestReadChannelManagementResponseRejectsUnknownLengthAtLimitPlusOne(
	t *testing.T,
) {
	const maxBytes int64 = 32
	body := &trackingChannelManagementBody{
		reader: bytes.NewReader(bytes.Repeat([]byte("x"), int(maxBytes+100))),
	}
	client := newChannelManagementTestClient(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: -1,
				Body:          body,
			}, nil
		},
	)

	result, err := readChannelManagementResponse(
		client,
		newChannelManagementTestRequest(t, context.Background()),
		maxBytes,
	)

	assert.Nil(t, result)
	assertChannelManagementError(
		t,
		err,
		"channel management response exceeds configured limit",
		"credential-sentinel",
	)
	assert.Equal(t, maxBytes+1, body.bytesRead)
	assert.True(t, body.closed)
}

func TestReadChannelManagementResponseRejectsNonOKWithoutReadingAndCloses(
	t *testing.T,
) {
	body := &trackingChannelManagementBody{
		reader: strings.NewReader("provider-body-sentinel"),
	}
	client := newChannelManagementTestClient(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				Status:        "503 provider-status-sentinel",
				StatusCode:    http.StatusServiceUnavailable,
				ContentLength: int64(len("provider-body-sentinel")),
				Body:          body,
			}, nil
		},
	)

	result, err := readChannelManagementResponse(
		client,
		newChannelManagementTestRequest(t, context.Background()),
		64,
	)

	assert.Nil(t, result)
	assertChannelManagementError(
		t,
		err,
		"status code: 503",
		"provider-body-sentinel",
		"provider-status-sentinel",
		"credential-sentinel",
	)
	assert.Zero(t, body.bytesRead)
	assert.True(t, body.closed)
}

func TestReadChannelManagementResponseRedactsTransportAndReadFailures(
	t *testing.T,
) {
	t.Run("transport failure", func(t *testing.T) {
		client := newChannelManagementTestClient(
			func(*http.Request) (*http.Response, error) {
				return nil, errors.New("raw-transport-sentinel")
			},
		)

		result, err := readChannelManagementResponse(
			client,
			newChannelManagementTestRequest(t, context.Background()),
			64,
		)

		assert.Nil(t, result)
		assertChannelManagementError(
			t,
			err,
			"channel management request failed",
			"raw-transport-sentinel",
			"management.example",
			"api_key",
			"credential-sentinel",
		)
	})

	t.Run("read failure", func(t *testing.T) {
		body := &trackingChannelManagementBody{
			reader: iotest.ErrReader(errors.New("raw-read-sentinel")),
		}
		client := newChannelManagementTestClient(
			func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode:    http.StatusOK,
					ContentLength: -1,
					Body:          body,
				}, nil
			},
		)

		result, err := readChannelManagementResponse(
			client,
			newChannelManagementTestRequest(t, context.Background()),
			64,
		)

		assert.Nil(t, result)
		assertChannelManagementError(
			t,
			err,
			"channel management response read failed",
			"raw-read-sentinel",
			"credential-sentinel",
		)
		assert.True(t, body.closed)
	})
}

func TestReadChannelManagementResponseAppliesDeadlineAndPreservesShorterCaller(
	t *testing.T,
) {
	t.Run("adds management deadline", func(t *testing.T) {
		var observedContext context.Context
		var observedDeadline time.Time
		var observedAt time.Time
		var hasDeadline bool
		body := &trackingChannelManagementBody{
			reader: strings.NewReader("ok"),
		}
		client := newChannelManagementTestClient(
			func(request *http.Request) (*http.Response, error) {
				observedContext = request.Context()
				observedAt = time.Now()
				observedDeadline, hasDeadline = observedContext.Deadline()
				return &http.Response{
					StatusCode:    http.StatusOK,
					ContentLength: 2,
					Body:          body,
				}, nil
			},
		)
		result, err := readChannelManagementResponse(
			client,
			newChannelManagementTestRequest(t, context.Background()),
			64,
		)

		require.NoError(t, err)
		assert.Equal(t, []byte("ok"), result)
		require.True(t, hasDeadline)
		assert.LessOrEqual(
			t,
			observedDeadline.Sub(observedAt),
			expectedChannelManagementRequestTimeout,
		)
		assert.Greater(
			t,
			observedDeadline.Sub(observedAt),
			expectedChannelManagementRequestTimeout-time.Second,
		)
		assert.ErrorIs(t, observedContext.Err(), context.Canceled)
	})

	t.Run("preserves shorter caller deadline", func(t *testing.T) {
		callerContext, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		callerDeadline, hasCallerDeadline := callerContext.Deadline()
		require.True(t, hasCallerDeadline)

		var observedDeadline time.Time
		body := &trackingChannelManagementBody{
			reader: strings.NewReader("ok"),
		}
		client := newChannelManagementTestClient(
			func(request *http.Request) (*http.Response, error) {
				var ok bool
				observedDeadline, ok = request.Context().Deadline()
				require.True(t, ok)
				return &http.Response{
					StatusCode:    http.StatusOK,
					ContentLength: 2,
					Body:          body,
				}, nil
			},
		)

		result, err := readChannelManagementResponse(
			client,
			newChannelManagementTestRequest(t, callerContext),
			64,
		)

		require.NoError(t, err)
		assert.Equal(t, []byte("ok"), result)
		assert.True(t, observedDeadline.Equal(callerDeadline))
	})
}

func TestReadChannelManagementResponseRejectsInvalidLocalState(t *testing.T) {
	request := newChannelManagementTestRequest(t, context.Background())
	client := newChannelManagementTestClient(
		func(*http.Request) (*http.Response, error) {
			t.Fatal("transport must not run for invalid local state")
			return nil, nil
		},
	)

	tests := []struct {
		name      string
		client    *http.Client
		request   *http.Request
		maxBytes  int64
		wantError string
	}{
		{
			name:      "nil client",
			request:   request,
			maxBytes:  1,
			wantError: "channel management HTTP client is required",
		},
		{
			name:      "nil request",
			client:    client,
			maxBytes:  1,
			wantError: "channel management request is required",
		},
		{
			name:      "non-positive limit",
			client:    client,
			request:   request,
			wantError: "channel management response limit must be positive",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := readChannelManagementResponse(
				test.client,
				test.request,
				test.maxBytes,
			)

			assert.Nil(t, result)
			require.EqualError(t, err, test.wantError)
		})
	}
}

func TestChannelManagementContextWrappersPreserveCallerCancellation(
	t *testing.T,
) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = response.Write([]byte("must-not-run"))
		},
	))
	t.Cleanup(server.Close)

	tests := []struct {
		name string
		call func(context.Context) error
	}{
		{
			name: "balance wrapper",
			call: func(ctx context.Context) error {
				_, err := getResponseBodyWithContext(
					ctx,
					http.MethodGet,
					server.URL+"/balance",
					&model.Channel{},
					nil,
				)
				return err
			},
		},
		{
			name: "model discovery wrapper",
			call: func(ctx context.Context) error {
				_, err := getFetchModelsResponseBodyWithContext(
					ctx,
					http.MethodGet,
					server.URL+"/models",
					&model.Channel{},
					nil,
				)
				return err
			},
		},
		{
			name: "balance production path",
			call: func(ctx context.Context) error {
				baseURL := server.URL
				_, err := updateChannelBalanceWithContext(
					ctx,
					&model.Channel{
						Type:    constant.ChannelTypeCustom,
						Key:     "key",
						BaseURL: &baseURL,
					},
				)
				return err
			},
		},
		{
			name: "model discovery production path",
			call: func(ctx context.Context) error {
				baseURL := server.URL
				_, err := fetchChannelUpstreamModelIDsWithContext(
					ctx,
					&model.Channel{
						Type:    constant.ChannelTypeCustom,
						Key:     "key",
						BaseURL: &baseURL,
					},
				)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := test.call(ctx)

			require.EqualError(t, err, "channel management request failed")
		})
	}
	assert.Zero(t, requests.Load())
}

func TestUpdateChannelBalancePropagatesCanceledRequestContext(t *testing.T) {
	database := setupModelListControllerTestDB(t)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = response.Write([]byte(
				`{"has_payment_method":true,"hard_limit_usd":10,"total_usage":0}`,
			))
		},
	))
	t.Cleanup(server.Close)

	baseURL := server.URL
	channel := &model.Channel{
		Name:    "canceled balance handler",
		Type:    constant.ChannelTypeCustom,
		Key:     "key",
		BaseURL: &baseURL,
		Status:  common.ChannelStatusEnabled,
	}
	require.NoError(t, database.Create(channel).Error)

	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Params = gin.Params{
		{Key: "id", Value: strconv.Itoa(channel.Id)},
	}
	ginContext.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/channel/"+strconv.Itoa(channel.Id)+"/balance",
		nil,
	).WithContext(requestContext)

	UpdateChannelBalance(ginContext)

	assert.Zero(t, requests.Load())
}

func TestFetchModelsPropagatesCanceledRequestContext(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = response.Write([]byte(`{"data":[{"id":"must-not-load"}]}`))
		},
	))
	t.Cleanup(server.Close)

	body, err := common.Marshal(map[string]any{
		"base_url": server.URL,
		"type":     constant.ChannelTypeCustom,
		"key":      "key",
	})
	require.NoError(t, err)

	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/channel/fetch_models",
		bytes.NewReader(body),
	).WithContext(requestContext)
	ginContext.Request.Header.Set("Content-Type", "application/json")

	FetchModels(ginContext)

	assert.Zero(t, requests.Load())
}

func TestFetchChannelUpstreamModelsRespectsCanceledContextForOllamaAndGemini(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = response.Write([]byte(`{"data":[{"id":"must-not-load"}]`))
		},
	))
	t.Cleanup(server.Close)

	baseURL := server.URL

	tests := []struct {
		name string
		ch   *model.Channel
	}{
		{
			name: "ollama",
			ch: &model.Channel{
				Type:    constant.ChannelTypeOllama,
				Key:     "key",
				BaseURL: &baseURL,
			},
		},
		{
			name: "gemini",
			ch: &model.Channel{
				Type:    constant.ChannelTypeGemini,
				Key:     "key",
				BaseURL: &baseURL,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requests.Store(0)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			_, err := fetchChannelUpstreamModelIDsWithContext(
				ctx,
				tc.ch,
			)

			require.Error(t, err)
			assert.Zero(t, requests.Load())
		})
	}
}

func TestScheduledModelUpdatePropagatesCancellationAfterProgress(
	t *testing.T,
) {
	database := setupModelListControllerTestDB(t)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = response.Write([]byte(`{"data":[{"id":"new-model"}]}`))
		},
	))
	t.Cleanup(server.Close)

	baseURL := server.URL
	channel := &model.Channel{
		Name:    "canceled scheduled discovery",
		Type:    constant.ChannelTypeCustom,
		Key:     "key",
		BaseURL: &baseURL,
		Models:  "old-model",
		Status:  common.ChannelStatusEnabled,
	}
	settings := channel.GetOtherSettings()
	settings.UpstreamModelUpdateCheckEnabled = true
	channel.SetOtherSettings(settings)
	require.NoError(t, database.Create(channel).Error)

	ctx, cancel := context.WithCancel(context.Background())
	summary := runChannelUpstreamModelUpdateTaskOnce(
		ctx,
		true,
		false,
		func(_, _ int) {
			cancel()
		},
	)

	assert.Equal(t, 1, summary.CheckedChannels)
	assert.Equal(t, 1, summary.FailedChannels)
	assert.Zero(t, requests.Load())
}

func TestChannelManagementWrappersRejectInvalidURLWithoutCredentialDisclosure(
	t *testing.T,
) {
	const invalidURL = "https://user-sentinel:password-sentinel@" +
		"management.example/%zz?api_key=credential-sentinel"
	tests := []struct {
		name string
		call func() ([]byte, error)
	}{
		{
			name: "balance",
			call: func() ([]byte, error) {
				return GetResponseBody(
					http.MethodGet,
					invalidURL,
					&model.Channel{},
					nil,
				)
			},
		},
		{
			name: "model discovery",
			call: func() ([]byte, error) {
				return getFetchModelsResponseBody(
					http.MethodGet,
					invalidURL,
					&model.Channel{},
					nil,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := test.call()

			assert.Nil(t, body)
			assertChannelManagementError(
				t,
				err,
				"channel management request is invalid",
				"user-sentinel",
				"password-sentinel",
				"credential-sentinel",
				"management.example",
				"%zz",
			)
		})
	}
}

func TestChannelBalanceResponseLimit(t *testing.T) {
	payload := bytes.Repeat(
		[]byte("b"),
		int(expectedChannelBalanceResponseMaxBytes),
	)
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			_, _ = response.Write(payload)
		},
	))
	t.Cleanup(server.Close)

	body, err := GetResponseBody(
		http.MethodGet,
		server.URL+"/balance",
		&model.Channel{},
		nil,
	)

	require.NoError(t, err)
	assert.True(t, bytes.Equal(payload, body))

	payload = bytes.Repeat(
		[]byte("b"),
		int(expectedChannelBalanceResponseMaxBytes+1),
	)
	body, err = GetResponseBody(
		http.MethodGet,
		server.URL+"/balance",
		&model.Channel{},
		nil,
	)

	assert.Nil(t, body)
	require.EqualError(
		t,
		err,
		"channel management response exceeds configured limit",
	)
}

func TestChannelModelResponseLimit(t *testing.T) {
	payload := bytes.Repeat(
		[]byte("m"),
		int(expectedChannelBalanceResponseMaxBytes+1),
	)
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			_, _ = response.Write(payload)
		},
	))
	t.Cleanup(server.Close)

	body, err := getFetchModelsResponseBody(
		http.MethodGet,
		server.URL+"/models",
		&model.Channel{},
		nil,
	)

	require.NoError(t, err)
	assert.True(t, bytes.Equal(payload, body))

	payload = bytes.Repeat(
		[]byte("m"),
		int(expectedChannelModelResponseMaxBytes),
	)
	body, err = getFetchModelsResponseBody(
		http.MethodGet,
		server.URL+"/models",
		&model.Channel{},
		nil,
	)

	require.NoError(t, err)
	assert.True(t, bytes.Equal(payload, body))

	payload = bytes.Repeat(
		[]byte("m"),
		int(expectedChannelModelResponseMaxBytes+1),
	)
	body, err = getFetchModelsResponseBody(
		http.MethodGet,
		server.URL+"/models",
		&model.Channel{},
		nil,
	)

	assert.Nil(t, body)
	require.EqualError(
		t,
		err,
		"channel management response exceeds configured limit",
	)
}
