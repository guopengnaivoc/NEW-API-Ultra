package relay

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type geminiTaskSubmitBillingStub struct{}

func (geminiTaskSubmitBillingStub) Settle(int) error         { return nil }
func (geminiTaskSubmitBillingStub) Refund(*gin.Context)      {}
func (geminiTaskSubmitBillingStub) NeedsRefund() bool        { return false }
func (geminiTaskSubmitBillingStub) GetPreConsumedQuota() int { return 0 }
func (geminiTaskSubmitBillingStub) Reserve(int) error        { return nil }

func TestRelayTaskSubmitGeminiFailureSanitizesBodyBeforeError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()

	const (
		credential        = "failure-credential-sentinel"
		providerPath      = "failure-provider-path-sentinel"
		signedQuery       = "failure-signed-query-sentinel"
		providerMessage   = "failure-provider-message-sentinel"
		operationSentinel = "failure-operation-name-sentinel"
	)
	rawProviderURI := "https://video.example.test/" + providerPath +
		"?key=" + credential + "&sig=" + signedQuery
	rawBody := fmt.Sprintf(
		`{"name":%q,"done":true,"uri":%q,"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":%q}}`,
		operationSentinel,
		rawProviderURI,
		providerMessage,
	)
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			assert.Equal(t, credential, request.Header.Get("x-goog-api-key"))
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = writer.Write([]byte(rawBody))
		},
	))
	t.Cleanup(upstream.Close)

	var capturedLogs bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultWriter = &capturedLogs
	gin.DefaultErrorWriter = &capturedLogs
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		gin.DefaultErrorWriter = previousErrorWriter
		common.LogWriterMu.Unlock()
	})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		strings.NewReader(`{"prompt":"privacy boundary"}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("platform", strconv.Itoa(constant.ChannelTypeGemini))
	common.SetContextKey(
		context,
		constant.ContextKeyChannelType,
		constant.ChannelTypeGemini,
	)
	common.SetContextKey(context, constant.ContextKeyChannelId, 38)
	common.SetContextKey(
		context,
		constant.ContextKeyChannelBaseUrl,
		upstream.URL,
	)
	common.SetContextKey(context, constant.ContextKeyChannelKey, credential)
	common.SetContextKey(
		context,
		constant.ContextKeyOriginalModel,
		"veo-3.0-generate-001",
	)
	t.Cleanup(func() {
		common.CleanupBodyStorage(context)
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "veo-3.0-generate-001",
		UsingGroup:      "default",
		UserGroup:       "default",
		Billing:         geminiTaskSubmitBillingStub{},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID:      "task_public_failure",
			ProviderResultURI: "stale-provider-result-sentinel",
		},
	}

	result, taskErr := RelayTaskSubmit(context, info)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, "fail_to_fetch_task", taskErr.Code)
	assert.Equal(t, http.StatusTooManyRequests, taskErr.StatusCode)
	assert.Equal(
		t,
		"Gemini task submission failed: HTTP 429, code 429, status RESOURCE_EXHAUSTED",
		taskErr.Message,
	)

	assert.Empty(t, info.ProviderResultURI)
	transientResultJSON, err := common.Marshal(TaskSubmitResult{
		ProviderResultURI: rawProviderURI,
		SuccessResponse: &relaycommon.TaskSuccessResponse{
			StatusCode: http.StatusOK,
			Body:       map[string]string{"raw": rawProviderURI},
		},
	})
	require.NoError(t, err)
	assert.NotContains(t, string(transientResultJSON), providerPath)
	assert.NotContains(t, string(transientResultJSON), signedQuery)
	assert.NotContains(t, string(transientResultJSON), credential)

	context.JSON(taskErr.StatusCode, taskErr)
	publicSinks := taskErr.Message + "\n" +
		fmt.Sprint(taskErr.Error) + "\n" +
		recorder.Body.String() + "\n" +
		capturedLogs.String()
	assert.Contains(t, publicSinks, "HTTP 429")
	assert.Contains(t, publicSinks, "RESOURCE_EXHAUSTED")
	for _, sentinel := range []string{
		credential,
		providerPath,
		signedQuery,
		providerMessage,
		operationSentinel,
		rawProviderURI,
	} {
		assert.NotContains(t, publicSinks, sentinel)
	}
}

func useGeminiRealtimeTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	previousMemoryCache := common.MemoryCacheEnabled
	previousBatchUpdate := common.BatchUpdateEnabled
	previousLogConsume := common.LogConsumeEnabled

	common.SetDatabaseTypes(
		common.DatabaseTypeSQLite,
		common.DatabaseTypeSQLite,
	)
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	service.InitHttpClient()

	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Channel{},
		&model.Task{},
		&model.BillingOperation{},
		&model.Log{},
	))
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(
			previousMainDatabaseType,
			previousLogDatabaseType,
		)
		common.RedisEnabled = previousRedis
		common.MemoryCacheEnabled = previousMemoryCache
		common.BatchUpdateEnabled = previousBatchUpdate
		common.LogConsumeEnabled = previousLogConsume
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func captureGeminiRealtimeLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	output := &bytes.Buffer{}
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultWriter = output
	gin.DefaultErrorWriter = output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		gin.DefaultErrorWriter = previousErrorWriter
		common.LogWriterMu.Unlock()
	})
	return output
}

func newGeminiRealtimeTask(
	t *testing.T,
	db *gorm.DB,
	channel *model.Channel,
	selectedKey string,
	taskID string,
) *model.Task {
	t.Helper()

	require.NoError(t, db.Create(channel).Error)
	task := model.InitTask(
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		&relaycommon.RelayInfo{
			UserId:     1,
			UsingGroup: "default",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId:   channel.Id,
				ChannelType: constant.ChannelTypeGemini,
				ApiKey:      selectedKey,
			},
		},
	)
	task.TaskID = taskID
	task.Action = constant.TaskActionGenerate
	task.Status = model.TaskStatusInProgress
	task.Progress = taskcommon.ProgressInProgress
	task.Data = geminitaskresult.EmptyPublicProjection(false)
	task.PrivateData.UpstreamTaskID = taskcommon.EncodeLocalTaskID(
		"projects/privacy/locations/us-central1/publishers/google/models/" +
			"veo-3.0-generate-001/operations/" + taskID,
	)
	require.NoError(t, db.Create(task).Error)
	return task
}

func newGeminiRealtimeContext() *gin.Context {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodGet,
		"/v1/videos/task/content",
		nil,
	).WithContext(context.Background())
	return ctx
}

func TestTryRealtimeFetchGeminiUsesExactResolvedKey(t *testing.T) {
	db := useGeminiRealtimeTestDB(t)

	const (
		selectedKey = "gemini-realtime-selected-key-sentinel"
		otherKey    = "gemini-realtime-other-key-sentinel"
	)
	requestKeys := make([]string, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			requestKeys = append(
				requestKeys,
				request.Header.Get("x-goog-api-key"),
			)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"done":false}`))
		},
	))
	t.Cleanup(upstream.Close)

	baseURL := upstream.URL
	channel := &model.Channel{
		Type:    constant.ChannelTypeGemini,
		Key:     otherKey + "\n" + selectedKey,
		Status:  common.ChannelStatusEnabled,
		Name:    "gemini-realtime-exact-key",
		BaseURL: &baseURL,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	task := newGeminiRealtimeTask(
		t,
		db,
		channel,
		selectedKey,
		"task_gemini_realtime_exact_key",
	)

	require.NotEmpty(t, tryRealtimeFetch(newGeminiRealtimeContext(), task, false))
	require.NoError(
		t,
		model.UpdateChannelKey(channel.Id, selectedKey+"\n"+otherKey),
	)
	require.NotEmpty(t, tryRealtimeFetch(newGeminiRealtimeContext(), task, false))

	assert.Equal(t, []string{selectedKey, selectedKey}, requestKeys)
}

