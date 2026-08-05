package model

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	UserSessionStatusActive   = "active"
	UserSessionStatusRevoking = "revoking"
	UserSessionStatusRevoked  = "revoked"

	UserSessionRevokedReasonIdleTimeout = "idle_timeout"

	userSessionCacheSchema      = 1
	userSessionListLimit        = 100
	userSessionRevokeBatchSize  = 500
	userSessionCleanupScanLimit = 1000
	userSessionCleanupBatchSize = 500
)

var (
	ErrUserSessionInvalid               = errors.New("user session is invalid")
	ErrUserSessionInactive              = errors.New("user session is inactive")
	ErrUserSessionRefreshInvalid        = errors.New("user session refresh token is invalid")
	ErrUserSessionRefreshRace           = errors.New("user session refresh is already in progress")
	ErrUserSessionRefreshReuse          = errors.New("user session refresh token was reused")
	ErrUserSessionLimit                 = errors.New("active user session limit reached")
	ErrUserSessionIssuanceLimit         = errors.New("user session issuance limit reached")
	ErrUserSessionAuthVersionMismatch   = errors.New("user session auth version does not match")
	errUserSessionCacheObservationStale = errors.New("user session cache observation is stale")
	errUserSessionIdle                  = errors.New("user session exceeded idle timeout")
)

// UserSessionAdmissionLimits defines the per-user active and issuance budgets
// enforced by AdmitUserSession.
type UserSessionAdmissionLimits struct {
	Active        int
	Issuance      int
	WindowSeconds int64
}

// UserSession is the server-side control plane for short-lived access JWTs.
// RefreshHash values are HMAC digests supplied by the service layer; opaque
// refresh secrets are never persisted.
type UserSession struct {
	SID                 string `json:"sid" gorm:"column:sid;type:varchar(64);primaryKey"`
	UserID              int    `json:"user_id" gorm:"column:user_id;not null;index:idx_user_sessions_user_status_expiry,priority:1;index:idx_user_sessions_user_created,priority:1"`
	Version             int64  `json:"version" gorm:"type:bigint;not null;default:1"`
	UserAuthVersion     int64  `json:"user_auth_version" gorm:"type:bigint;not null"`
	Status              string `json:"status" gorm:"type:varchar(16);not null;index:idx_user_sessions_user_status_expiry,priority:2;index:idx_user_sessions_status_revoked,priority:1"`
	RefreshHash         string `json:"-" gorm:"type:char(64);not null"`
	PreviousRefreshHash string `json:"-" gorm:"type:varchar(64)"`
	PreviousValidUntil  int64  `json:"-" gorm:"type:bigint;not null;default:0"`
	LoginMethod         string `json:"login_method" gorm:"type:varchar(32);not null"`
	IP                  string `json:"ip" gorm:"type:varchar(64)"`
	UserAgent           string `json:"user_agent" gorm:"type:text"`
	CreatedAt           int64  `json:"created_at" gorm:"autoCreateTime;column:created_at;index:idx_user_sessions_user_created,priority:2"`
	LastActiveAt        int64  `json:"last_active_at" gorm:"type:bigint;not null;column:last_active_at"`
	ExpiresAt           int64  `json:"expires_at" gorm:"type:bigint;not null;column:expires_at;index:idx_user_sessions_user_status_expiry,priority:3;index:idx_user_sessions_expires_at"`
	RevokedAt           int64  `json:"revoked_at,omitempty" gorm:"type:bigint;not null;default:0;column:revoked_at;index:idx_user_sessions_status_revoked,priority:2"`
	RevokedReason       string `json:"revoked_reason,omitempty" gorm:"type:varchar(64);column:revoked_reason"`
}

func (UserSession) TableName() string {
	return "user_sessions"
}

func (session *UserSession) AfterFind(_ *gorm.DB) error {
	session.PreviousRefreshHash = strings.TrimSpace(session.PreviousRefreshHash)
	return nil
}

func userSessionIdleCutoff(now int64) int64 {
	idleTimeout := common.UserSessionIdleTimeoutSeconds
	if idleTimeout <= 0 {
		return now
	}
	if now < math.MinInt64+idleTimeout {
		return math.MinInt64
	}
	return now - idleTimeout
}

func (session *UserSession) EffectiveExpiresAt() int64 {
	if session == nil || session.LastActiveAt <= 0 || common.UserSessionIdleTimeoutSeconds <= 0 {
		return 0
	}
	idleExpiresAt := int64(math.MaxInt64)
	if session.LastActiveAt <= math.MaxInt64-common.UserSessionIdleTimeoutSeconds {
		idleExpiresAt = session.LastActiveAt + common.UserSessionIdleTimeoutSeconds
	}
	return min(session.ExpiresAt, idleExpiresAt)
}

func (session *UserSession) IsActiveAt(now int64) bool {
	return session != nil &&
		session.Status == UserSessionStatusActive &&
		session.RevokedAt == 0 &&
		now < session.EffectiveExpiresAt()
}

type userSessionCacheEntry struct {
	SID             string
	UserID          int
	Version         int64
	UserAuthVersion int64
	Status          string
	LoginMethod     string
	IP              string
	UserAgent       string
	CreatedAt       int64
	LastActiveAt    int64
	ExpiresAt       int64
	RevokedAt       int64
	RevokedReason   string
	CacheSchema     int
}

