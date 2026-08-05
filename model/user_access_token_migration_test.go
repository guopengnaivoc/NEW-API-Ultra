package model

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const accessTokenMigrationTestDriverName = "newapi-access-token-migration-test"

var (
	accessTokenMigrationTestDriverOnce sync.Once
	accessTokenMigrationTestScriptsMu  sync.Mutex
	accessTokenMigrationTestScripts    = map[string]*accessTokenMigrationTestScript{}
)

type accessTokenMigrationTestEvent struct {
	kind  string
	query string
}

type accessTokenMigrationTestScript struct {
	mu                   sync.Mutex
	columnType           string
	trailingPadding      bool
	queryErrorContaining string
	execErrorContaining  string
	events               []accessTokenMigrationTestEvent
}

func (script *accessTokenMigrationTestScript) record(kind, query string) {
	script.mu.Lock()
	script.events = append(script.events, accessTokenMigrationTestEvent{kind: kind, query: query})
	script.mu.Unlock()
}

func (script *accessTokenMigrationTestScript) snapshot() []accessTokenMigrationTestEvent {
	script.mu.Lock()
	defer script.mu.Unlock()
	return append([]accessTokenMigrationTestEvent(nil), script.events...)
}

type accessTokenMigrationTestDriver struct{}

func (accessTokenMigrationTestDriver) Open(name string) (driver.Conn, error) {
	accessTokenMigrationTestScriptsMu.Lock()
	script := accessTokenMigrationTestScripts[name]
	accessTokenMigrationTestScriptsMu.Unlock()
	if script == nil {
		return nil, fmt.Errorf("unknown access-token migration test script %q", name)
	}
	return &accessTokenMigrationTestConn{script: script}, nil
}

type accessTokenMigrationTestConn struct {
	script *accessTokenMigrationTestScript
}

func (conn *accessTokenMigrationTestConn) Prepare(query string) (driver.Stmt, error) {
	return &accessTokenMigrationTestStmt{conn: conn, query: query}, nil
}

func (conn *accessTokenMigrationTestConn) Close() error { return nil }

func (conn *accessTokenMigrationTestConn) Begin() (driver.Tx, error) {
	return accessTokenMigrationTestTx{}, nil
}

func (conn *accessTokenMigrationTestConn) Ping(context.Context) error { return nil }

func (conn *accessTokenMigrationTestConn) ExecContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Result, error) {
	conn.script.record("exec", query)
	if conn.script.execErrorContaining != "" && strings.Contains(query, conn.script.execErrorContaining) {
		return nil, errors.New("scripted access-token migration exec failure")
	}
	return driver.RowsAffected(1), nil
}

func (conn *accessTokenMigrationTestConn) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	conn.script.record("query", query)
	if conn.script.queryErrorContaining != "" && strings.Contains(query, conn.script.queryErrorContaining) {
		return nil, errors.New("scripted access-token migration query failure")
	}
	normalized := strings.ToUpper(strings.Join(strings.Fields(query), " "))
	switch {
	case strings.Contains(normalized, "SELECT DATABASE()"):
		return &accessTokenMigrationTestRows{
			columns: []string{"DATABASE()"},
			values:  [][]driver.Value{{"newapi_test"}},
		}, nil
	case strings.Contains(normalized, "INFORMATION_SCHEMA.TABLES"):
		return &accessTokenMigrationTestRows{
			columns: []string{"count"},
			values:  [][]driver.Value{{int64(1)}},
		}, nil
	case strings.Contains(normalized, "INFORMATION_SCHEMA.COLUMNS") && strings.Contains(normalized, "COUNT("):
		return &accessTokenMigrationTestRows{
			columns: []string{"count"},
			values:  [][]driver.Value{{int64(1)}},
		}, nil
	case strings.Contains(normalized, "SELECT DATA_TYPE FROM INFORMATION_SCHEMA.COLUMNS"):
		return &accessTokenMigrationTestRows{
			columns: []string{"data_type"},
			values:  [][]driver.Value{{conn.script.columnType}},
		}, nil
	case strings.Contains(normalized, "SELECT COLUMN_TYPE FROM INFORMATION_SCHEMA.COLUMNS"):
		return &accessTokenMigrationTestRows{
			columns: []string{"COLUMN_TYPE"},
			values:  [][]driver.Value{{conn.script.columnType}},
		}, nil
	case strings.Contains(normalized, "SELECT 1 FROM USERS WHERE ACCESS_TOKEN LIKE"):
		values := [][]driver.Value(nil)
		if conn.script.trailingPadding {
			values = [][]driver.Value{{int64(1)}}
		}
		return &accessTokenMigrationTestRows{
			columns: []string{"1"},
			values:  values,
		}, nil
	default:
		return nil, fmt.Errorf("unexpected access-token migration query: %s", query)
	}
}

