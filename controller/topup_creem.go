package controller

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

const CreemSignatureHeader = "creem-signature"

var creemAdaptor = &CreemAdaptor{}

// 生成HMAC-SHA256签名
func generateCreemSignature(payload string, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// 验证Creem webhook签名
func verifyCreemSignature(payload string, signature string, secret string) bool {
	if secret == "" {
		return setting.CreemTestMode
	}
	expectedSignature := generateCreemSignature(payload, secret)
	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}

type CreemPayRequest struct {
	ProductId     string `json:"product_id"`
	PaymentMethod string `json:"payment_method"`
}

type CreemProduct struct {
	ProductId string  `json:"productId"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Currency  string  `json:"currency"`
	Quota     int64   `json:"quota"`
}

type CreemAdaptor struct {
}

func validateCreemProductQuota(quota int64) (int64, error) {
	validatedQuota, err := common.QuotaFromDecimalStrict(decimal.NewFromInt(quota))
	if err != nil {
		return 0, err
	}
	if validatedQuota <= 0 {
		return 0, fmt.Errorf("invalid product quota")
	}
	return int64(validatedQuota), nil
}

func (*CreemAdaptor) RequestPay(c *gin.Context, req *CreemPayRequest) {
	if req.PaymentMethod != model.PaymentMethodCreem {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付渠道"})
		return
	}

	if req.ProductId == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "请选择产品"})
		return
	}

	// 解析产品列表
	var products []CreemProduct
	err := common.Unmarshal([]byte(setting.CreemProducts), &products)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 产品配置解析失败 user_id=%d error=%q", c.GetInt("id"), err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "产品配置错误"})
		return
	}

	// 查找对应的产品
	var selectedProduct *CreemProduct
	for _, product := range products {
		if product.ProductId == req.ProductId {
			selectedProduct = &product
			break
		}
	}

	if selectedProduct == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "产品不存在"})
		return
	}
	validatedQuota, err := validateCreemProductQuota(selectedProduct.Quota)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "产品配置错误"})
		return
	}
	selectedProduct.Quota = validatedQuota

	id := c.GetInt("id")
	user, _ := model.GetUserById(id, false)

	// 生成唯一的订单引用ID
	reference := fmt.Sprintf("creem-api-ref-%d-%d-%s", user.Id, time.Now().UnixMilli(), randstr.String(4))
	referenceId := "ref_" + common.Sha1([]byte(reference))

	// 先创建订单记录，使用产品配置的金额和充值额度
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          selectedProduct.Quota, // 充值额度
		Money:           selectedProduct.Price, // 支付金额
		TradeNo:         referenceId,
		PaymentMethod:   model.PaymentMethodCreem,
		PaymentProvider: model.PaymentProviderCreem,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	err = topUp.Insert()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 创建充值订单失败 user_id=%d trade_no=%s product_id=%s error=%q", id, referenceId, selectedProduct.ProductId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	// 创建支付链接，传入用户邮箱
	checkoutUrl, err := genCreemLink(c.Request.Context(), referenceId, selectedProduct, user.Email, user.Username)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 创建支付链接失败 user_id=%d trade_no=%s product_id=%s error=%q", id, referenceId, selectedProduct.ProductId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 充值订单创建成功 user_id=%d trade_no=%s product_id=%s product_name=%q quota=%d money=%.2f", id, referenceId, selectedProduct.ProductId, selectedProduct.Name, selectedProduct.Quota, selectedProduct.Price))

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"checkout_url": checkoutUrl,
			"order_id":     referenceId,
		},
	})
}

func RequestCreemPay(c *gin.Context) {
	var req CreemPayRequest

	// 读取body内容用于打印，同时保留原始数据供后续使用
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 支付请求读取失败 error=%q", err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "read query error"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 支付请求已收到 user_id=%d body=%q", c.GetInt("id"), string(bodyBytes)))

	// 重新设置body供后续的ShouldBindJSON使用
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	err = c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	creemAdaptor.RequestPay(c, &req)
}

// 新的Creem Webhook结构体，匹配实际的webhook数据格式
type CreemWebhookEvent struct {
	Id        string `json:"id"`
	EventType string `json:"eventType"`
	CreatedAt int64  `json:"created_at"`
	Object    struct {
		Id        string `json:"id"`
		Object    string `json:"object"`
		RequestId string `json:"request_id"`
		Order     struct {
			Object      string `json:"object"`
			Id          string `json:"id"`
			Customer    string `json:"customer"`
			Product     string `json:"product"`
			Amount      int    `json:"amount"`
			Currency    string `json:"currency"`
			SubTotal    int    `json:"sub_total"`
			TaxAmount   int    `json:"tax_amount"`
			AmountDue   int    `json:"amount_due"`
			AmountPaid  int    `json:"amount_paid"`
			Status      string `json:"status"`
			Type        string `json:"type"`
			Transaction string `json:"transaction"`
			CreatedAt   string `json:"created_at"`
			UpdatedAt   string `json:"updated_at"`
			Mode        string `json:"mode"`
		} `json:"order"`
		Product struct {
			Id                string  `json:"id"`
			Object            string  `json:"object"`
			Name              string  `json:"name"`
			Description       string  `json:"description"`
			Price             int     `json:"price"`
			Currency          string  `json:"currency"`
			BillingType       string  `json:"billing_type"`
			BillingPeriod     string  `json:"billing_period"`
			Status            string  `json:"status"`
			TaxMode           string  `json:"tax_mode"`
			TaxCategory       string  `json:"tax_category"`
			DefaultSuccessUrl *string `json:"default_success_url"`
			CreatedAt         string  `json:"created_at"`
			UpdatedAt         string  `json:"updated_at"`
			Mode              string  `json:"mode"`
		} `json:"product"`
		Units    int `json:"units"`
		Customer struct {
			Id        string `json:"id"`
			Object    string `json:"object"`
			Email     string `json:"email"`
			Name      string `json:"name"`
			Country   string `json:"country"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
			Mode      string `json:"mode"`
		} `json:"customer"`
		Status   string            `json:"status"`
		Metadata map[string]string `json:"metadata"`
		Mode     string            `json:"mode"`
	} `json:"object"`
}

