package minimax

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMinimaxChatResponseFiltersUpstreamHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": {"application/json"},
			"Set-Cookie":   {"session=attacker"},
			"X-Secret":     {"minimax-secret-sentinel"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl-test"}`)),
	}

	_, err := handleChatCompletionResponse(c, resp, nil)
	require.Nil(t, err)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, `{"id":"chatcmpl-test"}`, recorder.Body.String())
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.Empty(t, recorder.Header().Values("Set-Cookie"))
	assert.Empty(t, recorder.Header().Values("X-Secret"))
	assert.NotContains(t, recorder.Result().Header.Values("Set-Cookie"), "session=attacker")
	assert.NotContains(t, recorder.Result().Header.Values("X-Secret"), "minimax-secret-sentinel")
}
