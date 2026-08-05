package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginSessionIdleObserversRevokeIndependently(t *testing.T) {
	tests := []struct {
		name    string
		observe func(t *testing.T, bundle *AuthBundle, now int64)
	}{
		{
			name: "access validation",
			observe: func(t *testing.T, bundle *AuthBundle, now int64) {
				identity, err := ParseAccessToken(bundle.AccessToken)
				require.NoError(t, err)
				session, _, err := validateLoginSessionAt(identity, now)
				assert.ErrorIs(t, err, ErrLoginSessionRevoked)
				assert.Nil(t, session)
			},
		},
		{
			name: "refresh",
			observe: func(t *testing.T, bundle *AuthBundle, now int64) {
				refreshed, _, err := refreshLoginSessionAt(
					bundle.RefreshToken,
					bundle.Session.SID,
					"127.0.0.2",
					"idle-refresh",
					time.Unix(now, 0),
				)
				assert.ErrorIs(t, err, ErrLoginSessionRevoked)
				assert.Nil(t, refreshed)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useTestSessionSecret(t)
			user := setupAuthSessionTestDB(t)
			bundle, err := CreateLoginSession(user.Id, "password", "127.0.0.1", "idle-test")
			require.NoError(t, err)

			now := time.Now().Unix()
			idleLastActiveAt := now - int64(7*24*time.Hour/time.Second)
			require.NoError(t, model.DB.Model(&model.UserSession{}).
				Where("sid = ?", bundle.Session.SID).
				Update("last_active_at", idleLastActiveAt).Error)

			test.observe(t, bundle, now)

			var stored model.UserSession
			require.NoError(t, model.DB.First(&stored, "sid = ?", bundle.Session.SID).Error)
			assert.Equal(t, model.UserSessionStatusRevoked, stored.Status)
			assert.Equal(t, "idle_timeout", stored.RevokedReason)
			assert.Equal(t, idleLastActiveAt, stored.LastActiveAt)
			assert.Equal(t, hashRefreshSecret(bundle.RefreshToken[len(bundle.Session.SID)+1:]), stored.RefreshHash)
		})
	}
}

func TestLoginSessionRefreshBeforeIdleDeadlineSlidesOnlyIdleActivity(t *testing.T) {
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	bundle, err := CreateLoginSession(user.Id, "password", "127.0.0.1", "idle-test")
	require.NoError(t, err)

	now := time.Now().Unix()
	lastActiveAt := now - int64(7*24*time.Hour/time.Second) + 1
	require.NoError(t, model.DB.Model(&model.UserSession{}).
		Where("sid = ?", bundle.Session.SID).
		Update("last_active_at", lastActiveAt).Error)

	refreshed, _, err := refreshLoginSessionAt(
		bundle.RefreshToken,
		bundle.Session.SID,
		"127.0.0.2",
		"idle-refresh",
		time.Unix(now, 0),
	)

	require.NoError(t, err)
	assert.Equal(t, now, refreshed.Session.LastActiveAt)
	assert.Equal(t, bundle.Session.ExpiresAt, refreshed.Session.ExpiresAt)
	assert.Equal(t, now+int64(7*24*time.Hour/time.Second), refreshed.RefreshExpiresAt)
}

func TestRefreshCookieUsesEffectiveIdleDeadline(t *testing.T) {
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	bundle, err := CreateLoginSession(user.Id, "password", "127.0.0.1", "idle-cookie")
	require.NoError(t, err)

	now := time.Now().Unix()
	lastActiveAt := now - int64(24*time.Hour/time.Second)
	idleDeadline := lastActiveAt + int64(7*24*time.Hour/time.Second)
	require.Less(t, idleDeadline, bundle.Session.ExpiresAt)
	require.NoError(t, model.DB.Model(&model.UserSession{}).
		Where("sid = ?", bundle.Session.SID).
		Update("last_active_at", lastActiveAt).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/refresh", nil)

	writeRefreshCookieAt(context, bundle.RefreshToken, idleDeadline, time.Unix(now, 0))

	cookies := recorder.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, RefreshCookieName, cookies[0].Name)
	assert.Equal(t, idleDeadline, cookies[0].Expires.Unix())
	assert.Positive(t, cookies[0].MaxAge)
	assert.Equal(t, idleDeadline-now, int64(cookies[0].MaxAge))
}

