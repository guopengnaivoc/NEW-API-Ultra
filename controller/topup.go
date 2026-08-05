package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

func GetTopUpInfo(c *gin.Context) {
	complianceConfirmed := operation_setting.IsPaymentComplianceConfirmed()

	// 获取支付方式
	payMethods := operation_setting.PayMethods
	if !complianceConfirmed {
		payMethods = []map[string]string{}
	}

	// 如果启用了 Stripe 支付，添加到支付方法列表
	if isStripeTopUpEnabled() {
		// 检查是否已经包含 Stripe
		hasStripe := false
		for _, method := range payMethods {
			if method["type"] == "stripe" {
				hasStripe = true
				break
			}
		}

		if !hasStripe {
			stripeMethod := map[string]string{
				"name":      "Stripe",
				"type":      "stripe",
				"color":     "#635BFF",
				"min_topup": strconv.Itoa(setting.StripeMinTopUp),
			}
			payMethods = append(payMethods, stripeMethod)
		}
	}

	// Waffo Pancake is displayed above the standard Waffo gateway.
	enableWaffoPancake := isWaffoPancakeTopUpEnabled()
	if enableWaffoPancake {
		hasWaffoPancake := false
		for _, method := range payMethods {
			if method["type"] == model.PaymentMethodWaffoPancake {
				hasWaffoPancake = true
				break
			}
		}

		if !hasWaffoPancake {
			payMethods = append(payMethods, map[string]string{
				"name":      "Waffo Pancake",
				"type":      model.PaymentMethodWaffoPancake,
				"color":     "#F97316",
				"min_topup": strconv.Itoa(setting.WaffoPancakeMinTopUp),
			})
		}
	}

	// 如果启用了 Waffo 支付，添加到支付方法列表
	enableWaffo := isWaffoTopUpEnabled()
	if enableWaffo {
		hasWaffo := false
		for _, method := range payMethods {
			if method["type"] == model.PaymentMethodWaffo {
				hasWaffo = true
				break
			}
		}

		if !hasWaffo {
			waffoMethod := map[string]string{
				"name":      "Waffo (Global Payment)",
				"type":      model.PaymentMethodWaffo,
				"color":     "#3B82F6",
				"min_topup": strconv.Itoa(setting.WaffoMinTopUp),
			}
			payMethods = append(payMethods, waffoMethod)
		}
	}

	data := gin.H{
		"enable_online_topup":              isEpayTopUpEnabled(),
		"enable_stripe_topup":              isStripeTopUpEnabled(),
		"enable_creem_topup":               isCreemTopUpEnabled(),
		"enable_waffo_topup":               enableWaffo,
		"enable_waffo_pancake_topup":       enableWaffoPancake,
		"enable_redemption":                complianceConfirmed,
		"payment_compliance_confirmed":     complianceConfirmed,
		"payment_compliance_terms_version": operation_setting.CurrentComplianceTermsVersion,
		"waffo_pay_methods": func() interface{} {
			if enableWaffo {
				return setting.GetWaffoPayMethods()
			}
			return nil
		}(),
		"creem_products":          setting.CreemProducts,
		"pay_methods":             payMethods,
		"min_topup":               operation_setting.MinTopUp,
		"stripe_min_topup":        setting.StripeMinTopUp,
		"waffo_min_topup":         setting.WaffoMinTopUp,
		"waffo_pancake_min_topup": setting.WaffoPancakeMinTopUp,
		"amount_options":          operation_setting.GetPaymentSetting().AmountOptions,
		"discount":                operation_setting.GetPaymentSetting().AmountDiscount,
		"topup_link":              common.TopUpLink,
	}
	common.ApiSuccess(c, data)
}

type EpayRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
}

type AmountRequest struct {
	Amount int64 `json:"amount"`
}

func GetEpayClient() *epay.Client {
	if operation_setting.PayAddress == "" || operation_setting.EpayId == "" || operation_setting.EpayKey == "" {
		return nil
	}
	withUrl, err := epay.NewClient(&epay.Config{
		PartnerID: operation_setting.EpayId,
		Key:       operation_setting.EpayKey,
	}, operation_setting.PayAddress)
	if err != nil {
		return nil
	}
	return withUrl
}

func getPayMoney(amount int64, group string) float64 {
	dAmount := decimal.NewFromInt(amount)
	// 充值金额以“展示类型”为准：
	// - USD/CNY: 前端传 amount 为金额单位；TOKENS: 前端传 tokens，需要换成 USD 金额
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		dAmount = dAmount.Div(dQuotaPerUnit)
	}

	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}

	dTopupGroupRatio := decimal.NewFromFloat(topupGroupRatio)
	dPrice := decimal.NewFromFloat(operation_setting.Price)
	// apply optional preset discount by the original request amount (if configured), default 1.0
	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(amount)]; ok {
		if ds > 0 {
			discount = ds
		}
	}
	dDiscount := decimal.NewFromFloat(discount)

	payMoney := dAmount.Mul(dPrice).Mul(dTopupGroupRatio).Mul(dDiscount)

	return payMoney.InexactFloat64()
}

