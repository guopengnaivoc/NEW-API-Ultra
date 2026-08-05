package model

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	sqlitedriver "github.com/glebarez/go-sqlite"
	"github.com/glebarez/sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestInitTaskDoesNotRetainProviderCredential(t *testing.T) {
	truncateTables(t)

	for _, channelType := range []int{
		constant.ChannelTypeGemini,
		constant.ChannelTypeVertexAi,
	} {
		t.Run(strconv.Itoa(channelType), func(t *testing.T) {
			credential := "provider-credential-must-not-be-copied"
			task := InitTask(
				constant.TaskPlatform("credential-test"),
				&relaycommon.RelayInfo{
					UserId:     9101,
					UsingGroup: "default",
					ChannelMeta: &relaycommon.ChannelMeta{
						ChannelId:   channelType,
						ChannelType: channelType,
						ApiKey:      credential,
					},
				},
			)

			assert.Empty(t, task.PrivateData.Key)
			assert.NotEmpty(t, task.PrivateData.ChannelKeyFingerprint)
			require.NoError(t, DB.Create(task).Error)

			var stored sql.NullString
			require.NoError(t, DB.Model(&Task{}).
				Select("private_data").
				Where("id = ?", task.ID).
				Scan(&stored).Error)
			require.True(t, stored.Valid)
			assert.NotContains(t, stored.String, credential)
		})
	}
}

func TestTaskPrivateDataJSONDoesNotExposeLegacyCredential(t *testing.T) {
	credential := "legacy-provider-credential"

	encoded, err := common.Marshal(TaskPrivateData{
		Key:            credential,
		UpstreamTaskID: "upstream-task",
	})

	require.NoError(t, err)
	assert.NotContains(t, string(encoded), credential)
	assert.NotContains(t, string(encoded), `"key"`)
	assert.Contains(t, string(encoded), `"channel_key_fingerprint"`)
}

func TestResolveTaskChannelKeyMatchesFingerprintAfterReorder(t *testing.T) {
	channel := &Channel{
		Key: "provider-key-a\nprovider-key-b",
		ChannelInfo: ChannelInfo{
			IsMultiKey: true,
		},
	}
	task := &Task{PrivateData: TaskPrivateData{
		ChannelKeyFingerprint: taskChannelKeyFingerprint("provider-key-b"),
	}}

	key, err := ResolveTaskChannelKey(channel, task)

	require.NoError(t, err)
	assert.Equal(t, "provider-key-b", key)

	channel.Key = "provider-key-b\nprovider-key-a"
	key, err = ResolveTaskChannelKey(channel, task)

	require.NoError(t, err)
	assert.Equal(t, "provider-key-b", key)
}

func TestResolveTaskChannelKeyRejectsRemovedOrAmbiguousCredential(t *testing.T) {
	channel := &Channel{
		Key: "provider-key-a\nprovider-key-b",
		ChannelInfo: ChannelInfo{
			IsMultiKey: true,
		},
	}

	_, err := ResolveTaskChannelKey(channel, &Task{PrivateData: TaskPrivateData{
		ChannelKeyFingerprint: taskChannelKeyFingerprint("removed-key"),
	}})
	require.ErrorIs(t, err, ErrTaskChannelCredentialUnavailable)

	_, err = ResolveTaskChannelKey(channel, &Task{})
	require.ErrorIs(t, err, ErrTaskChannelCredentialUnavailable)

	_, err = ResolveTaskChannelKey(nil, &Task{})
	require.ErrorIs(t, err, ErrTaskChannelCredentialUnavailable)
}

func TestResolveTaskChannelKeySupportsSingleAndInMemoryLegacyTasks(t *testing.T) {
	channel := &Channel{Key: "current-provider-key"}

	key, err := ResolveTaskChannelKey(channel, &Task{})
	require.NoError(t, err)
	assert.Equal(t, "current-provider-key", key)

	key, err = ResolveTaskChannelKey(channel, &Task{PrivateData: TaskPrivateData{
		Key: "legacy-provider-key",
	}})
	require.NoError(t, err)
	assert.Equal(t, "legacy-provider-key", key)
}

