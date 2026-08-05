package model

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHardDeleteUserFailsClosedWhenAuthFenceCannotPublish(t *testing.T) {
	truncateTables(t)

	user := User{Username: "hard-delete-user", Password: "password", TelegramId: "hard-delete-telegram"}
	require.NoError(t, DB.Create(&user).Error)
	_, _, err := RotateDashboardAccessToken(user.Id, user.AuthVersion)
	require.NoError(t, err)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, user.TelegramId, user.Id)
	}))
	require.NoError(t, DB.Create(&Token{UserId: user.Id, Key: "hard-delete-token"}).Error)
	require.NoError(t, DB.Create(&TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}).Error)
	require.NoError(t, DB.Create(&TwoFABackupCode{UserId: user.Id, CodeHash: "hash"}).Error)
	require.NoError(t, DB.Create(&PasskeyCredential{UserID: user.Id, CredentialID: "credential", PublicKey: "public-key"}).Error)
	require.NoError(t, DB.Create(&UserOAuthBinding{UserId: user.Id, ProviderId: 1, ProviderUserId: "provider-user"}).Error)
	require.NoError(t, DB.Create(&UserSession{
		SID: "hard-delete-session", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: UserSessionStatusActive, RefreshHash: "refresh-hash", LoginMethod: "password",
		LastActiveAt: 1, ExpiresAt: 2,
	}).Error)
	require.NoError(t, DB.Create(&AuthFlow{
		TokenHash: "hard-delete-auth-flow", Purpose: AuthFlowPurposeTwoFALogin,
		UserId: user.Id, ExpiresAt: time.Now().Add(time.Minute),
	}).Error)

	oldRedisEnabled, oldRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("forced redis failure")
		},
		MaxRetries: -1,
	})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RedisEnabled, common.RDB = oldRedisEnabled, oldRDB
	})

	require.Error(t, HardDeleteUserById(user.Id))

	var count int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	for _, record := range []any{
		&Token{},
		&DashboardAccessToken{},
		&TwoFA{},
		&TwoFABackupCode{},
		&PasskeyCredential{},
		&UserOAuthBinding{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
	} {
		require.NoError(t, DB.Unscoped().Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
		assert.EqualValues(t, 1, count)
	}
}

func TestSoftDeleteUserPurgesAuthenticationSurfaceButKeepsHistoryRow(t *testing.T) {
	truncateTables(t)

	user := User{
		Username: "soft-delete-user", Password: "password", AuthVersion: 1,
		TelegramId: "soft-delete-telegram",
	}
	require.NoError(t, DB.Create(&user).Error)
	_, _, err := RotateDashboardAccessToken(user.Id, user.AuthVersion)
	require.NoError(t, err)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, user.TelegramId, user.Id)
	}))
	require.NoError(t, DB.Create(&Token{UserId: user.Id, Key: "soft-delete-token"}).Error)
	require.NoError(t, DB.Create(&TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}).Error)
	require.NoError(t, DB.Create(&TwoFABackupCode{UserId: user.Id, CodeHash: "hash"}).Error)
	require.NoError(t, DB.Create(&PasskeyCredential{UserID: user.Id, CredentialID: "soft-delete-credential", PublicKey: "public-key"}).Error)
	require.NoError(t, DB.Create(&UserOAuthBinding{UserId: user.Id, ProviderId: 1, ProviderUserId: "soft-delete-provider-user"}).Error)
	require.NoError(t, DB.Create(&UserSession{
		SID: "soft-delete-session", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: UserSessionStatusActive, RefreshHash: "refresh-hash", LoginMethod: "password",
		LastActiveAt: 1, ExpiresAt: 2,
	}).Error)
	require.NoError(t, DB.Create(&AuthFlow{
		TokenHash: "soft-delete-auth-flow", Purpose: AuthFlowPurposeTwoFALogin,
		UserId: user.Id, ExpiresAt: time.Now().Add(time.Minute),
	}).Error)

	deleted := User{Id: user.Id}
	require.NoError(t, deleted.Delete())

	// Billing and audit history still resolves the account, so the row survives.
	var stored User
	require.NoError(t, DB.Unscoped().First(&stored, user.Id).Error)
	assert.True(t, stored.DeletedAt.Valid)
	assert.Greater(t, stored.AuthVersion, int64(1))
	assert.ErrorIs(t, DB.First(&User{}, user.Id).Error, gorm.ErrRecordNotFound)

	// Every credential is purged outright: there is no undelete path, so a
	// surviving key, secret, passkey, or identity binding would stay usable.
	var count int64
	for _, record := range []any{
		&Token{},
		&DashboardAccessToken{},
		&TwoFA{},
		&TwoFABackupCode{},
		&PasskeyCredential{},
		&UserOAuthBinding{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
	} {
		require.NoError(t, DB.Unscoped().Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
		assert.Zerof(t, count, "%T should be purged by soft delete", record)
	}

	// Retrying the delete must stay idempotent rather than failing on the
	// already-purged credential rows.
	require.NoError(t, (&User{Id: user.Id}).Delete())
}

func TestHardDeleteUserPublishesTombstoneAndPurgesAuthenticationData(t *testing.T) {
	truncateTables(t)
	server := useUserCacheMiniRedis(t)

	user := User{
		Username: "hard-delete-success", Password: "password", AuthVersion: 1,
		TelegramId: "hard-delete-success-telegram",
	}
	require.NoError(t, DB.Create(&user).Error)
	_, _, err := RotateDashboardAccessToken(user.Id, user.AuthVersion)
	require.NoError(t, err)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, user.TelegramId, user.Id)
	}))
	require.NoError(t, DB.Create(&Token{UserId: user.Id, Key: "hard-delete-success-token"}).Error)
	require.NoError(t, DB.Create(&TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}).Error)
	require.NoError(t, DB.Create(&TwoFABackupCode{UserId: user.Id, CodeHash: "hash"}).Error)
	require.NoError(t, DB.Create(&PasskeyCredential{UserID: user.Id, CredentialID: "credential-success", PublicKey: "public-key"}).Error)
	require.NoError(t, DB.Create(&UserOAuthBinding{UserId: user.Id, ProviderId: 1, ProviderUserId: "provider-user-success"}).Error)
	require.NoError(t, DB.Create(&UserSession{
		SID: "hard-delete-success-session", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: UserSessionStatusActive, RefreshHash: "refresh-hash", LoginMethod: "password",
		LastActiveAt: 1, ExpiresAt: 2,
	}).Error)
	require.NoError(t, DB.Create(&AuthFlow{
		TokenHash: "hard-delete-success-flow", Purpose: AuthFlowPurposeTwoFALogin,
		UserId: user.Id, ExpiresAt: time.Now().Add(time.Minute),
	}).Error)
	require.NoError(t, populateUserCache(user))
	// Administrative hard deletion commonly targets an already soft-deleted
	// user; the shared version increment must therefore query unscoped.
	require.NoError(t, DB.Delete(&user).Error)

	require.NoError(t, HardDeleteUserById(user.Id))

	var count int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
	for _, record := range []any{
		&Token{},
		&DashboardAccessToken{},
		&TwoFA{},
		&TwoFABackupCode{},
		&PasskeyCredential{},
		&UserOAuthBinding{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
	} {
		require.NoError(t, DB.Unscoped().Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
		assert.Zero(t, count)
	}
	assert.False(t, server.Exists(getUserAuthFenceKey(user.Id)))
	committed, err := common.RDB.Get(t.Context(), getUserAuthVersionKey(user.Id)).Result()
	require.NoError(t, err)
	assert.Equal(t, "2", committed)
	assert.False(t, server.Exists(getUserCacheKey(user.Id)))
}

func TestIncrementFailedAttemptsCountsConcurrentFailures(t *testing.T) {
	truncateTables(t)

	user := User{Username: "twofa-cas-user", Password: "password"}
	require.NoError(t, DB.Create(&user).Error)
	twoFA := TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}
	require.NoError(t, DB.Create(&twoFA).Error)

	const attempts = 4
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- (&TwoFA{Id: twoFA.Id}).IncrementFailedAttempts()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	var reloaded TwoFA
	require.NoError(t, DB.First(&reloaded, twoFA.Id).Error)
	assert.Equal(t, attempts, reloaded.FailedAttempts)
}

