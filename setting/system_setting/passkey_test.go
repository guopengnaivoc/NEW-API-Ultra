package system_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPasskeySettingsDerivedDefaultsDoNotMutatePublishedSnapshot(t *testing.T) {
	original, err := config.ConfigToMap(config.Snapshot[PasskeySettings]("passkey"))
	require.NoError(t, err)
	originalServerAddress := ServerAddress
	t.Cleanup(func() {
		ServerAddress = originalServerAddress
		updated, updateErr := config.GlobalConfig.Update("passkey", original)
		require.NoError(t, updateErr)
		require.True(t, updated)
	})
	updated, err := config.GlobalConfig.Update("passkey", map[string]string{
		"enabled": "true",
		"rp_id":   "",
		"origins": "",
	})
	require.NoError(t, err)
	require.True(t, updated)
	ServerAddress = "https://example.com"
	published := config.Snapshot[PasskeySettings]("passkey")

	derived := GetPasskeySettings()

	assert.NotSame(t, published, derived)
	assert.True(t, derived.Enabled)
	assert.Equal(t, "example.com", derived.RPID)
	assert.Equal(t, ServerAddress, derived.Origins)
	assert.Empty(t, published.RPID)
	assert.Empty(t, published.Origins)
}
