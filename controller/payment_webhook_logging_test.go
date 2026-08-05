package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81/webhook"
	waffo "github.com/waffo-com/waffo-go"
	waffoutils "github.com/waffo-com/waffo-go/utils"
	"gorm.io/gorm"
)

func capturePaymentWebhookLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultWriter = &output
	gin.DefaultErrorWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		gin.DefaultErrorWriter = previousErrorWriter
		common.LogWriterMu.Unlock()
	})
	return &output
}

func paymentWebhookRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	return request.WithContext(context.WithValue(
		request.Context(),
		common.RequestIdKey,
		"payment-webhook-test-request",
	))
}

func usePaymentWebhookLoggingTestSettings(t *testing.T) {
	t.Helper()
	confirmPaymentComplianceForTest(t)
	previousStripeAPISecret := setting.StripeApiSecret
	previousStripeWebhookSecret := setting.StripeWebhookSecret
	previousStripePriceID := setting.StripePriceId
	previousCreemAPIKey := setting.CreemApiKey
	previousCreemWebhookSecret := setting.CreemWebhookSecret
	previousCreemProducts := setting.CreemProducts
	t.Cleanup(func() {
		setting.StripeApiSecret = previousStripeAPISecret
		setting.StripeWebhookSecret = previousStripeWebhookSecret
		setting.StripePriceId = previousStripePriceID
		setting.CreemApiKey = previousCreemAPIKey
		setting.CreemWebhookSecret = previousCreemWebhookSecret
		setting.CreemProducts = previousCreemProducts
	})

	setting.StripeApiSecret = "sk_test_payment_webhook"
	setting.StripeWebhookSecret = "whsec_payment_webhook"
	setting.StripePriceId = "price_payment_webhook"

	setting.CreemApiKey = "creem_api_payment_webhook"
	setting.CreemWebhookSecret = "creem_secret_payment_webhook"
	setting.CreemProducts = `[{"productId":"prod_payment_webhook"}]`
}

func useWaffoWebhookLoggingTestSettings(t *testing.T) *waffoutils.KeyPair {
	t.Helper()
	confirmPaymentComplianceForTest(t)
	previousEnabled := setting.WaffoEnabled
	previousSandbox := setting.WaffoSandbox
	previousSandboxAPIKey := setting.WaffoSandboxApiKey
	previousSandboxPrivateKey := setting.WaffoSandboxPrivateKey
	previousSandboxPublicCert := setting.WaffoSandboxPublicCert
	t.Cleanup(func() {
		setting.WaffoEnabled = previousEnabled
		setting.WaffoSandbox = previousSandbox
		setting.WaffoSandboxApiKey = previousSandboxAPIKey
		setting.WaffoSandboxPrivateKey = previousSandboxPrivateKey
		setting.WaffoSandboxPublicCert = previousSandboxPublicCert
	})

	keyPair, err := waffo.GenerateKeyPair()
	require.NoError(t, err)
	setting.WaffoEnabled = true
	setting.WaffoSandbox = true
	setting.WaffoSandboxApiKey = "waffo-api-payment-webhook"
	setting.WaffoSandboxPrivateKey = keyPair.PrivateKey
	setting.WaffoSandboxPublicCert = keyPair.PublicKey
	return keyPair
}

func useWaffoPancakeWebhookLoggingTestSettings(t *testing.T) {
	t.Helper()
	confirmPaymentComplianceForTest(t)
	previousMerchantID := setting.WaffoPancakeMerchantID
	previousPrivateKey := setting.WaffoPancakePrivateKey
	previousProductID := setting.WaffoPancakeProductID
	t.Cleanup(func() {
		setting.WaffoPancakeMerchantID = previousMerchantID
		setting.WaffoPancakePrivateKey = previousPrivateKey
		setting.WaffoPancakeProductID = previousProductID
	})

	setting.WaffoPancakeMerchantID = "merchant-payment-webhook"
	setting.WaffoPancakePrivateKey = "private-payment-webhook"
	setting.WaffoPancakeProductID = "product-payment-webhook"
}

