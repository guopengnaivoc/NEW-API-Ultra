package model

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useSubscriptionBalancePurchaseTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB, previousLogDB := DB, LOG_DB
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousQuotaPerUnit := common.QuotaPerUnit
	previousRedisEnabled := common.RedisEnabled

	databasePath := filepath.Join(t.TempDir(), "subscription-balance-purchase.db")
	dsn := "file:" + databasePath +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(1000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	var journalMode string
	require.NoError(t, db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error)
	require.Equal(t, "wal", strings.ToLower(journalMode))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)

	DB, LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.QuotaPerUnit = 100
	common.RedisEnabled = false
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		common.QuotaPerUnit = previousQuotaPerUnit
		common.RedisEnabled = previousRedisEnabled
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, db.AutoMigrate(
		&User{},
		&Log{},
		&SubscriptionPlan{},
		&SubscriptionOrder{},
		&UserSubscription{},
	))
	return db
}

func seedSubscriptionBalancePurchase(t *testing.T, userID int, planID int) int {
	t.Helper()

	t.Cleanup(func() {
		InvalidateSubscriptionPlanCache(planID)
	})

	requiredQuota, err := calcSubscriptionBalanceQuota(1)
	require.NoError(t, err)
	require.NoError(t, DB.Create(&User{
		Id:       userID,
		Username: "subscription-balance-buyer",
		Status:   common.UserStatusEnabled,
		Quota:    requiredQuota,
	}).Error)
	plan := &SubscriptionPlan{
		Id:            planID,
		Title:         "Balance plan",
		PriceAmount:   1,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(planID)
	return requiredQuota
}

func TestPurchaseSubscriptionWithBalanceRollsBackWhenDebitAffectsNoRows(t *testing.T) {
	useSubscriptionBalancePurchaseTestDB(t)
	const (
		userID = 6801
		planID = 6802
	)
	requiredQuota := seedSubscriptionBalancePurchase(t, userID, planID)

	require.NoError(t, DB.Exec(`
		CREATE TRIGGER ignore_subscription_balance_debit
		BEFORE UPDATE OF quota ON users
		WHEN OLD.id = 6801 AND NEW.quota < OLD.quota
		BEGIN
			SELECT RAISE(IGNORE);
		END
	`).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DROP TRIGGER IF EXISTS ignore_subscription_balance_debit").Error)
	})

	err := PurchaseSubscriptionWithBalance(userID, planID)

	require.Error(t, err)
	assert.ErrorContains(t, err, "余额不足")

	var storedUser User
	require.NoError(t, DB.First(&storedUser, userID).Error)
	assert.Equal(t, requiredQuota, storedUser.Quota)

	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ?", userID, planID).
		Count(&subscriptionCount).Error)
	assert.Zero(t, subscriptionCount)

	var orderCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).
		Where("user_id = ? AND plan_id = ?", userID, planID).
		Count(&orderCount).Error)
	assert.Zero(t, orderCount)
}

func TestPurchaseSubscriptionWithBalanceDebitsBeforeCreatingPurchaseRecords(t *testing.T) {
	useSubscriptionBalancePurchaseTestDB(t)
	const (
		userID = 6811
		planID = 6812
	)
	seedSubscriptionBalancePurchase(t, userID, planID)

	require.NoError(t, PurchaseSubscriptionWithBalance(userID, planID))

	var storedUser User
	require.NoError(t, DB.First(&storedUser, userID).Error)
	assert.Equal(t, 0, storedUser.Quota)

	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ?", userID, planID).
		Count(&subscriptionCount).Error)
	assert.EqualValues(t, 1, subscriptionCount)

	var order SubscriptionOrder
	require.NoError(t, DB.Where("user_id = ? AND plan_id = ?", userID, planID).
		First(&order).Error)
	assert.Equal(t, PaymentMethodBalance, order.PaymentMethod)
	assert.Equal(t, PaymentProviderBalance, order.PaymentProvider)
	assert.Equal(t, common.TopUpStatusSuccess, order.Status)
	assert.Equal(t, "charged_quota=100", order.ProviderPayload)
}

func TestPurchaseSubscriptionWithBalanceConcurrentSQLitePurchaseCannotOverspend(t *testing.T) {
	useSubscriptionBalancePurchaseTestDB(t)
	const (
		userID = 6821
		planID = 6822
	)
	seedSubscriptionBalancePurchase(t, userID, planID)

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			results <- PurchaseSubscriptionWithBalance(userID, planID)
		}()
	}
	ready.Wait()
	close(start)

	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	assert.Equal(t, 1, successes)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, userID).Error)
	assert.Equal(t, 0, storedUser.Quota)

	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ?", userID, planID).
		Count(&subscriptionCount).Error)
	assert.EqualValues(t, 1, subscriptionCount)

	var orderCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).
		Where("user_id = ? AND plan_id = ?", userID, planID).
		Count(&orderCount).Error)
	assert.EqualValues(t, 1, orderCount)
}
