package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestTurnstileTokenSelectsHeaderBeforeLegacyQueryFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		header      string
		query       string
		wantToken   string
		wantPresent bool
	}{
		{
			name:        "header token",
			header:      "  header-token  ",
			wantToken:   "header-token",
			wantPresent: true,
		},
		{
			name:        "header takes precedence",
			header:      "header-token",
			query:       "query-token",
			wantToken:   "header-token",
			wantPresent: true,
		},
		{
			name:        "legacy query fallback",
			query:       "  query-token  ",
			wantToken:   "query-token",
			wantPresent: true,
		},
		{
			name: "empty values",
		},
		{
			name:   "whitespace values",
			header: "  ",
			query:  "\t",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/?turnstile="+url.QueryEscape(test.query), nil)
			context.Request.Header.Set("X-Turnstile-Token", test.header)

			token, present := selectTurnstileToken(context)

			assert.Equal(t, test.wantToken, token)
			assert.Equal(t, test.wantPresent, present)
		})
	}
}
