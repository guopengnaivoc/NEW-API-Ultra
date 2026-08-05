package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unsafeDiagnosticFileSource struct {
	baseFileSource
}

func (*unsafeDiagnosticFileSource) IsURL() bool {
	return true
}

func (*unsafeDiagnosticFileSource) GetIdentifier() string {
	panic("untrusted file source identifier must not be called")
}

func (*unsafeDiagnosticFileSource) GetRawData() string {
	return "TOPSECRETUNKNOWN"
}

func (*unsafeDiagnosticFileSource) ClearRawData() {}

func TestURLSourceIdentifierOmitsCredentialsQueryFragmentAndPlaintextPath(t *testing.T) {
	first := NewURLFileSource(
		"https://api-user:SUPERSECRETPASSWORD@storage.example.com/private/customer-123/image.png" +
			"?X-Amz-Signature=TOPSECRETSIGNATURE#TOPSECRETFRAGMENT",
	)
	second := NewURLFileSource(
		"https://storage.example.com/private/customer-123/image.png" +
			"?X-Amz-Signature=DIFFERENTSIGNATURE#DIFFERENTFRAGMENT",
	)

	firstIdentifier := first.GetIdentifier()
	secondIdentifier := second.GetIdentifier()

	assert.Equal(t, firstIdentifier, secondIdentifier)
	assert.Contains(t, firstIdentifier, "https://storage.example.com")
	for _, secret := range []string{
		"api-user",
		"SUPERSECRETPASSWORD",
		"private",
		"customer-123",
		"image.png",
		"X-Amz-Signature",
		"TOPSECRETSIGNATURE",
		"TOPSECRETFRAGMENT",
	} {
		assert.NotContains(t, firstIdentifier, secret)
	}

	differentPathIdentifier := NewURLFileSource(
		"https://storage.example.com/private/customer-456/image.png",
	).GetIdentifier()
	assert.NotEqual(t, firstIdentifier, differentPathIdentifier)
}

func TestURLSourceIdentifierFailsClosedForMalformedURL(t *testing.T) {
	rawURL := "https://storage.example.com/private/%zz?token=TOPSECRETTOKEN"
	source := NewURLFileSource(rawURL)

	identifier := source.GetIdentifier()

	require.NotEmpty(t, identifier)
	assert.Equal(t, identifier, source.GetIdentifier())
	assert.True(t, strings.HasPrefix(identifier, "url:"))
	assert.NotContains(t, identifier, "storage.example.com")
	assert.NotContains(t, identifier, "private")
	assert.NotContains(t, identifier, "TOPSECRETTOKEN")
}

func TestURLSourceIdentifierRejectsUnicodeAuthorityDelimiters(t *testing.T) {
	testCases := []struct {
		name      string
		delimiter string
	}{
		{name: "fullwidth solidus", delimiter: "／"},
		{name: "division slash", delimiter: "∕"},
		{name: "fraction slash", delimiter: "⁄"},
		{name: "fullwidth reverse solidus", delimiter: "＼"},
		{name: "fullwidth colon", delimiter: "："},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rawURL := "https://storage.example.com" + testCase.delimiter + "private" +
				testCase.delimiter + "TOPSECRET"

			identifier := NewURLFileSource(rawURL).GetIdentifier()
			lowerIdentifier := strings.ToLower(identifier)

			assert.NotContains(t, identifier, testCase.delimiter)
			assert.NotContains(t, lowerIdentifier, "private")
			assert.NotContains(t, lowerIdentifier, "topsecret")
			assert.NotContains(t, lowerIdentifier, "storage.example.com")
			assert.LessOrEqual(t, len(identifier), 384)
		})
	}
}

func TestURLSourceIdentifierRejectsUnicodeIDNButRetainsValidatedPunycode(t *testing.T) {
	unicodeIdentifier := NewURLFileSource(
		"https://例子.测试/private/TOPSECRET",
	).GetIdentifier()

	assert.NotContains(t, unicodeIdentifier, "例子")
	assert.NotContains(t, unicodeIdentifier, "测试")
	assert.NotContains(t, unicodeIdentifier, "private")
	assert.NotContains(t, unicodeIdentifier, "TOPSECRET")
	assert.LessOrEqual(t, len(unicodeIdentifier), 384)

	punycodeIdentifier := NewURLFileSource(
		"https://XN--FSQU00A.XN--0ZWM56D:443/private/TOPSECRET",
	).GetIdentifier()
	assert.Contains(t, punycodeIdentifier, "https://xn--fsqu00a.xn--0zwm56d:443")
	assert.NotContains(t, punycodeIdentifier, "private")
	assert.NotContains(t, punycodeIdentifier, "TOPSECRET")
	assert.LessOrEqual(t, len(punycodeIdentifier), 384)
}

func TestURLSourceIdentifierRejectsIPv6ZonesButRetainsUnzonedIPv6(t *testing.T) {
	zonedIdentifier := NewURLFileSource(
		"https://[fe80::1%25TOPSECRETZONE]:8443/private",
	).GetIdentifier()
	lowerZonedIdentifier := strings.ToLower(zonedIdentifier)

	assert.NotContains(t, lowerZonedIdentifier, "topsecretzone")
	assert.NotContains(t, lowerZonedIdentifier, "fe80::1")
	assert.NotContains(t, lowerZonedIdentifier, "private")
	assert.LessOrEqual(t, len(zonedIdentifier), 384)

	unzonedIdentifier := NewURLFileSource(
		"https://[2001:db8::1]:8443/private",
	).GetIdentifier()
	assert.Contains(t, unzonedIdentifier, "https://[2001:db8::1]:8443")
	assert.NotContains(t, unzonedIdentifier, "private")
	assert.LessOrEqual(t, len(unzonedIdentifier), 384)
}

