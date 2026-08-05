package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func restoreMonitorSnapshot(t *testing.T) {
	t.Helper()
	original, err := config.ConfigToMap(config.Snapshot[MonitorSetting]("monitor_setting"))
	require.NoError(t, err)
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("monitor_setting", original)
		require.NoError(t, updateErr)
		require.True(t, updated)
	})
}

func TestGetMonitorSettingDerivedEnvDoesNotMutatePublishedSnapshot(t *testing.T) {
	restoreMonitorSnapshot(t)

	t.Setenv("CHANNEL_TEST_ENABLED", "false")
	t.Setenv("CHANNEL_TEST_FREQUENCY", "5")
	updated, err := config.GlobalConfig.Update("monitor_setting", map[string]string{
		"auto_test_channel_enabled": "true",
		"auto_test_channel_minutes": "20",
		"channel_test_mode":         ChannelTestModePassiveRecovery,
	})
	require.NoError(t, err)
	require.True(t, updated)
	published := config.Snapshot[MonitorSetting]("monitor_setting")

	setting := GetMonitorSetting()

	require.NotNil(t, setting)
	assert.False(t, setting.AutoTestChannelEnabled)
	assert.Equal(t, float64(5), setting.AutoTestChannelMinutes)
	assert.Equal(t, ChannelTestModeScheduledAll, setting.ChannelTestMode)
	assert.True(t, published.AutoTestChannelEnabled)
	assert.Equal(t, float64(20), published.AutoTestChannelMinutes)
	assert.Equal(t, ChannelTestModePassiveRecovery, published.ChannelTestMode)
}

func TestGetMonitorSettingDerivedEnvCanEnablePublishedDisabledConfig(t *testing.T) {
	restoreMonitorSnapshot(t)

	t.Setenv("CHANNEL_TEST_ENABLED", "true")
	updated, err := config.GlobalConfig.Update("monitor_setting", map[string]string{
		"auto_test_channel_enabled": "false",
		"auto_test_channel_minutes": "12",
	})
	require.NoError(t, err)
	require.True(t, updated)
	published := config.Snapshot[MonitorSetting]("monitor_setting")

	setting := GetMonitorSetting()

	require.NotNil(t, setting)
	assert.True(t, setting.AutoTestChannelEnabled)
	assert.Equal(t, float64(12), setting.AutoTestChannelMinutes)
	assert.False(t, published.AutoTestChannelEnabled)
	assert.Equal(t, float64(12), published.AutoTestChannelMinutes)
}
