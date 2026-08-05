package model

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	geminiMigrationFirstKey        = "gemini-migration-first-key-sentinel"
	geminiMigrationSecondKey       = "gemini-migration-selected-key-sentinel"
	geminiMigrationRemovedKey      = "gemini-migration-removed-key-sentinel"
	geminiMigrationSignedQuery     = "gemini-migration-signed-query-sentinel"
	geminiMigrationProviderPath    = "gemini-migration-provider-path-sentinel"
	geminiMigrationOperation       = "gemini-migration-operation-sentinel"
	geminiMigrationProviderMessage = "gemini-migration-provider-message-sentinel"
	geminiMigrationLegacyResult    = "gemini-migration-legacy-result-sentinel"
	geminiStartupOldKeyID          = "old"
	geminiStartupActiveKeyID       = "active"
)

var geminiTaskResultPrivacyStartupMigrationMu sync.Mutex

type geminiTaskResultPrivacyColumns struct {
	Data              sql.NullString `gorm:"column:data"`
	PrivateData       sql.NullString `gorm:"column:private_data"`
	FailReason        string         `gorm:"column:fail_reason"`
	ProviderResultURI sql.NullString `gorm:"column:provider_result_uri"`
}

func geminiMigrationLegacyCaseVariant(mask int) string {
	value := []byte("gemini")
	for index := range value {
		if mask&(1<<index) != 0 {
			value[index] -= 'a' - 'A'
		}
	}
	return string(value)
}

func prepareGeminiTaskResultPrivacyMigrationTest(t *testing.T) {
	t.Helper()

	truncateTables(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Task{}))
	require.NoError(t, DB.Exec("DELETE FROM tasks").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)

	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://public.example.test/"
	t.Cleanup(func() {
		system_setting.ServerAddress = previousServerAddress
	})
}

func prepareGeminiTaskResultPrivacyStartupMigrationTest(t *testing.T) {
	t.Helper()

	geminiTaskResultPrivacyStartupMigrationMu.Lock()
	t.Cleanup(func() {
		geminiTaskResultPrivacyStartupMigrationMu.Unlock()
	})

	previousDB := DB
	previousLogDB := LOG_DB
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	dsn := "file:" +
		filepath.Join(t.TempDir(), "gemini-startup-migration.db") +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	DB = db
	LOG_DB = db
	common.SetDatabaseTypes(
		common.DatabaseTypeSQLite,
		common.DatabaseTypeSQLite,
	)
	initCol()
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Task{}))
	t.Cleanup(func() {
		DB = previousDB
		LOG_DB = previousLogDB
		common.SetDatabaseTypes(
			previousMainDatabaseType,
			previousLogDatabaseType,
		)
		initCol()
		require.NoError(t, sqlDB.Close())
	})

	configureModelDataEncryption(
		t,
		geminiStartupOldKeyID+"="+modelTestDataEncryptionKey('a'),
		geminiStartupOldKeyID,
		"true",
	)
	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://public.example.test/"
	t.Cleanup(func() {
		system_setting.ServerAddress = previousServerAddress
	})
}

func insertGeminiMigrationChannel(
	t *testing.T,
	keys ...string,
) *Channel {
	t.Helper()

	channel := &Channel{
		Type:   constant.ChannelTypeGemini,
		Key:    strings.Join(keys, "\n"),
		Name:   "gemini-result-migration-channel",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey: len(keys) > 1,
		},
	}
	require.NoError(t, DB.Create(channel).Error)
	return channel
}

func insertLegacyGeminiResultTask(
	t *testing.T,
	channelID int,
	selectedKey string,
	taskID string,
	platform constant.TaskPlatform,
	status TaskStatus,
	rawData []byte,
) *Task {
	t.Helper()

	task := InitTask(platform, &relaycommon.RelayInfo{
		UserId:     8301,
		UsingGroup: "default",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:   channelID,
			ChannelType: constant.ChannelTypeGemini,
			ApiKey:      selectedKey,
		},
	})
	task.TaskID = taskID
	task.Status = status
	task.Progress = "100%"
	task.PrivateData.ResultURL = "https://legacy.example.test/" +
		geminiMigrationLegacyResult
	task.FailReason = geminiMigrationProviderMessage
	task.Data = geminitaskresult.EmptyPublicProjection(false)
	insertTask(t, task)
	require.NoError(t, DB.Model(&Task{}).
		Where("id = ?", task.ID).
		UpdateColumn("data", rawData).Error)
	return task
}

func rawGeminiTaskResultPrivacyColumns(
	t *testing.T,
	taskID int64,
) geminiTaskResultPrivacyColumns {
	t.Helper()

	var columns geminiTaskResultPrivacyColumns
	require.NoError(t, DB.Model(&Task{}).
		Select("data", "private_data", "fail_reason", "provider_result_uri").
		Where("id = ?", taskID).
		Scan(&columns).Error)
	return columns
}

func geminiMigrationProviderURI(credential string) string {
	return "https://video.example.test/" +
		geminiMigrationProviderPath +
		"?key=" + credential +
		"&sig=" + geminiMigrationSignedQuery +
		"&keep=1"
}

func geminiMigrationFilteredProviderURI() string {
	return "https://video.example.test/" +
		geminiMigrationProviderPath +
		"?sig=" + geminiMigrationSignedQuery +
		"&keep=1"
}

func geminiMigrationResultBody(
	t *testing.T,
	shape string,
	providerURI string,
) []byte {
	t.Helper()

	var body map[string]any
	switch shape {
	case "generatedSamples":
		body = map[string]any{
			"name": geminiMigrationOperation,
			"done": true,
			"response": map[string]any{
				"generateVideoResponse": map[string]any{
					"generatedSamples": []any{
						map[string]any{
							"video": map[string]any{
								"uri":      providerURI,
								"mimeType": "video/mp4",
							},
						},
					},
				},
			},
		}
	case "generatedVideos":
		body = map[string]any{
			"name": geminiMigrationOperation,
			"done": true,
			"response": map[string]any{
				"generateVideoResponse": map[string]any{
					"generatedVideos": []any{
						map[string]any{
							"video": map[string]any{
								"uri":      providerURI,
								"mimeType": "video/mp4",
							},
						},
					},
				},
			},
		}
	case "response.videos":
		body = map[string]any{
			"name": geminiMigrationOperation,
			"done": true,
			"response": map[string]any{
				"videos": []any{
					map[string]any{
						"uri":      providerURI,
						"mimeType": "video/mp4",
					},
				},
			},
		}
	case "response.video":
		body = map[string]any{
			"name": geminiMigrationOperation,
			"done": true,
			"response": map[string]any{
				"video":    providerURI,
				"mimeType": "video/mp4",
			},
		}
	case "response.uri":
		body = map[string]any{
			"name": geminiMigrationOperation,
			"done": true,
			"response": map[string]any{
				"uri":      providerURI,
				"mimeType": "video/mp4",
			},
		}
	case "top-level uri":
		body = map[string]any{
			"name":     geminiMigrationOperation,
			"done":     true,
			"uri":      providerURI,
			"mimeType": "video/mp4",
		}
	case "provider error":
		body = map[string]any{
			"name": geminiMigrationOperation,
			"done": true,
			"error": map[string]any{
				"code":    13,
				"status":  "INTERNAL",
				"message": geminiMigrationProviderMessage,
			},
		}
	default:
		t.Fatalf("unsupported Gemini migration test shape %q", shape)
	}

	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	return encoded
}

