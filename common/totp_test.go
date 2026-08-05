package common

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateBackupCodesCountAndCharset(t *testing.T) {
	codes, err := GenerateBackupCodes()
	require.NoError(t, err)
	require.Len(t, codes, BackupCodeCount)

	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		require.Regexp(t, `^[A-Z0-9]{4}-[A-Z0-9]{4}$`, code)
		assert.False(t, seen[code], "backup codes must not repeat within one batch: %s", code)
		seen[code] = true
		assert.True(t, ValidateBackupCode(code))
	}
}

func TestGenerateQRCodeDataEscapesReservedCharacters(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	username := "evil?user&x=1#frag"

	raw := GenerateQRCodeData(secret, username)
	parsed, err := url.Parse(raw)
	require.NoError(t, err)

	require.Equal(t, "otpauth", parsed.Scheme)
	require.Equal(t, "totp", parsed.Host)

	query := parsed.Query()
	assert.Equal(t, secret, query.Get("secret"))
	assert.Equal(t, Get2FAIssuer(), query.Get("issuer"))
	assert.Equal(t, "6", query.Get("digits"))
	assert.Equal(t, "30", query.Get("period"))

	// The user-controlled username must stay inside the escaped label and
	// must not smuggle extra query keys into the URI.
	assert.NotContains(t, parsed.RawPath, "#")
	assert.Empty(t, parsed.Fragment)
	for key := range query {
		assert.Contains(t, []string{"secret", "issuer", "digits", "period"}, key)
	}

	label, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	require.NoError(t, err)
	assert.Contains(t, label, username)
}
