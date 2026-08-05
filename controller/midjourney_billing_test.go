package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type midjourneyPollingFixture struct {
	db          *gorm.DB
	user        model.User
	token       model.Token
	channel     model.Channel
	reservation *model.MidjourneyQuotaReservation
	task        model.Midjourney
	upstream    *httptest.Server
}

func setupMidjourneyPollingTest(
	t *testing.T,
	mjID string,
	responseBody string,
) *midjourneyPollingFixture {
	t.Helper()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	previousMemoryCache := common.MemoryCacheEnabled
	previousBatchUpdate := common.BatchUpdateEnabled
	previousLogConsume := common.LogConsumeEnabled
	previousDataExport := common.DataExportEnabled

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false
	service.InitHttpClient()

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

	fixture := &midjourneyPollingFixture{db: db}
	fixture.user = model.User{
		Username: "mj-polling-user",
		Password: "not-used-in-test",
		Quota:    100,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&fixture.user).Error)
	fixture.token = model.Token{
		UserId:      fixture.user.Id,
		Key:         "mj-polling-token",
		Name:        "mj-token",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
		Group:       "default",
	}
	require.NoError(t, db.Create(&fixture.token).Error)

	fixture.upstream = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/mj/task/list-by-condition", request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(responseBody))
	}))
	baseURL := fixture.upstream.URL
	fixture.channel = model.Channel{
		Name:    "mj-polling-channel",
		Key:     "mj-channel-secret",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
	}
	require.NoError(t, db.Create(&fixture.channel).Error)

	fixture.reservation, _, err = model.ReserveMidjourneyQuota(
		model.MidjourneyQuotaReservationRequest{
			RequestId:    "polling-request",
			UserId:       fixture.user.Id,
			TokenId:      fixture.token.Id,
			Quota:        30,
			TokenCharged: true,
		},
	)
	require.NoError(t, err)
	fixture.task = model.Midjourney{
		UserId:     fixture.user.Id,
		Code:       1,
		Action:     "IMAGINE",
		MjId:       mjID,
		SubmitTime: time.Now().UnixMilli(),
		Status:     "SUBMITTED",
		Progress:   "0%",
		ChannelId:  fixture.channel.Id,
		Quota:      30,
	}
	_, err = model.CreateMidjourneyTaskWithReservation(
		&fixture.task,
		fixture.reservation.Id,
		model.MidjourneyBillingOutcomePending,
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		fixture.upstream.Close()
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
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

	return fixture
}

func (fixture *midjourneyPollingFixture) loadBillingState(t *testing.T) (
	model.User,
	model.Token,
	model.Midjourney,
	model.MidjourneyQuotaReservation,
) {
	t.Helper()

	var user model.User
	var token model.Token
	var task model.Midjourney
	var reservation model.MidjourneyQuotaReservation
	require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
	require.NoError(t, fixture.db.Unscoped().First(&token, fixture.token.Id).Error)
	require.NoError(t, fixture.db.First(&task, fixture.task.Id).Error)
	require.NoError(t, fixture.db.First(&reservation, fixture.reservation.Id).Error)
	return user, token, task, reservation
}

func TestMidjourneyPollingFailureRefundsReservationOnce(t *testing.T) {
	fixture := setupMidjourneyPollingTest(
		t,
		"polling-failure",
		`[{"id":"polling-failure","progress":"100%","status":"FAILURE","failReason":"upstream failed"}]`,
	)

	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	runMidjourneyTaskUpdateOnce(context.Background(), nil)

	user, token, task, reservation := fixture.loadBillingState(t)
	assert.Equal(t, 100, user.Quota)
	assert.Equal(t, 100, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, "FAILURE", task.Status)
	assert.Equal(t, "100%", task.Progress)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusRefunded, reservation.Status)

	var refundLogs int64
	require.NoError(t, fixture.db.Model(&model.Log{}).
		Where("type = ? AND user_id = ?", model.LogTypeRefund, fixture.user.Id).
		Count(&refundLogs).Error)
	assert.EqualValues(t, 1, refundLogs)
}

func TestMidjourneyPollingSuccessSettlesReservation(t *testing.T) {
	fixture := setupMidjourneyPollingTest(
		t,
		"polling-success",
		`[{"id":"polling-success","progress":"100%","status":"SUCCESS","imageUrl":"https://example.invalid/image.png"}]`,
	)

	runMidjourneyTaskUpdateOnce(context.Background(), nil)

	user, token, task, reservation := fixture.loadBillingState(t)
	assert.Equal(t, 70, user.Quota)
	assert.Equal(t, 70, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)
	assert.Equal(t, "SUCCESS", task.Status)
	assert.Equal(t, "100%", task.Progress)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusSettled, reservation.Status)
}

func TestMidjourneyPollingFailReasonMarksTaskFailureBeforeRefund(t *testing.T) {
	fixture := setupMidjourneyPollingTest(
		t,
		"polling-fail-reason",
		`[{"id":"polling-fail-reason","progress":"50%","status":"IN_PROGRESS","failReason":"moderation failed"}]`,
	)

	runMidjourneyTaskUpdateOnce(context.Background(), nil)

	user, token, task, reservation := fixture.loadBillingState(t)
	assert.Equal(t, 100, user.Quota)
	assert.Equal(t, 100, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, "FAILURE", task.Status)
	assert.Equal(t, "100%", task.Progress)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusRefunded, reservation.Status)
}

func TestMidjourneyPollingNullTaskRefundsReservation(t *testing.T) {
	fixture := setupMidjourneyPollingTest(t, "", `[]`)

	runMidjourneyTaskUpdateOnce(context.Background(), nil)

	user, token, task, reservation := fixture.loadBillingState(t)
	assert.Equal(t, 100, user.Quota)
	assert.Equal(t, 100, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, "FAILURE", task.Status)
	assert.Equal(t, "100%", task.Progress)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusRefunded, reservation.Status)
}

func TestMidjourneyPollingMissingChannelRefundsReservation(t *testing.T) {
	fixture := setupMidjourneyPollingTest(t, "missing-channel", `[]`)
	require.NoError(t, fixture.db.Delete(&fixture.channel).Error)

	runMidjourneyTaskUpdateOnce(context.Background(), nil)

	user, token, task, reservation := fixture.loadBillingState(t)
	assert.Equal(t, 100, user.Quota)
	assert.Equal(t, 100, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, "FAILURE", task.Status)
	assert.Equal(t, "100%", task.Progress)
	assert.Contains(t, task.FailReason, "渠道")
	assert.Equal(t, model.MidjourneyQuotaReservationStatusRefunded, reservation.Status)
}

func TestMidjourneyPollingRefundFailureRollsBackTaskState(t *testing.T) {
	fixture := setupMidjourneyPollingTest(
		t,
		"polling-refund-rollback",
		`[{"id":"polling-refund-rollback","progress":"100%","status":"FAILURE","failReason":"upstream failed"}]`,
	)
	require.NoError(t, fixture.db.Unscoped().Delete(&fixture.token).Error)

	runMidjourneyTaskUpdateOnce(context.Background(), nil)

	var user model.User
	var task model.Midjourney
	var reservation model.MidjourneyQuotaReservation
	require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
	require.NoError(t, fixture.db.First(&task, fixture.task.Id).Error)
	require.NoError(t, fixture.db.First(&reservation, fixture.reservation.Id).Error)
	assert.Equal(t, 70, user.Quota)
	assert.Equal(t, "SUBMITTED", task.Status)
	assert.Equal(t, "0%", task.Progress)
	assert.Equal(t, model.MidjourneyQuotaReservationStatusReserved, reservation.Status)
}
