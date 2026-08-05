package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayPanicRecoverMasksPanicPayloadFromClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RelayPanicRecover())
	r.GET("/panic", func(c *gin.Context) {
		panic("TOPSECRET_INTERNAL_ERROR")
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	r.ServeHTTP(response, request)

	assert.Equal(t, http.StatusInternalServerError, response.Code)

	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, "new_api_panic", body.Error.Type)
	assert.Equal(t, "Panic detected. If this persists, please submit an issue at https://github.com/Calcium-Ion/new-api.", body.Error.Message)
	assert.False(t, strings.Contains(body.Error.Message, "TOPSECRET_INTERNAL_ERROR"))
}