func TestURLSourceIdentifierRejectsMalformedHostnameAndPort(t *testing.T) {
	testCases := []struct {
		name   string
		rawURL string
	}{
		{name: "non-numeric port", rawURL: "https://storage.example.com:TOPSECRET/private"},
		{name: "empty port", rawURL: "https://storage.example.com:/private"},
		{name: "out of range port", rawURL: "https://storage.example.com:65536/private"},
		{name: "zero port", rawURL: "https://storage.example.com:0/private"},
		{name: "invalid DNS character", rawURL: "https://storage_secret.example.com/private"},
		{name: "invalid DNS label boundary", rawURL: "https://-storage.example.com/private"},
		{
			name:   "oversized DNS label",
			rawURL: "https://" + strings.Repeat("a", 64) + ".example.com/private",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			identifier := NewURLFileSource(testCase.rawURL).GetIdentifier()

			assert.NotContains(t, identifier, "storage")
			assert.NotContains(t, identifier, "example.com")
			assert.NotContains(t, identifier, "TOPSECRET")
			assert.NotContains(t, identifier, "private")
			assert.LessOrEqual(t, len(identifier), 384)
		})
	}
}

func TestURLSourceIdentifierRejectsInvalidSchemes(t *testing.T) {
	testCases := []struct {
		name   string
		rawURL string
	}{
		{name: "file scheme", rawURL: "file://example.com/public/image.png"},
		{name: "gopher scheme", rawURL: "gopher://storage.example.com/resource"},
		{name: "ftp scheme", rawURL: "ftp://storage.example.com/resource"},
		{name: "mailto pseudo-url", rawURL: "mailto:alice@example.com"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			identifier := NewURLFileSource(testCase.rawURL).GetIdentifier()

			assert.NotContains(t, identifier, "example.com")
			assert.NotContains(t, identifier, "storage.example.com")
			assert.NotContains(t, identifier, "resource")
			assert.NotContains(t, identifier, "alice")
			assert.LessOrEqual(t, len(identifier), 384)
			assert.True(t, strings.HasPrefix(identifier, "url:invalid"))
		})
	}
}

func TestURLSourceIdentifierNormalizesValidatedASCIIAuthorityAndBoundsOutput(t *testing.T) {
	validIdentifier := NewURLFileSource(
		"HTTPS://STORAGE.EXAMPLE.COM.:00080/private/TOPSECRET",
	).GetIdentifier()

	assert.Contains(t, validIdentifier, "https://storage.example.com:80")
	assert.NotContains(t, validIdentifier, "private")
	assert.NotContains(t, validIdentifier, "TOPSECRET")
	assert.LessOrEqual(t, len(validIdentifier), 384)

	oversizedIdentifier := NewURLFileSource(
		"https://" + strings.Repeat("a", 4096) + "/private/TOPSECRET",
	).GetIdentifier()
	assert.NotContains(t, oversizedIdentifier, strings.Repeat("a", 64))
	assert.NotContains(t, oversizedIdentifier, "private")
	assert.NotContains(t, oversizedIdentifier, "TOPSECRET")
	assert.LessOrEqual(t, len(oversizedIdentifier), 384)
}

func TestBase64SourceIdentifierUsesLengthAndCompleteContentDigest(t *testing.T) {
	sharedPrefix := strings.Repeat("TOPSECRETPREFIX", 8)
	firstData := sharedPrefix + "FIRSTTAIL"
	secondData := sharedPrefix + "SECONDTAI"
	require.Equal(t, len(firstData), len(secondData))

	firstIdentifier := NewBase64FileSource(firstData, "application/octet-stream").GetIdentifier()
	repeatedIdentifier := NewBase64FileSource(firstData, "application/octet-stream").GetIdentifier()
	secondIdentifier := NewBase64FileSource(secondData, "application/octet-stream").GetIdentifier()

	assert.Equal(t, firstIdentifier, repeatedIdentifier)
	assert.NotEqual(t, firstIdentifier, secondIdentifier)
	assert.Contains(t, firstIdentifier, "len=")
	assert.NotContains(t, firstIdentifier, "TOPSECRETPREFIX")
	assert.NotContains(t, firstIdentifier, "FIRSTTAIL")
}

func TestFileSourceErrorFailsClosedForUnknownImplementations(t *testing.T) {
	fileSourceError := NewFileSourceError(
		FileSourceErrorCategoryDownloadFailed,
		&unsafeDiagnosticFileSource{},
	)

	assert.Equal(t, FileSourceErrorCategoryDownloadFailed, fileSourceError.Category())
	assert.Equal(t, "file:unknown", fileSourceError.Identifier())
	assert.Equal(t, "file source download failed: file:unknown", fileSourceError.Error())
	assert.NotContains(t, fileSourceError.Error(), "TOPSECRETUNKNOWN")
}
