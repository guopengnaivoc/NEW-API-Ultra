package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertTopUpQuotaConversionFixture(
	t *testing.T,
	tradeNo string,
	userID int,
	provider string,
	amount int64,
	money float64,
) {
	t.Helper()
	require.NoError(t, DB.Create(&TopUp{
		UserId:          userID,
		Amount:          amount,
		Money:           money,
		TradeNo:         tradeNo,
		PaymentMethod:   provider,
		PaymentProvider: provider,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}).Error)
}

func TestTopUpQuotaOverflowLeavesOrderPendingAndBalanceUnchanged(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
	})
	common.QuotaPerUnit = 500000
	overflowAmount := int64(common.MaxQuota)/500000 + 1

	testCases := []struct {
		name     string
		provider string
		amount   int64
		money    float64
		run      func(string) error
	}{
		{
			name:     "stripe live settlement",
			provider: PaymentProviderStripe,
			money:    5000,
			run: func(tradeNo string) error {
				return Recharge(tradeNo, "", "127.0.0.1")
			},
		},
		{
			name:     "stripe manual completion",
			provider: PaymentProviderStripe,
			money:    5000,
			run: func(tradeNo string) error {
				return ManualCompleteTopUp(tradeNo, "127.0.0.1")
			},
		},
		{
			name:     "waffo",
			provider: PaymentProviderWaffo,
			amount:   overflowAmount,
			run: func(tradeNo string) error {
				return RechargeWaffo(tradeNo, "127.0.0.1")
			},
		},
		{
			name:     "waffo pancake",
			provider: PaymentProviderWaffoPancake,
			amount:   overflowAmount,
			run:      RechargeWaffoPancake,
		},
		{
			name:     "creem configured quota",
			provider: PaymentProviderCreem,
			amount:   int64(common.MaxQuota) + 1,
			run: func(tradeNo string) error {
				return RechargeCreem(tradeNo, "", "", "127.0.0.1")
			},
		},
	}

	for index, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			userID := 700 + index
			tradeNo := fmt.Sprintf("quota-overflow-%d", index)
			insertUserForPaymentGuardTest(t, userID, 10)
			insertTopUpQuotaConversionFixture(
				t, tradeNo, userID, tc.provider, tc.amount, tc.money,
			)

			require.Error(t, tc.run(tradeNo))
			assert.Equal(
				t,
				common.TopUpStatusPending,
				getTopUpStatusForPaymentGuardTest(t, tradeNo),
			)
			assert.Equal(t, 10, getUserQuotaForPaymentGuardTest(t, userID))
		})
	}
}

func TestStripeTopUpLiveAndManualCompletionUseSameRounding(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
	})
	common.QuotaPerUnit = 10

	testCases := []struct {
		name string
		run  func(string) error
	}{
		{
			name: "live settlement",
			run: func(tradeNo string) error {
				return Recharge(tradeNo, "", "127.0.0.1")
			},
		},
		{
			name: "manual completion",
			run: func(tradeNo string) error {
				return ManualCompleteTopUp(tradeNo, "127.0.0.1")
			},
		},
	}

	for index, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			userID := 800 + index
			tradeNo := fmt.Sprintf("stripe-rounding-%d", index)
			insertUserForPaymentGuardTest(t, userID, 0)
			insertTopUpQuotaConversionFixture(
				t,
				tradeNo,
				userID,
				PaymentProviderStripe,
				1,
				1.25,
			)

			require.NoError(t, tc.run(tradeNo))
			assert.Equal(t, common.TopUpStatusSuccess, getTopUpStatusForPaymentGuardTest(t, tradeNo))
			assert.Equal(t, 13, getUserQuotaForPaymentGuardTest(t, userID))
		})
	}
}
