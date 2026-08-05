package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactAccessLogPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "no query", path: "/v1/models", want: "/v1/models"},
		{name: "ordinary query", path: "/v1/models?alt=sse", want: "/v1/models?alt=sse"},
		{name: "credential", path: "/v1/models?key=secret", want: "/v1/models?key=REDACTED"},
		{name: "credential and ordinary", path: "/v1/models?key=secret&alt=sse", want: "/v1/models?alt=sse&key=REDACTED"},
		{name: "duplicate credential", path: "/v1/models?key=one&key=two", want: "/v1/models?key=REDACTED"},
		{name: "malformed query", path: "/v1/models?key=secret&bad=%zz", want: "/v1/models"},
		{name: "stripe callback", path: "/api/stripe/webhook?signature=raw", want: "/api/stripe/webhook"},
		{name: "creem callback", path: "/api/creem/webhook?signature=raw", want: "/api/creem/webhook"},
		{name: "waffo callback", path: "/api/waffo/webhook?signature=raw", want: "/api/waffo/webhook"},
		{name: "waffo pancake callback", path: "/api/waffo-pancake/webhook/test?signature=raw", want: "/api/waffo-pancake/webhook/test"},
		{name: "epay wallet notify", path: "/api/user/epay/notify?sign=raw&name=customer", want: "/api/user/epay/notify"},
		{name: "epay subscription notify", path: "/api/subscription/epay/notify?sign=raw", want: "/api/subscription/epay/notify"},
		{name: "epay subscription return", path: "/api/subscription/epay/return?sign=raw", want: "/api/subscription/epay/return"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, redactAccessLogPath(test.path))
		})
	}
}

func TestSetUpLoggerDropsPaymentCallbackQueryWithoutMutatingRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer

	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	gin.DefaultWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		common.LogWriterMu.Unlock()
	})

	var observedSign string
	router := gin.New()
	SetUpLogger(router)
	router.GET("/api/user/epay/notify", func(c *gin.Context) {
		observedSign = c.Query("sign")
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/user/epay/notify?sign=epay-log-sentinel&name=customer-log-sentinel",
		nil,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, "epay-log-sentinel", observedSign)
	assert.Contains(t, output.String(), "/api/user/epay/notify")
	assert.NotContains(t, output.String(), "epay-log-sentinel")
	assert.NotContains(t, output.String(), "customer-log-sentinel")
}

func TestSetUpLoggerRedactsGeminiQueryKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousWriter := gin.DefaultWriter
	var output bytes.Buffer
	gin.DefaultWriter = &output
	t.Cleanup(func() { gin.DefaultWriter = previousWriter })

	router := gin.New()
	SetUpLogger(router)
	router.GET("/v1beta/models/:model", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1beta/models/gemini-pro:generateContent?key=AIza-secret&alt=sse",
		nil,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.NotContains(t, output.String(), "AIza-secret")
	assert.Contains(t, output.String(), "alt=sse")
	assert.Contains(t, output.String(), "key=REDACTED")
}

func TestGeminiQueryKeyRemainsAvailableToDownstreamMiddleware(t *testing.T) {
	previousWriter := gin.DefaultWriter
	var output bytes.Buffer
	gin.DefaultWriter = &output
	t.Cleanup(func() { gin.DefaultWriter = previousWriter })

	var observedKey string
	router := gin.New()
	SetUpLogger(router)
	router.GET("/v1beta/models/:model", func(c *gin.Context) {
		observedKey = c.Query("key")
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1beta/models/gemini-pro:generateContent?key=gemini-query-token&alt=sse",
		nil,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, "gemini-query-token", observedKey)
	assert.NotContains(t, output.String(), "gemini-query-token")
	assert.Contains(t, output.String(), "key=REDACTED")
}
