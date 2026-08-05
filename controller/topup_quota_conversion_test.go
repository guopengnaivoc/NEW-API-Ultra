package controller

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func restoreTopUpConversionSettings(t *testing.T) {
	t.Helper()
	originalGeneral, err := config.ConfigToMap(
		operation_setting.GetGeneralSetting(),
	)
	require.NoError(t, err)
	originalMinTopUp := operation_setting.MinTopUp
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update(
			"general_setting",
			originalGeneral,
		)
		require.NoError(t, updateErr)
		require.True(t, updated)
		operation_setting.MinTopUp = originalMinTopUp
		common.QuotaPerUnit = originalQuotaPerUnit
	})
}

func setTopUpQuotaDisplayType(t *testing.T, displayType string) {
	t.Helper()
	updated, err := config.GlobalConfig.Update(
		"general_setting",
		map[string]string{"quota_display_type": displayType},
	)
	require.NoError(t, err)
	require.True(t, updated)
}

func TestGetMinTopupRejectsTokenDisplayOverflow(t *testing.T) {
	restoreTopUpConversionSettings(t)
	common.QuotaPerUnit = 500000
	operation_setting.MinTopUp = 5000
	setTopUpQuotaDisplayType(t, operation_setting.QuotaDisplayTypeTokens)

	minimum, err := getMinTopup()
	assert.Zero(t, minimum)
	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	assert.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}

func TestNormalizeWaffoPancakeTopUpAmountPreservesTruncation(t *testing.T) {
	restoreTopUpConversionSettings(t)
	common.QuotaPerUnit = 500000
	setTopUpQuotaDisplayType(t, operation_setting.QuotaDisplayTypeTokens)

	normalized, err := normalizeWaffoPancakeTopUpAmount(1_250_000)
	require.NoError(t, err)
	assert.Equal(t, int64(2), normalized)

	normalized, err = normalizeWaffoPancakeTopUpAmount(
		int64(common.MaxQuota) + 1,
	)
	assert.Zero(t, normalized)
	require.Error(t, err)
}

func TestPaymentProviderRequestValidationRejectsOverflow(t *testing.T) {
	restoreTopUpConversionSettings(t)
	common.QuotaPerUnit = 500000
	setTopUpQuotaDisplayType(t, operation_setting.QuotaDisplayTypeUSD)

	overflowAmount := int64(common.MaxQuota)/500000 + 1

	_, err := normalizeEpayTopUpAmount(overflowAmount)
	require.Error(t, err)
	_, err = normalizeWaffoTopUpAmount(overflowAmount)
	require.Error(t, err)
	_, err = normalizeWaffoPancakeTopUpAmount(overflowAmount)
	require.Error(t, err)
	require.Error(t, validateStripeTopUpQuota(float64(overflowAmount)))
	_, err = validateCreemProductQuota(int64(common.MaxQuota) + 1)
	require.Error(t, err)
}

func TestEpayTopUpQuotaOverflowLeavesOrderPendingAndBalanceUnchanged(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))

	restoreTopUpConversionSettings(t)
	common.QuotaPerUnit = 500000
	user := &model.User{
		Username: "epay-quota-overflow",
		Status:   common.UserStatusEnabled,
		Quota:    10,
	}
	require.NoError(t, db.Create(user).Error)
	topUp := &model.TopUp{
		UserId:          user.Id,
		Amount:          int64(common.MaxQuota)/500000 + 1,
		Money:           1,
		TradeNo:         "epay-quota-overflow",
		PaymentMethod:   "alipay",
		PaymentProvider: model.PaymentProviderEpay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, db.Create(topUp).Error)

	require.Error(t, completeEpayTopUp(topUp, "alipay", "127.0.0.1"))

	var reloadedTopUp model.TopUp
	require.NoError(t, db.Where("trade_no = ?", topUp.TradeNo).First(&reloadedTopUp).Error)
	assert.Equal(t, common.TopUpStatusPending, reloadedTopUp.Status)
	var reloadedUser model.User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 10, reloadedUser.Quota)
}
