package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func securityHeadersResponse(t *testing.T, mutateRequest func(*http.Request)) http.Header {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(SecurityHeaders())
	router.GET("/ping", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	request := httptest.NewRequest(http.MethodGet, "/ping", nil)
	if mutateRequest != nil {
		mutateRequest(request)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	return recorder.Header()
}

func TestSecurityHeadersDefaults(t *testing.T) {
	t.Setenv("SECURITY_HSTS_ENABLED", "")
	t.Setenv("SECURITY_CONTENT_SECURITY_POLICY", "")

	header := securityHeadersResponse(t, nil)

	assert.Equal(t, "nosniff", header.Get("X-Content-Type-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", header.Get("Referrer-Policy"))
	assert.Equal(t, "SAMEORIGIN", header.Get("X-Frame-Options"))
	assert.Equal(t, "camera=(), microphone=(), geolocation=()", header.Get("Permissions-Policy"))
	assert.Empty(t, header.Get("Strict-Transport-Security"), "HSTS must be opt-in")
	assert.Empty(t, header.Get("Content-Security-Policy"), "CSP must not be sent unless configured")
}

func TestSecurityHeadersHSTS(t *testing.T) {
	testCases := []struct {
		name           string
		envValue       string
		forwardedProto string
		useTLS         bool
		wantHSTS       string
	}{
		{
			name:           "disabled by default even over forwarded https",
			envValue:       "",
			forwardedProto: "https",
			wantHSTS:       "",
		},
		{
			name:     "enabled but plain http request",
			envValue: "true",
			wantHSTS: "",
		},
		{
			name:           "enabled with forwarded https",
			envValue:       "true",
			forwardedProto: "https",
			wantHSTS:       "max-age=31536000; includeSubDomains",
		},
		{
			name:           "enabled with forwarded https list uses first hop",
			envValue:       "true",
			forwardedProto: "https, http",
			wantHSTS:       "max-age=31536000; includeSubDomains",
		},
		{
			name:           "enabled with forwarded http",
			envValue:       "true",
			forwardedProto: "http",
			wantHSTS:       "",
		},
		{
			name:     "enabled with direct TLS",
			envValue: "true",
			useTLS:   true,
			wantHSTS: "max-age=31536000; includeSubDomains",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("SECURITY_HSTS_ENABLED", testCase.envValue)
			t.Setenv("SECURITY_CONTENT_SECURITY_POLICY", "")

			header := securityHeadersResponse(t, func(request *http.Request) {
				if testCase.forwardedProto != "" {
					request.Header.Set("X-Forwarded-Proto", testCase.forwardedProto)
				}
				if testCase.useTLS {
					request.TLS = &tls.ConnectionState{}
				}
			})

			assert.Equal(t, testCase.wantHSTS, header.Get("Strict-Transport-Security"))
		})
	}
}

func TestSecurityHeadersContentSecurityPolicy(t *testing.T) {
	t.Setenv("SECURITY_HSTS_ENABLED", "")
	t.Setenv("SECURITY_CONTENT_SECURITY_POLICY", "  default-src 'self'  ")

	header := securityHeadersResponse(t, nil)

	assert.Equal(t, "default-src 'self'", header.Get("Content-Security-Policy"),
		"configured CSP must be echoed verbatim after trimming")
}
