package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	channelBalanceResponseMaxBytes  = 1 << 20
	channelModelResponseMaxBytes    = 4 << 20
	channelManagementRequestTimeout = 20 * time.Second
)

func newChannelManagementRequest(
	ctx context.Context,
	method string,
	requestURL string,
) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, errors.New("channel management request is invalid")
	}
	return request, nil
}

func readChannelManagementResponse(
	client *http.Client,
	request *http.Request,
	maxBytes int64,
) ([]byte, error) {
	if client == nil {
		return nil, errors.New("channel management HTTP client is required")
	}
	if request == nil {
		return nil, errors.New("channel management request is required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("channel management response limit must be positive")
	}

	ctx, cancel := context.WithTimeout(
		request.Context(),
		channelManagementRequestTimeout,
	)
	defer cancel()

	response, err := client.Do(request.WithContext(ctx))
	if err != nil {
		return nil, errors.New("channel management request failed")
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return nil, errors.New(
			"channel management response exceeds configured limit",
		)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, errors.New("channel management response read failed")
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New(
			"channel management response exceeds configured limit",
		)
	}
	return body, nil
}
