package model

import (
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useSubscriptionUsageTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := useSubscriptionRefundTestDB(t)
	require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}))
	return db
}

func seedSubscriptionUsageTest(
	t *testing.T,
	db *gorm.DB,
	planID int,
	subscriptionID int,
	userID int,
	amountUsed int64,
) UserSubscription {
	t.Helper()

	plan := SubscriptionPlan{
		Id:            planID,
		Title:         "Usage plan",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   100,
	}
	require.NoError(t, db.Create(&plan).Error)
	InvalidateSubscriptionPlanCache(planID)
	t.Cleanup(func() {
		InvalidateSubscriptionPlanCache(planID)
	})

	now := common.GetTimestamp()
	subscription := UserSubscription{
		Id:            subscriptionID,
		UserId:        userID,
		PlanId:        planID,
		AmountTotal:   100,
		AmountUsed:    amountUsed,
		StartTime:     now - 3600,
		EndTime:       now + 86400,
		Status:        "active",
		Source:        "order",
		NextResetTime: now + 3600,
	}
	require.NoError(t, db.Create(&subscription).Error)
	return subscription
}

func loadSubscriptionUsageState(
	t *testing.T,
	db *gorm.DB,
	subscriptionID int,
) UserSubscription {
	t.Helper()

	var subscription UserSubscription
	require.NoError(t, db.First(&subscription, subscriptionID).Error)
	return subscription
}

func injectUserSubscriptionScanDrift(
	t *testing.T,
	db *gorm.DB,
	subscriptionID int,
	changes map[string]interface{},
) func() {
	t.Helper()

	injected := false
	var injectionErr error
	const callbackName = "test:inject_user_subscription_scan_drift"
	require.NoError(t, db.Callback().Query().
		After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if injected {
				return
			}
			subscriptions, ok := tx.Statement.Dest.(*[]UserSubscription)
			if !ok || len(*subscriptions) == 0 {
				return
			}
			injected = true
			injectionErr = tx.Session(&gorm.Session{NewDB: true}).
				Model(&UserSubscription{}).
				Where("id = ?", subscriptionID).
				Updates(changes).Error
		}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(callbackName))
	})

	return func() {
		t.Helper()
		require.True(t, injected)
		require.NoError(t, injectionErr)
	}
}

func TestPreConsumeUserSubscriptionRollsBackRecordWhenUsageClaimAffectsNoRows(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	subscription := seedSubscriptionUsageTest(t, db, 6901, 6902, 6903, 0)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER ignore_subscription_usage_claim
		BEFORE UPDATE OF amount_used ON user_subscriptions
		WHEN OLD.id = 6902 AND NEW.amount_used > OLD.amount_used
		BEGIN
			SELECT RAISE(IGNORE);
		END
	`).Error)

	result, err := PreConsumeUserSubscription(
		"usage-zero-row",
		subscription.UserId,
		"test-model",
		0,
		40,
	)

	require.Error(t, err)
	assert.Nil(t, result)
	stored := loadSubscriptionUsageState(t, db, subscription.Id)
	assert.Zero(t, stored.AmountUsed)

	var recordCount int64
	require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).
		Where("request_id = ?", "usage-zero-row").
		Count(&recordCount).Error)
	assert.Zero(t, recordCount)
}

func TestPreConsumeUserSubscriptionDoesNotOverwriteResetMetadata(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	subscription := seedSubscriptionUsageTest(t, db, 6911, 6912, 6913, 0)
	const concurrentNextReset = int64(4102444800)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER update_reset_during_subscription_pre_consume
		BEFORE UPDATE OF amount_used ON user_subscriptions
		WHEN OLD.id = 6912 AND NEW.amount_used > OLD.amount_used
		BEGIN
			UPDATE user_subscriptions
			SET next_reset_time = 4102444800
			WHERE id = OLD.id;
		END
	`).Error)

	result, err := PreConsumeUserSubscription(
		"usage-preserve-reset",
		subscription.UserId,
		"test-model",
		0,
		40,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	stored := loadSubscriptionUsageState(t, db, subscription.Id)
	assert.EqualValues(t, 40, stored.AmountUsed)
	assert.Equal(t, concurrentNextReset, stored.NextResetTime)
}

