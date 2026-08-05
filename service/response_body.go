package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// MidjourneyResponseMaxBytes is shared by relay dispatches and scheduled polling.
const MidjourneyResponseMaxBytes int64 = 4 << 20

const (
	relayErrorResponseMaxBytes    int64 = 1 << 20
	codexOAuthResponseMaxBytes    int64 = 1 << 20
	codexMetadataResponseMaxBytes int64 = 4 << 20

	// RelayErrorResponseMaxBytes bounds non-200 upstream bodies that are only
	// read to build an error message.
	RelayErrorResponseMaxBytes int64 = relayErrorResponseMaxBytes
	// TaskSubmitResponseMaxBytes bounds task submission responses: JSON task
	// descriptors, sometimes with inline base64 previews.
	TaskSubmitResponseMaxBytes int64 = 16 << 20
	// SunoTaskPollingResponseMaxBytes bounds Suno polling responses (JSON with
	// audio metadata, no inline media).
	SunoTaskPollingResponseMaxBytes int64 = 8 << 20
	// VideoTaskPollingResponseMaxBytes bounds video task polling/fetch
	// responses, which can inline base64 video payloads.
	VideoTaskPollingResponseMaxBytes int64 = 96 << 20

	sunoTaskPollingResponseMaxBytes  = SunoTaskPollingResponseMaxBytes
	videoTaskPollingResponseMaxBytes = VideoTaskPollingResponseMaxBytes
)

var errServiceResponseTooLarge = errors.New("service response body exceeds limit")

// ErrServiceResponseTooLarge reports that an upstream body exceeded the
// declared or streamed limit; callers branch on it to shape user-facing
// messages without embedding upstream content.
var ErrServiceResponseTooLarge = errServiceResponseTooLarge

// ReadServiceResponseBody reads an upstream response under an explicit limit.
// It rejects a declared Content-Length beyond the limit before reading, and
// reads at most limit+1 bytes so a stream with an unknown or dishonest length
// is also rejected rather than truncated silently.
func ReadServiceResponseBody(resp *http.Response, maxBytes int64) ([]byte, error) {
	return readServiceResponseBody(resp, maxBytes)
}

// ReadMidjourneyResponseBody applies the shared Midjourney JSON response
// boundary for both relay dispatches and scheduled polling.
func ReadMidjourneyResponseBody(resp *http.Response) ([]byte, error) {
	return readServiceResponseBody(resp, MidjourneyResponseMaxBytes)
}

func readServiceResponseBody(resp *http.Response, maxBytes int64) ([]byte, error) {
	if resp == nil {
		return nil, errors.New("nil service response")
	}
	if resp.Body == nil {
		return nil, errors.New("nil service response body")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("invalid service response limit: %d", maxBytes)
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf(
			"%w: declared=%d limit=%d",
			errServiceResponseTooLarge,
			resp.ContentLength,
			maxBytes,
		)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf(
			"%w: limit=%d",
			errServiceResponseTooLarge,
			maxBytes,
		)
	}
	return body, nil
}
