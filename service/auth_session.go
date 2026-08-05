package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const RefreshCookieName = "new_api_refresh"

var (
	ErrLoginSessionInvalid  = errors.New("login session is invalid")
	ErrLoginSessionRevoked  = errors.New("login session is revoked")
	ErrLoginSessionMismatch = errors.New("login session does not match the expected session")
	ErrRefreshTokenInvalid  = errors.New("refresh token is invalid")
	ErrRefreshRace          = errors.New("refresh token was already rotated")
)

type LoginSessionView struct {
	SID          string `json:"sid"`
	Current      bool   `json:"current"`
	LoginMethod  string `json:"login_method"`
	IP           string `json:"ip"`
	UserAgent    string `json:"user_agent"`
	CreatedAt    int64  `json:"created_at"`
	LastActiveAt int64  `json:"last_active_at"`
	ExpiresAt    int64  `json:"expires_at"`
}

type AuthBundle struct {
	AccessToken      string           `json:"access_token"`
	TokenType        string           `json:"token_type"`
	AccessExpiresAt  int64            `json:"access_expires_at"`
	Session          LoginSessionView `json:"session"`
	RefreshToken     string           `json:"-"`
	RefreshExpiresAt int64            `json:"-"`
}

func CreateLoginSession(userID int, loginMethod, ip, userAgent string) (*AuthBundle, error) {
	return createLoginSession(userID, 0, loginMethod, ip, userAgent)
}

func CreateLoginSessionAtAuthVersion(userID int, expectedAuthVersion int64, loginMethod, ip, userAgent string) (*AuthBundle, error) {
	if expectedAuthVersion <= 0 {
		return nil, ErrLoginSessionInvalid
	}
	return createLoginSession(userID, expectedAuthVersion, loginMethod, ip, userAgent)
}

func createLoginSession(userID int, expectedAuthVersion int64, loginMethod, ip, userAgent string) (*AuthBundle, error) {
	now := time.Now()
	nowUnix := now.Unix()
	refreshSecret, err := common.GenerateRandomCharsKey(64)
	if err != nil {
		return nil, err
	}
	session := &model.UserSession{
		SID:          uuid.NewString(),
		UserID:       userID,
		Version:      1,
		Status:       model.UserSessionStatusActive,
		RefreshHash:  hashRefreshSecret(refreshSecret),
		LoginMethod:  strings.TrimSpace(loginMethod),
		IP:           truncateAuthMetadata(ip, 64),
		UserAgent:    truncateAuthMetadata(userAgent, 512),
		CreatedAt:    nowUnix,
		LastActiveAt: nowUnix,
		ExpiresAt:    now.Add(LoginSessionTTL).Unix(),
	}
	if session.LoginMethod == "" {
		session.LoginMethod = "unknown"
	}
	if err := model.AdmitUserSession(session, expectedAuthVersion, nowUnix, model.UserSessionAdmissionLimits{
		Active:        common.UserSessionActiveLimit,
		Issuance:      common.UserSessionIssuanceLimit,
		WindowSeconds: common.UserSessionIssuanceWindowSeconds,
	}); err != nil {
		if errors.Is(err, model.ErrUserSessionAuthVersionMismatch) {
			return nil, ErrLoginSessionRevoked
		}
		if errors.Is(err, model.ErrUserSessionInvalid) {
			return nil, ErrLoginSessionInvalid
		}
		return nil, err
	}
	bundle, err := issueAuthBundleAt(session, session.SID+"."+refreshSecret, true, now)
	if err != nil {
		_, _ = model.RevokeUserSession(userID, session.SID, "token_issue_failed")
		return nil, err
	}
	return bundle, nil
}

func ValidateLoginSession(identity AuthIdentity) (*model.UserSession, *model.UserBase, error) {
	return validateLoginSessionAt(identity, time.Now().Unix())
}

