package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareTwoFASecretTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&TwoFA{}, &CustomOAuthProvider{}, &Option{}))
	require.NoError(t, DB.Exec("DELETE FROM two_fas").Error)
	require.NoError(t, DB.Exec("DELETE FROM custom_oauth_providers").Error)
	require.NoError(t, DB.Exec("DELETE FROM options").Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM two_fas")
		DB.Exec("DELETE FROM custom_oauth_providers")
		DB.Exec("DELETE FROM options")
	})
}

func insertRawTwoFA(
	t *testing.T,
	id int,
	userID int,
	storedSecret string,
	deletedAt *time.Time,
) {
	t.Helper()
	require.NoError(t, DB.Table("two_fas").Create(map[string]any{
		"id":              id,
		"user_id":         userID,
		"secret":          storedSecret,
		"is_enabled":      false,
		"failed_attempts": 0,
		"created_at":      time.Now().UTC(),
		"updated_at":      time.Now().UTC(),
		"deleted_at":      deletedAt,
	}).Error)
}

func rawTwoFASecret(t *testing.T, id int) string {
	t.Helper()
	var stored string
	require.NoError(t, DB.Table("two_fas").
		Select("secret").
		Where("id = ?", id).
		Scan(&stored).Error)
	return stored
}

func TestTwoFAEncryptsSecretAtRestAndRoundTrips(t *testing.T) {
	prepareTwoFASecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)

	factor := &TwoFA{UserId: 5101, Secret: "totp-seed-value"}
	require.NoError(t, DB.Create(factor).Error)

	stored := rawTwoFASecret(t, factor.Id)
	assert.True(t, common.IsDataEncryptionEnvelope(stored))
	assert.NotContains(t, stored, factor.Secret)

	var loaded TwoFA
	require.NoError(t, DB.First(&loaded, factor.Id).Error)
	assert.Equal(t, "totp-seed-value", loaded.Secret)
}

func TestMigrateTwoFASecretsIncludesSoftDeletedRowsAndNeverBindsPlaintext(
	t *testing.T,
) {
	prepareTwoFASecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const (
		activeSecret  = "legacy-active-totp-seed"
		deletedSecret = "legacy-deleted-totp-seed"
	)
	deletedAt := time.Now().UTC()
	insertRawTwoFA(t, 5201, 5201, activeSecret, nil)
	insertRawTwoFA(t, 5202, 5202, deletedSecret, &deletedAt)

	callbackName := fmt.Sprintf("test:no-twofa-plaintext-bind:%s", t.Name())
	var plaintextBound bool
	require.NoError(t, DB.Callback().Update().Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			for _, variable := range tx.Statement.Vars {
				value, ok := variable.(string)
				if ok && (value == activeSecret || value == deletedSecret) {
					plaintextBound = true
				}
			}
		}))
	t.Cleanup(func() {
		DB.Callback().Update().Remove(callbackName)
	})

	require.NoError(t, MigrateTwoFASecrets())
	firstActive := rawTwoFASecret(t, 5201)
	firstDeleted := rawTwoFASecret(t, 5202)
	assert.True(t, common.IsDataEncryptionEnvelope(firstActive))
	assert.True(t, common.IsDataEncryptionEnvelope(firstDeleted))
	assert.False(t, plaintextBound)

	require.NoError(t, MigrateTwoFASecrets())
	assert.Equal(t, firstActive, rawTwoFASecret(t, 5201))
	assert.Equal(t, firstDeleted, rawTwoFASecret(t, 5202))

	var active TwoFA
	require.NoError(t, DB.First(&active, 5201).Error)
	assert.Equal(t, activeSecret, active.Secret)
	var deleted TwoFA
	require.NoError(t, DB.Unscoped().First(&deleted, 5202).Error)
	assert.Equal(t, deletedSecret, deleted.Secret)
}

func TestValidateReversibleSecretStorageRejectsCorruptTwoFAEnvelopeWithoutLeak(
	t *testing.T,
) {
	prepareTwoFASecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const corrupt = "naenc:v1:k1:not-a-valid-wrap:not-a-valid-payload"
	insertRawTwoFA(t, 5301, 5301, corrupt, nil)

	err := ValidateReversibleSecretStorage()
	require.Error(t, err)
	assert.Contains(t, err.Error(), twoFASecretDomain)
	assert.NotContains(t, err.Error(), corrupt)
}

func TestTwoFAProtectedWriteFailsWithoutKeyring(t *testing.T) {
	prepareTwoFASecretTest(t)
	configureModelDataEncryption(t, "", "", "true")

	err := DB.Create(&TwoFA{
		UserId: 5401,
		Secret: "must-not-be-plaintext",
	}).Error
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "must-not-be-plaintext")
	assert.Empty(t, rawTwoFASecret(t, 5401))
}
