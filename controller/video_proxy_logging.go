package controller

import (
	"context"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/logger"
)

const (
	videoProxyLogFieldMaxBytes    = 128
	videoProxyLogTruncationMarker = "...[truncated]"
)

func videoProxyLogField(value string) string {
	if len(value) <= videoProxyLogFieldMaxBytes {
		return strconv.QuoteToASCII(value)
	}
	end := videoProxyLogFieldMaxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return strconv.QuoteToASCII(value[:end] + videoProxyLogTruncationMarker)
}

func logVideoProxyFailure(
	ctx context.Context,
	taskID string,
	phase string,
	err error,
) {
	logger.LogError(ctx, fmt.Sprintf(
		"video_proxy task_id=%s phase=%s error_type=%T",
		videoProxyLogField(taskID),
		phase,
		err,
	))
}

func logVideoProxyUpstreamStatus(
	ctx context.Context,
	taskID string,
	status int,
) {
	logger.LogError(ctx, fmt.Sprintf(
		"video_proxy task_id=%s phase=upstream_status status=%d",
		videoProxyLogField(taskID),
		status,
	))
}
