package controller

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVideoProxyLogFieldEscapesAndBoundsTaskID(t *testing.T) {
	assert.Equal(
		t,
		`"task\r\nforged"`,
		videoProxyLogField("task\r\nforged"),
	)
	assert.Equal(
		t,
		`"`+strings.Repeat("a", 128)+`...[truncated]"`,
		videoProxyLogField(strings.Repeat("a", 129)),
	)
	assert.Equal(
		t,
		`"`+strings.Repeat("a", 127)+`...[truncated]"`,
		videoProxyLogField(strings.Repeat("a", 127)+"界"),
	)
}
