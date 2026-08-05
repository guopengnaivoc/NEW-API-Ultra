package model

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type geminiTaskResultPrivacyConfiguredDatabase struct {
	name         string
	envName      string
	databaseType common.DatabaseType
	dialector    func(string) gorm.Dialector
}

type geminiTaskResultPrivacyExternalColumns struct {
	Data              sql.NullString `gorm:"column:data"`
	PrivateData       sql.NullString `gorm:"column:private_data"`
	FailReason        string         `gorm:"column:fail_reason"`
	ProviderResultURI sql.NullString `gorm:"column:provider_result_uri"`
}

type geminiTaskResultPrivacyExternalServerMetadata struct {
	Version       string `gorm:"column:server_version"`
	Identity      string `gorm:"column:server_identity"`
	VersionNumber int    `gorm:"column:server_version_number"`
}

func TestGeminiTaskResultPrivacyConfiguredDatabases(t *testing.T) {
	databases := []geminiTaskResultPrivacyConfiguredDatabase{
		{
			name:         "sqlite",
			databaseType: common.DatabaseTypeSQLite,
			dialector: func(dsn string) gorm.Dialector {
				return sqlite.Open(dsn)
			},
		},
		{
			name:         "mysql",
			envName:      "TEST_MYSQL_DSN",
			databaseType: common.DatabaseTypeMySQL,
			dialector: func(dsn string) gorm.Dialector {
				return mysql.Open(dsn)
			},
		},
		{
			name:         "postgres",
			envName:      "TEST_POSTGRES_DSN",
			databaseType: common.DatabaseTypePostgreSQL,
			dialector: func(dsn string) gorm.Dialector {
				return postgres.New(postgres.Config{
					DSN:                  dsn,
					PreferSimpleProtocol: true,
				})
			},
		},
	}

	for _, database := range databases {
		t.Run(database.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(database.envName))
			if database.databaseType == common.DatabaseTypeSQLite {
				dsn = "file:" +
					filepath.Join(t.TempDir(), "gemini-task-result-privacy.db") +
					"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
			} else if dsn == "" {
				t.Skip(database.envName + " is not configured")
			}
			testGeminiTaskResultPrivacyConfiguredDatabase(t, database, dsn)
		})
	}
}