func requireGeminiMigrationStorageOmitsSentinels(
	t *testing.T,
	columns geminiTaskResultPrivacyColumns,
) {
	t.Helper()

	combined := columns.Data.String +
		columns.PrivateData.String +
		columns.FailReason +
		columns.ProviderResultURI.String
	for _, forbidden := range []string{
		geminiMigrationFirstKey,
		geminiMigrationSecondKey,
		geminiMigrationRemovedKey,
		geminiMigrationSignedQuery,
		geminiMigrationProviderPath,
		geminiMigrationOperation,
		geminiMigrationProviderMessage,
		geminiMigrationLegacyResult,
	} {
		assert.NotContains(t, combined, forbidden)
	}
}

func requireGeminiStartupMigrationEntryPointResult(
	t *testing.T,
	task *Task,
) {
	t.Helper()

	columns := rawGeminiTaskResultPrivacyColumns(t, task.ID)
	require.True(t, columns.Data.Valid)
	requireGeminiMigrationStorageOmitsSentinels(t, columns)
	assert.JSONEq(
		t,
		`{"done":true,"video":{"url":"`+
			geminitaskresult.ProxyPath(task.TaskID)+
			`","mime_type":"video/mp4"}}`,
		columns.Data.String,
	)
	require.True(t, columns.ProviderResultURI.Valid)
	assert.True(
		t,
		common.IsDataEncryptionEnvelope(columns.ProviderResultURI.String),
	)

	var loaded Task
	require.NoError(t, DB.First(&loaded, task.ID).Error)
	opened, err := loaded.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, geminiMigrationFilteredProviderURI(), opened)
	assert.Equal(
		t,
		"https://public.example.test"+
			geminitaskresult.ProxyPath(task.TaskID),
		loaded.PrivateData.ResultURL,
	)
	assert.Empty(t, loaded.FailReason)
}

type geminiStartupMigrationFixture struct {
	channel                *Channel
	rawResultTask          *Task
	plaintextProviderTask  *Task
	plaintextProviderURI   string
	expectedCredentialHash string
}

type geminiStartupPrerequisiteObservation struct {
	sawBackfill              bool
	observedRows             int
	providerResultsRewrapped bool
	privateKeysRemoved       bool
	fingerprintsReady        bool
}

func insertGeminiStartupMigrationFixture(
	t *testing.T,
	taskIDSuffix string,
) geminiStartupMigrationFixture {
	t.Helper()

	channel := insertGeminiMigrationChannel(
		t,
		geminiMigrationFirstKey,
		geminiMigrationSecondKey,
	)
	rawResultTask := insertLegacyGeminiResultTask(
		t,
		channel.Id,
		geminiMigrationSecondKey,
		"task_gemini_"+taskIDSuffix+"_raw_result",
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		TaskStatusSuccess,
		geminiMigrationResultBody(
			t,
			"generatedSamples",
			geminiMigrationProviderURI(geminiMigrationSecondKey),
		),
	)
	oldEnvelope, err := common.SealDataEncryptionValueRequired(
		taskProviderResultURIDomain,
		geminiMigrationFilteredProviderURI(),
	)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Task{}).
		Where("id = ?", rawResultTask.ID).
		UpdateColumn("provider_result_uri", oldEnvelope).Error)

	plaintextProviderTask := InitTask(
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		&relaycommon.RelayInfo{
			UserId:     8302,
			UsingGroup: "default",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId:   channel.Id,
				ChannelType: constant.ChannelTypeGemini,
				ApiKey:      geminiMigrationSecondKey,
			},
		},
	)
	plaintextProviderTask.TaskID =
		"task_gemini_" + taskIDSuffix + "_plaintext_provider"
	plaintextProviderTask.Status = TaskStatusInProgress
	plaintextProviderTask.Progress = "50%"
	plaintextProviderTask.Data = geminitaskresult.EmptyPublicProjection(false)
	insertTask(t, plaintextProviderTask)

	legacyPrivateData, err := common.Marshal(map[string]string{
		"key":              geminiMigrationSecondKey,
		"upstream_task_id": "upstream-" + taskIDSuffix,
	})
	require.NoError(t, err)
	for _, task := range []*Task{rawResultTask, plaintextProviderTask} {
		require.NoError(t, DB.Model(&Task{}).
			Where("id = ?", task.ID).
			UpdateColumn("private_data", legacyPrivateData).Error)
	}

	plaintextProviderURI :=
		geminiMigrationFilteredProviderURI() + "&plaintext=1"
	require.NoError(t, DB.Model(&Task{}).
		Where("id = ?", plaintextProviderTask.ID).
		UpdateColumn("provider_result_uri", plaintextProviderURI).Error)

	configureModelDataEncryption(
		t,
		geminiStartupOldKeyID+"="+modelTestDataEncryptionKey('a')+
			","+geminiStartupActiveKeyID+"="+
			modelTestDataEncryptionKey('b'),
		geminiStartupActiveKeyID,
		"true",
	)

	return geminiStartupMigrationFixture{
		channel:               channel,
		rawResultTask:         rawResultTask,
		plaintextProviderTask: plaintextProviderTask,
		plaintextProviderURI:  plaintextProviderURI,
		expectedCredentialHash: taskChannelKeyFingerprint(
			geminiMigrationSecondKey,
		),
	}
}