func TestPostConsumeUserSubscriptionDeltaDoesNotOverwriteResetMetadata(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	subscription := seedSubscriptionUsageTest(t, db, 6921, 6922, 6923, 10)
	const concurrentNextReset = int64(4102444800)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER update_reset_during_subscription_post_consume
		BEFORE UPDATE OF amount_used ON user_subscriptions
		WHEN OLD.id = 6922 AND NEW.amount_used > OLD.amount_used
		BEGIN
			UPDATE user_subscriptions
			SET next_reset_time = 4102444800
			WHERE id = OLD.id;
		END
	`).Error)

	require.NoError(t, PostConsumeUserSubscriptionDelta(subscription.Id, 25))

	stored := loadSubscriptionUsageState(t, db, subscription.Id)
	assert.EqualValues(t, 35, stored.AmountUsed)
	assert.Equal(t, concurrentNextReset, stored.NextResetTime)
}

func TestMaybeResetUserSubscriptionDoesNotOverwriteUnrelatedFields(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	now := common.GetTimestamp()
	plan := SubscriptionPlan{
		Id:               6931,
		Title:            "Daily reset plan",
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	require.NoError(t, db.Create(&plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() {
		InvalidateSubscriptionPlanCache(plan.Id)
	})
	subscription := UserSubscription{
		Id:            6932,
		UserId:        6933,
		PlanId:        plan.Id,
		AmountTotal:   100,
		AmountUsed:    50,
		StartTime:     now - 3*86400,
		EndTime:       now + 30*86400,
		Status:        "active",
		Source:        "order",
		LastResetTime: now - 2*86400,
		NextResetTime: now - 60,
	}
	require.NoError(t, db.Create(&subscription).Error)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER update_source_during_subscription_reset
		BEFORE UPDATE OF amount_used ON user_subscriptions
		WHEN OLD.id = 6932 AND NEW.amount_used = 0 AND OLD.amount_used > 0
		BEGIN
			UPDATE user_subscriptions
			SET source = 'concurrent-update'
			WHERE id = OLD.id;
		END
	`).Error)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var current UserSubscription
		if err := tx.First(&current, subscription.Id).Error; err != nil {
			return err
		}
		_, err := maybeResetUserSubscriptionWithPlanTx(tx, &current, &plan, now)
		return err
	}))

	stored := loadSubscriptionUsageState(t, db, subscription.Id)
	assert.Zero(t, stored.AmountUsed)
	assert.Equal(t, "concurrent-update", stored.Source)
	assert.Greater(t, stored.NextResetTime, now)
}

func TestPreConsumeUserSubscriptionRejectsUsageOverflow(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	subscription := seedSubscriptionUsageTest(t, db, 6941, 6942, 6943, 1)
	require.NoError(t, db.Model(&UserSubscription{}).
		Where("id = ?", subscription.Id).
		Update("amount_total", 0).Error)

	result, err := PreConsumeUserSubscription(
		"usage-overflow",
		subscription.UserId,
		"test-model",
		0,
		math.MaxInt64,
	)

	require.Error(t, err)
	assert.Nil(t, result)
	stored := loadSubscriptionUsageState(t, db, subscription.Id)
	assert.EqualValues(t, 1, stored.AmountUsed)

	var recordCount int64
	require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).
		Where("request_id = ?", "usage-overflow").
		Count(&recordCount).Error)
	assert.Zero(t, recordCount)
}

func TestPostConsumeUserSubscriptionDeltaRejectsUsageOverflow(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	subscription := seedSubscriptionUsageTest(t, db, 6951, 6952, 6953, 1)
	require.NoError(t, db.Model(&UserSubscription{}).
		Where("id = ?", subscription.Id).
		Update("amount_total", 0).Error)

	err := PostConsumeUserSubscriptionDelta(subscription.Id, math.MaxInt64)

	require.Error(t, err)
	stored := loadSubscriptionUsageState(t, db, subscription.Id)
	assert.EqualValues(t, 1, stored.AmountUsed)
}

