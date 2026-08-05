package model

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type setupExternalDatabase struct {
	name         string
	envName      string
	databaseType common.DatabaseType
	dialector    func(string) gorm.Dialector
}

func TestInitializeSystemConcurrentWinnerConfiguredDatabases(t *testing.T) {
	databases := []setupExternalDatabase{
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
			if dsn == "" {
				t.Skip(database.envName + " is not configured")
			}
			testInitializeSystemConcurrentWinnerOnExternalDatabase(t, database, dsn)
		})
	}
}

func testInitializeSystemConcurrentWinnerOnExternalDatabase(
	t *testing.T,
	database setupExternalDatabase,
	dsn string,
) {
	t.Helper()

	dialectMarker := string(database.name[0])
	tablePrefix := fmt.Sprintf(
		"na1_%s_%x_%x_",
		dialectMarker,
		os.Getpid(),
		time.Now().UnixNano(),
	)
	namingStrategy := schema.NamingStrategy{TablePrefix: tablePrefix}
	db, err := gorm.Open(database.dialector(dsn), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: namingStrategy,
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(16)
	sqlDB.SetMaxIdleConns(16)

	models := []struct {
		value         any
		expectedTable string
	}{
		{value: &User{}, expectedTable: tablePrefix + "users"},
		{value: &Setup{}, expectedTable: tablePrefix + "setups"},
		{value: &Option{}, expectedTable: tablePrefix + "options"},
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

	t.Cleanup(func() {
		for index := len(models) - 1; index >= 0; index-- {
			model := models[index]
			statement := &gorm.Statement{DB: db}
			if err := statement.Parse(model.value); err != nil {
				t.Errorf("refusing cleanup after table-name resolution failed: %v", err)
				continue
			}
			if statement.Schema.Table != model.expectedTable ||
				!strings.HasPrefix(statement.Schema.Table, tablePrefix) {
				t.Errorf("refusing to drop table outside test prefix: %q", statement.Schema.Table)
				continue
			}
			assert.NoError(t, db.Migrator().DropTable(model.value))
			assert.False(t, db.Migrator().HasTable(model.value))
		}
		assert.NoError(t, sqlDB.Close())
	})

	require.NoError(t, db.AutoMigrate(&User{}, &Setup{}, &Option{}))
	logExternalSetupDatabaseMetadata(t, database.name, db)

	previousDB := DB
	previousType := common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(database.databaseType)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
	})

	const callers = 12
	type initializationResult struct {
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan initializationResult, callers)
	var workers sync.WaitGroup
	for index := 0; index < callers; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			err := InitializeSystem(SetupInitializationParams{
				RootUser: User{
					Username: fmt.Sprintf("external-root-%02d", index),
					Password: "hashed-password",
					Role:     common.RoleRootUser,
					Status:   common.UserStatusEnabled,
				},
				SelfUseModeEnabled: index%2 == 0,
				DemoSiteEnabled:    index%3 == 0,
			})
			results <- initializationResult{index: index, err: err}
		}(index)
	}

	close(start)
	workers.Wait()
	close(results)

	successes := 0
	alreadyInitialized := 0
	winner := -1
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result.index
		case errors.Is(result.err, ErrSystemAlreadyInitialized):
			alreadyInitialized++
		default:
			require.NoError(t, result.err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, callers-1, alreadyInitialized)
	require.NotEqual(t, -1, winner)

	var setups []Setup
	require.NoError(t, db.Find(&setups).Error)
	require.Len(t, setups, 1)
	assert.Equal(t, uint(1), setups[0].ID)

	var roots []User
	require.NoError(t, db.Unscoped().
		Where("role = ?", common.RoleRootUser).
		Find(&roots).Error)
	require.Len(t, roots, 1)
	assert.Equal(t, fmt.Sprintf("external-root-%02d", winner), roots[0].Username)

	var options []Option
	require.NoError(t, db.Find(&options).Error)
	require.Len(t, options, 2)
	persistedOptions := make(map[string]string, len(options))
	for _, option := range options {
		persistedOptions[option.Key] = option.Value
	}
	assert.Equal(t, map[string]string{
		"SelfUseModeEnabled": strconv.FormatBool(winner%2 == 0),
		"DemoSiteEnabled":    strconv.FormatBool(winner%3 == 0),
	}, persistedOptions)

	t.Logf(
		"%s real-database concurrency: %d success, %d already initialized, winner=%d, prefix=%s",
		database.name,
		successes,
		alreadyInitialized,
		winner,
		tablePrefix,
	)
}

func logExternalSetupDatabaseMetadata(t *testing.T, dialect string, db *gorm.DB) {
	t.Helper()

	var version string
	switch dialect {
	case "mysql":
		if err := db.Raw("SELECT VERSION()").Scan(&version).Error; err != nil {
			t.Logf("mysql server version unavailable: %v", err)
		} else {
			t.Logf("mysql server version: %s", version)
		}

		var isolation string
		err := db.Raw("SELECT @@transaction_isolation").Scan(&isolation).Error
		if err != nil {
			err = db.Raw("SELECT @@tx_isolation").Scan(&isolation).Error
		}
		if err != nil {
			t.Logf("mysql transaction isolation unavailable: %v", err)
		} else {
			t.Logf("mysql transaction isolation: %s", isolation)
		}
	case "postgres":
		if err := db.Raw("SHOW server_version").Scan(&version).Error; err != nil {
			t.Logf("postgres server version unavailable: %v", err)
		} else {
			t.Logf("postgres server version: %s", version)
		}

		var isolation string
		if err := db.Raw("SHOW transaction_isolation").Scan(&isolation).Error; err != nil {
			t.Logf("postgres transaction isolation unavailable: %v", err)
		} else {
			t.Logf("postgres transaction isolation: %s", isolation)
		}
	}
}