func (session *UserSession) cacheEntry() *userSessionCacheEntry {
	return &userSessionCacheEntry{
		SID:             session.SID,
		UserID:          session.UserID,
		Version:         session.Version,
		UserAuthVersion: session.UserAuthVersion,
		Status:          session.Status,
		LoginMethod:     session.LoginMethod,
		IP:              session.IP,
		UserAgent:       session.UserAgent,
		CreatedAt:       session.CreatedAt,
		LastActiveAt:    session.LastActiveAt,
		ExpiresAt:       session.ExpiresAt,
		RevokedAt:       session.RevokedAt,
		RevokedReason:   session.RevokedReason,
		CacheSchema:     userSessionCacheSchema,
	}
}

func (entry *userSessionCacheEntry) session() *UserSession {
	return &UserSession{
		SID:             entry.SID,
		UserID:          entry.UserID,
		Version:         entry.Version,
		UserAuthVersion: entry.UserAuthVersion,
		Status:          entry.Status,
		LoginMethod:     entry.LoginMethod,
		IP:              entry.IP,
		UserAgent:       entry.UserAgent,
		CreatedAt:       entry.CreatedAt,
		LastActiveAt:    entry.LastActiveAt,
		ExpiresAt:       entry.ExpiresAt,
		RevokedAt:       entry.RevokedAt,
		RevokedReason:   entry.RevokedReason,
	}
}

func userSessionCacheKey(sid string) string {
	digest := common.GenerateHMACWithKey([]byte("user-session-cache-v1:"+common.SessionSecret), sid)
	return "auth:session:" + digest
}

func userSessionCacheDeadline() time.Time {
	return time.Now().Add(time.Duration(userCacheTTLSeconds()) * time.Second)
}

func prepareUserSessionForCreate(session *UserSession, now int64, requireAuthVersion bool) error {
	if session == nil || session.SID == "" || session.UserID <= 0 || session.RefreshHash == "" || session.ExpiresAt <= now {
		return ErrUserSessionInvalid
	}
	if requireAuthVersion && session.UserAuthVersion <= 0 {
		return ErrUserSessionInvalid
	}
	if session.Version <= 0 {
		session.Version = 1
	}
	if session.Status == "" {
		session.Status = UserSessionStatusActive
	}
	if session.Status != UserSessionStatusActive || session.RevokedAt != 0 {
		return ErrUserSessionInvalid
	}
	if session.LastActiveAt == 0 {
		session.LastActiveAt = now
	}
	if session.CreatedAt == 0 {
		session.CreatedAt = now
	}
	return nil
}

func publishCreatedUserSession(session *UserSession, cacheDeadline time.Time) error {
	if err := writeUserSessionCache(session.cacheEntry(), cacheDeadline); err != nil {
		if errors.Is(err, errUserSessionCacheObservationStale) {
			return confirmUserSessionActiveSnapshot(session)
		}
		if errors.Is(err, ErrUserSessionInactive) {
			return err
		}
		common.SysLog("failed to populate newly created user session cache: " + err.Error())
	}
	return nil
}

func CreateUserSession(session *UserSession) error {
	if err := prepareUserSessionForCreate(session, time.Now().Unix(), true); err != nil {
		return err
	}
	cacheDeadline := userSessionCacheDeadline()
	if err := DB.Create(session).Error; err != nil {
		return err
	}
	return publishCreatedUserSession(session, cacheDeadline)
}

// AdmitUserSession serializes authoritative user validation, both admission
// limits, and the new session insert. Callers may provide an expected auth
// version for flows that must not survive a security-version change.
func AdmitUserSession(session *UserSession, expectedAuthVersion, now int64, limits UserSessionAdmissionLimits) error {
	if expectedAuthVersion < 0 || now <= 0 || limits.Active <= 0 || limits.Issuance <= 0 || limits.WindowSeconds <= 0 {
		return ErrUserSessionInvalid
	}
	if err := prepareUserSessionForCreate(session, now, false); err != nil {
		return err
	}

	cacheDeadline := userSessionCacheDeadline()
	err := DB.Transaction(func(tx *gorm.DB) error {
		if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
			if err := tx.Model(&User{}).
				Where("id = ?", session.UserID).
				UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
				return err
			}
		}

		var user User
		if err := lockForUpdate(tx).
			Select("id", "status", "auth_version").
			Where("id = ?", session.UserID).
			First(&user).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled || user.AuthVersion <= 0 {
			return ErrUserSessionInvalid
		}
		if expectedAuthVersion > 0 && user.AuthVersion != expectedAuthVersion {
			return ErrUserSessionAuthVersionMismatch
		}

		var activeCount int64
		if err := tx.Model(&UserSession{}).
			Where(
				"user_id = ? AND status = ? AND revoked_at = ? AND expires_at > ? AND last_active_at > ?",
				session.UserID,
				UserSessionStatusActive,
				0,
				now,
				userSessionIdleCutoff(now),
			).
			Count(&activeCount).Error; err != nil {
			return err
		}
		if activeCount >= int64(limits.Active) {
			return ErrUserSessionLimit
		}

		var issuanceCount int64
		if err := tx.Model(&UserSession{}).
			Where("user_id = ? AND created_at > ?", session.UserID, now-limits.WindowSeconds).
			Count(&issuanceCount).Error; err != nil {
			return err
		}
		if issuanceCount >= int64(limits.Issuance) {
			return ErrUserSessionIssuanceLimit
		}

		session.UserAuthVersion = user.AuthVersion
		return tx.Create(session).Error
	})
	if err != nil {
		return err
	}
	return publishCreatedUserSession(session, cacheDeadline)
}