type accessTokenMigrationTestStmt struct {
	conn  *accessTokenMigrationTestConn
	query string
}

func (*accessTokenMigrationTestStmt) Close() error  { return nil }
func (*accessTokenMigrationTestStmt) NumInput() int { return -1 }

func (stmt *accessTokenMigrationTestStmt) Exec([]driver.Value) (driver.Result, error) {
	return stmt.conn.ExecContext(context.Background(), stmt.query, nil)
}

func (stmt *accessTokenMigrationTestStmt) Query([]driver.Value) (driver.Rows, error) {
	return stmt.conn.QueryContext(context.Background(), stmt.query, nil)
}

type accessTokenMigrationTestTx struct{}

func (accessTokenMigrationTestTx) Commit() error   { return nil }
func (accessTokenMigrationTestTx) Rollback() error { return nil }

type accessTokenMigrationTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (rows *accessTokenMigrationTestRows) Columns() []string { return rows.columns }
func (*accessTokenMigrationTestRows) Close() error           { return nil }

func (rows *accessTokenMigrationTestRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.index])
	rows.index++
	return nil
}

func openAccessTokenMigrationTestDB(
	t *testing.T,
	databaseType common.DatabaseType,
	columnType string,
) (*gorm.DB, *accessTokenMigrationTestScript) {
	t.Helper()
	accessTokenMigrationTestDriverOnce.Do(func() {
		sql.Register(accessTokenMigrationTestDriverName, accessTokenMigrationTestDriver{})
	})

	scriptName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	script := &accessTokenMigrationTestScript{columnType: columnType}
	accessTokenMigrationTestScriptsMu.Lock()
	accessTokenMigrationTestScripts[scriptName] = script
	accessTokenMigrationTestScriptsMu.Unlock()

	sqlDB, err := sql.Open(accessTokenMigrationTestDriverName, scriptName)
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		accessTokenMigrationTestScriptsMu.Lock()
		delete(accessTokenMigrationTestScripts, scriptName)
		accessTokenMigrationTestScriptsMu.Unlock()
	})

	var dialector gorm.Dialector
	switch databaseType {
	case common.DatabaseTypePostgreSQL:
		dialector = postgres.New(postgres.Config{
			Conn:                 sqlDB,
			WithoutReturning:     true,
			PreferSimpleProtocol: true,
		})
	case common.DatabaseTypeMySQL:
		dialector = mysql.New(mysql.Config{
			Conn:                      sqlDB,
			SkipInitializeWithVersion: true,
		})
	default:
		require.FailNow(t, "unsupported test database type", databaseType)
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	return db, script
}

func useAccessTokenMigrationTestDB(
	t *testing.T,
	databaseType common.DatabaseType,
	columnType string,
) *accessTokenMigrationTestScript {
	t.Helper()
	db, script := openAccessTokenMigrationTestDB(t, databaseType, columnType)
	previousDB := DB
	previousType := common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(databaseType)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
	})
	return script
}

