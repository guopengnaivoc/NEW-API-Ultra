package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserUpdateTestState(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)

	oldRedisEnabled := common.RedisEnabled
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})
}

func TestUserUpdateDoesNotOverwriteAccountingFields(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:           1,
		Username:     "quota-race-user",
		Password:     "password",
		DisplayName:  "before",
		Status:       common.UserStatusEnabled,
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 3,
	}
	require.NoError(t, DB.Create(&user).Error)

	staleUser, err := GetUserById(user.Id, true)
	require.NoError(t, err)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":         gorm.Expr("quota - ?", 400),
		"used_quota":    gorm.Expr("used_quota + ?", 400),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	staleUser.DisplayName = "after"
	require.NoError(t, staleUser.Update(false))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, "after", got.DisplayName)
	assert.Equal(t, 600, got.Quota)
	assert.Equal(t, 420, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
}

func TestUpdateUserSettingOnlyUpdatesSetting(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:           2,
		Username:     "setting-user",
		Password:     "password",
		Status:       common.UserStatusEnabled,
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 3,
	}
	require.NoError(t, DB.Create(&user).Error)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":         gorm.Expr("quota - ?", 250),
		"used_quota":    gorm.Expr("used_quota + ?", 250),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{Language: "zh"}))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, 750, got.Quota)
	assert.Equal(t, 270, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, "zh", got.GetSetting().Language)
}

func TestEnsureEmailAvailableRejectsExistingEmailCaseInsensitive(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "Taken@Example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := EnsureEmailAvailable(" taken@example.COM ", 0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	user, err := GetUniqueUserByEmail("TAKEN@example.com")
	require.NoError(t, err)
	assert.Equal(t, "existing", user.Username)

	require.NoError(t, EnsureEmailAvailable("taken@example.com", user.Id))
}

func TestBindEmailAdvancesAuthVersionWithMutation(t *testing.T) {
	setupUserUpdateTestState(t)

	user := &User{
		Username:    "email-step-up-user",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "email-step-up-aff",
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, BindEmailToUser(user, " Next@Example.com "))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, "next@example.com", stored.Email)
	assert.Equal(t, int64(2), stored.AuthVersion)
	assert.Equal(t, stored.AuthVersion, user.AuthVersion)
}

func TestRejectedEmailBindDoesNotAdvanceAuthVersion(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username:    "email-owner",
		Password:    "password",
		Email:       "taken@example.com",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "email-owner-aff",
	}).Error)
	user := &User{
		Username:    "email-step-up-user",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "email-step-up-aff",
	}
	require.NoError(t, DB.Create(user).Error)

	err := BindEmailToUser(user, "TAKEN@example.com")
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Empty(t, stored.Email)
	assert.Equal(t, int64(1), stored.AuthVersion)
}

func TestEmailBindWithVerificationCommitsMutationAndConsumption(t *testing.T) {
	setupUserUpdateTestState(t)
	user := &User{
		Username:    "verified-email-bind",
		Password:    "password-hash",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "verified-email-bind-aff",
	}
	require.NoError(t, DB.Create(user).Error)
	token, _, err := CreateEmailVerificationFlow(" Next@Example.COM ")
	require.NoError(t, err)

	require.NoError(t, BindEmailToUserWithVerification(user, " next@example.com ", token))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, "next@example.com", stored.Email)
	assert.Equal(t, int64(2), stored.AuthVersion)
	assert.Equal(t, stored.Email, user.Email)
	assert.Equal(t, stored.AuthVersion, user.AuthVersion)
	err = ValidateEmailVerificationFlow(token, stored.Email)
	assert.ErrorIs(t, err, ErrAuthFlowConsumed)
}

func TestEmailBindWithVerificationAcceptsSameNormalizedEmailWhenUpdateReportsZeroRows(t *testing.T) {
	setupUserUpdateTestState(t)
	user := &User{
		Username:    "verified-email-same-target",
		Password:    "password-hash",
		Email:       "same@example.com",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "verified-email-same-target-aff",
	}
	require.NoError(t, DB.Create(user).Error)
	token, _, err := CreateEmailVerificationFlow(" SAME@EXAMPLE.COM ")
	require.NoError(t, err)

	const callbackName = "test:email-update-reports-zero-rows"
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]interface{})
		if !ok {
			return
		}
		if _, updatesEmail := updates["email"]; updatesEmail {
			tx.Statement.RowsAffected = 0
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(callbackName))
	})

	require.NoError(t, BindEmailToUserWithVerification(user, " same@example.com ", token))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, "same@example.com", stored.Email)
	assert.Equal(t, int64(2), stored.AuthVersion)
	assert.Equal(t, stored.AuthVersion, user.AuthVersion)
	err = ValidateEmailVerificationFlow(token, stored.Email)
	assert.ErrorIs(t, err, ErrAuthFlowConsumed)
}

func TestEmailBindWithVerificationDuplicateRollsBackFlowAndUser(t *testing.T) {
	setupUserUpdateTestState(t)
	require.NoError(t, DB.Create(&User{
		Username:    "verified-email-owner",
		Password:    "password-hash",
		Email:       "taken@example.com",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "verified-email-owner-aff",
	}).Error)
	user := &User{
		Username:    "verified-email-duplicate",
		Password:    "password-hash",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "verified-email-duplicate-aff",
	}
	require.NoError(t, DB.Create(user).Error)
	token, _, err := CreateEmailVerificationFlow("taken@example.com")
	require.NoError(t, err)

	err = BindEmailToUserWithVerification(user, " TAKEN@example.com ", token)
	assert.ErrorIs(t, err, ErrEmailAlreadyTaken)
	require.NoError(t, ValidateEmailVerificationFlow(token, "taken@example.com"))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Empty(t, stored.Email)
	assert.Equal(t, int64(1), stored.AuthVersion)
}