func observeGeminiStartupPrerequisitesBeforeBackfill(
	t *testing.T,
	fixture geminiStartupMigrationFixture,
) *geminiStartupPrerequisiteObservation {
	t.Helper()

	observation := &geminiStartupPrerequisiteObservation{}
	expectedFingerprints := map[int64]string{
		fixture.rawResultTask.ID:         fixture.expectedCredentialHash,
		fixture.plaintextProviderTask.ID: fixture.expectedCredentialHash,
	}
	callbackName := fmt.Sprintf(
		"test:observe-gemini-startup-prerequisites:%s",
		t.Name(),
	)
	require.NoError(t, DB.Callback().Query().After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if observation.sawBackfill || tx.Statement.Table != "tasks" {
				return
			}
			limitClause, ok := tx.Statement.Clauses["LIMIT"]
			if !ok {
				return
			}
			limit, ok := limitClause.Expression.(clause.Limit)
			if !ok || limit.Limit == nil ||
				*limit.Limit != geminiTaskResultPrivacyBatchSize {
				return
			}
			tasks, ok := tx.Statement.Dest.(*[]Task)
			if !ok {
				return
			}

			observation.providerResultsRewrapped = true
			observation.privateKeysRemoved = true
			observation.fingerprintsReady = true
			for index := range *tasks {
				task := &(*tasks)[index]
				expectedFingerprint, tracked :=
					expectedFingerprints[task.ID]
				if !tracked {
					continue
				}
				observation.observedRows++
				if task.EncryptedProviderResultURI == nil {
					observation.providerResultsRewrapped = false
				} else {
					_, info, err := common.OpenDataEncryptionValue(
						taskProviderResultURIDomain,
						*task.EncryptedProviderResultURI,
					)
					if err != nil || !info.Encrypted ||
						info.KeyID != geminiStartupActiveKeyID {
						observation.providerResultsRewrapped = false
					}
				}
				if task.PrivateData.Key != "" {
					observation.privateKeysRemoved = false
				}
				if task.PrivateData.ChannelKeyFingerprint !=
					expectedFingerprint {
					observation.fingerprintsReady = false
				}
			}
			if observation.observedRows > 0 {
				observation.sawBackfill = true
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Query().Remove(callbackName))
	})
	return observation
}

func requireGeminiStartupMigrationFixtureResult(
	t *testing.T,
	fixture geminiStartupMigrationFixture,
	observation *geminiStartupPrerequisiteObservation,
) {
	t.Helper()

	require.True(t, observation.sawBackfill)
	assert.Equal(t, 2, observation.observedRows)
	assert.True(t, observation.providerResultsRewrapped)
	assert.True(t, observation.privateKeysRemoved)
	assert.True(t, observation.fingerprintsReady)
	requireGeminiStartupMigrationEntryPointResult(t, fixture.rawResultTask)

	columns := rawGeminiTaskResultPrivacyColumns(
		t,
		fixture.plaintextProviderTask.ID,
	)
	require.True(t, columns.ProviderResultURI.Valid)
	_, info, err := common.OpenDataEncryptionValue(
		taskProviderResultURIDomain,
		columns.ProviderResultURI.String,
	)
	require.NoError(t, err)
	assert.True(t, info.Encrypted)
	assert.Equal(t, geminiStartupActiveKeyID, info.KeyID)
	assert.NotContains(t, columns.PrivateData.String, `"key"`)
	assert.NotContains(
		t,
		columns.PrivateData.String,
		geminiMigrationSecondKey,
	)
	assert.JSONEq(t, `{"done":false}`, columns.Data.String)

	for _, task := range []*Task{
		fixture.rawResultTask,
		fixture.plaintextProviderTask,
	} {
		var loaded Task
		require.NoError(t, DB.First(&loaded, task.ID).Error)
		assert.Empty(t, loaded.PrivateData.Key)
		assert.Equal(
			t,
			fixture.expectedCredentialHash,
			loaded.PrivateData.ChannelKeyFingerprint,
		)
		resolved, err := ResolveTaskChannelKey(fixture.channel, &loaded)
		require.NoError(t, err)
		assert.Equal(t, geminiMigrationSecondKey, resolved)
	}

	var loadedPlaintextProviderTask Task
	require.NoError(t, DB.First(
		&loadedPlaintextProviderTask,
		fixture.plaintextProviderTask.ID,
	).Error)
	opened, err := loadedPlaintextProviderTask.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, fixture.plaintextProviderURI, opened)
}

func TestNormalMigrationRunsGeminiTaskResultPrivacyBeforeValidation(
	t *testing.T,
) {
	prepareGeminiTaskResultPrivacyStartupMigrationTest(t)
	fixture := insertGeminiStartupMigrationFixture(
		t,
		"normal_startup_migration",
	)
	observation := observeGeminiStartupPrerequisitesBeforeBackfill(
		t,
		fixture,
	)

	require.NoError(t, migrateDB())

	requireGeminiStartupMigrationFixtureResult(t, fixture, observation)
}

func TestFastMigrationRunsGeminiTaskResultPrivacyBeforeValidation(
	t *testing.T,
) {
	prepareGeminiTaskResultPrivacyStartupMigrationTest(t)
	fixture := insertGeminiStartupMigrationFixture(
		t,
		"fast_startup_migration",
	)
	observation := observeGeminiStartupPrerequisitesBeforeBackfill(
		t,
		fixture,
	)

	require.NoError(t, migrateDBFast())

	requireGeminiStartupMigrationFixtureResult(t, fixture, observation)
}

func requireMigrationEntryPointRejectsPostBackfillDrift(
	t *testing.T,
	taskIDSuffix string,
	migrate func() error,
) {
	t.Helper()

	prepareGeminiTaskResultPrivacyStartupMigrationTest(t)
	fixture := insertGeminiStartupMigrationFixture(t, taskIDSuffix)
	const driftSentinel = "post-backfill-validation-drift-sentinel"
	driftData := []byte(
		`{"done":false,"validation_drift":"` + driftSentinel + `"}`,
	)
	var (
		injected     bool
		injectionErr error
	)
	callbackName := fmt.Sprintf(
		"test:inject-gemini-post-backfill-drift:%s",
		t.Name(),
	)
	require.NoError(t, DB.Callback().Update().After("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if injected || tx.Statement.Table != "tasks" {
				return
			}
			updates, ok := tx.Statement.Dest.(map[string]any)
			if !ok {
				return
			}
			if _, updatesData := updates["data"]; !updatesData {
				return
			}
			injected = true
			_, injectionErr = tx.Statement.ConnPool.ExecContext(
				tx.Statement.Context,
				"UPDATE tasks SET data = ? WHERE id = ?",
				driftData,
				fixture.rawResultTask.ID,
			)
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(callbackName))
	})

	err := migrate()

	require.True(t, injected)
	require.NoError(t, injectionErr)
	require.Error(t, err)
	assert.Contains(
		t,
		err.Error(),
		fmt.Sprintf("task %d", fixture.rawResultTask.ID),
	)
	assert.Contains(t, err.Error(), "public data is not canonical")
	assert.NotContains(t, err.Error(), driftSentinel)
	assert.NotContains(t, err.Error(), geminiMigrationSecondKey)
}

