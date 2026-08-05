package service

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trackingResponseBody struct {
	reader    io.Reader
	closed    bool
	reads     int
	bytesRead int
}

func (body *trackingResponseBody) Read(p []byte) (int, error) {
	body.reads++
	n, err := body.reader.Read(p)
	body.bytesRead += n
	return n, err
}

func (body *trackingResponseBody) Close() error {
	body.closed = true
	return nil
}

type responseRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn responseRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func newTrackingResponseBody(content string) *trackingResponseBody {
	return &trackingResponseBody{reader: strings.NewReader(content)}
}

func TestReadServiceResponseBodyEnforcesDeclaredAndStreamedLimits(t *testing.T) {
	t.Run("accepts exact declared limit", func(t *testing.T) {
		body := newTrackingResponseBody("12345678")
		resp := &http.Response{
			Body:          body,
			ContentLength: 8,
		}

		got, err := readServiceResponseBody(resp, 8)

		require.NoError(t, err)
		assert.Equal(t, []byte("12345678"), got)
		assert.Positive(t, body.reads)
	})

	t.Run("rejects declared oversize before reading", func(t *testing.T) {
		body := newTrackingResponseBody("123456789")
		resp := &http.Response{
			Body:          body,
			ContentLength: 9,
		}

		got, err := readServiceResponseBody(resp, 8)

		require.ErrorIs(t, err, errServiceResponseTooLarge)
		assert.Nil(t, got)
		assert.Zero(t, body.reads)
	})

	t.Run("rejects false low declared length after limit plus one", func(t *testing.T) {
		const maxBytes int64 = 8
		body := newTrackingResponseBody(strings.Repeat("x", 64*1024))
		resp := &http.Response{
			Body:          body,
			ContentLength: 1,
		}

		got, err := readServiceResponseBody(resp, maxBytes)

		require.ErrorIs(t, err, errServiceResponseTooLarge)
		assert.Nil(t, got)
		assert.Positive(t, body.reads)
		assert.LessOrEqual(t, int64(body.bytesRead), maxBytes+1)
	})

	t.Run("rejects chunked oversize after limit plus one", func(t *testing.T) {
		const maxBytes int64 = 8
		body := newTrackingResponseBody(strings.Repeat("x", 64*1024))
		resp := &http.Response{
			Body:          body,
			ContentLength: -1,
		}

		got, err := readServiceResponseBody(resp, maxBytes)

		require.ErrorIs(t, err, errServiceResponseTooLarge)
		assert.Nil(t, got)
		assert.Positive(t, body.reads)
		assert.LessOrEqual(t, int64(body.bytesRead), maxBytes+1)
	})

	t.Run("propagates read failure", func(t *testing.T) {
		readErr := errors.New("read failed")
		resp := &http.Response{
			Body: &trackingResponseBody{reader: io.MultiReader(
				bytes.NewBufferString("ok"),
				errorReader{err: readErr},
			)},
			ContentLength: -1,
		}

		got, err := readServiceResponseBody(resp, 8)

		require.ErrorIs(t, err, readErr)
		assert.Nil(t, got)
	})

	t.Run("rejects invalid input", func(t *testing.T) {
		for _, testCase := range []struct {
			name     string
			resp     *http.Response
			maxBytes int64
		}{
			{name: "nil response", resp: nil, maxBytes: 1},
			{name: "nil body", resp: &http.Response{}, maxBytes: 1},
			{name: "zero limit", resp: &http.Response{Body: http.NoBody}, maxBytes: 0},
			{name: "negative limit", resp: &http.Response{Body: http.NoBody}, maxBytes: -1},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				got, err := readServiceResponseBody(testCase.resp, testCase.maxBytes)
				require.Error(t, err)
				assert.Nil(t, got)
			})
		}
	})
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}
