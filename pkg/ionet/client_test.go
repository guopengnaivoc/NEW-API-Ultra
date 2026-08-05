package ionet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	expectedSuccessResponseBodyLimit = 8 * 1024 * 1024
	expectedErrorResponseBodyLimit   = 64 * 1024
)

type recordingHTTPClient struct {
	mu       sync.Mutex
	requests []*HTTPRequest
	response *HTTPResponse
	err      error
}

func (c *recordingHTTPClient) Do(req *HTTPRequest) (*HTTPResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	return c.response, c.err
}

func (c *recordingHTTPClient) lastRequest(t *testing.T) *HTTPRequest {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()
	require.NotEmpty(t, c.requests)
	return c.requests[len(c.requests)-1]
}

type fixedResponseRoundTripper struct {
	response *http.Response
}

func (r fixedResponseRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return r.response, nil
}

type countingReadCloser struct {
	remaining int
	read      int
}

func (r *countingReadCloser) Read(buffer []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	size := len(buffer)
	if size > r.remaining {
		size = r.remaining
	}
	for i := 0; i < size; i++ {
		buffer[i] = 'x'
	}
	r.remaining -= size
	r.read += size
	return size, nil
}

func (r *countingReadCloser) Close() error {
	return nil
}

func TestDefaultHTTPClientDoPropagatesCallerCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		select {
		case <-r.Context().Done():
			close(requestCanceled)
		case <-releaseHandler:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(func() {
		select {
		case <-releaseHandler:
		default:
			close(releaseHandler)
		}
		server.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	client := NewDefaultHTTPClient(5 * time.Second)
	result := make(chan error, 1)
	go func() {
		_, err := client.Do(&HTTPRequest{
			Context: ctx,
			Method:  http.MethodGet,
			URL:     server.URL,
		})
		result <- err
	}()

	require.Eventually(t, func() bool {
		select {
		case <-requestStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	cancel()

	require.Eventually(t, func() bool {
		select {
		case <-requestCanceled:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	err := <-result
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestDefaultHTTPClientDoBoundsResponseBodies(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		size          int
		wantErr       bool
		wantBodySize  int
		wantTruncated bool
	}{
		{
			name:         "success at limit",
			status:       http.StatusOK,
			size:         expectedSuccessResponseBodyLimit,
			wantBodySize: expectedSuccessResponseBodyLimit,
		},
		{
			name:    "success over limit",
			status:  http.StatusOK,
			size:    expectedSuccessResponseBodyLimit + 1,
			wantErr: true,
		},
		{
			name:         "error at limit",
			status:       http.StatusBadGateway,
			size:         expectedErrorResponseBodyLimit,
			wantBodySize: expectedErrorResponseBodyLimit,
		},
		{
			name:          "error over limit",
			status:        http.StatusBadGateway,
			size:          expectedErrorResponseBodyLimit + 1,
			wantBodySize:  expectedErrorResponseBodyLimit,
			wantTruncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte("x"), tt.size)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write(payload)
			}))
			t.Cleanup(server.Close)

			response, err := NewDefaultHTTPClient(5 * time.Second).Do(&HTTPRequest{
				Context: context.Background(),
				Method:  http.MethodGet,
				URL:     server.URL,
			})
			if tt.wantErr {
				require.Error(t, err)
				var tooLargeErr *ResponseBodyTooLargeError
				require.ErrorAs(t, err, &tooLargeErr)
				assert.Equal(t, tt.status, tooLargeErr.StatusCode)
				assert.Equal(t, int64(expectedSuccessResponseBodyLimit), tooLargeErr.Limit)
				assert.Nil(t, response)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, response)
			assert.Len(t, response.Body, tt.wantBodySize)
			assert.Equal(t, tt.wantTruncated, response.BodyTruncated)
		})
	}
}

func TestDefaultHTTPClientDoStopsReadingAtBodyLimit(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		bodyLimit int
		wantErr   bool
	}{
		{
			name:      "success",
			status:    http.StatusOK,
			bodyLimit: expectedSuccessResponseBodyLimit,
			wantErr:   true,
		},
		{
			name:      "error",
			status:    http.StatusBadGateway,
			bodyLimit: expectedErrorResponseBodyLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &countingReadCloser{remaining: tt.bodyLimit * 2}
			client := &DefaultHTTPClient{
				client: &http.Client{
					Transport: fixedResponseRoundTripper{
						response: &http.Response{
							StatusCode:    tt.status,
							Header:        make(http.Header),
							Body:          body,
							ContentLength: -1,
						},
					},
				},
			}

			response, err := client.Do(&HTTPRequest{
				Context: context.Background(),
				Method:  http.MethodGet,
				URL:     "https://example.com/resource",
			})
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, response)
			} else {
				require.NoError(t, err)
				require.NotNil(t, response)
				assert.True(t, response.BodyTruncated)
			}
			assert.Equal(t, tt.bodyLimit+1, body.read)
		})
	}
}