func TestValidateBackupCodeCanOnlySucceedOnce(t *testing.T) {
	truncateTables(t)

	const code = "ABCD-1234"
	user := User{Id: 123, Username: "backup-code-user", Password: "password", AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: false}).Error)
	require.NoError(t, CreatePendingTwoFASetupBackupCodes(user.Id, []string{code}))

	const attempts = 2
	results := make(chan bool, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			valid, err := ValidateBackupCode(123, code)
			results <- valid
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	wins := 0
	for valid := range results {
		if valid {
			wins++
		}
	}
	assert.Equal(t, 1, wins)

	remaining, err := GetUnusedBackupCodeCount(123)
	require.NoError(t, err)
	assert.Zero(t, remaining)
}

func TestPendingTwoFASetupAPIsRejectEnabledFactor(t *testing.T) {
	truncateTables(t)

	user := User{Username: "enabled-twofa-guard", Password: "password", AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	twoFA := TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: true}
	require.NoError(t, DB.Create(&twoFA).Error)

	require.Error(t, CreatePendingTwoFASetupBackupCodes(user.Id, []string{"ABCD-1234"}))
	require.Error(t, twoFA.DeletePendingTwoFASetup())

	var stored TwoFA
	require.NoError(t, DB.First(&stored, twoFA.Id).Error)
	assert.True(t, stored.IsEnabled)
	var backupCodeCount int64
	require.NoError(t, DB.Model(&TwoFABackupCode{}).Where("user_id = ?", user.Id).Count(&backupCodeCount).Error)
	assert.Zero(t, backupCodeCount)
}