func getMinTopup() (int64, error) {
	minTopup := operation_setting.MinTopUp
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		if common.QuotaPerUnit <= 0 {
			return 0, fmt.Errorf("invalid quota unit")
		}
		dMinTopup := decimal.NewFromInt(int64(minTopup))
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		converted, err := common.QuotaFromDecimalStrict(
			dMinTopup.Mul(dQuotaPerUnit).Truncate(0),
		)
		if err != nil {
			return 0, err
		}
		minTopup = converted
	}
	return int64(minTopup), nil
}

func normalizeEpayTopUpAmount(amount int64) (int64, error) {
	if common.QuotaPerUnit <= 0 {
		return 0, fmt.Errorf("invalid quota unit")
	}

	normalizedAmount := amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		if _, err := common.QuotaFromDecimalStrict(decimal.NewFromInt(amount)); err != nil {
			return 0, err
		}
		normalized, err := common.QuotaFromDecimalStrict(
			decimal.NewFromInt(amount).
				Div(decimal.NewFromFloat(common.QuotaPerUnit)).
				Truncate(0),
		)
		if err != nil {
			return 0, err
		}
		if normalized < 1 {
			return 0, fmt.Errorf("invalid top-up amount")
		}
		normalizedAmount = int64(normalized)
	}

	quota, err := common.QuotaFromDecimalStrict(
		decimal.NewFromInt(normalizedAmount).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
			Truncate(0),
	)
	if err != nil {
		return 0, err
	}
	if quota <= 0 {
		return 0, fmt.Errorf("invalid top-up quota")
	}
	return normalizedAmount, nil
}

func RequestEpay(c *gin.Context) {
	var req EpayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	minTopup, err := getMinTopup()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值配置错误"})
		return
	}
	if req.Amount < minTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", minTopup)})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	if !operation_setting.ContainsPayMethod(req.PaymentMethod) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付方式不存在"})
		return
	}

	amount, err := normalizeEpayTopUpAmount(req.Amount)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值数量无效"})
		return
	}

	callBackAddress := service.GetCallbackAddress()
	returnUrl, _ := url.Parse(paymentReturnPath("/usage-logs"))
	notifyUrl, _ := url.Parse(callBackAddress + "/api/user/epay/notify")
	tradeNo := fmt.Sprintf("%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("USR%dNO%s", id, tradeNo)
	client := GetEpayClient()
	if client == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "当前管理员未配置支付信息"})
		return
	}
	uri, params, err := client.Purchase(&epay.PurchaseArgs{
		Type:           req.PaymentMethod,
		ServiceTradeNo: tradeNo,
		Name:           fmt.Sprintf("TUC%d", req.Amount),
		Money:          strconv.FormatFloat(payMoney, 'f', 2, 64),
		Device:         epay.PC,
		NotifyUrl:      notifyUrl,
		ReturnUrl:      returnUrl,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 拉起支付失败 user_id=%d trade_no=%s payment_method=%s amount=%d error=%q", id, tradeNo, req.PaymentMethod, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   req.PaymentMethod,
		PaymentProvider: model.PaymentProviderEpay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	err = topUp.Insert()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 创建充值订单失败 user_id=%d trade_no=%s payment_method=%s amount=%d error=%q", id, tradeNo, req.PaymentMethod, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 充值订单创建成功 user_id=%d trade_no=%s payment_method=%s amount=%d money=%.2f uri=%q params=%q", id, tradeNo, req.PaymentMethod, req.Amount, payMoney, uri, common.GetJsonString(params)))
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": params, "url": uri})
}

func completeEpayTopUp(topUp *model.TopUp, paymentType, callerIP string) error {
	if topUp == nil {
		return model.ErrTopUpNotFound
	}
	if topUp.PaymentProvider != model.PaymentProviderEpay {
		return model.ErrPaymentMethodMismatch
	}
	if topUp.Status != common.TopUpStatusPending {
		return model.ErrTopUpStatusInvalid
	}

	quotaToAdd, err := common.QuotaFromDecimalStrict(
		decimal.NewFromInt(topUp.Amount).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
			Truncate(0),
	)
	if err != nil {
		return err
	}
	if quotaToAdd <= 0 {
		return fmt.Errorf("invalid top-up quota")
	}

	topUp.PaymentMethod = paymentType
	topUp.Status = common.TopUpStatusSuccess
	if err := topUp.Update(); err != nil {
		return err
	}
	if err := model.IncreaseUserQuota(topUp.UserId, quotaToAdd, true); err != nil {
		return err
	}

	model.RecordTopupLog(
		topUp.UserId,
		fmt.Sprintf(
			"使用在线充值成功，充值金额: %v，支付金额：%f",
			logger.LogQuota(quotaToAdd),
			topUp.Money,
		),
		callerIP,
		topUp.PaymentMethod,
		"epay",
	)
	return nil
}

