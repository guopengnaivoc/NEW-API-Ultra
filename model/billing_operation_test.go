package model

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedBillingOperationAccount(t *testing.T, userQuota, tokenQuota int) (User, Token) {
	t.Helper()
	truncateTables(t)

	user := User{
		Username: "billing-operation-user",
		Password: "not-used-in-test",
		Quota:    userQuota,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(&user).Error)

	token := Token{
		UserId:      user.Id,
		Key:         "billing-operation-token",
		Name:        "billing-operation-token",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: tokenQuota,
	}
	require.NoError(t, DB.Create(&token).Error)
	return user, token
}

func reloadBillingOperationAccount(t *testing.T, userID, tokenID int) (User, Token) {
	t.Helper()

	var user User
	require.NoError(t, DB.Unscoped().First(&user, userID).Error)
	var token Token
	require.NoError(t, DB.Unscoped().First(&token, tokenID).Error)
	return user, token
}

func reserveWalletBillingOperation(
	t *testing.T,
	requestID string,
	user User,
	token Token,
	quota int,
) *BillingOperation {
	t.Helper()

	operation, created, err := ReserveBillingOperation(BillingOperationReserveRequest{
		RequestId:     requestID,
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TokenCharged:  true,
		FundingSource: BillingOperationFundingWallet,
		Quota:         quota,
	})
	require.NoError(t, err)
	require.True(t, created)
	return operation
}

func TestReserveBillingOperationIsAtomicAndIdempotent(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	input := BillingOperationReserveRequest{
		RequestId:     "billing-reserve-idempotent",
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TokenCharged:  true,
		FundingSource: BillingOperationFundingWallet,
		Quota:         30,
	}

	operation, created, err := ReserveBillingOperation(input)
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, BillingOperationStatusReserved, operation.Status)
	assert.Equal(t, 30, operation.ReservedQuota)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 50, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)

	replayed, created, err := ReserveBillingOperation(input)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, operation.Id, replayed.Id)

	reloadedUser, reloadedToken = reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 50, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)

	input.Quota = 31
	replayed, created, err = ReserveBillingOperation(input)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, operation.Id, replayed.Id)
	assert.Equal(t, 30, replayed.ReservedQuota)
}

func TestReserveBillingOperationRollsBackWalletWhenTokenIsInvalid(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)

	_, _, err := ReserveBillingOperation(BillingOperationReserveRequest{
		RequestId:     "billing-reserve-invalid-token",
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      "wrong-token-key",
		TokenCharged:  true,
		FundingSource: BillingOperationFundingWallet,
		Quota:         30,
	})
	require.ErrorIs(t, err, ErrBillingOperationTokenInvalid)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 80, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)

	var operationCount int64
	require.NoError(t, DB.Model(&BillingOperation{}).Count(&operationCount).Error)
	assert.Zero(t, operationCount)
}

func TestBillingOperationSettlementIsAtomicAndIdempotent(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(t, "billing-settle", user, token, 30)

	operation, changed, err := AdjustBillingOperationReservation(operation.Id, 40)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, 40, operation.ReservedQuota)

	operation, changed, err = SettleBillingOperation(operation.Id, 25)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, BillingOperationStatusSettled, operation.Status)
	assert.Equal(t, 25, operation.ActualQuota)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 75, reloadedUser.Quota)
	assert.Equal(t, 55, reloadedToken.RemainQuota)
	assert.Equal(t, 25, reloadedToken.UsedQuota)

	_, changed, err = SettleBillingOperation(operation.Id, 25)
	require.NoError(t, err)
	assert.False(t, changed)

	_, _, err = SettleBillingOperation(operation.Id, 26)
	require.ErrorIs(t, err, ErrBillingOperationConflict)
	_, _, err = RefundBillingOperation(operation.Id)
	require.ErrorIs(t, err, ErrBillingOperationTransition)

	reloadedUser, reloadedToken = reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 75, reloadedUser.Quota)
	assert.Equal(t, 55, reloadedToken.RemainQuota)
	assert.Equal(t, 25, reloadedToken.UsedQuota)
}