func CountActiveUserSessions(userID int, now int64) (int64, error) {
	if userID <= 0 {
		return 0, ErrUserSessionInvalid
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	var count int64
	err := DB.Model(&UserSession{}).
		Where(
			"user_id = ? AND status = ? AND revoked_at = ? AND expires_at > ? AND last_active_at > ?",
			userID,
			UserSessionStatusActive,
			0,
			now,
			userSessionIdleCutoff(now),
		).
		Count(&count).Error
	return count, err
}

// CountUserSessionsCreatedSince counts every issued row, regardless of its
// current status or expiry. userID zero selects the global count.
func CountUserSessionsCreatedSince(userID int, createdAfter int64) (int64, error) {
	if userID < 0 || createdAfter <= 0 {
		return 0, ErrUserSessionInvalid
	}
	query := DB.Model(&UserSession{}).Where("created_at > ?", createdAfter)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	var count int64
	err := query.Count(&count).Error
	return count, err
}

func GetUserSessionBySID(sid string) (*UserSession, error) {
	if sid == "" {
		return nil, ErrUserSessionInvalid
	}
	var session UserSession
	if err := DB.Where("sid = ?", sid).First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// GetActiveUserSessionBySIDAt reads the authoritative row and durably revokes
// a session that has reached its idle deadline. The conditional update makes
// SQLite race-safe without FOR UPDATE: a concurrent refresh that advances
// last_active_at wins the CAS and the row is re-read instead of being revoked.
func GetActiveUserSessionBySIDAt(sid string, now int64) (*UserSession, error) {
	if sid == "" || now <= 0 {
		return nil, ErrUserSessionInvalid
	}
	session, err := GetUserSessionBySID(sid)
	if err != nil {
		return nil, err
	}
	if session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now {
		return nil, ErrUserSessionInactive
	}
	if session.LastActiveAt > userSessionIdleCutoff(now) {
		return session, nil
	}

	for range 3 {
		var current UserSession
		var idleRevoked bool
		err = DB.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx).Where("sid = ?", sid).First(&current).Error; err != nil {
				return err
			}
			if current.Status != UserSessionStatusActive || current.RevokedAt != 0 || current.ExpiresAt <= now {
				return ErrUserSessionInactive
			}
			if current.LastActiveAt > userSessionIdleCutoff(now) {
				return nil
			}
			result := tx.Model(&UserSession{}).
				Where(
					"sid = ? AND status = ? AND revoked_at = ? AND expires_at > ? AND last_active_at = ?",
					sid,
					UserSessionStatusActive,
					0,
					now,
					current.LastActiveAt,
				).
				Updates(map[string]interface{}{
					"status":         UserSessionStatusRevoked,
					"revoked_at":     now,
					"revoked_reason": UserSessionRevokedReasonIdleTimeout,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errUserSessionCacheObservationStale
			}
			current.Status = UserSessionStatusRevoked
			current.RevokedAt = now
			current.RevokedReason = UserSessionRevokedReasonIdleTimeout
			idleRevoked = true
			return nil
		})
		if errors.Is(err, errUserSessionCacheObservationStale) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if idleRevoked {
			if err := writeUserSessionCache(current.cacheEntry(), time.Time{}); err != nil {
				common.SysLog("failed to cache idle-timeout session revoke: " + err.Error())
			}
			return nil, ErrUserSessionInactive
		}
		return &current, nil
	}
	return nil, ErrUserSessionInactive
}

// GetUserSessionCached validates cached state first and falls back to the
// database on a miss or Redis read failure. A deny tombstone never falls back.
func GetUserSessionCached(sid string) (*UserSession, error) {
	return GetUserSessionCachedAt(sid, time.Now().Unix())
}

func GetUserSessionCachedAt(sid string, now int64) (*UserSession, error) {
	if sid == "" {
		return nil, ErrUserSessionInvalid
	}
	if now <= 0 {
		return nil, ErrUserSessionInvalid
	}
	if common.RedisEnabled {
		entry, err := getUserSessionCacheAt(sid, now)
		if err == nil {
			return entry.session(), nil
		}
		if errors.Is(err, ErrUserSessionInactive) {
			return nil, err
		}
		if errors.Is(err, errUserSessionIdle) {
			session, authoritativeErr := GetActiveUserSessionBySIDAt(sid, now)
			if authoritativeErr != nil {
				return nil, authoritativeErr
			}
			cacheDeadline := userSessionCacheDeadline()
			if err := writeUserSessionCache(session.cacheEntry(), cacheDeadline); err != nil {
				common.SysLog("failed to refresh stale idle session cache: " + err.Error())
			}
			return session, nil
		}
	}

	cacheDeadline := userSessionCacheDeadline()
	session, err := GetActiveUserSessionBySIDAt(sid, now)
	if err != nil {
		return nil, err
	}
	if common.RedisEnabled {
		if err := writeUserSessionCache(session.cacheEntry(), cacheDeadline); err != nil {
			if errors.Is(err, errUserSessionCacheObservationStale) {
				if confirmErr := confirmUserSessionActiveSnapshot(session); confirmErr != nil {
					return nil, confirmErr
				}
				return session, nil
			}
			if errors.Is(err, ErrUserSessionInactive) {
				return nil, err
			}
			common.SysLog("failed to synchronously populate user session cache: " + err.Error())
		}
	}
	return session, nil
}

func getUserSessionCache(sid string) (*userSessionCacheEntry, error) {
	return getUserSessionCacheAt(sid, time.Now().Unix())
}