func CreemWebhook(c *gin.Context) {
	if !isCreemWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=rejected reason=webhook_disabled path=%s",
			paymentWebhookLogField(c.Request.URL.Path),
		))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	// 保留原始 body 内容用于签名验证和 JSON 解析。
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=rejected reason=body_read_failed path=%s error_type=%T",
			paymentWebhookLogField(c.Request.URL.Path), err,
		))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// 获取签名头
	signature := c.GetHeader(CreemSignatureHeader)
	logger.LogInfo(c.Request.Context(), fmt.Sprintf(
		"payment_webhook provider=creem phase=received path=%s body_bytes=%d signature_present=%t",
		paymentWebhookLogField(c.Request.URL.Path), len(bodyBytes), signature != "",
	))
	if signature == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=rejected reason=signature_missing path=%s body_bytes=%d signature_present=false",
			paymentWebhookLogField(c.Request.URL.Path), len(bodyBytes),
		))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	// 验证签名
	if !verifyCreemSignature(string(bodyBytes), signature, setting.CreemWebhookSecret) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=rejected reason=signature_invalid path=%s body_bytes=%d signature_present=true",
			paymentWebhookLogField(c.Request.URL.Path), len(bodyBytes),
		))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf(
		"payment_webhook provider=creem phase=verified path=%s body_bytes=%d",
		paymentWebhookLogField(c.Request.URL.Path), len(bodyBytes),
	))

	// 重新设置body供后续的ShouldBindJSON使用
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// 解析新格式的webhook数据
	var webhookEvent CreemWebhookEvent
	if err := c.ShouldBindJSON(&webhookEvent); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=rejected reason=body_parse_failed path=%s body_bytes=%d error_type=%T",
			paymentWebhookLogField(c.Request.URL.Path), len(bodyBytes), err,
		))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf(
		"payment_webhook provider=creem phase=parsed event_type=%s event_id=%s request_id=%s order_id=%s order_status=%s",
		paymentWebhookLogField(webhookEvent.EventType),
		paymentWebhookLogField(webhookEvent.Id),
		paymentWebhookLogField(webhookEvent.Object.RequestId),
		paymentWebhookLogField(webhookEvent.Object.Order.Id),
		paymentWebhookLogField(webhookEvent.Object.Order.Status),
	))

	// 根据事件类型处理不同的webhook
	switch webhookEvent.EventType {
	case "checkout.completed":
		handleCheckoutCompleted(c, &webhookEvent)
	default:
		logger.LogInfo(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=ignored event_type=%s event_id=%s",
			paymentWebhookLogField(webhookEvent.EventType),
			paymentWebhookLogField(webhookEvent.Id),
		))
		c.Status(http.StatusOK)
	}
}