func TestSettleBillingOperationCapsToAvailableFundingAndTokenCapacity(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 40, 35)
	operation := reserveWalletBillingOperation(t, "billing-limited-settle", user, token, 30)

	operation, changed, err := SettleBillingOperation(operation.Id, 50)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, BillingOperationStatusSettled, operation.Status)
	assert.Equal(t, 50, operation.RequestedQuota)
	assert.Equal(t, 35, operation.ActualQuota)
	assert.True(t, operation.SettlementLimited)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 5, reloadedUser.Quota)
	assert.Zero(t, reloadedToken.RemainQuota)
	assert.Equal(t, 35, reloadedToken.UsedQuota)

	replayed, changed, err := SettleBillingOperation(operation.Id, 50)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, 35, replayed.ActualQuota)
	reloadedUser, reloadedToken = reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 5, reloadedUser.Quota)
	assert.Zero(t, reloadedToken.RemainQuota)
	assert.Equal(t, 35, reloadedToken.UsedQuota)
}

func TestSettleBillingOperationCapsToWalletCapacity(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 35, 100)
	operation := reserveWalletBillingOperation(t, "billing-wallet-limited-settle", user, token, 30)

	operation, changed, err := SettleBillingOperation(operation.Id, 50)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, 35, operation.ActualQuota)
	assert.True(t, operation.SettlementLimited)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Zero(t, reloadedUser.Quota)
	assert.Equal(t, 65, reloadedToken.RemainQuota)
	assert.Equal(t, 35, reloadedToken.UsedQuota)
}

func TestTrustedZeroReserveSettlesAtomically(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(t, "billing-zero-reserve", user, token, 0)

	operation, changed, err := SettleBillingOperation(operation.Id, 20)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, 20, operation.ActualQuota)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 80, reloadedUser.Quota)
	assert.Equal(t, 60, reloadedToken.RemainQuota)
	assert.Equal(t, 20, reloadedToken.UsedQuota)
}

func TestRefundBillingOperationCanRestoreSoftDeletedToken(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(t, "billing-soft-deleted-token", user, token, 30)
	require.NoError(t, DB.Delete(&token).Error)

	operation, changed, err := RefundBillingOperation(operation.Id)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, BillingOperationStatusRefunded, operation.Status)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 80, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)

	_, changed, err = RefundBillingOperation(operation.Id)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestRefundBillingOperationCanRestoreSoftDeletedUser(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(t, "billing-soft-deleted-user", user, token, 30)
	require.NoError(t, DB.Delete(&user).Error)

	operation, changed, err := RefundBillingOperation(operation.Id)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, BillingOperationStatusRefunded, operation.Status)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 80, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)
}

func TestRefundBillingOperationAbandonsHardDeletedOwnerWithoutRetryLoop(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(t, "billing-hard-deleted-owner", user, token, 30)

	require.NoError(t, DB.Unscoped().Delete(&token).Error)
	require.NoError(t, DB.Unscoped().Delete(&user).Error)

	operation, changed, err := RefundBillingOperation(operation.Id)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, BillingOperationStatusAbandoned, operation.Status)
	assert.Equal(t, 30, operation.ActualQuota)
	assert.NotEmpty(t, operation.FailureReason)

	operation, changed, err = RefundBillingOperation(operation.Id)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, BillingOperationStatusAbandoned, operation.Status)
}

func TestAbandonedBillingOperationDoesNotPartiallyRefundToken(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(t, "billing-abandon-no-partial", user, token, 30)
	require.NoError(t, DB.Unscoped().Delete(&user).Error)

	operation, changed, err := RefundBillingOperation(operation.Id)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, BillingOperationStatusAbandoned, operation.Status)

	var reloadedToken Token
	require.NoError(t, DB.Unscoped().First(&reloadedToken, token.Id).Error)
	assert.Equal(t, 50, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)
}

func TestNewUnlimitedTokenOperationLeavesRemainQuotaUntouched(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	require.NoError(t, DB.Model(&Token{}).
		Where("id = ?", token.Id).
		Update("unlimited_quota", true).Error)
	token.UnlimitedQuota = true

	operation := reserveWalletBillingOperation(t, "billing-unlimited-token", user, token, 30)
	assert.True(t, operation.TokenUnlimited)
	assert.False(t, operation.TokenRemainCharged)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 80, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)

	_, changed, err := RefundBillingOperation(operation.Id)
	require.NoError(t, err)
	require.True(t, changed)
	reloadedUser, reloadedToken = reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 80, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)
}