func useEpayWebhookLoggingTestSettings(t *testing.T) {
	t.Helper()
	confirmPaymentComplianceForTest(t)
	previousPayAddress := operation_setting.PayAddress
	previousEpayID := operation_setting.EpayId
	previousEpayKey := operation_setting.EpayKey
	previousPayMethods := operation_setting.PayMethods
	t.Cleanup(func() {
		operation_setting.PayAddress = previousPayAddress
		operation_setting.EpayId = previousEpayID
		operation_setting.EpayKey = previousEpayKey
		operation_setting.PayMethods = previousPayMethods
	})

	operation_setting.PayAddress = "https://pay.example.com"
	operation_setting.EpayId = "epay-payment-webhook"
	operation_setting.EpayKey = "epay-key-payment-webhook"
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
}

func usePaymentWebhookControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
	})

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	return db
}

func TestPaymentWebhookLogFieldEscapesAndBoundsExternalValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "controls and quotes",
			value: "event\r\nnext\t\"quoted\"",
			want:  `"event\r\nnext\t\"quoted\""`,
		},
		{
			name:  "invalid utf8",
			value: string([]byte{'a', 0xff, 'b'}),
			want:  `"a\xffb"`,
		},
		{
			name:  "ascii truncation",
			value: strings.Repeat("a", 129),
			want:  `"` + strings.Repeat("a", 128) + `...[truncated]"`,
		},
		{
			name:  "multibyte boundary",
			value: strings.Repeat("a", 127) + "界",
			want:  `"` + strings.Repeat("a", 127) + `...[truncated]"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, paymentWebhookLogField(test.value))
		})
	}
}

func TestStripeWebhookSafeLogs(t *testing.T) {
	usePaymentWebhookLoggingTestSettings(t)

	t.Run("invalid signature omits callback secrets", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		const body = `{"email":"customer-body-log-sentinel@example.com"}`
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(
			http.MethodPost,
			"/api/stripe/webhook?query=query-log-sentinel",
			body,
		)
		ctx.Request.Header.Set("Stripe-Signature", "signature-log-sentinel")

		StripeWebhook(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Contains(t, logs, "payment_webhook provider=stripe")
		assert.Contains(t, logs, "phase=rejected")
		assert.Contains(t, logs, "signature_present=true")
		assert.Contains(t, logs, "body_bytes=")
		assert.NotContains(t, logs, "customer-body-log-sentinel@example.com")
		assert.NotContains(t, logs, "signature-log-sentinel")
		assert.NotContains(t, logs, "query-log-sentinel")
	})

	t.Run("valid ignored event logs only safe metadata", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		payload := []byte(`{"id":"evt_safe","type":"invoice.created","data":{"object":{"email":"stripe-customer-log-sentinel@example.com"}}}`)
		signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
			Payload: payload,
			Secret:  setting.StripeWebhookSecret,
		})
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(
			http.MethodPost,
			"/api/stripe/webhook?query=query-log-sentinel",
			string(payload),
		)
		ctx.Request.Header.Set("Stripe-Signature", signed.Header)

		StripeWebhook(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, logs, "payment_webhook provider=stripe")
		assert.Contains(t, logs, `event_type="invoice.created"`)
		assert.NotContains(t, logs, "stripe-customer-log-sentinel@example.com")
		assert.NotContains(t, logs, strconv.Quote(string(payload)))
		assert.NotContains(t, logs, "query-log-sentinel")
	})
}

func TestCreemWebhookSafeLogs(t *testing.T) {
	usePaymentWebhookLoggingTestSettings(t)

	t.Run("invalid signature omits callback secrets", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		const body = `{"email":"customer-body-log-sentinel@example.com"}`
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(
			http.MethodPost,
			"/api/creem/webhook?query=query-log-sentinel",
			body,
		)
		ctx.Request.Header.Set(CreemSignatureHeader, "signature-log-sentinel")

		CreemWebhook(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusUnauthorized, response.Code)
		assert.Contains(t, logs, "payment_webhook provider=creem")
		assert.Contains(t, logs, "phase=rejected")
		assert.Contains(t, logs, "signature_present=true")
		assert.Contains(t, logs, "body_bytes=")
		assert.NotContains(t, logs, "customer-body-log-sentinel@example.com")
		assert.NotContains(t, logs, "signature-log-sentinel")
		assert.NotContains(t, logs, "query-log-sentinel")
	})

	t.Run("valid ignored event logs only safe metadata", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		const payload = `{"id":"evt_creem\r\nforged","eventType":"ignored.event","object":{"request_id":"safe-request","order":{"id":"ord_safe","status":"pending"},"customer":{"email":"creem-customer-log-sentinel@example.com","name":"creem-name-log-sentinel"},"product":{"name":"creem-product-log-sentinel"}}}`
		signature := generateCreemSignature(payload, setting.CreemWebhookSecret)
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(http.MethodPost, "/api/creem/webhook", payload)
		ctx.Request.Header.Set(CreemSignatureHeader, signature)

		CreemWebhook(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, logs, "payment_webhook provider=creem")
		assert.Contains(t, logs, `event_type="ignored.event"`)
		assert.Contains(t, logs, `event_id="evt_creem\r\nforged"`)
		assert.Contains(t, logs, `request_id="safe-request"`)
		assert.Contains(t, logs, `order_id="ord_safe"`)
		assert.Contains(t, logs, `order_status="pending"`)
		assert.NotContains(t, logs, "creem-customer-log-sentinel@example.com")
		assert.NotContains(t, logs, "creem-name-log-sentinel")
		assert.NotContains(t, logs, "creem-product-log-sentinel")
	})

	t.Run("paid onetime missing local order omits PII", func(t *testing.T) {
		usePaymentWebhookControllerTestDB(t)
		output := capturePaymentWebhookLogs(t)
		const payload = `{"id":"evt_creem_paid","eventType":"checkout.completed","object":{"request_id":"safe-request-paid","order":{"id":"ord_safe_paid","status":"paid","type":"onetime","amount_paid":1200,"currency":"USD"},"customer":{"email":"creem-paid-customer-log-sentinel@example.com","name":"creem-paid-name-log-sentinel"},"product":{"name":"creem-paid-product-log-sentinel"}}}`
		signature := generateCreemSignature(payload, setting.CreemWebhookSecret)
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(http.MethodPost, "/api/creem/webhook", payload)
		ctx.Request.Header.Set(CreemSignatureHeader, signature)

		CreemWebhook(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Contains(t, logs, `event_type="checkout.completed"`)
		assert.Contains(t, logs, `event_id="evt_creem_paid"`)
		assert.Contains(t, logs, `request_id="safe-request-paid"`)
		assert.Contains(t, logs, `order_id="ord_safe_paid"`)
		assert.Contains(t, logs, `order_status="paid"`)
		assert.Contains(t, logs, `order_type="onetime"`)
		assert.NotContains(t, logs, "creem-paid-customer-log-sentinel@example.com")
		assert.NotContains(t, logs, "creem-paid-name-log-sentinel")
		assert.NotContains(t, logs, "creem-paid-product-log-sentinel")
	})
}

func TestEpayWebhookSafeLogs(t *testing.T) {
	useEpayWebhookLoggingTestSettings(t)

	t.Run("GET invalid signature omits callback secrets", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		const rawQuery = "sign=epay-signature-log-sentinel&name=epay-name-log-sentinel&extra=epay-query-log-sentinel"
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(
			http.MethodGet,
			"/api/user/epay/notify?"+rawQuery,
			"",
		)

		EpayNotify(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "fail", response.Body.String())
		assert.Contains(t, logs, "payment_webhook provider=epay")
		assert.Contains(t, logs, "phase=received")
		assert.Contains(t, logs, "phase=rejected")
		assert.Contains(t, logs, `method="GET"`)
		assert.Contains(t, logs, "parameter_count=3")
		assert.Contains(t, logs, "signature_present=true")
		assert.NotContains(t, logs, "epay-signature-log-sentinel")
		assert.NotContains(t, logs, "epay-name-log-sentinel")
		assert.NotContains(t, logs, "epay-query-log-sentinel")
		assert.NotContains(t, logs, rawQuery)
	})

	t.Run("POST invalid signature omits callback secrets", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		const form = "sign=epay-signature-log-sentinel&name=epay-name-log-sentinel&extra=epay-query-log-sentinel"
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(http.MethodPost, "/api/user/epay/notify", form)
		ctx.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		EpayNotify(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "fail", response.Body.String())
		assert.Contains(t, logs, "payment_webhook provider=epay")
		assert.Contains(t, logs, "phase=received")
		assert.Contains(t, logs, "phase=rejected")
		assert.Contains(t, logs, `method="POST"`)
		assert.Contains(t, logs, "parameter_count=3")
		assert.Contains(t, logs, "signature_present=true")
		assert.NotContains(t, logs, "epay-signature-log-sentinel")
		assert.NotContains(t, logs, "epay-name-log-sentinel")
		assert.NotContains(t, logs, "epay-query-log-sentinel")
		assert.NotContains(t, logs, form)
	})

	t.Run("valid non-success status keeps acknowledgement and safe metadata", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		params := epay.GenerateParams(map[string]string{
			"type":         "alipay",
			"trade_no":     "provider-order-safe",
			"out_trade_no": "internal-order-safe",
			"name":         "epay-product-log-sentinel",
			"money":        "1.00",
			"trade_status": "WAIT_BUYER_PAY",
		}, operation_setting.EpayKey)
		query := make(url.Values, len(params))
		for key, value := range params {
			query.Set(key, value)
		}
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(
			http.MethodGet,
			"/api/user/epay/notify?"+query.Encode(),
			"",
		)

		EpayNotify(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "success", response.Body.String())
		assert.Contains(t, logs, "payment_webhook provider=epay")
		assert.Contains(t, logs, `trade_no="internal-order-safe"`)
		assert.Contains(t, logs, `callback_type="alipay"`)
		assert.Contains(t, logs, `trade_status="WAIT_BUYER_PAY"`)
		assert.NotContains(t, logs, "epay-product-log-sentinel")
		assert.NotContains(t, logs, "verify_info=")
		assert.NotContains(t, logs, params["sign"])
	})
}

func TestWaffoWebhookSafeLogs(t *testing.T) {
	keyPair := useWaffoWebhookLoggingTestSettings(t)

	t.Run("invalid signature omits callback secrets", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		const body = `{"secret":"waffo-body-log-sentinel"}`
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(
			http.MethodPost,
			"/api/waffo/webhook?query=waffo-query-log-sentinel",
			body,
		)
		ctx.Request.Header.Set("X-SIGNATURE", "waffo-signature-log-sentinel")

		WaffoWebhook(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Contains(t, logs, "payment_webhook provider=waffo")
		assert.Contains(t, logs, "phase=rejected")
		assert.Contains(t, logs, "reason=signature_invalid")
		assert.Contains(t, logs, "signature_present=true")
		assert.Contains(t, logs, "body_bytes=")
		assert.NotContains(t, logs, "waffo-body-log-sentinel")
		assert.NotContains(t, logs, "waffo-signature-log-sentinel")
		assert.NotContains(t, logs, "waffo-query-log-sentinel")
	})

	t.Run("valid ignored event keeps signed response and safe metadata", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		const payload = `{"eventType":"IGNORED\r\nforged","secret":"waffo-body-log-sentinel"}`
		signature, err := waffoutils.Sign(payload, keyPair.PrivateKey)
		require.NoError(t, err)
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(http.MethodPost, "/api/waffo/webhook", payload)
		ctx.Request.Header.Set("X-SIGNATURE", signature)

		WaffoWebhook(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, `{"message":"success"}`, response.Body.String())
		responseSignature := response.Header().Get("X-SIGNATURE")
		require.NotEmpty(t, responseSignature)
		assert.True(t, waffoutils.Verify(response.Body.String(), responseSignature, keyPair.PublicKey))
		assert.Contains(t, logs, "payment_webhook provider=waffo")
		assert.Contains(t, logs, "phase=ignored")
		assert.Contains(t, logs, "reason=status_ignored")
		assert.Contains(t, logs, `event_type="IGNORED\r\nforged"`)
		assert.NotContains(t, logs, "waffo-body-log-sentinel")
		assert.NotContains(t, logs, signature)
	})
}

func TestWaffoPancakeWebhookSafeLogs(t *testing.T) {
	useWaffoPancakeWebhookLoggingTestSettings(t)

	t.Run("invalid signature omits callback secrets", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		const payload = `{"id":"evt_invalid","mode":"test","eventType":"order.completed","body_secret":"pancake-body-log-sentinel","data":{"orderId":"ord_invalid","buyerEmail":"pancake-buyer-email-log-sentinel@example.com","merchantProvidedBuyerIdentity":"pancake-buyer-identity-log-sentinel","productName":"pancake-product-log-sentinel"}}`
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(
			http.MethodPost,
			"/api/waffo-pancake/webhook/test?query=pancake-query-log-sentinel",
			payload,
		)
		ctx.Params = gin.Params{{Key: "env", Value: "test"}}
		ctx.Request.Header.Set(
			"X-Waffo-Signature",
			fmt.Sprintf(
				"t=%d,v1=cGFuY2FrZS1pbnZhbGlkLXNpZ25hdHVyZQ==,sentinel=pancake-signature-log-sentinel",
				time.Now().UnixMilli(),
			),
		)

		WaffoPancakeWebhook(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusUnauthorized, response.Code)
		assert.Equal(t, "invalid signature", response.Body.String())
		assert.Contains(t, logs, "payment_webhook provider=waffo_pancake")
		assert.Contains(t, logs, "phase=rejected")
		assert.Contains(t, logs, "reason=signature_invalid")
		assert.Contains(t, logs, "signature_present=true")
		assert.Contains(t, logs, "body_bytes=")
		assert.NotContains(t, logs, "pancake-body-log-sentinel")
		assert.NotContains(t, logs, "pancake-buyer-email-log-sentinel@example.com")
		assert.NotContains(t, logs, "pancake-buyer-identity-log-sentinel")
		assert.NotContains(t, logs, "pancake-product-log-sentinel")
		assert.NotContains(t, logs, "pancake-signature-log-sentinel")
		assert.NotContains(t, logs, "pancake-query-log-sentinel")
	})

	t.Run("invalid environment omits query and escapes metadata", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = paymentWebhookRequest(
			http.MethodPost,
			"/api/waffo-pancake/webhook/invalid?query=pancake-query-log-sentinel",
			"",
		)
		ctx.Params = gin.Params{{Key: "env", Value: "invalid\r\nforged"}}

		WaffoPancakeWebhook(ctx)

		logs := output.String()
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, "unknown env", response.Body.String())
		assert.Contains(t, logs, "payment_webhook provider=waffo_pancake")
		assert.Contains(t, logs, "phase=rejected")
		assert.Contains(t, logs, "reason=invalid_env")
		assert.Contains(t, logs, `env="invalid\r\nforged"`)
		assert.Contains(t, logs, `path="/api/waffo-pancake/webhook/invalid"`)
		assert.NotContains(t, logs, "pancake-query-log-sentinel")
	})

	t.Run("resolution failure omits buyer identity and secret comparisons", func(t *testing.T) {
		output := capturePaymentWebhookLogs(t)
		event := &service.WaffoPancakeWebhookEvent{
			ID: "evt_safe",
			Data: service.WaffoPancakeWebhookData{
				OrderID:                       "ord_safe",
				MerchantProvidedBuyerIdentity: "buyer-identity-log-sentinel",
			},
		}

		logWaffoPancakeResolutionFailure(context.Background(), event, false)

		logs := output.String()
		assert.Contains(t, logs, `event_id="evt_safe"`)
		assert.Contains(t, logs, `order_id="ord_safe"`)
		assert.Contains(t, logs, "reason=order_resolution_failed")
		assert.NotContains(t, logs, "buyer-identity-log-sentinel")
		assert.NotContains(t, logs, "expected=")
		assert.NotContains(t, logs, "actual=")
	})
}
