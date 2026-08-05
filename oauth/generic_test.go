package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

const oversizedOAuthResponsePayload = 1 << 20

func TestGenericOAuthRejectsOversizedTokenResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + strings.Repeat("a", oversizedOAuthResponsePayload) + `"}`))
	}))
	t.Cleanup(server.Close)

	provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Name:          "Oversized Token Test",
		Slug:          "oversized-token-test",
		ClientId:      "client",
		ClientSecret:  "secret",
		TokenEndpoint: server.URL,
	})
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())

	token, err := provider.ExchangeToken(context.Background(), "code", ginContext)

	assert.Nil(t, token)
	assert.Error(t, err)
}

func TestGenericOAuthRejectsOversizedUserInfoResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"` + strings.Repeat("a", oversizedOAuthResponsePayload) + `"}`))
	}))
	t.Cleanup(server.Close)

	provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Name:             "Oversized User Info Test",
		Slug:             "oversized-user-info-test",
		UserInfoEndpoint: server.URL,
		UserIdField:      "sub",
	})

	user, err := provider.GetUserInfo(context.Background(), &OAuthToken{
		AccessToken: "access-token",
		TokenType:   "Bearer",
	})

	assert.Nil(t, user)
	assert.Error(t, err)
}
