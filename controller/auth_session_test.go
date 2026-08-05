package controller

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRefreshAuthClearsCookieWhenIdleRevocationWinsBeforeRotation(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousSecret := common.SessionSecret
	previousMainDatabaseType := common.MainDatabaseType()
	previousActiveLimit := common.UserSessionActiveLimit
	previousIssuanceLimit := common.UserSessionIssuanceLimit
	previousIssuanceWindow := common.UserSessionIssuanceWindowSeconds
	previousIdleTimeout := common.UserSessionIdleTimeoutSeconds
	databasePath := filepath.Join(t.TempDir(), "refresh-idle-revocation-race.db")
	db, err := gorm.Open(
		sqlite.Open("file:"+databasePath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.SessionSecret = "refresh-idle-revocation-race-secret"
	common.UserSessionActiveLimit = common.DefaultUserSessionActiveLimit
	common.UserSessionIssuanceLimit = common.DefaultUserSessionIssuanceLimit
	common.UserSessionIssuanceWindowSeconds = int64(common.DefaultUserSessionIssuanceWindowSeconds)
	common.UserSessionIdleTimeoutSeconds = int64(common.DefaultUserSessionIdleTimeoutSeconds)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousMainDatabaseType)
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
		common.UserSessionActiveLimit = previousActiveLimit
		common.UserSessionIssuanceLimit = previousIssuanceLimit
		common.UserSessionIssuanceWindowSeconds = previousIssuanceWindow
		common.UserSessionIdleTimeoutSeconds = previousIdleTimeout
		require.NoError(t, sqlDB.Close())
	})

	user := &model.User{
		Username:    "refresh-idle-race-user",
		Password:    "unused",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "idle-race")
	require.NoError(t, err)
	lastActiveAt := time.Now().Unix()
	require.NoError(t, db.Model(&model.UserSession{}).
		Where("sid = ?", bundle.Session.SID).
		Update("last_active_at", lastActiveAt).Error)

	initialReadObserved := make(chan struct{})
	releaseInitialRead := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseInitialRead)
		})
	}
	var intercepted atomic.Bool
	var observedStatus string
	var observedLastActiveAt int64
	callbackName := "test:pause_refresh_after_initial_session_read"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Table != "user_sessions" ||
			!intercepted.CompareAndSwap(false, true) {
			return
		}
		if observed, ok := tx.Statement.Dest.(*model.UserSession); ok {
			observedStatus = observed.Status
			observedLastActiveAt = observed.LastActiveAt
		}
		close(initialReadObserved)
		<-releaseInitialRead
	}))
	t.Cleanup(func() {
		release()
		_ = db.Callback().Query().Remove(callbackName)
	})

	gin.SetMode(gin.TestMode)
	handlerDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/refresh", nil)
		context.Request.Header.Set("X-Auth-Session", bundle.Session.SID)
		context.Request.Header.Set("User-Agent", "idle-race")
		context.Request.AddCookie(&http.Cookie{
			Name:  service.RefreshCookieName,
			Value: bundle.RefreshToken,
		})
		RefreshAuth(context)
		handlerDone <- recorder
	}()

	<-initialReadObserved
	assert.Equal(t, model.UserSessionStatusActive, observedStatus)
	assert.Equal(t, lastActiveAt, observedLastActiveAt)
	_, revokeErr := model.GetActiveUserSessionBySIDAt(
		bundle.Session.SID,
		lastActiveAt+common.UserSessionIdleTimeoutSeconds,
	)
	release()
	require.ErrorIs(t, revokeErr, model.ErrUserSessionInactive)

	var recorder *httptest.ResponseRecorder
	select {
	case recorder = <-handlerDone:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "refresh handler did not finish")
	}
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "AUTH_SESSION_REVOKED", response.Code)
	cookies := recorder.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, service.RefreshCookieName, cookies[0].Name)
	assert.Empty(t, cookies[0].Value)
	assert.Equal(t, -1, cookies[0].MaxAge)

	var stored model.UserSession
	require.NoError(t, db.First(&stored, "sid = ?", bundle.Session.SID).Error)
	assert.Equal(t, model.UserSessionStatusRevoked, stored.Status)
	assert.Equal(t, model.UserSessionRevokedReasonIdleTimeout, stored.RevokedReason)
	assert.Equal(t, lastActiveAt, stored.LastActiveAt)
}