func TestMigrateTaskPrivateCredentialsRemovesLegacyKeyIdempotently(
	t *testing.T,
) {
	truncateTables(t)

	task := &Task{
		TaskID:    "legacy-task-credential",
		Platform:  constant.TaskPlatform("credential-test"),
		UserId:    9201,
		ChannelId: 9202,
		Status:    TaskStatusInProgress,
		Progress:  "50%",
	}
	require.NoError(t, DB.Create(task).Error)

	credential := "legacy-provider-credential-in-database"
	legacyJSON := `{"key":"` + credential +
		`","upstream_task_id":"upstream-legacy"}`
	require.NoError(t, DB.Model(&Task{}).
		Where("id = ?", task.ID).
		UpdateColumn("private_data", []byte(legacyJSON)).Error)

	var before sql.NullString
	require.NoError(t, DB.Model(&Task{}).
		Select("private_data").
		Where("id = ?", task.ID).
		Scan(&before).Error)
	require.True(t, before.Valid)
	require.Contains(t, before.String, credential)

	predicate, err := legacyTaskCredentialPredicate()
	require.NoError(t, err)
	var legacyCount int64
	require.NoError(t, DB.Model(&Task{}).Where(predicate).Count(&legacyCount).Error)
	require.EqualValues(t, 1, legacyCount)

	require.NoError(t, MigrateTaskPrivateCredentials())

	var after sql.NullString
	require.NoError(t, DB.Model(&Task{}).
		Select("private_data").
		Where("id = ?", task.ID).
		Scan(&after).Error)
	require.True(t, after.Valid)
	assert.NotContains(t, after.String, credential)
	assert.NotContains(t, after.String, `"key"`)

	var privateData TaskPrivateData
	require.NoError(t, common.Unmarshal([]byte(after.String), &privateData))
	assert.Empty(t, privateData.Key)
	assert.Equal(
		t,
		taskChannelKeyFingerprint(credential),
		privateData.ChannelKeyFingerprint,
	)
	assert.Equal(t, "upstream-legacy", privateData.UpstreamTaskID)

	require.NoError(t, MigrateTaskPrivateCredentials())

	var afterRetry sql.NullString
	require.NoError(t, DB.Model(&Task{}).
		Select("private_data").
		Where("id = ?", task.ID).
		Scan(&afterRetry).Error)
	assert.Equal(t, after, afterRetry)
}

