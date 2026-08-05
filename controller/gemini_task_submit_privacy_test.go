package controller

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type geminiTaskSubmitStoredRow struct {
	Data              []byte         `gorm:"column:data"`
	PrivateData       []byte         `gorm:"column:private_data"`
	ProviderResultURI sql.NullString `gorm:"column:provider_result_uri"`
}

func useGeminiTaskSubmitTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	previousMemoryCache := common.MemoryCacheEnabled
	previousBatchUpdate := common.BatchUpdateEnabled
	previousLogConsume := common.LogConsumeEnabled
	previousDataExport := common.DataExportEnabled

	common.SetDatabaseTypes(
		common.DatabaseTypeSQLite,
		common.DatabaseTypeSQLite,
	)
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false
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
		&model.Token{},
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
		common.DataExportEnabled = previousDataExport
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func newGeminiTaskSubmitTestContext(
	t *testing.T,
	db *gorm.DB,
	upstreamURL string,
	credential string,
	requestID string,
) (*gin.Context, *httptest.ResponseRecorder, model.User, model.Token) {
	t.Helper()

	user := model.User{
		Username: "gemini-submit-user-" + strings.ReplaceAll(t.Name(), "/", "-"),
		Password: "not-used-in-test",
		Quota:    common.MaxQuota,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{
		UserId:      user.Id,
		Key:         "gemini-submit-token-" + strings.ReplaceAll(t.Name(), "/", "-"),
		Name:        "gemini-submit-token-name-" + strings.ReplaceAll(t.Name(), "/", "-"),
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: common.MaxQuota,
		Group:       "default",
	}
	require.NoError(t, db.Create(&token).Error)

	baseURL := upstreamURL
	channel := model.Channel{
		Type:    constant.ChannelTypeGemini,
		Key:     credential,
		Status:  common.ChannelStatusEnabled,
		Name:    "gemini-submit-channel-" + strings.ReplaceAll(t.Name(), "/", "-"),
		BaseURL: &baseURL,
		Group:   "default",
		Models:  "veo-3.0-generate-001",
	}
	require.NoError(t, db.Create(&channel).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		strings.NewReader(
			`{"prompt":"privacy boundary","model":"veo-3.0-generate-001"}`,
		),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set(common.RequestIdKey, requestID)
	context.Set("platform", strconv.Itoa(constant.ChannelTypeGemini))
	context.Set("channel_id", channel.Id)
	context.Set("channel_type", channel.Type)
	context.Set("channel_name", channel.Name)
	context.Set("token_name", token.Name)
	context.Set("username", user.Username)
	common.SetContextKey(context, constant.ContextKeyUserId, user.Id)
	common.SetContextKey(context, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(context, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(context, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(context, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(context, constant.ContextKeyTokenId, token.Id)
	common.SetContextKey(context, constant.ContextKeyTokenKey, token.Key)
	common.SetContextKey(
		context,
		constant.ContextKeyUserSetting,
		dto.UserSetting{BillingPreference: "wallet_only"},
	)
	common.SetContextKey(
		context,
		constant.ContextKeyRequestStartTime,
		time.Now(),
	)
	common.SetContextKey(
		context,
		constant.ContextKeyOriginalModel,
		"veo-3.0-generate-001",
	)
	common.SetContextKey(context, constant.ContextKeyChannelId, channel.Id)
	common.SetContextKey(
		context,
		constant.ContextKeyChannelType,
		channel.Type,
	)
	common.SetContextKey(
		context,
		constant.ContextKeyChannelName,
		channel.Name,
	)
	common.SetContextKey(
		context,
		constant.ContextKeyChannelBaseUrl,
		upstreamURL,
	)
	common.SetContextKey(context, constant.ContextKeyChannelKey, credential)
	t.Cleanup(func() {
		common.CleanupBodyStorage(context)
	})

	return context, recorder, user, token
}

func configureGeminiTaskSubmitDataEncryption(
	t *testing.T,
	keys string,
	activeKeyID string,
	enabled string,
) {
	t.Helper()
	t.Cleanup(func() {
		require.NoError(t, common.InitDataEncryption())
	})
	t.Setenv("DATA_ENCRYPTION_KEYS", keys)
	t.Setenv("DATA_ENCRYPTION_ACTIVE_KEY_ID", activeKeyID)
	t.Setenv("DATA_ENCRYPTION_ENABLE", enabled)
	require.NoError(t, common.InitDataEncryption())
}

func captureGeminiTaskSubmitLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var captured bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultWriter = &captured
	gin.DefaultErrorWriter = &captured
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		gin.DefaultErrorWriter = previousErrorWriter
		common.LogWriterMu.Unlock()
	})
	return &captured
}

func TestRelayTaskPersistsGeminiPublicAndPrivateRepresentations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := useGeminiTaskSubmitTestDB(t)

	const (
		credential        = "persist-credential-sentinel"
		providerPath      = "persist-provider-path-sentinel"
		signedQuery       = "persist-signed-query-sentinel"
		operationSentinel = "persist-operation-name-sentinel"
	)
	rawProviderURI := "https://video.example.test/" + providerPath +
		"?key=" + credential + "&sig=" + signedQuery
	filteredProviderURI := "https://video.example.test/" + providerPath +
		"?sig=" + signedQuery
	upstreamBody := fmt.Sprintf(
		`{"name":"models/veo-3.0-generate-001/operations/%s","done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":%q,"mimeType":"video/mp4"}}]}}}`,
		operationSentinel,
		rawProviderURI,
	)
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			assert.Equal(t, credential, request.Header.Get("x-goog-api-key"))
			assert.Contains(
				t,
				request.URL.Path,
				"/models/veo-3.0-generate-001:predictLongRunning",
			)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(upstreamBody))
		},
	))
	t.Cleanup(upstream.Close)

	context, recorder, _, _ := newGeminiTaskSubmitTestContext(
		t,
		db,
		upstream.URL,
		credential,
		"gemini-submit-privacy-request",
	)

	RelayTask(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var successResponse dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &successResponse))
	expectedSuccessResponse := dto.NewOpenAIVideo()
	expectedSuccessResponse.ID = successResponse.ID
	expectedSuccessResponse.TaskID = successResponse.TaskID
	expectedSuccessResponse.CreatedAt = successResponse.CreatedAt
	expectedSuccessResponse.Model = "veo-3.0-generate-001"
	expectedSuccessJSON, err := common.Marshal(expectedSuccessResponse)
	require.NoError(t, err)
	assert.JSONEq(t, string(expectedSuccessJSON), recorder.Body.String())

	var tasks []model.Task
	require.NoError(t, db.Find(&tasks).Error)
	require.Len(t, tasks, 1)
	task := tasks[0]
	assert.Equal(t, task.TaskID, successResponse.ID)
	assert.Equal(t, task.TaskID, successResponse.TaskID)

	expectedPublicData := `{"done":true,"video":{"url":"` +
		geminitaskresult.ProxyPath(task.TaskID) +
		`","mime_type":"video/mp4"}}`
	assert.Equal(t, expectedPublicData, string(task.Data))

	var rawRow geminiTaskSubmitStoredRow
	require.NoError(t, db.Table("tasks").
		Select("data, private_data, provider_result_uri").
		Where("id = ?", task.ID).
		Scan(&rawRow).Error)
	assert.Equal(t, expectedPublicData, string(rawRow.Data))
	require.True(t, rawRow.ProviderResultURI.Valid)
	assert.True(
		t,
		common.IsDataEncryptionEnvelope(rawRow.ProviderResultURI.String),
	)
	assert.NotContains(t, rawRow.ProviderResultURI.String, providerPath)
	assert.NotContains(t, rawRow.ProviderResultURI.String, signedQuery)
	assert.NotContains(t, rawRow.ProviderResultURI.String, credential)

	openedProviderURI, err := task.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, filteredProviderURI, openedProviderURI)

	var logs []model.Log
	require.NoError(t, db.Find(&logs).Error)
	require.NotEmpty(t, logs)
	logBytes, err := common.Marshal(logs)
	require.NoError(t, err)
	publicSinks := recorder.Body.String() + "\n" +
		string(rawRow.Data) + "\n" +
		string(rawRow.PrivateData) + "\n" +
		string(logBytes)
	assert.Contains(t, recorder.Body.String(), task.TaskID)
	assert.Contains(t, publicSinks, geminitaskresult.ProxyPath(task.TaskID))
	for _, sentinel := range []string{
		rawProviderURI,
		providerPath,
		signedQuery,
		credential,
		operationSentinel,
	} {
		assert.NotContains(t, publicSinks, sentinel)
	}
}

