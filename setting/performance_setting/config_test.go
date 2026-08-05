package performance_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateAndSyncUsesPublishedPerformanceSnapshot(t *testing.T) {
	original, err := config.ConfigToMap(config.Snapshot[PerformanceSetting]("performance_setting"))
	require.NoError(t, err)
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("performance_setting", original)
		require.NoError(t, updateErr)
		require.True(t, updated)
		UpdateAndSync()
	})
	updated, err := config.GlobalConfig.Update("performance_setting", map[string]string{
		"disk_cache_enabled":       "true",
		"disk_cache_threshold_mb":  "23",
		"disk_cache_max_size_mb":   "456",
		"disk_cache_path":          "/tmp/newapi-performance-test",
		"monitor_enabled":          "false",
		"monitor_cpu_threshold":    "71",
		"monitor_memory_threshold": "72",
		"monitor_disk_threshold":   "73",
	})
	require.NoError(t, err)
	require.True(t, updated)

	UpdateAndSync()

	assert.Equal(t, common.DiskCacheConfig{
		Enabled:     true,
		ThresholdMB: 23,
		MaxSizeMB:   456,
		Path:        "/tmp/newapi-performance-test",
	}, common.GetDiskCacheConfig())
	assert.Equal(t, common.PerformanceMonitorConfig{
		Enabled:         false,
		CPUThreshold:    71,
		MemoryThreshold: 72,
		DiskThreshold:   73,
	}, common.GetPerformanceMonitorConfig())
	assert.Same(t, config.Snapshot[PerformanceSetting]("performance_setting"), GetPerformanceSetting())
}
