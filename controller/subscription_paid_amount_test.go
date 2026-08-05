package controller

import (
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
)

func stripeCheckoutEvent(amountTotal any, currency string) stripe.Event {
	object := map[string]any{"currency": currency}
	if amountTotal != nil {
		object["amount_total"] = amountTotal
	}
	return stripe.Event{Data: &stripe.EventData{Object: object}}
}

// Stripe reports amount_total in the currency's minor unit, so a plan priced at
// 9.99 arrives as 999. Converting with a fixed /100 would report a 100x
// underpayment for zero-decimal currencies and a 10x overpayment for
// three-decimal ones, either rejecting a valid settlement or accepting a short
// one.
func TestStripeSubscriptionPaidAmountConvertsMinorUnits(t *testing.T) {
	testCases := []struct {
		name        string
		amountTotal any
		currency    string
		expected    string
	}{
		{name: "two decimal usd", amountTotal: 999, currency: "usd", expected: "9.99"},
		{name: "zero decimal jpy", amountTotal: 1500, currency: "jpy", expected: "1500"},
		{name: "three decimal kwd", amountTotal: 9990, currency: "kwd", expected: "9.99"},
		{name: "unknown currency defaults to two decimals", amountTotal: 999, currency: "zzz", expected: "9.99"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			paid := stripeSubscriptionPaidAmount(stripeCheckoutEvent(tc.amountTotal, tc.currency))
			require.True(t, paid.Reported)
			assert.True(t, decimal.RequireFromString(tc.expected).Equal(paid.Amount),
				"expected %s, got %s", tc.expected, paid.Amount.String())
		})
	}
}

// A checkout event without a usable amount must not be treated as a payment;
// CompleteSubscriptionOrder rejects an unreported amount rather than settling.
func TestStripeSubscriptionPaidAmountRejectsMissingAmount(t *testing.T) {
	testCases := []struct {
		name        string
		amountTotal any
	}{
		{name: "absent", amountTotal: nil},
		{name: "empty string", amountTotal: ""},
		{name: "not a number", amountTotal: "free"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			paid := stripeSubscriptionPaidAmount(stripeCheckoutEvent(tc.amountTotal, "usd"))
			assert.False(t, paid.Reported)
		})
	}
}

// An event that names an amount but no currency must not produce a reported
// paid amount: without the currency the minor-unit exponent is a guess and the
// model-level comparison would run on an unlabeled magnitude.
func TestStripeSubscriptionPaidAmountRejectsMissingCurrency(t *testing.T) {
	for _, currency := range []string{"", "   "} {
		paid := stripeSubscriptionPaidAmount(stripeCheckoutEvent("999", currency))
		assert.False(t, paid.Reported)
		assert.Empty(t, paid.Currency)
	}
}

func TestCreemSubscriptionPaidAmountConvertsMinorUnits(t *testing.T) {
	var event CreemWebhookEvent
	event.Object.Order.AmountPaid = 999
	event.Object.Order.Amount = 1299
	event.Object.Order.Currency = "usd"

	paid := creemSubscriptionPaidAmount(&event)
	require.True(t, paid.Reported)
	assert.Equal(t, "USD", paid.Currency)
	// AmountPaid, not the pre-discount Amount.
	assert.True(t, decimal.RequireFromString("9.99").Equal(paid.Amount), "got %s", paid.Amount.String())

	assert.False(t, creemSubscriptionPaidAmount(nil).Reported)
}

// AmountPaid present but no currency: the webhook does not report a payment.
func TestCreemSubscriptionPaidAmountRejectsMissingCurrency(t *testing.T) {
	var event CreemWebhookEvent
	event.Object.Order.AmountPaid = 999
	event.Object.Order.Currency = "  "

	paid := creemSubscriptionPaidAmount(&event)
	assert.False(t, paid.Reported)
	assert.Empty(t, paid.Currency)
}

// Pancake echoes the decimal string we submitted at checkout, in major units.
func TestWaffoPancakeSubscriptionPaidAmount(t *testing.T) {
	testCases := []struct {
		name         string
		amount       string
		wantReported bool
		expected     string
	}{
		{name: "decimal string", amount: "9.99", wantReported: true, expected: "9.99"},
		{name: "whole units", amount: "12", wantReported: true, expected: "12"},
		{name: "surrounding whitespace", amount: " 9.99 ", wantReported: true, expected: "9.99"},
		{name: "empty", amount: "", wantReported: false},
		{name: "not a number", amount: "free", wantReported: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			event := &service.WaffoPancakeWebhookEvent{
				Data: service.WaffoPancakeWebhookData{Amount: tc.amount, Currency: "usd"},
			}
			paid := waffoPancakeSubscriptionPaidAmount(event)
			require.Equal(t, tc.wantReported, paid.Reported)
			if tc.wantReported {
				assert.Equal(t, "USD", paid.Currency)
				assert.True(t, decimal.RequireFromString(tc.expected).Equal(paid.Amount), "got %s", paid.Amount.String())
			}
		})
	}

	assert.False(t, waffoPancakeSubscriptionPaidAmount(nil).Reported)

	// Amount present but no currency: the amount is only meaningful in the
	// units Data.Currency names, so the webhook does not report a payment.
	noCurrency := waffoPancakeSubscriptionPaidAmount(&service.WaffoPancakeWebhookEvent{
		Data: service.WaffoPancakeWebhookData{Amount: "9.99", Currency: " "},
	})
	assert.False(t, noCurrency.Reported)
}

// epay echoes the money string we submitted and names no currency, so the
// comparison in CompleteSubscriptionOrder must run against the plan's price.
// This is the only extractor allowed to return Reported=true with an empty
// Currency; the model layer additionally rejects that shape for any order
// whose provider is not epay.
func TestEpaySubscriptionPaidAmount(t *testing.T) {
	paid := epaySubscriptionPaidAmount(&epay.VerifyRes{Money: "9.99"})
	require.True(t, paid.Reported)
	assert.Empty(t, paid.Currency)
	assert.True(t, decimal.RequireFromString("9.99").Equal(paid.Amount), "got %s", paid.Amount.String())

	assert.False(t, epaySubscriptionPaidAmount(&epay.VerifyRes{Money: ""}).Reported)
	assert.False(t, epaySubscriptionPaidAmount(&epay.VerifyRes{Money: "free"}).Reported)
	assert.False(t, epaySubscriptionPaidAmount(nil).Reported)
}