func TestUnlimitedTokenDoesNotLimitSettlementCapacity(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 40, 0)
	require.NoError(t, DB.Model(&Token{}).
		Where("id = ?", token.Id).
		Update("unlimited_quota", true).Error)
	token.UnlimitedQuota = true

	operation := reserveWalletBillingOperation(t, "billing-unlimited-settle", user, token, 30)
	operation, changed, err := SettleBillingOperation(operation.Id, 40)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, 40, operation.ActualQuota)
	assert.False(t, operation.SettlementLimited)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Zero(t, reloadedUser.Quota)
	assert.Zero(t, reloadedToken.RemainQuota)
	assert.Equal(t, 40, reloadedToken.UsedQuota)
}

func TestSettleBillingOperationCapsToTokenUsedQuotaCapacity(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(
		t,
		"billing-token-used-capacity",
		user,
		token,
		30,
	)
	require.NoError(t, DB.Model(&Token{}).
		Where("id = ?", token.Id).
		Update("used_quota", common.MaxQuota-5).Error)

	operation, changed, err := SettleBillingOperation(operation.Id, 50)
	require.NoError(t, err)
	require.True(t, changed)
	assert.True(t, operation.SettlementLimited)
	assert.Equal(t, 50, operation.RequestedQuota)
	assert.Equal(t, 35, operation.ActualQuota)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(
		t,
		user.Id,
		token.Id,
	)
	assert.Equal(t, 65, reloadedUser.Quota)
	assert.Equal(t, 45, reloadedToken.RemainQuota)
	assert.Equal(t, common.MaxQuota, reloadedToken.UsedQuota)
}

func TestCreateTaskWithBillingOperationRollsBackReservationAdjustmentOnInsertFailure(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(t, "billing-task-insert-failure", user, token, 10)

	require.NoError(t, DB.Exec(`
		CREATE TRIGGER reject_billing_task_insert
		BEFORE INSERT ON tasks
		BEGIN
			SELECT RAISE(ABORT, 'forced task insert failure');
		END
	`).Error)
	t.Cleanup(func() {
		DB.Exec("DROP TRIGGER IF EXISTS reject_billing_task_insert")
	})

	task := &Task{
		TaskID:   "billing-task-insert-failure",
		UserId:   user.Id,
		Quota:    30,
		Status:   TaskStatusInProgress,
		Progress: "50%",
		PrivateData: TaskPrivateData{
			BillingSource: BillingOperationFundingWallet,
			TokenId:       token.Id,
		},
	}

	_, err := CreateTaskWithBillingOperation(task, operation.Id, 30)
	require.Error(t, err)

	var reloadedOperation BillingOperation
	require.NoError(t, DB.First(&reloadedOperation, operation.Id).Error)
	assert.Equal(t, 10, reloadedOperation.ReservedQuota)
	assert.Zero(t, reloadedOperation.TaskId)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 90, reloadedUser.Quota)
	assert.Equal(t, 70, reloadedToken.RemainQuota)
	assert.Equal(t, 10, reloadedToken.UsedQuota)

	var taskCount int64
	require.NoError(t, DB.Model(&Task{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount)
}

func TestApplyTaskBillingTransitionRefundsExactlyOnce(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(t, "billing-task-refund", user, token, 30)

	task := &Task{
		TaskID:    "billing-task-refund",
		UserId:    user.Id,
		Quota:     30,
		Status:    TaskStatusInProgress,
		Progress:  "50%",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		PrivateData: TaskPrivateData{
			BillingSource: BillingOperationFundingWallet,
			TokenId:       token.Id,
		},
	}
	_, err := CreateTaskWithBillingOperation(task, operation.Id, 30)
	require.NoError(t, err)

	task.Status = TaskStatusFailure
	task.Progress = "100%"
	task.FailReason = "upstream failed"
	result, err := ApplyTaskBillingTransition(
		task,
		TaskStatusInProgress,
		0,
		BillingOperationOutcomeRefund,
	)
	require.NoError(t, err)
	require.True(t, result.Changed)
	assert.True(t, result.BillingChanged)
	assert.True(t, result.TaskChanged)
	assert.Equal(t, -30, result.Delta)

	var reloadedTask Task
	require.NoError(t, DB.First(&reloadedTask, task.ID).Error)
	assert.EqualValues(t, TaskStatusFailure, reloadedTask.Status)
	assert.Zero(t, reloadedTask.Quota)

	var reloadedOperation BillingOperation
	require.NoError(t, DB.First(&reloadedOperation, operation.Id).Error)
	assert.Equal(t, BillingOperationStatusRefunded, reloadedOperation.Status)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 80, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)

	result, err = ApplyTaskBillingTransition(
		task,
		TaskStatusInProgress,
		0,
		BillingOperationOutcomeRefund,
	)
	require.NoError(t, err)
	assert.False(t, result.Changed)
	assert.False(t, result.BillingChanged)
	assert.False(t, result.TaskChanged)

	task.Status = TaskStatusSuccess
	_, err = ApplyTaskBillingTransition(
		task,
		TaskStatusInProgress,
		30,
		BillingOperationOutcomeSettle,
	)
	require.ErrorIs(t, err, ErrBillingOperationTransition)
}