func TestEnableWithAuthVersionRejectsExpiredPendingSetup(t *testing.T) {
	truncateTables(t)

	user := User{
		Username:    "expired-twofa-enrollment",
		Password:    "password",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)
	twoFA := TwoFA{
		UserId:    user.Id,
		Secret:    "expired-secret",
		IsEnabled: false,
		CreatedAt: time.Now().Add(-48 * time.Hour),
	}
	require.NoError(t, DB.Create(&twoFA).Error)
	require.NoError(t, DB.Create(&TwoFABackupCode{
		UserId:   user.Id,
		CodeHash: "expired-code",
	}).Error)

	err := twoFA.EnableWithAuthVersion()
	require.ErrorIs(t, err, ErrTwoFASetupExpired)

	var factorCount int64
	require.NoError(t, DB.Unscoped().Model(&TwoFA{}).
		Where("id = ?", twoFA.Id).
		Count(&factorCount).Error)
	assert.Zero(t, factorCount)
	var codeCount int64
	require.NoError(t, DB.Unscoped().Model(&TwoFABackupCode{}).
		Where("user_id = ?", user.Id).
		Count(&codeCount).Error)
	assert.Zero(t, codeCount)
	assertUserAuthVersion(t, user.Id, 1)
}

func TestCleanupExpiredPendingTwoFASetupsDeletesOnlyPendingCredentials(t *testing.T) {
	truncateTables(t)

	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	fixtures := []struct {
		username  string
		createdAt time.Time
		enabled   bool
		expired   bool
	}{
		{
			username:  "expired-twofa-one",
			createdAt: now.Add(-48 * time.Hour),
			expired:   true,
		},
		{
			username:  "expired-twofa-two",
			createdAt: now.Add(-24 * time.Hour),
			expired:   true,
		},
		{
			username:  "fresh-pending-twofa",
			createdAt: now.Add(-23 * time.Hour),
		},
		{
			username:  "old-enabled-twofa",
			createdAt: now.Add(-72 * time.Hour),
			enabled:   true,
		},
	}
	factors := make([]TwoFA, 0, len(fixtures))
	for index, fixture := range fixtures {
		user := User{
			Username:    fixture.username,
			Password:    "password",
			AffCode:     fmt.Sprintf("twofa-cleanup-%d", index),
			AuthVersion: 1,
		}
		require.NoError(t, DB.Create(&user).Error)
		factor := TwoFA{
			UserId:    user.Id,
			Secret:    fixture.username + "-secret",
			IsEnabled: fixture.enabled,
			CreatedAt: fixture.createdAt,
		}
		require.NoError(t, DB.Create(&factor).Error)
		require.NoError(t, DB.Create(&TwoFABackupCode{
			UserId:   user.Id,
			CodeHash: fmt.Sprintf("code-%d", index),
		}).Error)
		factors = append(factors, factor)
	}

	deleted, err := CleanupExpiredPendingTwoFASetups(t.Context(), now, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	for index, fixture := range fixtures {
		factor := factors[index]
		var factorCount int64
		require.NoError(t, DB.Unscoped().Model(&TwoFA{}).
			Where("id = ?", factor.Id).
			Count(&factorCount).Error)
		var codeCount int64
		require.NoError(t, DB.Unscoped().Model(&TwoFABackupCode{}).
			Where("user_id = ?", factor.UserId).
			Count(&codeCount).Error)
		if fixture.expired {
			assert.Zero(t, factorCount)
			assert.Zero(t, codeCount)
			continue
		}
		assert.EqualValues(t, 1, factorCount)
		assert.EqualValues(t, 1, codeCount)
	}

	deleted, err = CleanupExpiredPendingTwoFASetups(t.Context(), now, 1)
	require.NoError(t, err)
	assert.Zero(t, deleted)

	replacement := TwoFA{
		UserId:    factors[0].UserId,
		Secret:    "replacement-secret",
		IsEnabled: false,
		CreatedAt: now,
	}
	require.NoError(t, replacement.CreatePendingTwoFASetupWithBackupCodes(
		[]string{"ABCD-1234"},
	))
	var replacementCodeCount int64
	require.NoError(t, DB.Model(&TwoFABackupCode{}).
		Where("user_id = ?", replacement.UserId).
		Count(&replacementCodeCount).Error)
	assert.EqualValues(t, 1, replacementCodeCount)
}

func TestCreatePendingTwoFASetupWithBackupCodesRollsBackTogether(t *testing.T) {
	truncateTables(t)

	user := User{
		Username: "atomic-twofa-enrollment",
		Password: "password",
	}
	require.NoError(t, DB.Create(&user).Error)
	twoFA := TwoFA{
		UserId:    user.Id,
		Secret:    "pending-secret",
		IsEnabled: false,
	}

	err := twoFA.CreatePendingTwoFASetupWithBackupCodes(
		[]string{strings.Repeat("x", 73)},
	)
	require.Error(t, err)

	var factorCount int64
	require.NoError(t, DB.Unscoped().Model(&TwoFA{}).
		Where("user_id = ?", user.Id).
		Count(&factorCount).Error)
	assert.Zero(t, factorCount)
	var codeCount int64
	require.NoError(t, DB.Unscoped().Model(&TwoFABackupCode{}).
		Where("user_id = ?", user.Id).
		Count(&codeCount).Error)
	assert.Zero(t, codeCount)
}

func TestStalePendingTwoFACleanupCandidatePreservesReplacementCodes(t *testing.T) {
	truncateTables(t)

	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	user := User{
		Username:    "stale-twofa-cleanup-candidate",
		Password:    "password",
		AffCode:     "stale-twofa-cleanup-aff",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)
	expired := TwoFA{
		UserId:    user.Id,
		Secret:    "expired-secret",
		IsEnabled: false,
		CreatedAt: now.Add(-48 * time.Hour),
	}
	require.NoError(t, expired.CreatePendingTwoFASetupWithBackupCodes(
		[]string{"OLD1-CODE"},
	))

	staleCandidateID := expired.Id
	require.NoError(t, expired.DeletePendingTwoFASetup())
	replacement := TwoFA{
		UserId:    user.Id,
		Secret:    "replacement-secret",
		IsEnabled: false,
		CreatedAt: now,
	}
	require.NoError(t, replacement.CreatePendingTwoFASetupWithBackupCodes(
		[]string{"NEW1-CODE"},
	))

	var claimed bool
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		claimed, err = deletePendingTwoFASetupWithTx(
			tx,
			staleCandidateID,
			user.Id,
			now.Add(-PendingTwoFASetupTTL),
		)
		return err
	}))
	assert.False(t, claimed)

	var stored TwoFA
	require.NoError(t, DB.First(&stored, replacement.Id).Error)
	assert.Equal(t, replacement.Secret, stored.Secret)
	var codeCount int64
	require.NoError(t, DB.Model(&TwoFABackupCode{}).
		Where("user_id = ?", user.Id).
		Count(&codeCount).Error)
	assert.EqualValues(t, 1, codeCount)
}

