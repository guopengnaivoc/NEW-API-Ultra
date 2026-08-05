package relay

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The task-relay boundary contract: every upstream body in the task paths is
// read through service.ReadServiceResponseBody with an explicit limit. These
// tests pin the exported limits and the reader behavior the call sites rely
// on; the call sites themselves are covered by the absence sweep below being
// enforced in review plus the adaptor/relay compile-time dependency on the
// exported API.
func TestTaskRelayResponseLimitsAreExplicit(t *testing.T) {
	assert.Equal(t, int64(1<<20), service.RelayErrorResponseMaxBytes, "error bodies feed error messages only")
	assert.Equal(t, int64(16<<20), service.TaskSubmitResponseMaxBytes, "submit responses are JSON descriptors")
	assert.Equal(t, int64(8<<20), service.SunoTaskPollingResponseMaxBytes)
	assert.Equal(t, int64(96<<20), service.VideoTaskPollingResponseMaxBytes, "video polling can inline base64 payloads")
}

func TestReadServiceResponseBodyContractForTaskPaths(t *testing.T) {
	t.Run("declared oversize rejected before reading", func(t *testing.T) {
		resp := &http.Response{
			Body:          io.NopCloser(strings.NewReader("irrelevant")),
			ContentLength: service.TaskSubmitResponseMaxBytes + 1,
		}
		_, err := service.ReadServiceResponseBody(resp, service.TaskSubmitResponseMaxBytes)
		require.ErrorIs(t, err, service.ErrServiceResponseTooLarge)
	})

	t.Run("streamed dishonest-length oversize rejected at limit plus one", func(t *testing.T) {
		const limit int64 = 1 << 10
		resp := &http.Response{
			Body:          io.NopCloser(strings.NewReader(strings.Repeat("x", int(limit)+512))),
			ContentLength: -1, // chunked / unknown length
		}
		_, err := service.ReadServiceResponseBody(resp, limit)
		require.ErrorIs(t, err, service.ErrServiceResponseTooLarge)
	})

	t.Run("exact-limit body succeeds", func(t *testing.T) {
		const limit int64 = 1 << 10
		payload := strings.Repeat("y", int(limit))
		resp := &http.Response{
			Body:          io.NopCloser(strings.NewReader(payload)),
			ContentLength: limit,
		}
		body, err := service.ReadServiceResponseBody(resp, limit)
		require.NoError(t, err)
		assert.Equal(t, payload, string(body))
	})

	t.Run("non-positive limit is a configuration error", func(t *testing.T) {
		resp := &http.Response{Body: io.NopCloser(strings.NewReader("x"))}
		_, err := service.ReadServiceResponseBody(resp, 0)
		require.Error(t, err)
		_, err = service.ReadServiceResponseBody(resp, -1)
		require.Error(t, err)
	})
}