func TestEmailBindWithVerificationWrongTargetChangesNothing(t *testing.T) {
	setupUserUpdateTestState(t)
	user := &User{
		Username:    "verified-email-wrong-target",
		Password:    "password-hash",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "verified-email-wrong-target-aff",
	}
	require.NoError(t, DB.Create(user).Error)
	token, _, err := CreateEmailVerificationFlow("issued@example.com")
	require.NoError(t, err)

	err = BindEmailToUserWithVerification(user, "different@example.com", token)
	assert.ErrorIs(t, err, ErrAuthFlowInvalid)
	require.NoError(t, ValidateEmailVerificationFlow(token, "issued@example.com"))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Empty(t, stored.Email)
	assert.Equal(t, int64(1), stored.AuthVersion)
}

func TestEmailBindWithVerificationReplayCannotBindSecondUser(t *testing.T) {
	setupUserUpdateTestState(t)
	first := &User{
		Username:    "verified-email-replay-first",
		Password:    "password-hash",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "verified-email-replay-first-aff",
	}
	second := &User{
		Username:    "verified-email-replay-second",
		Password:    "password-hash",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "verified-email-replay-second-aff",
	}
	require.NoError(t, DB.Create(first).Error)
	require.NoError(t, DB.Create(second).Error)
	token, _, err := CreateEmailVerificationFlow("replay@example.com")
	require.NoError(t, err)

	require.NoError(t, BindEmailToUserWithVerification(first, "replay@example.com", token))
	err = BindEmailToUserWithVerification(second, "replay@example.com", token)
	assert.ErrorIs(t, err, ErrAuthFlowConsumed)

	var storedSecond User
	require.NoError(t, DB.First(&storedSecond, second.Id).Error)
	assert.Empty(t, storedSecond.Email)
	assert.Equal(t, int64(1), storedSecond.AuthVersion)
}

func TestInsertRejectsDuplicateEmailWithoutUniqueIndex(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "taken@example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	user := &User{
		Username: "oauth-user",
		Email:    "TAKEN@example.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	err := user.Insert(0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	var count int64
	require.NoError(t, DB.Model(&User{}).Where("username = ?", "oauth-user").Count(&count).Error)
	assert.Zero(t, count)
}

func TestInsertKeepsBlankPasswordForPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	user := &User{
		Username: "passwordless-user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	require.NoError(t, user.Insert(0))

	var stored User
	require.NoError(t, DB.Where("username = ?", user.Username).First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestValidateAndFillRejectsPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "passwordless-user",
		Password: "",
		Status:   common.UserStatusEnabled,
	}).Error)

	loginUser := User{
		Username: "passwordless-user",
		Password: "NewPassword123",
	}
	err := loginUser.ValidateAndFill()
	require.ErrorIs(t, err, ErrInvalidCredentials)

	var stored User
	require.NoError(t, DB.Where("username = ?", "passwordless-user").First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestResetUserPasswordByEmailRequiresSingleActiveMatch(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "duplicate-1",
		Password: "old-1",
		Email:    "legacy@example.com",
		AffCode:  "dupe1",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&User{
		Username: "duplicate-2",
		Password: "old-2",
		Email:    "LEGACY@example.com",
		AffCode:  "dupe2",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := ResetUserPasswordByEmail("legacy@example.com", "NewPassword123")
	require.ErrorIs(t, err, ErrEmailAmbiguous)

	var duplicates []User
	require.NoError(t, DB.Where("LOWER(email) = ?", "legacy@example.com").Order("username asc").Find(&duplicates).Error)
	require.Len(t, duplicates, 2)
	assert.Equal(t, "old-1", duplicates[0].Password)
	assert.Equal(t, "old-2", duplicates[1].Password)

	require.NoError(t, DB.Create(&User{
		Username: "unique",
		Password: "old",
		Email:    "unique@example.com",
		AffCode:  "unique",
		Status:   common.UserStatusEnabled,
	}).Error)

	require.NoError(t, ResetUserPasswordByEmail("UNIQUE@example.com", "NewPassword123"))

	var unique User
	require.NoError(t, DB.Where("username = ?", "unique").First(&unique).Error)
	assert.True(t, common.ValidatePasswordAndHash("NewPassword123", unique.Password))

	err = ResetUserPasswordByEmail("missing@example.com", "NewPassword123")
	require.True(t, errors.Is(err, ErrEmailNotFound))
}

func TestInsertAndInsertWithTxGenerateSecureInviteCode(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Username: "insert-user-with-invite",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, user.Insert(0))

	var stored User
	require.NoError(t, DB.Where("username = ?", user.Username).First(&stored).Error)
	assert.Len(t, stored.AffCode, common.InviteCodeLength)

	err := DB.Transaction(func(tx *gorm.DB) error {
		userWithTx := User{
			Username: "insert-user-with-tx-invite",
			Password: "password",
			Status:   common.UserStatusEnabled,
		}
		return userWithTx.InsertWithTx(tx, 0)
	})
	require.NoError(t, err)

	var storedWithTx User
	require.NoError(t, DB.Where("username = ?", "insert-user-with-tx-invite").First(&storedWithTx).Error)
	assert.Len(t, storedWithTx.AffCode, common.InviteCodeLength)
}