func TestSecurityFactorMutationsAdvanceUserAuthVersion(t *testing.T) {
	truncateTables(t)

	user := User{
		Username:    "security-factor-version-user",
		Password:    "password",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)
	twoFA := TwoFA{UserId: user.Id, Secret: "secret", IsEnabled: false}
	require.NoError(t, DB.Create(&twoFA).Error)

	require.NoError(t, twoFA.EnableWithAuthVersion())
	assertUserAuthVersion(t, user.Id, 2)
	assert.ErrorIs(t, twoFA.EnableWithAuthVersion(), ErrTwoFAAlreadyEnabled)
	assertUserAuthVersion(t, user.Id, 2)
	require.NoError(t, ReplaceBackupCodesWithAuthVersion(user.Id, []string{"ABCD-1234"}))
	assertUserAuthVersion(t, user.Id, 3)
	require.NoError(t, DisableTwoFAWithAuthVersion(user.Id))
	assertUserAuthVersion(t, user.Id, 4)

	credential := &PasskeyCredential{UserID: user.Id, CredentialID: "credential-id", PublicKey: "public-key"}
	require.NoError(t, UpsertPasskeyCredentialWithAuthVersion(credential))
	assertUserAuthVersion(t, user.Id, 5)
	require.NoError(t, DeletePasskeyByUserIDWithAuthVersion(user.Id))
	assertUserAuthVersion(t, user.Id, 6)
}