func TestAdminResetSubscriptionsPreservesConcurrentFields(t *testing.T) {
	tests := []struct {
		name      string
		resetPlan bool
	}{
		{name: "user plan reset"},
		{name: "whole plan reset", resetPlan: true},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := useSubscriptionUsageTestDB(t)
			planID := 6961 + index*10
			subscriptionID := planID + 1
			userID := planID + 2
			subscription := seedSubscriptionUsageTest(
				t,
				db,
				planID,
				subscriptionID,
				userID,
				50,
			)
			concurrentEndTime := subscription.EndTime + 3600
			require.NoError(t, db.Exec(fmt.Sprintf(`
				CREATE TRIGGER update_fields_during_admin_subscription_reset
				BEFORE UPDATE OF amount_used ON user_subscriptions
				WHEN OLD.id = %d AND NEW.amount_used = 0 AND OLD.amount_used > 0
				BEGIN
					UPDATE user_subscriptions
					SET source = 'concurrent-update',
						status = 'cancelled',
						end_time = %d
					WHERE id = OLD.id;
				END
			`, subscription.Id, concurrentEndTime)).Error)

			var (
				result *SubscriptionResetResult
				err    error
			)
			if test.resetPlan {
				result, err = AdminResetPlanSubscriptions(planID, false)
			} else {
				result, err = AdminResetUserSubscriptionsByPlan(userID, planID, false)
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, 1, result.ResetCount)
			stored := loadSubscriptionUsageState(t, db, subscription.Id)
			assert.Zero(t, stored.AmountUsed)
			assert.Equal(t, "concurrent-update", stored.Source)
			assert.Equal(t, "cancelled", stored.Status)
			assert.Equal(t, concurrentEndTime, stored.EndTime)
		})
	}
}

func TestAdminResetUserSubscriptionsRejectsStaleState(t *testing.T) {
	tests := []struct {
		name   string
		column string
		value  func(UserSubscription) any
	}{
		{
			name:   "usage",
			column: "amount_used",
			value: func(subscription UserSubscription) any {
				return subscription.AmountUsed + 1
			},
		},
		{
			name:   "ownership",
			column: "user_id",
			value: func(subscription UserSubscription) any {
				return subscription.UserId + 1
			},
		},
		{
			name:   "status",
			column: "status",
			value: func(UserSubscription) any {
				return "cancelled"
			},
		},
		{
			name:   "end time",
			column: "end_time",
			value: func(subscription UserSubscription) any {
				return subscription.EndTime + 1
			},
		},
		{
			name:   "plan",
			column: "plan_id",
			value: func(subscription UserSubscription) any {
				return subscription.PlanId + 1
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := useSubscriptionUsageTestDB(t)
			planID := 6981 + index*10
			subscription := seedSubscriptionUsageTest(
				t,
				db,
				planID,
				planID+1,
				planID+2,
				50,
			)

			injected := false
			var injectionErr error
			const callbackName = "test:inject_stale_admin_subscription_reset"
			require.NoError(t, db.Callback().Query().
				After("gorm:query").
				Register(callbackName, func(tx *gorm.DB) {
					if injected {
						return
					}
					subscriptions, ok := tx.Statement.Dest.(*[]UserSubscription)
					if !ok || len(*subscriptions) == 0 {
						return
					}
					injected = true
					injectionErr = tx.Session(&gorm.Session{NewDB: true}).
						Model(&UserSubscription{}).
						Where("id = ?", subscription.Id).
						Update(test.column, test.value(subscription)).Error
				}))
			t.Cleanup(func() {
				require.NoError(t, db.Callback().Query().Remove(callbackName))
			})

			result, err := AdminResetUserSubscriptionsByPlan(
				subscription.UserId,
				planID,
				false,
			)

			require.True(t, injected)
			require.NoError(t, injectionErr)
			require.Error(t, err)
			assert.Nil(t, result)
			stored := loadSubscriptionUsageState(t, db, subscription.Id)
			assert.Equal(t, subscription.UserId, stored.UserId)
			assert.Equal(t, subscription.PlanId, stored.PlanId)
			assert.Equal(t, subscription.AmountUsed, stored.AmountUsed)
			assert.Equal(t, subscription.Status, stored.Status)
			assert.Equal(t, subscription.EndTime, stored.EndTime)
		})
	}
}

