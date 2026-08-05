package model

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMidjourneyQuotaReservationSchema(t *testing.T) {
	truncateTables(t)

	assert.True(t, DB.Migrator().HasTable(&MidjourneyQuotaReservation{}))
	assert.True(t, DB.Migrator().HasColumn(&Midjourney{}, "quota_reservation_id"))

	reservation := MidjourneyQuotaReservation{
		RequestId:    "schema-request",
		UserId:       101,
		TokenId:      202,
		Quota:        300,
		TokenCharged: true,
		Status:       MidjourneyQuotaReservationStatusReserved,
	}
	require.NoError(t, DB.Create(&reservation).Error)

	duplicate := reservation
	duplicate.Id = 0
	assert.Error(t, DB.Create(&duplicate).Error)
}

func seedMidjourneyQuotaAccount(t *testing.T, userQuota, tokenQuota int, unlimited bool) (User, Token) {
	t.Helper()
	truncateTables(t)

	user := User{
		Username: "mj-reservation-user",
		Password: "not-used-in-test",
		Quota:    userQuota,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(&user).Error)

	token := Token{
		UserId:         user.Id,
		Key:            "mj-reservation-token",
		Name:           "mj-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		RemainQuota:    tokenQuota,
		UnlimitedQuota: unlimited,
	}
	require.NoError(t, DB.Create(&token).Error)
	return user, token
}

func reloadMidjourneyQuotaAccount(t *testing.T, userID, tokenID int) (User, Token) {
	t.Helper()
	var user User
	require.NoError(t, DB.First(&user, userID).Error)
	var token Token
	require.NoError(t, DB.First(&token, tokenID).Error)
	return user, token
}

func TestReserveMidjourneyQuotaIsIdempotent(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	input := MidjourneyQuotaReservationRequest{
		RequestId:    "idempotent-request",
		UserId:       user.Id,
		TokenId:      token.Id,
		Quota:        30,
		TokenCharged: true,
	}

	first, created, err := ReserveMidjourneyQuota(input)
	require.NoError(t, err)
	require.True(t, created)

	second, created, err := ReserveMidjourneyQuota(input)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.Id, second.Id)

	var count int64
	require.NoError(t, DB.Model(&MidjourneyQuotaReservation{}).
		Where("request_id = ?", input.RequestId).
		Count(&count).Error)
	assert.EqualValues(t, 1, count)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 70, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)
}

func TestReserveMidjourneyQuotaRollsBackWalletWhenTokenIsInsufficient(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 10, false)

	_, created, err := ReserveMidjourneyQuota(MidjourneyQuotaReservationRequest{
		RequestId:    "token-insufficient",
		UserId:       user.Id,
		TokenId:      token.Id,
		Quota:        30,
		TokenCharged: true,
	})
	require.ErrorIs(t, err, ErrMidjourneyTokenQuotaInsufficient)
	assert.False(t, created)

	var count int64
	require.NoError(t, DB.Model(&MidjourneyQuotaReservation{}).Count(&count).Error)
	assert.Zero(t, count)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 10, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)
}

func TestReserveMidjourneyQuotaRejectsMismatchedRequestId(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	input := MidjourneyQuotaReservationRequest{
		RequestId:    "conflicting-request",
		UserId:       user.Id,
		TokenId:      token.Id,
		Quota:        30,
		TokenCharged: true,
	}
	_, created, err := ReserveMidjourneyQuota(input)
	require.NoError(t, err)
	require.True(t, created)

	input.Quota = 20
	_, created, err = ReserveMidjourneyQuota(input)
	require.ErrorIs(t, err, ErrMidjourneyQuotaReservationConflict)
	assert.False(t, created)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 70, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)
}

func TestReserveMidjourneyQuotaUsesAuthoritativeUnlimitedTokenState(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 5, true)

	reservation, created, err := ReserveMidjourneyQuota(MidjourneyQuotaReservationRequest{
		RequestId:    "unlimited-token",
		UserId:       user.Id,
		TokenId:      token.Id,
		Quota:        30,
		TokenCharged: true,
	})
	require.NoError(t, err)
	require.True(t, created)
	assert.True(t, reservation.TokenUnlimited)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 5, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)
}