func runAccessTokenColumnMigrationWithFakeDialect(
	t *testing.T,
	databaseType common.DatabaseType,
	columnType string,
) []accessTokenMigrationTestEvent {
	t.Helper()
	script := useAccessTokenMigrationTestDB(t, databaseType, columnType)
	require.NoError(t, migrateUserAccessTokenColumnType())
	return script.snapshot()
}

func TestUserAccessTokenColumnMigrationStatements(t *testing.T) {
	testCases := []struct {
		name         string
		databaseType common.DatabaseType
		statements   []string
	}{
		{
			name:         "postgresql converts and trims atomically",
			databaseType: common.DatabaseTypePostgreSQL,
			statements: []string{
				`ALTER TABLE users ALTER COLUMN access_token TYPE varchar(128) ` +
					`USING NULLIF(RTRIM(access_token), '')`,
			},
		},
		{
			name:         "mysql converts before trimming",
			databaseType: common.DatabaseTypeMySQL,
			statements: []string{
				"ALTER TABLE users MODIFY COLUMN access_token VARCHAR(128)",
				"UPDATE users SET access_token = NULLIF(RTRIM(access_token), '') WHERE access_token LIKE '% '",
			},
		},
		{
			name:         "sqlite needs no type migration",
			databaseType: common.DatabaseTypeSQLite,
			statements:   nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.statements, userAccessTokenMigrationStatements(testCase.databaseType))
		})
	}
}

func TestUserAccessTokenColumnTypeDetection(t *testing.T) {
	assert.True(t, userAccessTokenColumnNeedsMigration(common.DatabaseTypePostgreSQL, "character"))
	assert.False(t, userAccessTokenColumnNeedsMigration(common.DatabaseTypePostgreSQL, "character varying"))
	assert.True(t, userAccessTokenColumnNeedsMigration(common.DatabaseTypeMySQL, "char(32)"))
	assert.True(t, userAccessTokenColumnNeedsMigration(common.DatabaseTypeMySQL, "CHAR(32)"))
	assert.False(t, userAccessTokenColumnNeedsMigration(common.DatabaseTypeMySQL, "varchar(128)"))
	assert.False(t, userAccessTokenColumnNeedsMigration(common.DatabaseTypeSQLite, "char(32)"))
}

func TestMigrateUserAccessTokenColumnTypeExecutesDialectSequence(t *testing.T) {
	testCases := []struct {
		name         string
		databaseType common.DatabaseType
		columnType   string
		metadataSQL  string
		mutations    []string
	}{
		{
			name:         "postgresql legacy character",
			databaseType: common.DatabaseTypePostgreSQL,
			columnType:   "character",
			metadataSQL:  "SELECT data_type FROM information_schema.columns",
			mutations: userAccessTokenMigrationStatements(
				common.DatabaseTypePostgreSQL,
			),
		},
		{
			name:         "mysql legacy char",
			databaseType: common.DatabaseTypeMySQL,
			columnType:   "char(32)",
			metadataSQL:  "SELECT COLUMN_TYPE FROM information_schema.columns",
			mutations: userAccessTokenMigrationStatements(
				common.DatabaseTypeMySQL,
			),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			events := runAccessTokenColumnMigrationWithFakeDialect(
				t,
				testCase.databaseType,
				testCase.columnType,
			)
			var executed []string
			metadataIndex := -1
			firstMutationIndex := -1
			for index, event := range events {
				if strings.Contains(event.query, testCase.metadataSQL) {
					metadataIndex = index
				}
				if event.kind == "exec" {
					if firstMutationIndex == -1 {
						firstMutationIndex = index
					}
					executed = append(executed, event.query)
				}
			}
			require.NotEqual(t, -1, metadataIndex)
			require.Greater(t, firstMutationIndex, metadataIndex)
			assert.Equal(t, testCase.mutations, executed)
		})
	}
}

