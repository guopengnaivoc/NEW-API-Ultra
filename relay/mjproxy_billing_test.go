package relay

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type midjourneyBillingFixture struct {
	db                    *gorm.DB
	user                  model.User
	token                 model.Token
	channel               model.Channel
	upstream              *httptest.Server
	upstreamURL           string
	requestCount          atomic.Int64
	reservationSeenBefore atomic.Bool
}

func setupMidjourneyBillingTest(
	t *testing.T,
	userQuota int,
	tokenQuota int,
	responseStatus int,
	responseBody string,
) *midjourneyBillingFixture {
	t.Helper()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousType := common.MainDatabaseType()
	previousRedis := common.RedisEnabled
	previousMemoryCache := common.MemoryCacheEnabled
	previousBatchUpdate := common.BatchUpdateEnabled
	previousLogConsume := common.LogConsumeEnabled
	previousModelPrices := ratio_setting.ModelPrice2JSONString()

	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = false
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.Midjourney{},
		&model.MidjourneyQuotaReservation{},
		&model.Channel{},
		&model.Log{},
	))

	price := float64(30) / float64(common.QuotaPerUnit)
	priceJSON, err := common.Marshal(map[string]float64{
		"mj_imagine": price,
		"swap_face":  price,
	})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(priceJSON)))

	fixture := &midjourneyBillingFixture{db: db}
	fixture.user = model.User{
		Username: "mj-relay-user",
		Password: "not-used-in-test",
		Quota:    userQuota,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&fixture.user).Error)
	fixture.token = model.Token{
		UserId:      fixture.user.Id,
		Key:         "mj-relay-token",
		Name:        "mj-token",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: tokenQuota,
		Group:       "default",
	}
	require.NoError(t, db.Create(&fixture.token).Error)
	fixture.channel = model.Channel{
		Name:   "mj-relay-channel",
		Key:    "mj-channel-secret",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, db.Create(&fixture.channel).Error)

	if responseStatus != 0 {
		fixture.upstream = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			fixture.requestCount.Add(1)
			var currentUser model.User
			var currentToken model.Token
			userErr := db.First(&currentUser, fixture.user.Id).Error
			tokenErr := db.First(&currentToken, fixture.token.Id).Error
			if userErr == nil && tokenErr == nil &&
				currentUser.Quota == userQuota-30 &&
				currentToken.RemainQuota == tokenQuota-30 &&
				currentToken.UsedQuota == 30 {
				fixture.reservationSeenBefore.Store(true)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(responseStatus)
			_, _ = writer.Write([]byte(responseBody))
		}))
		fixture.upstreamURL = fixture.upstream.URL
	}

	t.Cleanup(func() {
		if fixture.upstream != nil {
			fixture.upstream.Close()
		}
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousModelPrices))
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled = previousRedis
		common.MemoryCacheEnabled = previousMemoryCache
		common.BatchUpdateEnabled = previousBatchUpdate
		common.LogConsumeEnabled = previousLogConsume
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	return fixture
}

func (fixture *midjourneyBillingFixture) perform(
	t *testing.T,
	requestID string,
	relayMode int,
	path string,
	body string,
) (*httptest.ResponseRecorder, *relaycommon.RelayInfo, *dto.MidjourneyResponse) {
	t.Helper()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set(common.RequestIdKey, requestID)
	context.Set("base_url", fixture.upstreamURL)
	context.Set("channel_id", fixture.channel.Id)
	context.Set("token_name", fixture.token.Name)
	context.Set("username", fixture.user.Username)
	common.SetContextKey(context, constant.ContextKeyChannelId, fixture.channel.Id)
	common.SetContextKey(context, constant.ContextKeyChannelBaseUrl, fixture.upstreamURL)
	common.SetContextKey(context, constant.ContextKeyChannelKey, fixture.channel.Key)
	common.SetContextKey(context, constant.ContextKeyChannelType, constant.ChannelTypeMidjourney)
	t.Cleanup(func() {
		common.CleanupBodyStorage(context)
	})

	modelName := "mj_imagine"
	if relayMode == relayconstant.RelayModeSwapFace {
		modelName = "swap_face"
	}
	info := &relaycommon.RelayInfo{
		TokenId:         fixture.token.Id,
		TokenKey:        fixture.token.Key,
		UserId:          fixture.user.Id,
		UsingGroup:      "default",
		UserGroup:       "default",
		StartTime:       time.Now(),
		RelayMode:       relayMode,
		OriginModelName: modelName,
		RequestId:       requestID,
	}

	var response *dto.MidjourneyResponse
	if relayMode == relayconstant.RelayModeSwapFace {
		response = RelaySwapFace(context, info)
	} else {
		response = RelayMidjourneySubmit(context, info)
	}
	return recorder, info, response
}