func TestGeminiTaskResultPrivacyExternalServerProvenance(t *testing.T) {
	testCases := []struct {
		name           string
		databaseType   common.DatabaseType
		serverVersion  string
		serverIdentity string
		versionNumber  int
		errorContains  string
	}{
		{
			name:           "mysql minimum supported",
			databaseType:   common.DatabaseTypeMySQL,
			serverVersion:  "5.7.8",
			serverIdentity: "MySQL Community Server - GPL",
		},
		{
			name:           "mysql current release suffix",
			databaseType:   common.DatabaseTypeMySQL,
			serverVersion:  "8.4.1-commercial",
			serverIdentity: "MySQL Enterprise Server - Commercial",
		},
		{
			name:           "mysql source distribution",
			databaseType:   common.DatabaseTypeMySQL,
			serverVersion:  "8.0.36",
			serverIdentity: "Source distribution",
		},
		{
			name:           "mysql below minimum",
			databaseType:   common.DatabaseTypeMySQL,
			serverVersion:  "5.7.7",
			serverIdentity: "MySQL Community Server - GPL",
			errorContains:  "requires MySQL 5.7.8 or newer",
		},
		{
			name:           "mariadb rejected",
			databaseType:   common.DatabaseTypeMySQL,
			serverVersion:  "10.11.7-MariaDB",
			serverIdentity: "mariadb.org binary distribution",
			errorContains:  "unexpected MySQL-compatible server",
		},
		{
			name:           "mariadb legacy handshake rejected",
			databaseType:   common.DatabaseTypeMySQL,
			serverVersion:  "5.5.5-10.11.8-MariaDB",
			serverIdentity: "mariadb.org binary distribution",
			errorContains:  "unexpected MySQL-compatible server",
		},
		{
			name:           "mariadb comment rejected",
			databaseType:   common.DatabaseTypeMySQL,
			serverVersion:  "10.11.8",
			serverIdentity: "MariaDB Server",
			errorContains:  "unexpected MySQL-compatible server",
		},
		{
			name:           "tidb rejected",
			databaseType:   common.DatabaseTypeMySQL,
			serverVersion:  "5.7.25-TiDB-v7.5.1",
			serverIdentity: "TiDB Server",
			errorContains:  "unexpected MySQL-compatible server",
		},
		{
			name:          "mysql identity required",
			databaseType:  common.DatabaseTypeMySQL,
			serverVersion: "8.0.36",
			errorContains: "server identity is empty",
		},
		{
			name:           "mysql malformed version rejected",
			databaseType:   common.DatabaseTypeMySQL,
			serverVersion:  "devel",
			serverIdentity: "MySQL development server",
			errorContains:  "parse server version",
		},
		{
			name:           "postgres minimum supported",
			databaseType:   common.DatabaseTypePostgreSQL,
			serverVersion:  "9.6",
			serverIdentity: "PostgreSQL 9.6 on x86_64",
			versionNumber:  90600,
		},
		{
			name:           "postgres current packaged release",
			databaseType:   common.DatabaseTypePostgreSQL,
			serverVersion:  "16.2 (Ubuntu 16.2-1)",
			serverIdentity: "PostgreSQL 16.2 on x86_64-pc-linux-gnu",
			versionNumber:  160002,
		},
		{
			name:           "postgres below minimum",
			databaseType:   common.DatabaseTypePostgreSQL,
			serverVersion:  "99.0 display text is not authoritative",
			serverIdentity: "PostgreSQL 9.5.25 on x86_64",
			versionNumber:  90525,
			errorContains:  "requires PostgreSQL 9.6 or newer",
		},
		{
			name:           "cockroach rejected",
			databaseType:   common.DatabaseTypePostgreSQL,
			serverVersion:  "15.0",
			serverIdentity: "CockroachDB CCL v23.2.4",
			versionNumber:  150000,
			errorContains:  "unexpected PostgreSQL-compatible server",
		},
		{
			name:           "postgres version number required",
			databaseType:   common.DatabaseTypePostgreSQL,
			serverVersion:  "devel",
			serverIdentity: "PostgreSQL devel on x86_64",
			errorContains:  "server_version_num is invalid",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateGeminiTaskResultExternalServerProvenance(
				testCase.databaseType,
				testCase.serverVersion,
				testCase.serverIdentity,
				testCase.versionNumber,
			)
			if testCase.errorContains == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorContains(t, err, testCase.errorContains)
		})
	}
}