func TestTryRealtimeFetchGeminiPersistsOnlySanitizedRepresentations(
	t *testing.T,
) {
	for _, shape := range []string{"generatedSamples", "generatedVideos"} {
		t.Run(shape, func(t *testing.T) {
			db := useGeminiRealtimeTestDB(t)
			logs := captureGeminiRealtimeLogs(t)

			const (
				selectedKey      = "gemini-realtime-persist-key-sentinel"
				providerPath     = "gemini-realtime-provider-path-sentinel"
				signedQuery      = "gemini-realtime-signed-query-sentinel"
				operationName    = "gemini-realtime-operation-sentinel"
				metadataSentinel = "gemini-realtime-metadata-sentinel"
			)
			rawProviderURI := "https://video.example.test/" + providerPath +
				"?key=" + selectedKey + "&sig=" + signedQuery
			filteredProviderURI := "https://video.example.test/" + providerPath +
				"?sig=" + signedQuery
			rawBody := fmt.Sprintf(
				`{"name":%q,"done":true,"response":{"generateVideoResponse":{%q:[{"video":{"uri":%q,"mimeType":"video/webm; codecs=vp9"}}]},"metadata":%q}}`,
				operationName,
				shape,
				rawProviderURI,
				metadataSentinel,
			)
			upstream := httptest.NewServer(http.HandlerFunc(
				func(writer http.ResponseWriter, request *http.Request) {
					assert.Equal(
						t,
						selectedKey,
						request.Header.Get("x-goog-api-key"),
					)
					writer.Header().Set("Content-Type", "application/json")
					_, _ = writer.Write([]byte(rawBody))
				},
			))
			t.Cleanup(upstream.Close)

			baseURL := upstream.URL
			channel := &model.Channel{
				Type:    constant.ChannelTypeGemini,
				Key:     selectedKey,
				Status:  common.ChannelStatusEnabled,
				Name:    "gemini-realtime-sanitized-persistence",
				BaseURL: &baseURL,
			}
			require.NoError(t, db.Create(&model.User{
				Id:       1,
				Username: "gemini-realtime-user",
				Quota:    common.MaxQuota,
				Status:   common.UserStatusEnabled,
				Group:    "default",
			}).Error)
			task := newGeminiRealtimeTask(
				t,
				db,
				channel,
				selectedKey,
				"task_gemini_realtime_sanitized_"+shape,
			)
			task.Data = []byte(`{"legacy":"` + metadataSentinel + `"}`)
			task.PrivateData.ResultURL = rawProviderURI
			task.FailReason = metadataSentinel
			require.NoError(t, task.Update())

			responseBody := tryRealtimeFetch(
				newGeminiRealtimeContext(),
				task,
				false,
			)
			require.NotEmpty(t, responseBody)

			var persisted model.Task
			require.NoError(t, db.First(&persisted, task.ID).Error)
			assert.EqualValues(t, model.TaskStatusSuccess, persisted.Status)
			assert.JSONEq(t, fmt.Sprintf(`{
				"done": true,
				"video": {
					"url": "/v1/videos/%s/content",
					"mime_type": "video/webm"
				}
			}`, persisted.TaskID), string(persisted.Data))
			assert.Equal(
				t,
				taskcommon.BuildProxyURL(persisted.TaskID),
				persisted.PrivateData.ResultURL,
			)
			assert.Empty(t, persisted.FailReason)
			require.NotNil(t, persisted.EncryptedProviderResultURI)
			assert.True(
				t,
				common.IsDataEncryptionEnvelope(
					*persisted.EncryptedProviderResultURI,
				),
			)
			openedURI, err := persisted.OpenProviderResultURI()
			require.NoError(t, err)
			assert.Equal(t, filteredProviderURI, openedURI)

			var billingOperations []model.BillingOperation
			require.NoError(t, db.Find(&billingOperations).Error)
			require.Len(t, billingOperations, 1)
			var billingLogs []model.Log
			require.NoError(t, db.Find(&billingLogs).Error)

			privateData, err := common.Marshal(persisted.PrivateData)
			require.NoError(t, err)
			billingData, err := common.Marshal(billingOperations)
			require.NoError(t, err)
			billingLogData, err := common.Marshal(billingLogs)
			require.NoError(t, err)
			publicSinks := string(responseBody) + "\n" +
				string(persisted.Data) + "\n" +
				string(privateData) + "\n" +
				string(billingData) + "\n" +
				string(billingLogData) + "\n" +
				logs.String()
			assert.Contains(
				t,
				string(responseBody),
				taskcommon.BuildProxyURL(persisted.TaskID),
			)
			assert.Contains(t, string(responseBody), `"format":"video/webm"`)
			for _, sentinel := range []string{
				selectedKey,
				providerPath,
				signedQuery,
				operationName,
				metadataSentinel,
				rawProviderURI,
				filteredProviderURI,
			} {
				assert.NotContains(t, publicSinks, sentinel)
			}
		})
	}
}