func TestAdminResetSubscriptionsSkipsAlreadyZeroUsage(t *testing.T) {
	tests := []struct {
		name             string
		resetPlan        bool
		advanceResetTime bool
	}{
		{name: "user plan reset"},
		{name: "user plan reset with schedule advance", advanceResetTime: true},
		{name: "whole plan reset", resetPlan: true},
		{
			name:             "whole plan reset with schedule advance",
			resetPlan:        true,
			advanceResetTime: true,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := useSubscriptionUsageTestDB(t)
			planID := 7101 + index*10
			subscription := seedSubscriptionUsageTest(
				t,
				db,
				planID,
				planID+1,
				planID+2,
				0,
			)
			require.NoError(t, db.Model(&UserSubscription{}).
				Where("id = ?", subscription.Id).
				Updates(map[string]interface{}{
					"last_reset_time": 0,
					"next_reset_time": 0,
				}).Error)
			require.NoError(t, db.Exec(fmt.Sprintf(`
				CREATE TRIGGER reject_unnecessary_admin_subscription_reset
				BEFORE UPDATE ON user_subscriptions
				WHEN OLD.id = %d
				BEGIN
					SELECT RAISE(FAIL, 'already-zero subscription must not be updated');
				END
			`, subscription.Id)).Error)

			var (
				result *SubscriptionResetResult
				err    error
			)
			if test.resetPlan {
				result, err = AdminResetPlanSubscriptions(
					planID,
					test.advanceResetTime,
				)
			} else {
				result, err = AdminResetUserSubscriptionsByPlan(
					subscription.UserId,
					planID,
					test.advanceResetTime,
				)
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, 1, result.ResetCount)
			assert.Zero(t, loadSubscriptionUsageState(t, db, subscription.Id).AmountUsed)
		})
	}
}

func TestResetDueSubscriptionsRevalidatesEligibilityAfterScan(t *testing.T) {
	tests := []struct {
		name    string
		changes func(now int64) map[string]interface{}
	}{
		{
			name: "cancelled",
			changes: func(now int64) map[string]interface{} {
				return map[string]interface{}{
					"status":   "cancelled",
					"end_time": now,
				}
			},
		},
		{
			name: "expired",
			changes: func(now int64) map[string]interface{} {
				return map[string]interface{}{
					"end_time": now,
				}
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := useSubscriptionUsageTestDB(t)
			planID := 7001 + index*10
			plan := SubscriptionPlan{
				Id:               planID,
				Title:            "Daily reset plan",
				DurationUnit:     SubscriptionDurationMonth,
				DurationValue:    1,
				TotalAmount:      100,
				QuotaResetPeriod: SubscriptionResetDaily,
			}
			require.NoError(t, db.Create(&plan).Error)
			InvalidateSubscriptionPlanCache(plan.Id)
			t.Cleanup(func() {
				InvalidateSubscriptionPlanCache(plan.Id)
			})

			now := common.GetTimestamp()
			subscription := UserSubscription{
				Id:            planID + 1,
				UserId:        planID + 2,
				PlanId:        plan.Id,
				AmountTotal:   100,
				AmountUsed:    50,
				StartTime:     now - 3*86400,
				EndTime:       now + 30*86400,
				Status:        "active",
				Source:        "order",
				LastResetTime: now - 2*86400,
				NextResetTime: now - 60,
			}
			require.NoError(t, db.Create(&subscription).Error)
			verifyInjection := injectUserSubscriptionScanDrift(
				t,
				db,
				subscription.Id,
				test.changes(now),
			)

			resetCount, err := ResetDueSubscriptions(10)

			verifyInjection()
			require.NoError(t, err)
			assert.Zero(t, resetCount)
			stored := loadSubscriptionUsageState(t, db, subscription.Id)
			assert.Equal(t, subscription.AmountUsed, stored.AmountUsed)
			if test.name == "cancelled" {
				assert.Equal(t, "cancelled", stored.Status)
			}
			assert.LessOrEqual(t, stored.EndTime, now)
		})
	}
}

