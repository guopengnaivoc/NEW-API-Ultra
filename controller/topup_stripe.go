package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/stripe/stripe-go/v81/webhook"
	"github.com/thanhpk/randstr"
)

var stripeAdaptor = &StripeAdaptor{}

// StripePayRequest represents a payment request for Stripe checkout.
type StripePayRequest struct {
	// Amount is the quantity of units to purchase.
	Amount int64 `json:"amount"`
	// PaymentMethod specifies the payment method (e.g., "stripe").
	PaymentMethod string `json:"payment_method"`
	// SuccessURL is the optional custom URL to redirect after successful payment.
	// If empty, defaults to the server's console log page.
	SuccessURL string `json:"success_url,omitempty"`
	// CancelURL is the optional custom URL to redirect when payment is canceled.
	// If empty, defaults to the server's console topup page.
	CancelURL string `json:"cancel_url,omitempty"`
}

type StripeAdaptor struct {
}

func validateStripeTopUpQuota(money float64) error {
	quota, err := common.QuotaFromDecimalStrict(
		decimal.NewFromFloat(money).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit)),
	)
	if err != nil {
		return err
	}
	if quota <= 0 {
		return fmt.Errorf("invalid top-up quota")
	}
	return nil
}

func (*StripeAdaptor) RequestAmount(c *gin.Context, req *StripePayRequest) {
	if req.Amount < getStripeMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getStripeMinTopup())})
		return
	}
	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getStripePayMoney(float64(req.Amount), group)
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	if err := validateStripeTopUpQuota(payMoney); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值数量无效"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

func (*StripeAdaptor) RequestPay(c *gin.Context, req *StripePayRequest) {
	if req.PaymentMethod != model.PaymentMethodStripe {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付渠道"})
		return
	}
	if req.Amount < getStripeMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("充值数量不能小于 %d", getStripeMinTopup()), "data": 10})
		return
	}
	if req.Amount > 10000 {
		c.JSON(http.StatusOK, gin.H{"message": "充值数量不能大于 10000", "data": 10})
		return
	}

	if req.SuccessURL != "" && common.ValidateRedirectURL(req.SuccessURL) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "支付成功重定向URL不在可信任域名列表中", "data": ""})
		return
	}

	if req.CancelURL != "" && common.ValidateRedirectURL(req.CancelURL) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "支付取消重定向URL不在可信任域名列表中", "data": ""})
		return
	}

	id := c.GetInt("id")
	user, _ := model.GetUserById(id, false)
	chargedMoney := GetChargedAmount(float64(req.Amount), *user)
	if err := validateStripeTopUpQuota(chargedMoney); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值数量无效"})
		return
	}

	reference := fmt.Sprintf("new-api-ref-%d-%d-%s", user.Id, time.Now().UnixMilli(), randstr.String(4))
	referenceId := "ref_" + common.Sha1([]byte(reference))

	payLink, err := genStripeLink(referenceId, user.StripeCustomer, user.Email, req.Amount, req.SuccessURL, req.CancelURL)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 创建 Checkout Session 失败 user_id=%d trade_no=%s amount=%d error=%q", id, referenceId, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	topUp := &model.TopUp{
		UserId:          id,
		Amount:          req.Amount,
		Money:           chargedMoney,
		TradeNo:         referenceId,
		PaymentMethod:   model.PaymentMethodStripe,
		PaymentProvider: model.PaymentProviderStripe,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	err = topUp.Insert()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, referenceId, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Stripe 充值订单创建成功 user_id=%d trade_no=%s amount=%d money=%.2f", id, referenceId, req.Amount, chargedMoney))
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"pay_link": payLink,
		},
	})
}

func RequestStripeAmount(c *gin.Context) {
	var req StripePayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	stripeAdaptor.RequestAmount(c, &req)
}

func RequestStripePay(c *gin.Context) {
	var req StripePayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	stripeAdaptor.RequestPay(c, &req)
}

func StripeWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	if !isStripeWebhookEnabled() {
		logger.LogWarn(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=rejected reason=webhook_disabled path=%s",
			paymentWebhookLogField(c.Request.URL.Path),
		))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=rejected reason=body_read_failed path=%s error_type=%T",
			paymentWebhookLogField(c.Request.URL.Path), err,
		))
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}

	signature := c.GetHeader("Stripe-Signature")
	logger.LogInfo(ctx, fmt.Sprintf(
		"payment_webhook provider=stripe phase=received path=%s body_bytes=%d signature_present=%t",
		paymentWebhookLogField(c.Request.URL.Path), len(payload), signature != "",
	))
	event, err := webhook.ConstructEventWithOptions(payload, signature, setting.StripeWebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})

	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=rejected reason=signature_invalid path=%s body_bytes=%d signature_present=%t",
			paymentWebhookLogField(c.Request.URL.Path), len(payload), signature != "",
		))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	callerIp := c.ClientIP()
	logger.LogInfo(ctx, fmt.Sprintf(
		"payment_webhook provider=stripe phase=verified event_type=%s body_bytes=%d",
		paymentWebhookLogField(string(event.Type)), len(payload),
	))
	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted:
		sessionCompleted(ctx, event, callerIp)
	case stripe.EventTypeCheckoutSessionExpired:
		sessionExpired(ctx, event)
	case stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded:
		sessionAsyncPaymentSucceeded(ctx, event, callerIp)
	case stripe.EventTypeCheckoutSessionAsyncPaymentFailed:
		sessionAsyncPaymentFailed(ctx, event, callerIp)
	default:
		logger.LogInfo(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=ignored event_type=%s",
			paymentWebhookLogField(string(event.Type)),
		))
	}

	c.Status(http.StatusOK)
}

func sessionCompleted(ctx context.Context, event stripe.Event, callerIp string) {
	customerId := event.GetObjectValue("customer")
	referenceId := event.GetObjectValue("client_reference_id")
	status := event.GetObjectValue("status")
	if "complete" != status {
		logger.LogWarn(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=ignored reason=status_invalid event_type=%s trade_no=%s status=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(status),
		))
		return
	}

	paymentStatus := event.GetObjectValue("payment_status")
	if paymentStatus != "paid" {
		logger.LogInfo(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=deferred event_type=%s trade_no=%s payment_status=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(paymentStatus),
		))
		return
	}

	fulfillOrder(ctx, event, referenceId, customerId, callerIp)
}

// sessionAsyncPaymentSucceeded handles delayed payment methods (bank transfer, SEPA, etc.)
// that confirm payment after the checkout session completes.
func sessionAsyncPaymentSucceeded(ctx context.Context, event stripe.Event, callerIp string) {
	customerId := event.GetObjectValue("customer")
	referenceId := event.GetObjectValue("client_reference_id")
	logger.LogInfo(ctx, fmt.Sprintf(
		"payment_webhook provider=stripe phase=processing event_type=%s trade_no=%s",
		paymentWebhookLogField(string(event.Type)),
		paymentWebhookLogField(referenceId),
	))

	fulfillOrder(ctx, event, referenceId, customerId, callerIp)
}

// sessionAsyncPaymentFailed marks orders as failed when delayed payment methods
// ultimately fail (e.g. bank transfer not received, SEPA rejected).
func sessionAsyncPaymentFailed(ctx context.Context, event stripe.Event, callerIp string) {
	referenceId := event.GetObjectValue("client_reference_id")
	logger.LogWarn(ctx, fmt.Sprintf(
		"payment_webhook provider=stripe phase=processing event_type=%s trade_no=%s",
		paymentWebhookLogField(string(event.Type)),
		paymentWebhookLogField(referenceId),
	))

	if len(referenceId) == 0 {
		logger.LogWarn(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=rejected reason=order_not_found event_type=%s trade_no=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
		))
		return
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)

	topUp := model.GetTopUpByTradeNo(referenceId)
	if topUp == nil {
		logger.LogWarn(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=rejected reason=order_not_found event_type=%s trade_no=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
		))
		return
	}

	if topUp.PaymentProvider != model.PaymentProviderStripe {
		logger.LogWarn(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=rejected reason=provider_mismatch event_type=%s trade_no=%s payment_provider=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(topUp.PaymentProvider),
		))
		return
	}

	if topUp.Status != common.TopUpStatusPending {
		logger.LogInfo(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=ignored reason=status_invalid event_type=%s trade_no=%s status=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(topUp.Status),
		))
		return
	}

	topUp.Status = common.TopUpStatusFailed
	if err := topUp.Update(); err != nil {
		logger.LogError(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=failed reason=settlement_failed event_type=%s trade_no=%s error_type=%T",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
			err,
		))
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf(
		"payment_webhook provider=stripe phase=settled event_type=%s trade_no=%s status=%s",
		paymentWebhookLogField(string(event.Type)),
		paymentWebhookLogField(referenceId),
		paymentWebhookLogField(topUp.Status),
	))
}