func TestExternalIdentityMutationsAdvanceUserAuthVersion(t *testing.T) {
	truncateTables(t)
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedisEnabled })

	user := User{
		Username:    "external-factor-user",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AffCode:     "external-factor-aff",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)

	updated, err := BindBuiltInExternalIdentityWithAuthVersion(
		user.Id,
		"wechat",
		"wechat-subject",
	)
	require.NoError(t, err)
	assert.Equal(t, "wechat-subject", updated.WeChatId)
	assert.Equal(t, int64(2), updated.AuthVersion)
	assertUserAuthVersion(t, user.Id, 2)
	var builtInClaims int64
	require.NoError(t, DB.Model(&ExternalIdentityClaim{}).
		Where(
			"provider = ? AND user_id = ?",
			ExternalIdentityProviderWeChat,
			user.Id,
		).
		Count(&builtInClaims).Error)
	assert.Equal(t, int64(1), builtInClaims)

	require.NoError(t, UpdateUserOAuthBindingWithAuthVersion(
		user.Id,
		9,
		"custom-subject",
	))
	assertUserAuthVersion(t, user.Id, 3)
	binding, err := GetUserOAuthBinding(user.Id, 9)
	require.NoError(t, err)
	assert.Equal(t, "custom-subject", binding.ProviderUserId)

	require.NoError(t, DeleteUserOAuthBindingWithAuthVersion(user.Id, 9))
	assertUserAuthVersion(t, user.Id, 4)
	_, err = GetUserOAuthBinding(user.Id, 9)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	require.NoError(t, updated.ClearBinding("wechat"))
	assertUserAuthVersion(t, user.Id, 5)
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Empty(t, stored.WeChatId)
	require.NoError(t, DB.Model(&ExternalIdentityClaim{}).
		Where(
			"provider = ? AND user_id = ?",
			ExternalIdentityProviderWeChat,
			user.Id,
		).
		Count(&builtInClaims).Error)
	assert.Zero(t, builtInClaims)
}

func TestRejectedExternalIdentityMutationDoesNotAdvanceAuthVersion(t *testing.T) {
	truncateTables(t)
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedisEnabled })

	user := User{
		Username:    "external-factor-user",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AffCode:     "external-factor-aff",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)

	_, err := BindBuiltInExternalIdentityWithAuthVersion(
		user.Id,
		"unsupported",
		"subject",
	)
	require.Error(t, err)
	assertUserAuthVersion(t, user.Id, 1)

	err = DeleteUserOAuthBindingWithAuthVersion(user.Id, 99)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assertUserAuthVersion(t, user.Id, 1)
}