func testGeminiTaskResultPrivacyConfiguredDatabase(
	t *testing.T,
	database geminiTaskResultPrivacyConfiguredDatabase,
	dsn string,
) {
	t.Helper()

	tablePrefix := fmt.Sprintf(
		"na38_%s_%x_%x_",
		string(database.name[0]),
		os.Getpid(),
		time.Now().UnixNano(),
	)
	db, err := gorm.Open(database.dialector(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix: tablePrefix,
		},
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, sqlDB.Close())
	})
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)

	models := []struct {
		value         any
		expectedTable string
	}{
		{value: &Channel{}, expectedTable: tablePrefix + "channels"},
		{value: &Task{}, expectedTable: tablePrefix + "tasks"},
	}
	for _, model := range models {
		statement := &gorm.Statement{DB: db}
		require.NoError(t, statement.Parse(model.value))
		require.Equal(t, model.expectedTable, statement.Schema.Table)
		require.True(t, strings.HasPrefix(statement.Schema.Table, tablePrefix))
		require.False(
			t,
			db.Migrator().HasTable(model.value),
			"refusing to reuse or clean up a pre-existing table",
		)
	}
	if database.databaseType != common.DatabaseTypeSQLite {
		requireGeminiTaskResultExternalServerProvenance(t, database, db)
	}
	t.Cleanup(func() {
		for index := len(models) - 1; index >= 0; index-- {
			model := models[index]
			statement := &gorm.Statement{DB: db}
			if err := statement.Parse(model.value); err != nil {
				t.Errorf(
					"refusing cleanup after table-name resolution failed: %v",
					err,
				)
				continue
			}
			if statement.Schema.Table != model.expectedTable ||
				!strings.HasPrefix(statement.Schema.Table, tablePrefix) {
				t.Errorf(
					"refusing to drop table outside test prefix: %q",
					statement.Schema.Table,
				)
				continue
			}
			assert.NoError(t, db.Migrator().DropTable(model.value))
			assert.False(t, db.Migrator().HasTable(model.value))
		}
	})

	require.NoError(t, db.AutoMigrate(&Channel{}, &Task{}))
	taskTable := tablePrefix + "tasks"

	previousDB := DB
	previousDatabaseType := common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(database.databaseType)
	initCol()
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousDatabaseType)
		initCol()
	})
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)

	var (
		providerResultColumnFound bool
		mysqlDataColumnFound      bool
	)
	columnTypes, err := db.Migrator().ColumnTypes(&Task{})
	require.NoError(t, err)
	for _, columnType := range columnTypes {
		switch {
		case strings.EqualFold(columnType.Name(), "data") &&
			database.databaseType == common.DatabaseTypeMySQL:
			mysqlDataColumnFound = true
			assert.Equal(
				t,
				"JSON",
				strings.ToUpper(columnType.DatabaseTypeName()),
			)
		case strings.EqualFold(columnType.Name(), "provider_result_uri"):
			providerResultColumnFound = true
			assert.Contains(
				t,
				strings.ToUpper(columnType.DatabaseTypeName()),
				"TEXT",
			)
			if database.databaseType != common.DatabaseTypeSQLite {
				nullable, ok := columnType.Nullable()
				require.True(t, ok)
				assert.True(t, nullable)
			}
		}
	}
	require.True(t, providerResultColumnFound)
	if database.databaseType == common.DatabaseTypeMySQL {
		require.True(t, mysqlDataColumnFound)
	}
	if database.databaseType == common.DatabaseTypeSQLite {
		var sqliteColumns []struct {
			Name    string `gorm:"column:name"`
			Type    string `gorm:"column:type"`
			NotNull int    `gorm:"column:notnull"`
		}
		require.NoError(t, db.Raw(
			"PRAGMA table_info("+taskTable+")",
		).Scan(&sqliteColumns).Error)
		var sqliteProviderResultColumnFound bool
		for _, column := range sqliteColumns {
			if !strings.EqualFold(column.Name, "provider_result_uri") {
				continue
			}
			sqliteProviderResultColumnFound = true
			assert.Contains(t, strings.ToUpper(column.Type), "TEXT")
			assert.Zero(t, column.NotNull)
		}
		require.True(t, sqliteProviderResultColumnFound)
	}

	const (
		firstKey    = "external-gemini-first-key-sentinel"
		selectedKey = "external-gemini-selected-key-sentinel"
		thirdKey    = "external-gemini-third-key-sentinel"
		signedQuery = "external-gemini-signed-query-sentinel"
	)
	providerURI := "https://video.example.test/external-gemini-provider-path" +
		"?key=" + selectedKey +
		"&sig=" + signedQuery +
		"&keep=1"
	filteredProviderURI := "https://video.example.test/external-gemini-provider-path" +
		"?sig=" + signedQuery +
		"&keep=1"
	rawData, err := common.Marshal(map[string]any{
		"name": "external-gemini-operation-sentinel",
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
	})
	require.NoError(t, err)

	channel := &Channel{
		Type:   constant.ChannelTypeGemini,
		Key:    firstKey + "\n" + selectedKey + "\n" + thirdKey,
		Name:   "external-gemini-result-privacy",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey: true,
		},
	}
	require.NoError(t, db.Create(channel).Error)

	taskID := "task_gemini_external_" + database.name
	task := InitTask(
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		&relaycommon.RelayInfo{
			UserId:     8301,
			UsingGroup: "default",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId:   channel.Id,
				ChannelType: constant.ChannelTypeGemini,
				ApiKey:      selectedKey,
			},
		},
	)
	task.TaskID = taskID
	task.Status = TaskStatusSuccess
	task.Progress = "100%"
	task.Data = rawData
	task.PrivateData.ResultURL =
		"https://legacy.example.test/external-gemini-result-sentinel"
	task.FailReason = "external-gemini-provider-message-sentinel"
	require.NoError(t, db.Create(task).Error)

	channel.Key = thirdKey + "\n" + firstKey + "\n" + selectedKey
	require.NoError(t, db.Save(channel).Error)

	var migrationTaskQueries []string
	queryCallback := fmt.Sprintf(
		"test:capture-gemini-external-migration-query:%s:%d",
		t.Name(),
		time.Now().UnixNano(),
	)
	queryCallbackRegistered := true
	require.NoError(t, db.Callback().Query().After("gorm:query").
		Register(queryCallback, func(tx *gorm.DB) {
			if tx.Statement.Table != taskTable {
				return
			}
			querySQL := tx.Dialector.Explain(
				tx.Statement.SQL.String(),
				tx.Statement.Vars...,
			)
			upperQuerySQL := strings.ToUpper(querySQL)
			if strings.Contains(upperQuerySQL, "PLATFORM") &&
				strings.Contains(upperQuerySQL, " IN ") &&
				strings.Contains(upperQuerySQL, "LIMIT") {
				migrationTaskQueries = append(migrationTaskQueries, querySQL)
			}
		}))
	t.Cleanup(func() {
		if queryCallbackRegistered {
			require.NoError(t, db.Callback().Query().Remove(queryCallback))
		}
	})

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, db.Callback().Query().Remove(queryCallback))
	queryCallbackRegistered = false
	require.NotEmpty(t, migrationTaskQueries)
	if database.databaseType == common.DatabaseTypeSQLite {
		for _, querySQL := range migrationTaskQueries {
			assert.NotContains(t, strings.ToUpper(querySQL), "FOR UPDATE")
		}
	} else {
		var lockedQueryFound bool
		for _, querySQL := range migrationTaskQueries {
			if strings.Contains(strings.ToUpper(querySQL), "FOR UPDATE") {
				lockedQueryFound = true
			}
		}
		assert.True(t, lockedQueryFound)
	}

	expectedPublicData := `{"done":true,"video":{"url":"` +
		geminitaskresult.ProxyPath(taskID) +
		`","mime_type":"video/mp4"}}`
	first := readGeminiTaskResultPrivacyExternalColumns(t, db, task.ID)
	require.True(t, first.Data.Valid)
	assert.JSONEq(t, expectedPublicData, first.Data.String)
	if database.databaseType != common.DatabaseTypeMySQL {
		assert.Equal(t, expectedPublicData, first.Data.String)
	}
	require.True(t, first.ProviderResultURI.Valid)
	assert.True(
		t,
		common.IsDataEncryptionEnvelope(first.ProviderResultURI.String),
	)
	assert.NotContains(t, first.ProviderResultURI.String, selectedKey)
	assert.NotContains(
		t,
		first.ProviderResultURI.String,
		"external-gemini-provider-path",
	)

	var loaded Task
	require.NoError(t, db.First(&loaded, task.ID).Error)
	openedProviderURI, err := loaded.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, filteredProviderURI, openedProviderURI)
	assert.NotContains(t, openedProviderURI, selectedKey)

	if database.databaseType == common.DatabaseTypeMySQL {
		mysqlStorageVariant := []byte(
			`{"video": {"mime_type": "video/mp4", "url": "` +
				geminitaskresult.ProxyPath(taskID) +
				`"}, "done": true}`,
		)
		require.NoError(t, db.Model(&Task{}).
			Where("id = ?", task.ID).
			UpdateColumn("data", mysqlStorageVariant).Error)
		mysqlStored := readGeminiTaskResultPrivacyExternalColumns(
			t,
			db,
			task.ID,
		)
		require.True(t, mysqlStored.Data.Valid)
		assert.JSONEq(
			t,
			string(mysqlStorageVariant),
			mysqlStored.Data.String,
		)
		assert.NotEqual(
			t,
			string(mysqlStorageVariant),
			mysqlStored.Data.String,
			"MySQL JSON storage must physically normalize the submitted variant",
		)
		assert.NotEqual(
			t,
			expectedPublicData,
			mysqlStored.Data.String,
			"the semantic-comparison path requires non-canonical bytes",
		)
	}
	require.NoError(t, ValidateGeminiTaskResultPrivacy())
	first = readGeminiTaskResultPrivacyExternalColumns(t, db, task.ID)
	assert.JSONEq(t, expectedPublicData, first.Data.String)

	secondPassUpdates := 0
	updateCallback := fmt.Sprintf(
		"test:capture-gemini-external-second-pass:%s:%d",
		t.Name(),
		time.Now().UnixNano(),
	)
	updateCallbackRegistered := true
	require.NoError(t, db.Callback().Update().Before("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			if tx.Statement.Table == taskTable {
				secondPassUpdates++
			}
		}))
	t.Cleanup(func() {
		if updateCallbackRegistered {
			require.NoError(t, db.Callback().Update().Remove(updateCallback))
		}
	})

	require.NoError(t, MigrateGeminiTaskResultPrivacy())
	require.NoError(t, ValidateGeminiTaskResultPrivacy())
	require.NoError(t, db.Callback().Update().Remove(updateCallback))
	updateCallbackRegistered = false
	second := readGeminiTaskResultPrivacyExternalColumns(t, db, task.ID)
	assert.Zero(t, secondPassUpdates)
	assert.Equal(t, first, second)
	t.Logf(
		"%s task-result privacy: prefix=%s migration_queries=%d second_pass_updates=%d",
		database.name,
		tablePrefix,
		len(migrationTaskQueries),
		secondPassUpdates,
	)
}