func TestTaskBillingProviderResultWritesClassifyDriverErrors(t *testing.T) {
	const (
		envelope = "naenc:v1:billing-write-private-key:" +
			"billing-write-private-nonce:billing-write-private-ciphertext"
		driverMessage = "billing-write-driver-private-sentinel " + envelope
	)

	t.Run("create with reservation", func(t *testing.T) {
		user, token := seedBillingOperationAccount(t, 100, 80)
		operation := reserveWalletBillingOperation(
			t,
			"billing-task-private-create",
			user,
			token,
			30,
		)
		task := &Task{
			TaskID:                     "billing-task-private-create",
			UserId:                     user.Id,
			Status:                     TaskStatusInProgress,
			EncryptedProviderResultURI: common.GetPointer(envelope),
			Data:                       []byte(`{"done":false}`),
			PrivateData: TaskPrivateData{
				BillingSource: BillingOperationFundingWallet,
				TokenId:       token.Id,
			},
		}
		callbackName := "test:billing-task-provider-result-create-driver-error"
		require.NoError(t, DB.Callback().Create().Before("gorm:create").
			Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table == "tasks" {
					tx.AddError(&mysql.MySQLError{
						Number:  1062,
						Message: driverMessage,
					})
				}
			}))
		t.Cleanup(func() {
			require.NoError(t, DB.Callback().Create().Remove(callbackName))
		})

		_, err := CreateTaskWithBillingOperation(task, operation.Id, 30)

		require.EqualError(t, err, "mysql error 1062")
		assert.NotContains(t, err.Error(), driverMessage)
		assert.NotContains(t, err.Error(), envelope)
		assert.NotContains(t, err.Error(), "naenc:v1")
	})

	t.Run("terminal transition", func(t *testing.T) {
		user, token := seedBillingOperationAccount(t, 100, 80)
		operation := reserveWalletBillingOperation(
			t,
			"billing-task-private-transition",
			user,
			token,
			30,
		)
		task := &Task{
			TaskID:   "billing-task-private-transition",
			UserId:   user.Id,
			Status:   TaskStatusInProgress,
			Progress: "50%",
			Data:     []byte(`{"done":false}`),
			PrivateData: TaskPrivateData{
				BillingSource: BillingOperationFundingWallet,
				TokenId:       token.Id,
			},
		}
		_, err := CreateTaskWithBillingOperation(task, operation.Id, 30)
		require.NoError(t, err)

		task.Status = TaskStatusSuccess
		task.Progress = "100%"
		task.EncryptedProviderResultURI = common.GetPointer(envelope)
		callbackName := "test:billing-task-provider-result-update-driver-error"
		require.NoError(t, DB.Callback().Update().Before("gorm:update").
			Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table == "tasks" {
					tx.AddError(&mysql.MySQLError{
						Number:  1062,
						Message: driverMessage,
					})
				}
			}))
		t.Cleanup(func() {
			require.NoError(t, DB.Callback().Update().Remove(callbackName))
		})

		_, err = ApplyTaskBillingTransition(
			task,
			TaskStatusInProgress,
			30,
			BillingOperationOutcomeSettle,
		)

		require.EqualError(t, err, "mysql error 1062")
		assert.NotContains(t, err.Error(), driverMessage)
		assert.NotContains(t, err.Error(), envelope)
		assert.NotContains(t, err.Error(), "naenc:v1")
	})
}

