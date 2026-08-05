package model

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testUserSessionIdleTimeoutSeconds = int64(7 * 24 * 60 * 60)

func TestUserSessionIdleBoundaryRejectsCurrentRefreshWithoutRotating(t *testing.T) {
	setupUserSessionTest(t)
	const now = int64(2_000_000_000)

	tests := []struct {
		name       string
		offset     int64
		wantActive bool
	}{
		{name: "deadline minus one", offset: -1, wantActive: true},
		{name: "deadline", offset: 0, wantActive: false},
		{name: "deadline plus one", offset: 1, wantActive: false},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			userID := 1200 + index
			createUserSessionTestUser(t, userID, 1)
			session := newTestUserSession(fmt.Sprintf("idle-current-%d", index), userID, now-testUserSessionIdleTimeoutSeconds-test.offset)
			session.ExpiresAt = now + 3600
			originalHash := session.RefreshHash
			originalLastActiveAt := session.LastActiveAt
			originalExpiresAt := session.ExpiresAt
			require.NoError(t, DB.Create(session).Error)

			rotated, err := RotateUserSessionRefresh(
				userID,
				session.SID,
				originalHash,
				fmt.Sprintf("next-idle-current-%d", index),
				now,
				30*time.Second,
			)

			if test.wantActive {
				require.NoError(t, err)
				require.NotNil(t, rotated)
				assert.Equal(t, now, rotated.LastActiveAt)
				assert.Equal(t, originalExpiresAt, rotated.ExpiresAt)
				return
			}

			assert.ErrorIs(t, err, ErrUserSessionInactive)
			assert.Nil(t, rotated)
			var stored UserSession
			require.NoError(t, DB.First(&stored, "sid = ?", session.SID).Error)
			assert.Equal(t, UserSessionStatusRevoked, stored.Status)
			assert.Equal(t, "idle_timeout", stored.RevokedReason)
			assert.Equal(t, originalHash, stored.RefreshHash)
			assert.Equal(t, originalLastActiveAt, stored.LastActiveAt)
			assert.Equal(t, originalExpiresAt, stored.ExpiresAt)
		})
	}
}

func TestUserSessionIdleBoundaryRejectsPreviousRefreshRace(t *testing.T) {
	setupUserSessionTest(t)
	const now = int64(2_000_100_000)
	const userID = 1210
	createUserSessionTestUser(t, userID, 1)
	session := newTestUserSession("idle-previous", userID, now-testUserSessionIdleTimeoutSeconds)
	session.ExpiresAt = now + 3600
	session.PreviousRefreshHash = "previous-idle-refresh"
	session.PreviousValidUntil = now + 30
	originalRefreshHash := session.RefreshHash
	originalPreviousValidUntil := session.PreviousValidUntil
	originalExpiresAt := session.ExpiresAt
	require.NoError(t, DB.Create(session).Error)

	rotated, err := RotateUserSessionRefresh(
		userID,
		session.SID,
		session.PreviousRefreshHash,
		"unused-next-hash",
		now,
		30*time.Second,
	)

	assert.ErrorIs(t, err, ErrUserSessionInactive)
	assert.Nil(t, rotated)
	var stored UserSession
	require.NoError(t, DB.First(&stored, "sid = ?", session.SID).Error)
	assert.Equal(t, UserSessionStatusRevoked, stored.Status)
	assert.Equal(t, "idle_timeout", stored.RevokedReason)
	assert.Equal(t, originalRefreshHash, stored.RefreshHash)
	assert.Equal(t, "previous-idle-refresh", stored.PreviousRefreshHash)
	assert.Equal(t, originalPreviousValidUntil, stored.PreviousValidUntil)
	assert.Equal(t, now-testUserSessionIdleTimeoutSeconds, stored.LastActiveAt)
	assert.Equal(t, originalExpiresAt, stored.ExpiresAt)
}

func TestUserSessionIdleRowsDoNotCountOrAppearActive(t *testing.T) {
	setupUserSessionTest(t)
	const now = int64(2_000_200_000)
	const userID = 1220
	createUserSessionTestUser(t, userID, 1)

	active := newTestUserSession("idle-set-active", userID, now-testUserSessionIdleTimeoutSeconds+1)
	idle := newTestUserSession("idle-set-expired", userID, now-testUserSessionIdleTimeoutSeconds)
	require.NoError(t, DB.Create([]*UserSession{active, idle}).Error)

	count, err := CountActiveUserSessions(userID, now)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)

	sessions, err := ListActiveUserSessions(userID, active.SID, now)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, active.SID, sessions[0].SID)
}

