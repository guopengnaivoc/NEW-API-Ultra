package controller

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sentinels that stand in for every class of raw material a relay error can
// carry. None of them may survive into persistence, the status endpoint
// response, captured logs, or generic error formatting of the stored reason.
const (
	sentinelPath     = "SENTINEL-PATH-/v1/chat/completions%3Fuser%3Dalice"
	sentinelQuery    = "SENTINEL-QUERY-api-version=2024-secret"
	sentinelAPIKey   = "SENTINEL-KEY-sk-abcdef1234567890"
	sentinelHeader   = "SENTINEL-HEADER-Authorization-Bearer-xyz"
	sentinelRespBody = "SENTINEL-BODY-your-org-1234-has-been-suspended"
	sentinelProvider = "SENTINEL-PROVIDER-openai-error-text"
)

// The last line the asynchronous disable flow emits. NotifyRootUser always
// logs either a success path completion or this failure; the notification
// backend is unconfigured in the test fixture, so the failure line is the
// deterministic tail.
const disableFlowTerminalLogMarker = "failed to notify root user:"

func allDisableReasonSentinels() []string {
	return []string{
		sentinelPath, sentinelQuery, sentinelAPIKey,
		sentinelHeader, sentinelRespBody, sentinelProvider,
	}
}

// The disable flow finishes on a gopool goroutine, so the log sink it writes
// through outlives the synchronous part of the test. syncLogBuffer makes the
// capture safe to read while that goroutine may still be writing; the test
// additionally waits for the flow's terminal log line before asserting, so
// the assertion sees the complete stream rather than a torn prefix.
type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The multi-key auto-disable path persists a reason into
// ChannelInfo.MultiKeyDisabledReason and later returns it to administrators
// holding only ChannelOperate through the get_key_status action. The stored
// copy must therefore already be sanitized: masking at response time would
// still leave raw upstream text (paths, queries, keys, headers, response
// bodies) in the database.
func TestMultiKeyAutoDisableReasonNeverPersistsRawErrorMaterial(t *testing.T) {
	setupModelListControllerTestDB(t)

	previousAutoDisable := common.AutomaticDisableChannelEnabled
	previousMemoryCache := common.MemoryCacheEnabled
	common.AutomaticDisableChannelEnabled = true
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = previousAutoDisable
		common.MemoryCacheEnabled = previousMemoryCache
	})

	keys := []string{"sk-key-zero", "sk-key-one"}
	channel := insertMultiKeyStatusChannel(t, keys, model.ChannelInfo{})

	// Capture SYS logs for the whole disable flow.
	logBuffer := &syncLogBuffer{}
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	gin.DefaultWriter = logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		common.LogWriterMu.Unlock()
	})

	rawUpstream := fmt.Errorf(
		"POST %s?%s failed for key %s (header %s): %s | provider said: %s",
		sentinelPath, sentinelQuery, sentinelAPIKey,
		sentinelHeader, sentinelRespBody, sentinelProvider,
	)
	relayErr := types.NewErrorWithStatusCode(rawUpstream, types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized)
	require.True(t, types.IsChannelError(relayErr) || relayErr.StatusCode == http.StatusUnauthorized)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	channelError := types.NewChannelError(
		channel.Id, channel.Type, channel.Name,
		true, keys[0], true,
	)
	processChannelError(ctx, *channelError, relayErr)

	// The disable runs on gopool; wait for the persisted state, not a timer.
	require.Eventually(t, func() bool {
		reloaded, err := model.GetChannelById(channel.Id, true)
		if err != nil {
			return false
		}
		return len(reloaded.ChannelInfo.MultiKeyDisabledReason) > 0
	}, 5*time.Second, 20*time.Millisecond, "auto-disable never persisted a reason")

	reloaded, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	storedReason := reloaded.ChannelInfo.MultiKeyDisabledReason[0]
	require.NotEmpty(t, storedReason)

	// 1. Persistence is sanitized: the stored reason is the bounded category,
	//    and neither it, the whole ChannelInfo, nor generic formatting of the
	//    reason contains any sentinel.
	assert.Equal(t, "upstream_error: status_code=401, error_code=bad_response_status_code", storedReason)
	infoJSON, err := common.Marshal(reloaded.ChannelInfo)
	require.NoError(t, err)
	formatted := fmt.Sprintf("reason=%v err=%s", storedReason, storedReason)
	for _, sentinel := range allDisableReasonSentinels() {
		assert.NotContains(t, string(infoJSON), sentinel, "persisted ChannelInfo leaked %q", sentinel)
		assert.NotContains(t, formatted, sentinel)
	}

	// 2. The status endpoint (ChannelOperate-only read path: get_key_status
	//    requires no ChannelSensitiveWrite) returns JSON free of sentinels
	//    while still returning the bounded category to the operator.
	statusRecorder, response := requestMultiKeyStatus(t, channel.Id, map[string]any{
		"page":      1,
		"page_size": 50,
	})
	require.True(t, response.Success, response.Message)
	require.NotEmpty(t, response.Data.Keys)
	var disabledKey *KeyStatus
	for i := range response.Data.Keys {
		if response.Data.Keys[i].Index == 0 {
			disabledKey = &response.Data.Keys[i]
		}
	}
	require.NotNil(t, disabledKey)
	assert.Equal(t, common.ChannelStatusAutoDisabled, disabledKey.Status)
	assert.Equal(t, storedReason, disabledKey.Reason)
	for _, sentinel := range allDisableReasonSentinels() {
		assert.NotContains(t, statusRecorder.Body.String(), sentinel, "status endpoint leaked %q", sentinel)
	}

	// 3. The SYS/notify log stream for the disable flow carries the category,
	//    not the raw error. (Request-correlated diagnostics flow through the
	//    separate sanitized error-log path, which is already masked.)
	// The disable flow ends with the root-user notification attempt; waiting
	// for that line is a deterministic completion condition for the whole
	// asynchronous chain (DisableChannel -> UpdateChannelStatus ->
	// NotifyRootUser), not a timing guess.
	require.Eventually(t, func() bool {
		return strings.Contains(logBuffer.String(), disableFlowTerminalLogMarker)
	}, 5*time.Second, 20*time.Millisecond, "disable flow never reached its terminal log line")

	logs := logBuffer.String()
	assert.Contains(t, logs, "upstream_error: status_code=401")
	for _, sentinel := range allDisableReasonSentinels() {
		assert.NotContains(t, logs, sentinel, "disable-flow logs leaked %q", sentinel)
	}
}