func TestTaskBillingTransitionRejectsStaleTerminalMetadataRewrite(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(
		t,
		"billing-task-metadata-only",
		user,
		token,
		30,
	)
	task := &Task{
		TaskID:   "billing-task-metadata-only",
		UserId:   user.Id,
		Quota:    30,
		Status:   TaskStatusInProgress,
		Progress: "50%",
		PrivateData: TaskPrivateData{
			BillingSource: BillingOperationFundingWallet,
			TokenId:       token.Id,
		},
	}
	_, err := CreateTaskWithBillingOperation(task, operation.Id, 30)
	require.NoError(t, err)

	task.Status = TaskStatusSuccess
	task.Progress = "100%"
	result, err := ApplyTaskBillingTransition(
		task,
		TaskStatusInProgress,
		30,
		BillingOperationOutcomeSettle,
	)
	require.NoError(t, err)
	assert.True(t, result.BillingChanged)
	assert.True(t, result.TaskChanged)

	winnerSnapshot := task.Snapshot()
	task.Data = []byte(`{"refreshed":true}`)
	result, err = ApplyTaskBillingTransition(
		task,
		TaskStatusInProgress,
		30,
		BillingOperationOutcomeSettle,
	)
	require.NoError(t, err)
	assert.False(t, result.Changed)
	assert.False(t, result.BillingChanged)
	assert.False(t, result.TaskChanged)
	assert.True(t, winnerSnapshot.Equal(task.Snapshot()))
}

func TestTaskBillingTransitionRepairsReservedOperationWithoutRewritingTerminalMetadata(
	t *testing.T,
) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(
		t,
		"billing-task-terminal-reserved-recovery",
		user,
		token,
		30,
	)
	task := &Task{
		TaskID:   "billing-task-terminal-reserved-recovery",
		UserId:   user.Id,
		Quota:    30,
		Status:   TaskStatusInProgress,
		Progress: "50%",
		PrivateData: TaskPrivateData{
			BillingSource: BillingOperationFundingWallet,
			TokenId:       token.Id,
		},
	}
	_, err := CreateTaskWithBillingOperation(task, operation.Id, 30)
	require.NoError(t, err)

	winnerData := []byte(`{"done":true,"winner":"persisted"}`)
	require.NoError(t, DB.Model(&Task{}).
		Where("id = ?", task.ID).
		Updates(map[string]any{
			"status":   TaskStatusSuccess,
			"progress": "100%",
			"data":     winnerData,
		}).Error)

	staleWorker := *task
	staleWorker.Status = TaskStatusSuccess
	staleWorker.Progress = "100%"
	staleWorker.Data = []byte(`{"done":true,"winner":"stale"}`)
	result, err := ApplyTaskBillingTransition(
		&staleWorker,
		TaskStatusInProgress,
		20,
		BillingOperationOutcomeSettle,
	)
	require.NoError(t, err)
	require.True(t, result.Changed)
	assert.True(t, result.BillingChanged)
	assert.True(t, result.TaskChanged)
	assert.Equal(t, -10, result.Delta)
	assert.Equal(t, 20, result.FinalQuota)
	assert.Equal(t, operation.Id, result.OperationId)

	var persistedTask Task
	require.NoError(t, DB.First(&persistedTask, task.ID).Error)
	assert.JSONEq(t, string(winnerData), string(persistedTask.Data))
	assert.JSONEq(t, string(winnerData), string(staleWorker.Data))
	assert.Equal(t, 20, persistedTask.Quota)
	assert.Equal(t, operation.Id, persistedTask.BillingOperationId)

	var persistedOperation BillingOperation
	require.NoError(t, DB.First(&persistedOperation, operation.Id).Error)
	assert.Equal(t, BillingOperationStatusSettled, persistedOperation.Status)
	assert.Equal(t, 20, persistedOperation.ActualQuota)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 80, reloadedUser.Quota)
	assert.Equal(t, 60, reloadedToken.RemainQuota)
	assert.Equal(t, 20, reloadedToken.UsedQuota)
}

