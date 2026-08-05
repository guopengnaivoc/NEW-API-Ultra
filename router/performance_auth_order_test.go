package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayRoutesAuthenticateBeforeSystemPerformanceCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousPerformanceCheck := relaySystemPerformanceCheck
	t.Cleanup(func() {
		relaySystemPerformanceCheck = previousPerformanceCheck
	})

	performanceChecks := 0
	relaySystemPerformanceCheck = func() gin.HandlerFunc {
		return func(c *gin.Context) {
			performanceChecks++
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "performance probe ran before authentication",
			})
		}
	}

	engine := gin.New()
	SetRelayRouter(engine)
	testCases := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/pg/chat/completions"},
		{method: http.MethodPost, path: "/v1/chat/completions"},
		{method: http.MethodPost, path: "/v1/messages"},
		{method: http.MethodPost, path: "/v1/responses"},
		{method: http.MethodGet, path: "/v1/realtime"},
		{method: http.MethodPost, path: "/v1beta/models/gemini:test"},
		{method: http.MethodPost, path: "/suno/submit/music"},
		{method: http.MethodPost, path: "/suno/fetch"},
		{method: http.MethodPost, path: "/mj/submit/imagine"},
		{method: http.MethodGet, path: "/mj/image/missing"},
		{method: http.MethodPost, path: "/relay/mj/submit/imagine"},
		{method: http.MethodGet, path: "/relay/mj/image/missing"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			before := performanceChecks
			request := httptest.NewRequest(testCase.method, testCase.path, nil)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			assert.Equal(t, http.StatusUnauthorized, response.Code)
			body := response.Body.String()
			assert.NotContains(t, body, "performance probe")
			assert.NotContains(t, body, "overloaded")
			assert.NotContains(t, body, "system_")
			assert.Equal(t, before, performanceChecks, "performance probe ran for an unauthenticated request")
		})
	}

	require.Zero(t, performanceChecks)
}
