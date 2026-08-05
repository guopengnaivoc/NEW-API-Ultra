package middleware

import (
	"net"

	"github.com/gin-gonic/gin"
)

// ElectronStartupToken lets the desktop shell verify it is talking to the
// backend process it spawned rather than a local port squatter: the shell
// generates a random token, hands it to the child via ELECTRON_STARTUP_TOKEN,
// and only treats the server as ready once a response echoes that token
// (NA-ISSUE-0087). The token is disclosed to loopback peers only, so it never
// leaves the machine even when the server is bound to a public interface.
func ElectronStartupToken(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token != "" {
			if ip := net.ParseIP(c.RemoteIP()); ip != nil && ip.IsLoopback() {
				c.Header("X-Electron-Startup-Token", token)
			}
		}
		c.Next()
	}
}