func TestReserveMidjourneyQuotaConcurrentCallsCannotOverdraw(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)

	const callers = 10
	start := make(chan struct{})
	results := make(chan error, callers)
	var successes atomic.Int64
	var wait sync.WaitGroup
	wait.Add(callers)

	for index := range callers {
		go func() {
			defer wait.Done()
			<-start
			_, created, err := ReserveMidjourneyQuota(MidjourneyQuotaReservationRequest{
				RequestId:    fmt.Sprintf("concurrent-request-%d", index),
				UserId:       user.Id,
				TokenId:      token.Id,
				Quota:        30,
				TokenCharged: true,
			})
			if err == nil && created {
				successes.Add(1)
			}
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	for err := range results {
		if err == nil {
			continue
		}
		assert.True(t,
			errors.Is(err, ErrMidjourneyWalletQuotaInsufficient) ||
				errors.Is(err, ErrMidjourneyTokenQuotaInsufficient),
			"unexpected reservation error: %v", err,
		)
	}

	assert.EqualValues(t, 3, successes.Load())
	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 10, reloadedUser.Quota)
	assert.Equal(t, 10, reloadedToken.RemainQuota)
	assert.Equal(t, 90, reloadedToken.UsedQuota)
	assert.GreaterOrEqual(t, reloadedUser.Quota, 0)
	assert.GreaterOrEqual(t, reloadedToken.RemainQuota, 0)
}

func TestRefundMidjourneyQuotaReservationIsAtomicAndIdempotent(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	reservation, created, err := ReserveMidjourneyQuota(MidjourneyQuotaReservationRequest{
		RequestId:    "refundable-request",
		UserId:       user.Id,
		TokenId:      token.Id,
		Quota:        30,
		TokenCharged: true,
	})
	require.NoError(t, err)
	require.True(t, created)

	refunded, err := RefundMidjourneyQuotaReservation(reservation.Id)
	require.NoError(t, err)
	assert.True(t, refunded)

	refunded, err = RefundMidjourneyQuotaReservation(reservation.Id)
	require.NoError(t, err)
	assert.False(t, refunded)

	var reloadedReservation MidjourneyQuotaReservation
	require.NoError(t, DB.First(&reloadedReservation, reservation.Id).Error)
	assert.Equal(t, MidjourneyQuotaReservationStatusRefunded, reloadedReservation.Status)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 100, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)
}

func TestSettleMidjourneyQuotaReservationPreventsRefund(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	reservation, created, err := ReserveMidjourneyQuota(MidjourneyQuotaReservationRequest{
		RequestId:    "settled-request",
		UserId:       user.Id,
		TokenId:      token.Id,
		Quota:        30,
		TokenCharged: true,
	})
	require.NoError(t, err)
	require.True(t, created)

	settled, err := SettleMidjourneyQuotaReservation(reservation.Id)
	require.NoError(t, err)
	assert.True(t, settled)

	settled, err = SettleMidjourneyQuotaReservation(reservation.Id)
	require.NoError(t, err)
	assert.False(t, settled)

	refunded, err := RefundMidjourneyQuotaReservation(reservation.Id)
	require.ErrorIs(t, err, ErrMidjourneyQuotaReservationTransition)
	assert.False(t, refunded)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 70, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)
}

func reserveMidjourneyQuotaForTask(t *testing.T, user User, token Token, requestID string) *MidjourneyQuotaReservation {
	t.Helper()
	reservation, created, err := ReserveMidjourneyQuota(MidjourneyQuotaReservationRequest{
		RequestId:    requestID,
		UserId:       user.Id,
		TokenId:      token.Id,
		Quota:        30,
		TokenCharged: true,
	})
	require.NoError(t, err)
	require.True(t, created)
	return reservation
}

func reloadMidjourneyReservation(t *testing.T, reservationID int) MidjourneyQuotaReservation {
	t.Helper()
	var reservation MidjourneyQuotaReservation
	require.NoError(t, DB.First(&reservation, reservationID).Error)
	return reservation
}