func getUserSessionCacheAt(sid string, now int64) (*userSessionCacheEntry, error) {
	var entry userSessionCacheEntry
	if err := common.RedisHGetObj(userSessionCacheKey(sid), &entry); err != nil {
		return nil, err
	}
	if entry.CacheSchema != userSessionCacheSchema || entry.SID != sid || entry.UserID <= 0 || entry.Version <= 0 || entry.UserAuthVersion <= 0 {
		return nil, fmt.Errorf("user session cache schema is stale")
	}
	if entry.Status != UserSessionStatusActive || entry.RevokedAt != 0 || entry.ExpiresAt <= now {
		return nil, ErrUserSessionInactive
	}
	if entry.LastActiveAt <= userSessionIdleCutoff(now) {
		return nil, errUserSessionIdle
	}
	return &entry, nil
}

// writeUserSessionCache writes a bounded Session snapshot. Active snapshots
// must carry a deadline captured immediately before their authoritative
// database read or mutation. Delayed fills inherit the unspent portion of that
// window, so a stale active snapshot cannot outlive a short deny tombstone and
// reactivate a revoked Session after the tombstone expires. Deny states pass a
// zero deadline because their TTL starts when they are published.
func writeUserSessionCache(entry *userSessionCacheEntry, cacheDeadline time.Time) error {
	if entry == nil || !common.RedisEnabled {
		return nil
	}
	now := time.Now()
	cacheExpiresAtUnix := entry.ExpiresAt
	if entry.Status == UserSessionStatusActive {
		cacheExpiresAtUnix = entry.session().EffectiveExpiresAt()
	}
	sessionExpiresAt := time.Unix(cacheExpiresAtUnix, 0)
	sessionTTL := sessionExpiresAt.Sub(now)
	var redisExpiration int64
	if entry.Status == UserSessionStatusActive {
		if cacheDeadline.IsZero() {
			return ErrUserSessionInvalid
		}
		cacheTTL := cacheDeadline.Sub(now)
		if cacheTTL <= 0 {
			return errUserSessionCacheObservationStale
		}
		if sessionTTL <= 0 {
			return ErrUserSessionInactive
		}
		cacheExpiresAt := cacheDeadline
		if sessionExpiresAt.Before(cacheExpiresAt) {
			cacheExpiresAt = sessionExpiresAt
		}
		if cacheExpiresAt.Sub(now) < time.Millisecond {
			return errUserSessionCacheObservationStale
		}
		redisExpiration = cacheExpiresAt.UnixMilli()
	} else {
		ttl := min(sessionTTL, time.Duration(userCacheTTLSeconds())*time.Second)
		if ttl <= 0 {
			ttl = time.Second
		}
		redisExpiration = ttl.Milliseconds()
		if redisExpiration <= 0 {
			redisExpiration = 1
		}
	}
	entry.CacheSchema = userSessionCacheSchema
	const script = `
local current_status = redis.call('HGET', KEYS[1], 'Status')
local current_version = tonumber(redis.call('HGET', KEYS[1], 'Version') or '0')
if ARGV[5] == 'active' and (current_status == 'revoking' or current_status == 'revoked') then
  return 0
end
if current_version > tonumber(ARGV[3]) then
  return 0
end
redis.call('HSET', KEYS[1],
  'SID', ARGV[1], 'UserID', ARGV[2], 'Version', ARGV[3],
  'UserAuthVersion', ARGV[4], 'Status', ARGV[5],
  'LoginMethod', ARGV[6], 'IP', ARGV[7], 'UserAgent', ARGV[8],
  'CreatedAt', ARGV[9], 'LastActiveAt', ARGV[10], 'ExpiresAt', ARGV[11],
  'RevokedAt', ARGV[12], 'RevokedReason', ARGV[13], 'CacheSchema', ARGV[14])
if ARGV[5] == 'active' then
  redis.call('PEXPIREAT', KEYS[1], ARGV[15])
else
  redis.call('PEXPIRE', KEYS[1], ARGV[15])
end
return 1`
	result, err := common.RDB.Eval(context.Background(), script, []string{userSessionCacheKey(entry.SID)},
		entry.SID, entry.UserID, entry.Version, entry.UserAuthVersion, entry.Status,
		entry.LoginMethod, entry.IP, entry.UserAgent, entry.CreatedAt, entry.LastActiveAt,
		entry.ExpiresAt, entry.RevokedAt, entry.RevokedReason, entry.CacheSchema, redisExpiration,
	).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrUserSessionInactive
	}
	if entry.Status == UserSessionStatusActive {
		completedAt := time.Now()
		if !completedAt.Before(cacheDeadline) {
			return errUserSessionCacheObservationStale
		}
		if !completedAt.Before(sessionExpiresAt) {
			return ErrUserSessionInactive
		}
	}
	return nil
}

func confirmUserSessionActiveSnapshot(session *UserSession) error {
	if session == nil || session.SID == "" || session.UserID <= 0 || session.Version <= 0 || session.UserAuthVersion <= 0 {
		return ErrUserSessionInvalid
	}
	now := time.Now().Unix()
	var count int64
	err := DB.Model(&UserSession{}).
		Where(
			"sid = ? AND user_id = ? AND status = ? AND revoked_at = ? AND expires_at > ? AND last_active_at > ? AND version = ? AND user_auth_version = ?",
			session.SID,
			session.UserID,
			UserSessionStatusActive,
			0,
			now,
			userSessionIdleCutoff(now),
			session.Version,
			session.UserAuthVersion,
		).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrUserSessionInactive
	}
	return nil
}