func TestRelayTaskGeminiProtectionFailureReturnsOneErrorAndRefundsOnce(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	configureGeminiTaskSubmitDataEncryption(t, "", "", "false")
	db := useGeminiTaskSubmitTestDB(t)
	capturedLogs := captureGeminiTaskSubmitLogs(t)

	const (
		credential        = "protection-failure-credential-sentinel"
		providerPath      = "protection-failure-provider-path-sentinel"
		signedQuery       = "protection-failure-signed-query-sentinel"
		operationSentinel = "protection-failure-operation-sentinel"
	)
	rawProviderURI := "https://video.example.test/" + providerPath +
		"?key=" + credential + "&sig=" + signedQuery
	upstreamBody := fmt.Sprintf(
		`{"name":"models/veo-3.0-generate-001/operations/%s","done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":%q,"mimeType":"video/mp4"}}]}}}`,
		operationSentinel,
		rawProviderURI,
	)
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			upstreamCalls.Add(1)
			assert.Equal(t, credential, request.Header.Get("x-goog-api-key"))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(upstreamBody))
		},
	))
	t.Cleanup(upstream.Close)

	var refundWrites atomic.Int32
	callbackName := "test:count-gemini-submit-protection-refunds"
	require.NoError(t, db.Callback().Update().After("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Schema == nil ||
				tx.Statement.Schema.Table != "billing_operations" {
				return
			}
			for _, variable := range tx.Statement.Vars {
				if value, ok := variable.(string); ok &&
					value == model.BillingOperationStatusRefunded {
					refundWrites.Add(1)
					return
				}
			}
		}))
	t.Cleanup(func() {
		db.Callback().Update().Remove(callbackName)
	})

	context, recorder, user, token := newGeminiTaskSubmitTestContext(
		t,
		db,
		upstream.URL,
		credential,
		"gemini-submit-protection-failure-request",
	)

	RelayTask(context)

	assert.Equal(t, int32(1), upstreamCalls.Load())
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.JSONEq(
		t,
		`{"code":"task_result_protection_failed","message":"Gemini task result protection failed","data":null}`,
		recorder.Body.String(),
	)
	assert.Equal(
		t,
		1,
		strings.Count(recorder.Body.String(), `"task_result_protection_failed"`),
	)
	assert.NotContains(t, recorder.Body.String(), `"object":"video"`)
	assert.NotContains(t, recorder.Body.String(), `"task_id"`)

	var taskCount int64
	require.NoError(t, db.Model(&model.Task{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount)

	var operations []model.BillingOperation
	require.NoError(t, db.Find(&operations).Error)
	require.Len(t, operations, 1)
	operation := operations[0]
	assert.Equal(t, model.BillingOperationStatusRefunded, operation.Status)
	assert.Zero(t, operation.TaskId)
	assert.Zero(t, operation.ActualQuota)
	assert.Equal(t, int32(1), refundWrites.Load())

	var reloadedUser model.User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, common.MaxQuota, reloadedUser.Quota)
	var reloadedToken model.Token
	require.NoError(t, db.First(&reloadedToken, token.Id).Error)
	assert.Equal(t, common.MaxQuota, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)

	var databaseLogs []model.Log
	require.NoError(t, db.Find(&databaseLogs).Error)
	databaseLogJSON, err := common.Marshal(databaseLogs)
	require.NoError(t, err)
	operationJSON, err := common.Marshal(operation)
	require.NoError(t, err)
	publicSinks := recorder.Body.String() + "\n" +
		capturedLogs.String() + "\n" +
		string(databaseLogJSON) + "\n" +
		string(operationJSON)
	assert.Contains(t, publicSinks, "task_result_protection_failed")
	for _, sentinel := range []string{
		rawProviderURI,
		providerPath,
		signedQuery,
		credential,
		operationSentinel,
	} {
		assert.NotContains(t, publicSinks, sentinel)
	}
}

