package model

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useDashboardAccessTokenTestDB(t *testing.T) *gorm.DB {
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
	require.NoError(t, db.AutoMigrate(&User{}, &DashboardAccessToken{}, &UserSession{}, &AuthFlow{}))
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func createDashboardAccessTokenTestUser(t *testing.T, db *gorm.DB, suffix string) *User {
	t.Helper()
	user := &User{
		Username:    "pat-user-" + suffix,
		Password:    "password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AffCode:     "pat-aff-" + suffix,
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func TestDashboardAccessTokenRotationPersistsOnlyHash(t *testing.T) {
	db := useDashboardAccessTokenTestDB(t)
	user := createDashboardAccessTokenTestUser(t, db, "hash")

	before := time.Now().Unix()
	raw, expiresAt, err := RotateDashboardAccessToken(user.Id, user.AuthVersion)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(raw, "pat_"))
	assert.Len(t, raw, 68)
	assert.GreaterOrEqual(t, expiresAt, before+int64(DashboardAccessTokenTTL/time.Second)-1)

	var record DashboardAccessToken
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&record).Error)
	assert.NotEqual(t, raw, record.TokenHash)
	assert.Equal(t, dashboardAccessTokenHash(raw), record.TokenHash)
	assert.Equal(t, raw[:12], record.Prefix)
	assert.Equal(t, DashboardAccessTokenScopeUserAPI, record.Scope)
	assert.Equal(t, user.AuthVersion, record.UserAuthVersion)
	assert.Nil(t, record.RevokedAt)

	authenticated, err := ValidateDashboardAccessToken(raw)
	require.NoError(t, err)
	require.NotNil(t, authenticated)
	assert.Equal(t, user.Id, authenticated.Id)

	unmatched, err := ValidateDashboardAccessToken(raw + "x")
	require.NoError(t, err)
	assert.Nil(t, unmatched)
}

func TestDashboardAccessTokenRejectsInvalidLifecycleState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, db *gorm.DB, user *User, record *DashboardAccessToken)
	}{
		{
			name: "expired",
			mutate: func(t *testing.T, db *gorm.DB, _ *User, record *DashboardAccessToken) {
				require.NoError(t, db.Model(record).Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)
			},
		},
		{
			name: "revoked",
			mutate: func(t *testing.T, db *gorm.DB, _ *User, record *DashboardAccessToken) {
				now := time.Now().Unix()
				require.NoError(t, db.Model(record).Update("revoked_at", now).Error)
			},
		},
		{
			name: "wrong scope",
			mutate: func(t *testing.T, db *gorm.DB, _ *User, record *DashboardAccessToken) {
				require.NoError(t, db.Model(record).Update("scope", "admin").Error)
			},
		},
		{
			name: "stale auth version",
			mutate: func(t *testing.T, db *gorm.DB, user *User, _ *DashboardAccessToken) {
				require.NoError(t, db.Model(user).Update("auth_version", user.AuthVersion+1).Error)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := useDashboardAccessTokenTestDB(t)
			user := createDashboardAccessTokenTestUser(t, db, strings.ReplaceAll(test.name, " ", "-"))
			raw, _, err := RotateDashboardAccessToken(user.Id, user.AuthVersion)
			require.NoError(t, err)
			var record DashboardAccessToken
			require.NoError(t, db.Where("user_id = ?", user.Id).First(&record).Error)
			test.mutate(t, db, user, &record)

			authenticated, err := ValidateDashboardAccessToken(raw)
			require.Error(t, err)
			assert.Nil(t, authenticated)
		})
	}
}

func TestDashboardAccessTokenConcurrentRotationLeavesOneValidToken(t *testing.T) {
	db := useDashboardAccessTokenTestDB(t)
	user := createDashboardAccessTokenTestUser(t, db, "concurrent")

	const workers = 8
	start := make(chan struct{})
	results := make(chan string, workers)
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			raw, _, err := RotateDashboardAccessToken(user.Id, user.AuthVersion)
			if err != nil {
				errors <- err
				return
			}
			results <- raw
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)

	for err := range errors {
		require.NoError(t, err)
	}
	valid := 0
	for raw := range results {
		authenticated, err := ValidateDashboardAccessToken(raw)
		if err == nil && authenticated != nil {
			valid++
		}
	}
	assert.Equal(t, 1, valid)
}