func TestMigrateUserAccessTokenColumnTypeIsNoOpAfterConversion(t *testing.T) {
	testCases := []struct {
		name         string
		databaseType common.DatabaseType
		columnType   string
	}{
		{
			name:         "postgresql character varying",
			databaseType: common.DatabaseTypePostgreSQL,
			columnType:   "character varying",
		},
		{
			name:         "mysql varchar",
			databaseType: common.DatabaseTypeMySQL,
			columnType:   "varchar(128)",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			events := runAccessTokenColumnMigrationWithFakeDialect(
				t,
				testCase.databaseType,
				testCase.columnType,
			)
			for _, event := range events {
				assert.NotEqual(t, "exec", event.kind, "unexpected mutation: %s", event.query)
			}
		})
	}
}

func TestMigrateUserAccessTokenColumnTypeMySQLVarcharCleansOnlyDetectedResidue(t *testing.T) {
	script := useAccessTokenMigrationTestDB(t, common.DatabaseTypeMySQL, "varchar(128)")
	script.trailingPadding = true

	require.NoError(t, migrateUserAccessTokenColumnType())

	var executions []string
	for _, event := range script.snapshot() {
		if event.kind == "exec" {
			executions = append(executions, event.query)
		}
	}
	assert.Equal(t, []string{
		"UPDATE users SET access_token = NULLIF(RTRIM(access_token), '') WHERE access_token LIKE '% '",
	}, executions)
}

func TestMigrateUserAccessTokenColumnTypeFailsClosedOnResidueProbeError(t *testing.T) {
	script := useAccessTokenMigrationTestDB(t, common.DatabaseTypeMySQL, "varchar(128)")
	script.queryErrorContaining = "SELECT 1 FROM users WHERE access_token LIKE"

	err := migrateUserAccessTokenColumnType()
	require.ErrorContains(t, err, "failed to inspect users.access_token trailing padding")
	for _, event := range script.snapshot() {
		assert.NotEqual(t, "exec", event.kind, "unexpected mutation: %s", event.query)
	}
}

func TestMigrateUserAccessTokenColumnTypeRecoversMySQLInterruptedCleanup(t *testing.T) {
	script := useAccessTokenMigrationTestDB(t, common.DatabaseTypeMySQL, "char(32)")
	script.execErrorContaining = "UPDATE users SET access_token"

	err := migrateUserAccessTokenColumnType()
	require.ErrorContains(t, err, "scripted access-token migration exec failure")

	script.mu.Lock()
	script.columnType = "varchar(128)"
	script.trailingPadding = true
	script.execErrorContaining = ""
	script.events = nil
	script.mu.Unlock()

	require.NoError(t, migrateUserAccessTokenColumnType())
	events := script.snapshot()
	var executions []string
	var residueProbeSeen bool
	for _, event := range events {
		if strings.Contains(event.query, "SELECT 1 FROM users WHERE access_token LIKE") {
			residueProbeSeen = true
		}
		if event.kind == "exec" {
			executions = append(executions, event.query)
		}
	}
	assert.True(t, residueProbeSeen)
	assert.Equal(t, []string{
		"UPDATE users SET access_token = NULLIF(RTRIM(access_token), '') WHERE access_token LIKE '% '",
	}, executions)
}

func TestMigrateUserAccessTokenColumnTypeFailsClosedOnMissingMetadata(t *testing.T) {
	testCases := []struct {
		name                 string
		columnType           string
		queryErrorContaining string
	}{
		{name: "empty metadata"},
		{
			name:                 "metadata query error",
			queryErrorContaining: "SELECT COLUMN_TYPE FROM information_schema.columns",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			script := useAccessTokenMigrationTestDB(t, common.DatabaseTypeMySQL, testCase.columnType)
			script.queryErrorContaining = testCase.queryErrorContaining

			err := migrateUserAccessTokenColumnType()
			require.Error(t, err)
			for _, event := range script.snapshot() {
				assert.NotEqual(t, "exec", event.kind, "unexpected mutation: %s", event.query)
			}
		})
	}
}
