package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func useSetupOptionPublicationTestState(t *testing.T) *gorm.DB {
	t.Helper()
	db := useSetupTestDB(t)
	previousSelfUse := operation_setting.SelfUseModeEnabled
	previousDemo := operation_setting.DemoSiteEnabled
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = map[string]string{"UnrelatedOption": "kept"}
	common.OptionMapRWMutex.Unlock()
	operation_setting.SelfUseModeEnabled = false
	operation_setting.DemoSiteEnabled = true
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = previousSelfUse
		operation_setting.DemoSiteEnabled = previousDemo
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})
	return db
}

func TestPublishPersistedSetupOptionsQuotesReservedOptionKey(t *testing.T) {
	tests := []struct {
		name      string
		dialector gorm.Dialector
		quotedKey string
	}{
		{
			name:      "sqlite",
			dialector: sqlite.Open(":memory:"),
			quotedKey: "`key`",
		},
		{
			name: "mysql",
			dialector: mysql.New(mysql.Config{
				DSN:                       "test:test@tcp(127.0.0.1:3306)/test?charset=utf8mb4&parseTime=True&loc=Local",
				SkipInitializeWithVersion: true,
			}),
			quotedKey: "`key`",
		},
		{
			name: "postgres",
			dialector: postgres.New(postgres.Config{
				DSN:                  "host=127.0.0.1 user=test password=test dbname=test port=5432 sslmode=disable",
				PreferSimpleProtocol: true,
			}),
			quotedKey: `"key"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &migrationSQLRecorder{}
			db, err := gorm.Open(test.dialector, &gorm.Config{
				DisableAutomaticPing: true,
				DryRun:               true,
				Logger:               recorder,
			})
			require.NoError(t, err)
			previousDB := DB
			DB = db
			t.Cleanup(func() {
				DB = previousDB
				sqlDB, dbErr := db.DB()
				require.NoError(t, dbErr)
				require.NoError(t, sqlDB.Close())
			})

			require.NoError(t, PublishPersistedSetupOptions())

			recorder.mu.Lock()
			statements := append([]string(nil), recorder.statements...)
			recorder.mu.Unlock()
			require.Len(t, statements, 1)
			assert.Contains(t, statements[0], "WHERE "+test.quotedKey+" IN")
		})
	}
}

func TestPublishPersistedSetupOptionsToleratesNoRows(t *testing.T) {
	useSetupOptionPublicationTestState(t)

	err := PublishPersistedSetupOptions()

	require.NoError(t, err)
	assert.False(t, operation_setting.SelfUseModeEnabled)
	assert.True(t, operation_setting.DemoSiteEnabled)
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, map[string]string{"UnrelatedOption": "kept"}, common.OptionMap)
}

func TestPublishPersistedSetupOptionsPublishesOnePresentRow(t *testing.T) {
	db := useSetupOptionPublicationTestState(t)
	require.NoError(t, db.Create(&Option{
		Key: "SelfUseModeEnabled", Value: "true",
	}).Error)
	require.NoError(t, db.Create(&Option{
		Key: "SystemName", Value: "must-not-publish",
	}).Error)

	err := PublishPersistedSetupOptions()

	require.NoError(t, err)
	assert.True(t, operation_setting.SelfUseModeEnabled)
	assert.True(t, operation_setting.DemoSiteEnabled)
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, "true", common.OptionMap["SelfUseModeEnabled"])
	assert.Equal(t, "kept", common.OptionMap["UnrelatedOption"])
	assert.NotContains(t, common.OptionMap, "SystemName")
	assert.NotContains(t, common.OptionMap, "DemoSiteEnabled")
}

func TestPublishPersistedSetupOptionsPublishesBothRows(t *testing.T) {
	db := useSetupOptionPublicationTestState(t)
	require.NoError(t, db.Create(&[]Option{
		{Key: "SelfUseModeEnabled", Value: "true"},
		{Key: "DemoSiteEnabled", Value: "false"},
	}).Error)

	err := PublishPersistedSetupOptions()

	require.NoError(t, err)
	assert.True(t, operation_setting.SelfUseModeEnabled)
	assert.False(t, operation_setting.DemoSiteEnabled)
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, "true", common.OptionMap["SelfUseModeEnabled"])
	assert.Equal(t, "false", common.OptionMap["DemoSiteEnabled"])
	assert.Equal(t, "kept", common.OptionMap["UnrelatedOption"])
}

func TestPublishPersistedSetupOptionsReturnsQueryErrorWithoutPublication(t *testing.T) {
	db := useSetupOptionPublicationTestState(t)
	require.NoError(t, db.Migrator().DropTable(&Option{}))

	err := PublishPersistedSetupOptions()

	require.Error(t, err)
	assert.False(t, operation_setting.SelfUseModeEnabled)
	assert.True(t, operation_setting.DemoSiteEnabled)
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, map[string]string{"UnrelatedOption": "kept"}, common.OptionMap)
}