// tradeNo lock
var orderLocks sync.Map
var createLock sync.Mutex

// refCountedMutex 带引用计数的互斥锁，确保最后一个使用者才从 map 中删除
type refCountedMutex struct {
	mu       sync.Mutex
	refCount int
}

// LockOrder 尝试对给定订单号加锁
func LockOrder(tradeNo string) {
	createLock.Lock()
	var rcm *refCountedMutex
	if v, ok := orderLocks.Load(tradeNo); ok {
		rcm = v.(*refCountedMutex)
	} else {
		rcm = &refCountedMutex{}
		orderLocks.Store(tradeNo, rcm)
	}
	rcm.refCount++
	createLock.Unlock()
	rcm.mu.Lock()
}

// UnlockOrder 释放给定订单号的锁
func UnlockOrder(tradeNo string) {
	v, ok := orderLocks.Load(tradeNo)
	if !ok {
		return
	}
	rcm := v.(*refCountedMutex)
	rcm.mu.Unlock()

	createLock.Lock()
	rcm.refCount--
	if rcm.refCount == 0 {
		orderLocks.Delete(tradeNo)
	}
	createLock.Unlock()
}

func EpayNotify(c *gin.Context) {
	if !isEpayWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=epay phase=rejected reason=webhook_disabled path=%s",
			paymentWebhookLogField(c.Request.URL.Path),
		))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	var params map[string]string

	if c.Request.Method == "POST" {
		// POST 请求：从 POST body 解析参数
		if err := c.Request.ParseForm(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf(
				"payment_webhook provider=epay phase=rejected reason=form_parse_failed path=%s method=%s error_type=%T",
				paymentWebhookLogField(c.Request.URL.Path),
				paymentWebhookLogField(c.Request.Method),
				err,
			))
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		params = lo.Reduce(lo.Keys(c.Request.PostForm), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.PostForm.Get(t)
			return r
		}, map[string]string{})
	} else {
		// GET 请求：从 URL Query 解析参数
		params = lo.Reduce(lo.Keys(c.Request.URL.Query()), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.URL.Query().Get(t)
			return r
		}, map[string]string{})
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf(
		"payment_webhook provider=epay phase=received path=%s method=%s parameter_count=%d signature_present=%t",
		paymentWebhookLogField(c.Request.URL.Path),
		paymentWebhookLogField(c.Request.Method),
		len(params),
		params["sign"] != "",
	))

	if len(params) == 0 {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=epay phase=rejected reason=parameters_empty path=%s method=%s",
			paymentWebhookLogField(c.Request.URL.Path),
			paymentWebhookLogField(c.Request.Method),
		))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}
	client := GetEpayClient()
	if client == nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=epay phase=rejected reason=client_init_failed path=%s method=%s",
			paymentWebhookLogField(c.Request.URL.Path),
			paymentWebhookLogField(c.Request.Method),
		))
		_, writeErr := c.Writer.Write([]byte("fail"))
		if writeErr != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf(
				"payment_webhook provider=epay phase=response_write_failed reason=write_failed error_type=%T",
				writeErr,
			))
		}
		return
	}
	verifyInfo, verifyErr := client.Verify(params)
	if verifyErr != nil || !verifyInfo.VerifyStatus {
		_, writeErr := c.Writer.Write([]byte("fail"))
		if writeErr != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf(
				"payment_webhook provider=epay phase=response_write_failed reason=write_failed error_type=%T",
				writeErr,
			))
		}
		reason := "signature_invalid"
		if verifyErr != nil {
			reason = "verification_error"
		}
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=epay phase=rejected reason=%s method=%s parameter_count=%d signature_present=%t",
			reason,
			paymentWebhookLogField(c.Request.Method),
			len(params),
			params["sign"] != "",
		))
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf(
		"payment_webhook provider=epay phase=verified trade_no=%s callback_type=%s trade_status=%s",
		paymentWebhookLogField(verifyInfo.ServiceTradeNo),
		paymentWebhookLogField(verifyInfo.Type),
		paymentWebhookLogField(verifyInfo.TradeStatus),
	))
	_, writeErr := c.Writer.Write([]byte("success"))
	if writeErr != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=epay phase=response_write_failed reason=write_failed trade_no=%s error_type=%T",
			paymentWebhookLogField(verifyInfo.ServiceTradeNo),
			writeErr,
		))
	}

	if verifyInfo.TradeStatus == epay.StatusTradeSuccess {
		LockOrder(verifyInfo.ServiceTradeNo)
		defer UnlockOrder(verifyInfo.ServiceTradeNo)
		topUp := model.GetTopUpByTradeNo(verifyInfo.ServiceTradeNo)
		if topUp == nil {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf(
				"payment_webhook provider=epay phase=rejected reason=order_not_found trade_no=%s callback_type=%s",
				paymentWebhookLogField(verifyInfo.ServiceTradeNo),
				paymentWebhookLogField(verifyInfo.Type),
			))
			return
		}
		if topUp.PaymentProvider != model.PaymentProviderEpay {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf(
				"payment_webhook provider=epay phase=rejected reason=provider_mismatch trade_no=%s order_provider=%s callback_type=%s",
				paymentWebhookLogField(verifyInfo.ServiceTradeNo),
				paymentWebhookLogField(topUp.PaymentProvider),
				paymentWebhookLogField(verifyInfo.Type),
			))
			return
		}
		if topUp.Status == common.TopUpStatusPending {
			if topUp.PaymentMethod != verifyInfo.Type {
				logger.LogInfo(c.Request.Context(), fmt.Sprintf(
					"payment_webhook provider=epay phase=processing reason=payment_method_changed trade_no=%s order_payment_method=%s callback_type=%s",
					paymentWebhookLogField(verifyInfo.ServiceTradeNo),
					paymentWebhookLogField(topUp.PaymentMethod),
					paymentWebhookLogField(verifyInfo.Type),
				))
			}
			if err := completeEpayTopUp(topUp, verifyInfo.Type, c.ClientIP()); err != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf(
					"payment_webhook provider=epay phase=failed reason=settlement_failed trade_no=%s user_id=%d error_type=%T",
					paymentWebhookLogField(topUp.TradeNo),
					topUp.UserId,
					err,
				))
				return
			}
			logger.LogInfo(c.Request.Context(), fmt.Sprintf(
				"payment_webhook provider=epay phase=completed trade_no=%s user_id=%d money=%.2f status=%s",
				paymentWebhookLogField(topUp.TradeNo),
				topUp.UserId,
				topUp.Money,
				paymentWebhookLogField(topUp.Status),
			))
		}
	} else {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf(
			"payment_webhook provider=epay phase=ignored reason=status_ignored trade_no=%s callback_type=%s trade_status=%s",
			paymentWebhookLogField(verifyInfo.ServiceTradeNo),
			paymentWebhookLogField(verifyInfo.Type),
			paymentWebhookLogField(verifyInfo.TradeStatus),
		))
	}
}