func TestResetDueSubscriptionsRevalidatesCurrentPlanAfterScan(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	dailyPlan := SubscriptionPlan{
		Id:               7021,
		Title:            "Daily reset plan",
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	monthlyPlan := SubscriptionPlan{
		Id:               7022,
		Title:            "Monthly reset plan",
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetMonthly,
	}
	require.NoError(t, db.Create(&dailyPlan).Error)
	require.NoError(t, db.Create(&monthlyPlan).Error)
	for _, planID := range []int{dailyPlan.Id, monthlyPlan.Id} {
		InvalidateSubscriptionPlanCache(planID)
		planID := planID
		t.Cleanup(func() {
			InvalidateSubscriptionPlanCache(planID)
		})
	}

	now := common.GetTimestamp()
	nowTime := time.Unix(now, 0)
	lastResetTime := time.Date(
		nowTime.Year(),
		nowTime.Month()-1,
		1,
		0,
		0,
		0,
		0,
		nowTime.Location(),
	).Unix()
	subscription := UserSubscription{
		Id:            7023,
		UserId:        7024,
		PlanId:        dailyPlan.Id,
		AmountTotal:   100,
		AmountUsed:    50,
		StartTime:     lastResetTime,
		EndTime:       now + 180*86400,
		Status:        "active",
		Source:        "order",
		LastResetTime: lastResetTime,
		NextResetTime: now - 60,
	}
	require.NoError(t, db.Create(&subscription).Error)
	verifyInjection := injectUserSubscriptionScanDrift(
		t,
		db,
		subscription.Id,
		map[string]interface{}{"plan_id": monthlyPlan.Id},
	)

	resetCount, err := ResetDueSubscriptions(10)

	verifyInjection()
	require.NoError(t, err)
	assert.Equal(t, 1, resetCount)
	stored := loadSubscriptionUsageState(t, db, subscription.Id)
	assert.Equal(t, monthlyPlan.Id, stored.PlanId)
	assert.Zero(t, stored.AmountUsed)

	expectedBase := time.Unix(lastResetTime, 0)
	expectedNextReset := calcNextResetTime(expectedBase, &monthlyPlan, subscription.EndTime)
	for expectedNextReset > 0 && expectedNextReset <= now {
		expectedBase = time.Unix(expectedNextReset, 0)
		expectedNextReset = calcNextResetTime(expectedBase, &monthlyPlan, subscription.EndTime)
	}
	assert.Equal(t, expectedBase.Unix(), stored.LastResetTime)
	assert.Equal(t, expectedNextReset, stored.NextResetTime)
}

func TestResetDueSubscriptionsDoesNotCountPlanChangedToNever(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	dailyPlan := SubscriptionPlan{
		Id:               7121,
		Title:            "Daily reset plan",
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	neverPlan := SubscriptionPlan{
		Id:               7122,
		Title:            "Never reset plan",
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	require.NoError(t, db.Create(&dailyPlan).Error)
	require.NoError(t, db.Create(&neverPlan).Error)
	for _, planID := range []int{dailyPlan.Id, neverPlan.Id} {
		InvalidateSubscriptionPlanCache(planID)
		planID := planID
		t.Cleanup(func() {
			InvalidateSubscriptionPlanCache(planID)
		})
	}

	now := common.GetTimestamp()
	subscription := UserSubscription{
		Id:            7123,
		UserId:        7124,
		PlanId:        dailyPlan.Id,
		AmountTotal:   100,
		AmountUsed:    50,
		StartTime:     now - 3*86400,
		EndTime:       now + 30*86400,
		Status:        "active",
		Source:        "order",
		LastResetTime: now - 2*86400,
		NextResetTime: now - 60,
	}
	require.NoError(t, db.Create(&subscription).Error)
	verifyInjection := injectUserSubscriptionScanDrift(
		t,
		db,
		subscription.Id,
		map[string]interface{}{"plan_id": neverPlan.Id},
	)

	resetCount, err := ResetDueSubscriptions(10)

	verifyInjection()
	require.NoError(t, err)
	assert.Zero(t, resetCount)
	stored := loadSubscriptionUsageState(t, db, subscription.Id)
	assert.Equal(t, neverPlan.Id, stored.PlanId)
	assert.Equal(t, subscription.AmountUsed, stored.AmountUsed)
	assert.Zero(t, stored.LastResetTime)
	assert.Zero(t, stored.NextResetTime)
}

func TestResetDueSubscriptionsBypassesStalePlanCacheAndRepairsSchedule(t *testing.T) {
	tests := []struct {
		name               string
		updatedResetPeriod string
		expectedSchedule   func(
			subscription UserSubscription,
			plan SubscriptionPlan,
		) (int64, int64)
	}{
		{
			name:               "plan changed to never",
			updatedResetPeriod: SubscriptionResetNever,
			expectedSchedule: func(
				UserSubscription,
				SubscriptionPlan,
			) (int64, int64) {
				return 0, 0
			},
		},
		{
			name:               "plan changed to a future monthly reset",
			updatedResetPeriod: SubscriptionResetMonthly,
			expectedSchedule: func(
				subscription UserSubscription,
				plan SubscriptionPlan,
			) (int64, int64) {
				return subscription.LastResetTime, calcNextResetTime(
					time.Unix(subscription.LastResetTime, 0),
					&plan,
					subscription.EndTime,
				)
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := useSubscriptionUsageTestDB(t)
			planID := 7161 + index*10
			plan := SubscriptionPlan{
				Id:               planID,
				Title:            "Cached daily reset plan",
				DurationUnit:     SubscriptionDurationMonth,
				DurationValue:    1,
				TotalAmount:      100,
				QuotaResetPeriod: SubscriptionResetDaily,
			}
			require.NoError(t, db.Create(&plan).Error)
			InvalidateSubscriptionPlanCache(plan.Id)
			t.Cleanup(func() {
				InvalidateSubscriptionPlanCache(plan.Id)
			})
			cachedPlan, err := GetSubscriptionPlanById(plan.Id)
			require.NoError(t, err)
			require.Equal(t, SubscriptionResetDaily, cachedPlan.QuotaResetPeriod)

			now := common.GetTimestamp()
			nowTime := time.Unix(now, 0)
			lastResetTime := time.Date(
				nowTime.Year(),
				nowTime.Month(),
				1,
				0,
				0,
				0,
				0,
				nowTime.Location(),
			).Unix()
			subscription := UserSubscription{
				Id:            planID + 1,
				UserId:        planID + 2,
				PlanId:        plan.Id,
				AmountTotal:   100,
				AmountUsed:    50,
				StartTime:     lastResetTime,
				EndTime:       now + 180*86400,
				Status:        "active",
				Source:        "order",
				LastResetTime: lastResetTime,
				NextResetTime: now - 60,
			}
			require.NoError(t, db.Create(&subscription).Error)

			injected := false
			var injectionErr error
			const callbackName = "test:update_cached_subscription_plan_after_scan"
			require.NoError(t, db.Callback().Query().
				After("gorm:query").
				Register(callbackName, func(tx *gorm.DB) {
					if injected {
						return
					}
					subscriptions, ok := tx.Statement.Dest.(*[]UserSubscription)
					if !ok || len(*subscriptions) == 0 {
						return
					}
					injected = true
					injectionErr = tx.Session(&gorm.Session{NewDB: true}).
						Model(&SubscriptionPlan{}).
						Where("id = ?", plan.Id).
						Update("quota_reset_period", test.updatedResetPeriod).Error
				}))
			t.Cleanup(func() {
				require.NoError(t, db.Callback().Query().Remove(callbackName))
			})

			resetCount, err := ResetDueSubscriptions(10)

			require.True(t, injected)
			require.NoError(t, injectionErr)
			require.NoError(t, err)
			assert.Zero(t, resetCount)
			stored := loadSubscriptionUsageState(t, db, subscription.Id)
			assert.Equal(t, subscription.AmountUsed, stored.AmountUsed)
			plan.QuotaResetPeriod = test.updatedResetPeriod
			expectedLastReset, expectedNextReset := test.expectedSchedule(
				subscription,
				plan,
			)
			assert.Equal(t, expectedLastReset, stored.LastResetTime)
			assert.Equal(t, expectedNextReset, stored.NextResetTime)
			assert.GreaterOrEqual(t, expectedNextReset, int64(0))
		})
	}
}

func TestResetDueSubscriptionsBatchTracksRepairedRows(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	now := common.GetTimestamp()
	missingPlanSubscription := UserSubscription{
		Id:            7181,
		UserId:        7182,
		PlanId:        7183,
		AmountTotal:   100,
		AmountUsed:    50,
		StartTime:     now - 3*86400,
		EndTime:       now + 30*86400,
		Status:        "active",
		Source:        "order",
		LastResetTime: now - 2*86400,
		NextResetTime: now - 120,
	}
	require.NoError(t, db.Create(&missingPlanSubscription).Error)
	validSubscription := seedSubscriptionUsageTest(t, db, 7191, 7192, 7193, 50)
	require.NoError(t, db.Model(&UserSubscription{}).
		Where("id = ?", validSubscription.Id).
		Updates(map[string]interface{}{
			"last_reset_time": now - 2*86400,
			"next_reset_time": now - 60,
		}).Error)
	require.NoError(t, db.Model(&SubscriptionPlan{}).
		Where("id = ?", validSubscription.PlanId).
		Update("quota_reset_period", SubscriptionResetDaily).Error)
	InvalidateSubscriptionPlanCache(validSubscription.PlanId)

	firstBatch, err := ResetDueSubscriptionsBatch(1)

	require.NoError(t, err)
	assert.Equal(t, 1, firstBatch.ProcessedCount)
	assert.Zero(t, firstBatch.ResetCount)
	storedMissingPlan := loadSubscriptionUsageState(
		t,
		db,
		missingPlanSubscription.Id,
	)
	assert.Equal(t, missingPlanSubscription.AmountUsed, storedMissingPlan.AmountUsed)
	assert.Zero(t, storedMissingPlan.LastResetTime)
	assert.Zero(t, storedMissingPlan.NextResetTime)

	secondBatch, err := ResetDueSubscriptionsBatch(1)

	require.NoError(t, err)
	assert.Equal(t, 1, secondBatch.ProcessedCount)
	assert.Equal(t, 1, secondBatch.ResetCount)
	assert.Zero(
		t,
		loadSubscriptionUsageState(t, db, validSubscription.Id).AmountUsed,
	)

	finalBatch, err := ResetDueSubscriptionsBatch(1)

	require.NoError(t, err)
	assert.Zero(t, finalBatch.ProcessedCount)
	assert.Zero(t, finalBatch.ResetCount)
}

func TestPreConsumeUserSubscriptionIdempotencyRejectsConflictingReplay(t *testing.T) {
	tests := []struct {
		name         string
		replayUserID func(firstUserID int) int
		replayAmount int64
	}{
		{
			name: "different user",
			replayUserID: func(firstUserID int) int {
				return firstUserID + 100
			},
			replayAmount: 40,
		},
		{
			name: "different amount",
			replayUserID: func(firstUserID int) int {
				return firstUserID
			},
			replayAmount: 41,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := useSubscriptionUsageTestDB(t)
			planID := 7041 + index*20
			first := seedSubscriptionUsageTest(
				t,
				db,
				planID,
				planID+1,
				planID+2,
				0,
			)
			replayUserID := test.replayUserID(first.UserId)
			var replaySubscription *UserSubscription
			if replayUserID != first.UserId {
				other := seedSubscriptionUsageTest(
					t,
					db,
					planID+10,
					planID+11,
					replayUserID,
					0,
				)
				replaySubscription = &other
			}

			firstResult, err := PreConsumeUserSubscription(
				"idempotency-conflict-"+test.name,
				first.UserId,
				"test-model",
				0,
				40,
			)
			require.NoError(t, err)
			require.NotNil(t, firstResult)

			replayResult, err := PreConsumeUserSubscription(
				"idempotency-conflict-"+test.name,
				replayUserID,
				"test-model",
				0,
				test.replayAmount,
			)

			require.Error(t, err)
			assert.Nil(t, replayResult)
			assert.EqualValues(t, 40, loadSubscriptionUsageState(t, db, first.Id).AmountUsed)
			if replaySubscription != nil {
				assert.Zero(
					t,
					loadSubscriptionUsageState(t, db, replaySubscription.Id).AmountUsed,
				)
			}
			var recordCount int64
			require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).
				Where("request_id = ?", "idempotency-conflict-"+test.name).
				Count(&recordCount).Error)
			assert.EqualValues(t, 1, recordCount)
		})
	}
}

