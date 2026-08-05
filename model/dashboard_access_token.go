package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DashboardAccessTokenScopeUserAPI = "user_api"
	DashboardAccessTokenTTL          = 90 * 24 * time.Hour
	dashboardAccessTokenRandomBytes  = 32
	dashboardAccessTokenPrefixLength = 12
	dashboardAccessTokenUseInterval  = 5 * time.Minute
)

var (
	ErrDashboardAccessTokenInvalid          = errors.New("dashboard access token is invalid")
	ErrDashboardAccessTokenExpired          = errors.New("dashboard access token has expired")
	ErrDashboardAccessTokenRevoked          = errors.New("dashboard access token has been revoked")
	errDashboardAccessTokenMigrationChanged = errors.New("legacy dashboard access token changed during migration")
)

type DashboardAccessToken struct {
	Id              int64  `json:"id" gorm:"primaryKey"`
	UserId          int    `json:"user_id" gorm:"not null;uniqueIndex"`
	TokenHash       string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	Prefix          string `json:"prefix" gorm:"type:varchar(16);not null"`
	Scope           string `json:"scope" gorm:"type:varchar(32);not null"`
	UserAuthVersion int64  `json:"-" gorm:"type:bigint;not null"`
	CreatedAt       int64  `json:"created_at" gorm:"type:bigint;not null"`
	ExpiresAt       int64  `json:"expires_at" gorm:"type:bigint;not null;index"`
	LastUsedAt      int64  `json:"last_used_at" gorm:"type:bigint;not null"`
	RevokedAt       *int64 `json:"revoked_at,omitempty" gorm:"type:bigint;index"`
	UpdatedAt       int64  `json:"updated_at" gorm:"type:bigint;not null"`
}

func (DashboardAccessToken) TableName() string {
	return "dashboard_access_tokens"
}

func dashboardAccessTokenHash(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func newDashboardAccessToken() (string, error) {
	random := make([]byte, dashboardAccessTokenRandomBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate dashboard access token: %w", err)
	}
	return "pat_" + hex.EncodeToString(random), nil
}

func RotateDashboardAccessToken(userID int, expectedAuthVersion int64) (string, int64, error) {
	if userID <= 0 || expectedAuthVersion <= 0 {
		return "", 0, ErrDashboardAccessTokenInvalid
	}
	raw, err := newDashboardAccessToken()
	if err != nil {
		return "", 0, err
	}
	now := common.GetTimestamp()
	expiresAt := now + int64(DashboardAccessTokenTTL/time.Second)
	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).
			Select("id", "auth_version").
			First(&user, userID).Error; err != nil {
			return err
		}
		if user.AuthVersion != expectedAuthVersion {
			return ErrDashboardAccessTokenInvalid
		}

		record := DashboardAccessToken{
			UserId:          userID,
			TokenHash:       dashboardAccessTokenHash(raw),
			Prefix:          raw[:dashboardAccessTokenPrefixLength],
			Scope:           DashboardAccessTokenScopeUserAPI,
			UserAuthVersion: expectedAuthVersion,
			CreatedAt:       now,
			ExpiresAt:       expiresAt,
			LastUsedAt:      0,
			UpdatedAt:       now,
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"token_hash":        record.TokenHash,
				"prefix":            record.Prefix,
				"scope":             record.Scope,
				"user_auth_version": record.UserAuthVersion,
				"created_at":        record.CreatedAt,
				"expires_at":        record.ExpiresAt,
				"last_used_at":      record.LastUsedAt,
				"revoked_at":        nil,
				"updated_at":        record.UpdatedAt,
			}),
		}).Create(&record).Error
	})
	if err != nil {
		return "", 0, err
	}
	return raw, expiresAt, nil
}

func RevokeDashboardAccessToken(userID int, expectedAuthVersion int64) error {
	if userID <= 0 || expectedAuthVersion <= 0 {
		return ErrDashboardAccessTokenInvalid
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).
			Select("id", "auth_version").
			First(&user, userID).Error; err != nil {
			return err
		}
		if user.AuthVersion != expectedAuthVersion {
			return ErrDashboardAccessTokenInvalid
		}
		now := common.GetTimestamp()
		return tx.Model(&DashboardAccessToken{}).
			Where("user_id = ? AND revoked_at IS NULL", userID).
			Updates(map[string]any{
				"revoked_at": now,
				"updated_at": now,
			}).Error
	})
}

