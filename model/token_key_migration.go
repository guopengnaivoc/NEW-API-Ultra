package model

import (
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	tokenKeyMigrationBatchSize = 500
	invalidTokenKeyPrefix      = "invalid"
)

type legacyTokenKeyRow struct {
	Id        int    `gorm:"column:id"`
	RawKey    string `gorm:"column:key"`
	KeyPrefix string `gorm:"column:key_prefix"`
}

func isSafeLegacyTokenKey(key string) bool {
	if len(key) < 32 || len(key) > 128 || strings.TrimSpace(key) != key {
		return false
	}
	if len(key) != sha256DigestHexLength {
		return true
	}
	_, err := hex.DecodeString(key)
	return err != nil
}

const sha256DigestHexLength = 64

// MigrateTokenKeys replaces recoverable legacy Relay Token values with
// SHA-256 digests. key_prefix is the idempotency marker: active legacy rows
// receive their display prefix, while ambiguous or weak rows are disabled and
// marked invalid rather than being treated as usable credentials.
func MigrateTokenKeys() error {
	initCol()
	if !DB.Migrator().HasTable(&Token{}) {
		return nil
	}
	if !DB.Migrator().HasColumn(&Token{}, "key_prefix") {
		return fmt.Errorf("token key migration requires key_prefix column")
	}

	var (
		lastID      int
		migrated    int64
		invalidated int64
	)
	for {
		var rows []legacyTokenKeyRow
		err := DB.Unscoped().
			Model(&Token{}).
			Select("id, "+commonKeyCol+", key_prefix").
			Where("id > ? AND (key_prefix IS NULL OR key_prefix = '')", lastID).
			Order("id ASC").
			Limit(tokenKeyMigrationBatchSize).
			Find(&rows).Error
		if err != nil {
			return fmt.Errorf("load legacy token keys: %w", err)
		}
		if len(rows) == 0 {
			break
		}

		err = DB.Transaction(func(tx *gorm.DB) error {
			for _, row := range rows {
				lastID = row.Id
				updates := map[string]any{
					"key":        HashTokenKey(row.RawKey),
					"key_prefix": TokenKeyPrefix(row.RawKey),
				}
				if !isSafeLegacyTokenKey(row.RawKey) {
					unrecoverableDigest := make([]byte, sha256DigestHexLength/2)
					if _, err := crand.Read(unrecoverableDigest); err != nil {
						return fmt.Errorf("generate invalidated token digest: %w", err)
					}
					updates["key"] = hex.EncodeToString(unrecoverableDigest)
					updates["key_prefix"] = invalidTokenKeyPrefix
					updates["status"] = common.TokenStatusDisabled
				}

				result := tx.Unscoped().
					Model(&Token{}).
					Where("id = ? AND (key_prefix IS NULL OR key_prefix = '')", row.Id).
					Updates(updates)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 0 {
					continue
				}
				if updates["key_prefix"] == invalidTokenKeyPrefix {
					invalidated++
				} else {
					migrated++
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("migrate legacy token keys: %w", err)
		}
	}

	if migrated > 0 || invalidated > 0 {
		common.SysLog(fmt.Sprintf(
			"Relay Token key migration completed: migrated=%d invalidated=%d",
			migrated,
			invalidated,
		))
	}
	return nil
}