func requireGeminiTaskResultExternalServerProvenance(
	t *testing.T,
	database geminiTaskResultPrivacyConfiguredDatabase,
	db *gorm.DB,
) {
	t.Helper()

	var metadata geminiTaskResultPrivacyExternalServerMetadata
	var err error
	switch database.databaseType {
	case common.DatabaseTypeMySQL:
		err = db.Raw(
			"SELECT VERSION() AS server_version, " +
				"@@version_comment AS server_identity",
		).Scan(&metadata).Error
	case common.DatabaseTypePostgreSQL:
		err = db.Raw(
			"SELECT current_setting('server_version') AS server_version, " +
				"version() AS server_identity, " +
				"current_setting('server_version_num')::integer " +
				"AS server_version_number",
		).Scan(&metadata).Error
	default:
		require.Failf(
			t,
			"unsupported external database type",
			"database type %q has no provenance check",
			database.databaseType,
		)
	}
	require.NoError(t, err)
	require.NoError(
		t,
		validateGeminiTaskResultExternalServerProvenance(
			database.databaseType,
			metadata.Version,
			metadata.Identity,
			metadata.VersionNumber,
		),
	)
	if database.databaseType == common.DatabaseTypePostgreSQL {
		t.Logf(
			"%s server provenance: version=%q version_number=%d identity=%q",
			database.name,
			metadata.Version,
			metadata.VersionNumber,
			metadata.Identity,
		)
		return
	}
	t.Logf(
		"%s server provenance: version=%q identity=%q",
		database.name,
		metadata.Version,
		metadata.Identity,
	)
}