func TestPreConsumeUserSubscriptionIdempotencyRejectsUnknownStatus(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	subscription := seedSubscriptionUsageTest(t, db, 7081, 7082, 7083, 0)
	record := SubscriptionPreConsumeRecord{
		RequestId:          "idempotency-unknown-status",
		UserId:             subscription.UserId,
		UserSubscriptionId: subscription.Id,
		PreConsumed:        40,
		Status:             "pending",
	}
	require.NoError(t, db.Create(&record).Error)

	result, err := PreConsumeUserSubscription(
		record.RequestId,
		subscription.UserId,
		"test-model",
		0,
		record.PreConsumed,
	)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Zero(t, loadSubscriptionUsageState(t, db, subscription.Id).AmountUsed)
}

func TestPreConsumeUserSubscriptionConcurrentReplayChargesOnce(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	subscription := seedSubscriptionUsageTest(t, db, 7091, 7092, 7093, 0)
	const requestID = "idempotency-concurrent-replay"

	var insertAttempts atomic.Int32
	firstInserted := make(chan struct{})
	secondAttempted := make(chan struct{})
	releaseFirst := make(chan struct{})
	const (
		beforeCallback = "test:observe_subscription_claim_attempt"
		afterCallback  = "test:block_first_subscription_claim"
		attemptKey     = "test:subscription_claim_attempt"
	)
	require.NoError(t, db.Callback().Create().
		Before("gorm:create").
		Register(beforeCallback, func(tx *gorm.DB) {
			record, ok := tx.Statement.Dest.(*SubscriptionPreConsumeRecord)
			if !ok || record.RequestId != requestID {
				return
			}
			attempt := insertAttempts.Add(1)
			tx.InstanceSet(attemptKey, attempt)
			if attempt == 2 {
				close(secondAttempted)
			}
		}))
	require.NoError(t, db.Callback().Create().
		After("gorm:create").
		Register(afterCallback, func(tx *gorm.DB) {
			value, ok := tx.InstanceGet(attemptKey)
			if !ok || value.(int32) != 1 || tx.Error != nil {
				return
			}
			close(firstInserted)
			<-releaseFirst
		}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Create().Remove(beforeCallback))
		require.NoError(t, db.Callback().Create().Remove(afterCallback))
	})

	type outcome struct {
		result *SubscriptionPreConsumeResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	consume := func() {
		result, err := PreConsumeUserSubscription(
			requestID,
			subscription.UserId,
			"test-model",
			0,
			40,
		)
		outcomes <- outcome{result: result, err: err}
	}

	go consume()
	<-firstInserted
	go consume()
	<-secondAttempted
	close(releaseFirst)

	firstOutcome := <-outcomes
	secondOutcome := <-outcomes
	require.NoError(t, firstOutcome.err)
	require.NoError(t, secondOutcome.err)
	require.NotNil(t, firstOutcome.result)
	require.NotNil(t, secondOutcome.result)
	assert.Equal(
		t,
		firstOutcome.result.UserSubscriptionId,
		secondOutcome.result.UserSubscriptionId,
	)
	assert.Equal(t, subscription.Id, firstOutcome.result.UserSubscriptionId)
	assert.EqualValues(t, 40, loadSubscriptionUsageState(t, db, subscription.Id).AmountUsed)

	var recordCount int64
	require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).
		Where("request_id = ?", requestID).
		Count(&recordCount).Error)
	assert.EqualValues(t, 1, recordCount)
}