func ValidateDashboardAccessToken(raw string) (*User, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "Bearer "))
	if raw == "" {
		return nil, nil
	}
	canonicalHash := dashboardAccessTokenHash(raw)
	var record DashboardAccessToken
	err := DB.Where("token_hash = ?", canonicalHash).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && len(raw) == 28 {
		legacyHash := dashboardAccessTokenHash(raw + "    ")
		err = DB.Where("token_hash = ?", legacyHash).First(&record).Error
		if err == nil {
			result := DB.Model(&DashboardAccessToken{}).
				Where("id = ? AND token_hash = ?", record.Id, legacyHash).
				Update("token_hash", canonicalHash)
			if result.Error != nil {
				return nil, fmt.Errorf("%w: %v", ErrDatabase, result.Error)
			}
			if result.RowsAffected != 1 {
				err = DB.Where("id = ? AND token_hash = ?", record.Id, canonicalHash).First(&record).Error
			} else {
				record.TokenHash = canonicalHash
			}
		}
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	now := common.GetTimestamp()
	if record.RevokedAt != nil {
		return nil, ErrDashboardAccessTokenRevoked
	}
	if record.ExpiresAt <= now {
		return nil, ErrDashboardAccessTokenExpired
	}
	if record.Scope != DashboardAccessTokenScopeUserAPI {
		return nil, ErrDashboardAccessTokenInvalid
	}
	var user User
	if err := DB.First(&user, record.UserId).Error; err != nil {
		return nil, err
	}
	if user.AuthVersion <= 0 || user.AuthVersion != record.UserAuthVersion {
		return nil, ErrDashboardAccessTokenInvalid
	}
	if record.LastUsedAt <= now-int64(dashboardAccessTokenUseInterval/time.Second) {
		if err := DB.Model(&DashboardAccessToken{}).
			Where("id = ? AND last_used_at = ?", record.Id, record.LastUsedAt).
			Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error; err != nil {
			common.SysError(fmt.Sprintf("failed to update dashboard access token last use: %v", err))
		}
	}
	return &user, nil
}

func InitializeDashboardAccessTokens() error {
	for {
		var users []User
		if err := DB.
			Where("access_token IS NOT NULL").
			Order("id").
			Limit(100).
			Find(&users).Error; err != nil {
			return err
		}
		if len(users) == 0 {
			return nil
		}
		for _, candidate := range users {
			err := DB.Transaction(func(tx *gorm.DB) error {
				var user User
				if err := lockForUpdate(tx).
					Select("id", "auth_version", "access_token").
					First(&user, candidate.Id).Error; err != nil {
					return err
				}
				if user.AccessToken == nil {
					return nil
				}
				return migrateLegacyDashboardAccessToken(tx, user.Id, user.AuthVersion, *user.AccessToken)
			})
			if errors.Is(err, errDashboardAccessTokenMigrationChanged) {
				continue
			}
			if err != nil {
				return err
			}
		}
	}
}

func migrateLegacyDashboardAccessToken(tx *gorm.DB, userID int, authVersion int64, storedRaw string) error {
	raw := strings.TrimSpace(storedRaw)
	if raw != "" {
		now := common.GetTimestamp()
		record := DashboardAccessToken{
			UserId:          userID,
			TokenHash:       dashboardAccessTokenHash(raw),
			Prefix:          raw[:min(len(raw), dashboardAccessTokenPrefixLength)],
			Scope:           DashboardAccessTokenScopeUserAPI,
			UserAuthVersion: authVersion,
			CreatedAt:       now,
			ExpiresAt:       now + int64(DashboardAccessTokenTTL/time.Second),
			LastUsedAt:      0,
			UpdatedAt:       now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoNothing: true,
		}).Create(&record).Error; err != nil {
			return err
		}
	}

	result := tx.Model(&User{}).
		Where("id = ? AND access_token = ?", userID, storedRaw).
		Update("access_token", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errDashboardAccessTokenMigrationChanged
	}
	return nil
}
