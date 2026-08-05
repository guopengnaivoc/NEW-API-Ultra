package common

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDataEncryptionKey(fill byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(fill), 32)))
}

func configureDataEncryptionForTest(
	t *testing.T,
	keys string,
	activeKeyID string,
	enabled string,
) {
	t.Helper()
	t.Cleanup(func() {
		require.NoError(t, InitDataEncryption())
	})
	t.Setenv("DATA_ENCRYPTION_KEYS", keys)
	t.Setenv("DATA_ENCRYPTION_ACTIVE_KEY_ID", activeKeyID)
	t.Setenv("DATA_ENCRYPTION_ENABLE", enabled)
	require.NoError(t, InitDataEncryption())
}

func TestDataEncryptionEnvelopeRandomizesAndRoundTrips(t *testing.T) {
	configureDataEncryptionForTest(
		t,
		"k1="+testDataEncryptionKey('a'),
		"k1",
		"true",
	)

	first, err := SealDataEncryptionValue("custom_oauth_providers:client_secret", "oauth-secret")
	require.NoError(t, err)
	second, err := SealDataEncryptionValue("custom_oauth_providers:client_secret", "oauth-secret")
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
	assert.True(t, IsDataEncryptionEnvelope(first))
	assert.NotContains(t, first, "oauth-secret")

	plaintext, info, err := OpenDataEncryptionValue(
		"custom_oauth_providers:client_secret",
		first,
	)
	require.NoError(t, err)
	assert.Equal(t, "oauth-secret", plaintext)
	assert.True(t, info.Encrypted)
	assert.Equal(t, "k1", info.KeyID)
}

func TestDataEncryptionEnvelopeFailsClosedOnTamperDomainAndUnknownKey(t *testing.T) {
	configureDataEncryptionForTest(
		t,
		"k1="+testDataEncryptionKey('a'),
		"k1",
		"true",
	)
	envelope, err := SealDataEncryptionValue("custom_oauth_providers:client_secret", "oauth-secret")
	require.NoError(t, err)

	parts := strings.Split(envelope, ":")
	require.Len(t, parts, 5)
	payload, err := base64.RawURLEncoding.DecodeString(parts[4])
	require.NoError(t, err)
	payload[len(payload)-1] ^= 1
	parts[4] = base64.RawURLEncoding.EncodeToString(payload)
	tampered := strings.Join(parts, ":")

	_, _, err = OpenDataEncryptionValue("custom_oauth_providers:client_secret", tampered)
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), "oauth-secret")

	_, _, err = OpenDataEncryptionValue("channels:key", envelope)
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), envelope)

	configureDataEncryptionForTest(
		t,
		"k2="+testDataEncryptionKey('b'),
		"k2",
		"true",
	)
	_, _, err = OpenDataEncryptionValue("custom_oauth_providers:client_secret", envelope)
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), envelope)
}

func TestDataEncryptionRewrapChangesOnlyWrappedKey(t *testing.T) {
	configureDataEncryptionForTest(
		t,
		"old="+testDataEncryptionKey('a'),
		"old",
		"true",
	)
	original, err := SealDataEncryptionValue("custom_oauth_providers:client_secret", "oauth-secret")
	require.NoError(t, err)
	originalParts := strings.Split(original, ":")
	require.Len(t, originalParts, 5)

	configureDataEncryptionForTest(
		t,
		"old="+testDataEncryptionKey('a')+",new="+testDataEncryptionKey('b'),
		"new",
		"true",
	)
	rewrapped, changed, err := RewrapDataEncryptionValue(
		"custom_oauth_providers:client_secret",
		original,
	)
	require.NoError(t, err)
	assert.True(t, changed)

	rewrappedParts := strings.Split(rewrapped, ":")
	require.Len(t, rewrappedParts, 5)
	assert.Equal(t, "new", rewrappedParts[2])
	assert.NotEqual(t, originalParts[3], rewrappedParts[3])
	assert.Equal(t, originalParts[4], rewrappedParts[4])

	plaintext, info, err := OpenDataEncryptionValue(
		"custom_oauth_providers:client_secret",
		rewrapped,
	)
	require.NoError(t, err)
	assert.Equal(t, "oauth-secret", plaintext)
	assert.Equal(t, "new", info.KeyID)
}

func TestDataEncryptionPreparationGateKeepsLegacyWrites(t *testing.T) {
	configureDataEncryptionForTest(t, "", "", "false")

	stored, err := SealDataEncryptionValue("custom_oauth_providers:client_secret", "oauth-secret")
	require.NoError(t, err)
	assert.Equal(t, "oauth-secret", stored)

	plaintext, info, err := OpenDataEncryptionValue(
		"custom_oauth_providers:client_secret",
		stored,
	)
	require.NoError(t, err)
	assert.Equal(t, "oauth-secret", plaintext)
	assert.False(t, info.Encrypted)
}

func TestDataEncryptionRuntimeReadRejectsLegacyPlaintextWhenEnforced(t *testing.T) {
	configureDataEncryptionForTest(
		t,
		"k1="+testDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const (
		domain = "custom_oauth_providers:client_secret"
		secret = "runtime-legacy-secret-must-not-leak"
	)

	plaintext, info, err := OpenDataEncryptionValue(domain, secret)

	require.Error(t, err)
	assert.Empty(t, plaintext)
	assert.False(t, info.Encrypted)
	assert.Contains(t, err.Error(), domain)
	assert.NotContains(t, err.Error(), secret)
}

func TestDataEncryptionMigrationReaderAcceptsLegacyPlaintextWithKeyring(
	t *testing.T,
) {
	configureDataEncryptionForTest(
		t,
		"k1="+testDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const (
		domain = "custom_oauth_providers:client_secret"
		secret = "legacy-secret-for-migration"
	)

	plaintext, info, err := OpenLegacyDataEncryptionValueForMigration(
		domain,
		secret,
	)

	require.NoError(t, err)
	assert.Equal(t, secret, plaintext)
	assert.False(t, info.Encrypted)
	assert.Empty(t, info.KeyID)
}

func TestDataEncryptionMigrationReaderClassifiesPlaintextBeforeRequiringKey(
	t *testing.T,
) {
	configureDataEncryptionForTest(t, "", "", "true")
	const (
		domain    = "users:setting"
		plaintext = `{"language":"fr"}`
	)

	value, info, err := OpenLegacyDataEncryptionValueForMigration(
		domain,
		plaintext,
	)

	require.NoError(t, err)
	assert.Equal(t, plaintext, value)
	assert.False(t, info.Encrypted)
	assert.Empty(t, info.KeyID)
}

func TestDataEncryptionEnforcementRejectsProtectedWriteWithoutKeyring(t *testing.T) {
	configureDataEncryptionForTest(t, "", "", "true")

	_, err := SealDataEncryptionValue("custom_oauth_providers:client_secret", "oauth-secret")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "oauth-secret")
}

func TestInitDataEncryptionRejectsUnsafeConfiguration(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, InitDataEncryption())
	})
	t.Setenv("DATA_ENCRYPTION_KEYS", "unsafe:id="+testDataEncryptionKey('a'))
	t.Setenv("DATA_ENCRYPTION_ACTIVE_KEY_ID", "unsafe:id")
	t.Setenv("DATA_ENCRYPTION_ENABLE", "true")

	err := InitDataEncryption()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key identifier")
}