func validateGeminiTaskResultExternalServerProvenance(
	databaseType common.DatabaseType,
	serverVersion string,
	serverIdentity string,
	serverVersionNumber int,
) error {
	serverVersion = strings.TrimSpace(serverVersion)
	serverIdentity = strings.TrimSpace(serverIdentity)
	if serverIdentity == "" {
		return errors.New("server identity is empty")
	}

	signature := strings.ToLower(serverVersion + " " + serverIdentity)
	switch databaseType {
	case common.DatabaseTypeMySQL:
		for _, incompatibleMarker := range []string{
			"mariadb",
			"tidb",
			"singlestore",
			"vitess",
			"oceanbase",
		} {
			if strings.Contains(signature, incompatibleMarker) {
				return fmt.Errorf(
					"unexpected MySQL-compatible server identity %q",
					serverIdentity,
				)
			}
		}
		parsed, err := parseGeminiTaskResultExternalServerVersion(serverVersion)
		if err != nil {
			return fmt.Errorf("parse server version %q: %w", serverVersion, err)
		}
		minimum := [3]int{5, 7, 8}
		for index := range minimum {
			if parsed[index] > minimum[index] {
				return nil
			}
			if parsed[index] < minimum[index] {
				return fmt.Errorf(
					"configured server version %q requires MySQL 5.7.8 or newer",
					serverVersion,
				)
			}
		}
		return nil
	case common.DatabaseTypePostgreSQL:
		if !strings.HasPrefix(serverIdentity, "PostgreSQL ") ||
			strings.Contains(signature, "cockroach") ||
			strings.Contains(signature, "yugabyte") ||
			strings.Contains(signature, "-yb-") {
			return fmt.Errorf(
				"unexpected PostgreSQL-compatible server identity %q",
				serverIdentity,
			)
		}
		if serverVersionNumber <= 0 {
			return fmt.Errorf(
				"PostgreSQL server_version_num is invalid: %d",
				serverVersionNumber,
			)
		}
		if serverVersionNumber < 90600 {
			return fmt.Errorf(
				"configured server version %q (%d) requires PostgreSQL 9.6 or newer",
				serverVersion,
				serverVersionNumber,
			)
		}
		return nil
	default:
		return fmt.Errorf("unsupported database type %q", databaseType)
	}
}