func TestNormalMigrationValidationRejectsPostBackfillDrift(t *testing.T) {
	requireMigrationEntryPointRejectsPostBackfillDrift(
		t,
		"normal_validation",
		migrateDB,
	)
}

func TestFastMigrationValidationRejectsPostBackfillDrift(t *testing.T) {
	requireMigrationEntryPointRejectsPostBackfillDrift(
		t,
		"fast_validation",
		migrateDBFast,
	)
}

func TestValidateServingStorageOnStartupChecksReplicaAfterReversibleSecrets(
	t *testing.T,
) {
	prepareGeminiTaskResultPrivacyStartupMigrationTest(t)
	require.NoError(t, DB.AutoMigrate(
		&CustomOAuthProvider{},
		&Option{},
		&TwoFA{},
		&User{},
	))
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() {
		common.IsMasterNode = previousMaster
	})

	channel := insertGeminiMigrationChannel(
		t,
		geminiMigrationFirstKey,
		geminiMigrationSecondKey,
	)
	task := insertLegacyGeminiResultTask(
		t,
		channel.Id,
		geminiMigrationSecondKey,
		"task_gemini_replica_startup_validation",
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		TaskStatusSuccess,
		geminiMigrationResultBody(
			t,
			"generatedSamples",
			geminiMigrationProviderURI(geminiMigrationSecondKey),
		),
	)
	legacyProviderURI := geminiMigrationFilteredProviderURI()
	require.NoError(t, DB.Model(&Task{}).
		Where("id = ?", task.ID).
		UpdateColumn("provider_result_uri", legacyProviderURI).Error)

	err := ValidateServingStorageOnStartup()

	require.Error(t, err)
	assert.Contains(t, err.Error(), taskProviderResultURIDomain)
	assert.NotContains(t, err.Error(), "public data is not canonical")
	assert.NotContains(t, err.Error(), legacyProviderURI)
	require.NoError(t, MigrateTaskProviderResultURISecrets())

	err = ValidateServingStorageOnStartup()

	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("task %d", task.ID))
	assert.Contains(t, err.Error(), "public data is not canonical")
	for _, sentinel := range []string{
		geminiMigrationSecondKey,
		geminiMigrationSignedQuery,
		geminiMigrationProviderPath,
		geminiMigrationOperation,
		geminiMigrationProviderMessage,
	} {
		assert.NotContains(t, err.Error(), sentinel)
	}
}

func TestMigrateGeminiTaskResultPrivacySanitizesLegacyShapes(t *testing.T) {
	prepareGeminiTaskResultPrivacyMigrationTest(t)
	channel := insertGeminiMigrationChannel(
		t,
		geminiMigrationFirstKey,
		geminiMigrationSecondKey,
	)
	providerURI := geminiMigrationProviderURI(geminiMigrationSecondKey)
	shapes := []struct {
		name     string
		raw      []byte
		status   TaskStatus
		hasVideo bool
		failed   bool
	}{
		{
			name:     "generatedSamples",
			raw:      geminiMigrationResultBody(t, "generatedSamples", providerURI),
			status:   TaskStatusSuccess,
			hasVideo: true,
		},
		{
			name:     "generatedVideos",
			raw:      geminiMigrationResultBody(t, "generatedVideos", providerURI),
			status:   TaskStatusSuccess,
			hasVideo: true,
		},
		{
			name:     "response.videos",
			raw:      geminiMigrationResultBody(t, "response.videos", providerURI),
			status:   TaskStatusSuccess,
			hasVideo: true,
		},
		{
			name:     "response.video",
			raw:      geminiMigrationResultBody(t, "response.video", providerURI),
			status:   TaskStatusSuccess,
			hasVideo: true,
		},
		{
			name:     "response.uri",
			raw:      geminiMigrationResultBody(t, "response.uri", providerURI),
			status:   TaskStatusSuccess,
			hasVideo: true,
		},
		{
			name:     "top-level uri",
			raw:      geminiMigrationResultBody(t, "top-level uri", providerURI),
			status:   TaskStatusSuccess,
			hasVideo: true,
		},
		{
			name:   "malformed JSON",
			raw:    []byte(`{"done":true,"uri":"malformed-json-sentinel"`),
			status: TaskStatusInProgress,
		},
		{
			name:   "provider error message",
			raw:    geminiMigrationResultBody(t, "provider error", providerURI),
			status: TaskStatusFailure,
			failed: true,
		},
	}

	tasks := make(map[string]*Task, len(shapes))
	for index, shape := range shapes {
		platform := constant.TaskPlatform(
			strconv.Itoa(constant.ChannelTypeGemini),
		)
		if index%2 == 1 {
			platform = constant.TaskPlatform("gemini")
		}
		taskID := "task_gemini_migration_" +
			strings.NewReplacer(" ", "_", ".", "_").Replace(shape.name)
		tasks[shape.name] = insertLegacyGeminiResultTask(
			t,
			channel.Id,
			geminiMigrationSecondKey,
			taskID,
			platform,
			shape.status,
			shape.raw,
		)
	}

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())

	for _, shape := range shapes {
		task := tasks[shape.name]
		columns := rawGeminiTaskResultPrivacyColumns(t, task.ID)
		require.True(t, columns.Data.Valid)
		requireGeminiMigrationStorageOmitsSentinels(t, columns)

		var loaded Task
		require.NoError(t, DB.First(&loaded, task.ID).Error)
		if shape.hasVideo {
			expectedPublic, err := common.Marshal(map[string]any{
				"done": true,
				"video": map[string]any{
					"url":       geminitaskresult.ProxyPath(task.TaskID),
					"mime_type": "video/mp4",
				},
			})
			require.NoError(t, err)
			assert.JSONEq(t, string(expectedPublic), columns.Data.String)
			require.True(t, columns.ProviderResultURI.Valid)
			assert.True(
				t,
				common.IsDataEncryptionEnvelope(columns.ProviderResultURI.String),
			)
			opened, err := loaded.OpenProviderResultURI()
			require.NoError(t, err)
			assert.Equal(t, geminiMigrationFilteredProviderURI(), opened)
			assert.Equal(
				t,
				"https://public.example.test"+
					geminitaskresult.ProxyPath(task.TaskID),
				loaded.PrivateData.ResultURL,
			)
			assert.Empty(t, loaded.FailReason)
			continue
		}

		assert.False(t, columns.ProviderResultURI.Valid)
		assert.Empty(t, loaded.PrivateData.ResultURL)
		if shape.failed {
			assert.Equal(t, "upstream task failed", loaded.FailReason)
			assert.JSONEq(
				t,
				`{"done":true,"error":{"code":13,"status":"INTERNAL"}}`,
				columns.Data.String,
			)
		} else {
			assert.Empty(t, loaded.FailReason)
			assert.Equal(
				t,
				string(geminitaskresult.EmptyPublicProjection(false)),
				columns.Data.String,
			)
		}
	}
}

