package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEscapeMultipartHeaderValueNeutralizesInjection(t *testing.T) {
	assert.Equal(t, "photo.png", EscapeMultipartHeaderValue("photo.png"))
	assert.Equal(t, `a\"b`, EscapeMultipartHeaderValue(`a"b`))
	assert.Equal(t, `a\\b`, EscapeMultipartHeaderValue(`a\b`))
	// CR/LF must vanish so a filename cannot start a new MIME header.
	assert.Equal(t, "evilContent-Type: text/html", EscapeMultipartHeaderValue("evil\r\nContent-Type: text/html"))
}
