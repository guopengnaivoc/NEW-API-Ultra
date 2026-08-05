package model

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useSubscriptionRefundTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := DB
	previousDatabaseType := common.MainDatabaseType()
	databasePath := filepath.Join(t.TempDir(), "subscription-refund.db")
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

	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousDatabaseType)
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, db.AutoMigrate(
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
	))
	return db
}

func seedSubscriptionRefundTest(
	t *testing.T,
	db *gorm.DB,
	requestID string,
) (UserSubscription, SubscriptionPreConsumeRecord) {
	t.Helper()

	subscription := UserSubscription{
		UserId:      4101,
		PlanId:      5101,
		AmountTotal: 1000,
		AmountUsed:  100,
		StartTime:   1,
		EndTime:     4102444800,
		Status:      "active",
		Source:      "order",
	}
	require.NoError(t, db.Create(&subscription).Error)

	record := SubscriptionPreConsumeRecord{
		RequestId:          requestID,
		UserId:             subscription.UserId,
		UserSubscriptionId: subscription.Id,
		PreConsumed:        40,
		Status:             "consumed",
	}
	require.NoError(t, db.Create(&record).Error)
	return subscription, record
}

func loadSubscriptionRefundState(
	t *testing.T,
	db *gorm.DB,
	subscriptionID int,
	recordID int,
) (UserSubscription, SubscriptionPreConsumeRecord) {
	t.Helper()

	var subscription UserSubscription
	require.NoError(t, db.First(&subscription, subscriptionID).Error)
	var record SubscriptionPreConsumeRecord
	require.NoError(t, db.First(&record, recordID).Error)
	return subscription, record
}

func TestRefundSubscriptionPreConsumeIsAtomicAndIdempotent(t *testing.T) {
	db := useSubscriptionRefundTestDB(t)
	subscription, record := seedSubscriptionRefundTest(t, db, "refund-idempotent")

	require.NoError(t, RefundSubscriptionPreConsume(record.RequestId))
	storedSubscription, storedRecord := loadSubscriptionRefundState(
		t,
		db,
		subscription.Id,
		record.Id,
	)
	assert.EqualValues(t, 60, storedSubscription.AmountUsed)
	assert.Equal(t, "refunded", storedRecord.Status)

	require.NoError(t, RefundSubscriptionPreConsume(record.RequestId))
	storedSubscription, storedRecord = loadSubscriptionRefundState(
		t,
		db,
		subscription.Id,
		record.Id,
	)
	assert.EqualValues(t, 60, storedSubscription.AmountUsed)
	assert.Equal(t, "refunded", storedRecord.Status)
}

func TestRefundSubscriptionPreConsumeUsesSingleTransaction(t *testing.T) {
	db := useSubscriptionRefundTestDB(t)
	_, record := seedSubscriptionRefundTest(t, db, "refund-single-transaction")

	var subscriptionConnPool gorm.ConnPool
	var recordConnPool gorm.ConnPool
	callbackName := "test:capture_subscription_refund_transaction"
	require.NoError(t, db.Callback().Update().
		Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Schema == nil {
				return
			}
			switch tx.Statement.Schema.Table {
			case "user_subscriptions":
				subscriptionConnPool = tx.Statement.ConnPool
			case "subscription_pre_consume_records":
				recordConnPool = tx.Statement.ConnPool
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Update().Remove(callbackName))
	})

	require.NoError(t, RefundSubscriptionPreConsume(record.RequestId))
	require.NotNil(t, subscriptionConnPool)
	require.NotNil(t, recordConnPool)
	assert.Same(t, subscriptionConnPool, recordConnPool)
}

func TestRefundSubscriptionPreConsumeRollsBackQuotaWhenStatusWriteFails(t *testing.T) {
	db := useSubscriptionRefundTestDB(t)
	subscription, record := seedSubscriptionRefundTest(t, db, "refund-rollback")
	require.NoError(t, db.Exec(`
		CREATE TRIGGER fail_subscription_refund_status
		BEFORE UPDATE OF status ON subscription_pre_consume_records
		WHEN NEW.status = 'refunded'
		BEGIN
			SELECT RAISE(ABORT, 'forced refund status failure');
		END
	`).Error)

	for attempt := 0; attempt < 2; attempt++ {
		err := RefundSubscriptionPreConsume(record.RequestId)
		require.Error(t, err)
		assert.ErrorContains(t, err, "forced refund status failure")
	}

	storedSubscription, storedRecord := loadSubscriptionRefundState(
		t,
		db,
		subscription.Id,
		record.Id,
	)
	assert.EqualValues(t, 100, storedSubscription.AmountUsed)
	assert.Equal(t, "consumed", storedRecord.Status)
}
