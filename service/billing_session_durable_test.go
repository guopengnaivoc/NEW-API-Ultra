package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBillingSessionTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("POST", "/v1/test", nil)
	return context
}

func billingSessionRelayInfo(
	requestID string,
	userID int,
	tokenID int,
	tokenKey string,
	preference string,
) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RequestId:       requestID,
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        tokenKey,
		ForcePreConsume: true,
		OriginModelName: "billing-session-model",
		UserSetting: dto.UserSetting{
			BillingPreference: preference,
		},
	}
}

func loadBillingOperationByRequestID(t *testing.T, requestID string) model.BillingOperation {
	t.Helper()
	var operation model.BillingOperation
	require.NoError(t, model.DB.Where("request_id = ?", requestID).First(&operation).Error)
	return operation
}

func TestBillingSessionWalletSettleUsesDurableOperation(t *testing.T) {
	truncate(t)
	const userID, tokenID = 501, 501
	seedUser(t, userID, 100)
	seedToken(t, tokenID, userID, "billing-session-wallet", 80)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"billing-session-wallet-settle",
		userID,
		tokenID,
		"billing-session-wallet",
		"wallet_only",
	)

	require.Nil(t, PreConsumeBilling(context, 30, info))
	session, ok := info.Billing.(*BillingSession)
	require.True(t, ok)
	assert.Positive(t, session.OperationId())
	assert.Equal(t, 70, getUserQuota(t, userID))
	assert.Equal(t, 50, getTokenRemainQuota(t, tokenID))

	require.NoError(t, session.Settle(25))
	assert.Equal(t, 75, getUserQuota(t, userID))
	assert.Equal(t, 55, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, 25, getTokenUsedQuota(t, tokenID))

	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, model.BillingOperationStatusSettled, operation.Status)
	assert.Equal(t, 25, operation.ActualQuota)
	assert.False(t, session.NeedsRefund())
}

func TestBillingSessionRefundIsSynchronousAndRestoresSoftDeletedToken(t *testing.T) {
	truncate(t)
	const userID, tokenID = 502, 502
	seedUser(t, userID, 100)
	seedToken(t, tokenID, userID, "billing-session-refund", 80)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"billing-session-soft-delete-refund",
		userID,
		tokenID,
		"billing-session-refund",
		"wallet_only",
	)

	require.Nil(t, PreConsumeBilling(context, 30, info))
	require.NoError(t, model.DB.Delete(&model.Token{Id: tokenID}).Error)

	info.Billing.Refund(context)

	assert.Equal(t, 100, getUserQuota(t, userID))
	var token model.Token
	require.NoError(t, model.DB.Unscoped().First(&token, tokenID).Error)
	assert.Equal(t, 80, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.False(t, info.Billing.NeedsRefund())
	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, model.BillingOperationStatusRefunded, operation.Status)
}

func TestBillingSessionRefundFailureRemainsRetryable(t *testing.T) {
	truncate(t)
	const userID, tokenID = 503, 503
	seedUser(t, userID, 100)
	seedToken(t, tokenID, userID, "billing-session-retry", 80)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"billing-session-refund-retry",
		userID,
		tokenID,
		"billing-session-retry",
		"wallet_only",
	)

	require.Nil(t, PreConsumeBilling(context, 30, info))
	require.NoError(t, model.DB.Model(&model.Token{}).
		Where("id = ?", tokenID).
		Update("used_quota", 0).Error)

	info.Billing.Refund(context)

	assert.Equal(t, 70, getUserQuota(t, userID))
	assert.Equal(t, 50, getTokenRemainQuota(t, tokenID))
	assert.True(t, info.Billing.NeedsRefund())
	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, model.BillingOperationStatusReserved, operation.Status)
}

func TestBillingSessionSubscriptionSettleUsesOneTransactionOwner(t *testing.T) {
	truncate(t)
	const userID, tokenID, subscriptionID = 504, 504, 504
	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "billing-session-subscription", 80)

	plan := model.SubscriptionPlan{
		Title:            "billing-session-plan",
		TotalAmount:      100,
		Enabled:          common.GetPointer(true),
		QuotaResetPeriod: model.SubscriptionResetNever,
	}
	require.NoError(t, model.DB.Create(&plan).Error)
	subscription := model.UserSubscription{
		Id:          subscriptionID,
		UserId:      userID,
		PlanId:      plan.Id,
		AmountTotal: 100,
		AmountUsed:  10,
		Status:      "active",
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(&subscription).Error)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"billing-session-subscription-settle",
		userID,
		tokenID,
		"billing-session-subscription",
		"subscription_only",
	)

	require.Nil(t, PreConsumeBilling(context, 30, info))
	assert.Equal(t, subscriptionID, info.SubscriptionId)
	assert.EqualValues(t, 40, getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, 50, getTokenRemainQuota(t, tokenID))

	require.NoError(t, info.Billing.Settle(20))
	assert.EqualValues(t, 30, getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, 60, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, 20, getTokenUsedQuota(t, tokenID))

	var legacyRecordCount int64
	require.NoError(t, model.DB.Model(&model.SubscriptionPreConsumeRecord{}).
		Where("request_id = ?", info.RequestId).
		Count(&legacyRecordCount).Error)
	assert.Zero(t, legacyRecordCount)
	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, model.BillingOperationStatusSettled, operation.Status)
}

func TestBillingSessionLinksAcceptedTaskWithoutPrematureSettlement(t *testing.T) {
	truncate(t)
	const userID, tokenID = 505, 505
	seedUser(t, userID, 100)
	seedToken(t, tokenID, userID, "billing-session-task-link", 80)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"billing-session-task-link",
		userID,
		tokenID,
		"billing-session-task-link",
		"wallet_only",
	)
	require.Nil(t, PreConsumeBilling(context, 30, info))
	session, ok := info.Billing.(*BillingSession)
	require.True(t, ok)

	task := makeTask(
		userID,
		0,
		40,
		tokenID,
		BillingSourceWallet,
		0,
	)
	limited, err := session.LinkTask(task, 40)
	require.NoError(t, err)
	assert.False(t, limited)
	assert.Positive(t, task.ID)
	assert.Equal(t, session.OperationId(), task.BillingOperationId)
	assert.Equal(t, 40, task.Quota)
	assert.Equal(t, 60, getUserQuota(t, userID))
	assert.Equal(t, 40, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, 40, getTokenUsedQuota(t, tokenID))

	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, model.BillingOperationStatusReserved, operation.Status)
	assert.Equal(t, task.ID, operation.TaskId)
	assert.Equal(t, 40, operation.ReservedQuota)
	assert.Zero(t, operation.ActualQuota)
}
