package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func restoreGroupRatioSnapshot(t *testing.T) {
	t.Helper()
	original, err := config.ConfigToMap(config.Snapshot[GroupRatioSetting]("group_ratio_setting"))
	require.NoError(t, err)
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("group_ratio_setting", original)
		require.NoError(t, updateErr)
		require.True(t, updated)
	})
}

func TestGroupRatioSnapshotConvergenceLegacyUpdate(t *testing.T) {
	restoreGroupRatioSnapshot(t)
	updated, err := config.GlobalConfig.Update("group_ratio_setting", map[string]string{
		"group_ratio": `{"default":1,"before":2}`,
	})
	require.NoError(t, err)
	require.True(t, updated)
	before := config.Snapshot[GroupRatioSetting]("group_ratio_setting")

	require.NoError(t, UpdateGroupRatioByJSONString(`{"default":1,"legacy":0.5}`))

	after := config.Snapshot[GroupRatioSetting]("group_ratio_setting")
	assert.NotSame(t, before, after)
	assert.Equal(t, float64(2), valueFromGroupRatio(t, before, "before"))
	_, oldHasLegacy := before.GroupRatio.Get("legacy")
	assert.False(t, oldHasLegacy)
	assert.Equal(t, 0.5, GetGroupRatio("legacy"))
	assert.Equal(t, map[string]float64{"default": 1, "legacy": 0.5}, GetGroupRatioCopy())
	assert.Equal(t, 0.5, valueFromGroupRatio(t, after, "legacy"))
}

func TestGroupRatioSnapshotConvergenceLayeredUpdate(t *testing.T) {
	restoreGroupRatioSnapshot(t)

	updated, err := config.GlobalConfig.Update("group_ratio_setting", map[string]string{
		"group_ratio": `{"default":1,"layered":0}`,
	})

	require.NoError(t, err)
	require.True(t, updated)
	assert.True(t, ContainsGroupRatio("layered"))
	assert.Equal(t, float64(0), GetGroupRatio("layered"))
	assert.JSONEq(t, `{"default":1,"layered":0}`, GroupRatio2JSONString())
}

func TestGroupRatioSnapshotConvergenceGroupGroupRatio(t *testing.T) {
	restoreGroupRatioSnapshot(t)
	updated, err := config.GlobalConfig.Update("group_ratio_setting", map[string]string{
		"group_group_ratio": `{"before":{"target":0.7}}`,
	})
	require.NoError(t, err)
	require.True(t, updated)
	before := config.Snapshot[GroupRatioSetting]("group_ratio_setting")

	require.NoError(t, UpdateGroupGroupRatioByJSONString(`{"legacy":{"target":0.8}}`))

	after := config.Snapshot[GroupRatioSetting]("group_ratio_setting")
	assert.NotSame(t, before, after)
	beforeRatio, beforeOK := before.GroupGroupRatio.Get("before")
	require.True(t, beforeOK)
	assert.Equal(t, 0.7, beforeRatio["target"])
	ratio, ok := GetGroupGroupRatio("legacy", "target")
	require.True(t, ok)
	assert.Equal(t, 0.8, ratio)
	assert.JSONEq(t, `{"legacy":{"target":0.8}}`, GroupGroupRatio2JSONString())
}

func TestGroupRatioSnapshotConvergenceNilSpecialMapIsDerivedPurely(t *testing.T) {
	restoreGroupRatioSnapshot(t)
	updated, err := config.GlobalConfig.Update("group_ratio_setting", map[string]string{
		"group_special_usable_group": "null",
	})
	require.NoError(t, err)
	require.True(t, updated)
	published := config.Snapshot[GroupRatioSetting]("group_ratio_setting")
	require.Nil(t, published.GroupSpecialUsableGroup)

	derived := GetGroupRatioSetting()

	assert.NotSame(t, published, derived)
	assert.Same(t, published.GroupRatio, derived.GroupRatio)
	assert.Same(t, published.GroupGroupRatio, derived.GroupGroupRatio)
	require.NotNil(t, derived.GroupSpecialUsableGroup)
	assert.Empty(t, derived.GroupSpecialUsableGroup.ReadAll())
	assert.Nil(t, published.GroupSpecialUsableGroup)
}

func valueFromGroupRatio(t *testing.T, setting *GroupRatioSetting, name string) float64 {
	t.Helper()
	value, ok := setting.GroupRatio.Get(name)
	require.True(t, ok)
	return value
}