func TestClientWithContextClonesAndPropagatesRequestContext(t *testing.T) {
	httpClient := &recordingHTTPClient{
		response: &HTTPResponse{
			StatusCode: http.StatusOK,
			Body:       []byte("{}"),
		},
	}
	baseClient := NewClientWithConfig("key", "https://example.com", httpClient)
	callerContext, cancel := context.WithCancel(context.Background())
	boundClient := baseClient.WithContext(callerContext)

	require.NotSame(t, baseClient, boundClient)
	_, err := boundClient.makeRequest(http.MethodGet, "/bound", nil)
	require.NoError(t, err)
	boundRequest := httpClient.lastRequest(t)
	require.Same(t, callerContext, boundRequest.Context)

	cancel()
	assert.ErrorIs(t, boundRequest.Context.Err(), context.Canceled)

	_, err = baseClient.makeRequest(http.MethodGet, "/base", nil)
	require.NoError(t, err)
	baseRequest := httpClient.lastRequest(t)
	assert.NoError(t, baseRequest.Context.Err())
	assert.NotEqual(t, callerContext, baseRequest.Context)
}

func TestClientMakeRequestBoundsAndOmitsUpstreamErrorContent(t *testing.T) {
	secret := "TOPSECRETTOKEN"
	privateBody := []byte(
		`{"detail":"private ` + secret +
			` https://storage.example.com/private?token=` + secret + `"}`,
	)
	tests := []struct {
		name            string
		body            []byte
		wantDescription string
	}{
		{
			name:            "bounded error body",
			body:            privateBody,
			wantDescription: "omitted",
		},
		{
			name: "oversized error body",
			body: append(
				append([]byte(nil), privateBody...),
				bytes.Repeat([]byte("x"), expectedErrorResponseBodyLimit+1)...,
			),
			wantDescription: "exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpClient := &recordingHTTPClient{
				response: &HTTPResponse{
					StatusCode: http.StatusBadGateway,
					Body:       tt.body,
				},
			}
			client := NewClientWithConfig("key", "https://example.com", httpClient)

			_, err := client.makeRequest(http.MethodGet, "/failure", nil)
			require.Error(t, err)

			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusBadGateway, apiErr.Code)
			assert.Equal(t, "API request failed with status 502", apiErr.Message)
			assert.NotContains(t, apiErr.Details, secret)
			assert.NotContains(t, apiErr.Error(), secret)
			assert.NotContains(t, apiErr.Details, "storage.example.com")
			assert.LessOrEqual(t, len(apiErr.Details), 128)
			assert.Contains(t, strings.ToLower(apiErr.Details), tt.wantDescription)
		})
	}
}

func TestClientMakeRequestRejectsOversizedSuccessFromCustomTransport(t *testing.T) {
	httpClient := &recordingHTTPClient{
		response: &HTTPResponse{
			StatusCode: http.StatusOK,
			Body:       bytes.Repeat([]byte("x"), expectedSuccessResponseBodyLimit+1),
		},
	}
	client := NewClientWithConfig("key", "https://example.com", httpClient)

	_, err := client.makeRequest(http.MethodGet, "/success", nil)
	require.Error(t, err)
	var tooLargeErr *ResponseBodyTooLargeError
	assert.True(t, errors.As(err, &tooLargeErr))
}
