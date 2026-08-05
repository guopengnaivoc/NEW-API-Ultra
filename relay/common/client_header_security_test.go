package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSensitiveClientHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		header    string
		sensitive bool
	}{
		{name: "authorization", header: "Authorization", sensitive: true},
		{name: "case insensitive", header: "aUtHoRiZaTiOn", sensitive: true},
		{name: "trimmed", header: "  Cookie  ", sensitive: true},
		{name: "underscore api key", header: "X_API_KEY", sensitive: true},
		{name: "google api key", header: "x-goog-api-key", sensitive: true},
		{name: "midjourney secret", header: "MJ_API_SECRET", sensitive: true},
		{name: "auth session", header: "X_Auth_Session", sensitive: true},
		{name: "security proof", header: "X-Security-Proof", sensitive: true},
		{name: "turnstile proof", header: "X_Turnstile_Token", sensitive: true},
		{name: "websocket key", header: "Sec_WebSocket_Key", sensitive: true},
		{name: "websocket protocol", header: "Sec-WebSocket-Protocol", sensitive: true},
		{name: "safe trace", header: "X-Trace-Id", sensitive: false},
		{name: "safe originator", header: "Originator", sensitive: false},
		{name: "empty", header: "", sensitive: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.sensitive, IsSensitiveClientHeader(tt.header))
		})
	}
}
