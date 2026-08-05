package model

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	"github.com/glebarez/sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const oversizedTaskProviderResultURISentinel = "oversized-provider-result-uri-sentinel"

func oversizedTaskProviderResultURIForTest() string {
	const prefix = "https://video.example.test/"
	padding := geminitaskresult.MaxProviderResultURIBytes + 1 -
		len(prefix) - len(oversizedTaskProviderResultURISentinel)
	return prefix + strings.Repeat("x", padding) +
		oversizedTaskProviderResultURISentinel
}

func useFileBackedTaskProviderResultURITestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := DB
	previousMainDatabaseType := common.MainDatabaseType()
	dsn := "file:" + filepath.Join(t.TempDir(), "task-result-uri-migration-race.db") +
		"?_pragma=busy_timeout(50)&_pragma=journal_mode(WAL)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)

	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	initCol()
	require.NoError(t, DB.AutoMigrate(&Task{}))
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousMainDatabaseType)
		initCol()
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func insertRawTaskProviderResultURI(
	t *testing.T,
	taskID string,
	storedURI string,
) *Task {
	t.Helper()

	task := &Task{
		TaskID: taskID,
		Data:   []byte(`{"done":false}`),
	}
	insertTask(t, task)
	require.NoError(t, DB.Model(&Task{}).
		Where("id = ?", task.ID).
		UpdateColumn("provider_result_uri", storedURI).Error)
	return task
}

func TestTaskProviderResultWriteMethodsClassifyDriverErrors(t *testing.T) {
	const (
		envelope = "naenc:v1:task-write-private-key:" +
			"task-write-private-nonce:task-write-private-ciphertext"
		driverMessage = "task-write-driver-private-sentinel " + envelope
	)

	t.Run("insert", func(t *testing.T) {
		prepareTaskProviderResultURITest(t)
		task := &Task{
			TaskID:                     "task_write_insert_public",
			Status:                     TaskStatusInProgress,
			EncryptedProviderResultURI: common.GetPointer(envelope),
			Data:                       []byte(`{"done":false}`),
		}
		callbackName := "test:task-provider-result-insert-driver-error"
		require.NoError(t, DB.Callback().Create().Before("gorm:create").
			Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table == "tasks" {
					tx.AddError(&mysql.MySQLError{
						Number:  1062,
						Message: driverMessage,
					})
				}
			}))
		t.Cleanup(func() {
			require.NoError(t, DB.Callback().Create().Remove(callbackName))
		})

		err := task.Insert()

		require.EqualError(t, err, "mysql error 1062")
		assert.NotContains(t, err.Error(), driverMessage)
		assert.NotContains(t, err.Error(), envelope)
		assert.NotContains(t, err.Error(), "naenc:v1")
	})

	t.Run("save", func(t *testing.T) {
		prepareTaskProviderResultURITest(t)
		task := &Task{
			TaskID: "task_write_save_public",
			Status: TaskStatusInProgress,
			Data:   []byte(`{"done":false}`),
		}
		insertTask(t, task)
		task.EncryptedProviderResultURI = common.GetPointer(envelope)
		callbackName := "test:task-provider-result-save-driver-error"
		require.NoError(t, DB.Callback().Update().Before("gorm:update").
			Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table == "tasks" {
					tx.AddError(&mysql.MySQLError{
						Number:  1062,
						Message: driverMessage,
					})
				}
			}))
		t.Cleanup(func() {
			require.NoError(t, DB.Callback().Update().Remove(callbackName))
		})

		err := task.Update()

		require.EqualError(t, err, "mysql error 1062")
		assert.NotContains(t, err.Error(), driverMessage)
		assert.NotContains(t, err.Error(), envelope)
		assert.NotContains(t, err.Error(), "naenc:v1")
	})

	t.Run("status compare and swap", func(t *testing.T) {
		prepareTaskProviderResultURITest(t)
		task := &Task{
			TaskID: "task_write_cas_public",
			Status: TaskStatusInProgress,
			Data:   []byte(`{"done":false}`),
		}
		insertTask(t, task)
		task.EncryptedProviderResultURI = common.GetPointer(envelope)
		callbackName := "test:task-provider-result-cas-driver-error"
		require.NoError(t, DB.Callback().Update().Before("gorm:update").
			Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table == "tasks" {
					tx.AddError(&mysql.MySQLError{
						Number:  1062,
						Message: driverMessage,
					})
				}
			}))
		t.Cleanup(func() {
			require.NoError(t, DB.Callback().Update().Remove(callbackName))
		})

		won, err := task.UpdateWithStatus(TaskStatusInProgress)

		assert.False(t, won)
		require.EqualError(t, err, "mysql error 1062")
		assert.NotContains(t, err.Error(), driverMessage)
		assert.NotContains(t, err.Error(), envelope)
		assert.NotContains(t, err.Error(), "naenc:v1")
	})
}

