package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useSetupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousType := common.MainDatabaseType()
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared&_busy_timeout=5000",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Setup{}, &Option{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestCheckSetupMarksLegacySetupComplete(t *testing.T) {
	db := useSetupTestDB(t)
	constant.SetSetupCompleted(false)
	t.Cleanup(func() {
		constant.SetSetupCompleted(false)
	})
	require.NoError(t, db.Create(&Setup{
		ID: 7, Version: "legacy", InitializedAt: 1,
	}).Error)

	CheckSetup()

	assert.True(t, constant.SetupCompleted())
	var count int64
	require.NoError(t, db.Model(&Setup{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestCheckSetupRepairsHistoricalRootWithFixedSetupID(t *testing.T) {
	db := useSetupTestDB(t)
	constant.SetSetupCompleted(false)
	t.Cleanup(func() {
		constant.SetSetupCompleted(false)
	})
	require.NoError(t, db.Create(&Setup{
		ID: 7, Version: "deleted-legacy", InitializedAt: 1,
	}).Error)
	require.NoError(t, db.Delete(&Setup{}, 7).Error)
	require.NoError(t, db.Create(&User{
		Username: "historical-root", Password: "hash",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Callback().Create().Before("gorm:create").
		Register("setup:require-singleton-repair-id", func(tx *gorm.DB) {
			if tx.Statement.Table != "setups" {
				return
			}
			setup, ok := tx.Statement.Dest.(*Setup)
			if !ok || setup.ID != 1 {
				tx.AddError(errors.New("setup repair must use singleton ID 1"))
			}
		}))

	CheckSetup()

	assert.True(t, constant.SetupCompleted())
	var setup Setup
	require.NoError(t, db.First(&setup).Error)
	assert.Equal(t, uint(1), setup.ID)
}

func TestCheckSetupRepairsSoftDeletedHistoricalRoot(t *testing.T) {
	db := useSetupTestDB(t)
	constant.SetSetupCompleted(false)
	t.Cleanup(func() {
		constant.SetSetupCompleted(false)
	})
	root := User{
		Username: "historical-root", Password: "hash",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled,
		AffCode: "historical-root-code",
	}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Delete(&root).Error)

	CheckSetup()

	assert.True(t, constant.SetupCompleted())
	var setup Setup
	require.NoError(t, db.First(&setup).Error)
	assert.Equal(t, uint(1), setup.ID)
}

func TestCheckSetupKeepsEmptyDatabaseUninitialized(t *testing.T) {
	useSetupTestDB(t)
	constant.SetSetupCompleted(true)
	t.Cleanup(func() {
		constant.SetSetupCompleted(false)
	})

	CheckSetup()

	assert.False(t, constant.SetupCompleted())
}

type setupRollbackSeedingPool struct {
	*sql.DB
	afterRollback func()
}

func (pool *setupRollbackSeedingPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := pool.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &setupRollbackSeedingTx{
		Tx:            tx,
		afterRollback: pool.afterRollback,
	}, nil
}

type setupRollbackSeedingTx struct {
	*sql.Tx
	afterRollback func()
}

func (tx *setupRollbackSeedingTx) Rollback() error {
	err := tx.Tx.Rollback()
	tx.afterRollback()
	return err
}

func TestInitializeSystemPreservesDownstreamErrorWhenInitializedStateAppears(t *testing.T) {
	db := useSetupTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)

	downstreamErr := errors.New("forced downstream persistence failure")
	var seedErr error
	wrappedDB, err := gorm.Open(&sqlite.Dialector{
		Conn: &setupRollbackSeedingPool{
			DB: sqlDB,
			afterRollback: func() {
				seedErr = db.Create(&Setup{
					ID: 7, Version: "concurrent-winner", InitializedAt: 1,
				}).Error
				if seedErr != nil {
					return
				}
				seedErr = db.Create(&User{
					Username: "concurrent-root", Password: "hash",
					Role: common.RoleRootUser, Status: common.UserStatusEnabled,
				}).Error
			},
		},
	}, &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, wrappedDB.Callback().Create().Before("gorm:create").
		Register("setup:fail_option_persistence", func(tx *gorm.DB) {
			if tx.Statement.Table == "options" {
				tx.AddError(downstreamErr)
			}
		}))
	DB = wrappedDB

	err = InitializeSystem(SetupInitializationParams{
		RootUser: User{
			Username: "failed-root", Password: "hash",
			Role: common.RoleRootUser, Status: common.UserStatusEnabled,
		},
		SelfUseModeEnabled: true,
		DemoSiteEnabled:    true,
	})

	require.NoError(t, seedErr)
	require.Same(t, downstreamErr, err)
	var setup Setup
	require.NoError(t, db.First(&setup).Error)
	assert.Equal(t, uint(7), setup.ID)
}

func TestInitializeSystemCommitsSetupRootAndOptions(t *testing.T) {
	db := useSetupTestDB(t)
	params := SetupInitializationParams{
		RootUser: User{
			Username: "root-owner", Password: "hashed-password",
			Role: common.RoleRootUser, Status: common.UserStatusEnabled,
			DisplayName: "Root User", Quota: 100000000,
		},
		SelfUseModeEnabled: true,
		DemoSiteEnabled:    false,
	}

	require.NoError(t, InitializeSystem(params))

	var setup Setup
	require.NoError(t, db.First(&setup).Error)
	assert.Equal(t, uint(1), setup.ID)
	assert.Equal(t, common.Version, setup.Version)
	assert.Positive(t, setup.InitializedAt)

	var root User
	require.NoError(t, db.Where("role = ?", common.RoleRootUser).First(&root).Error)
	assert.Equal(t, "root-owner", root.Username)
	assert.Len(t, root.AffCode, common.InviteCodeLength)
	inviterID, err := GetUserIdByAffCode(root.AffCode)
	require.NoError(t, err)
	assert.Equal(t, root.Id, inviterID)

	expectedOptions := map[string]string{
		"SelfUseModeEnabled": "true",
		"DemoSiteEnabled":    "false",
	}
	for key, expected := range expectedOptions {
		var option Option
		require.NoError(t, db.Where("key = ?", key).First(&option).Error)
		assert.Equal(t, expected, option.Value)
	}
}

func TestInitializeSystemRollsBackWhenRootAffCodeConflicts(t *testing.T) {
	db := useSetupTestDB(t)
	require.NoError(t, db.Create(&User{
		Username: "existing-user",
		Password: "hash",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		AffCode:  "duplicate-aff-code",
	}).Error)

	err := InitializeSystem(SetupInitializationParams{
		RootUser: User{
			Username: "root-owner",
			Password: "hashed-password",
			Role:     common.RoleRootUser,
			Status:   common.UserStatusEnabled,
			AffCode:  "duplicate-aff-code",
		},
		SelfUseModeEnabled: true,
		DemoSiteEnabled:    false,
	})
	require.Error(t, err)

	var setupCount, rootCount, optionCount int64
	require.NoError(t, db.Model(&Setup{}).Count(&setupCount).Error)
	require.NoError(t, db.Model(&User{}).
		Where("role = ?", common.RoleRootUser).Count(&rootCount).Error)
	require.NoError(t, db.Model(&Option{}).Count(&optionCount).Error)
	assert.Zero(t, setupCount)
	assert.Zero(t, rootCount)
	assert.Zero(t, optionCount)
}

func TestInitializeSystemRejectsExistingSetupOrRoot(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, db *gorm.DB)
	}{
		{
			name: "legacy setup id",
			seed: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Create(&Setup{
					ID: 7, Version: "legacy", InitializedAt: 1,
				}).Error)
			},
		},
		{
			name: "root without setup",
			seed: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Create(&User{
					Username: "existing-root", Password: "hash",
					Role: common.RoleRootUser, Status: common.UserStatusEnabled,
				}).Error)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := useSetupTestDB(t)
			test.seed(t, db)
			err := InitializeSystem(SetupInitializationParams{
				RootUser: User{
					Username: "attacker", Password: "hash",
					Role: common.RoleRootUser, Status: common.UserStatusEnabled,
				},
				SelfUseModeEnabled: true,
				DemoSiteEnabled:    true,
			})
			require.ErrorIs(t, err, ErrSystemAlreadyInitialized)

			var optionCount int64
			require.NoError(t, db.Model(&Option{}).Count(&optionCount).Error)
			assert.Zero(t, optionCount)
		})
	}
}