func writeUserSessionDenyFence(session *UserSession, status string, now int64, reason string) error {
	if !common.RedisEnabled {
		return nil
	}
	entry := session.cacheEntry()
	entry.Status = status
	entry.RevokedAt = now
	entry.RevokedReason = reason
	return writeUserSessionCache(entry, time.Time{})
}

func ListActiveUserSessions(userID int, currentSID string, now int64) ([]UserSession, error) {
	if userID <= 0 {
		return nil, ErrUserSessionInvalid
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	var authVersion int64
	if err := DB.Model(&User{}).Where("id = ?", userID).Select("auth_version").Find(&authVersion).Error; err != nil {
		return nil, err
	}
	if authVersion <= 0 {
		return nil, ErrUserSessionInvalid
	}
	sessions := make([]UserSession, 0, userSessionListLimit)
	if currentSID != "" {
		var current []UserSession
		if err := DB.Where(
			"user_id = ? AND user_auth_version = ? AND status = ? AND revoked_at = ? AND expires_at > ? AND last_active_at > ? AND sid = ?",
			userID,
			authVersion,
			UserSessionStatusActive,
			0,
			now,
			userSessionIdleCutoff(now),
			currentSID,
		).Limit(1).Find(&current).Error; err != nil {
			return nil, err
		}
		if len(current) == 1 {
			sessions = append(sessions, current[0])
		}
	}
	remainingLimit := userSessionListLimit - len(sessions)

	otherQuery := DB.Where(
		"user_id = ? AND user_auth_version = ? AND status = ? AND revoked_at = ? AND expires_at > ? AND last_active_at > ?",
		userID,
		authVersion,
		UserSessionStatusActive,
		0,
		now,
		userSessionIdleCutoff(now),
	)
	if currentSID != "" {
		otherQuery = otherQuery.Where("sid <> ?", currentSID)
	}
	var others []UserSession
	if err := otherQuery.Order("last_active_at DESC").Order("created_at DESC").Limit(remainingLimit).Find(&others).Error; err != nil {
		return nil, err
	}
	sessions = append(sessions, others...)
	return sessions, nil
}

// RotateUserSessionRefresh atomically rotates HMAC digests. The UPDATE itself
// is a compare-and-swap so SQLite, where lockForUpdate is intentionally a
// no-op, has the same single-winner behavior as MySQL and PostgreSQL. Only a
// recognized previous digest outside its grace window is treated as reuse;
// an unknown secret never revokes the victim session.
func RotateUserSessionRefresh(userID int, sid, presentedHash, nextHash string, now int64, grace time.Duration) (*UserSession, error) {
	if userID <= 0 || sid == "" || presentedHash == "" || nextHash == "" || hmac.Equal([]byte(presentedHash), []byte(nextHash)) {
		return nil, ErrUserSessionInvalid
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	graceSeconds := int64(grace / time.Second)
	if graceSeconds < 0 {
		return nil, ErrUserSessionInvalid
	}
	for range 3 {
		cacheDeadline := userSessionCacheDeadline()
		var session UserSession
		if err := DB.Where("sid = ? AND user_id = ?", sid, userID).First(&session).Error; err != nil {
			return nil, err
		}
		if session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now {
			return nil, ErrUserSessionInactive
		}
		if session.LastActiveAt <= userSessionIdleCutoff(now) {
			_, err := GetActiveUserSessionBySIDAt(sid, now)
			if errors.Is(err, ErrUserSessionInactive) {
				return nil, ErrUserSessionInactive
			}
			if err != nil {
				return nil, err
			}
			continue
		}

		if hmac.Equal([]byte(session.RefreshHash), []byte(presentedHash)) {
			result := DB.Model(&UserSession{}).
				Where(
					"sid = ? AND user_id = ? AND status = ? AND revoked_at = ? AND expires_at > ? AND last_active_at > ? AND refresh_hash = ?",
					sid,
					userID,
					UserSessionStatusActive,
					0,
					now,
					userSessionIdleCutoff(now),
					presentedHash,
				).
				Updates(map[string]interface{}{
					"previous_refresh_hash": session.RefreshHash,
					"previous_valid_until":  now + graceSeconds,
					"refresh_hash":          nextHash,
					"last_active_at":        now,
				})
			if result.Error != nil {
				return nil, result.Error
			}
			if result.RowsAffected == 0 {
				continue
			}
			session.PreviousRefreshHash = session.RefreshHash
			session.PreviousValidUntil = now + graceSeconds
			session.RefreshHash = nextHash
			session.LastActiveAt = now
			if err := writeUserSessionCache(session.cacheEntry(), cacheDeadline); err != nil {
				if errors.Is(err, errUserSessionCacheObservationStale) {
					if confirmErr := confirmUserSessionActiveSnapshot(&session); confirmErr != nil {
						return nil, confirmErr
					}
				} else if errors.Is(err, ErrUserSessionInactive) {
					return nil, err
				} else {
					common.SysLog("failed to update rotated user session cache: " + err.Error())
				}
			}
			return &session, nil
		}

		if session.PreviousRefreshHash == "" || !hmac.Equal([]byte(session.PreviousRefreshHash), []byte(presentedHash)) {
			return nil, ErrUserSessionRefreshInvalid
		}
		if now <= session.PreviousValidUntil {
			return &session, ErrUserSessionRefreshRace
		}

		// Once a known previous token is replayed outside the grace window the
		// whole token family is compromised. Publish the deny fence first, then
		// revoke the active row regardless of a concurrent refresh rotation.
		if err := writeUserSessionDenyFence(&session, UserSessionStatusRevoking, now, "refresh_reuse"); err != nil {
			return nil, err
		}
		result := DB.Model(&UserSession{}).
			Where("sid = ? AND user_id = ? AND status = ? AND revoked_at = ? AND expires_at > ?",
				sid, userID, UserSessionStatusActive, 0, now).
			Updates(map[string]interface{}{
				"status":         UserSessionStatusRevoked,
				"revoked_at":     now,
				"revoked_reason": "refresh_reuse",
			})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			return nil, ErrUserSessionInactive
		}
		session.Status = UserSessionStatusRevoked
		session.RevokedAt = now
		session.RevokedReason = "refresh_reuse"
		if err := writeUserSessionCache(session.cacheEntry(), time.Time{}); err != nil {
			common.SysLog("failed to cache refresh-reuse session revoke: " + err.Error())
		}
		return nil, ErrUserSessionRefreshReuse
	}
	return nil, ErrUserSessionRefreshInvalid
}

// RecoverUserSessionRefresh authoritatively confirms that a recognized
// previous refresh token still resolves to the deterministic successor and
// the same session security versions. The transaction provides the recovery
// decision's linearization point: row locking is used on MySQL/PostgreSQL,
// while SQLite first acquires its write reservation before reading the row.
//
// The caller reaches this path after comparing the session against a cached
// user snapshot, so the user row is re-read under the same lock the auth-epoch
// bump takes. That serializes recovery against a concurrent password change,
// ban, or deletion instead of reissuing a refresh bundle for a revoked epoch.
// The user is locked before the session because IncrementUserAuthVersionWithTx
// also takes the user lock first.
func RecoverUserSessionRefresh(
	userID int,
	sid, presentedPreviousHash, expectedCurrentHash string,
	expectedVersion, expectedAuthVersion, now int64,
) (*UserSession, error) {
	if userID <= 0 || sid == "" || presentedPreviousHash == "" || expectedCurrentHash == "" ||
		expectedVersion <= 0 || expectedAuthVersion <= 0 || now <= 0 {
		return nil, ErrUserSessionInvalid
	}

	var recovered UserSession
	err := DB.Transaction(func(tx *gorm.DB) error {
		if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
			if err := tx.Model(&UserSession{}).
				Where("sid = ? AND user_id = ?", sid, userID).
				UpdateColumn("version", gorm.Expr("version")).Error; err != nil {
				return err
			}
		}
		var owner User
		if err := lockForUpdate(tx).
			Select("id", "status", "auth_version").
			Where("id = ?", userID).
			First(&owner).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserSessionInactive
			}
			return err
		}
		if owner.Status != common.UserStatusEnabled || owner.AuthVersion != expectedAuthVersion {
			return ErrUserSessionInactive
		}
		if err := lockForUpdate(tx).
			Where("sid = ? AND user_id = ?", sid, userID).
			First(&recovered).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserSessionInactive
			}
			return err
		}
		if recovered.Status != UserSessionStatusActive || recovered.RevokedAt != 0 || recovered.ExpiresAt <= now {
			return ErrUserSessionInactive
		}
		if !recovered.IsActiveAt(now) {
			return errUserSessionIdle
		}
		if recovered.Version != expectedVersion || recovered.UserAuthVersion != expectedAuthVersion {
			return ErrUserSessionInactive
		}
		if recovered.PreviousRefreshHash == "" ||
			!hmac.Equal([]byte(recovered.PreviousRefreshHash), []byte(presentedPreviousHash)) ||
			!hmac.Equal([]byte(recovered.RefreshHash), []byte(expectedCurrentHash)) ||
			now > recovered.PreviousValidUntil {
			return ErrUserSessionRefreshRace
		}
		return nil
	})
	if errors.Is(err, errUserSessionIdle) {
		_, reconcileErr := GetActiveUserSessionBySIDAt(sid, now)
		if reconcileErr != nil && !errors.Is(reconcileErr, ErrUserSessionInactive) {
			return nil, reconcileErr
		}
		return nil, ErrUserSessionInactive
	}
	if err != nil {
		return nil, err
	}
	return &recovered, nil
}