func TestMigrateTaskProviderResultURISecretsEncryptsPlaintextIdempotently(
	t *testing.T,
) {
	prepareTaskProviderResultURITest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"false",
	)
	legacyURI := taskProviderResultURIForTest +
		"&credential_marker=" + taskProviderCredentialForTest
	task := insertRawTaskProviderResultURI(
		t,
		"task_provider_result_legacy_plaintext",
		legacyURI,
	)
	emptyTask := insertRawTaskProviderResultURI(
		t,
		"task_provider_result_empty",
		"",
	)
	nullTask := &Task{
		TaskID: "task_provider_result_null",
		Data:   []byte(`{"done":false}`),
	}
	insertTask(t, nullTask)

	var sqlLog bytes.Buffer
	previousDB := DB
	DB = DB.Session(&gorm.Session{
		Logger: gormlogger.New(
			log.New(&sqlLog, "", 0),
			gormlogger.Config{
				LogLevel:             gormlogger.Info,
				ParameterizedQueries: false,
				Colorful:             false,
			},
		),
	})
	t.Cleanup(func() {
		DB = previousDB
	})

	callbackName := fmt.Sprintf("test:no-plaintext-update-value:%s", t.Name())
	var sawProviderResultURIUpdate bool
	var plaintextUsedAsUpdateValue bool
	var plaintextBoundInStatement bool
	require.NoError(t, DB.Callback().Update().After("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table != "tasks" {
				return
			}
			for _, variable := range tx.Statement.Vars {
				value, ok := variable.(string)
				if !ok {
					continue
				}
				for _, forbidden := range []string{
					legacyURI,
					"provider-path-sentinel",
					"signed-query-sentinel",
					taskProviderCredentialForTest,
				} {
					if strings.Contains(value, forbidden) {
						plaintextBoundInStatement = true
					}
				}
			}
			updates, ok := tx.Statement.Dest.(map[string]any)
			if !ok {
				return
			}
			value, ok := updates["provider_result_uri"]
			if !ok {
				return
			}
			sawProviderResultURIUpdate = true
			if value == legacyURI {
				plaintextUsedAsUpdateValue = true
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(callbackName))
	})

	sqlLog.Reset()
	require.NoError(t, MigrateTaskProviderResultURISecrets())
	require.True(t, sawProviderResultURIUpdate)
	assert.False(t, plaintextUsedAsUpdateValue)
	assert.False(t, plaintextBoundInStatement)
	require.Contains(t, sqlLog.String(), "SELECT")
	for _, forbidden := range []string{
		legacyURI,
		"provider-path-sentinel",
		"signed-query-sentinel",
		taskProviderCredentialForTest,
	} {
		assert.NotContains(t, sqlLog.String(), forbidden)
	}
	first := rawTaskProviderResultURI(t, task.ID)
	require.True(t, first.Valid)
	assert.True(t, common.IsDataEncryptionEnvelope(first.String))
	for _, forbidden := range []string{
		legacyURI,
		"provider-path-sentinel",
		"signed-query-sentinel",
		taskProviderCredentialForTest,
	} {
		assert.NotContains(t, first.String, forbidden)
	}

	require.NoError(t, MigrateTaskProviderResultURISecrets())
	assert.Equal(t, first, rawTaskProviderResultURI(t, task.ID))
	assert.Equal(t, "", rawTaskProviderResultURI(t, emptyTask.ID).String)
	assert.False(t, rawTaskProviderResultURI(t, nullTask.ID).Valid)

	var loaded Task
	require.NoError(t, DB.First(&loaded, task.ID).Error)
	opened, err := loaded.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, legacyURI, opened)
}

func TestMigrateTaskProviderResultURISecretsRejectsOversizedLegacyPlaintextWithoutLeak(
	t *testing.T,
) {
	prepareTaskProviderResultURITest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"false",
	)
	oversizedURI := oversizedTaskProviderResultURIForTest()
	require.Len(
		t,
		[]byte(oversizedURI),
		geminitaskresult.MaxProviderResultURIBytes+1,
	)
	task := insertRawTaskProviderResultURI(
		t,
		"task_provider_result_oversized_legacy_plaintext",
		oversizedURI,
	)

	err := MigrateTaskProviderResultURISecrets()

	requireSafeTaskProviderResultURIError(
		t,
		err,
		oversizedURI,
		oversizedTaskProviderResultURISentinel,
	)
	assert.Contains(
		t,
		err.Error(),
		"plaintext exceeds "+
			strconv.Itoa(geminitaskresult.MaxProviderResultURIBytes)+" bytes",
	)
	stored := rawTaskProviderResultURI(t, task.ID)
	require.True(t, stored.Valid)
	assert.Equal(t, oversizedURI, stored.String)
}