func TestInitializeSystemRejectsSoftDeletedRoot(t *testing.T) {
	db := useSetupTestDB(t)
	root := User{
		Username: "historical-root", Password: "hash",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled,
		AffCode: "historical-root-code",
	}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Delete(&root).Error)

	err := InitializeSystem(SetupInitializationParams{
		RootUser: User{
			Username: "replacement-root", Password: "hash",
			Role: common.RoleRootUser, Status: common.UserStatusEnabled,
			AffCode: "replacement-root-code",
		},
		SelfUseModeEnabled: true,
		DemoSiteEnabled:    true,
	})

	require.ErrorIs(t, err, ErrSystemAlreadyInitialized)
	var setupCount, optionCount int64
	require.NoError(t, db.Model(&Setup{}).Count(&setupCount).Error)
	require.NoError(t, db.Model(&Option{}).Count(&optionCount).Error)
	assert.Zero(t, setupCount)
	assert.Zero(t, optionCount)
}

func TestInitializeSystemRollsBackClaimWhenRootCreationFails(t *testing.T) {
	db := useSetupTestDB(t)
	require.NoError(t, db.Create(&User{
		Username: "duplicate-name", Password: "existing",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		AffCode: "existing-user-code",
	}).Error)

	err := InitializeSystem(SetupInitializationParams{
		RootUser: User{
			Username: "duplicate-name", Password: "new",
			Role: common.RoleRootUser, Status: common.UserStatusEnabled,
		},
		SelfUseModeEnabled: true,
		DemoSiteEnabled:    true,
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrSystemAlreadyInitialized)

	var setupCount, optionCount, rootCount int64
	require.NoError(t, db.Model(&Setup{}).Count(&setupCount).Error)
	require.NoError(t, db.Model(&Option{}).Count(&optionCount).Error)
	require.NoError(t, db.Model(&User{}).
		Where("role = ?", common.RoleRootUser).Count(&rootCount).Error)
	assert.Zero(t, setupCount)
	assert.Zero(t, optionCount)
	assert.Zero(t, rootCount)
}

func TestInitializeSystemRollsBackRootWhenOptionPersistenceFails(t *testing.T) {
	db := useSetupTestDB(t)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_demo_option
		BEFORE INSERT ON options
		WHEN NEW.key = 'DemoSiteEnabled'
		BEGIN
			SELECT RAISE(FAIL, 'forced option persistence failure');
		END
	`).Error)

	err := InitializeSystem(SetupInitializationParams{
		RootUser: User{
			Username: "rolled-back-root", Password: "hash",
			Role: common.RoleRootUser, Status: common.UserStatusEnabled,
		},
		SelfUseModeEnabled: true,
		DemoSiteEnabled:    true,
	})
	require.Error(t, err)

	var setupCount, optionCount, rootCount int64
	require.NoError(t, db.Model(&Setup{}).Count(&setupCount).Error)
	require.NoError(t, db.Model(&Option{}).Count(&optionCount).Error)
	require.NoError(t, db.Model(&User{}).
		Where("role = ?", common.RoleRootUser).Count(&rootCount).Error)
	assert.Zero(t, setupCount)
	assert.Zero(t, optionCount)
	assert.Zero(t, rootCount)
}

func TestInitializeSystemAllowsExactlyOneConcurrentWinner(t *testing.T) {
	db := useSetupTestDB(t)
	const callers = 8
	type initializationResult struct {
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan initializationResult, callers)
	var workers sync.WaitGroup

	for i := 0; i < callers; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			err := InitializeSystem(SetupInitializationParams{
				RootUser: User{
					Username: fmt.Sprintf("root-%d", index),
					Password: "hash", Role: common.RoleRootUser,
					Status: common.UserStatusEnabled,
				},
				SelfUseModeEnabled: index%2 == 0,
				DemoSiteEnabled:    index%2 != 0,
			})
			results <- initializationResult{index: index, err: err}
		}(i)
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
	assert.Equal(t, 1, successes)
	assert.Equal(t, callers-1, alreadyInitialized)

	var setupCount, rootCount int64
	require.NoError(t, db.Model(&Setup{}).Count(&setupCount).Error)
	require.NoError(t, db.Model(&User{}).
		Where("role = ?", common.RoleRootUser).Count(&rootCount).Error)
	assert.Equal(t, int64(1), setupCount)
	assert.Equal(t, int64(1), rootCount)

	winningOptions := map[string]string{
		"SelfUseModeEnabled": strconv.FormatBool(winner%2 == 0),
		"DemoSiteEnabled":    strconv.FormatBool(winner%2 != 0),
	}
	for key, expected := range winningOptions {
		var option Option
		require.NoError(t, db.Where("key = ?", key).First(&option).Error)
		assert.Equal(t, expected, option.Value)
	}
}
