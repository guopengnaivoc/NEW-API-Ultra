package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reserveLinkedWalletTask(
	t *testing.T,
	requestID string,
	userID int,
	tokenID int,
	tokenKey string,
	userQuota int,
	tokenQuota int,
	reservedQuota int,
) (*model.Task, model.BillingOperation) {
	t.Helper()
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, tokenKey, tokenQuota)

	operation, created, err := model.ReserveBillingOperation(
		model.BillingOperationReserveRequest{
			RequestId:     requestID,
			UserId:        userID,
			TokenId:       tokenID,
			TokenKey:      tokenKey,
			TokenCharged:  true,
			FundingSource: model.BillingOperationFundingWallet,
			Quota:         reservedQuota,
		},
	)
	require.NoError(t, err)
	require.True(t, created)

	task := makeTask(
		userID,
		0,
		reservedQuota,
		tokenID,
		BillingSourceWallet,
		0,
	)
	limited, err := model.CreateTaskWithBillingOperation(
		task,
		operation.Id,
		reservedQuota,
	)
	require.NoError(t, err)
	require.False(t, limited)
	return task, *operation
}

func TestApplyDurableTaskBillingTransitionRefundsAtomically(t *testing.T) {
	truncate(t)
	task, operation := reserveLinkedWalletTask(
		t,
		"service-task-refund-atomic",
		601,
		601,
		"service-task-refund",
		100,
		80,
		30,
	)

	fromStatus := task.Status
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FailReason = "upstream failed"

	result, err := ApplyDurableTaskBillingTransition(
		context.Background(),
		task,
		fromStatus,
		0,
		model.BillingOperationOutcomeRefund,
		task.FailReason,
	)
	require.NoError(t, err)
	require.True(t, result.Changed)
	assert.Equal(t, 100, getUserQuota(t, task.UserId))
	assert.Equal(t, 80, getTokenRemainQuota(t, task.PrivateData.TokenId))
	assert.Zero(t, getTokenUsedQuota(t, task.PrivateData.TokenId))

	var persistedTask model.Task
	require.NoError(t, model.DB.First(&persistedTask, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, persistedTask.Status)
	assert.Zero(t, persistedTask.Quota)

	var persistedOperation model.BillingOperation
	require.NoError(t, model.DB.First(&persistedOperation, operation.Id).Error)
	assert.Equal(t, model.BillingOperationStatusRefunded, persistedOperation.Status)
}

func TestApplyDurableTaskBillingTransitionAbandonsInconsistentRefund(t *testing.T) {
	truncate(t)
	task, operation := reserveLinkedWalletTask(
		t,
		"service-task-refund-rollback",
		602,
		602,
		"service-task-refund-rollback",
		100,
		80,
		30,
	)
	require.NoError(t, model.DB.Model(&model.Token{}).
		Where("id = ?", task.PrivateData.TokenId).
		Update("used_quota", 0).Error)

	fromStatus := task.Status
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FailReason = "upstream failed"

	result, err := ApplyDurableTaskBillingTransition(
		context.Background(),
		task,
		fromStatus,
		0,
		model.BillingOperationOutcomeRefund,
		task.FailReason,
	)
	require.NoError(t, err)
	require.True(t, result.Abandoned)
	assert.Equal(t, "token_balance_inconsistent", result.FailureReason)
	assert.Equal(t, 70, getUserQuota(t, task.UserId))
	assert.Equal(t, 50, getTokenRemainQuota(t, task.PrivateData.TokenId))

	var persistedTask model.Task
	require.NoError(t, model.DB.First(&persistedTask, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, persistedTask.Status)
	assert.Equal(t, 30, persistedTask.Quota)

	var persistedOperation model.BillingOperation
	require.NoError(t, model.DB.First(&persistedOperation, operation.Id).Error)
	assert.Equal(t, model.BillingOperationStatusAbandoned, persistedOperation.Status)
}

func TestApplyDurableTaskBillingTransitionAuditsCapacityLimitedSettlement(t *testing.T) {
	truncate(t)
	task, operation := reserveLinkedWalletTask(
		t,
		"service-task-settle-capacity",
		603,
		603,
		"service-task-settle-capacity",
		40,
		80,
		30,
	)

	fromStatus := task.Status
	task.Status = model.TaskStatusSuccess
	task.Progress = "100%"

	result, err := ApplyDurableTaskBillingTransition(
		context.Background(),
		task,
		fromStatus,
		60,
		model.BillingOperationOutcomeSettle,
		"adaptor settlement",
	)
	require.NoError(t, err)
	require.True(t, result.SettlementLimited)
	assert.Equal(t, 40, result.FinalQuota)
	assert.Equal(t, 0, getUserQuota(t, task.UserId))
	assert.Equal(t, 40, getTokenRemainQuota(t, task.PrivateData.TokenId))
	assert.Equal(t, 40, getTokenUsedQuota(t, task.PrivateData.TokenId))

	var persistedOperation model.BillingOperation
	require.NoError(t, model.DB.First(&persistedOperation, operation.Id).Error)
	assert.Equal(t, model.BillingOperationStatusSettled, persistedOperation.Status)
	assert.Equal(t, 60, persistedOperation.RequestedQuota)
	assert.Equal(t, 40, persistedOperation.ActualQuota)

	log := getLastLog(t)
	require.NotNil(t, log)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	saturation, ok := adminInfo["quota_saturation"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, string(common.QuotaClampCapacity), saturation["kind"])
	assert.Equal(t, "BillingOperationSettle", saturation["op"])
}

func TestApplyDurableTaskBillingTransitionDoesNotRelogOrRewriteStaleTerminalMetadata(
	t *testing.T,
) {
	truncate(t)
	task, _ := reserveLinkedWalletTask(
		t,
		"service-task-terminal-metadata",
		604,
		604,
		"service-task-terminal-metadata",
		100,
		80,
		30,
	)

	fromStatus := task.Status
	task.Status = model.TaskStatusSuccess
	task.Progress = "100%"
	result, err := ApplyDurableTaskBillingTransition(
		context.Background(),
		task,
		fromStatus,
		20,
		model.BillingOperationOutcomeSettle,
		"initial settlement",
	)
	require.NoError(t, err)
	require.True(t, result.BillingChanged)
	assert.Equal(t, int64(1), countLogs(t))

	winnerSnapshot := task.Snapshot()
	task.Data = []byte(`{"refreshed":true}`)
	result, err = ApplyDurableTaskBillingTransition(
		context.Background(),
		task,
		fromStatus,
		20,
		model.BillingOperationOutcomeSettle,
		"stale metadata refresh",
	)
	require.NoError(t, err)
	assert.False(t, result.Changed)
	assert.False(t, result.BillingChanged)
	assert.False(t, result.TaskChanged)
	assert.True(t, winnerSnapshot.Equal(task.Snapshot()))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestApplyDurableTaskBillingTransitionKeepsFirstTerminalMetadataWinner(
	t *testing.T,
) {
	truncate(t)
	task, _ := reserveLinkedWalletTask(
		t,
		"service-task-terminal-metadata-winner",
		605,
		605,
		"service-task-terminal-metadata-winner",
		100,
		80,
		30,
	)
	task.Platform = constant.TaskPlatform("gemini")

	firstWorker := *task
	firstWorker.Status = model.TaskStatusSuccess
	firstWorker.Progress = "100%"
	firstWorker.Data = []byte(`{"done":true,"winner":"first"}`)
	firstURI := "https://first-winner.example.test/video" +
		"?signed=first-winner-private-query"
	_, err := firstWorker.SetProviderResultURI(firstURI)
	require.NoError(t, err)

	secondWorker := *task
	secondWorker.Status = model.TaskStatusSuccess
	secondWorker.Progress = "100%"
	secondWorker.Data = []byte(`{"done":true,"winner":"second"}`)
	secondURI := "https://second-loser.example.test/video" +
		"?signed=second-loser-private-query"
	_, err = secondWorker.SetProviderResultURI(secondURI)
	require.NoError(t, err)
	require.NotEqual(
		t,
		*firstWorker.EncryptedProviderResultURI,
		*secondWorker.EncryptedProviderResultURI,
	)

	firstResult, err := ApplyDurableTaskBillingTransition(
		context.Background(),
		&firstWorker,
		model.TaskStatusInProgress,
		20,
		model.BillingOperationOutcomeSettle,
		"first worker settlement",
	)
	require.NoError(t, err)
	require.True(t, firstResult.Changed)
	assert.True(t, firstResult.BillingChanged)
	assert.True(t, firstResult.TaskChanged)
	assert.Equal(t, int64(1), countLogs(t))

	secondResult, err := ApplyDurableTaskBillingTransition(
		context.Background(),
		&secondWorker,
		model.TaskStatusInProgress,
		20,
		model.BillingOperationOutcomeSettle,
		"stale second worker settlement",
	)
	require.NoError(t, err)
	assert.False(t, secondResult.Changed)
	assert.False(t, secondResult.BillingChanged)
	assert.False(t, secondResult.TaskChanged)
	assert.Equal(t, int64(1), countLogs(t))

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.JSONEq(t, string(firstWorker.Data), string(persisted.Data))
	assert.NotContains(t, string(persisted.Data), "second")
	openedURI, err := persisted.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, firstURI, openedURI)
	assert.NotEqual(t, secondURI, openedURI)
	assert.Equal(t, firstWorker.Snapshot(), persisted.Snapshot())
	assert.Equal(t, firstWorker.Snapshot(), secondWorker.Snapshot())
}

func TestApplyDurableTaskBillingTransitionAuditsZeroDeltaCapacityLimit(t *testing.T) {
	truncate(t)
	task, _ := reserveLinkedWalletTask(
		t,
		"service-task-zero-capacity",
		605,
		605,
		"service-task-zero-capacity",
		30,
		80,
		30,
	)

	fromStatus := task.Status
	task.Status = model.TaskStatusSuccess
	task.Progress = "100%"
	result, err := ApplyDurableTaskBillingTransition(
		context.Background(),
		task,
		fromStatus,
		60,
		model.BillingOperationOutcomeSettle,
		"capacity exhausted",
	)
	require.NoError(t, err)
	require.True(t, result.SettlementLimited)
	assert.Zero(t, result.Delta)
	assert.Equal(t, 30, result.FinalQuota)
	assert.Equal(t, int64(1), countLogs(t))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Zero(t, log.Quota)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	saturation, ok := adminInfo["quota_saturation"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, string(common.QuotaClampCapacity), saturation["kind"])
}