func TestMigrateTaskProviderResultURISecretsRewrapsWithoutPayloadRewrite(
	t *testing.T,
) {
	prepareTaskProviderResultURITest(t)
	configureModelDataEncryption(
		t,
		"old="+modelTestDataEncryptionKey('a'),
		"old",
		"true",
	)
	before, err := common.SealDataEncryptionValueRequired(
		taskProviderResultURIDomain,
		taskProviderResultURIForTest,
	)
	require.NoError(t, err)
	task := insertRawTaskProviderResultURI(
		t,
		"task_provider_result_old_root_key",
		before,
	)
	beforeParts := strings.Split(before, ":")
	require.Len(t, beforeParts, 5)

	configureModelDataEncryption(
		t,
		"old="+modelTestDataEncryptionKey('a')+
			",new="+modelTestDataEncryptionKey('b'),
		"new",
		"true",
	)
	require.NoError(t, MigrateTaskProviderResultURISecrets())
	after := rawTaskProviderResultURI(t, task.ID)
	require.True(t, after.Valid)
	afterParts := strings.Split(after.String, ":")
	require.Len(t, afterParts, 5)
	assert.Equal(t, "new", afterParts[2])
	assert.NotEqual(t, beforeParts[3], afterParts[3])
	assert.Equal(t, beforeParts[4], afterParts[4])

	plaintext, info, err := common.OpenDataEncryptionValue(
		taskProviderResultURIDomain,
		after.String,
	)
	require.NoError(t, err)
	assert.True(t, info.Encrypted)
	assert.Equal(t, "new", info.KeyID)
	assert.Equal(t, taskProviderResultURIForTest, plaintext)
}

func TestMigrateTaskProviderResultURISecretsUsesOneLockedTransaction(
	t *testing.T,
) {
	prepareTaskProviderResultURITest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	task := insertRawTaskProviderResultURI(
		t,
		"task_provider_result_locked_migration",
		taskProviderResultURIForTest,
	)

	var readPool any
	var writePool any
	queryCallback := fmt.Sprintf("test:capture-task-result-read-tx:%s", t.Name())
	updateCallback := fmt.Sprintf("test:capture-task-result-write-tx:%s", t.Name())
	require.NoError(t, DB.Callback().Row().Before("gorm:row").
		Register(queryCallback, func(tx *gorm.DB) {
			if readPool == nil && tx.Statement.Table == "tasks" {
				readPool = tx.Statement.ConnPool
			}
		}))
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			if writePool == nil && tx.Statement.Table == "tasks" {
				writePool = tx.Statement.ConnPool
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Row().Remove(queryCallback))
		require.NoError(t, DB.Callback().Update().Remove(updateCallback))
	})

	require.NoError(t, MigrateTaskProviderResultURISecrets())
	require.NotNil(t, readPool)
	require.NotNil(t, writePool)
	assert.Equal(t, readPool, writePool)
	_, readUsesTransaction := readPool.(gorm.TxCommitter)
	_, writeUsesTransaction := writePool.(gorm.TxCommitter)
	assert.True(t, readUsesTransaction)
	assert.True(t, writeUsesTransaction)

	var loaded Task
	require.NoError(t, DB.First(&loaded, task.ID).Error)
	opened, err := loaded.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, taskProviderResultURIForTest, opened)
}

func TestMigrateTaskProviderResultURISecretsDoesNotOverwriteConcurrentWrite(
	t *testing.T,
) {
	useFileBackedTaskProviderResultURITestDB(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	staleURI := taskProviderResultURIForTest + "&version=stale-uri-sentinel"
	currentURI := taskProviderResultURIForTest + "&version=current-uri-sentinel"
	task := insertRawTaskProviderResultURI(
		t,
		"task_provider_result_concurrent_write",
		staleURI,
	)

	readLocked := make(chan struct{})
	writerDone := make(chan struct{})
	var writerErr error
	var signalRead sync.Once
	queryCallback := fmt.Sprintf("test:pause-after-task-result-read:%s", t.Name())
	updateCallback := fmt.Sprintf("test:wait-for-task-result-write:%s", t.Name())
	require.NoError(t, DB.Callback().Row().After("gorm:row").
		Register(queryCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == "tasks" {
				signalRead.Do(func() { close(readLocked) })
			}
		}))
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == "tasks" {
				<-writerDone
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Row().Remove(queryCallback))
		require.NoError(t, DB.Callback().Update().Remove(updateCallback))
	})

	go func() {
		<-readLocked
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		writerErr = DB.WithContext(ctx).Exec(
			"UPDATE tasks SET provider_result_uri = ? WHERE id = ?",
			currentURI,
			task.ID,
		).Error
		close(writerDone)
	}()

	migrationErr := MigrateTaskProviderResultURISecrets()
	<-writerDone
	require.NoError(t, writerErr)
	requireSafeTaskProviderResultURIError(
		t,
		migrationErr,
		staleURI,
		currentURI,
		"provider-path-sentinel",
		"signed-query-sentinel",
		"stale-uri-sentinel",
		"current-uri-sentinel",
	)
	stored := rawTaskProviderResultURI(t, task.ID)
	require.True(t, stored.Valid)
	assert.Equal(t, currentURI, stored.String)

	require.NoError(t, MigrateTaskProviderResultURISecrets())
	stored = rawTaskProviderResultURI(t, task.ID)
	require.True(t, stored.Valid)
	assert.True(t, common.IsDataEncryptionEnvelope(stored.String))
	plaintext, _, err := common.OpenDataEncryptionValue(
		taskProviderResultURIDomain,
		stored.String,
	)
	require.NoError(t, err)
	assert.Equal(t, currentURI, plaintext)
}