func TestInitializeDashboardAccessTokensMigratesPlaintextIdempotently(t *testing.T) {
	db := useDashboardAccessTokenTestDB(t)
	user := createDashboardAccessTokenTestUser(t, db, "legacy")
	legacyRaw := "legacy-dashboard-access-token"
	require.NoError(t, db.Model(user).Update("access_token", legacyRaw).Error)

	require.NoError(t, InitializeDashboardAccessTokens())
	require.NoError(t, InitializeDashboardAccessTokens())

	var migrated User
	require.NoError(t, db.First(&migrated, user.Id).Error)
	assert.Nil(t, migrated.AccessToken)

	var records []DashboardAccessToken
	require.NoError(t, db.Where("user_id = ?", user.Id).Find(&records).Error)
	require.Len(t, records, 1)
	assert.Equal(t, dashboardAccessTokenHash(legacyRaw), records[0].TokenHash)
	assert.Equal(t, DashboardAccessTokenScopeUserAPI, records[0].Scope)
	assert.Equal(t, user.AuthVersion, records[0].UserAuthVersion)

	authenticated, err := ValidateDashboardAccessToken(legacyRaw)
	require.NoError(t, err)
	require.NotNil(t, authenticated)
	assert.Equal(t, user.Id, authenticated.Id)
}

func TestInitializeDashboardAccessTokensRemovesLegacyCharPadding(t *testing.T) {
	db := useDashboardAccessTokenTestDB(t)
	user := createDashboardAccessTokenTestUser(t, db, "legacy-char-padding")
	legacyRaw := "legacy-dashboard-access-token"
	require.NoError(t, db.Model(user).Update("access_token", legacyRaw+"   ").Error)

	require.NoError(t, InitializeDashboardAccessTokens())

	var migrated User
	require.NoError(t, db.Select("id", "access_token").First(&migrated, user.Id).Error)
	assert.Nil(t, migrated.AccessToken)

	var record DashboardAccessToken
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&record).Error)
	assert.Equal(t, dashboardAccessTokenHash(legacyRaw), record.TokenHash)

	authenticated, err := ValidateDashboardAccessToken(legacyRaw)
	require.NoError(t, err)
	require.NotNil(t, authenticated)
	assert.Equal(t, user.Id, authenticated.Id)
}

func TestValidateDashboardAccessTokenRepairsPostgreSQLPaddedHash(t *testing.T) {
	db := useDashboardAccessTokenTestDB(t)
	user := createDashboardAccessTokenTestUser(t, db, "legacy-padded-hash")
	legacyRaw := "1234567890123456789012345678"
	require.Len(t, legacyRaw, 28)
	now := common.GetTimestamp()
	record := DashboardAccessToken{
		UserId:          user.Id,
		TokenHash:       dashboardAccessTokenHash(legacyRaw + "    "),
		Prefix:          legacyRaw[:dashboardAccessTokenPrefixLength],
		Scope:           DashboardAccessTokenScopeUserAPI,
		UserAuthVersion: user.AuthVersion,
		CreatedAt:       now,
		ExpiresAt:       now + int64(DashboardAccessTokenTTL/time.Second),
		UpdatedAt:       now,
	}
	require.NoError(t, db.Create(&record).Error)

	authenticated, err := ValidateDashboardAccessToken(legacyRaw)
	require.NoError(t, err)
	require.NotNil(t, authenticated)
	assert.Equal(t, user.Id, authenticated.Id)

	var repaired DashboardAccessToken
	require.NoError(t, db.First(&repaired, record.Id).Error)
	assert.Equal(t, dashboardAccessTokenHash(legacyRaw), repaired.TokenHash)
	assert.NotEqual(t, dashboardAccessTokenHash(legacyRaw+"    "), repaired.TokenHash)

	authenticated, err = ValidateDashboardAccessToken(legacyRaw)
	require.NoError(t, err)
	require.NotNil(t, authenticated)
	assert.Equal(t, user.Id, authenticated.Id)
}