func TestTaskBillingTransitionAbandonsInconsistentTokenBalance(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(
		t,
		"billing-task-inconsistent-token",
		user,
		token,
		30,
	)
	task := &Task{
		TaskID:   "billing-task-inconsistent-token",
		UserId:   user.Id,
		Quota:    30,
		Status:   TaskStatusInProgress,
		Progress: "50%",
		PrivateData: TaskPrivateData{
			BillingSource: BillingOperationFundingWallet,
			TokenId:       token.Id,
		},
	}
	_, err := CreateTaskWithBillingOperation(task, operation.Id, 30)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Token{}).
		Where("id = ?", token.Id).
		Update("used_quota", 0).Error)

	task.Status = TaskStatusFailure
	task.Progress = "100%"
	result, err := ApplyTaskBillingTransition(
		task,
		TaskStatusInProgress,
		0,
		BillingOperationOutcomeRefund,
	)
	require.NoError(t, err)
	assert.True(t, result.Changed)
	assert.True(t, result.BillingChanged)
	assert.True(t, result.TaskChanged)
	assert.True(t, result.Abandoned)
	assert.Equal(t, "token_balance_inconsistent", result.FailureReason)
	assert.Equal(t, 30, result.FinalQuota)

	var persistedTask Task
	require.NoError(t, DB.First(&persistedTask, task.ID).Error)
	assert.EqualValues(t, TaskStatusFailure, persistedTask.Status)
	assert.Equal(t, 30, persistedTask.Quota)

	var persistedOperation BillingOperation
	require.NoError(t, DB.First(&persistedOperation, operation.Id).Error)
	assert.Equal(t, BillingOperationStatusAbandoned, persistedOperation.Status)
	assert.Equal(t, "token_balance_inconsistent", persistedOperation.FailureReason)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(
		t,
		user.Id,
		token.Id,
	)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 50, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)
}

func TestLegacyTaskAdoptionRefundsWithoutSecondPreConsume(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 70, 50)
	require.NoError(t, DB.Model(&Token{}).
		Where("id = ?", token.Id).
		Updates(map[string]any{"unlimited_quota": true, "used_quota": 30}).Error)

	task := &Task{
		TaskID:     "billing-legacy-adoption",
		UserId:     user.Id,
		Quota:      30,
		Status:     TaskStatusInProgress,
		Progress:   "50%",
		SubmitTime: TaskRefundLegacyCutoff,
		PrivateData: TaskPrivateData{
			BillingSource: BillingOperationFundingWallet,
			TokenId:       token.Id,
		},
	}
	require.NoError(t, DB.Create(task).Error)

	task.Status = TaskStatusFailure
	task.Progress = "100%"
	result, err := ApplyTaskBillingTransition(
		task,
		TaskStatusInProgress,
		0,
		BillingOperationOutcomeRefund,
	)
	require.NoError(t, err)
	require.True(t, result.Changed)
	assert.False(t, result.LegacyNoRefund)

	var operation BillingOperation
	require.NoError(t, DB.First(&operation, task.BillingOperationId).Error)
	assert.True(t, operation.TokenUnlimited)
	assert.True(t, operation.TokenRemainCharged)
	assert.Equal(t, BillingOperationStatusRefunded, operation.Status)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 80, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)
}

func TestLegacyRefundCutoffTerminalizesWithoutFinancialMutation(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 70, 50)
	require.NoError(t, DB.Model(&Token{}).
		Where("id = ?", token.Id).
		Update("used_quota", 30).Error)

	task := &Task{
		TaskID:     "billing-legacy-cutoff",
		UserId:     user.Id,
		Quota:      30,
		Status:     TaskStatusInProgress,
		Progress:   "50%",
		SubmitTime: TaskRefundLegacyCutoff - 1,
		PrivateData: TaskPrivateData{
			BillingSource: BillingOperationFundingWallet,
			TokenId:       token.Id,
		},
	}
	require.NoError(t, DB.Create(task).Error)

	task.Status = TaskStatusFailure
	task.Progress = "100%"
	result, err := ApplyTaskBillingTransition(
		task,
		TaskStatusInProgress,
		0,
		BillingOperationOutcomeRefund,
	)
	require.NoError(t, err)
	require.True(t, result.Changed)
	assert.True(t, result.LegacyNoRefund)
	assert.Zero(t, task.Quota)
	assert.Zero(t, task.BillingOperationId)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 70, reloadedUser.Quota)
	assert.Equal(t, 50, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)

	var operationCount int64
	require.NoError(t, DB.Model(&BillingOperation{}).Count(&operationCount).Error)
	assert.Zero(t, operationCount)
}