func RevokeUserSession(userID int, sid, reason string) (bool, error) {
	if userID <= 0 || sid == "" {
		return false, ErrUserSessionInvalid
	}
	now := time.Now().Unix()
	var candidate UserSession
	if err := DB.Where("sid = ? AND user_id = ?", sid, userID).First(&candidate).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if candidate.Status != UserSessionStatusActive || candidate.RevokedAt != 0 || candidate.ExpiresAt <= now {
		return false, nil
	}
	if err := writeUserSessionDenyFence(&candidate, UserSessionStatusRevoking, now, reason); err != nil {
		return false, err
	}

	var revoked bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current UserSession
		if err := lockForUpdate(tx).Where("sid = ? AND user_id = ?", sid, userID).First(&current).Error; err != nil {
			return err
		}
		if current.Status != UserSessionStatusActive || current.RevokedAt != 0 || current.ExpiresAt <= now {
			return nil
		}
		result := tx.Model(&UserSession{}).Where("sid = ? AND status = ?", sid, UserSessionStatusActive).Updates(map[string]interface{}{
			"status":         UserSessionStatusRevoked,
			"revoked_at":     now,
			"revoked_reason": reason,
		})
		if result.Error != nil {
			return result.Error
		}
		revoked = result.RowsAffected == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	if revoked {
		candidate.Status = UserSessionStatusRevoked
		candidate.RevokedAt = now
		candidate.RevokedReason = reason
		if err := writeUserSessionCache(candidate.cacheEntry(), time.Time{}); err != nil {
			common.SysLog("failed to finalize user session revoke tombstone: " + err.Error())
		}
	}
	return revoked, nil
}

