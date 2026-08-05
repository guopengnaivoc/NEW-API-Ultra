package model

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useSubscriptionPlanDefaultsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousType := common.MainDatabaseType()
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		sqlDB, dbErr := db.DB()
		require.NoError(t, dbErr)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestSubscriptionPlanEnabledDefaultsWithoutLosingExplicitFalse(t *testing.T) {
	db := useSubscriptionPlanDefaultsTestDB(t)
	require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}))

	tests := []struct {
		name        string
		requestJSON string
		wantEnabled bool
	}{
		{
			name:        "omitted defaults enabled",
			requestJSON: `{"title":"default-enabled"}`,
			wantEnabled: true,
		},
		{
			name:        "explicit false stays disabled",
			requestJSON: `{"title":"explicitly-disabled","enabled":false}`,
			wantEnabled: false,
		},
		{
			name:        "explicit true stays enabled",
			requestJSON: `{"title":"explicitly-enabled","enabled":true}`,
			wantEnabled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var plan SubscriptionPlan
			require.NoError(t, common.Unmarshal([]byte(test.requestJSON), &plan))
			require.NoError(t, db.Create(&plan).Error)

			var stored struct {
				Enabled bool
			}
			require.NoError(t, db.Model(&SubscriptionPlan{}).
				Select("enabled").
				Where("id = ?", plan.Id).
				Scan(&stored).Error)
			assert.Equal(t, test.wantEnabled, stored.Enabled)
		})
	}
}

func TestEnsureSubscriptionPlanTableSQLiteBackfillsMissingEnabledColumn(t *testing.T) {
	db := useSubscriptionPlanDefaultsTestDB(t)
	require.NoError(t, ensureSubscriptionPlanTableSQLite())
	require.NoError(t, db.Exec("ALTER TABLE `subscription_plans` DROP COLUMN `enabled`").Error)
	require.NoError(t, db.Exec(`
		INSERT INTO subscription_plans
			(id, title, price_amount, currency, duration_unit, duration_value, custom_seconds, total_amount)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, 1, "legacy plan", 1, "USD", "month", 1, 0, 100).Error)

	require.NoError(t, ensureSubscriptionPlanTableSQLite())

	var stored struct {
		Enabled sql.NullBool
	}
	require.NoError(t, db.Raw(
		"SELECT enabled FROM subscription_plans WHERE id = ?",
		1,
	).Scan(&stored).Error)
	require.True(t, stored.Enabled.Valid)
	assert.True(t, stored.Enabled.Bool)
}

func TestSubscriptionPlanEnabledSchemaHasNoDatabaseDefault(t *testing.T) {
	tests := []struct {
		name    string
		migrate func(t *testing.T, db *gorm.DB)
	}{
		{
			name: "gorm schema",
			migrate: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}))
			},
		},
		{
			name: "manual sqlite schema",
			migrate: func(t *testing.T, _ *gorm.DB) {
				require.NoError(t, ensureSubscriptionPlanTableSQLite())
				require.NoError(t, ensureSubscriptionPlanTableSQLite())
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := useSubscriptionPlanDefaultsTestDB(t)
			test.migrate(t, db)

			var columns []struct {
				Name         string
				DefaultValue sql.NullString `gorm:"column:dflt_value"`
			}
			require.NoError(t, db.Raw("PRAGMA table_info(`subscription_plans`)").Scan(&columns).Error)

			for _, column := range columns {
				if column.Name == "enabled" {
					assert.False(t, column.DefaultValue.Valid)
					return
				}
			}
			t.Fatal("enabled column not found")
		})
	}
}