func TestConcurrentTaskBillingTransitionHasOneWinner(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(t, "billing-concurrent-terminal", user, token, 30)
	task := &Task{
		TaskID:   "billing-concurrent-terminal",
		UserId:   user.Id,
		Quota:    30,
		Status:   TaskStatusInProgress,
		Progress: "50%",
		PrivateData: TaskPrivateData{
			BillingSource: BillingOperationFundingWallet,
			TokenId:       token.Id,
		},
	}
	_, err := CreateTaskWithBillingOperation(task, operation.Id, 30)
	require.NoError(t, err)

	first := *task
	second := *task
	first.Status = TaskStatusFailure
	first.Progress = "100%"
	second.Status = TaskStatusFailure
	second.Progress = "100%"

	var wg sync.WaitGroup
	results := make(chan *TaskBillingTransitionResult, 2)
	errorsCh := make(chan error, 2)
	for _, candidate := range []*Task{&first, &second} {
		wg.Add(1)
		go func(candidate *Task) {
			defer wg.Done()
			result, transitionErr := ApplyTaskBillingTransition(
				candidate,
				TaskStatusInProgress,
				0,
				BillingOperationOutcomeRefund,
			)
			results <- result
			errorsCh <- transitionErr
		}(candidate)
	}
	wg.Wait()
	close(results)
	close(errorsCh)

	for transitionErr := range errorsCh {
		require.NoError(t, transitionErr)
	}
	winners := 0
	for result := range results {
		require.NotNil(t, result)
		if result.Changed {
			winners++
		}
	}
	assert.Equal(t, 1, winners)

	reloadedUser, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 100, reloadedUser.Quota)
	assert.Equal(t, 80, reloadedToken.RemainQuota)
	assert.Zero(t, reloadedToken.UsedQuota)
}

func TestSubscriptionBillingOperationRefundsFundingAndTokenTogether(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 0, 80)

	plan := SubscriptionPlan{
		Title:            "billing-operation-plan",
		TotalAmount:      100,
		Enabled:          common.GetPointer(true),
		QuotaResetPeriod: SubscriptionResetNever,
	}
	require.NoError(t, DB.Create(&plan).Error)
	subscription := UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 100,
		AmountUsed:  10,
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
		Status:      "active",
	}
	require.NoError(t, DB.Create(&subscription).Error)

	operation, created, err := ReserveBillingOperation(BillingOperationReserveRequest{
		RequestId:     "billing-subscription-refund",
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TokenCharged:  true,
		FundingSource: BillingOperationFundingSubscription,
		Quota:         30,
	})
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, subscription.Id, operation.SubscriptionId)

	var chargedSubscription UserSubscription
	require.NoError(t, DB.First(&chargedSubscription, subscription.Id).Error)
	assert.EqualValues(t, 40, chargedSubscription.AmountUsed)

	_, changed, err := RefundBillingOperation(operation.Id)
	require.NoError(t, err)
	require.True(t, changed)

	var refundedSubscription UserSubscription
	require.NoError(t, DB.First(&refundedSubscription, subscription.Id).Error)
	assert.EqualValues(t, 10, refundedSubscription.AmountUsed)
	_, refundedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 80, refundedToken.RemainQuota)
	assert.Zero(t, refundedToken.UsedQuota)
}

func TestSubscriptionBillingOperationRejectsInexactRefund(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 0, 80)

	plan := SubscriptionPlan{
		Title:            "billing-operation-exact-refund-plan",
		TotalAmount:      100,
		Enabled:          common.GetPointer(true),
		QuotaResetPeriod: SubscriptionResetNever,
	}
	require.NoError(t, DB.Create(&plan).Error)
	subscription := UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 100,
		AmountUsed:  10,
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
		Status:      "active",
	}
	require.NoError(t, DB.Create(&subscription).Error)

	operation, _, err := ReserveBillingOperation(BillingOperationReserveRequest{
		RequestId:     "billing-subscription-inexact-refund",
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TokenCharged:  true,
		FundingSource: BillingOperationFundingSubscription,
		Quota:         30,
	})
	require.NoError(t, err)

	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("id = ?", subscription.Id).
		Update("amount_used", 5).Error)

	_, _, err = RefundBillingOperation(operation.Id)
	require.ErrorIs(t, err, ErrBillingOperationFundingInvalid)

	var reloadedOperation BillingOperation
	require.NoError(t, DB.First(&reloadedOperation, operation.Id).Error)
	assert.Equal(t, BillingOperationStatusReserved, reloadedOperation.Status)
	_, reloadedToken := reloadBillingOperationAccount(t, user.Id, token.Id)
	assert.Equal(t, 50, reloadedToken.RemainQuota)
	assert.Equal(t, 30, reloadedToken.UsedQuota)
}