func TestCreateMidjourneyTaskWithReservationLinksPendingTask(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	reservation := reserveMidjourneyQuotaForTask(t, user, token, "create-pending")
	task := Midjourney{
		UserId:   user.Id,
		MjId:     "pending-task",
		Action:   "IMAGINE",
		Quota:    30,
		Status:   "",
		Progress: "0%",
	}

	refunded, err := CreateMidjourneyTaskWithReservation(
		&task,
		reservation.Id,
		MidjourneyBillingOutcomePending,
	)
	require.NoError(t, err)
	assert.False(t, refunded)
	assert.NotZero(t, task.Id)
	assert.Equal(t, reservation.Id, task.QuotaReservationId)

	reloadedReservation := reloadMidjourneyReservation(t, reservation.Id)
	assert.Equal(t, task.Id, reloadedReservation.MidjourneyTaskId)
	assert.Equal(t, MidjourneyQuotaReservationStatusReserved, reloadedReservation.Status)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 70, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)
}

func TestCreateMidjourneyTaskWithReservationSettlesImmediateSuccess(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	reservation := reserveMidjourneyQuotaForTask(t, user, token, "create-success")
	task := Midjourney{
		UserId:   user.Id,
		MjId:     "successful-task",
		Action:   "IMAGINE",
		Quota:    30,
		Status:   "SUCCESS",
		Progress: "100%",
	}

	refunded, err := CreateMidjourneyTaskWithReservation(
		&task,
		reservation.Id,
		MidjourneyBillingOutcomeSuccess,
	)
	require.NoError(t, err)
	assert.False(t, refunded)
	assert.Equal(
		t,
		MidjourneyQuotaReservationStatusSettled,
		reloadMidjourneyReservation(t, reservation.Id).Status,
	)
}

func TestCreateMidjourneyTaskWithReservationRefundsImmediateFailure(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	reservation := reserveMidjourneyQuotaForTask(t, user, token, "create-failure")
	task := Midjourney{
		UserId:     user.Id,
		MjId:       "failed-task",
		Action:     "IMAGINE",
		Quota:      30,
		Status:     "FAILURE",
		Progress:   "100%",
		FailReason: "queue full",
	}

	refunded, err := CreateMidjourneyTaskWithReservation(
		&task,
		reservation.Id,
		MidjourneyBillingOutcomeFailure,
	)
	require.NoError(t, err)
	assert.True(t, refunded)
	assert.Equal(
		t,
		MidjourneyQuotaReservationStatusRefunded,
		reloadMidjourneyReservation(t, reservation.Id).Status,
	)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 100, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)
}

func TestUpdateMidjourneyTaskWithReservationRefundsFailureAtomically(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	reservation := reserveMidjourneyQuotaForTask(t, user, token, "update-failure")
	task := Midjourney{
		UserId:   user.Id,
		MjId:     "polling-failure",
		Action:   "IMAGINE",
		Quota:    30,
		Status:   "",
		Progress: "0%",
	}
	_, err := CreateMidjourneyTaskWithReservation(
		&task,
		reservation.Id,
		MidjourneyBillingOutcomePending,
	)
	require.NoError(t, err)

	task.Status = "FAILURE"
	task.Progress = "100%"
	task.FailReason = "upstream failure"
	won, refunded, err := task.UpdateWithQuotaReservation(
		"",
		MidjourneyBillingOutcomeFailure,
	)
	require.NoError(t, err)
	assert.True(t, won)
	assert.True(t, refunded)

	var reloadedTask Midjourney
	require.NoError(t, DB.First(&reloadedTask, task.Id).Error)
	assert.Equal(t, "FAILURE", reloadedTask.Status)
	assert.Equal(t, "100%", reloadedTask.Progress)
	assert.Equal(
		t,
		MidjourneyQuotaReservationStatusRefunded,
		reloadMidjourneyReservation(t, reservation.Id).Status,
	)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 100, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)
}