func TestMigrateGeminiTaskResultPrivacyNeverSubstitutesRemovedKey(t *testing.T) {
	prepareGeminiTaskResultPrivacyMigrationTest(t)
	channel := insertGeminiMigrationChannel(
		t,
		geminiMigrationFirstKey,
		geminiMigrationSecondKey,
	)
	task := insertLegacyGeminiResultTask(
		t,
		channel.Id,
		geminiMigrationRemovedKey,
		"task_gemini_removed_key",
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		TaskStatusSuccess,
		geminiMigrationResultBody(
			t,
			"generatedSamples",
			geminiMigrationProviderURI(geminiMigrationRemovedKey),
		),
	)
	changed, err := task.SetProviderResultURI(
		geminiMigrationProviderURI(geminiMigrationFirstKey),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, DB.Model(&Task{}).
		Where("id = ?", task.ID).
		UpdateColumn(
			"provider_result_uri",
			task.EncryptedProviderResultURI,
		).Error)

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())

	columns := rawGeminiTaskResultPrivacyColumns(t, task.ID)
	requireGeminiMigrationStorageOmitsSentinels(t, columns)
	assert.False(t, columns.ProviderResultURI.Valid)
	assert.JSONEq(
		t,
		`{"done":true,"video":{"url":"`+
			geminitaskresult.ProxyPath(task.TaskID)+
			`","mime_type":"video/mp4"}}`,
		columns.Data.String,
	)

	var loaded Task
	require.NoError(t, DB.First(&loaded, task.ID).Error)
	opened, err := loaded.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Empty(t, opened)
	assert.Equal(
		t,
		"https://public.example.test"+geminitaskresult.ProxyPath(task.TaskID),
		loaded.PrivateData.ResultURL,
	)
}

func TestMigrateGeminiTaskResultPrivacySanitizesMissingChannelRows(t *testing.T) {
	prepareGeminiTaskResultPrivacyMigrationTest(t)
	task := insertLegacyGeminiResultTask(
		t,
		982341,
		geminiMigrationSecondKey,
		"task_gemini_missing_channel",
		constant.TaskPlatform("gemini"),
		TaskStatusSuccess,
		geminiMigrationResultBody(
			t,
			"generatedVideos",
			geminiMigrationProviderURI(geminiMigrationSecondKey),
		),
	)

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())

	columns := rawGeminiTaskResultPrivacyColumns(t, task.ID)
	requireGeminiMigrationStorageOmitsSentinels(t, columns)
	assert.False(t, columns.ProviderResultURI.Valid)
	assert.JSONEq(
		t,
		`{"done":true,"video":{"url":"`+
			geminitaskresult.ProxyPath(task.TaskID)+
			`","mime_type":"video/mp4"}}`,
		columns.Data.String,
	)
}

func TestMigrateGeminiTaskResultPrivacyIsIdempotent(t *testing.T) {
	prepareGeminiTaskResultPrivacyMigrationTest(t)
	channel := insertGeminiMigrationChannel(
		t,
		geminiMigrationFirstKey,
		geminiMigrationSecondKey,
	)
	task := insertLegacyGeminiResultTask(
		t,
		channel.Id,
		geminiMigrationSecondKey,
		"task_gemini_idempotent",
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		TaskStatusSuccess,
		geminiMigrationResultBody(
			t,
			"generatedSamples",
			geminiMigrationProviderURI(geminiMigrationSecondKey),
		),
	)

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	first := rawGeminiTaskResultPrivacyColumns(t, task.ID)
	require.True(t, first.ProviderResultURI.Valid)

	secondPassUpdates := 0
	updateCallback := fmt.Sprintf(
		"test:capture-gemini-idempotent-second-pass:%s",
		t.Name(),
	)
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == "tasks" {
				secondPassUpdates++
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(updateCallback))
	})

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	second := rawGeminiTaskResultPrivacyColumns(t, task.ID)

	assert.Equal(t, first, second)
	assert.Zero(t, secondPassUpdates)
	require.NoError(t, ValidateGeminiTaskResultPrivacy())
}

func TestValidateGeminiTaskResultPrivacyRejectsReintroducedRawData(
	t *testing.T,
) {
	prepareGeminiTaskResultPrivacyMigrationTest(t)
	channel := insertGeminiMigrationChannel(
		t,
		geminiMigrationFirstKey,
		geminiMigrationSecondKey,
	)
	rawData := geminiMigrationResultBody(
		t,
		"generatedSamples",
		geminiMigrationProviderURI(geminiMigrationSecondKey),
	)
	task := insertLegacyGeminiResultTask(
		t,
		channel.Id,
		geminiMigrationSecondKey,
		"task_gemini_validation_raw_reintroduced",
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		TaskStatusSuccess,
		rawData,
	)
	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())
	require.NoError(t, DB.Model(&Task{}).
		Where("id = ?", task.ID).
		UpdateColumn("data", rawData).Error)

	err := ValidateGeminiTaskResultPrivacy()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "task "+strconv.FormatInt(task.ID, 10))
	for _, forbidden := range []string{
		geminiMigrationSecondKey,
		geminiMigrationSignedQuery,
		geminiMigrationProviderPath,
		geminiMigrationOperation,
	} {
		assert.NotContains(t, err.Error(), forbidden)
	}
}