func loadOnlyMidjourneyReservation(t *testing.T, db *gorm.DB) model.MidjourneyQuotaReservation {
	t.Helper()
	var reservations []model.MidjourneyQuotaReservation
	require.NoError(t, db.Find(&reservations).Error)
	require.Len(t, reservations, 1)
	return reservations[0]
}

func assertAcceptedMidjourneyTaskPersistenceFailureIsLogged(
	t *testing.T,
	relayMode int,
	path string,
	body string,
	upstreamBody string,
) {
	t.Helper()

	fixture := setupMidjourneyBillingTest(
		t,
		100,
		100,
		http.StatusOK,
		upstreamBody,
	)
	callbackName := "test:fail_midjourney_task_create:" + strings.ReplaceAll(t.Name(), "/", "_")
	require.NoError(t, fixture.db.Callback().Create().Before("gorm:create").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Schema != nil &&
				tx.Statement.Schema.Name == "Midjourney" {
				_ = tx.AddError(errors.New("forced Midjourney task insert failure"))
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, fixture.db.Callback().Create().Remove(callbackName))
	})

	var logOutput bytes.Buffer
	common.LogWriterMu.Lock()
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logOutput
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = previousErrorWriter
		common.LogWriterMu.Unlock()
	})

	requestID := "accepted-task-persistence-failure"
	_, _, response := fixture.perform(t, requestID, relayMode, path, body)
	require.NotNil(t, response)
	assert.Equal(t, "insert_midjourney_task_failed", response.Description)
	assert.EqualValues(t, 1, fixture.requestCount.Load())

	reservation := loadOnlyMidjourneyReservation(t, fixture.db)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusReserved, reservation.Status)
	assert.Zero(t, reservation.MidjourneyTaskId)
	var taskCount int64
	require.NoError(t, fixture.db.Model(&model.Midjourney{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount)

	logged := logOutput.String()
	assert.Contains(t, logged, requestID)
	assert.Contains(t, logged, fmt.Sprintf("reservation_id=%d", reservation.Id))
	assert.Contains(t, logged, fmt.Sprintf("user_id=%d", fixture.user.Id))
	assert.Contains(t, logged, fmt.Sprintf("token_id=%d", fixture.token.Id))
	assert.Contains(t, logged, "quota=30")
	assert.Contains(t, logged, "forced Midjourney task insert failure")
}

func TestRelayMidjourneyAcceptedTaskPersistenceFailureLogsRetainedReservation(t *testing.T) {
	assertAcceptedMidjourneyTaskPersistenceFailureIsLogged(
		t,
		relayconstant.RelayModeMidjourneyImagine,
		"/mj/submit/imagine",
		`{"prompt":"accepted persistence failure"}`,
		`{"code":1,"description":"submitted","result":"mj-task-unlinked"}`,
	)
}

func TestRelaySwapFaceAcceptedTaskPersistenceFailureLogsRetainedReservation(t *testing.T) {
	assertAcceptedMidjourneyTaskPersistenceFailureIsLogged(
		t,
		relayconstant.RelayModeSwapFace,
		"/mj/insight-face/swap",
		`{"sourceBase64":"source","targetBase64":"target"}`,
		`{"code":1,"description":"submitted","result":"swap-task-unlinked"}`,
	)
}

func TestRelayMidjourneySubmitInsufficientWalletDoesNotCallUpstream(t *testing.T) {
	fixture := setupMidjourneyBillingTest(
		t,
		20,
		100,
		http.StatusOK,
		`{"code":1,"description":"submitted","result":"mj-task-wallet"}`,
	)

	_, _, response := fixture.perform(
		t,
		"wallet-insufficient",
		relayconstant.RelayModeMidjourneyImagine,
		"/mj/submit/imagine",
		`{"prompt":"wallet test"}`,
	)
	require.NotNil(t, response)
	assert.Equal(t, "quota_not_enough", response.Description)
	assert.Zero(t, fixture.requestCount.Load())

	var count int64
	require.NoError(t, fixture.db.Model(&model.MidjourneyQuotaReservation{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestRelayMidjourneySubmitInsufficientTokenDoesNotCallUpstream(t *testing.T) {
	fixture := setupMidjourneyBillingTest(
		t,
		100,
		20,
		http.StatusOK,
		`{"code":1,"description":"submitted","result":"mj-task-token"}`,
	)

	_, _, response := fixture.perform(
		t,
		"token-insufficient",
		relayconstant.RelayModeMidjourneyImagine,
		"/mj/submit/imagine",
		`{"prompt":"token test"}`,
	)
	require.NotNil(t, response)
	assert.Equal(t, "quota_not_enough", response.Description)
	assert.Zero(t, fixture.requestCount.Load())

	var currentUser model.User
	require.NoError(t, fixture.db.First(&currentUser, fixture.user.Id).Error)
	assert.Equal(t, 100, currentUser.Quota)
}

func TestRelayMidjourneySubmitAcceptedResponseLinksReservation(t *testing.T) {
	fixture := setupMidjourneyBillingTest(
		t,
		100,
		100,
		http.StatusOK,
		`{"code":1,"description":"submitted","result":"mj-task-accepted"}`,
	)

	_, _, response := fixture.perform(
		t,
		"accepted-request",
		relayconstant.RelayModeMidjourneyImagine,
		"/mj/submit/imagine",
		`{"prompt":"accepted test"}`,
	)
	require.Nil(t, response)
	assert.EqualValues(t, 1, fixture.requestCount.Load())
	assert.True(t, fixture.reservationSeenBefore.Load())

	reservation := loadOnlyMidjourneyReservation(t, fixture.db)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusReserved, reservation.Status)
	assert.NotZero(t, reservation.MidjourneyTaskId)

	var task model.Midjourney
	require.NoError(t, fixture.db.First(&task, reservation.MidjourneyTaskId).Error)
	assert.Equal(t, reservation.Id, task.QuotaReservationId)
	assert.Equal(t, "mj-task-accepted", task.MjId)
}

func TestRelayMidjourneySubmitRejectedResponseRefundsReservation(t *testing.T) {
	fixture := setupMidjourneyBillingTest(
		t,
		100,
		100,
		http.StatusOK,
		`{"code":23,"description":"queue full","result":""}`,
	)

	_, _, response := fixture.perform(
		t,
		"rejected-request",
		relayconstant.RelayModeMidjourneyImagine,
		"/mj/submit/imagine",
		`{"prompt":"rejected test"}`,
	)
	require.Nil(t, response)

	reservation := loadOnlyMidjourneyReservation(t, fixture.db)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusRefunded, reservation.Status)

	var currentUser model.User
	var currentToken model.Token
	require.NoError(t, fixture.db.First(&currentUser, fixture.user.Id).Error)
	require.NoError(t, fixture.db.First(&currentToken, fixture.token.Id).Error)
	assert.Equal(t, 100, currentUser.Quota)
	assert.Equal(t, 100, currentToken.RemainQuota)
	assert.Zero(t, currentToken.UsedQuota)

	var task model.Midjourney
	require.NoError(t, fixture.db.First(&task, reservation.MidjourneyTaskId).Error)
	assert.Equal(t, "FAILURE", task.Status)
	assert.Equal(t, "100%", task.Progress)
}

func TestRelayMidjourneySubmitImmediateSuccessSettlesReservation(t *testing.T) {
	fixture := setupMidjourneyBillingTest(
		t,
		100,
		100,
		http.StatusOK,
		`{"code":21,"description":"exists","result":"mj-task-existing","properties":{"status":"SUCCESS","imageUrl":"https://example.invalid/image.png"}}`,
	)

	recorder, _, response := fixture.perform(
		t,
		"immediate-success",
		relayconstant.RelayModeMidjourneyImagine,
		"/mj/submit/imagine",
		`{"prompt":"immediate success"}`,
	)
	require.Nil(t, response)
	assert.Contains(t, recorder.Body.String(), `"code":1`)

	reservation := loadOnlyMidjourneyReservation(t, fixture.db)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusSettled, reservation.Status)
	var task model.Midjourney
	require.NoError(t, fixture.db.First(&task, reservation.MidjourneyTaskId).Error)
	assert.Equal(t, "SUCCESS", task.Status)
	assert.Equal(t, "100%", task.Progress)
}

func TestRelayMidjourneySubmitAmbiguousDispatchKeepsReservation(t *testing.T) {
	fixture := setupMidjourneyBillingTest(t, 100, 100, 0, "")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fixture.upstreamURL = "http://" + listener.Addr().String()
	require.NoError(t, listener.Close())

	_, _, response := fixture.perform(
		t,
		"ambiguous-dispatch",
		relayconstant.RelayModeMidjourneyImagine,
		"/mj/submit/imagine",
		`{"prompt":"ambiguous dispatch"}`,
	)
	require.NotNil(t, response)
	assert.Equal(t, "do_request_failed", response.Description)

	reservation := loadOnlyMidjourneyReservation(t, fixture.db)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusReserved, reservation.Status)
	var currentUser model.User
	require.NoError(t, fixture.db.First(&currentUser, fixture.user.Id).Error)
	assert.Equal(t, 70, currentUser.Quota)
}

func TestRelayMidjourneySubmitEmptyResponseKeepsReservation(t *testing.T) {
	fixture := setupMidjourneyBillingTest(t, 100, 100, http.StatusOK, "")

	_, _, response := fixture.perform(
		t,
		"empty-response",
		relayconstant.RelayModeMidjourneyImagine,
		"/mj/submit/imagine",
		`{"prompt":"empty response"}`,
	)
	require.NotNil(t, response)
	assert.Equal(t, "empty_response_body", response.Description)

	reservation := loadOnlyMidjourneyReservation(t, fixture.db)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusReserved, reservation.Status)
	var currentUser model.User
	var currentToken model.Token
	require.NoError(t, fixture.db.First(&currentUser, fixture.user.Id).Error)
	require.NoError(t, fixture.db.First(&currentToken, fixture.token.Id).Error)
	assert.Equal(t, 70, currentUser.Quota)
	assert.Equal(t, 70, currentToken.RemainQuota)
	assert.Equal(t, 30, currentToken.UsedQuota)
}