func TestUserSessionIdleRowDoesNotConsumeAdmissionCapacity(t *testing.T) {
	setupUserSessionTest(t)
	const now = int64(2_000_300_000)
	const userID = 1230
	createUserSessionTestUser(t, userID, 1)

	idle := newTestUserSession("idle-admission-existing", userID, now-testUserSessionIdleTimeoutSeconds)
	require.NoError(t, DB.Create(idle).Error)
	candidate := newTestUserSession("idle-admission-candidate", userID, now)

	err := AdmitUserSession(candidate, 1, now, UserSessionAdmissionLimits{
		Active:        1,
		Issuance:      10,
		WindowSeconds: 60,
	})

	require.NoError(t, err)
	var count int64
	require.NoError(t, DB.Model(&UserSession{}).Where("user_id = ?", userID).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}

func TestUserSessionIdleCacheHitAndDatabaseMissFailClosed(t *testing.T) {
	tests := []struct {
		name      string
		withRedis bool
	}{
		{name: "database fallback"},
		{name: "redis hit", withRedis: true},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupUserSessionTest(t)
			now := time.Now().Unix()
			evaluationTime := now + testUserSessionIdleTimeoutSeconds
			userID := 1240 + index
			createUserSessionTestUser(t, userID, 1)
			session := newTestUserSession(fmt.Sprintf("idle-cache-%d", index), userID, now)
			require.NoError(t, DB.Create(session).Error)

			if test.withRedis {
				useUserCacheMiniRedis(t)
				require.NoError(t, writeUserSessionCache(session.cacheEntry(), userSessionCacheDeadline()))
				_, err := getUserSessionCache(session.SID)
				require.NoError(t, err, "the baseline cache entry must be readable before idle enforcement")
			} else {
				common.RedisEnabled = false
			}

			loaded, err := GetUserSessionCachedAt(session.SID, evaluationTime)

			assert.ErrorIs(t, err, ErrUserSessionInactive)
			assert.Nil(t, loaded)
			var stored UserSession
			require.NoError(t, DB.First(&stored, "sid = ?", session.SID).Error)
			assert.Equal(t, UserSessionStatusRevoked, stored.Status)
			assert.Equal(t, "idle_timeout", stored.RevokedReason)
		})
	}
}

func TestUserSessionStaleIdleCacheCannotRevokeRefreshedDatabaseRow(t *testing.T) {
	setupUserSessionTest(t)
	useUserCacheMiniRedis(t)
	now := time.Now().Unix()
	evaluationTime := now + testUserSessionIdleTimeoutSeconds
	const userID = 1245
	createUserSessionTestUser(t, userID, 1)
	session := newTestUserSession("stale-idle-cache-fresh-database", userID, now)
	require.NoError(t, DB.Create(session).Error)
	require.NoError(t, writeUserSessionCache(session.cacheEntry(), userSessionCacheDeadline()))

	require.NoError(t, DB.Model(&UserSession{}).
		Where("sid = ?", session.SID).
		Update("last_active_at", evaluationTime).Error)

	loaded, err := GetUserSessionCachedAt(session.SID, evaluationTime)

	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, evaluationTime, loaded.LastActiveAt)
	assert.Equal(t, UserSessionStatusActive, loaded.Status)
	var stored UserSession
	require.NoError(t, DB.First(&stored, "sid = ?", session.SID).Error)
	assert.Equal(t, UserSessionStatusActive, stored.Status)
	assert.Empty(t, stored.RevokedReason)
}

func TestUserSessionIdleRevocationPreservesExplicitRevocationAndVersionErrors(t *testing.T) {
	setupUserSessionTest(t)
	now := time.Now().Unix()
	const userID = 1250
	createUserSessionTestUser(t, userID, 2)
	session := newTestUserSession("idle-existing-revocation", userID, now)
	session.UserAuthVersion = 1
	session.Status = UserSessionStatusRevoked
	session.RevokedAt = now
	session.RevokedReason = "logout"
	require.NoError(t, DB.Create(session).Error)

	_, err := GetUserSessionCached(session.SID)

	assert.True(t, errors.Is(err, ErrUserSessionInactive))
	var stored UserSession
	require.NoError(t, DB.First(&stored, "sid = ?", session.SID).Error)
	assert.Equal(t, "logout", stored.RevokedReason)
	assert.EqualValues(t, 1, stored.UserAuthVersion)
}