func TestBuiltInExternalIdentityBindingHasSingleOwner(t *testing.T) {
	truncateTables(t)
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedisEnabled })

	first := User{
		Username:    "external-owner-one",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AffCode:     "external-owner-one-aff",
		AuthVersion: 1,
	}
	second := User{
		Username:    "external-owner-two",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AffCode:     "external-owner-two-aff",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&first).Error)
	require.NoError(t, DB.Create(&second).Error)

	_, err := BindBuiltInExternalIdentityWithAuthVersion(
		first.Id,
		"github",
		"github-subject",
	)
	require.NoError(t, err)

	_, err = BindBuiltInExternalIdentityWithAuthVersion(
		second.Id,
		"github",
		"github-other-subject",
	)
	require.NoError(t, err)

	_, err = BindBuiltInExternalIdentityWithAuthVersion(
		second.Id,
		"github",
		"github-subject",
	)
	assert.ErrorIs(t, err, ErrExternalIdentityAlreadyClaimed)

	var storedSecond User
	require.NoError(t, DB.First(&storedSecond, second.Id).Error)
	assert.Equal(t, "github-other-subject", storedSecond.GitHubId)
	assert.Equal(t, int64(2), storedSecond.AuthVersion)

	var secondClaim ExternalIdentityClaim
	require.NoError(t, DB.Where(
		"provider = ? AND user_id = ?",
		ExternalIdentityProviderGitHub,
		second.Id,
	).First(&secondClaim).Error)
	assert.Equal(t, "github-other-subject", secondClaim.Subject)
}

func TestUpdatePasskeyAssertionStateCannotRewriteRegistrationIdentity(t *testing.T) {
	truncateTables(t)

	user := User{Username: "passkey-assertion-state", Password: "password", AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	credentialID := []byte("stable-credential-id")
	stored := PasskeyCredential{
		UserID:          user.Id,
		CredentialID:    base64.StdEncoding.EncodeToString(credentialID),
		PublicKey:       "original-public-key",
		AttestationType: "packed",
		AAGUID:          "original-aaguid",
		SignCount:       1,
		Transports:      `["usb"]`,
		Attachment:      "platform",
	}
	require.NoError(t, DB.Create(&stored).Error)
	usedAt := time.Now().UTC().Truncate(time.Second)
	validated := &webauthn.Credential{
		ID:              credentialID,
		PublicKey:       []byte("replacement-public-key"),
		AttestationType: "none",
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   true,
			BackupEligible: true,
			BackupState:    true,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:       []byte("replacement-aaguid"),
			SignCount:    8,
			CloneWarning: true,
		},
	}
	require.NoError(t, UpdatePasskeyAssertionState(user.Id, validated, usedAt))

	var updated PasskeyCredential
	require.NoError(t, DB.First(&updated, stored.ID).Error)
	assert.Equal(t, stored.CredentialID, updated.CredentialID)
	assert.Equal(t, stored.PublicKey, updated.PublicKey)
	assert.Equal(t, stored.AttestationType, updated.AttestationType)
	assert.Equal(t, stored.AAGUID, updated.AAGUID)
	assert.Equal(t, stored.Transports, updated.Transports)
	assert.Equal(t, stored.Attachment, updated.Attachment)
	assert.EqualValues(t, 8, updated.SignCount)
	assert.True(t, updated.CloneWarning)
	assert.True(t, updated.UserPresent)
	assert.True(t, updated.UserVerified)
	assert.True(t, updated.BackupEligible)
	assert.True(t, updated.BackupState)
	require.NotNil(t, updated.LastUsedAt)
	assert.Equal(t, usedAt.Unix(), updated.LastUsedAt.Unix())

	validated.ID = []byte("another-credential")
	assert.ErrorIs(t, UpdatePasskeyAssertionState(user.Id, validated, usedAt), ErrPasskeyNotFound)
}

func assertUserAuthVersion(t *testing.T, userID int, expected int64) {
	t.Helper()
	var version int64
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userID).Select("auth_version").Scan(&version).Error)
	assert.Equal(t, expected, version)
}
