package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	// Wildcard origins must not be combined with credentials: browsers
	// reject "Access-Control-Allow-Origin: *" on credentialed responses,
	// and reflecting arbitrary origins with credentials would let any
	// site ride the user's session cookies. Relay and usage APIs
	// authenticate with the Authorization header, which does not need
	// credentials mode; the dashboard is served same-origin, where CORS
	// does not apply.
	config.AllowCredentials = false
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	// The Authorization header is never covered by the "*" wildcard, so
	// list the headers clients actually send.
	config.AllowHeaders = []string{
		"Origin", "Content-Type", "Accept", "Authorization",
		"X-Requested-With", "Cache-Control",
	}
	return cors.New(config)
}

func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		c.Next()
	}
}