func TestRefreshAuthRejectsPreviousTokenRecoveryWhenIdleRevocationWins(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousSecret := common.SessionSecret
	previousMainDatabaseType := common.MainDatabaseType()
	previousActiveLimit := common.UserSessionActiveLimit
	previousIssuanceLimit := common.UserSessionIssuanceLimit
	previousIssuanceWindow := common.UserSessionIssuanceWindowSeconds
	previousIdleTimeout := common.UserSessionIdleTimeoutSeconds
	databasePath := filepath.Join(t.TempDir(), "previous-refresh-idle-revocation-race.db")
	db, err := gorm.Open(
		sqlite.Open("file:"+databasePath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.SessionSecret = "previous-refresh-idle-revocation-race-secret"
	common.UserSessionActiveLimit = common.DefaultUserSessionActiveLimit
	common.UserSessionIssuanceLimit = common.DefaultUserSessionIssuanceLimit
	common.UserSessionIssuanceWindowSeconds = int64(common.DefaultUserSessionIssuanceWindowSeconds)
	common.UserSessionIdleTimeoutSeconds = int64(common.DefaultUserSessionIdleTimeoutSeconds)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousMainDatabaseType)
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
		common.UserSessionActiveLimit = previousActiveLimit
		common.UserSessionIssuanceLimit = previousIssuanceLimit
		common.UserSessionIssuanceWindowSeconds = previousIssuanceWindow
		common.UserSessionIdleTimeoutSeconds = previousIdleTimeout
		require.NoError(t, sqlDB.Close())
	})

	user := &model.User{
		Username:    "previous-refresh-idle-race-user",
		Password:    "unused",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	original, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "previous-idle-race")
	require.NoError(t, err)
	successor, _, err := service.RefreshLoginSession(
		original.RefreshToken,
		original.Session.SID,
		"127.0.0.2",
		"previous-idle-race-successor",
	)
	require.NoError(t, err)
	require.NotEqual(t, original.RefreshToken, successor.RefreshToken)

	var before model.UserSession
	require.NoError(t, db.First(&before, "sid = ?", original.Session.SID).Error)
	require.NotEmpty(t, before.PreviousRefreshHash)
	require.Positive(t, before.PreviousValidUntil)

	rotationReadObserved := make(chan struct{})
	releaseRotationRead := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseRotationRead)
		})
	}
	var sessionQueryCount atomic.Int64
	callbackName := "test:pause_previous_refresh_recovery_after_rotation_read"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Table != "user_sessions" ||
			sessionQueryCount.Add(1) != 2 {
			return
		}
		close(rotationReadObserved)
		<-releaseRotationRead
	}))
	t.Cleanup(func() {
		release()
		_ = db.Callback().Query().Remove(callbackName)
	})

	gin.SetMode(gin.TestMode)
	handlerDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/refresh", nil)
		context.Request.Header.Set("X-Auth-Session", original.Session.SID)
		context.Request.Header.Set("User-Agent", "previous-idle-race")
		context.Request.AddCookie(&http.Cookie{
			Name:  service.RefreshCookieName,
			Value: original.RefreshToken,
		})
		RefreshAuth(context)
		handlerDone <- recorder
	}()

	select {
	case <-rotationReadObserved:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "refresh did not reach the previous-token recovery read")
	}
	idleDeadline := before.LastActiveAt + common.UserSessionIdleTimeoutSeconds
	_, revokeErr := model.GetActiveUserSessionBySIDAt(original.Session.SID, idleDeadline)
	require.ErrorIs(t, revokeErr, model.ErrUserSessionInactive)
	release()

	var recorder *httptest.ResponseRecorder
	select {
	case recorder = <-handlerDone:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "refresh handler did not finish")
	}
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "AUTH_SESSION_REVOKED", response.Code)
	cookies := recorder.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, service.RefreshCookieName, cookies[0].Name)
	assert.Empty(t, cookies[0].Value)
	assert.Equal(t, -1, cookies[0].MaxAge)

	var stored model.UserSession
	require.NoError(t, db.First(&stored, "sid = ?", original.Session.SID).Error)
	assert.Equal(t, model.UserSessionStatusRevoked, stored.Status)
	assert.Equal(t, idleDeadline, stored.RevokedAt)
	assert.Equal(t, model.UserSessionRevokedReasonIdleTimeout, stored.RevokedReason)
	assert.Equal(t, before.RefreshHash, stored.RefreshHash)
	assert.Equal(t, before.PreviousRefreshHash, stored.PreviousRefreshHash)
	assert.Equal(t, before.PreviousValidUntil, stored.PreviousValidUntil)
	assert.Equal(t, before.LastActiveAt, stored.LastActiveAt)
	assert.Equal(t, before.ExpiresAt, stored.ExpiresAt)
	assert.Equal(t, before.Version, stored.Version)
	assert.Equal(t, before.UserAuthVersion, stored.UserAuthVersion)
}