func TestMigrateGeminiTaskResultPrivacyUsesBoundedLockedBatches(t *testing.T) {
	prepareGeminiTaskResultPrivacyMigrationTest(t)

	tasks := make([]Task, geminiTaskResultPrivacyBatchSize+1)
	for index := range tasks {
		tasks[index] = Task{
			TaskID: "task_gemini_batch_" + strconv.Itoa(index),
			Platform: constant.TaskPlatform(
				strconv.Itoa(constant.ChannelTypeGemini),
			),
			UserId:   8301,
			Status:   TaskStatusInProgress,
			Progress: "50%",
			PrivateData: TaskPrivateData{
				ResultURL: "https://legacy.example.test/" +
					geminiMigrationLegacyResult,
			},
			FailReason: geminiMigrationProviderMessage,
			Data:       []byte(`{"name":"legacy-operation"}`),
		}
	}
	require.NoError(t, DB.CreateInBatches(&tasks, 50).Error)

	var (
		readPools       []any
		writePools      []any
		limits          []int
		currentReadPool any
	)
	queryCallback := fmt.Sprintf(
		"test:capture-gemini-result-batch-read:%s",
		t.Name(),
	)
	updateCallback := fmt.Sprintf(
		"test:capture-gemini-result-batch-write:%s",
		t.Name(),
	)
	require.NoError(t, DB.Callback().Query().Before("gorm:query").
		Register(queryCallback, func(tx *gorm.DB) {
			if tx.Statement.Table != "tasks" {
				return
			}
			limitClause, ok := tx.Statement.Clauses["LIMIT"]
			if !ok {
				return
			}
			limit, ok := limitClause.Expression.(clause.Limit)
			if !ok || limit.Limit == nil {
				return
			}
			limits = append(limits, *limit.Limit)
			currentReadPool = tx.Statement.ConnPool
			readPools = append(readPools, currentReadPool)
		}))
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == "tasks" {
				writePools = append(writePools, tx.Statement.ConnPool)
				assert.Equal(t, currentReadPool, tx.Statement.ConnPool)
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Query().Remove(queryCallback))
		require.NoError(t, DB.Callback().Update().Remove(updateCallback))
	})

	require.NoError(t, MigrateGeminiTaskResultPrivacy())

	require.Len(t, limits, 3)
	assert.Equal(t, []int{100, 100, 100}, limits)
	require.Len(t, readPools, 3)
	require.NotEmpty(t, writePools)
	for _, pool := range readPools[:2] {
		_, usesTransaction := pool.(gorm.TxCommitter)
		assert.True(t, usesTransaction)
	}
	for _, pool := range writePools {
		_, usesTransaction := pool.(gorm.TxCommitter)
		assert.True(t, usesTransaction)
	}
}

func useFileBackedGeminiTaskResultMigrationTestDB(t *testing.T) {
	t.Helper()

	previousDB := DB
	previousMainDatabaseType := common.MainDatabaseType()
	dsn := "file:" +
		filepath.Join(t.TempDir(), "gemini-result-backfill-race.db") +
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
}

func TestMigrateGeminiTaskResultPrivacyDoesNotOverwriteConcurrentWrite(
	t *testing.T,
) {
	useFileBackedGeminiTaskResultMigrationTestDB(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	rawData := geminiMigrationResultBody(
		t,
		"generatedSamples",
		geminiMigrationProviderURI(geminiMigrationSecondKey),
	)
	task := &Task{
		TaskID:   "task_gemini_concurrent_backfill",
		Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		Status:   TaskStatusSuccess,
		Progress: "100%",
		Data:     rawData,
	}
	insertTask(t, task)

	const concurrentData = `{"done":false,"writer_marker":"current-writer-sentinel"}`
	readLocked := make(chan struct{})
	writerDone := make(chan struct{})
	var writerErr error
	var signalRead sync.Once
	queryCallback := fmt.Sprintf(
		"test:pause-after-gemini-result-read:%s",
		t.Name(),
	)
	updateCallback := fmt.Sprintf(
		"test:wait-for-gemini-result-writer:%s",
		t.Name(),
	)
	require.NoError(t, DB.Callback().Query().After("gorm:query").
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
		require.NoError(t, DB.Callback().Query().Remove(queryCallback))
		require.NoError(t, DB.Callback().Update().Remove(updateCallback))
	})

	go func() {
		<-readLocked
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		writerErr = DB.WithContext(ctx).Exec(
			"UPDATE tasks SET data = ? WHERE id = ?",
			[]byte(concurrentData),
			task.ID,
		).Error
		close(writerDone)
	}()

	migrationErr := MigrateGeminiTaskResultPrivacy()
	<-writerDone
	require.NoError(t, writerErr)
	require.Error(t, migrationErr)
	for _, forbidden := range []string{
		string(rawData),
		concurrentData,
		"current-writer-sentinel",
		geminiMigrationSecondKey,
		geminiMigrationSignedQuery,
		geminiMigrationProviderPath,
	} {
		assert.NotContains(t, migrationErr.Error(), forbidden)
	}

	columns := rawGeminiTaskResultPrivacyColumns(t, task.ID)
	assert.Equal(t, concurrentData, columns.Data.String)

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())
	columns = rawGeminiTaskResultPrivacyColumns(t, task.ID)
	assert.Equal(
		t,
		string(geminitaskresult.EmptyPublicProjection(false)),
		columns.Data.String,
	)
}

func TestMigrateGeminiTaskResultPrivacyIgnoresNonGeminiTasks(t *testing.T) {
	prepareGeminiTaskResultPrivacyMigrationTest(t)
	rawData := []byte(`{"uri":"https://other.example/non-gemini-sentinel"}`)
	task := &Task{
		TaskID:   "task_non_gemini_backfill_scope",
		Platform: constant.TaskPlatform("other-provider"),
		Status:   TaskStatusSuccess,
		Data:     rawData,
	}
	insertTask(t, task)

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())

	columns := rawGeminiTaskResultPrivacyColumns(t, task.ID)
	assert.True(t, bytes.Equal(rawData, []byte(columns.Data.String)))
	assert.False(t, columns.ProviderResultURI.Valid)
}