// stripeZeroDecimalCurrencies lists the currencies Stripe charges in whole
// units, where amount_total is already a major-unit amount. Dividing those by
// 100 would report a 100x underpayment and reject a valid settlement.
// https://docs.stripe.com/currencies#zero-decimal
var stripeZeroDecimalCurrencies = map[string]struct{}{
	"BIF": {}, "CLP": {}, "DJF": {}, "GNF": {}, "JPY": {}, "KMF": {},
	"KRW": {}, "MGA": {}, "PYG": {}, "RWF": {}, "UGX": {}, "VND": {},
	"VUV": {}, "XAF": {}, "XOF": {}, "XPF": {},
}

// stripeThreeDecimalCurrencies lists the currencies whose minor unit is a
// thousandth rather than a hundredth.
// https://docs.stripe.com/currencies#three-decimal
var stripeThreeDecimalCurrencies = map[string]struct{}{
	"BHD": {}, "JOD": {}, "KWD": {}, "OMR": {}, "TND": {},
}

// stripeSubscriptionPaidAmount converts the checkout session's amount_total,
// which Stripe reports in the currency's minor unit, into major units. A missing
// or unparseable value means the event did not report a payment and settlement
// must not proceed. The currency is equally load-bearing: without it the
// minor-unit exponent is a guess and the amount cannot be compared to the plan
// price, so an event that names an amount but no currency is treated as not
// reporting a payment at all.
func stripeSubscriptionPaidAmount(event stripe.Event) model.SubscriptionPaidAmount {
	raw := strings.TrimSpace(event.GetObjectValue("amount_total"))
	if raw == "" {
		return model.SubscriptionPaidAmount{}
	}
	minorUnits, err := decimal.NewFromString(raw)
	if err != nil {
		return model.SubscriptionPaidAmount{}
	}
	currency := strings.ToUpper(strings.TrimSpace(event.GetObjectValue("currency")))
	if currency == "" {
		return model.SubscriptionPaidAmount{}
	}
	exponent := int32(-2)
	if _, ok := stripeZeroDecimalCurrencies[currency]; ok {
		exponent = 0
	} else if _, ok := stripeThreeDecimalCurrencies[currency]; ok {
		exponent = -3
	}
	return model.SubscriptionPaidAmount{
		Amount:   minorUnits.Shift(exponent),
		Currency: currency,
		Reported: true,
	}
}

// fulfillOrder is the shared logic for crediting quota after payment is confirmed.
func fulfillOrder(ctx context.Context, event stripe.Event, referenceId string, customerId string, callerIp string) {
	if len(referenceId) == 0 {
		logger.LogWarn(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=rejected reason=order_not_found event_type=%s trade_no=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
		))
		return
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	payload := map[string]any{
		"customer":     customerId,
		"amount_total": event.GetObjectValue("amount_total"),
		"currency":     strings.ToUpper(event.GetObjectValue("currency")),
		"event_type":   string(event.Type),
	}
	if err := model.CompleteSubscriptionOrder(referenceId, common.GetJsonString(payload), model.PaymentProviderStripe, "", stripeSubscriptionPaidAmount(event)); err == nil {
		logger.LogInfo(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=settled order_kind=subscription trade_no=%s event_type=%s",
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(string(event.Type)),
		))
		return
	} else if err != nil && !errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		logger.LogError(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=failed reason=settlement_failed order_kind=subscription trade_no=%s event_type=%s error_type=%T",
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(string(event.Type)),
			err,
		))
		return
	}

	err := model.Recharge(referenceId, customerId, callerIp)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=failed reason=settlement_failed order_kind=topup trade_no=%s event_type=%s error_type=%T",
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(string(event.Type)),
			err,
		))
		return
	}

	total, _ := strconv.ParseFloat(event.GetObjectValue("amount_total"), 64)
	currency := strings.ToUpper(event.GetObjectValue("currency"))
	logger.LogInfo(ctx, fmt.Sprintf(
		"payment_webhook provider=stripe phase=settled order_kind=topup trade_no=%s amount_total=%.2f currency=%s event_type=%s",
		paymentWebhookLogField(referenceId),
		total/100,
		paymentWebhookLogField(currency),
		paymentWebhookLogField(string(event.Type)),
	))
}