func TestAuthLogoutRejectsRefreshCookieSessionMismatch(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousSecret := common.SessionSecret
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	model.DB = db
	common.RedisEnabled = false
	common.SessionSecret = "auth-logout-mismatch-test-secret"
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
	})

	user := &model.User{
		Username: "logout-mismatch-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	sessionA, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent-a")
	require.NoError(t, err)
	sessionB, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent-b")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/logout", nil)
	c.Request.Header.Set("Authorization", "Bearer "+sessionA.AccessToken)
	c.Request.Header.Set("X-Auth-Session", sessionA.Session.SID)
	c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: sessionB.RefreshToken})

	AuthLogout(c)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "AUTH_SESSION_MISMATCH", response.Code)
	for _, sid := range []string{sessionA.Session.SID, sessionB.Session.SID} {
		stored, err := model.GetUserSessionBySID(sid)
		require.NoError(t, err)
		assert.Equal(t, model.UserSessionStatusActive, stored.Status)
	}
}

func TestWriteAuthSessionErrorMapsSessionGrowthLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		err            error
		expectedStatus int
		expectedCode   string
	}{
		{
			name:           "active session limit",
			err:            model.ErrUserSessionLimit,
			expectedStatus: http.StatusConflict,
			expectedCode:   "AUTH_SESSION_LIMIT",
		},
		{
			name:           "issuance limit",
			err:            model.ErrUserSessionIssuanceLimit,
			expectedStatus: http.StatusTooManyRequests,
			expectedCode:   "AUTH_SESSION_ISSUANCE_LIMIT",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			writeAuthSessionError(c, test.err)

			assert.Equal(t, test.expectedStatus, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Code    string `json:"code"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.Equal(t, test.expectedCode, response.Code)
		})
	}
}

func TestSetupLoginRequiresTwoFAAfterAnyFirstFactor(t *testing.T) {
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousRedis := common.RedisEnabled
	previousSecret := common.SessionSecret
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.TwoFA{},
		&model.AuthFlow{},
		&model.Log{},
	))
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.SessionSecret = "shared-login-twofa-test-secret"
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, i18n.Init())
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
		common.SetMainDatabaseType(previousMainDatabaseType)
		common.SetLogDatabaseType(previousLogDatabaseType)
	})

	const previousLastLoginAt = int64(321)
	user := &model.User{
		Username: "shared-login-twofa-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 7, LastLoginAt: previousLastLoginAt,
	}
	require.NoError(t, db.Create(user).Error)
	require.NoError(t, db.Create(&model.TwoFA{
		UserId: user.Id, Secret: "unused-test-secret", IsEnabled: true,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/example", nil)
	setupLogin(&model.User{
		Id: user.Id, Status: common.UserStatusEnabled, AuthVersion: 1,
	}, c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Require2FA bool   `json:"require_2fa"`
			FlowToken  string `json:"flow_token"`
			ExpiresAt  int64  `json:"expires_at"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.True(t, response.Data.Require2FA)
	assert.NotEmpty(t, response.Data.FlowToken)
	assert.Greater(t, response.Data.ExpiresAt, time.Now().Unix())
	assert.Empty(t, recorder.Result().Cookies())

	var sessionCount int64
	require.NoError(t, db.Model(&model.UserSession{}).Count(&sessionCount).Error)
	assert.Zero(t, sessionCount)
	var loginLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeLogin).Count(&loginLogCount).Error)
	assert.Zero(t, loginLogCount)
	var storedUser model.User
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	assert.Equal(t, previousLastLoginAt, storedUser.LastLoginAt)

	flow, err := model.GetAuthFlow(response.Data.FlowToken, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposeTwoFALogin,
		UserId:  user.Id,
	})
	require.NoError(t, err)
	var payload twoFALoginFlowPayload
	require.NoError(t, common.UnmarshalJsonStr(flow.Payload, &payload))
	assert.EqualValues(t, user.AuthVersion, payload.AuthVersion)
}

func TestSessionLimitDoesNotRecordRejectedLoginAsSuccessful(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousActiveLimit := common.UserSessionActiveLimit
	previousIssuanceLimit := common.UserSessionIssuanceLimit
	previousIssuanceWindow := common.UserSessionIssuanceWindowSeconds
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.TwoFA{},
		&model.AuthFlow{},
	))
	model.DB = db
	common.RedisEnabled = false
	common.UserSessionActiveLimit = 1
	common.UserSessionIssuanceLimit = 100
	common.UserSessionIssuanceWindowSeconds = int64(common.DefaultUserSessionIssuanceWindowSeconds)
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		common.UserSessionActiveLimit = previousActiveLimit
		common.UserSessionIssuanceLimit = previousIssuanceLimit
		common.UserSessionIssuanceWindowSeconds = previousIssuanceWindow
	})

	const previousLastLoginAt = int64(123)
	user := &model.User{
		Username: "rejected-login-audit-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, LastLoginAt: previousLastLoginAt,
	}
	require.NoError(t, db.Create(user).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.UserSession{
		SID: "existing-active-session", UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion,
		Status: model.UserSessionStatusActive, RefreshHash: "hash", LoginMethod: "password",
		CreatedAt: now, LastActiveAt: now, ExpiresAt: now + 3600,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/login", nil)
	setupLogin(user, c)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, previousLastLoginAt, stored.LastLoginAt)
}
