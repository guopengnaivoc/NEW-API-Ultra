package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestCacheNoCacheForRootAndSensitivePaths(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		expectNoCT string
	}{
		{"root", "/", "no-cache"},
		{"index", "/index.html", "no-cache"},
		{"api", "/api/foo", "no-cache"},
		{"v1", "/v1/models", "no-cache"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(Cache())
			r.GET("/", func(c *gin.Context) {
				c.String(http.StatusOK, "ok")
			})
			r.Any("/api/foo", func(c *gin.Context) {
				c.String(http.StatusOK, "ok")
			})
			r.Any("/v1/models", func(c *gin.Context) {
				c.String(http.StatusOK, "ok")
			})

			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			responseRecorder := httptest.NewRecorder()
			r.ServeHTTP(responseRecorder, request)

			assert.Equal(t, tc.expectNoCT, responseRecorder.Result().Header.Get("Cache-Control"))
		})
	}
}

func TestCacheMaxAgeForStaticPath(t *testing.T) {
	r := gin.New()
	r.Use(Cache())
	r.GET("/assets/app.js", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	responseRecorder := httptest.NewRecorder()
	r.ServeHTTP(responseRecorder, request)

	assert.Equal(t, "max-age=604800", responseRecorder.Result().Header.Get("Cache-Control"))
}
