package model

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"gorm.io/gorm"
)

const geminiTaskResultPrivacyBatchSize = 100

var geminiTaskResultPrivacyPlatforms = func() []string {
	platforms := make([]string, 0, 1+(1<<len("gemini")))
	platforms = append(
		platforms,
		strconv.Itoa(constant.ChannelTypeGemini),
	)
	for mask := 0; mask < 1<<len("gemini"); mask++ {
		legacy := []byte("gemini")
		for index := range legacy {
			if mask&(1<<index) != 0 {
				legacy[index] -= 'a' - 'A'
			}
		}
		platforms = append(platforms, string(legacy))
	}
	return platforms
}()

func geminiTaskResultPrivacyRows(db *gorm.DB) *gorm.DB {
	return db.Where("platform IN ?", geminiTaskResultPrivacyPlatforms)
}

// geminiTaskResultPublicDataEqual keeps strict physical-byte canonicalization
// on SQLite and PostgreSQL. MySQL's native JSON column normalizes whitespace
// and object-key order before values are read, so its comparison must be
// semantic. The sanitizer's output remains the exact public allowlist, making
// extra stored fields unequal. MySQL also removes duplicate object members at
// storage time; the strict paths intentionally keep byte comparison so a
// duplicate allowed key cannot conceal retained raw bytes there.
func geminiTaskResultPublicDataEqual(
	databaseType common.DatabaseType,
	stored []byte,
	canonical []byte,
) bool {
	if databaseType != common.DatabaseTypeMySQL {
		return bytes.Equal(stored, canonical)
	}

	var storedValue any
	if err := common.Unmarshal(stored, &storedValue); err != nil {
		return false
	}
	var canonicalValue any
	if err := common.Unmarshal(canonical, &canonicalValue); err != nil {
		return false
	}
	return reflect.DeepEqual(storedValue, canonicalValue)
}

// MigrateGeminiTaskResultPrivacy replaces legacy Gemini result payloads with
// the canonical public projection and moves the usable provider URI into its
// dedicated encrypted column. Each committed batch advances independently, so
// an interrupted run can safely restart from the beginning without ciphertext
// churn or duplicate writes.
func MigrateGeminiTaskResultPrivacy() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}

	var (
		lastID             int64
		scanned            int64
		updated            int64
		privateURICaptured int64
		privateURICleared  int64
	)
	for {
		var (
			tasks                []Task
			batchLastID          = lastID
			batchUpdated         int64
			batchPrivateCaptured int64
			batchPrivateCleared  int64
		)
		err := DB.Transaction(func(tx *gorm.DB) error {
			query := lockForUpdate(geminiTaskResultPrivacyRows(tx.Model(&Task{})))
			if err := query.
				Where("id > ?", lastID).
				Order("id ASC").
				Limit(geminiTaskResultPrivacyBatchSize).
				Find(&tasks).Error; err != nil {
				return fmt.Errorf(
					"scan Gemini task result privacy rows: %w",
					sanitizeDBError(err),
				)
			}

			for index := range tasks {
				task := &tasks[index]
				batchLastID = task.ID
				if !task.IsGeminiTask() {
					continue
				}

				resolvedCredential := ""
				capturePrivateURI := false
				if task.ChannelId != 0 {
					var channel Channel
					channelErr := tx.First(&channel, task.ChannelId).Error
					switch {
					case channelErr == nil:
						key, resolveErr := ResolveTaskChannelKey(&channel, task)
						if resolveErr == nil {
							resolvedCredential = key
							capturePrivateURI = true
						} else if !errors.Is(
							resolveErr,
							ErrTaskChannelCredentialUnavailable,
						) {
							return fmt.Errorf(
								"resolve Gemini task result credential for task %d",
								task.ID,
							)
						}
					case errors.Is(channelErr, gorm.ErrRecordNotFound):
						// A removed channel cannot yield the exact historical
						// credential. Public remediation still proceeds.
					default:
						return fmt.Errorf(
							"load Gemini task result channel for task %d",
							task.ID,
						)
					}
				}

				sanitized, sanitizeErr := geminitaskresult.Sanitize(
					task.Data,
					geminitaskresult.Options{
						Phase:              geminitaskresult.PhaseBackfill,
						PublicTaskID:       task.TaskID,
						ResolvedCredential: resolvedCredential,
						CapturePrivateURI:  capturePrivateURI,
					},
				)
				if sanitizeErr != nil && len(sanitized.PublicData) == 0 {
					return fmt.Errorf(
						"sanitize Gemini task result for task %d: safe projection unavailable",
						task.ID,
					)
				}

				originalData := append([]byte(nil), task.Data...)
				originalPrivateData := task.PrivateData
				originalFailReason := task.FailReason
				originalProviderResultURI := ""
				originalProviderResultURISet :=
					task.EncryptedProviderResultURI != nil
				if originalProviderResultURISet {
					originalProviderResultURI =
						*task.EncryptedProviderResultURI
				}

				task.Data = append(task.Data[:0], sanitized.PublicData...)
				switch {
				case sanitized.HadProviderURI &&
					sanitized.ProviderURI != "":
					changed, err := task.SetProviderResultURI(
						sanitized.ProviderURI,
					)
					if err != nil {
						return fmt.Errorf(
							"seal Gemini task result for task %d: %w",
							task.ID,
							err,
						)
					}
					if changed {
						batchPrivateCaptured++
					}
				case sanitized.HadProviderURI:
					if task.EncryptedProviderResultURI != nil {
						batchPrivateCleared++
					}
					task.ClearProviderResultURI()
				case task.EncryptedProviderResultURI != nil &&
					*task.EncryptedProviderResultURI != "":
					if _, err := task.OpenProviderResultURI(); err != nil {
						return fmt.Errorf(
							"validate retained Gemini task result for task %d: %w",
							task.ID,
							err,
						)
					}
				}

				if sanitized.Status == string(TaskStatusSuccess) {
					task.PrivateData.ResultURL =
						strings.TrimRight(
							system_setting.ServerAddress,
							"/",
						) + geminitaskresult.ProxyPath(task.TaskID)
				} else {
					task.PrivateData.ResultURL = ""
				}
				if sanitized.Failed {
					task.FailReason = "upstream task failed"
				} else {
					task.FailReason = ""
				}

				currentProviderResultURI := ""
				currentProviderResultURISet :=
					task.EncryptedProviderResultURI != nil
				if currentProviderResultURISet {
					currentProviderResultURI =
						*task.EncryptedProviderResultURI
				}
				if geminiTaskResultPublicDataEqual(
					common.MainDatabaseType(),
					originalData,
					task.Data,
				) &&
					reflect.DeepEqual(
						originalPrivateData,
						task.PrivateData,
					) &&
					originalFailReason == task.FailReason &&
					originalProviderResultURISet ==
						currentProviderResultURISet &&
					originalProviderResultURI ==
						currentProviderResultURI {
					continue
				}

				result := tx.Model(&Task{}).
					Where("id = ?", task.ID).
					UpdateColumns(map[string]any{
						"data":                task.Data,
						"private_data":        task.PrivateData,
						"fail_reason":         task.FailReason,
						"provider_result_uri": task.EncryptedProviderResultURI,
					})
				if result.Error != nil {
					return fmt.Errorf(
						"update Gemini task result privacy for task %d: %w",
						task.ID,
						sanitizeDBError(result.Error),
					)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf(
						"update Gemini task result privacy for task %d: concurrent modification detected",
						task.ID,
					)
				}
				batchUpdated++
			}
			return nil
		})
		if err != nil {
			return sanitizeDBError(err)
		}
		if len(tasks) == 0 {
			break
		}

		lastID = batchLastID
		scanned += int64(len(tasks))
		updated += batchUpdated
		privateURICaptured += batchPrivateCaptured
		privateURICleared += batchPrivateCleared
	}

	common.SysLog(fmt.Sprintf(
		"Gemini task result privacy migration completed: scanned=%d updated=%d private_captured=%d private_cleared=%d",
		scanned,
		updated,
		privateURICaptured,
		privateURICleared,
	))
	return nil
}