func TestRelayTaskGeminiPersistenceFailureDoesNotCommitSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := useGeminiTaskSubmitTestDB(t)
	capturedLogs := captureGeminiTaskSubmitLogs(t)

	const (
		credential        = "persistence-failure-credential-sentinel"
		providerPath      = "persistence-failure-provider-path-sentinel"
		signedQuery       = "persistence-failure-signed-query-sentinel"
		operationSentinel = "persistence-failure-operation-sentinel"
	)
	rawProviderURI := "https://video.example.test/" + providerPath +
		"?key=" + credential + "&sig=" + signedQuery
	upstreamBody := fmt.Sprintf(
		`{"name":"models/veo-3.0-generate-001/operations/%s","done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":%q,"mimeType":"video/mp4"}}]}}}`,
		operationSentinel,
		rawProviderURI,
	)
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			assert.Equal(t, credential, request.Header.Get("x-goog-api-key"))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(upstreamBody))
		},
	))
	t.Cleanup(upstream.Close)

	callbackName := "test:fail-gemini-submit-task-create"
	require.NoError(t, db.Callback().Create().Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Schema != nil &&
				tx.Statement.Schema.Table == "tasks" {
				tx.AddError(errors.New("forced task persistence failure"))
			}
		}))
	t.Cleanup(func() {
		db.Callback().Create().Remove(callbackName)
	})

	context, recorder, user, token := newGeminiTaskSubmitTestContext(
		t,
		db,
		upstream.URL,
		credential,
		"gemini-submit-persistence-failure-request",
	)

	RelayTask(context)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.JSONEq(
		t,
		`{"code":"task_persistence_failed","message":"Accepted task could not be persisted","data":null}`,
		recorder.Body.String(),
	)
	assert.NotContains(t, recorder.Body.String(), `"object":"video"`)
	assert.NotContains(t, recorder.Body.String(), `"task_id"`)

	var taskCount int64
	require.NoError(t, db.Model(&model.Task{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount)

	var operations []model.BillingOperation
	require.NoError(t, db.Find(&operations).Error)
	require.Len(t, operations, 1)
	operation := operations[0]
	assert.Equal(t, model.BillingOperationStatusReserved, operation.Status)
	assert.Zero(t, operation.TaskId)
	assert.Greater(t, operation.ReservedQuota, 0)

	var reloadedUser model.User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(
		t,
		common.MaxQuota-operation.ReservedQuota,
		reloadedUser.Quota,
	)
	var reloadedToken model.Token
	require.NoError(t, db.First(&reloadedToken, token.Id).Error)
	assert.Equal(
		t,
		common.MaxQuota-operation.ReservedQuota,
		reloadedToken.RemainQuota,
	)
	assert.EqualValues(t, operation.ReservedQuota, reloadedToken.UsedQuota)

	var databaseLogs []model.Log
	require.NoError(t, db.Find(&databaseLogs).Error)
	databaseLogJSON, err := common.Marshal(databaseLogs)
	require.NoError(t, err)
	publicSinks := recorder.Body.String() + "\n" +
		capturedLogs.String() + "\n" +
		string(databaseLogJSON)
	for _, sentinel := range []string{
		rawProviderURI,
		providerPath,
		signedQuery,
		credential,
		operationSentinel,
	} {
		assert.NotContains(t, publicSinks, sentinel)
	}
}