func sessionExpired(ctx context.Context, event stripe.Event) {
	referenceId := event.GetObjectValue("client_reference_id")
	status := event.GetObjectValue("status")
	if "expired" != status {
		logger.LogWarn(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=ignored reason=status_invalid event_type=%s trade_no=%s status=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(status),
		))
		return
	}

	if len(referenceId) == 0 {
		logger.LogWarn(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=rejected reason=order_not_found event_type=%s trade_no=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
		))
		return
	}

	// Subscription order expiration
	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	if err := model.ExpireSubscriptionOrder(referenceId, model.PaymentProviderStripe); err == nil {
		logger.LogInfo(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=settled order_kind=subscription event_type=%s trade_no=%s status=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(status),
		))
		return
	} else if err != nil && !errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		logger.LogError(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=failed reason=settlement_failed order_kind=subscription event_type=%s trade_no=%s error_type=%T",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
			err,
		))
		return
	}

	err := model.UpdatePendingTopUpStatus(referenceId, model.PaymentProviderStripe, common.TopUpStatusExpired)
	if errors.Is(err, model.ErrTopUpNotFound) {
		logger.LogWarn(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=rejected reason=order_not_found order_kind=topup event_type=%s trade_no=%s",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
		))
		return
	}
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf(
			"payment_webhook provider=stripe phase=failed reason=settlement_failed order_kind=topup event_type=%s trade_no=%s error_type=%T",
			paymentWebhookLogField(string(event.Type)),
			paymentWebhookLogField(referenceId),
			err,
		))
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf(
		"payment_webhook provider=stripe phase=settled order_kind=topup event_type=%s trade_no=%s status=%s",
		paymentWebhookLogField(string(event.Type)),
		paymentWebhookLogField(referenceId),
		paymentWebhookLogField(status),
	))
}

// genStripeLink generates a Stripe Checkout session URL for payment.
// It creates a new checkout session with the specified parameters and returns the payment URL.
//
// Parameters:
//   - referenceId: unique reference identifier for the transaction
//   - customerId: existing Stripe customer ID (empty string if new customer)
//   - email: customer email address for new customer creation
//   - amount: quantity of units to purchase
//   - successURL: custom URL to redirect after successful payment (empty for default)
//   - cancelURL: custom URL to redirect when payment is canceled (empty for default)
//
// Returns the checkout session URL or an error if the session creation fails.
func genStripeLink(referenceId string, customerId string, email string, amount int64, successURL string, cancelURL string) (string, error) {
	if !strings.HasPrefix(setting.StripeApiSecret, "sk_") && !strings.HasPrefix(setting.StripeApiSecret, "rk_") {
		return "", fmt.Errorf("无效的Stripe API密钥")
	}

	stripe.Key = setting.StripeApiSecret

	// Use custom URLs if provided, otherwise use defaults
	if successURL == "" {
		successURL = paymentReturnPath("/usage-logs")
	}
	if cancelURL == "" {
		cancelURL = paymentReturnPath("/wallet")
	}

	params := &stripe.CheckoutSessionParams{
		ClientReferenceID: stripe.String(referenceId),
		SuccessURL:        stripe.String(successURL),
		CancelURL:         stripe.String(cancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(setting.StripePriceId),
				Quantity: stripe.Int64(amount),
			},
		},
		Mode:                stripe.String(string(stripe.CheckoutSessionModePayment)),
		AllowPromotionCodes: stripe.Bool(setting.StripePromotionCodesEnabled),
	}

	if "" == customerId {
		if "" != email {
			params.CustomerEmail = stripe.String(email)
		}

		params.CustomerCreation = stripe.String(string(stripe.CheckoutSessionCustomerCreationAlways))
	} else {
		params.Customer = stripe.String(customerId)
	}

	result, err := session.New(params)
	if err != nil {
		return "", err
	}

	return result.URL, nil
}

func GetChargedAmount(count float64, user model.User) float64 {
	topUpGroupRatio := common.GetTopupGroupRatio(user.Group)
	if topUpGroupRatio == 0 {
		topUpGroupRatio = 1
	}

	return count * topUpGroupRatio
}

func getStripePayMoney(amount float64, group string) float64 {
	originalAmount := amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = amount / common.QuotaPerUnit
	}
	// Using float64 for monetary calculations is acceptable here due to the small amounts involved
	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}
	// apply optional preset discount by the original request amount (if configured), default 1.0
	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(originalAmount)]; ok {
		if ds > 0 {
			discount = ds
		}
	}
	payMoney := amount * setting.StripeUnitPrice * topupGroupRatio * discount
	return payMoney
}

func getStripeMinTopup() int64 {
	minTopup := setting.StripeMinTopUp
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		minTopup = minTopup * int(common.QuotaPerUnit)
	}
	return int64(minTopup)
}