func TestAdvanceUserSessionAuthVersionPreservesIdleActivity(t *testing.T) {
	setupUserSessionTest(t)
	const now = int64(2_000_400_000)
	const userID = 1260
	createUserSessionTestUser(t, userID, 2)
	session := newTestUserSession(
		"version-advance-preserves-idle-activity",
		userID,
		now-testUserSessionIdleTimeoutSeconds+120,
	)
	session.Version = 4
	session.UserAuthVersion = 1
	session.PreviousRefreshHash = "previous-version-advance"
	session.PreviousValidUntil = now + 30
	session.ExpiresAt = now + testUserSessionIdleTimeoutSeconds
	require.NoError(t, DB.Create(session).Error)
	originalIdleDeadline := session.LastActiveAt + testUserSessionIdleTimeoutSeconds

	advanced, err := AdvanceUserSessionAuthVersionAt(
		userID,
		session.SID,
		session.Version,
		session.UserAuthVersion,
		2,
		now,
	)

	require.NoError(t, err)
	require.NotNil(t, advanced)
	assert.Equal(t, int64(5), advanced.Version)
	assert.Equal(t, int64(2), advanced.UserAuthVersion)
	assert.Equal(t, session.LastActiveAt, advanced.LastActiveAt)
	assert.Equal(t, originalIdleDeadline, advanced.EffectiveExpiresAt())

	var stored UserSession
	require.NoError(t, DB.First(&stored, "sid = ?", session.SID).Error)
	expected := *session
	expected.Version = 5
	expected.UserAuthVersion = 2
	assert.Equal(t, expected, stored, "security-version advancement must change only version fields")
	assert.Equal(t, originalIdleDeadline, stored.EffectiveExpiresAt())
}

func TestAdvanceUserSessionAuthVersionDoesNotRevokeConcurrentRefreshWinner(t *testing.T) {
	previousDB := DB
	previousMainDatabaseType := common.MainDatabaseType()
	previousRedisEnabled := common.RedisEnabled
	previousIdleTimeout := common.UserSessionIdleTimeoutSeconds
	databasePath := filepath.Join(t.TempDir(), "auth-version-idle-race.db")
	db, err := gorm.Open(
		sqlite.Open("file:"+databasePath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	require.NoError(t, db.AutoMigrate(&User{}, &UserSession{}))
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.UserSessionIdleTimeoutSeconds = testUserSessionIdleTimeoutSeconds
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousMainDatabaseType)
		common.RedisEnabled = previousRedisEnabled
		common.UserSessionIdleTimeoutSeconds = previousIdleTimeout
		require.NoError(t, sqlDB.Close())
	})

	const now = int64(2_000_500_000)
	const userID = 1270
	createUserSessionTestUser(t, userID, 2)
	session := newTestUserSession(
		"version-advance-concurrent-refresh",
		userID,
		now-testUserSessionIdleTimeoutSeconds,
	)
	session.UserAuthVersion = 1
	session.ExpiresAt = now + 3600
	require.NoError(t, DB.Create(session).Error)

	queryObserved := make(chan struct{})
	releaseQuery := make(chan struct{})
	var intercepted atomic.Bool
	callbackName := "test:pause_idle_version_advance_after_read"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "user_sessions" &&
			intercepted.CompareAndSwap(false, true) {
			close(queryObserved)
			<-releaseQuery
		}
	}))
	t.Cleanup(func() {
		_ = DB.Callback().Query().Remove(callbackName)
	})

	type advanceResult struct {
		session *UserSession
		err     error
	}
	result := make(chan advanceResult, 1)
	go func() {
		advanced, advanceErr := AdvanceUserSessionAuthVersionAt(
			userID,
			session.SID,
			session.Version,
			session.UserAuthVersion,
			2,
			now,
		)
		result <- advanceResult{session: advanced, err: advanceErr}
	}()

	<-queryObserved
	refreshErr := DB.Model(&UserSession{}).
		Where("sid = ?", session.SID).
		Update("last_active_at", now).Error
	close(releaseQuery)
	require.NoError(t, refreshErr)

	advanced := <-result
	require.NoError(t, advanced.err)
	require.NotNil(t, advanced.session)
	assert.Equal(t, now, advanced.session.LastActiveAt)
	assert.Equal(t, int64(2), advanced.session.Version)
	assert.Equal(t, int64(2), advanced.session.UserAuthVersion)
	var stored UserSession
	require.NoError(t, DB.First(&stored, "sid = ?", session.SID).Error)
	assert.Equal(t, UserSessionStatusActive, stored.Status)
	assert.Zero(t, stored.RevokedAt)
	assert.Empty(t, stored.RevokedReason)
	assert.Equal(t, now, stored.LastActiveAt)
	assert.Equal(t, int64(2), stored.Version)
	assert.Equal(t, int64(2), stored.UserAuthVersion)
}
