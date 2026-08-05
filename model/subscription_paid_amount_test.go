package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertSubscriptionFixtureForPaidAmountTest(t *testing.T, tradeNo string, userID int, planID int, price float64, currency string, provider string) {
	t.Helper()
	user := &User{
		Id:       userID,
		Username: "paid_amount_user",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	plan := &SubscriptionPlan{
		Id:            planID,
		Title:         "Paid Amount Plan",
		PriceAmount:   price,
		Currency:      currency,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       common.GetPointer(true),
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)

	order := &SubscriptionOrder{
		UserId:          userID,
		PlanId:          planID,
		Money:           price,
		TradeNo:         tradeNo,
		PaymentMethod:   provider,
		PaymentProvider: provider,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, order.Insert())
}

// A gateway callback that reports less than the order amount must not activate
// the subscription: the order stays pending and no entitlement is granted.
func TestCompleteSubscriptionOrder_RejectsUnderpayment(t *testing.T) {
	testCases := []struct {
		name string
		paid SubscriptionPaidAmount
	}{
		{
			name: "half the price",
			paid: SubscriptionPaidAmount{Amount: decimal.NewFromFloat(4.99), Currency: "USD", Reported: true},
		},
		{
			name: "one cent past the rounding tolerance",
			paid: SubscriptionPaidAmount{Amount: decimal.NewFromFloat(9.97), Currency: "USD", Reported: true},
		},
		{
			name: "zero",
			paid: SubscriptionPaidAmount{Amount: decimal.Zero, Currency: "USD", Reported: true},
		},
		{
			name: "negative",
			paid: SubscriptionPaidAmount{Amount: decimal.NewFromFloat(-9.99), Currency: "USD", Reported: true},
		},
		{
			name: "not reported by the gateway",
			paid: SubscriptionPaidAmount{},
		},
		{
			// The plan is priced in USD but the gateway claims settlement in
			// another currency. The magnitudes are not comparable, so the
			// comparison cannot be skipped: a signed callback reporting CNY 1
			// must not activate a USD 9.99 plan. Note the reported amount here
			// far exceeds the order figure -- it still fails.
			name: "currency mismatch fails closed",
			paid: SubscriptionPaidAmount{Amount: decimal.NewFromFloat(1500), Currency: "JPY", Reported: true},
		},
		{
			name: "currency mismatch with a covering-looking amount",
			paid: SubscriptionPaidAmount{Amount: decimal.NewFromFloat(9.99), Currency: "CNY", Reported: true},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			insertSubscriptionFixtureForPaidAmountTest(t, "sub-underpaid", 501, 601, 9.99, "USD", PaymentProviderEpay)

			err := CompleteSubscriptionOrder("sub-underpaid", `{}`, PaymentProviderEpay, "alipay", tc.paid)
			require.ErrorIs(t, err, ErrSubscriptionOrderAmountInvalid)

			order := GetSubscriptionOrderByTradeNo("sub-underpaid")
			require.NotNil(t, order)
			assert.Equal(t, common.TopUpStatusPending, order.Status)

			var subscriptions int64
			require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", 501).Count(&subscriptions).Error)
			assert.Zero(t, subscriptions)
			assert.Nil(t, GetTopUpByTradeNo("sub-underpaid"))
		})
	}
}