func TestValidateDashboardAccessTokenDoesNotTryPaddedHashForOtherLengths(t *testing.T) {
	db := useDashboardAccessTokenTestDB(t)
	user := createDashboardAccessTokenTestUser(t, db, "nonlegacy-padded-hash")
	raw := "12345678901234567890123456789"
	require.Len(t, raw, 29)
	now := common.GetTimestamp()
	record := DashboardAccessToken{
		UserId:          user.Id,
		TokenHash:       dashboardAccessTokenHash(raw + "   "),
		Prefix:          raw[:dashboardAccessTokenPrefixLength],
		Scope:           DashboardAccessTokenScopeUserAPI,
		UserAuthVersion: user.AuthVersion,
		CreatedAt:       now,
		ExpiresAt:       now + int64(DashboardAccessTokenTTL/time.Second),
		UpdatedAt:       now,
	}
	require.NoError(t, db.Create(&record).Error)

	authenticated, err := ValidateDashboardAccessToken(raw)
	require.NoError(t, err)
	assert.Nil(t, authenticated)

	var unchanged DashboardAccessToken
	require.NoError(t, db.First(&unchanged, record.Id).Error)
	assert.Equal(t, dashboardAccessTokenHash(raw+"   "), unchanged.TokenHash)
}

func TestMigrateLegacyDashboardAccessTokenDoesNotClearReplacement(t *testing.T) {
	db := useDashboardAccessTokenTestDB(t)
	user := createDashboardAccessTokenTestUser(t, db, "legacy-replaced")
	replacement := "replacement-dashboard-access-token"
	require.NoError(t, db.Model(user).Update("access_token", replacement).Error)

	err := db.Transaction(func(tx *gorm.DB) error {
		return migrateLegacyDashboardAccessToken(
			tx,
			user.Id,
			user.AuthVersion,
			"stale-dashboard-access-token",
		)
	})
	require.ErrorIs(t, err, errDashboardAccessTokenMigrationChanged)

	var stored User
	require.NoError(t, db.Select("id", "access_token").First(&stored, user.Id).Error)
	require.NotNil(t, stored.AccessToken)
	assert.Equal(t, replacement, *stored.AccessToken)

	var recordCount int64
	require.NoError(t, db.Model(&DashboardAccessToken{}).Where("user_id = ?", user.Id).Count(&recordCount).Error)
	assert.Zero(t, recordCount, "the stale hash insert and failed conditional clear must roll back together")
}

func TestMigrateLegacyDashboardAccessTokenClearsWhitespaceOnlyValue(t *testing.T) {
	db := useDashboardAccessTokenTestDB(t)
	user := createDashboardAccessTokenTestUser(t, db, "legacy-whitespace")
	require.NoError(t, db.Model(user).Update("access_token", "   ").Error)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return migrateLegacyDashboardAccessToken(tx, user.Id, user.AuthVersion, "   ")
	}))

	var stored User
	require.NoError(t, db.Select("id", "access_token").First(&stored, user.Id).Error)
	assert.Nil(t, stored.AccessToken)

	var recordCount int64
	require.NoError(t, db.Model(&DashboardAccessToken{}).Where("user_id = ?", user.Id).Count(&recordCount).Error)
	assert.Zero(t, recordCount)
}

func TestRevokeDashboardAccessTokenInvalidatesRawToken(t *testing.T) {
	db := useDashboardAccessTokenTestDB(t)
	user := createDashboardAccessTokenTestUser(t, db, "revoke")
	raw, _, err := RotateDashboardAccessToken(user.Id, user.AuthVersion)
	require.NoError(t, err)

	require.NoError(t, RevokeDashboardAccessToken(user.Id, user.AuthVersion))

	authenticated, err := ValidateDashboardAccessToken(raw)
	require.ErrorIs(t, err, ErrDashboardAccessTokenRevoked)
	assert.Nil(t, authenticated)
}

func TestPasswordResetFlowInvalidatesDashboardAccessToken(t *testing.T) {
	db := useDashboardAccessTokenTestDB(t)
	user := createDashboardAccessTokenTestUser(t, db, "password-reset")
	user.Email = "password-reset-token@example.com"
	require.NoError(t, db.Model(user).Update("email", user.Email).Error)
	raw, _, err := RotateDashboardAccessToken(user.Id, user.AuthVersion)
	require.NoError(t, err)
	resetToken, _, err := CreatePasswordResetFlow(user)
	require.NoError(t, err)

	require.NoError(t, ResetUserPasswordWithFlow(resetToken, "NewPassword123"))

	authenticated, err := ValidateDashboardAccessToken(raw)
	assert.ErrorIs(t, err, ErrDashboardAccessTokenInvalid)
	assert.Nil(t, authenticated)
}