func TestPreConsumeUserSubscriptionReplayIgnoresDuplicateRowsAffected(t *testing.T) {
	db := useSubscriptionUsageTestDB(t)
	subscription := seedSubscriptionUsageTest(t, db, 7141, 7142, 7143, 0)
	const requestID = "idempotency-client-found-rows"

	firstResult, err := PreConsumeUserSubscription(
		requestID,
		subscription.UserId,
		"test-model",
		0,
		40,
	)
	require.NoError(t, err)
	require.NotNil(t, firstResult)

	forcedDuplicateRowsAffected := false
	const callbackName = "test:simulate_mysql_client_found_rows"
	require.NoError(t, db.Callback().Create().
		After("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			record, ok := tx.Statement.Dest.(*SubscriptionPreConsumeRecord)
			if !ok || record.RequestId != requestID || tx.Error != nil {
				return
			}
			if tx.RowsAffected == 0 {
				forcedDuplicateRowsAffected = true
				tx.RowsAffected = 1
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Create().Remove(callbackName))
	})

	replayResult, err := PreConsumeUserSubscription(
		requestID,
		subscription.UserId,
		"test-model",
		0,
		40,
	)

	require.True(t, forcedDuplicateRowsAffected)
	require.NoError(t, err)
	require.NotNil(t, replayResult)
	assert.Equal(t, firstResult.UserSubscriptionId, replayResult.UserSubscriptionId)
	assert.EqualValues(t, 40, loadSubscriptionUsageState(t, db, subscription.Id).AmountUsed)
}
