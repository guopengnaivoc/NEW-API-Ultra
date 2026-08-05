package model

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	customOAuthClientSecretDomain = "custom_oauth_providers:client_secret"
	reversibleSecretBatchSize     = 100
)

type storedCustomOAuthSecret struct {
	Id           int
	ClientSecret string
}

type storedOptionSecret struct {
	Key   string
	Value string
}

type storedTwoFASecret struct {
	Id     int
	Secret string
}

type storedChannelSecret struct {
	Id        int
	StoredKey string
}

type storedUserSettingSecret struct {
	Id            int
	StoredSetting string
}

type storedTaskProviderResultURI struct {
	Id        int64
	StoredURI string `gorm:"column:stored_uri"`
}

func MigrateCustomOAuthProviderSecrets() error {
	if !common.DataEncryptionEnabled() {
		return validateCustomOAuthProviderSecrets()
	}

	var migrated int
	var rewrapped int
	lastID := 0
	for {
		var rows []storedCustomOAuthSecret
		if err := DB.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx.Table("custom_oauth_providers")).
				Select("id", "client_secret").
				Where("id > ? AND client_secret <> ''", lastID).
				Order("id ASC").
				Limit(reversibleSecretBatchSize).
				Scan(&rows).Error; err != nil {
				return fmt.Errorf("scan reversible secret domain %s: %w", customOAuthClientSecretDomain, err)
			}

			for _, row := range rows {
				lastID = row.Id
				_, info, err := common.OpenLegacyDataEncryptionValueForMigration(
					customOAuthClientSecretDomain,
					row.ClientSecret,
				)
				if err != nil {
					return reversibleSecretStorageError(
						customOAuthClientSecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				replacement, changed, err := common.RewrapDataEncryptionValue(
					customOAuthClientSecretDomain,
					row.ClientSecret,
				)
				if err != nil {
					return reversibleSecretStorageError(
						customOAuthClientSecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				if !changed {
					continue
				}

				query := tx.Table("custom_oauth_providers").Where("id = ?", row.Id)
				if info.Encrypted {
					query = query.Where(
						"client_secret = ?",
						row.ClientSecret,
					)
				} else {
					query = query.Where("client_secret NOT LIKE ?", "naenc:%")
				}
				result := query.Update("client_secret", replacement)
				if result.Error != nil {
					return fmt.Errorf(
						"update reversible secret domain %s row %d: %w",
						customOAuthClientSecretDomain,
						row.Id,
						result.Error,
					)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf(
						"update reversible secret domain %s row %d: concurrent modification detected",
						customOAuthClientSecretDomain,
						row.Id,
					)
				}
				if info.Encrypted {
					rewrapped++
				} else {
					migrated++
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
	}

	common.SysLog(fmt.Sprintf(
		"reversible secret migration completed: domain=%s migrated=%d rewrapped=%d",
		customOAuthClientSecretDomain,
		migrated,
		rewrapped,
	))
	return validateCustomOAuthProviderSecrets()
}

func MigrateTaskProviderResultURISecrets() error {
	var migrated int
	var rewrapped int
	var lastID int64
	for {
		var rows []storedTaskProviderResultURI
		if err := DB.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx.Model(&Task{})).
				Select("id, provider_result_uri AS stored_uri").
				Where(
					"id > ? AND provider_result_uri IS NOT NULL AND provider_result_uri <> ''",
					lastID,
				).
				Order("id ASC").
				Limit(reversibleSecretBatchSize).
				Scan(&rows).Error; err != nil {
				return fmt.Errorf(
					"scan reversible secret domain %s: %w",
					taskProviderResultURIDomain,
					err,
				)
			}

			for _, row := range rows {
				lastID = row.Id
				plaintext, info, err := common.OpenLegacyDataEncryptionValueForMigration(
					taskProviderResultURIDomain,
					row.StoredURI,
				)
				if err != nil {
					return reversibleSecretStorageError(
						taskProviderResultURIDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				if len(plaintext) > geminitaskresult.MaxProviderResultURIBytes {
					return reversibleSecretStorageError(
						taskProviderResultURIDomain,
						fmt.Sprintf("%d", row.Id),
						fmt.Errorf(
							"plaintext exceeds %d bytes",
							geminitaskresult.MaxProviderResultURIBytes,
						),
					)
				}
				var replacement string
				var changed bool
				if info.Encrypted {
					replacement, changed, err = common.RewrapDataEncryptionValue(
						taskProviderResultURIDomain,
						row.StoredURI,
					)
				} else {
					replacement, err = common.SealDataEncryptionValueRequired(
						taskProviderResultURIDomain,
						plaintext,
					)
					changed = replacement != row.StoredURI
				}
				if err != nil {
					return reversibleSecretStorageError(
						taskProviderResultURIDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				if !changed {
					continue
				}

				query := tx.Session(&gorm.Session{Logger: logger.Discard}).
					Model(&Task{}).
					Where("id = ?", row.Id)
				if info.Encrypted {
					query = query.Where(
						"provider_result_uri = ?",
						row.StoredURI,
					)
				} else {
					query = query.Where(
						"provider_result_uri IS NOT NULL AND "+
							"provider_result_uri <> '' AND "+
							"provider_result_uri NOT LIKE ?",
						"naenc:%",
					)
				}
				result := query.UpdateColumn(
					"provider_result_uri",
					replacement,
				)
				if result.Error != nil {
					return fmt.Errorf(
						"update reversible secret domain %s row %d: %w",
						taskProviderResultURIDomain,
						row.Id,
						sanitizeDBError(result.Error),
					)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf(
						"update reversible secret domain %s row %d: concurrent modification detected",
						taskProviderResultURIDomain,
						row.Id,
					)
				}
				if info.Encrypted {
					rewrapped++
				} else {
					migrated++
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
	}

	common.SysLog(fmt.Sprintf(
		"reversible secret migration completed: domain=%s migrated=%d rewrapped=%d",
		taskProviderResultURIDomain,
		migrated,
		rewrapped,
	))
	return validateTaskProviderResultURISecrets()
}

func ValidateReversibleSecretStorage() error {
	if err := validateCustomOAuthProviderSecrets(); err != nil {
		return err
	}
	if err := validateOptionSecrets(); err != nil {
		return err
	}
	if err := validateTwoFASecrets(); err != nil {
		return err
	}
	if err := validateChannelSecrets(); err != nil {
		return err
	}
	if err := validateUserSettingSecrets(); err != nil {
		return err
	}
	return validateTaskProviderResultURISecrets()
}

// ValidateServingStorageOnStartup applies the fail-closed storage gates that
// every serving node needs after the database connection is initialized.
// Masters already validate Gemini task results after their ordered migration;
// replicas do not migrate, so they must perform that scan here.
func ValidateServingStorageOnStartup() error {
	if err := ValidateReversibleSecretStorage(); err != nil {
		return err
	}
	if common.IsMasterNode {
		return nil
	}
	return ValidateGeminiTaskResultPrivacy()
}

func validateTaskProviderResultURISecrets() error {
	var lastID int64
	keyUsage := make(map[string]int)
	for {
		var rows []storedTaskProviderResultURI
		if err := DB.Model(&Task{}).
			Select("id, provider_result_uri AS stored_uri").
			Where(
				"id > ? AND provider_result_uri IS NOT NULL AND provider_result_uri <> ''",
				lastID,
			).
			Order("id ASC").
			Limit(reversibleSecretBatchSize).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf(
				"scan reversible secret domain %s: %w",
				taskProviderResultURIDomain,
				err,
			)
		}
		if len(rows) == 0 {
			logReversibleSecretKeyUsage(taskProviderResultURIDomain, keyUsage)
			return nil
		}

		for _, row := range rows {
			lastID = row.Id
			if !common.IsDataEncryptionEnvelope(row.StoredURI) {
				return reversibleSecretStorageError(
					taskProviderResultURIDomain,
					fmt.Sprintf("%d", row.Id),
					fmt.Errorf("stored value is not an encrypted envelope"),
				)
			}
			plaintext, info, err := common.OpenDataEncryptionValue(
				taskProviderResultURIDomain,
				row.StoredURI,
			)
			if err != nil {
				return reversibleSecretStorageError(
					taskProviderResultURIDomain,
					fmt.Sprintf("%d", row.Id),
					err,
				)
			}
			if len(plaintext) > geminitaskresult.MaxProviderResultURIBytes {
				return reversibleSecretStorageError(
					taskProviderResultURIDomain,
					fmt.Sprintf("%d", row.Id),
					fmt.Errorf(
						"plaintext exceeds %d bytes",
						geminitaskresult.MaxProviderResultURIBytes,
					),
				)
			}
			if !info.Encrypted {
				return reversibleSecretStorageError(
					taskProviderResultURIDomain,
					fmt.Sprintf("%d", row.Id),
					fmt.Errorf("stored value is not an encrypted envelope"),
				)
			}
			keyUsage[info.KeyID]++
		}
	}
}

func validateCustomOAuthProviderSecrets() error {
	lastID := 0
	keyUsage := make(map[string]int)
	for {
		var rows []storedCustomOAuthSecret
		if err := DB.Table("custom_oauth_providers").
			Select("id", "client_secret").
			Where("id > ? AND client_secret <> ''", lastID).
			Order("id ASC").
			Limit(reversibleSecretBatchSize).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("scan reversible secret domain %s: %w", customOAuthClientSecretDomain, err)
		}
		if len(rows) == 0 {
			logReversibleSecretKeyUsage(customOAuthClientSecretDomain, keyUsage)
			return nil
		}

		for _, row := range rows {
			lastID = row.Id
			if !common.IsDataEncryptionEnvelope(row.ClientSecret) {
				if !common.DataEncryptionEnabled() {
					continue
				}
				return reversibleSecretStorageError(
					customOAuthClientSecretDomain,
					fmt.Sprintf("%d", row.Id),
					fmt.Errorf("legacy plaintext remains while encryption enforcement is enabled"),
				)
			}
			_, info, err := common.OpenDataEncryptionValue(
				customOAuthClientSecretDomain,
				row.ClientSecret,
			)
			if err != nil {
				return reversibleSecretStorageError(
					customOAuthClientSecretDomain,
					fmt.Sprintf("%d", row.Id),
					err,
				)
			}
			keyUsage[info.KeyID]++
		}
	}
}

func MigrateOptionSecrets() error {
	if !common.DataEncryptionEnabled() {
		return validateOptionSecrets()
	}

	var migrated int
	var rewrapped int
	lastKey := ""
	for {
		var rows []storedOptionSecret
		if err := DB.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx.Table("options")).
				Select(commonKeyCol+" AS key, value").
				Where(commonKeyCol+" > ?", lastKey).
				Order(commonKeyCol + " ASC").
				Limit(reversibleSecretBatchSize).
				Scan(&rows).Error; err != nil {
				return fmt.Errorf("scan reversible secret domain options: %w", err)
			}

			for _, row := range rows {
				lastKey = row.Key
				protected, err := classifyProtectedOptionKey(row.Key)
				if err != nil {
					return reversibleSecretStorageError("options", row.Key, err)
				}
				if !protected {
					if common.IsDataEncryptionEnvelope(row.Value) {
						return reversibleSecretStorageError(
							"options",
							row.Key,
							fmt.Errorf("unprotected option contains an encrypted envelope"),
						)
					}
					continue
				}
				if row.Value == "" {
					continue
				}

				domain := optionSecretDomain(row.Key)
				_, info, err := common.OpenLegacyDataEncryptionValueForMigration(
					domain,
					row.Value,
				)
				if err != nil {
					return reversibleSecretStorageError(domain, row.Key, err)
				}
				replacement, changed, err := common.RewrapDataEncryptionValue(domain, row.Value)
				if err != nil {
					return reversibleSecretStorageError(domain, row.Key, err)
				}
				if !changed {
					continue
				}

				query := tx.Table("options").Where(commonKeyCol+" = ?", row.Key)
				if info.Encrypted {
					query = query.Where("value = ?", row.Value)
				} else {
					query = query.Where("value NOT LIKE ?", "naenc:%")
				}
				result := query.Update("value", replacement)
				if result.Error != nil {
					return fmt.Errorf(
						"update reversible secret domain %s row %s: %w",
						domain,
						row.Key,
						result.Error,
					)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf(
						"update reversible secret domain %s row %s: concurrent modification detected",
						domain,
						row.Key,
					)
				}
				if info.Encrypted {
					rewrapped++
				} else {
					migrated++
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
	}

	common.SysLog(fmt.Sprintf(
		"reversible secret migration completed: domain=options migrated=%d rewrapped=%d",
		migrated,
		rewrapped,
	))
	return validateOptionSecrets()
}

func validateOptionSecrets() error {
	lastKey := ""
	keyUsage := make(map[string]int)
	for {
		var rows []storedOptionSecret
		if err := DB.Table("options").
			Select(commonKeyCol+" AS key, value").
			Where(commonKeyCol+" > ?", lastKey).
			Order(commonKeyCol + " ASC").
			Limit(reversibleSecretBatchSize).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("scan reversible secret domain options: %w", err)
		}
		if len(rows) == 0 {
			logReversibleSecretKeyUsage("options", keyUsage)
			return nil
		}

		for _, row := range rows {
			lastKey = row.Key
			protected, err := classifyProtectedOptionKey(row.Key)
			if err != nil {
				return reversibleSecretStorageError("options", row.Key, err)
			}
			if !protected {
				if common.IsDataEncryptionEnvelope(row.Value) {
					return reversibleSecretStorageError(
						"options",
						row.Key,
						fmt.Errorf("unprotected option contains an encrypted envelope"),
					)
				}
				continue
			}
			if row.Value == "" {
				continue
			}
			if !common.IsDataEncryptionEnvelope(row.Value) {
				if !common.DataEncryptionEnabled() {
					continue
				}
				return reversibleSecretStorageError(
					optionSecretDomain(row.Key),
					row.Key,
					fmt.Errorf("legacy plaintext remains while encryption enforcement is enabled"),
				)
			}
			_, info, err := common.OpenDataEncryptionValue(
				optionSecretDomain(row.Key),
				row.Value,
			)
			if err != nil {
				return reversibleSecretStorageError(
					optionSecretDomain(row.Key),
					row.Key,
					err,
				)
			}
			keyUsage[info.KeyID]++
		}
	}
}

func MigrateTwoFASecrets() error {
	if !common.DataEncryptionEnabled() {
		return validateTwoFASecrets()
	}

	var migrated int
	var rewrapped int
	lastID := 0
	for {
		var rows []storedTwoFASecret
		if err := DB.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx.Unscoped().Table("two_fas")).
				Select("id", "secret").
				Where("id > ? AND secret <> ''", lastID).
				Order("id ASC").
				Limit(reversibleSecretBatchSize).
				Scan(&rows).Error; err != nil {
				return fmt.Errorf("scan reversible secret domain %s: %w", twoFASecretDomain, err)
			}

			for _, row := range rows {
				lastID = row.Id
				_, info, err := common.OpenLegacyDataEncryptionValueForMigration(
					twoFASecretDomain,
					row.Secret,
				)
				if err != nil {
					return reversibleSecretStorageError(
						twoFASecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				replacement, changed, err := common.RewrapDataEncryptionValue(
					twoFASecretDomain,
					row.Secret,
				)
				if err != nil {
					return reversibleSecretStorageError(
						twoFASecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				if !changed {
					continue
				}

				query := tx.Unscoped().Table("two_fas").Where("id = ?", row.Id)
				if info.Encrypted {
					query = query.Where(
						"secret = ?",
						row.Secret,
					)
				} else {
					query = query.Where("secret NOT LIKE ?", "naenc:%")
				}
				result := query.Update("secret", replacement)
				if result.Error != nil {
					return fmt.Errorf(
						"update reversible secret domain %s row %d: %w",
						twoFASecretDomain,
						row.Id,
						result.Error,
					)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf(
						"update reversible secret domain %s row %d: concurrent modification detected",
						twoFASecretDomain,
						row.Id,
					)
				}
				if info.Encrypted {
					rewrapped++
				} else {
					migrated++
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
	}

	common.SysLog(fmt.Sprintf(
		"reversible secret migration completed: domain=%s migrated=%d rewrapped=%d",
		twoFASecretDomain,
		migrated,
		rewrapped,
	))
	return validateTwoFASecrets()
}

func validateTwoFASecrets() error {
	lastID := 0
	keyUsage := make(map[string]int)
	for {
		var rows []storedTwoFASecret
		if err := DB.Unscoped().Table("two_fas").
			Select("id", "secret").
			Where("id > ? AND secret <> ''", lastID).
			Order("id ASC").
			Limit(reversibleSecretBatchSize).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("scan reversible secret domain %s: %w", twoFASecretDomain, err)
		}
		if len(rows) == 0 {
			logReversibleSecretKeyUsage(twoFASecretDomain, keyUsage)
			return nil
		}

		for _, row := range rows {
			lastID = row.Id
			if !common.IsDataEncryptionEnvelope(row.Secret) {
				if !common.DataEncryptionEnabled() {
					continue
				}
				return reversibleSecretStorageError(
					twoFASecretDomain,
					fmt.Sprintf("%d", row.Id),
					fmt.Errorf("legacy plaintext remains while encryption enforcement is enabled"),
				)
			}
			_, info, err := common.OpenDataEncryptionValue(
				twoFASecretDomain,
				row.Secret,
			)
			if err != nil {
				return reversibleSecretStorageError(
					twoFASecretDomain,
					fmt.Sprintf("%d", row.Id),
					err,
				)
			}
			keyUsage[info.KeyID]++
		}
	}
}

func MigrateChannelSecrets() error {
	if !common.DataEncryptionEnabled() {
		return validateChannelSecrets()
	}

	var migrated int
	var rewrapped int
	lastID := 0
	for {
		var rows []storedChannelSecret
		if err := DB.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx.Table("channels")).
				Select("id, "+commonKeyCol+" AS stored_key").
				Where("id > ? AND "+commonKeyCol+" <> ''", lastID).
				Order("id ASC").
				Limit(reversibleSecretBatchSize).
				Scan(&rows).Error; err != nil {
				return fmt.Errorf("scan reversible secret domain %s: %w", channelKeySecretDomain, err)
			}

			for _, row := range rows {
				lastID = row.Id
				_, info, err := common.OpenLegacyDataEncryptionValueForMigration(
					channelKeySecretDomain,
					row.StoredKey,
				)
				if err != nil {
					return reversibleSecretStorageError(
						channelKeySecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				replacement, changed, err := common.RewrapDataEncryptionValue(
					channelKeySecretDomain,
					row.StoredKey,
				)
				if err != nil {
					return reversibleSecretStorageError(
						channelKeySecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				if !changed {
					continue
				}

				query := tx.Table("channels").Where("id = ?", row.Id)
				if info.Encrypted {
					query = query.Where(
						commonKeyCol+" = ?",
						row.StoredKey,
					)
				} else {
					query = query.Where(commonKeyCol+" NOT LIKE ?", "naenc:%")
				}
				result := query.Update("key", replacement)
				if result.Error != nil {
					return fmt.Errorf(
						"update reversible secret domain %s row %d: %w",
						channelKeySecretDomain,
						row.Id,
						result.Error,
					)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf(
						"update reversible secret domain %s row %d: concurrent modification detected",
						channelKeySecretDomain,
						row.Id,
					)
				}
				if info.Encrypted {
					rewrapped++
				} else {
					migrated++
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
	}

	common.SysLog(fmt.Sprintf(
		"reversible secret migration completed: domain=%s migrated=%d rewrapped=%d",
		channelKeySecretDomain,
		migrated,
		rewrapped,
	))
	return validateChannelSecrets()
}

func validateChannelSecrets() error {
	lastID := 0
	keyUsage := make(map[string]int)
	for {
		var rows []storedChannelSecret
		if err := DB.Table("channels").
			Select("id, "+commonKeyCol+" AS stored_key").
			Where("id > ? AND "+commonKeyCol+" <> ''", lastID).
			Order("id ASC").
			Limit(reversibleSecretBatchSize).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("scan reversible secret domain %s: %w", channelKeySecretDomain, err)
		}
		if len(rows) == 0 {
			logReversibleSecretKeyUsage(channelKeySecretDomain, keyUsage)
			return nil
		}

		for _, row := range rows {
			lastID = row.Id
			if !common.IsDataEncryptionEnvelope(row.StoredKey) {
				if !common.DataEncryptionEnabled() {
					continue
				}
				return reversibleSecretStorageError(
					channelKeySecretDomain,
					fmt.Sprintf("%d", row.Id),
					fmt.Errorf("legacy plaintext remains while encryption enforcement is enabled"),
				)
			}
			_, info, err := common.OpenDataEncryptionValue(
				channelKeySecretDomain,
				row.StoredKey,
			)
			if err != nil {
				return reversibleSecretStorageError(
					channelKeySecretDomain,
					fmt.Sprintf("%d", row.Id),
					err,
				)
			}
			keyUsage[info.KeyID]++
		}
	}
}

func MigrateUserSettingSecrets() error {
	if !common.DataEncryptionEnabled() {
		return validateUserSettingSecrets()
	}

	var migrated int
	var rewrapped int
	lastID := 0
	for {
		var rows []storedUserSettingSecret
		if err := DB.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx.Unscoped().Table("users")).
				Select("id, setting AS stored_setting").
				Where("id > ? AND setting <> ''", lastID).
				Order("id ASC").
				Limit(reversibleSecretBatchSize).
				Scan(&rows).Error; err != nil {
				return fmt.Errorf("scan reversible secret domain %s: %w", userSettingSecretDomain, err)
			}

			for _, row := range rows {
				lastID = row.Id
				plaintext, info, err := common.OpenLegacyDataEncryptionValueForMigration(
					userSettingSecretDomain,
					row.StoredSetting,
				)
				if err != nil {
					return reversibleSecretStorageError(
						userSettingSecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				protected, err := inspectUserSettingValue(plaintext)
				if err != nil {
					return reversibleSecretStorageError(
						userSettingSecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				if !info.Encrypted && !protected {
					continue
				}

				replacement, changed, err := common.RewrapDataEncryptionValue(
					userSettingSecretDomain,
					row.StoredSetting,
				)
				if err != nil {
					return reversibleSecretStorageError(
						userSettingSecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				if !changed {
					continue
				}

				query := tx.Unscoped().Table("users").Where("id = ?", row.Id)
				if info.Encrypted {
					query = query.Where(
						"setting = ?",
						row.StoredSetting,
					)
				} else {
					query = query.Where("setting NOT LIKE ?", "naenc:%")
				}
				result := query.Update("setting", replacement)
				if result.Error != nil {
					return fmt.Errorf(
						"update reversible secret domain %s row %d: %w",
						userSettingSecretDomain,
						row.Id,
						result.Error,
					)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf(
						"update reversible secret domain %s row %d: concurrent modification detected",
						userSettingSecretDomain,
						row.Id,
					)
				}
				if info.Encrypted {
					rewrapped++
				} else {
					migrated++
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
	}

	common.SysLog(fmt.Sprintf(
		"reversible secret migration completed: domain=%s migrated=%d rewrapped=%d",
		userSettingSecretDomain,
		migrated,
		rewrapped,
	))
	return validateUserSettingSecrets()
}

func validateUserSettingSecrets() error {
	lastID := 0
	keyUsage := make(map[string]int)
	for {
		var rows []storedUserSettingSecret
		if err := DB.Unscoped().Table("users").
			Select("id, setting AS stored_setting").
			Where("id > ? AND setting <> ''", lastID).
			Order("id ASC").
			Limit(reversibleSecretBatchSize).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("scan reversible secret domain %s: %w", userSettingSecretDomain, err)
		}
		if len(rows) == 0 {
			logReversibleSecretKeyUsage(userSettingSecretDomain, keyUsage)
			return nil
		}

		for _, row := range rows {
			lastID = row.Id
			if common.IsDataEncryptionEnvelope(row.StoredSetting) {
				plaintext, info, err := common.OpenDataEncryptionValue(
					userSettingSecretDomain,
					row.StoredSetting,
				)
				if err != nil {
					return reversibleSecretStorageError(
						userSettingSecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				if _, err := inspectUserSettingValue(plaintext); err != nil {
					return reversibleSecretStorageError(
						userSettingSecretDomain,
						fmt.Sprintf("%d", row.Id),
						err,
					)
				}
				keyUsage[info.KeyID]++
				continue
			}

			protected, err := inspectUserSettingValue(row.StoredSetting)
			if err != nil {
				return reversibleSecretStorageError(
					userSettingSecretDomain,
					fmt.Sprintf("%d", row.Id),
					err,
				)
			}
			if protected && common.DataEncryptionEnabled() {
				return reversibleSecretStorageError(
					userSettingSecretDomain,
					fmt.Sprintf("%d", row.Id),
					fmt.Errorf("legacy plaintext remains while encryption enforcement is enabled"),
				)
			}
		}
	}
}

func logReversibleSecretKeyUsage(domain string, usage map[string]int) {
	if len(usage) == 0 {
		common.SysLog(fmt.Sprintf(
			"reversible secret key usage: domain=%s key_usage=none",
			domain,
		))
		return
	}
	keyIDs := make([]string, 0, len(usage))
	for keyID := range usage {
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	counts := make([]string, 0, len(keyIDs))
	for _, keyID := range keyIDs {
		counts = append(counts, fmt.Sprintf("%s=%d", keyID, usage[keyID]))
	}
	common.SysLog(fmt.Sprintf(
		"reversible secret key usage: domain=%s key_usage=%s",
		domain,
		strings.Join(counts, ","),
	))
}

func reversibleSecretStorageError(domain string, row string, cause error) error {
	detail := "protected value validation failed"
	if cause != nil {
		detail = strings.TrimSpace(cause.Error())
	}
	return fmt.Errorf(
		"reversible secret storage error: domain=%s row=%s: %s; configure "+
			"DATA_ENCRYPTION_KEYS and DATA_ENCRYPTION_ACTIVE_KEY_ID, or set "+
			"DATA_ENCRYPTION_ENABLE=false before the staged all-nodes upgrade",
		domain,
		row,
		detail,
	)
}