func RequestAmount(c *gin.Context) {
	var req AmountRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	minTopup, err := getMinTopup()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值配置错误"})
		return
	}
	if req.Amount < minTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", minTopup)})
		return
	}
	if _, err := normalizeEpayTopUpAmount(req.Amount); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值数量无效"})
		return
	}
	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(req.Amount, group)
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

func GetUserTopUps(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	keyword := c.Query("keyword")

	var (
		topups []*model.TopUp
		total  int64
		err    error
	)
	if keyword != "" {
		topups, total, err = model.SearchUserTopUps(userId, keyword, pageInfo)
	} else {
		topups, total, err = model.GetUserTopUps(userId, pageInfo)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(topups)
	common.ApiSuccess(c, pageInfo)
}

// GetAllTopUps 管理员获取全平台充值记录
func GetAllTopUps(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	keyword := c.Query("keyword")

	var (
		topups []*model.TopUp
		total  int64
		err    error
	)
	if keyword != "" {
		topups, total, err = model.SearchAllTopUps(keyword, pageInfo)
	} else {
		topups, total, err = model.GetAllTopUps(pageInfo)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(topups)
	common.ApiSuccess(c, pageInfo)
}

type AdminCompleteTopupRequest struct {
	TradeNo string `json:"trade_no"`
}

// AdminCompleteTopUp 管理员补单接口
func AdminCompleteTopUp(c *gin.Context) {
	var req AdminCompleteTopupRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TradeNo == "" {
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	// 订单级互斥，防止并发补单
	LockOrder(req.TradeNo)
	defer UnlockOrder(req.TradeNo)

	if err := model.ManualCompleteTopUp(req.TradeNo, c.ClientIP()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