// creemSubscriptionPaidAmount converts the webhook's amount_paid, which Creem
// reports in the currency's minor unit, into major units. AmountPaid is what
// the buyer actually settled, unlike Amount which is the pre-discount total.
// A webhook that names an amount but no currency is treated as not reporting
// a payment: without the currency the amount cannot be compared to the plan
// price, and settlement must not proceed on an unlabeled magnitude.
func creemSubscriptionPaidAmount(event *CreemWebhookEvent) model.SubscriptionPaidAmount {
	if event == nil {
		return model.SubscriptionPaidAmount{}
	}
	currency := strings.ToUpper(strings.TrimSpace(event.Object.Order.Currency))
	if currency == "" {
		return model.SubscriptionPaidAmount{}
	}
	return model.SubscriptionPaidAmount{
		Amount:   decimal.NewFromInt(int64(event.Object.Order.AmountPaid)).Shift(-2),
		Currency: currency,
		Reported: true,
	}
}

// 处理支付完成事件
func handleCheckoutCompleted(c *gin.Context, event *CreemWebhookEvent) {
	// 验证订单状态
	if event.Object.Order.Status != "paid" {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=ignored reason=status_invalid event_type=%s event_id=%s request_id=%s order_id=%s order_status=%s",
			paymentWebhookLogField(event.EventType),
			paymentWebhookLogField(event.Id),
			paymentWebhookLogField(event.Object.RequestId),
			paymentWebhookLogField(event.Object.Order.Id),
			paymentWebhookLogField(event.Object.Order.Status),
		))
		c.Status(http.StatusOK)
		return
	}

	// 获取引用ID（这是我们创建订单时传递的request_id）
	referenceId := event.Object.RequestId
	if referenceId == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=rejected reason=order_not_found event_type=%s event_id=%s request_id=%s order_id=%s",
			paymentWebhookLogField(event.EventType),
			paymentWebhookLogField(event.Id),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(event.Object.Order.Id),
		))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// Try complete subscription order first
	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	if err := model.CompleteSubscriptionOrder(referenceId, common.GetJsonString(event), model.PaymentProviderCreem, "", creemSubscriptionPaidAmount(event)); err == nil {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=settled order_kind=subscription event_type=%s trade_no=%s order_id=%s",
			paymentWebhookLogField(event.EventType),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(event.Object.Order.Id),
		))
		c.Status(http.StatusOK)
		return
	} else if err != nil && !errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=failed reason=settlement_failed order_kind=subscription event_type=%s trade_no=%s order_id=%s error_type=%T",
			paymentWebhookLogField(event.EventType),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(event.Object.Order.Id),
			err,
		))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// 验证订单类型，目前只处理一次性付款（充值）
	if event.Object.Order.Type != "onetime" {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=ignored reason=order_type_unsupported event_type=%s request_id=%s order_id=%s order_type=%s",
			paymentWebhookLogField(event.EventType),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(event.Object.Order.Id),
			paymentWebhookLogField(event.Object.Order.Type),
		))
		c.Status(http.StatusOK)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf(
		"payment_webhook provider=creem phase=processing event_type=%s trade_no=%s order_id=%s order_status=%s order_type=%s amount_paid=%d currency=%s",
		paymentWebhookLogField(event.EventType),
		paymentWebhookLogField(referenceId),
		paymentWebhookLogField(event.Object.Order.Id),
		paymentWebhookLogField(event.Object.Order.Status),
		paymentWebhookLogField(event.Object.Order.Type),
		event.Object.Order.AmountPaid,
		paymentWebhookLogField(event.Object.Order.Currency),
	))

	// 查询本地订单确认存在
	topUp := model.GetTopUpByTradeNo(referenceId)
	if topUp == nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=rejected reason=order_not_found event_type=%s trade_no=%s order_id=%s",
			paymentWebhookLogField(event.EventType),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(event.Object.Order.Id),
		))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	if topUp.Status != common.TopUpStatusPending {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=ignored reason=status_invalid event_type=%s trade_no=%s order_id=%s local_status=%s",
			paymentWebhookLogField(event.EventType),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(event.Object.Order.Id),
			paymentWebhookLogField(topUp.Status),
		))
		c.Status(http.StatusOK) // 已处理过的订单，返回成功避免重复处理
		return
	}

	// 处理充值，传入客户邮箱和姓名信息
	customerEmail := event.Object.Customer.Email
	customerName := event.Object.Customer.Name

	// 防护性检查，确保邮箱和姓名不为空字符串
	if customerEmail == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=processing reason=customer_email_missing event_type=%s trade_no=%s order_id=%s",
			paymentWebhookLogField(event.EventType),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(event.Object.Order.Id),
		))
	}
	if customerName == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=processing reason=customer_name_missing event_type=%s trade_no=%s order_id=%s",
			paymentWebhookLogField(event.EventType),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(event.Object.Order.Id),
		))
	}

	err := model.RechargeCreem(referenceId, customerEmail, customerName, c.ClientIP())
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=creem phase=failed reason=settlement_failed order_kind=topup event_type=%s trade_no=%s order_id=%s error_type=%T",
			paymentWebhookLogField(event.EventType),
			paymentWebhookLogField(referenceId),
			paymentWebhookLogField(event.Object.Order.Id),
			err,
		))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf(
		"payment_webhook provider=creem phase=settled order_kind=topup event_type=%s trade_no=%s order_id=%s quota=%d money=%.2f",
		paymentWebhookLogField(event.EventType),
		paymentWebhookLogField(referenceId),
		paymentWebhookLogField(event.Object.Order.Id),
		topUp.Amount,
		topUp.Money,
	))
	c.Status(http.StatusOK)
}