// Amounts that cover the order settle it, including a one-cent shortfall from
// gateway rounding and overpayment from gateway-added tax or fees.
func TestCompleteSubscriptionOrder_AcceptsCoveringPayment(t *testing.T) {
	testCases := []struct {
		name string
		paid SubscriptionPaidAmount
	}{
		{
			name: "exact",
			paid: SubscriptionPaidAmount{Amount: decimal.NewFromFloat(9.99), Currency: "USD", Reported: true},
		},
		{
			name: "one cent of gateway rounding",
			paid: SubscriptionPaidAmount{Amount: decimal.NewFromFloat(9.98), Currency: "USD", Reported: true},
		},
		{
			name: "overpaid with tax on top",
			paid: SubscriptionPaidAmount{Amount: decimal.NewFromFloat(11.49), Currency: "USD", Reported: true},
		},
		{
			// The order's provider is epay, the one gateway allowed to omit the
			// currency: it echoes the exact checkout amount in the units we
			// submitted, so the amount is implicitly in the plan currency and
			// stays comparable. Every other provider fails closed on an empty
			// currency (covered separately below).
			name: "epay reports no currency",
			paid: SubscriptionPaidAmount{Amount: decimal.NewFromFloat(9.99), Reported: true},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			insertSubscriptionFixtureForPaidAmountTest(t, "sub-covered", 502, 602, 9.99, "USD", PaymentProviderEpay)

			require.NoError(t, CompleteSubscriptionOrder("sub-covered", `{}`, PaymentProviderEpay, "alipay", tc.paid))

			order := GetSubscriptionOrderByTradeNo("sub-covered")
			require.NotNil(t, order)
			assert.Equal(t, common.TopUpStatusSuccess, order.Status)

			var subscriptions int64
			require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", 502).Count(&subscriptions).Error)
			assert.Equal(t, int64(1), subscriptions)
		})
	}
}

// A covering amount with an empty currency settles only epay orders. Stripe,
// Creem, and Waffo Pancake all document a currency in their callbacks, so an
// empty currency from one of them means the extractor could not read it and
// the magnitudes cannot be proven comparable; settlement must fail closed even
// though the number matches the order figure exactly. (The extractors
// themselves also refuse to build such a value -- controller-side tests cover
// that -- so this guard is the second layer of the defense.)
func TestCompleteSubscriptionOrder_RejectsEmptyCurrencyForNonEpayProviders(t *testing.T) {
	for _, provider := range []string{
		PaymentProviderStripe,
		PaymentProviderCreem,
		PaymentProviderWaffoPancake,
	} {
		t.Run(provider, func(t *testing.T) {
			truncateTables(t)
			insertSubscriptionFixtureForPaidAmountTest(t, "sub-no-currency", 504, 604, 9.99, "USD", provider)

			err := CompleteSubscriptionOrder("sub-no-currency", `{}`, provider, "",
				SubscriptionPaidAmount{Amount: decimal.NewFromFloat(9.99), Reported: true})
			require.ErrorIs(t, err, ErrSubscriptionOrderAmountInvalid)

			order := GetSubscriptionOrderByTradeNo("sub-no-currency")
			require.NotNil(t, order)
			assert.Equal(t, common.TopUpStatusPending, order.Status)

			var subscriptions int64
			require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", 504).Count(&subscriptions).Error)
			assert.Zero(t, subscriptions)
			assert.Nil(t, GetTopUpByTradeNo("sub-no-currency"))
		})
	}
}

// The synthetic TopUp row a subscription writes carries Amount=0 with Money>0,
// so it must be tagged as a subscription order to stay out of wallet-credit
// reporting, while ordinary wallet top-ups default to the wallet kind.
func TestTopUpOrderKindSeparatesSubscriptionFromWallet(t *testing.T) {
	truncateTables(t)
	insertSubscriptionFixtureForPaidAmountTest(t, "sub-order-kind", 503, 603, 9.99, "USD", PaymentProviderEpay)

	require.NoError(t, CompleteSubscriptionOrder("sub-order-kind", `{}`, PaymentProviderEpay, "alipay",
		SubscriptionPaidAmount{Amount: decimal.NewFromFloat(9.99), Currency: "USD", Reported: true}))

	subscriptionTopUp := GetTopUpByTradeNo("sub-order-kind")
	require.NotNil(t, subscriptionTopUp)
	assert.Equal(t, TopUpOrderKindSubscription, subscriptionTopUp.OrderKind)
	assert.True(t, subscriptionTopUp.IsSubscriptionOrder())
	assert.Zero(t, subscriptionTopUp.Amount)
	assert.Positive(t, subscriptionTopUp.Money)

	walletTopUp := &TopUp{
		UserId:          503,
		Amount:          10,
		Money:           9.99,
		TradeNo:         "wallet-order-kind",
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		Status:          common.TopUpStatusPending,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, walletTopUp.Insert())

	stored := GetTopUpByTradeNo("wallet-order-kind")
	require.NotNil(t, stored)
	assert.Equal(t, TopUpOrderKindWallet, stored.OrderKind)
	assert.False(t, stored.IsSubscriptionOrder())
}
