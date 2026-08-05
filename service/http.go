package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/gin-gonic/gin"
)

type UpstreamHeaderPolicy uint8

const (
	UpstreamHeaderPolicyRelay UpstreamHeaderPolicy = iota
	UpstreamHeaderPolicyMedia
)

var relayUpstreamResponseHeaders = map[string]struct{}{
	"Content-Type":         {},
	"Content-Encoding":     {},
	"Content-Language":     {},
	"Retry-After":          {},
	"Request-Id":           {},
	"X-Request-Id":         {},
	"X-Reasoning-Included": {},
	"X-Codex-Turn-State":   {},
}

var mediaOnlyUpstreamResponseHeaders = map[string]struct{}{
	"Content-Length":      {},
	"Content-Disposition": {},
	"Etag":                {},
	"Last-Modified":       {},
}

func CloseResponseBodyGracefully(httpResponse *http.Response) {
	if httpResponse == nil || httpResponse.Body == nil {
		return
	}
	err := httpResponse.Body.Close()
	if err != nil {
		common.SysError("failed to close response body: " + err.Error())
	}
}

// ShouldCopyUpstreamHeader checks whether an upstream response header is safe
// and supported by the requested downstream response policy.
func ShouldCopyUpstreamHeader(c *gin.Context, source http.Header, name string, policy UpstreamHeaderPolicy) bool {
	if policy != UpstreamHeaderPolicyRelay && policy != UpstreamHeaderPolicyMedia {
		return false
	}

	canonicalName := http.CanonicalHeaderKey(strings.TrimSpace(name))
	if canonicalName == "" {
		return false
	}

	for _, connectionValue := range source.Values("Connection") {
		for _, token := range strings.Split(connectionValue, ",") {
			if strings.EqualFold(strings.TrimSpace(token), canonicalName) {
				return false
			}
		}
	}

	if strings.EqualFold(canonicalName, common.RequestIdKey) {
		if c != nil {
			if values := source.Values(name); len(values) > 0 {
				c.Set(common.UpstreamRequestIdKey, values[0])
			}
		}
		return false
	}

	if _, ok := relayUpstreamResponseHeaders[canonicalName]; ok {
		return true
	}
	if policy == UpstreamHeaderPolicyMedia {
		_, ok := mediaOnlyUpstreamResponseHeaders[canonicalName]
		return ok
	}
	return false
}

func CopyUpstreamResponseHeaders(c *gin.Context, source http.Header, policy UpstreamHeaderPolicy) {
	if c == nil || c.Writer == nil || source == nil {
		return
	}

	for name, values := range source {
		if !ShouldCopyUpstreamHeader(c, source, name, policy) {
			continue
		}
		canonicalName := http.CanonicalHeaderKey(strings.TrimSpace(name))
		c.Writer.Header().Del(canonicalName)
		for _, value := range values {
			c.Writer.Header().Add(canonicalName, value)
		}
	}
}

func IOCopyBytesGracefully(c *gin.Context, src *http.Response, data []byte) {
	if c == nil || c.Writer == nil {
		return
	}

	body := io.NopCloser(bytes.NewBuffer(data))

	// We shouldn't set the header before we parse the response body, because the parse part may fail.
	// And then we will have to send an error response, but in this case, the header has already been set.
	// So the httpClient will be confused by the response.
	// For example, Postman will report error, and we cannot check the response at all.
	if src != nil {
		CopyUpstreamResponseHeaders(c, src.Header, UpstreamHeaderPolicyRelay)
	}

	// set Content-Length header manually BEFORE calling WriteHeader
	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Write header with status code (this sends the headers)
	if src != nil {
		c.Writer.WriteHeader(src.StatusCode)
	} else {
		c.Writer.WriteHeader(http.StatusOK)
	}

	_, err := io.Copy(c.Writer, body)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("failed to copy response body: %s", err.Error()))
	}
	c.Writer.Flush()
}