func TestUpdateMidjourneyTaskWithReservationRollsBackWhenRefundFails(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	reservation := reserveMidjourneyQuotaForTask(t, user, token, "refund-rollback")
	task := Midjourney{
		UserId:   user.Id,
		MjId:     "refund-rollback-task",
		Action:   "IMAGINE",
		Quota:    30,
		Status:   "",
		Progress: "0%",
	}
	_, err := CreateMidjourneyTaskWithReservation(
		&task,
		reservation.Id,
		MidjourneyBillingOutcomePending,
	)
	require.NoError(t, err)
	require.NoError(t, DB.Unscoped().Delete(&token).Error)

	task.Status = "FAILURE"
	task.Progress = "100%"
	won, refunded, err := task.UpdateWithQuotaReservation(
		"",
		MidjourneyBillingOutcomeFailure,
	)
	require.ErrorIs(t, err, ErrMidjourneyQuotaReservationTokenInvalid)
	assert.False(t, won)
	assert.False(t, refunded)

	var reloadedTask Midjourney
	require.NoError(t, DB.First(&reloadedTask, task.Id).Error)
	assert.Empty(t, reloadedTask.Status)
	assert.Equal(t, "0%", reloadedTask.Progress)
	assert.Equal(
		t,
		MidjourneyQuotaReservationStatusReserved,
		reloadMidjourneyReservation(t, reservation.Id).Status,
	)

	var reloadedUser User
	require.NoError(t, DB.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 70, reloadedUser.Quota)
}

func TestUpdateMidjourneyTaskWithReservationSettlesSuccess(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	reservation := reserveMidjourneyQuotaForTask(t, user, token, "update-success")
	task := Midjourney{
		UserId:   user.Id,
		MjId:     "polling-success",
		Action:   "IMAGINE",
		Quota:    30,
		Status:   "",
		Progress: "0%",
	}
	_, err := CreateMidjourneyTaskWithReservation(
		&task,
		reservation.Id,
		MidjourneyBillingOutcomePending,
	)
	require.NoError(t, err)

	task.Status = "SUCCESS"
	task.Progress = "100%"
	won, refunded, err := task.UpdateWithQuotaReservation(
		"",
		MidjourneyBillingOutcomeSuccess,
	)
	require.NoError(t, err)
	assert.True(t, won)
	assert.False(t, refunded)
	assert.Equal(
		t,
		MidjourneyQuotaReservationStatusSettled,
		reloadMidjourneyReservation(t, reservation.Id).Status,
	)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 70, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)
}

func TestUpdateMidjourneyTaskWithReservationCASLoserDoesNotRefund(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	reservation := reserveMidjourneyQuotaForTask(t, user, token, "cas-loser")
	task := Midjourney{
		UserId:   user.Id,
		MjId:     "cas-loser-task",
		Action:   "IMAGINE",
		Quota:    30,
		Status:   "",
		Progress: "0%",
	}
	_, err := CreateMidjourneyTaskWithReservation(
		&task,
		reservation.Id,
		MidjourneyBillingOutcomePending,
	)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Midjourney{}).
		Where("id = ?", task.Id).
		Update("status", "SUCCESS").Error)

	task.Status = "FAILURE"
	task.Progress = "100%"
	won, refunded, err := task.UpdateWithQuotaReservation(
		"",
		MidjourneyBillingOutcomeFailure,
	)
	require.NoError(t, err)
	assert.False(t, won)
	assert.False(t, refunded)
	assert.Equal(
		t,
		MidjourneyQuotaReservationStatusReserved,
		reloadMidjourneyReservation(t, reservation.Id).Status,
	)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 70, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)
}

func TestUpdateLegacyMidjourneyTaskRefundsWalletInTaskTransaction(t *testing.T) {
	user, token := seedMidjourneyQuotaAccount(t, 100, 100, false)
	require.NoError(t, DB.Model(&User{}).
		Where("id = ?", user.Id).
		Update("quota", gorm.Expr("quota - ?", 30)).Error)
	task := Midjourney{
		UserId:   user.Id,
		MjId:     "legacy-task",
		Action:   "IMAGINE",
		Quota:    30,
		Status:   "",
		Progress: "0%",
	}
	require.NoError(t, DB.Create(&task).Error)

	task.Status = "FAILURE"
	task.Progress = "100%"
	won, refunded, err := task.UpdateWithQuotaReservation(
		"",
		MidjourneyBillingOutcomeFailure,
	)
	require.NoError(t, err)
	assert.True(t, won)
	assert.True(t, refunded)

	reloadedUser, reloadedToken := reloadMidjourneyQuotaAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 100, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)
}