type CreemCheckoutRequest struct {
	ProductId string `json:"product_id"`
	RequestId string `json:"request_id"`
	Customer  struct {
		Email string `json:"email"`
	} `json:"customer"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type CreemCheckoutResponse struct {
	CheckoutUrl string `json:"checkout_url"`
	Id          string `json:"id"`
}

func genCreemLink(ctx context.Context, referenceId string, product *CreemProduct, email string, username string) (string, error) {
	if setting.CreemApiKey == "" {
		return "", fmt.Errorf("未配置Creem API密钥")
	}

	// 根据测试模式选择 API 端点
	apiUrl := "https://api.creem.io/v1/checkouts"
	if setting.CreemTestMode {
		apiUrl = "https://test-api.creem.io/v1/checkouts"
		logger.LogInfo(ctx, fmt.Sprintf("Creem 使用测试环境 api_url=%s", apiUrl))
	}

	// 构建请求数据，确保包含用户邮箱
	requestData := CreemCheckoutRequest{
		ProductId: product.ProductId,
		RequestId: referenceId, // 这个作为订单ID传递给Creem
		Customer: struct {
			Email string `json:"email"`
		}{
			Email: email, // 用户邮箱会在支付页面预填充
		},
		Metadata: map[string]string{
			"username":     username,
			"reference_id": referenceId,
			"product_name": product.Name,
			"quota":        fmt.Sprintf("%d", product.Quota),
		},
	}

	// 序列化请求数据
	jsonData, err := common.Marshal(requestData)
	if err != nil {
		return "", fmt.Errorf("序列化请求数据失败: %v", err)
	}

	// 创建 HTTP 请求
	req, err := http.NewRequest("POST", apiUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建HTTP请求失败: %v", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", setting.CreemApiKey)

	logger.LogInfo(ctx, fmt.Sprintf("Creem 支付请求已发送 api_url=%s product_id=%s email=%q trade_no=%s", apiUrl, product.ProductId, email, referenceId))

	// 发送请求
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("发送HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %v", err)
	}

	logger.LogInfo(ctx, fmt.Sprintf("Creem API 响应已收到 trade_no=%s status_code=%d body=%q", referenceId, resp.StatusCode, string(body)))

	// 检查响应状态
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("Creem API http status %d ", resp.StatusCode)
	}
	// 解析响应
	var checkoutResp CreemCheckoutResponse
	err = common.Unmarshal(body, &checkoutResp)
	if err != nil {
		return "", fmt.Errorf("解析响应失败: %v", err)
	}

	if checkoutResp.CheckoutUrl == "" {
		return "", fmt.Errorf("Creem API resp no checkout url ")
	}

	logger.LogInfo(ctx, fmt.Sprintf("Creem 支付链接创建成功 trade_no=%s response_id=%s checkout_url=%q", referenceId, checkoutResp.Id, checkoutResp.CheckoutUrl))
	return checkoutResp.CheckoutUrl, nil
}
