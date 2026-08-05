package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// ChannelKeySecurityProofRequired protects channel key disclosure with a
// one-time proof bound to the exact canonical channel ID.
func ChannelKeySecurityProofRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		target, err := service.CanonicalSecurityProofIDTarget(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "渠道ID格式错误",
			})
			c.Abort()
			return
		}
		if !RequireOneTimeSecurityProof(
			c,
			"channel.key.read",
			target,
			[]string{"2fa", "passkey"},
		) {
			return
		}
		c.Set("secure_verified", true)
		c.Next()
	}
}

// RequireOneTimeSecurityProof validates and consumes a security-sensitive
// operation proof bound to the exact browser session, operation and target.
func RequireOneTimeSecurityProof(c *gin.Context, requiredScope, requiredTarget string, allowedMethods []string) bool {
	identity, ok := GetSessionAuthIdentity(c)
	if !ok {
		securityProofError(c, "SECURITY_PROOF_INVALID", "安全验证状态无效")
		return false
	}
	raw := strings.TrimSpace(c.GetHeader("X-Security-Proof"))
	if raw == "" {
		securityProofError(c, "SECURITY_PROOF_REQUIRED", "需要安全验证")
		return false
	}
	if _, err := service.ConsumeOneTimeSecurityProof(raw, identity, requiredScope, requiredTarget, allowedMethods); err != nil {
		switch {
		case errors.Is(err, service.ErrAuthTokenExpired):
			securityProofError(c, "SECURITY_PROOF_EXPIRED", "安全验证已过期")
		case errors.Is(err, service.ErrProofScope):
			securityProofError(c, "SECURITY_PROOF_SCOPE_MISMATCH", "安全验证范围不匹配")
		case errors.Is(err, service.ErrProofTarget):
			securityProofError(c, "SECURITY_PROOF_TARGET_MISMATCH", "安全验证目标不匹配")
		case errors.Is(err, service.ErrProofMethod):
			securityProofError(c, "SECURITY_PROOF_METHOD_MISMATCH", "安全验证方式不匹配")
		case errors.Is(err, service.ErrProofConsumed):
			securityProofError(c, "SECURITY_PROOF_CONSUMED", "安全验证已使用")
		default:
			securityProofError(c, "SECURITY_PROOF_INVALID", "安全验证状态无效")
		}
		return false
	}
	return true
}

func securityProofError(c *gin.Context, code, message string) {
	c.JSON(http.StatusForbidden, gin.H{
		"success": false,
		"message": message,
		"code":    code,
	})
	c.Abort()
}
