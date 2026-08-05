package common

import "strings"

var sensitiveClientHeaderNames = map[string]struct{}{
	"api-key":                {},
	"authorization":          {},
	"cookie":                 {},
	"mj-api-secret":          {},
	"proxy-authorization":    {},
	"sec-websocket-key":      {},
	"sec-websocket-protocol": {},
	"x-api-key":              {},
	"x-auth-session":         {},
	"x-goog-api-key":         {},
	"x-security-proof":       {},
	"x-turnstile-token":      {},
}

// IsSensitiveClientHeader reports whether a client request header can carry a
// gateway credential or proof that must never be copied to an upstream.
//
// Some proxies normalize underscores to hyphens, so both spellings belong to
// the same security boundary.
func IsSensitiveClientHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "-")
	_, sensitive := sensitiveClientHeaderNames[name]
	return sensitive
}