// RevokeUserSessionByRefreshHash is used when logout is authenticated only by
// the HttpOnly refresh cookie. Possession of a SID alone is insufficient. The
// immediately previous digest is accepted only inside the refresh race window.
func RevokeUserSessionByRefreshHash(sid, presentedHash, reason string) (bool, error) {
	if sid == "" || presentedHash == "" {
		return false, ErrUserSessionInvalid
	}
	now := time.Now().Unix()
	var session UserSession
	var revoked bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("sid = ?", sid).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now {
			return nil
		}
		validCurrent := hmac.Equal([]byte(session.RefreshHash), []byte(presentedHash))
		validPrevious := session.PreviousRefreshHash != "" && now <= session.PreviousValidUntil &&
			hmac.Equal([]byte(session.PreviousRefreshHash), []byte(presentedHash))
		if !validCurrent && !validPrevious {
			return nil
		}
		if err := writeUserSessionDenyFence(&session, UserSessionStatusRevoking, now, reason); err != nil {
			return err
		}
		result := tx.Model(&UserSession{}).Where("sid = ? AND status = ?", sid, UserSessionStatusActive).Updates(map[string]interface{}{
			"status":         UserSessionStatusRevoked,
			"revoked_at":     now,
			"revoked_reason": reason,
		})
		if result.Error != nil {
			return result.Error
		}
		revoked = result.RowsAffected == 1
		if revoked {
			session.Status = UserSessionStatusRevoked
			session.RevokedAt = now
			session.RevokedReason = reason
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if revoked {
		if err := writeUserSessionCache(session.cacheEntry(), time.Time{}); err != nil {
			common.SysLog("failed to finalize refresh-authenticated session revoke tombstone: " + err.Error())
		}
	}
	return revoked, nil
}

// AdvanceUserSessionAuthVersion preserves one browser session across a
// user-level security-version change. Both old access JWTs and concurrent
// updates are invalidated by advancing the per-session version as well.
func AdvanceUserSessionAuthVersion(userID int, sid string, expectedSessionVersion, expectedUserAuthVersion, nextUserAuthVersion int64) (*UserSession, error) {
	return AdvanceUserSessionAuthVersionAt(
		userID,
		sid,
		expectedSessionVersion,
		expectedUserAuthVersion,
		nextUserAuthVersion,
		time.Now().Unix(),
	)
}

func AdvanceUserSessionAuthVersionAt(userID int, sid string, expectedSessionVersion, expectedUserAuthVersion, nextUserAuthVersion, now int64) (*UserSession, error) {
	if userID <= 0 || sid == "" || expectedSessionVersion <= 0 || expectedUserAuthVersion <= 0 ||
		nextUserAuthVersion <= expectedUserAuthVersion || now <= 0 {
		return nil, ErrUserSessionInvalid
	}
	cacheDeadline := userSessionCacheDeadline()
	for range 3 {
		var session UserSession
		err := DB.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx).Where("sid = ? AND user_id = ?", sid, userID).First(&session).Error; err != nil {
				return err
			}
			if session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now {
				return ErrUserSessionInactive
			}
			if session.LastActiveAt <= userSessionIdleCutoff(now) {
				return errUserSessionIdle
			}
			if session.Version != expectedSessionVersion || session.UserAuthVersion != expectedUserAuthVersion {
				return ErrUserSessionInactive
			}
			session.Version++
			session.UserAuthVersion = nextUserAuthVersion
			result := tx.Model(&UserSession{}).
				Where(
					"sid = ? AND status = ? AND revoked_at = ? AND expires_at > ? AND last_active_at > ? AND version = ? AND user_auth_version = ?",
					sid,
					UserSessionStatusActive,
					0,
					now,
					userSessionIdleCutoff(now),
					expectedSessionVersion,
					expectedUserAuthVersion,
				).
				Updates(map[string]interface{}{
					"version":           session.Version,
					"user_auth_version": session.UserAuthVersion,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrUserSessionInactive
			}
			return nil
		})
		if errors.Is(err, errUserSessionIdle) {
			active, reconcileErr := GetActiveUserSessionBySIDAt(sid, now)
			if reconcileErr != nil {
				return nil, reconcileErr
			}
			if active.UserID != userID {
				return nil, ErrUserSessionInactive
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := writeUserSessionCache(session.cacheEntry(), cacheDeadline); err != nil {
			if errors.Is(err, errUserSessionCacheObservationStale) {
				if confirmErr := confirmUserSessionActiveSnapshot(&session); confirmErr != nil {
					return nil, confirmErr
				}
			} else {
				return nil, err
			}
		}
		return &session, nil
	}
	return nil, ErrUserSessionInactive
}

func RevokeOtherUserSessions(userID int, currentSID, reason string) (int64, error) {
	return revokeUserSessions(userID, currentSID, reason)
}

func RevokeAllUserSessions(userID int, reason string) (int64, error) {
	return revokeUserSessions(userID, "", reason)
}

func revokeUserSessions(userID int, excludedSID, reason string) (int64, error) {
	if userID <= 0 {
		return 0, ErrUserSessionInvalid
	}
	now := time.Now().Unix()
	var totalAffected int64
	for {
		query := DB.Where("user_id = ? AND status = ? AND expires_at > ?", userID, UserSessionStatusActive, now)
		if excludedSID != "" {
			query = query.Where("sid <> ?", excludedSID)
		}
		var candidates []UserSession
		if err := query.Order("sid").Limit(userSessionRevokeBatchSize).Find(&candidates).Error; err != nil {
			return totalAffected, err
		}
		if len(candidates) == 0 {
			return totalAffected, nil
		}
		for i := range candidates {
			if err := writeUserSessionDenyFence(&candidates[i], UserSessionStatusRevoking, now, reason); err != nil {
				return totalAffected, err
			}
		}

		sids := make([]string, 0, len(candidates))
		for i := range candidates {
			sids = append(sids, candidates[i].SID)
		}
		var affected int64
		var revoked []UserSession
		err := DB.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx).Where("sid IN ? AND status = ?", sids, UserSessionStatusActive).Find(&revoked).Error; err != nil {
				return err
			}
			if len(revoked) == 0 {
				return nil
			}
			lockedSIDs := make([]string, 0, len(revoked))
			for i := range revoked {
				lockedSIDs = append(lockedSIDs, revoked[i].SID)
			}
			result := tx.Model(&UserSession{}).Where("sid IN ? AND status = ?", lockedSIDs, UserSessionStatusActive).Updates(map[string]interface{}{
				"status":         UserSessionStatusRevoked,
				"revoked_at":     now,
				"revoked_reason": reason,
			})
			affected = result.RowsAffected
			return result.Error
		})
		if err != nil {
			return totalAffected, err
		}
		totalAffected += affected
		for i := range revoked {
			revoked[i].Status = UserSessionStatusRevoked
			revoked[i].RevokedAt = now
			revoked[i].RevokedReason = reason
			if err := writeUserSessionCache(revoked[i].cacheEntry(), time.Time{}); err != nil {
				common.SysLog("failed to finalize bulk user session revoke tombstone: " + err.Error())
			}
		}
	}
}

