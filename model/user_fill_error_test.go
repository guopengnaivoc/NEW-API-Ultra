package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useUserFillTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousType := common.MainDatabaseType()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&User{}))
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

// FillUserBy* must keep treating "no such user" as a non-error (callers
// detect absence via user.Id == 0), while real database failures must
// surface instead of being swallowed.
func TestFillUserByIdMissingUserIsNotAnError(t *testing.T) {
	useUserFillTestDB(t)

	user := &User{Id: 12345}
	require.NoError(t, user.FillUserById())
	require.Equal(t, 12345, user.Id)
	require.Empty(t, user.Username)
}

func TestFillUserByIdPropagatesDatabaseError(t *testing.T) {
	db := useUserFillTestDB(t)
	require.NoError(t, db.Migrator().DropTable(&User{}))

	user := &User{Id: 1}
	err := user.FillUserById()
	require.Error(t, err)
	require.NotErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestFillUserByWeChatIdPropagatesDatabaseError(t *testing.T) {
	db := useUserFillTestDB(t)
	require.NoError(t, db.Migrator().DropTable(&User{}))

	user := &User{WeChatId: "wx-1"}
	require.Error(t, user.FillUserByWeChatId())
}