func TestValidateTaskProviderResultURISecretsRejectsPlaintextWhenPreparationModeIsDisabled(
	t *testing.T,
) {
	prepareTaskProviderResultURITest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	plaintextURI := taskProviderResultURIForTest + "&mode=disabled-mode-sentinel"
	insertRawTaskProviderResultURI(
		t,
		"task_provider_result_plaintext_preparation_mode",
		plaintextURI,
	)

	err := validateTaskProviderResultURISecrets()

	requireSafeTaskProviderResultURIError(
		t,
		err,
		plaintextURI,
		"provider-path-sentinel",
		"signed-query-sentinel",
		"disabled-mode-sentinel",
	)
	assert.Contains(t, err.Error(), taskProviderResultURIDomain)
}

func TestValidateTaskProviderResultURISecretsRejectsPlaintextWhenPreparationModeIsEnabled(
	t *testing.T,
) {
	prepareTaskProviderResultURITest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"false",
	)
	plaintextURI := taskProviderResultURIForTest + "&mode=enabled-mode-sentinel"
	insertRawTaskProviderResultURI(
		t,
		"task_provider_result_plaintext_enforcement_mode",
		plaintextURI,
	)

	err := validateTaskProviderResultURISecrets()

	requireSafeTaskProviderResultURIError(
		t,
		err,
		plaintextURI,
		"provider-path-sentinel",
		"signed-query-sentinel",
		"enabled-mode-sentinel",
	)
	assert.Contains(t, err.Error(), taskProviderResultURIDomain)
}

func TestValidateTaskProviderResultURISecretsRejectsOversizedEnvelopeWithoutLeak(
	t *testing.T,
) {
	prepareTaskProviderResultURITest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	oversizedURI := oversizedTaskProviderResultURIForTest()
	require.Len(
		t,
		[]byte(oversizedURI),
		geminitaskresult.MaxProviderResultURIBytes+1,
	)
	envelope, err := common.SealDataEncryptionValueRequired(
		taskProviderResultURIDomain,
		oversizedURI,
	)
	require.NoError(t, err)
	task := insertRawTaskProviderResultURI(
		t,
		"task_provider_result_oversized_envelope",
		envelope,
	)

	err = validateTaskProviderResultURISecrets()

	requireSafeTaskProviderResultURIError(
		t,
		err,
		oversizedURI,
		envelope,
		oversizedTaskProviderResultURISentinel,
	)
	assert.Contains(
		t,
		err.Error(),
		"plaintext exceeds "+
			strconv.Itoa(geminitaskresult.MaxProviderResultURIBytes)+" bytes",
	)
	stored := rawTaskProviderResultURI(t, task.ID)
	require.True(t, stored.Valid)
	assert.Equal(t, envelope, stored.String)
}

func TestValidateTaskProviderResultURISecretsRejectsCorruptEnvelopeWithoutLeak(
	t *testing.T,
) {
	prepareTaskProviderResultURITest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const corruptEnvelope = "naenc:v1:k1:" +
		"corrupt-wrapped-key-sentinel:corrupt-payload-sentinel"
	insertRawTaskProviderResultURI(
		t,
		"task_provider_result_corrupt_envelope",
		corruptEnvelope,
	)

	err := validateTaskProviderResultURISecrets()

	requireSafeTaskProviderResultURIError(
		t,
		err,
		corruptEnvelope,
		"corrupt-wrapped-key-sentinel",
		"corrupt-payload-sentinel",
	)
	assert.Contains(t, err.Error(), taskProviderResultURIDomain)
}