func DeleteExpiredUserSessions(now int64) error {
	if now <= 0 {
		now = time.Now().Unix()
	}
	if common.UserSessionRevokedRetentionDays <= 0 || common.UserSessionIssuanceWindowSeconds <= 0 {
		return ErrUserSessionInvalid
	}
	issuanceCutoff := now - common.UserSessionIssuanceWindowSeconds
	revokedBefore := now - int64(common.UserSessionRevokedRetentionDays)*24*60*60
	return deleteExpiredUserSessionsBefore(now, issuanceCutoff, revokedBefore)
}

func DeleteOldRevokedUserSessions(now int64) error {
	if now <= 0 {
		now = time.Now().Unix()
	}
	if common.UserSessionRevokedRetentionDays <= 0 || common.UserSessionIssuanceWindowSeconds <= 0 {
		return ErrUserSessionInvalid
	}
	issuanceCutoff := now - common.UserSessionIssuanceWindowSeconds
	revokedBefore := now - int64(common.UserSessionRevokedRetentionDays)*24*60*60
	return deleteRevokedUserSessionsBefore(revokedBefore, issuanceCutoff)
}

func deleteExpiredUserSessionsBefore(expiredBefore, issuanceCutoff, revokedBefore int64) error {
	for {
		var sids []string
		if err := DB.Model(&UserSession{}).
			Where(
				"expires_at < ? AND created_at <= ? AND (status <> ? OR revoked_at <= 0 OR revoked_at < ?)",
				expiredBefore,
				issuanceCutoff,
				UserSessionStatusRevoked,
				revokedBefore,
			).
			Order("expires_at").Limit(userSessionCleanupScanLimit).Pluck("sid", &sids).Error; err != nil {
			return err
		}
		if len(sids) == 0 {
			return nil
		}
		for start := 0; start < len(sids); start += userSessionCleanupBatchSize {
			end := start + userSessionCleanupBatchSize
			if end > len(sids) {
				end = len(sids)
			}
			if err := DB.Where("sid IN ?", sids[start:end]).
				Where(
					"expires_at < ? AND created_at <= ? AND (status <> ? OR revoked_at <= 0 OR revoked_at < ?)",
					expiredBefore,
					issuanceCutoff,
					UserSessionStatusRevoked,
					revokedBefore,
				).
				Delete(&UserSession{}).Error; err != nil {
				return err
			}
		}
	}
}

func deleteRevokedUserSessionsBefore(revokedBefore, issuanceCutoff int64) error {
	for {
		var sids []string
		if err := DB.Model(&UserSession{}).
			Where(
				"status = ? AND revoked_at > 0 AND revoked_at < ? AND created_at <= ?",
				UserSessionStatusRevoked,
				revokedBefore,
				issuanceCutoff,
			).
			Order("revoked_at").Limit(userSessionCleanupScanLimit).Pluck("sid", &sids).Error; err != nil {
			return err
		}
		if len(sids) == 0 {
			return nil
		}
		for start := 0; start < len(sids); start += userSessionCleanupBatchSize {
			end := start + userSessionCleanupBatchSize
			if end > len(sids) {
				end = len(sids)
			}
			if err := DB.Where("sid IN ?", sids[start:end]).
				Where(
					"status = ? AND revoked_at > 0 AND revoked_at < ? AND created_at <= ?",
					UserSessionStatusRevoked,
					revokedBefore,
					issuanceCutoff,
				).
				Delete(&UserSession{}).Error; err != nil {
				return err
			}
		}
	}
}
