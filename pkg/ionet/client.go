package ionet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	DefaultEnterpriseBaseURL = "https://api.io.solutions/enterprise/v1/io-cloud/caas"
	DefaultBaseURL           = "https://api.io.solutions/v1/io-cloud/caas"
	DefaultTimeout           = 30 * time.Second

	maxSuccessResponseBodyBytes = 8 * 1024 * 1024
	maxErrorResponseBodyBytes   = 64 * 1024
)

// ResponseBodyTooLargeError reports an upstream response that exceeded the
// bounded in-memory response size.
type ResponseBodyTooLargeError struct {
	StatusCode int
	Limit      int64
}

func (e *ResponseBodyTooLargeError) Error() string {
	return fmt.Sprintf("IO.NET response body exceeded %d-byte limit (status %d)", e.Limit, e.StatusCode)
}

// DefaultHTTPClient is the default HTTP client implementation
type DefaultHTTPClient struct {
	client *http.Client
}

// NewDefaultHTTPClient creates a new default HTTP client
func NewDefaultHTTPClient(timeout time.Duration) *DefaultHTTPClient {
	return &DefaultHTTPClient{
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Do executes an HTTP request
func (c *DefaultHTTPClient) Do(req *HTTPRequest) (*HTTPResponse, error) {
	requestContext := req.Context
	if requestContext == nil {
		requestContext = context.Background()
	}
	httpReq, err := http.NewRequestWithContext(requestContext, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyLimit := maxSuccessResponseBodyBytes
	if resp.StatusCode >= http.StatusBadRequest {
		bodyLimit = maxErrorResponseBodyBytes
	}
	bodyTruncated := false
	if resp.ContentLength > int64(bodyLimit) {
		if resp.StatusCode < http.StatusBadRequest {
			return nil, &ResponseBodyTooLargeError{
				StatusCode: resp.StatusCode,
				Limit:      int64(bodyLimit),
			}
		}
		bodyTruncated = true
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(bodyLimit)+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if len(body) > bodyLimit {
		if resp.StatusCode < http.StatusBadRequest {
			return nil, &ResponseBodyTooLargeError{
				StatusCode: resp.StatusCode,
				Limit:      int64(bodyLimit),
			}
		}
		body = body[:bodyLimit]
		bodyTruncated = true
	}

	// Convert headers
	headers := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	return &HTTPResponse{
		StatusCode:    resp.StatusCode,
		Headers:       headers,
		Body:          body,
		BodyTruncated: bodyTruncated,
	}, nil
}

// NewEnterpriseClient creates a new IO.NET API client targeting the enterprise API base URL.
func NewEnterpriseClient(apiKey string) *Client {
	return NewClientWithConfig(apiKey, DefaultEnterpriseBaseURL, nil)
}

// NewClient creates a new IO.NET API client targeting the public API base URL.
func NewClient(apiKey string) *Client {
	return NewClientWithConfig(apiKey, DefaultBaseURL, nil)
}

// NewClientWithConfig creates a new IO.NET API client with custom configuration
func NewClientWithConfig(apiKey, baseURL string, httpClient HTTPClient) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = NewDefaultHTTPClient(DefaultTimeout)
	}
	return &Client{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: httpClient,
		context:    context.Background(),
	}
}

// WithContext returns a shallow client clone whose requests use ctx. The
// original client remains safe for concurrent reuse.
func (c *Client) WithContext(ctx context.Context) *Client {
	if ctx == nil {
		ctx = context.Background()
	}
	clone := *c
	clone.context = ctx
	return &clone
}

// makeRequest performs an HTTP request and handles common response processing
func (c *Client) makeRequest(method, endpoint string, body interface{}) (*HTTPResponse, error) {
	var reqBody []byte
	var err error

	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	headers := map[string]string{
		"X-API-KEY":    c.APIKey,
		"Content-Type": "application/json",
	}

	requestContext := c.context
	if requestContext == nil {
		requestContext = context.Background()
	}
	req := &HTTPRequest{
		Context: requestContext,
		Method:  method,
		URL:     c.BaseURL + endpoint,
		Headers: headers,
		Body:    reqBody,
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("request failed: empty HTTP response")
	}
	if resp.StatusCode < http.StatusBadRequest && len(resp.Body) > maxSuccessResponseBodyBytes {
		return nil, &ResponseBodyTooLargeError{
			StatusCode: resp.StatusCode,
			Limit:      int64(maxSuccessResponseBodyBytes),
		}
	}

	// Handle API errors
	if resp.StatusCode >= http.StatusBadRequest {
		bodyTruncated := resp.BodyTruncated || len(resp.Body) > maxErrorResponseBodyBytes
		apiErr := APIError{
			Code:    resp.StatusCode,
			Message: fmt.Sprintf("API request failed with status %d", resp.StatusCode),
		}
		if bodyTruncated {
			apiErr.Details = fmt.Sprintf(
				"upstream error response body exceeded %d-byte limit",
				maxErrorResponseBodyBytes,
			)
		} else if len(resp.Body) > 0 {
			apiErr.Details = fmt.Sprintf("upstream error response body omitted (%d bytes)", len(resp.Body))
		}
		return nil, &apiErr
	}

	return resp, nil
}

// buildQueryParams builds query parameters for GET requests
func buildQueryParams(params map[string]interface{}) string {
	if len(params) == 0 {
		return ""
	}

	values := url.Values{}
	for key, value := range params {
		if value == nil {
			continue
		}
		switch v := value.(type) {
		case string:
			if v != "" {
				values.Add(key, v)
			}
		case int:
			if v != 0 {
				values.Add(key, strconv.Itoa(v))
			}
		case int64:
			if v != 0 {
				values.Add(key, strconv.FormatInt(v, 10))
			}
		case float64:
			if v != 0 {
				values.Add(key, strconv.FormatFloat(v, 'f', -1, 64))
			}
		case bool:
			values.Add(key, strconv.FormatBool(v))
		case time.Time:
			if !v.IsZero() {
				values.Add(key, v.Format(time.RFC3339))
			}
		case *time.Time:
			if v != nil && !v.IsZero() {
				values.Add(key, v.Format(time.RFC3339))
			}
		case []int:
			if len(v) > 0 {
				if encoded, err := json.Marshal(v); err == nil {
					values.Add(key, string(encoded))
				}
			}
		case []string:
			if len(v) > 0 {
				if encoded, err := json.Marshal(v); err == nil {
					values.Add(key, string(encoded))
				}
			}
		default:
			values.Add(key, fmt.Sprint(v))
		}
	}

	if len(values) > 0 {
		return "?" + values.Encode()
	}
	return ""
}