// The category builder is the single boundary that turns a relay error into a
// persistable reason; it must stay constant-shaped for every input, including
// nil and code-less errors.
func TestChannelDisableCategoryIsBoundedAndConstantShaped(t *testing.T) {
	raw := errors.New(strings.Repeat(sentinelRespBody+" ", 200))

	withCode := types.NewErrorWithStatusCode(raw, types.ErrorCodeBadResponse, http.StatusForbidden)
	assert.Equal(t, "upstream_error: status_code=403, error_code=bad_response", withCode.ChannelDisableCategory())

	// NewError defaults the status code to 500, so the category still names it.
	noStatus := types.NewError(raw, types.ErrorCodeDoRequestFailed)
	assert.Equal(t, "upstream_error: status_code=500, error_code=do_request_failed", noStatus.ChannelDisableCategory())

	var nilErr *types.NewAPIError
	assert.Equal(t, "upstream_error", nilErr.ChannelDisableCategory())

	for _, category := range []string{withCode.ChannelDisableCategory(), noStatus.ChannelDisableCategory()} {
		assert.NotContains(t, category, sentinelRespBody)
		assert.Less(t, len(category), 128, "category must stay bounded")
	}
}

// WithOpenAIError and WithClaudeError copy provider-supplied strings straight
// into the relay error code, so the end-to-end disable path must be proven
// against those constructors, not only against an internal compile-time
// ErrorCode. Every hostile value has to be absent from the persisted reason,
// the ChannelOperate-only status response, and the disable-flow logs.
func TestMultiKeyAutoDisableSanitizesUpstreamControlledErrorCodes(t *testing.T) {
	tests := []struct {
		name    string
		relay   func() *types.NewAPIError
		hostile string
	}{
		{
			name: "openai code carrying an api key",
			relay: func() *types.NewAPIError {
				return types.WithOpenAIError(types.OpenAIError{
					Code:    sentinelAPIKey,
					Message: sentinelRespBody,
					Type:    sentinelProvider,
				}, http.StatusUnauthorized)
			},
			hostile: sentinelAPIKey,
		},
		{
			name: "claude type carrying an authorization header",
			relay: func() *types.NewAPIError {
				return types.WithClaudeError(types.ClaudeError{
					Type:    sentinelHeader,
					Message: sentinelRespBody,
				}, http.StatusUnauthorized)
			},
			hostile: sentinelHeader,
		},
		{
			name: "very long upstream code",
			relay: func() *types.NewAPIError {
				return types.WithOpenAIError(types.OpenAIError{
					Code:    strings.Repeat("A", 5000),
					Message: sentinelRespBody,
				}, http.StatusUnauthorized)
			},
			hostile: strings.Repeat("A", 5000),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupModelListControllerTestDB(t)

			previousAutoDisable := common.AutomaticDisableChannelEnabled
			previousMemoryCache := common.MemoryCacheEnabled
			common.AutomaticDisableChannelEnabled = true
			common.MemoryCacheEnabled = false
			t.Cleanup(func() {
				common.AutomaticDisableChannelEnabled = previousAutoDisable
				common.MemoryCacheEnabled = previousMemoryCache
			})

			keys := []string{"sk-key-zero", "sk-key-one"}
			channel := insertMultiKeyStatusChannel(t, keys, model.ChannelInfo{})

			logBuffer := &syncLogBuffer{}
			common.LogWriterMu.Lock()
			previousWriter := gin.DefaultWriter
			gin.DefaultWriter = logBuffer
			common.LogWriterMu.Unlock()
			t.Cleanup(func() {
				common.LogWriterMu.Lock()
				gin.DefaultWriter = previousWriter
				common.LogWriterMu.Unlock()
			})

			relayErr := tc.relay()
			// The category is already safe at the boundary itself.
			category := relayErr.ChannelDisableCategory()
			assert.NotContains(t, category, tc.hostile)
			assert.LessOrEqual(t, len(category), types.MaxChannelDisableCategoryLen)

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			channelError := types.NewChannelError(
				channel.Id, channel.Type, channel.Name,
				true, keys[0], true,
			)
			processChannelError(ctx, *channelError, relayErr)

			require.Eventually(t, func() bool {
				reloaded, err := model.GetChannelById(channel.Id, true)
				if err != nil {
					return false
				}
				return len(reloaded.ChannelInfo.MultiKeyDisabledReason) > 0
			}, 5*time.Second, 20*time.Millisecond, "auto-disable never persisted a reason")

			reloaded, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			storedReason := reloaded.ChannelInfo.MultiKeyDisabledReason[0]

			// Persistence carries the bounded category and no hostile material.
			assert.Equal(t, category, storedReason)
			assert.Equal(t, "upstream_error: status_code=401, error_code=unknown", storedReason)
			assert.LessOrEqual(t, len(storedReason), types.MaxChannelDisableCategoryLen)
			infoJSON, err := common.Marshal(reloaded.ChannelInfo)
			require.NoError(t, err)
			assert.NotContains(t, string(infoJSON), tc.hostile)

			// The ChannelOperate-only status boundary stays clean, and the key
			// is still reported as auto-disabled with the operator category.
			statusRecorder, response := requestMultiKeyStatus(t, channel.Id, map[string]any{
				"page":      1,
				"page_size": 50,
			})
			require.True(t, response.Success, response.Message)
			var disabledKey *KeyStatus
			for i := range response.Data.Keys {
				if response.Data.Keys[i].Index == 0 {
					disabledKey = &response.Data.Keys[i]
				}
			}
			require.NotNil(t, disabledKey)
			assert.Equal(t, common.ChannelStatusAutoDisabled, disabledKey.Status)
			assert.Equal(t, storedReason, disabledKey.Reason)
			assert.NotContains(t, statusRecorder.Body.String(), tc.hostile)

			require.Eventually(t, func() bool {
				return strings.Contains(logBuffer.String(), disableFlowTerminalLogMarker)
			}, 5*time.Second, 20*time.Millisecond, "disable flow never reached its terminal log line")
			assert.NotContains(t, logBuffer.String(), tc.hostile, "disable-flow logs leaked hostile material")
		})
	}
}
