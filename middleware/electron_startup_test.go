package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func doElectronTokenRequest(t *testing.T, token, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ElectronStartupToken(token))
	router.GET("/api/status", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.RemoteAddr = remoteAddr
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	return recorder
}

func TestElectronStartupTokenSentToLoopbackPeer(t *testing.T) {
	recorder := doElectronTokenRequest(t, "secret-token", "127.0.0.1:52000")
	assert.Equal(t, "secret-token", recorder.Header().Get("X-Electron-Startup-Token"))

	recorder = doElectronTokenRequest(t, "secret-token", "[::1]:52000")
	assert.Equal(t, "secret-token", recorder.Header().Get("X-Electron-Startup-Token"))
}

func TestElectronStartupTokenWithheldFromRemotePeer(t *testing.T) {
	recorder := doElectronTokenRequest(t, "secret-token", "192.0.2.10:52000")
	assert.Empty(t, recorder.Header().Get("X-Electron-Startup-Token"))
}

func TestElectronStartupTokenEmptyTokenSendsNothing(t *testing.T) {
	recorder := doElectronTokenRequest(t, "", "127.0.0.1:52000")
	assert.Empty(t, recorder.Header().Get("X-Electron-Startup-Token"))
}
