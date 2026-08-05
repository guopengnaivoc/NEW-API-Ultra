package model

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/proto"
	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type forcedSlowGormLogger struct {
	gormlogger.Interface
}

func (l *forcedSlowGormLogger) ParamsFilter(
	ctx context.Context,
	sql string,
	params ...interface{},
) (string, []interface{}) {
	filter, ok := l.Interface.(gorm.ParamsFilter)
	if !ok {
		return sql, params
	}
	return filter.ParamsFilter(ctx, sql, params...)
}

func (l *forcedSlowGormLogger) Trace(
	ctx context.Context,
	begin time.Time,
	fc func() (string, int64),
	err error,
) {
	l.Interface.Trace(ctx, begin.Add(-time.Hour), fc, err)
}

// 保护契约:数据库驱动错误消息可能内联数据值,非 DEBUG 下日志只保留错误码。
func TestSanitizeDBErrorStripsDriverMessage(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   string
		leaked string
	}{
		{
			name:   "mysql duplicate entry",
			err:    &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'secret-value' for key 'users.idx'"},
			want:   "mysql error 1062",
			leaked: "secret-value",
		},
		{
			name:   "postgres unique violation",
			err:    &pgconn.PgError{Code: "23505", Message: "duplicate key value", Detail: "Key (k)=(secret-value) already exists."},
			want:   "postgres error SQLSTATE 23505",
			leaked: "secret-value",
		},
		{
			name:   "clickhouse exception",
			err:    &proto.Exception{Code: 241, Message: "Memory limit exceeded while processing 'secret-value'"},
			want:   "clickhouse error 241",
			leaked: "secret-value",
		},
		{
			name:   "wrapped driver error",
			err:    fmt.Errorf("exec failed: %w", &mysql.MySQLError{Number: 1064, Message: "syntax error near 'secret-value'"}),
			want:   "mysql error 1064",
			leaked: "secret-value",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeDBError(tc.err)
			require.Error(t, got)
			assert.Equal(t, tc.want, got.Error())
			assert.NotContains(t, got.Error(), tc.leaked)
		})
	}
}

func TestSanitizeDBErrorSQLiteDriver(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	execErr := db.Exec("INSERT INTO missing_table (k) VALUES (?)", "secret-value").Error
	require.Error(t, execErr)

	got := sanitizeDBError(execErr)
	assert.Regexp(t, `^sqlite error \d+$`, got.Error())
	assert.NotContains(t, got.Error(), "secret-value")
}

func TestSanitizeDBErrorKeepsNonDriverErrors(t *testing.T) {
	err := fmt.Errorf("dial tcp 127.0.0.1:3306: connect: connection refused")
	assert.Equal(t, err, sanitizeDBError(err))
}

// 保护契约:经 gorm 真实链路,错误日志在所有模式下同时满足 SQL 参数化、
// 驱动错误脱敏、调用点归因到业务代码。
func TestGormLoggerEndToEndSanitizedOutput(t *testing.T) {
	previousDebug := common.DebugEnabled
	t.Cleanup(func() { common.DebugEnabled = previousDebug })

	execQuery := func() string {
		var buf bytes.Buffer
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: newGormLogger(&buf)})
		require.NoError(t, err)
		db.Exec("SELECT * FROM missing_table WHERE k = ?", "secret-value")
		return buf.String()
	}

	common.DebugEnabled = false
	out := execQuery()
	assert.Contains(t, out, "k = ?")
	assert.NotContains(t, out, "secret-value")
	assert.Contains(t, out, "sqlite error")
	assert.Contains(t, out, "gorm_logger_test.go")

	common.DebugEnabled = true
	debugOut := execQuery()
	assert.Contains(t, debugOut, "k = ?")
	assert.NotContains(t, debugOut, "secret-value")
	assert.Contains(t, debugOut, "sqlite error")
	assert.NotContains(t, debugOut, "no such table")
}

func TestGormLoggerDebugProviderResultWritesRemainValueFree(t *testing.T) {
	previousDebug := common.DebugEnabled
	common.DebugEnabled = true
	t.Cleanup(func() { common.DebugEnabled = previousDebug })
	t.Setenv("SQL_SLOW_THRESHOLD_MS", "1")

	const (
		operationSentinel   = "sql-operation-private-sentinel"
		fingerprintSentinel = "sql-fingerprint-private-sentinel"
		upstreamSentinel    = "sql-upstream-private-sentinel"
		uriSentinel         = "https://sql-provider-private.example.test/video" +
			"?signed=sql-query-private-sentinel"
		keySentinel      = "sql-key-private-sentinel"
		envelopeSentinel = "naenc:v1:sql-private-key:" +
			"sql-private-nonce:sql-private-ciphertext"
		driverSentinel = "sql-driver-error-private-sentinel"
	)

	newPrivateTask := func() *Task {
		envelope := envelopeSentinel
		return &Task{
			TaskID:   "task_sql_logger_public",
			Platform: "gemini",
			Status:   TaskStatusInProgress,
			Progress: "50%",
			Data: []byte(
				`{"operation":"` + operationSentinel +
					`","uri":"` + uriSentinel + `"}`,
			),
			PrivateData: TaskPrivateData{
				Key:                   keySentinel,
				ChannelKeyFingerprint: fingerprintSentinel,
				UpstreamTaskID:        upstreamSentinel,
			},
			EncryptedProviderResultURI: &envelope,
		}
	}
	forbidden := []string{
		operationSentinel,
		fingerprintSentinel,
		upstreamSentinel,
		uriSentinel,
		"sql-query-private-sentinel",
		keySentinel,
		envelopeSentinel,
		"naenc:v1",
	}

	t.Run("forced slow query", func(t *testing.T) {
		var output bytes.Buffer
		productionLogger := newGormLogger(&output)
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
			Logger: &forcedSlowGormLogger{Interface: productionLogger},
		})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&Task{}))
		output.Reset()

		require.NoError(t, db.Create(newPrivateTask()).Error)

		logged := output.String()
		assert.Contains(t, logged, "SLOW SQL")
		assert.Contains(t, logged, "provider_result_uri")
		assert.Contains(t, logged, "?")
		for _, value := range forbidden {
			assert.NotContains(t, logged, value)
		}
	})

	t.Run("forced database error", func(t *testing.T) {
		var output bytes.Buffer
		productionLogger := newGormLogger(&output)
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
			Logger: productionLogger,
		})
		require.NoError(t, err)

		err = db.Table("missing_provider_result_uri_table").
			Create(newPrivateTask()).Error
		require.Error(t, err)
		logged := output.String()
		assert.Contains(t, logged, "sqlite error")
		assert.Contains(t, logged, "provider_result_uri")
		assert.Contains(t, logged, "?")
		for _, value := range forbidden {
			assert.NotContains(t, logged, value)
		}

		output.Reset()
		driverErr := &mysql.MySQLError{
			Number:  1062,
			Message: driverSentinel + " " + envelopeSentinel,
		}
		productionLogger.Trace(
			context.Background(),
			time.Now(),
			func() (string, int64) {
				return "UPDATE tasks SET provider_result_uri = ? WHERE id = ?", 0
			},
			driverErr,
		)
		driverLog := output.String()
		assert.Contains(t, driverLog, "mysql error 1062")
		assert.NotContains(t, driverLog, driverSentinel)
		assert.NotContains(t, driverLog, envelopeSentinel)
		assert.NotContains(t, driverLog, "naenc:v1")
	})
}
