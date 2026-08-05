package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedeemCreditIsVisibleWithPreexistingUserMetadataCache(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Exec("DELETE FROM redemptions").Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DELETE FROM redemptions").Error)
	})
	useUserCacheMiniRedis(t)
	user := User{
		Username:    "redeem-cache",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       10,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))
	assert.False(t, common.RDB.HExists(t.Context(), getUserCacheKey(user.Id), "Quota").Val())
	redemption := Redemption{
		Key:    "quota-cache-credit",
		Status: common.RedemptionCodeStatusEnabled,
		Quota:  90,
	}
	require.NoError(t, DB.Create(&redemption).Error)

	credited, err := Redeem(redemption.Key, user.Id)
	require.NoError(t, err)
	assert.Equal(t, 90, credited)
	quota, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 100, quota)
}

func TestManualCompleteTopUpCreditIsVisibleWithPreexistingUserMetadataCache(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	user := User{
		Username:    "manual-topup-cache",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       10,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))
	assert.False(t, common.RDB.HExists(t.Context(), getUserCacheKey(user.Id), "Quota").Val())
	topUp := TopUp{
		UserId:          user.Id,
		Amount:          2,
		Money:           2,
		TradeNo:         "manual-cache-credit",
		PaymentMethod:   "alipay",
		PaymentProvider: PaymentProviderEpay,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(&topUp).Error)

	require.NoError(t, ManualCompleteTopUp(topUp.TradeNo, "127.0.0.1"))
	quota, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 10+int(2*common.QuotaPerUnit), quota)
}
