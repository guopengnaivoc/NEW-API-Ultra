package perf_metrics_setting

import "github.com/QuantumNous/new-api/setting/config"

type PerfMetricsSetting struct {
	Enabled       bool   `json:"enabled"`
	FlushInterval int    `json:"flush_interval"`
	BucketTime    string `json:"bucket_time"`
	RetentionDays int    `json:"retention_days"`
}

var perfMetricsSetting = PerfMetricsSetting{
	Enabled:       true,
	FlushInterval: 5,
	BucketTime:    "hour",
	RetentionDays: 0,
}

func init() {
	config.GlobalConfig.Register("perf_metrics_setting", &perfMetricsSetting)
}

func GetSetting() PerfMetricsSetting {
	return *config.Snapshot[PerfMetricsSetting]("perf_metrics_setting")
}

func GetBucketSeconds() int64 {
	current := config.Snapshot[PerfMetricsSetting]("perf_metrics_setting")
	switch current.BucketTime {
	case "minute":
		return 60
	case "5min":
		return 300
	case "hour":
		return 3600
	default:
		return 3600
	}
}

func GetFlushIntervalMinutes() int {
	current := config.Snapshot[PerfMetricsSetting]("perf_metrics_setting")
	if current.FlushInterval < 1 {
		return 1
	}
	return current.FlushInterval
}