func TestRelaySwapFaceReservesBeforeUpstream(t *testing.T) {
	fixture := setupMidjourneyBillingTest(
		t,
		100,
		100,
		http.StatusOK,
		`{"code":1,"description":"submitted","result":"swap-task"}`,
	)

	_, _, response := fixture.perform(
		t,
		"swap-face-request",
		relayconstant.RelayModeSwapFace,
		"/mj/insight-face/swap",
		`{"sourceBase64":"source","targetBase64":"target"}`,
	)
	require.Nil(t, response)
	assert.True(t, fixture.reservationSeenBefore.Load())
	reservation := loadOnlyMidjourneyReservation(t, fixture.db)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusReserved, reservation.Status)
}

func TestRelayDuplicateReservationDoesNotCallUpstreamTwice(t *testing.T) {
	fixture := setupMidjourneyBillingTest(
		t,
		100,
		100,
		http.StatusOK,
		`{"code":1,"description":"submitted","result":"duplicate-task"}`,
	)

	_, _, firstResponse := fixture.perform(
		t,
		"duplicate-request",
		relayconstant.RelayModeMidjourneyImagine,
		"/mj/submit/imagine",
		`{"prompt":"first invocation"}`,
	)
	require.Nil(t, firstResponse)

	_, _, secondResponse := fixture.perform(
		t,
		"duplicate-request",
		relayconstant.RelayModeMidjourneyImagine,
		"/mj/submit/imagine",
		`{"prompt":"second invocation"}`,
	)
	require.NotNil(t, secondResponse)
	assert.Equal(t, "duplicate_billing_operation", secondResponse.Description)
	assert.EqualValues(t, 1, fixture.requestCount.Load())
}