// ValidateGeminiTaskResultPrivacy verifies that every stored Gemini public
// result is already canonical and that every non-empty private result can be
// opened through the required encrypted domain.
func ValidateGeminiTaskResultPrivacy() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}

	var (
		lastID    int64
		validated int64
	)
	for {
		var tasks []Task
		if err := geminiTaskResultPrivacyRows(DB.Model(&Task{})).
			Where("id > ?", lastID).
			Order("id ASC").
			Limit(geminiTaskResultPrivacyBatchSize).
			Find(&tasks).Error; err != nil {
			return fmt.Errorf(
				"scan Gemini task result privacy validation rows: %w",
				sanitizeDBError(err),
			)
		}
		if len(tasks) == 0 {
			common.SysLog(fmt.Sprintf(
				"Gemini task result privacy validation completed: validated=%d",
				validated,
			))
			return nil
		}

		for index := range tasks {
			task := &tasks[index]
			lastID = task.ID
			if !task.IsGeminiTask() {
				continue
			}
			sanitized, err := geminitaskresult.Sanitize(
				task.Data,
				geminitaskresult.Options{
					Phase:             geminitaskresult.PhasePublicRead,
					PublicTaskID:      task.TaskID,
					CapturePrivateURI: false,
				},
			)
			if err != nil || !geminiTaskResultPublicDataEqual(
				common.MainDatabaseType(),
				task.Data,
				sanitized.PublicData,
			) {
				return fmt.Errorf(
					"Gemini task result privacy validation failed for task %d: public data is not canonical",
					task.ID,
				)
			}
			if task.EncryptedProviderResultURI != nil &&
				*task.EncryptedProviderResultURI != "" {
				if _, err := task.OpenProviderResultURI(); err != nil {
					return fmt.Errorf(
						"Gemini task result privacy validation failed for task %d: private result is unavailable: %w",
						task.ID,
						err,
					)
				}
			}
			validated++
		}
	}
}