func TestTryRealtimeFetchGeminiFailureAndLogsOmitRawProviderData(
	t *testing.T,
) {
	t.Run("non-retryable provider failure persists refund and falls back", func(t *testing.T) {
		db := useGeminiRealtimeTestDB(t)
		logs := captureGeminiRealtimeLogs(t)

		const (
			selectedKey     = "gemini-realtime-terminal-failure-key-sentinel"
			providerPath    = "gemini-realtime-terminal-failure-path-sentinel"
			providerQuery   = "gemini-realtime-terminal-failure-query-sentinel"
			providerMessage = "gemini-realtime-terminal-failure-message-sentinel"
		)
		rawProviderURI := "https://video.example.test/" + providerPath +
			"?key=" + selectedKey + "&sig=" + providerQuery
		rawBody := fmt.Sprintf(`{
			"done": true,
			"uri": %q,
			"mimeType": "video/mp4",
			"error": {
				"code": 9,
				"status": "FAILED_PRECONDITION",
				"message": %q
			}
		}`, rawProviderURI, providerMessage+" "+rawProviderURI)
		upstream := httptest.NewServer(http.HandlerFunc(
			func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = writer.Write([]byte(rawBody))
			},
		))
		t.Cleanup(upstream.Close)

		require.NoError(t, db.Create(&model.User{
			Id:       1,
			Username: "gemini-terminal-failure-user",
			Quota:    75,
			Status:   common.UserStatusEnabled,
			Group:    "default",
		}).Error)
		baseURL := upstream.URL
		channel := &model.Channel{
			Type:    constant.ChannelTypeGemini,
			Key:     selectedKey,
			Status:  common.ChannelStatusEnabled,
			Name:    "gemini-realtime-terminal-failure",
			BaseURL: &baseURL,
		}
		task := newGeminiRealtimeTask(
			t,
			db,
			channel,
			selectedKey,
			"task_gemini_realtime_terminal_failure",
		)
		task.Quota = 25
		operation := &model.BillingOperation{
			RequestId:     "gemini-realtime-terminal-failure-refund",
			TaskId:        task.ID,
			UserId:        task.UserId,
			FundingSource: model.BillingOperationFundingWallet,
			ReservedQuota: task.Quota,
			Status:        model.BillingOperationStatusReserved,
		}
		require.NoError(t, db.Create(operation).Error)
		task.BillingOperationId = operation.Id
		require.NoError(t, task.Update())

		realtimeBody := tryRealtimeFetch(
			newGeminiRealtimeContext(),
			task,
			false,
		)
		assert.Nil(t, realtimeBody)

		var persisted model.Task
		require.NoError(t, db.First(&persisted, task.ID).Error)
		assert.EqualValues(t, model.TaskStatusFailure, persisted.Status)
		assert.Equal(t, taskcommon.ProgressComplete, persisted.Progress)
		assert.Equal(t, "upstream task failed", persisted.FailReason)
		assert.Empty(t, persisted.PrivateData.ResultURL)
		assert.Zero(t, persisted.Quota)
		assert.JSONEq(t, fmt.Sprintf(`{
			"done": true,
			"video": {
				"url": "/v1/videos/%s/content",
				"mime_type": "video/mp4"
			},
			"error": {
				"code": 9,
				"status": "FAILED_PRECONDITION"
			}
		}`, task.TaskID), string(persisted.Data))
		assert.True(t, persisted.Snapshot().Equal(task.Snapshot()))

		var persistedOperation model.BillingOperation
		require.NoError(t, db.First(&persistedOperation, operation.Id).Error)
		assert.Equal(
			t,
			model.BillingOperationStatusRefunded,
			persistedOperation.Status,
		)
		assert.Zero(t, persistedOperation.ActualQuota)
		var persistedUser model.User
		require.NoError(t, db.First(&persistedUser, 1).Error)
		assert.Equal(t, 100, persistedUser.Quota)

		fallbackBody, err := common.Marshal(dto.TaskResponse[any]{
			Code: dto.TaskSuccessCode,
			Data: TaskModel2Dto(task),
		})
		require.NoError(t, err)
		assert.Contains(t, string(fallbackBody), "upstream task failed")
		assert.NotContains(t, string(fallbackBody), `"result_url"`)

		var billingLogs []model.Log
		require.NoError(t, db.Find(&billingLogs).Error)
		require.Len(t, billingLogs, 1)
		rowJSON, err := common.Marshal(persisted)
		require.NoError(t, err)
		dtoJSON, err := common.Marshal(TaskModel2Dto(&persisted))
		require.NoError(t, err)
		billingJSON, err := common.Marshal(billingLogs)
		require.NoError(t, err)
		publicSinks := string(realtimeBody) + "\n" +
			string(rowJSON) + "\n" +
			string(dtoJSON) + "\n" +
			string(fallbackBody) + "\n" +
			string(billingJSON) + "\n" +
			logs.String()
		for _, sentinel := range []string{
			selectedKey,
			providerPath,
			providerQuery,
			providerMessage,
			rawProviderURI,
		} {
			assert.NotContains(t, publicSinks, sentinel)
		}
	})

	t.Run("retryable 429 does not mutate state", func(t *testing.T) {
		db := useGeminiRealtimeTestDB(t)
		logs := captureGeminiRealtimeLogs(t)

		const (
			selectedKey     = "gemini-realtime-429-key-sentinel"
			providerMessage = "gemini-realtime-429-message-sentinel"
			providerPath    = "gemini-realtime-429-path-sentinel"
		)
		rawBody := `{"done":true,"error":{"code":429,` +
			`"status":"RESOURCE_EXHAUSTED","message":"` +
			providerMessage + ` https://video.example.test/` +
			providerPath + `?key=` + selectedKey + `"}}`
		upstream := httptest.NewServer(http.HandlerFunc(
			func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusTooManyRequests)
				_, _ = writer.Write([]byte(rawBody))
			},
		))
		t.Cleanup(upstream.Close)

		baseURL := upstream.URL
		channel := &model.Channel{
			Type:    constant.ChannelTypeGemini,
			Key:     selectedKey,
			Status:  common.ChannelStatusEnabled,
			Name:    "gemini-realtime-429",
			BaseURL: &baseURL,
		}
		task := newGeminiRealtimeTask(
			t,
			db,
			channel,
			selectedKey,
			"task_gemini_realtime_429",
		)
		before := task.Snapshot()

		assert.Nil(t, tryRealtimeFetch(newGeminiRealtimeContext(), task, false))
		var persisted model.Task
		require.NoError(t, db.First(&persisted, task.ID).Error)
		assert.True(t, before.Equal(task.Snapshot()))
		assert.True(t, before.Equal(persisted.Snapshot()))
		for _, sentinel := range []string{
			selectedKey,
			providerMessage,
			providerPath,
		} {
			assert.NotContains(t, logs.String(), sentinel)
		}
	})

	t.Run("malformed response and channel drift fail closed", func(t *testing.T) {
		db := useGeminiRealtimeTestDB(t)
		logs := captureGeminiRealtimeLogs(t)

		const malformedSentinel = "gemini-realtime-malformed-sentinel"
		var requests int
		upstream := httptest.NewServer(http.HandlerFunc(
			func(writer http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = writer.Write(
					[]byte(`{"raw":"` + malformedSentinel + `"`),
				)
			},
		))
		t.Cleanup(upstream.Close)

		baseURL := upstream.URL
		channel := &model.Channel{
			Type:    constant.ChannelTypeGemini,
			Key:     "gemini-realtime-malformed-key",
			Status:  common.ChannelStatusEnabled,
			Name:    "gemini-realtime-malformed",
			BaseURL: &baseURL,
		}
		task := newGeminiRealtimeTask(
			t,
			db,
			channel,
			channel.Key,
			"task_gemini_realtime_malformed",
		)
		before := task.Snapshot()
		assert.Nil(t, tryRealtimeFetch(newGeminiRealtimeContext(), task, false))
		assert.Equal(t, 1, requests)
		assert.True(t, before.Equal(task.Snapshot()))
		assert.NotContains(t, logs.String(), malformedSentinel)

		for _, driftType := range []int{
			constant.ChannelTypeKling,
			constant.ChannelTypeVertexAi,
		} {
			require.NoError(t, db.Model(&model.Channel{}).
				Where("id = ?", channel.Id).
				Update("type", driftType).Error)
			assert.Nil(
				t,
				tryRealtimeFetch(newGeminiRealtimeContext(), task, false),
			)
			assert.Equal(t, 1, requests)
			assert.True(t, before.Equal(task.Snapshot()))
		}
	})
}