func TestMigrateGeminiTaskResultPrivacyCoversEveryRecognizedLegacyCaseAcrossBatches(
	t *testing.T,
) {
	prepareGeminiTaskResultPrivacyMigrationTest(t)
	channel := insertGeminiMigrationChannel(
		t,
		geminiMigrationFirstKey,
		geminiMigrationSecondKey,
	)
	rawData := geminiMigrationResultBody(
		t,
		"generatedSamples",
		geminiMigrationProviderURI(geminiMigrationSecondKey),
	)
	numericPlatform := constant.TaskPlatform(
		strconv.Itoa(constant.ChannelTypeGemini),
	)
	recognizedPlatforms := make(
		[]constant.TaskPlatform,
		0,
		2*(1+(1<<len("gemini"))),
	)
	for repeat := 0; repeat < 2; repeat++ {
		recognizedPlatforms = append(recognizedPlatforms, numericPlatform)
		for mask := 0; mask < 1<<len("gemini"); mask++ {
			recognizedPlatforms = append(
				recognizedPlatforms,
				constant.TaskPlatform(geminiMigrationLegacyCaseVariant(mask)),
			)
		}
	}
	require.Greater(
		t,
		len(recognizedPlatforms),
		geminiTaskResultPrivacyBatchSize,
	)

	recognizedTasks := make([]*Task, 0, len(recognizedPlatforms))
	nonGeminiTasks := make([]*Task, 0, len(recognizedPlatforms)/10)
	for index, platform := range recognizedPlatforms {
		task := insertLegacyGeminiResultTask(
			t,
			channel.Id,
			geminiMigrationSecondKey,
			"task_gemini_case_batch_"+strconv.Itoa(index),
			platform,
			TaskStatusSuccess,
			rawData,
		)
		require.True(t, task.IsGeminiTask(), platform)
		recognizedTasks = append(recognizedTasks, task)

		if index%10 == 5 {
			nonGemini := &Task{
				TaskID:   "task_non_gemini_case_batch_" + strconv.Itoa(index),
				Platform: constant.TaskPlatform("other-provider"),
				Status:   TaskStatusSuccess,
				Data: []byte(
					`{"uri":"https://other.example/non-gemini-case-sentinel"}`,
				),
			}
			insertTask(t, nonGemini)
			nonGeminiTasks = append(nonGeminiTasks, nonGemini)
		}
	}

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())

	firstPass := make(
		map[int64]geminiTaskResultPrivacyColumns,
		len(recognizedTasks),
	)
	for _, task := range recognizedTasks {
		columns := rawGeminiTaskResultPrivacyColumns(t, task.ID)
		combined := columns.Data.String +
			columns.PrivateData.String +
			columns.FailReason +
			columns.ProviderResultURI.String
		require.NotContains(t, combined, geminiMigrationSecondKey)
		require.NotContains(t, combined, geminiMigrationSignedQuery)
		require.NotContains(t, combined, geminiMigrationOperation)
		require.NotContains(t, combined, geminiMigrationLegacyResult)
		require.True(t, columns.ProviderResultURI.Valid)
		firstPass[task.ID] = columns
	}
	for _, task := range nonGeminiTasks {
		columns := rawGeminiTaskResultPrivacyColumns(t, task.ID)
		assert.JSONEq(
			t,
			`{"uri":"https://other.example/non-gemini-case-sentinel"}`,
			columns.Data.String,
		)
		assert.False(t, columns.ProviderResultURI.Valid)
	}

	secondPassUpdates := 0
	updateCallback := fmt.Sprintf(
		"test:capture-gemini-case-second-pass:%s",
		t.Name(),
	)
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == "tasks" {
				secondPassUpdates++
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(updateCallback))
	})

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())
	assert.Zero(t, secondPassUpdates)
	for _, task := range recognizedTasks {
		assert.Equal(
			t,
			firstPass[task.ID],
			rawGeminiTaskResultPrivacyColumns(t, task.ID),
		)
	}
}

func TestGeminiTaskResultPrivacyQuerySQLSupportsEveryDialectAndLegacyCase(
	t *testing.T,
) {
	previousDatabaseType := common.MainDatabaseType()
	t.Cleanup(func() {
		common.SetMainDatabaseType(previousDatabaseType)
	})

	tests := []struct {
		name       string
		database   common.DatabaseType
		dialector  gorm.Dialector
		lockSuffix string
	}{
		{
			name:      "sqlite",
			database:  common.DatabaseTypeSQLite,
			dialector: sqlite.Open(":memory:"),
		},
		{
			name:     "mysql",
			database: common.DatabaseTypeMySQL,
			dialector: mysql.New(mysql.Config{
				DSN:                       "review:review@tcp(127.0.0.1:3306)/review",
				SkipInitializeWithVersion: true,
			}),
			lockSuffix: " FOR UPDATE",
		},
		{
			name:     "postgres",
			database: common.DatabaseTypePostgreSQL,
			dialector: postgres.New(postgres.Config{
				DSN: "host=127.0.0.1 user=review password=review " +
					"dbname=review sslmode=disable",
				PreferSimpleProtocol: true,
			}),
			lockSuffix: " FOR UPDATE",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(test.dialector, &gorm.Config{
				DisableAutomaticPing: true,
				DryRun:               true,
			})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, sqlDB.Close())
			})

			common.SetMainDatabaseType(test.database)
			querySQL := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
				var tasks []Task
				return lockForUpdate(
					geminiTaskResultPrivacyRows(tx.Model(&Task{})),
				).
					Where("id > ?", 41).
					Order("id ASC").
					Limit(geminiTaskResultPrivacyBatchSize).
					Find(&tasks)
			})

			assert.Contains(
				t,
				querySQL,
				strconv.Itoa(constant.ChannelTypeGemini),
			)
			for mask := 0; mask < 1<<len("gemini"); mask++ {
				assert.Contains(
					t,
					querySQL,
					geminiMigrationLegacyCaseVariant(mask),
				)
			}
			assert.NotContains(t, strings.ToUpper(querySQL), "LOWER(")
			assert.Contains(t, querySQL, "ORDER BY id ASC")
			assert.Contains(t, querySQL, "LIMIT 100")
			if test.lockSuffix == "" {
				assert.NotContains(t, strings.ToUpper(querySQL), "FOR UPDATE")
			} else {
				assert.True(
					t,
					strings.HasSuffix(
						strings.ToUpper(querySQL),
						test.lockSuffix,
					),
					querySQL,
				)
			}
		})
	}
}