func TestRefreshAuthCookieFractionalDeadlineNeverExtends(t *testing.T) {
	now := time.Unix(2_000_000_000, 500_000_000)
	tests := []struct {
		name          string
		expiresAt     int64
		wantMaxAge    int
		wantExpiresAt int64
		wantValue     string
	}{
		{
			name:          "one and a half seconds remaining truncates to one",
			expiresAt:     now.Unix() + 2,
			wantMaxAge:    1,
			wantExpiresAt: now.Unix() + 2,
			wantValue:     "refresh-token",
		},
		{
			name:          "positive subsecond remainder clears cookie",
			expiresAt:     now.Unix() + 1,
			wantMaxAge:    -1,
			wantExpiresAt: 1,
			wantValue:     "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/refresh", nil)

			writeRefreshCookieAt(context, "refresh-token", test.expiresAt, now)

			cookies := recorder.Result().Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, test.wantMaxAge, cookies[0].MaxAge)
			assert.Equal(t, test.wantExpiresAt, cookies[0].Expires.Unix())
			assert.Equal(t, test.wantValue, cookies[0].Value)
		})
	}
}

func TestAdvanceCurrentSessionVersionUsesOperationTimeWithoutSlidingIdleActivity(t *testing.T) {
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	bundle, err := CreateLoginSession(user.Id, "password", "127.0.0.1", "version-advance")
	require.NoError(t, err)
	identity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)

	operationTime := time.Now().Truncate(time.Second)
	lastActiveAt := operationTime.Unix() - common.UserSessionIdleTimeoutSeconds + 120
	require.NoError(t, model.DB.Model(&model.UserSession{}).
		Where("sid = ?", identity.SessionID).
		Update("last_active_at", lastActiveAt).Error)
	require.NoError(t, model.DB.Model(&model.User{}).
		Where("id = ?", user.Id).
		Update("auth_version", identity.UserAuthVersion+1).Error)

	advanced, err := advanceCurrentSessionToVersionAt(
		identity,
		identity.UserAuthVersion+1,
		"security_update",
		operationTime,
	)

	require.NoError(t, err)
	require.NotNil(t, advanced)
	assert.Equal(t, lastActiveAt, advanced.Session.LastActiveAt)
	assert.Equal(t, lastActiveAt+common.UserSessionIdleTimeoutSeconds, advanced.RefreshExpiresAt)
	assert.Equal(t, operationTime.Add(AccessTokenTTL).Unix(), advanced.AccessExpiresAt)
	claims, err := parseAuthClaims(advanced.AccessToken, accessTokenUse, authSigningKey(accessTokenUse))
	require.NoError(t, err)
	require.NotNil(t, claims.IssuedAt)
	assert.Equal(t, operationTime.Unix(), claims.IssuedAt.Unix())

	var stored model.UserSession
	require.NoError(t, model.DB.First(&stored, "sid = ?", identity.SessionID).Error)
	assert.Equal(t, lastActiveAt, stored.LastActiveAt)
}

func TestAdvanceCurrentSessionVersionAtIdleDeadlineDurablyRevokes(t *testing.T) {
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	bundle, err := CreateLoginSession(user.Id, "password", "127.0.0.1", "idle-version-advance")
	require.NoError(t, err)
	identity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)

	operationTime := time.Now().Truncate(time.Second)
	lastActiveAt := operationTime.Unix() - common.UserSessionIdleTimeoutSeconds
	require.NoError(t, model.DB.Model(&model.UserSession{}).
		Where("sid = ?", identity.SessionID).
		Updates(map[string]interface{}{
			"last_active_at":        lastActiveAt,
			"expires_at":            operationTime.Unix() + 3600,
			"previous_refresh_hash": "idle-version-previous",
			"previous_valid_until":  operationTime.Unix() + 30,
		}).Error)
	require.NoError(t, model.DB.Model(&model.User{}).
		Where("id = ?", user.Id).
		Update("auth_version", identity.UserAuthVersion+1).Error)
	var before model.UserSession
	require.NoError(t, model.DB.First(&before, "sid = ?", identity.SessionID).Error)

	advanced, err := advanceCurrentSessionToVersionAt(
		identity,
		identity.UserAuthVersion+1,
		"security_update",
		operationTime,
	)

	assert.ErrorIs(t, err, ErrLoginSessionRevoked)
	assert.Nil(t, advanced)
	var stored model.UserSession
	require.NoError(t, model.DB.First(&stored, "sid = ?", identity.SessionID).Error)
	assert.Equal(t, model.UserSessionStatusRevoked, stored.Status)
	assert.Equal(t, operationTime.Unix(), stored.RevokedAt)
	assert.Equal(t, model.UserSessionRevokedReasonIdleTimeout, stored.RevokedReason)
	assert.Equal(t, before.RefreshHash, stored.RefreshHash)
	assert.Equal(t, before.PreviousRefreshHash, stored.PreviousRefreshHash)
	assert.Equal(t, before.PreviousValidUntil, stored.PreviousValidUntil)
	assert.Equal(t, before.LastActiveAt, stored.LastActiveAt)
	assert.Equal(t, before.ExpiresAt, stored.ExpiresAt)
	assert.Equal(t, before.Version, stored.Version)
	assert.Equal(t, before.UserAuthVersion, stored.UserAuthVersion)
}
