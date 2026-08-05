package router

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRelayTokenRoutesExposeRotationButNoCredentialRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	assert.Contains(t, routes, "POST /api/token/:id/rotate")
	assert.NotContains(t, routes, "POST /api/token/:id/key")
	assert.NotContains(t, routes, "POST /api/token/batch/keys")
}