func TestMigrateGeminiTaskResultPrivacyGuardsDatabaseCollationOvermatch(
	t *testing.T,
) {
	previousDB := DB
	previousDatabaseType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	initCol()
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousDatabaseType)
		initCol()
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, DB.Exec(`
		CREATE TABLE tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT,
			platform TEXT COLLATE RTRIM,
			channel_id INTEGER,
			status TEXT,
			data JSON,
			private_data JSON,
			fail_reason TEXT,
			provider_result_uri TEXT
		)
	`).Error)
	rawData := []byte(
		`{"done":true,"uri":"https://other.example/` +
			`collation-overmatch-secret?key=collation-secret"}`,
	)
	require.NoError(t, DB.Exec(
		`INSERT INTO tasks `+
			`(task_id, platform, channel_id, status, data, fail_reason) `+
			`VALUES (?, ?, ?, ?, ?, ?)`,
		"task_collation_overmatch",
		"gemini ",
		0,
		TaskStatusSuccess,
		rawData,
		"collation-fail-reason",
	).Error)

	var selected int64
	require.NoError(t, geminiTaskResultPrivacyRows(DB.Model(&Task{})).
		Count(&selected).Error)
	require.Equal(t, int64(1), selected)

	var task Task
	require.NoError(t, DB.First(&task).Error)
	require.False(t, task.IsGeminiTask())

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())

	var stored geminiTaskResultPrivacyColumns
	require.NoError(t, DB.Model(&Task{}).
		Select("data", "private_data", "fail_reason", "provider_result_uri").
		First(&stored).Error)
	assert.Equal(t, string(rawData), stored.Data.String)
	assert.Equal(t, "collation-fail-reason", stored.FailReason)
	assert.False(t, stored.ProviderResultURI.Valid)
}

func TestValidateGeminiTaskResultPrivacyAcceptsMySQLNormalizedCanonicalData(
	t *testing.T,
) {
	prepareGeminiTaskResultPrivacyMigrationTest(t)
	task := &Task{
		TaskID: "task_gemini_mysql_normalized",
		Platform: constant.TaskPlatform(
			strconv.Itoa(constant.ChannelTypeGemini),
		),
		Status: TaskStatusSuccess,
		Data: []byte(
			`{"video": {"mime_type": "video/mp4", "url": "` +
				geminitaskresult.ProxyPath("task_gemini_mysql_normalized") +
				`"}, "done": true}`,
		),
	}
	insertTask(t, task)

	previousDatabaseType := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	t.Cleanup(func() {
		common.SetMainDatabaseType(previousDatabaseType)
	})

	require.NoError(t, ValidateGeminiTaskResultPrivacy())
}

func TestMigrateGeminiTaskResultPrivacyDoesNotRewriteMySQLNormalizedCanonicalData(
	t *testing.T,
) {
	prepareGeminiTaskResultPrivacyMigrationTest(t)
	channel := insertGeminiMigrationChannel(
		t,
		geminiMigrationFirstKey,
		geminiMigrationSecondKey,
	)
	task := insertLegacyGeminiResultTask(
		t,
		channel.Id,
		geminiMigrationSecondKey,
		"task_gemini_mysql_idempotent",
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		TaskStatusSuccess,
		geminiMigrationResultBody(
			t,
			"generatedSamples",
			geminiMigrationProviderURI(geminiMigrationSecondKey),
		),
	)
	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	first := rawGeminiTaskResultPrivacyColumns(t, task.ID)
	require.True(t, first.ProviderResultURI.Valid)

	mysqlNormalized := []byte(
		`{"video": {"mime_type": "video/mp4", "url": "` +
			geminitaskresult.ProxyPath(task.TaskID) +
			`"}, "done": true}`,
	)
	require.NoError(t, DB.Model(&Task{}).
		Where("id = ?", task.ID).
		UpdateColumn("data", mysqlNormalized).Error)

	previousDatabaseType := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	t.Cleanup(func() {
		common.SetMainDatabaseType(previousDatabaseType)
	})
	secondPassUpdates := 0
	updateCallback := fmt.Sprintf(
		"test:capture-gemini-mysql-second-pass:%s",
		t.Name(),
	)
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == "tasks" {
				secondPassUpdates++
			}
		}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(updateCallback))
	})

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())
	assert.Zero(t, secondPassUpdates)
	second := rawGeminiTaskResultPrivacyColumns(t, task.ID)
	assert.Equal(t, string(mysqlNormalized), second.Data.String)
	assert.Equal(t, first.PrivateData, second.PrivateData)
	assert.Equal(t, first.FailReason, second.FailReason)
	assert.Equal(t, first.ProviderResultURI, second.ProviderResultURI)
}

func TestGeminiTaskResultPublicDataEqualDoesNotBroadenStoredStructure(
	t *testing.T,
) {
	canonical := []byte(
		`{"done":true,"video":{"url":"/v1/videos/task/content",` +
			`"mime_type":"video/mp4"}}`,
	)
	mysqlNormalized := []byte(
		`{"video": {"mime_type": "video/mp4", ` +
			`"url": "/v1/videos/task/content"}, "done": true}`,
	)
	mysqlUnknownField := []byte(
		`{"video": {"mime_type": "video/mp4", ` +
			`"url": "/v1/videos/task/content"}, "done": true, ` +
			`"provider_uri": "must-not-be-accepted"}`,
	)
	duplicateAllowedKey := []byte(
		`{"done":"must-not-survive","done":true,` +
			`"video":{"url":"/v1/videos/task/content",` +
			`"mime_type":"video/mp4"}}`,
	)

	assert.True(t, geminiTaskResultPublicDataEqual(
		common.DatabaseTypeMySQL,
		mysqlNormalized,
		canonical,
	))
	assert.False(t, geminiTaskResultPublicDataEqual(
		common.DatabaseTypeMySQL,
		mysqlUnknownField,
		canonical,
	))
	assert.False(t, geminiTaskResultPublicDataEqual(
		common.DatabaseTypeSQLite,
		mysqlNormalized,
		canonical,
	))
	assert.False(t, geminiTaskResultPublicDataEqual(
		common.DatabaseTypePostgreSQL,
		mysqlNormalized,
		canonical,
	))
	assert.False(t, geminiTaskResultPublicDataEqual(
		common.DatabaseTypeSQLite,
		duplicateAllowedKey,
		canonical,
	))
	assert.False(t, geminiTaskResultPublicDataEqual(
		common.DatabaseTypePostgreSQL,
		duplicateAllowedKey,
		canonical,
	))
}
