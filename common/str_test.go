package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateInviteCodeReturnsExpectedLengthAndAlphabet(t *testing.T) {
	code, err := GenerateInviteCode(InviteCodeLength)
	require.NoError(t, err)
	assert.Len(t, code, InviteCodeLength)
	for _, c := range code {
		assert.True(t, strings.ContainsRune(InviteCodeAlphabet, c))
	}
}

func TestGenerateInviteCodeProducesMultipleDifferentValues(t *testing.T) {
	const sampleCount = 16
	values := make(map[string]struct{}, sampleCount)
	for range sampleCount {
		code, err := GenerateInviteCode(InviteCodeLength)
		require.NoError(t, err)
		assert.Len(t, code, InviteCodeLength)
		values[code] = struct{}{}
	}
	assert.Greater(t, len(values), 14)
}

func TestGenerateInviteCodeWithZeroLengthReturnsEmptyString(t *testing.T) {
	code, err := GenerateInviteCode(0)
	require.NoError(t, err)
	assert.Empty(t, code)
}