func TestTryRealtimeFetchGeminiNonterminalCompatibilityOmitsResultURL(
	t *testing.T,
) {
	db := useGeminiRealtimeTestDB(t)

	const selectedKey = "gemini-realtime-nonterminal-key-sentinel"
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{"done":false}`))
		},
	))
	t.Cleanup(upstream.Close)

	baseURL := upstream.URL
	channel := &model.Channel{
		Type:    constant.ChannelTypeGemini,
		Key:     selectedKey,
		Status:  common.ChannelStatusEnabled,
		Name:    "gemini-realtime-nonterminal",
		BaseURL: &baseURL,
	}
	task := newGeminiRealtimeTask(
		t,
		db,
		channel,
		selectedKey,
		"task_gemini_realtime_nonterminal",
	)

	responseBody := tryRealtimeFetch(
		newGeminiRealtimeContext(),
		task,
		false,
	)
	require.NotEmpty(t, responseBody)
	var response dto.TaskResponse[map[string]any]
	require.NoError(t, common.Unmarshal(responseBody, &response))
	assert.Equal(t, dto.TaskSuccessCode, response.Code)
	assert.Equal(t, "processing", response.Data["status"])
	assert.Equal(t, "", response.Data["url"])
	assert.Equal(t, task.TaskID, response.Data["task_id"])

	var persisted model.Task
	require.NoError(t, db.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusInProgress, persisted.Status)
	assert.Empty(t, persisted.PrivateData.ResultURL)
	taskDTO := TaskModel2Dto(&persisted)
	assert.Empty(t, taskDTO.ResultURL)
	assert.Empty(t, taskDTO.FailReason)
}

func TestTryRealtimeFetchGeminiAmbiguousLegacyKeyFailsBeforeRequest(
	t *testing.T,
) {
	db := useGeminiRealtimeTestDB(t)

	var requests int
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			requests++
			_, _ = writer.Write([]byte(`{"done":false}`))
		},
	))
	t.Cleanup(upstream.Close)

	baseURL := upstream.URL
	channel := &model.Channel{
		Type:    constant.ChannelTypeGemini,
		Key:     "gemini-ambiguous-key-a\ngemini-ambiguous-key-b",
		Status:  common.ChannelStatusEnabled,
		Name:    "gemini-realtime-ambiguous-legacy",
		BaseURL: &baseURL,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	require.NoError(t, db.Create(channel).Error)
	task := &model.Task{
		TaskID:    "task_gemini_realtime_ambiguous",
		Platform:  constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		UserId:    1,
		ChannelId: channel.Id,
		Status:    model.TaskStatusInProgress,
		Progress:  taskcommon.ProgressInProgress,
		Data:      geminitaskresult.EmptyPublicProjection(false),
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: taskcommon.EncodeLocalTaskID(
				"projects/privacy/operations/ambiguous",
			),
		},
	}
	require.NoError(t, db.Create(task).Error)
	before := task.Snapshot()

	assert.Nil(t, tryRealtimeFetch(newGeminiRealtimeContext(), task, false))
	assert.Zero(t, requests)
	assert.True(t, before.Equal(task.Snapshot()))
	var persisted model.Task
	require.NoError(t, db.First(&persisted, task.ID).Error)
	assert.True(t, before.Equal(persisted.Snapshot()))
}

func TestTryRealtimeFetchGeminiRequiredEncryptionFailureIsAtomic(
	t *testing.T,
) {
	db := useGeminiRealtimeTestDB(t)

	const (
		selectedKey  = "gemini-protection-failure-key-sentinel"
		providerPath = "gemini-protection-failure-provider-sentinel"
	)
	rawProviderURI := "https://video.example.test/" + providerPath +
		"?key=" + selectedKey
	rawBody := fmt.Sprintf(
		`{"done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":%q,"mimeType":"video/mp4"}}]}}}`,
		rawProviderURI,
	)
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(rawBody))
		},
	))
	t.Cleanup(upstream.Close)

	baseURL := upstream.URL
	channel := &model.Channel{
		Type:    constant.ChannelTypeGemini,
		Key:     selectedKey,
		Status:  common.ChannelStatusEnabled,
		Name:    "gemini-realtime-protection-failure",
		BaseURL: &baseURL,
	}
	task := newGeminiRealtimeTask(
		t,
		db,
		channel,
		selectedKey,
		"task_gemini_realtime_protection_failure",
	)
	invalidEnvelope := "naenc:v1:test:invalid-wrapped-key:" +
		"invalid-provider-result-ciphertext"
	task.EncryptedProviderResultURI = &invalidEnvelope
	require.NoError(t, task.Update())
	before := task.Snapshot()

	assert.Nil(t, tryRealtimeFetch(newGeminiRealtimeContext(), task, false))
	assert.True(t, before.Equal(task.Snapshot()))
	var persisted model.Task
	require.NoError(t, db.First(&persisted, task.ID).Error)
	assert.True(t, before.Equal(persisted.Snapshot()))
	var billingCount int64
	require.NoError(t, db.Model(&model.BillingOperation{}).
		Count(&billingCount).Error)
	assert.Zero(t, billingCount)
	var logCount int64
	require.NoError(t, db.Model(&model.Log{}).Count(&logCount).Error)
	assert.Zero(t, logCount)
}

func TestTryRealtimeFetchGeminiTerminalWinnerAndBillingRecovery(
	t *testing.T,
) {
	db := useGeminiRealtimeTestDB(t)
	logs := captureGeminiRealtimeLogs(t)

	const (
		selectedKey = "gemini-terminal-recovery-key-sentinel"
		winnerPath  = "gemini-terminal-winner-provider-sentinel"
		loserPath   = "gemini-terminal-loser-provider-sentinel"
	)
	loserURI := "https://video.example.test/" + loserPath +
		"?key=" + selectedKey
	rawBody := fmt.Sprintf(
		`{"done":true,"response":{"generateVideoResponse":{"generatedVideos":[{"video":{"uri":%q,"mimeType":"video/mp4"}}]}}}`,
		loserURI,
	)
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(rawBody))
		},
	))
	t.Cleanup(upstream.Close)

	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "gemini-terminal-recovery-user",
		Quota:    common.MaxQuota,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)
	baseURL := upstream.URL
	channel := &model.Channel{
		Type:    constant.ChannelTypeGemini,
		Key:     selectedKey,
		Status:  common.ChannelStatusEnabled,
		Name:    "gemini-realtime-terminal-recovery",
		BaseURL: &baseURL,
	}
	task := newGeminiRealtimeTask(
		t,
		db,
		channel,
		selectedKey,
		"task_gemini_realtime_terminal_recovery",
	)
	winnerURI := "https://video.example.test/" + winnerPath
	task.Status = model.TaskStatusSuccess
	task.Progress = taskcommon.ProgressComplete
	task.Data = []byte(`{
		"done": true,
		"video": {
			"url": "/v1/videos/task_gemini_realtime_terminal_recovery/content",
			"mime_type": "video/mp4"
		}
	}`)
	task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
	_, err := task.SetProviderResultURI(winnerURI)
	require.NoError(t, err)
	require.NoError(t, task.Update())

	operation := &model.BillingOperation{
		RequestId:     "gemini-terminal-recovery",
		TaskId:        task.ID,
		UserId:        task.UserId,
		FundingSource: model.BillingOperationFundingWallet,
		ReservedQuota: 0,
		Status:        model.BillingOperationStatusReserved,
	}
	require.NoError(t, db.Create(operation).Error)
	task.BillingOperationId = operation.Id
	require.NoError(t, task.Update())
	winnerSnapshot := task.Snapshot()

	for range 2 {
		require.NotEmpty(
			t,
			tryRealtimeFetch(newGeminiRealtimeContext(), task, false),
		)
		assert.True(t, winnerSnapshot.Equal(task.Snapshot()))
	}

	var persisted model.Task
	require.NoError(t, db.First(&persisted, task.ID).Error)
	assert.True(t, winnerSnapshot.Equal(persisted.Snapshot()))
	openedURI, err := persisted.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, winnerURI, openedURI)
	var persistedOperation model.BillingOperation
	require.NoError(t, db.First(&persistedOperation, operation.Id).Error)
	assert.Equal(
		t,
		model.BillingOperationStatusSettled,
		persistedOperation.Status,
	)
	for _, sentinel := range []string{
		selectedKey,
		loserPath,
		loserURI,
	} {
		assert.NotContains(t, logs.String(), sentinel)
		assert.NotContains(t, string(persisted.Data), sentinel)
	}
}

func TestTaskModel2DtoGeminiResanitizesLegacyRawData(t *testing.T) {
	const (
		providerPath     = "gemini-dto-provider-path-sentinel"
		providerQuery    = "gemini-dto-provider-query-sentinel"
		providerMessage  = "gemini-dto-provider-message-sentinel"
		privateResultURL = "gemini-dto-private-result-sentinel"
	)
	task := &model.Task{
		TaskID: "task_gemini_dto_resanitize",
		Platform: constant.TaskPlatform(
			strconv.Itoa(constant.ChannelTypeGemini),
		),
		Status:     model.TaskStatusSuccess,
		Progress:   taskcommon.ProgressComplete,
		FailReason: providerMessage,
		PrivateData: model.TaskPrivateData{
			ResultURL: privateResultURL,
		},
		Data: []byte(`{
			"done": true,
			"response": {
				"generateVideoResponse": {
					"generatedSamples": [{
						"video": {
							"uri": "https://video.example.test/` +
			providerPath + `?sig=` + providerQuery + `",
							"mimeType": "video/mp4"
						}
					}]
				}
			}
		}`),
	}
	_, err := task.SetProviderResultURI(
		"https://encrypted.example.test/" + providerPath +
			"?sig=" + providerQuery,
	)
	require.NoError(t, err)
	envelope := *task.EncryptedProviderResultURI

	result := TaskModel2Dto(task)
	resultJSON, err := common.Marshal(result)
	require.NoError(t, err)

	assert.JSONEq(t, fmt.Sprintf(`{
		"done": true,
		"video": {
			"url": "/v1/videos/%s/content",
			"mime_type": "video/mp4"
		}
	}`, task.TaskID), string(result.Data))
	assert.Equal(t, taskcommon.BuildProxyURL(task.TaskID), result.ResultURL)
	assert.Empty(t, result.FailReason)
	for _, sentinel := range []string{
		providerPath,
		providerQuery,
		providerMessage,
		privateResultURL,
		envelope,
		"naenc:v1",
	} {
		assert.NotContains(t, string(resultJSON), sentinel)
	}
}

func TestTaskModel2DtoGeminiMalformedDataFailsClosed(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		status     model.TaskStatus
		done       bool
		resultURL  string
		failReason string
	}{
		{
			name:      "success",
			status:    model.TaskStatusSuccess,
			done:      true,
			resultURL: taskcommon.BuildProxyURL("task_gemini_dto_success"),
		},
		{
			name:       "failure",
			status:     model.TaskStatusFailure,
			done:       true,
			failReason: "upstream task failed",
		},
		{
			name:   "nonterminal",
			status: model.TaskStatusInProgress,
			done:   false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			taskID := "task_gemini_dto_" + testCase.name
			task := &model.Task{
				TaskID: taskID,
				Platform: constant.TaskPlatform(
					strconv.Itoa(constant.ChannelTypeGemini),
				),
				Status:     testCase.status,
				FailReason: "malformed-dto-failure-sentinel",
				PrivateData: model.TaskPrivateData{
					ResultURL: "malformed-dto-result-sentinel",
				},
				Data: []byte(`{"malformed":"dto-private-sentinel"`),
			}

			result := TaskModel2Dto(task)
			assert.JSONEq(
				t,
				fmt.Sprintf(`{"done":%t}`, testCase.done),
				string(result.Data),
			)
			assert.Equal(t, testCase.failReason, result.FailReason)
			assert.Equal(t, testCase.resultURL, result.ResultURL)
			resultJSON, err := common.Marshal(result)
			require.NoError(t, err)
			for _, sentinel := range []string{
				"malformed-dto-failure-sentinel",
				"malformed-dto-result-sentinel",
				"dto-private-sentinel",
			} {
				assert.NotContains(t, string(resultJSON), sentinel)
			}
		})
	}
}