func TestMigrateTaskPrivateCredentialsSanitizesTaskWriteDriverErrors(
	t *testing.T,
) {
	const (
		operationSentinel   = "task-credential-migration-operation-sentinel"
		fingerprintSentinel = "task-credential-migration-fingerprint-sentinel"
		legacyKey           = "task-credential-migration-legacy-key"
	)

	tests := []struct {
		name           string
		driverIdentity string
		newDriverError func(t *testing.T, db *gorm.DB) error
	}{
		{
			name:           "mysql",
			driverIdentity: "mysql error 1062",
			newDriverError: func(t *testing.T, _ *gorm.DB) error {
				t.Helper()
				return &mysql.MySQLError{
					Number:  1062,
					Message: operationSentinel + " " + fingerprintSentinel,
				}
			},
		},
		{
			name:           "postgres",
			driverIdentity: "postgres error SQLSTATE 23505",
			newDriverError: func(t *testing.T, _ *gorm.DB) error {
				t.Helper()
				return &pgconn.PgError{
					Code:    "23505",
					Message: operationSentinel,
					Detail:  fingerprintSentinel,
				}
			},
		},
		{
			name:           "sqlite",
			driverIdentity: "sqlite error 1",
			newDriverError: func(t *testing.T, db *gorm.DB) error {
				t.Helper()
				err := db.Exec(
					"UPDATE task_credential_migration_missing_table SET private_data = ?",
					"trigger-driver-error",
				).Error
				require.Error(t, err)
				var sqliteErr *sqlitedriver.Error
				require.ErrorAs(t, err, &sqliteErr)
				require.Equal(t, 1, sqliteErr.Code())
				return fmt.Errorf(
					"%s %s: %w",
					operationSentinel,
					fingerprintSentinel,
					err,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var databaseLog bytes.Buffer
			dsn := "file:" + filepath.Join(
				t.TempDir(),
				"task-credential-migration-driver-error.db",
			) + "?_pragma=busy_timeout(1000)"
			db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
				Logger: newGormLogger(&databaseLog),
			})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)

			previousDB := DB
			previousDatabaseType := common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseTypeSQLite)
			initCol()
			t.Cleanup(func() {
				DB = previousDB
				common.SetMainDatabaseType(previousDatabaseType)
				initCol()
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, DB.AutoMigrate(&Task{}))

			task := &Task{
				TaskID:   "task_credential_migration_driver_error",
				Platform: constant.TaskPlatform("credential-test"),
				Status:   TaskStatusInProgress,
				Progress: "50%",
			}
			require.NoError(t, DB.Create(task).Error)
			require.NoError(t, DB.Model(&Task{}).
				Where("id = ?", task.ID).
				UpdateColumn(
					"private_data",
					[]byte(`{"key":"`+legacyKey+`","upstream_task_id":"`+
						operationSentinel+`","channel_key_fingerprint":"`+
						fingerprintSentinel+`"}`),
				).Error)

			driverErr := test.newDriverError(t, DB)
			databaseLog.Reset()
			callbackName := "test:task-credential-migration-private-data-error:" +
				test.name
			matchedPrivateDataUpdate := false
			unexpectedTaskUpdate := false
			require.NoError(t, DB.Callback().Update().Before("gorm:update").
				Register(callbackName, func(tx *gorm.DB) {
					if tx.Statement == nil || tx.Statement.Table != "tasks" {
						return
					}
					updates, ok := tx.Statement.Dest.(map[string]interface{})
					if !ok || len(updates) != 1 {
						unexpectedTaskUpdate = true
						return
					}
					_, onlyPrivateData := updates["private_data"]
					if !onlyPrivateData {
						unexpectedTaskUpdate = true
						return
					}
					matchedPrivateDataUpdate = true
					tx.AddError(driverErr)
				}))
			t.Cleanup(func() {
				require.NoError(t, DB.Callback().Update().Remove(callbackName))
			})

			var applicationLog bytes.Buffer
			common.LogWriterMu.Lock()
			previousApplicationLogWriter := gin.DefaultErrorWriter
			gin.DefaultErrorWriter = &applicationLog
			common.LogWriterMu.Unlock()
			t.Cleanup(func() {
				common.LogWriterMu.Lock()
				gin.DefaultErrorWriter = previousApplicationLogWriter
				common.LogWriterMu.Unlock()
			})

			err = MigrateTaskPrivateCredentials()
			require.Error(t, err)
			DB.Logger.Trace(
				context.Background(),
				time.Now(),
				func() (string, int64) {
					return "UPDATE tasks SET private_data = ? WHERE id = ?", 0
				},
				driverErr,
			)
			common.SysError(err.Error())

			require.True(t, matchedPrivateDataUpdate)
			assert.False(t, unexpectedTaskUpdate)
			assert.ErrorContains(t, err, "sanitize task private data for task")
			for _, surface := range []string{
				err.Error(),
				databaseLog.String(),
				applicationLog.String(),
			} {
				assert.Contains(t, surface, test.driverIdentity)
				assert.NotContains(t, surface, operationSentinel)
				assert.NotContains(t, surface, fingerprintSentinel)
			}
		})
	}
}
