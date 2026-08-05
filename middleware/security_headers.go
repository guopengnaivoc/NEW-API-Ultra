package middleware

import (
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders sets conservative security response headers on every response.
//
// X-Frame-Options is SAMEORIGIN rather than DENY because the dashboard supports
// admin-configured URL embeds (custom home page, about page, HTML content with
// iframes) that may legitimately point at the deployment's own origin.
//
// HSTS is opt-in via SECURITY_HSTS_ENABLED and only emitted when the request
// actually arrived over TLS (directly or per X-Forwarded-Proto), so plain-HTTP
// deployments are never poisoned with an unusable policy.
//
// No Content-Security-Policy is hardcoded: the SPA relies on inline styles and
// optional injected analytics scripts, so any default would break deployments.
// Operators can supply a policy via SECURITY_CONTENT_SECURITY_POLICY.
func SecurityHeaders() gin.HandlerFunc {
	hstsEnabled := common.GetEnvOrDefaultBool("SECURITY_HSTS_ENABLED", false)
	contentSecurityPolicy := strings.TrimSpace(os.Getenv("SECURITY_CONTENT_SECURITY_POLICY"))
	return func(c *gin.Context) {
		header := c.Writer.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		header.Set("X-Frame-Options", "SAMEORIGIN")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if hstsEnabled {
			secure := c.Request.TLS != nil
			if !secure {
				forwardedProto := c.Request.Header.Get("X-Forwarded-Proto")
				if commaIndex := strings.IndexByte(forwardedProto, ','); commaIndex >= 0 {
					forwardedProto = forwardedProto[:commaIndex]
				}
				secure = strings.EqualFold(strings.TrimSpace(forwardedProto), "https")
			}
			if secure {
				header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
		}
		if contentSecurityPolicy != "" {
			header.Set("Content-Security-Policy", contentSecurityPolicy)
		}
		c.Next()
	}
}
