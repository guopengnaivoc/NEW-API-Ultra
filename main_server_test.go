package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewHTTPServerAppliesSecurityTimeouts(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	server := newHTTPServer(":8080", handler)

	assert.Equal(t, 10*time.Second, server.ReadHeaderTimeout)
	assert.Equal(t, 5*time.Minute, server.ReadTimeout)
	assert.Equal(t, 3*time.Minute, server.IdleTimeout)
	assert.Equal(t, ":8080", server.Addr)
	assert.NotNil(t, server.Handler)
}