func TestBillingOperationRejectsUnknownFundingSource(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)

	_, _, err := ReserveBillingOperation(BillingOperationReserveRequest{
		RequestId:     "billing-invalid-source",
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TokenCharged:  true,
		FundingSource: "unknown",
		Quota:         10,
	})
	require.ErrorIs(t, err, ErrBillingOperationInvalid)
}

func TestBillingOperationRequestIdentityConflicts(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	base := BillingOperationReserveRequest{
		RequestId:     "billing-request-conflict",
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TokenCharged:  true,
		FundingSource: BillingOperationFundingWallet,
		Quota:         10,
	}
	_, created, err := ReserveBillingOperation(base)
	require.NoError(t, err)
	require.True(t, created)

	conflicts := []BillingOperationReserveRequest{
		func() BillingOperationReserveRequest {
			changed := base
			changed.UserId++
			return changed
		}(),
		func() BillingOperationReserveRequest {
			changed := base
			changed.TokenId++
			return changed
		}(),
		func() BillingOperationReserveRequest {
			changed := base
			changed.FundingSource = BillingOperationFundingSubscription
			return changed
		}(),
	}
	for _, conflict := range conflicts {
		_, _, err := ReserveBillingOperation(conflict)
		require.ErrorIs(t, err, ErrBillingOperationConflict)
	}
}

func TestBillingOperationReplayAfterReservationAdjustmentUsesImmutableIdentity(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	input := BillingOperationReserveRequest{
		RequestId:     "billing-request-adjusted-replay",
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TokenCharged:  true,
		FundingSource: BillingOperationFundingWallet,
		Quota:         10,
	}
	operation, created, err := ReserveBillingOperation(input)
	require.NoError(t, err)
	require.True(t, created)

	operation, changed, err := AdjustBillingOperationReservation(
		operation.Id,
		20,
	)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, 20, operation.ReservedQuota)

	replayed, created, err := ReserveBillingOperation(input)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, operation.Id, replayed.Id)
	assert.Equal(t, 20, replayed.ReservedQuota)
}

func TestBillingOperationContradictoryTransitionsAreRejected(t *testing.T) {
	t.Run("refund after settle", func(t *testing.T) {
		user, token := seedBillingOperationAccount(t, 100, 80)
		operation := reserveWalletBillingOperation(t, "billing-refund-after-settle", user, token, 20)
		_, _, err := SettleBillingOperation(operation.Id, 20)
		require.NoError(t, err)
		_, _, err = RefundBillingOperation(operation.Id)
		require.ErrorIs(t, err, ErrBillingOperationTransition)
	})

	t.Run("settle after refund", func(t *testing.T) {
		user, token := seedBillingOperationAccount(t, 100, 80)
		operation := reserveWalletBillingOperation(t, "billing-settle-after-refund", user, token, 20)
		_, _, err := RefundBillingOperation(operation.Id)
		require.NoError(t, err)
		_, _, err = SettleBillingOperation(operation.Id, 20)
		require.ErrorIs(t, err, ErrBillingOperationTransition)
	})

	t.Run("transition after abandon", func(t *testing.T) {
		user, token := seedBillingOperationAccount(t, 100, 80)
		operation := reserveWalletBillingOperation(t, "billing-after-abandon", user, token, 20)
		require.NoError(t, DB.Unscoped().Delete(&user).Error)
		_, _, err := RefundBillingOperation(operation.Id)
		require.NoError(t, err)
		_, _, err = SettleBillingOperation(operation.Id, 20)
		require.ErrorIs(t, err, ErrBillingOperationTransition)
		_, changed, err := RefundBillingOperation(operation.Id)
		require.NoError(t, err)
		assert.False(t, changed)
	})
}

func TestAdjustBillingOperationReservationRejectsTerminalOperation(t *testing.T) {
	user, token := seedBillingOperationAccount(t, 100, 80)
	operation := reserveWalletBillingOperation(t, "billing-adjust-terminal", user, token, 20)
	_, _, err := SettleBillingOperation(operation.Id, 20)
	require.NoError(t, err)

	_, _, err = AdjustBillingOperationReservation(operation.Id, 30)
	require.ErrorIs(t, err, ErrBillingOperationTransition)
}
