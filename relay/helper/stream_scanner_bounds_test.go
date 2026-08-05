package helper

import (
	"bufio"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withScannerBufferMB(t *testing.T, valueMB int) {
	t.Helper()
	previous := constant.StreamScannerMaxBufferMB
	constant.StreamScannerMaxBufferMB = valueMB
	t.Cleanup(func() { constant.StreamScannerMaxBufferMB = previous })
}

// The scanner buffer configuration contract: 16 MiB default, explicit values
// honored up to the 64 MiB hard cap, and zero/negative/overflowing settings
// clamped consistently instead of disabling the bound.
func TestGetScannerBufferSizeConfigurationContract(t *testing.T) {
	testCases := []struct {
		name         string
		configuredMB int
		expected     int
	}{
		{name: "zero falls back to the 16MiB default", configuredMB: 0, expected: DefaultMaxScannerBufferSize},
		{name: "negative falls back to the 16MiB default", configuredMB: -5, expected: DefaultMaxScannerBufferSize},
		{name: "explicit small value honored", configuredMB: 1, expected: 1 << 20},
		{name: "explicit value at the cap honored", configuredMB: MaxScannerBufferMB, expected: MaxScannerBufferMB << 20},
		{name: "value beyond the cap clamps to 64MiB", configuredMB: MaxScannerBufferMB + 1, expected: MaxScannerBufferMB << 20},
		{name: "huge value clamps instead of overflowing", configuredMB: 1 << 40, expected: MaxScannerBufferMB << 20},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			withScannerBufferMB(t, tc.configuredMB)
			assert.Equal(t, tc.expected, getScannerBufferSize())
		})
	}
	// The default itself is 16 MiB, not the old 128 MiB.
	assert.Equal(t, 16<<20, DefaultMaxScannerBufferSize)
}

// A line at exactly the configured bound scans; one byte beyond fails with
// bufio.ErrTooLong instead of growing without limit.
func TestNewStreamScannerEnforcesConfiguredLineBound(t *testing.T) {
	withScannerBufferMB(t, 1)

	// bufio.Scanner must also see the trailing delimiter within the buffer,
	// so the largest scannable line is bound-1 content bytes plus the newline.
	atLimit := strings.Repeat("x", (1<<20)-len("data: ")-1)
	scanner := NewStreamScanner(strings.NewReader("data: " + atLimit + "\n"))
	scanner.Split(bufio.ScanLines)
	require.True(t, scanner.Scan(), "a line at the configured bound must scan")
	require.NoError(t, scanner.Err())

	beyond := strings.Repeat("x", 1<<20)
	scanner = NewStreamScanner(strings.NewReader("data: " + beyond + "\n"))
	scanner.Split(bufio.ScanLines)
	assert.False(t, scanner.Scan(), "a line beyond the configured bound must be rejected")
	assert.ErrorIs(t, scanner.Err(), bufio.ErrTooLong)
}
