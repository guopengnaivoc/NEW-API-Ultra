package common

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomEventRendersSSEBodyAndHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	event := CustomEvent{Data: "data: hello"}

	require.NoError(t, event.Render(recorder))

	assert.Equal(t, "data: hello\n\n", recorder.Body.String())
	assert.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
}