func validateLoginSessionAt(identity AuthIdentity, now int64) (*model.UserSession, *model.UserBase, error) {
	session, err := model.GetActiveUserSessionBySIDAt(identity.SessionID, now)
	if err != nil {
		if errors.Is(err, model.ErrUserSessionInactive) {
			return nil, nil, ErrLoginSessionRevoked
		}
		return nil, nil, err
	}
	if session.UserID != identity.UserID || !session.IsActiveAt(now) || session.Version != identity.SessionVersion || session.UserAuthVersion != identity.UserAuthVersion {
		return nil, nil, ErrLoginSessionRevoked
	}
	user, err := model.GetUserById(identity.UserID, false)
	if err != nil {
		return nil, nil, err
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion != identity.UserAuthVersion {
		return nil, nil, ErrLoginSessionRevoked
	}
	return session, user.ToBaseUser(), nil
}

// ValidateSessionReference validates a server-side flow bound to an existing
// dashboard session without requiring an access token on the callback request.
func ValidateSessionReference(userID int, sid string) (AuthIdentity, error) {
	if userID <= 0 || strings.TrimSpace(sid) == "" {
		return AuthIdentity{}, ErrLoginSessionInvalid
	}
	session, err := model.GetUserSessionBySID(sid)
	if err != nil {
		return AuthIdentity{}, err
	}
	identity := AuthIdentity{
		UserID:          userID,
		SessionID:       sid,
		UserAuthVersion: session.UserAuthVersion,
		SessionVersion:  session.Version,
	}
	if _, _, err := ValidateLoginSession(identity); err != nil {
		return AuthIdentity{}, err
	}
	return identity, nil
}

// AdvanceCurrentSessionSecurity increments the user's global auth version,
// preserves only the current browser session at a new session version and
// returns a replacement access token. Call after a successful 2FA/passkey
// security-setting mutation that did not already advance AuthVersion.
func AdvanceCurrentSessionSecurity(identity AuthIdentity, reason string) (*AuthBundle, error) {
	now := time.Now()
	nextUserAuthVersion, err := model.BumpUserAuthVersion(identity.UserID)
	if err != nil {
		return nil, err
	}
	return advanceCurrentSessionToVersionAt(identity, nextUserAuthVersion, reason, now)
}

// AdvanceCurrentSessionToUserVersion is used when the security mutation and
// AuthVersion increment were committed in the same transaction (for example,
// a password change).
func AdvanceCurrentSessionToUserVersion(identity AuthIdentity, reason string) (*AuthBundle, error) {
	now := time.Now()
	user, err := model.GetUserCache(identity.UserID)
	if err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion <= identity.UserAuthVersion {
		return nil, ErrLoginSessionRevoked
	}
	return advanceCurrentSessionToVersionAt(identity, user.AuthVersion, reason, now)
}

func advanceCurrentSessionToVersionAt(identity AuthIdentity, nextUserAuthVersion int64, reason string, now time.Time) (*AuthBundle, error) {
	session, err := model.AdvanceUserSessionAuthVersionAt(
		identity.UserID,
		identity.SessionID,
		identity.SessionVersion,
		identity.UserAuthVersion,
		nextUserAuthVersion,
		now.Unix(),
	)
	if err != nil {
		if errors.Is(err, model.ErrUserSessionInactive) {
			return nil, ErrLoginSessionRevoked
		}
		return nil, err
	}
	if _, err := model.RevokeOtherUserSessions(identity.UserID, identity.SessionID, reason); err != nil {
		return nil, err
	}
	return issueAuthBundleAt(session, "", true, now)
}

func RefreshLoginSession(rawRefreshToken, expectedSID, ip, userAgent string) (*AuthBundle, *model.User, error) {
	return refreshLoginSessionAt(rawRefreshToken, expectedSID, ip, userAgent, time.Now())
}

func refreshLoginSessionAt(rawRefreshToken, expectedSID, ip, userAgent string, now time.Time) (*AuthBundle, *model.User, error) {
	nowUnix := now.Unix()
	sid, secret, ok := splitRefreshToken(rawRefreshToken)
	if !ok {
		return nil, nil, ErrRefreshTokenInvalid
	}
	if expectedSID = strings.TrimSpace(expectedSID); expectedSID != "" && expectedSID != sid {
		return nil, nil, ErrLoginSessionMismatch
	}
	session, err := model.GetUserSessionCachedAt(sid, nowUnix)
	if err != nil {
		if errors.Is(err, model.ErrUserSessionInactive) {
			return nil, nil, ErrLoginSessionRevoked
		}
		return nil, nil, ErrRefreshTokenInvalid
	}
	if !session.IsActiveAt(nowUnix) {
		return nil, nil, ErrLoginSessionRevoked
	}
	userCache, err := model.GetUserCache(session.UserID)
	if err != nil {
		return nil, nil, err
	}
	currentUser, err := model.GetUserById(session.UserID, false)
	if err != nil {
		return nil, nil, err
	}
	if userCache.Status != common.UserStatusEnabled || userCache.AuthVersion != session.UserAuthVersion ||
		currentUser.Status != common.UserStatusEnabled || currentUser.AuthVersion != session.UserAuthVersion {
		_, _ = model.RevokeUserSession(session.UserID, session.SID, "user_security_changed")
		return nil, nil, ErrLoginSessionRevoked
	}
	nextSecret := deriveNextRefreshSecret(sid, secret)
	presentedHash := hashRefreshSecret(secret)
	expectedNextHash := hashRefreshSecret(nextSecret)
	rotated, err := model.RotateUserSessionRefresh(
		session.UserID,
		sid,
		presentedHash,
		expectedNextHash,
		nowUnix,
		RefreshReplayWindow,
	)
	if err != nil {
		if errors.Is(err, model.ErrUserSessionRefreshRace) && rotated != nil &&
			expectedNextHash == rotated.RefreshHash {
			recovered, recoverErr := model.RecoverUserSessionRefresh(
				session.UserID,
				sid,
				presentedHash,
				expectedNextHash,
				rotated.Version,
				rotated.UserAuthVersion,
				nowUnix,
			)
			if errors.Is(recoverErr, model.ErrUserSessionInactive) {
				return nil, nil, ErrLoginSessionRevoked
			}
			if errors.Is(recoverErr, model.ErrUserSessionRefreshRace) {
				return nil, nil, ErrRefreshRace
			}
			if recoverErr != nil {
				return nil, nil, recoverErr
			}
			bundle, issueErr := issueAuthBundleAt(recovered, sid+"."+nextSecret, true, now)
			if issueErr != nil {
				return nil, nil, issueErr
			}
			return bundle, currentUser, nil
		}
		if errors.Is(err, model.ErrUserSessionInactive) ||
			errors.Is(err, model.ErrUserSessionRefreshReuse) {
			return nil, nil, ErrLoginSessionRevoked
		}
		if errors.Is(err, model.ErrUserSessionRefreshInvalid) {
			return nil, nil, ErrRefreshTokenInvalid
		}
		if errors.Is(err, model.ErrUserSessionRefreshRace) {
			return nil, nil, ErrRefreshRace
		}
		return nil, nil, err
	}
	rotated.IP = truncateAuthMetadata(ip, 64)
	rotated.UserAgent = truncateAuthMetadata(userAgent, 512)
	bundle, err := issueAuthBundleAt(rotated, sid+"."+nextSecret, true, now)
	if err != nil {
		return nil, nil, err
	}
	return bundle, currentUser, nil
}

func RevokeByRefreshToken(rawRefreshToken, expectedSID, reason string) error {
	sid, secret, ok := splitRefreshToken(rawRefreshToken)
	if !ok {
		return nil
	}
	if expectedSID = strings.TrimSpace(expectedSID); expectedSID != "" && expectedSID != sid {
		return ErrLoginSessionMismatch
	}
	_, err := model.RevokeUserSessionByRefreshHash(sid, hashRefreshSecret(secret), reason)
	return err
}

func RefreshTokenSID(rawRefreshToken string) (string, bool) {
	sid, _, ok := splitRefreshToken(rawRefreshToken)
	return sid, ok
}

func ListLoginSessions(userID int, currentSID string) ([]LoginSessionView, error) {
	now := time.Now().Unix()
	sessions, err := model.ListActiveUserSessions(userID, currentSID, now)
	if err != nil {
		return nil, err
	}
	views := make([]LoginSessionView, 0, len(sessions))
	for i := range sessions {
		views = append(views, sessionView(&sessions[i], sessions[i].SID == currentSID))
	}
	return views, nil
}

func WriteRefreshCookie(c *gin.Context, rawToken string, refreshExpiresAt int64) {
	writeRefreshCookieAt(c, rawToken, refreshExpiresAt, time.Now())
}

func writeRefreshCookieAt(c *gin.Context, rawToken string, refreshExpiresAt int64, now time.Time) {
	expiresAt := time.Unix(refreshExpiresAt, 0)
	maxAge := int(expiresAt.Sub(now) / time.Second)
	if maxAge < 1 {
		ClearRefreshCookie(c)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    rawToken,
		Path:     "/api/user/auth",
		MaxAge:   maxAge,
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   common.SessionCookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func ClearRefreshCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     "/api/user/auth",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   common.SessionCookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func issueAuthBundleAt(session *model.UserSession, rawRefreshToken string, current bool, now time.Time) (*AuthBundle, error) {
	identity := AuthIdentity{
		UserID:          session.UserID,
		SessionID:       session.SID,
		UserAuthVersion: session.UserAuthVersion,
		SessionVersion:  session.Version,
	}
	accessToken, accessExpiresAt, err := issueAccessTokenAt(identity, now)
	if err != nil {
		return nil, err
	}
	return &AuthBundle{
		AccessToken:      accessToken,
		TokenType:        "Bearer",
		AccessExpiresAt:  accessExpiresAt,
		Session:          sessionView(session, current),
		RefreshToken:     rawRefreshToken,
		RefreshExpiresAt: session.EffectiveExpiresAt(),
	}, nil
}

func sessionView(session *model.UserSession, current bool) LoginSessionView {
	return LoginSessionView{
		SID:          session.SID,
		Current:      current,
		LoginMethod:  session.LoginMethod,
		IP:           session.IP,
		UserAgent:    session.UserAgent,
		CreatedAt:    session.CreatedAt,
		LastActiveAt: session.LastActiveAt,
		ExpiresAt:    session.ExpiresAt,
	}
}

func splitRefreshToken(raw string) (string, string, bool) {
	sid, secret, ok := strings.Cut(strings.TrimSpace(raw), ".")
	if !ok || sid == "" || secret == "" || strings.Contains(secret, ".") {
		return "", "", false
	}
	if _, err := uuid.Parse(sid); err != nil {
		return "", "", false
	}
	return sid, secret, true
}

func hashRefreshSecret(secret string) string {
	return common.GenerateHMACWithKey(authSigningKey("refresh"), secret)
}

func deriveNextRefreshSecret(sid, currentSecret string) string {
	return common.GenerateHMACWithKey(authSigningKey("refresh-rotate"), sid+"."+currentSecret)
}

func truncateAuthMetadata(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func authSessionErrorCode(err error) (int, string) {
	switch {
	case errors.Is(err, model.ErrUserSessionLimit):
		return http.StatusConflict, "AUTH_SESSION_LIMIT"
	case errors.Is(err, model.ErrUserSessionIssuanceLimit):
		return http.StatusTooManyRequests, "AUTH_SESSION_ISSUANCE_LIMIT"
	case errors.Is(err, ErrLoginSessionMismatch):
		return http.StatusConflict, "AUTH_SESSION_MISMATCH"
	case errors.Is(err, ErrRefreshRace):
		return http.StatusConflict, "AUTH_REFRESH_RACE"
	case errors.Is(err, ErrAuthTokenExpired):
		return http.StatusUnauthorized, "AUTH_TOKEN_EXPIRED"
	case errors.Is(err, ErrLoginSessionRevoked):
		return http.StatusUnauthorized, "AUTH_SESSION_REVOKED"
	case errors.Is(err, ErrRefreshTokenInvalid), errors.Is(err, ErrAuthTokenInvalid):
		return http.StatusUnauthorized, "AUTH_UNAUTHORIZED"
	default:
		return http.StatusInternalServerError, "AUTH_INTERNAL_ERROR"
	}
}

func AuthSessionErrorCode(err error) (int, string) {
	return authSessionErrorCode(err)
}

func FormatAuthError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("authentication failed: %v", err)
}
