package middleware

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckSystemPerformanceDoesNotExposeResourceMeasurements(t *testing.T) {
	previousConfig := getSystemPerformanceMonitorConfig
	previousStatus := getCurrentSystemStatus
	t.Cleanup(func() {
		getSystemPerformanceMonitorConfig = previousConfig
		getCurrentSystemStatus = previousStatus
	})

	getSystemPerformanceMonitorConfig = func() common.PerformanceMonitorConfig {
		return common.PerformanceMonitorConfig{
			Enabled:         true,
			CPUThreshold:    90,
			MemoryThreshold: 80,
			DiskThreshold:   70,
		}
	}

	testCases := []struct {
		name   string
		status common.SystemStatus
		code   string
	}{
		{name: "cpu", status: common.SystemStatus{CPUUsage: 91.25}, code: "system_cpu_overloaded"},
		{name: "memory", status: common.SystemStatus{MemoryUsage: 81.5}, code: "system_memory_overloaded"},
		{name: "disk", status: common.SystemStatus{DiskUsage: 71.75}, code: "system_disk_overloaded"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			getCurrentSystemStatus = func() common.SystemStatus {
				return testCase.status
			}

			err := checkSystemPerformance()
			require.NotNil(t, err)
			assert.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
			assert.Equal(t, testCase.code, string(err.GetErrorCode()))
			assert.Equal(t, "system resources are currently overloaded", err.Error())
			assert.NotContains(t, err.Error(), "current:")
			assert.NotContains(t, err.Error(), "threshold:")
			assert.NotContains(t, err.Error(), "%")
		})
	}
}