func parseGeminiTaskResultExternalServerVersion(
	serverVersion string,
) ([3]int, error) {
	var parsed [3]int
	components := strings.Split(strings.TrimSpace(serverVersion), ".")
	if len(components) < 2 {
		return parsed, errors.New("expected at least major and minor components")
	}

	for index := range parsed {
		if index >= len(components) {
			break
		}
		component := components[index]
		digitEnd := 0
		for digitEnd < len(component) &&
			component[digitEnd] >= '0' &&
			component[digitEnd] <= '9' {
			digitEnd++
		}
		if digitEnd == 0 {
			return parsed, fmt.Errorf(
				"component %d has no numeric prefix",
				index+1,
			)
		}
		value, err := strconv.Atoi(component[:digitEnd])
		if err != nil {
			return parsed, fmt.Errorf(
				"parse component %d: %w",
				index+1,
				err,
			)
		}
		parsed[index] = value
	}
	return parsed, nil
}

func readGeminiTaskResultPrivacyExternalColumns(
	t *testing.T,
	db *gorm.DB,
	taskID int64,
) geminiTaskResultPrivacyExternalColumns {
	t.Helper()

	var columns geminiTaskResultPrivacyExternalColumns
	require.NoError(t, db.Model(&Task{}).
		Select("data", "private_data", "fail_reason", "provider_result_uri").
		Where("id = ?", taskID).
		Scan(&columns).Error)
	return columns
}
