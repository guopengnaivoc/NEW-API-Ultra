package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const taskCredentialMigrationBatchSize = 200

func legacyTaskCredentialPredicate() (string, error) {
	switch common.MainDatabaseType() {
	case common.DatabaseTypeSQLite:
		return `CASE WHEN json_valid(private_data) THEN ` +
			`COALESCE(json_extract(private_data, '$.key'), '') ` +
			`ELSE '' END <> ''`, nil
	case common.DatabaseTypeMySQL:
		return `CASE WHEN JSON_VALID(private_data) THEN ` +
			`COALESCE(JSON_UNQUOTE(JSON_EXTRACT(private_data, '$.key')), '') ` +
			`ELSE '' END <> ''`, nil
	case common.DatabaseTypePostgreSQL:
		// PostgreSQL 9.6 has no non-throwing JSON validity predicate. This cast
		// relies on TaskPrivateData.Value writing only valid JSON or NULL.
		return `COALESCE(private_data::json ->> 'key', '') <> ''`, nil
	default:
		return "", fmt.Errorf(
			"task credential migration does not support database type %q",
			common.MainDatabaseType(),
		)
	}
}

// MigrateTaskPrivateCredentials replaces provider credentials copied into
// legacy task JSON with non-reversible references. Re-running the migration is
// safe because normalized rows no longer contain a key member.
func MigrateTaskPrivateCredentials() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	predicate, err := legacyTaskCredentialPredicate()
	if err != nil {
		return err
	}

	var (
		lastID   int64
		migrated int64
	)
	for {
		var tasks []Task
		if err := DB.
			Select("id", "private_data").
			Where(predicate).
			Where("id > ?", lastID).
			Order("id").
			Limit(taskCredentialMigrationBatchSize).
			Find(&tasks).Error; err != nil {
			return fmt.Errorf("find legacy task credentials: %w", err)
		}
		if len(tasks) == 0 {
			break
		}

		for index := range tasks {
			task := &tasks[index]
			lastID = task.ID
			if strings.TrimSpace(task.PrivateData.Key) == "" {
				continue
			}
			if task.PrivateData.ChannelKeyFingerprint == "" {
				task.PrivateData.ChannelKeyFingerprint =
					taskChannelKeyFingerprint(task.PrivateData.Key)
			}
			task.PrivateData.Key = ""
			result := DB.Model(&Task{}).
				Where("id = ?", task.ID).
				Update("private_data", task.PrivateData)
			if result.Error != nil {
				return fmt.Errorf(
					"sanitize task private data for task %d: %w",
					task.ID,
					sanitizeDBError(result.Error),
				)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf(
					"sanitize task private data for task %d: row changed",
					task.ID,
				)
			}
			migrated++
		}
	}

	if migrated > 0 {
		common.SysLog(fmt.Sprintf(
			"sanitized legacy provider credentials from %d task rows",
			migrated,
		))
	}
	return nil
}
