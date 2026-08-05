package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBuiltInExternalIdentityConcurrentClaimsHaveOneWinner(t *testing.T) {
	truncateTables(t)

	first := User{Username: "github-concurrent-one", Password: "password", AffCode: "github-concurrent-one"}
	second := User{Username: "github-concurrent-two", Password: "password", AffCode: "github-concurrent-two"}
	require.NoError(t, DB.Create(&first).Error)
	require.NoError(t, DB.Create(&second).Error)

	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, userID := range []int{first.Id, second.Id} {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- DB.Transaction(func(tx *gorm.DB) error {
				return ClaimExternalIdentityWithTx(
					tx,
					ExternalIdentityProviderGitHub,
					"github-concurrent-subject",
					userID,
				)
			})
		}()
	}
	close(start)
	group.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrExternalIdentityAlreadyClaimed):
			conflicts++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)

	var claims int64
	require.NoError(t, DB.Model(&ExternalIdentityClaim{}).
		Where(
			"provider = ? AND subject = ?",
			ExternalIdentityProviderGitHub,
			"github-concurrent-subject",
		).
		Count(&claims).Error)
	assert.Equal(t, int64(1), claims)
}

func TestExternalIdentityClaimEnforcesSingleOwnerAtomically(t *testing.T) {
	truncateTables(t)

	first := User{Username: "telegram-owner-one", Password: "password", AffCode: "telegram-owner-one"}
	second := User{Username: "telegram-owner-two", Password: "password", AffCode: "telegram-owner-two"}
	require.NoError(t, DB.Create(&first).Error)
	require.NoError(t, DB.Create(&second).Error)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, "telegram-123", first.Id)
	}))
	err := DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, "telegram-123", second.Id)
	})
	assert.ErrorIs(t, err, ErrExternalIdentityAlreadyClaimed)

	err = DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, "telegram-456", first.Id)
	})
	assert.ErrorIs(t, err, ErrExternalIdentityAlreadyClaimed)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, "telegram-456", second.Id)
	}))
	err = DB.Transaction(func(tx *gorm.DB) error {
		return ReplaceExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, "telegram-456", first.Id)
	})
	assert.ErrorIs(t, err, ErrExternalIdentityAlreadyClaimed)

	var claims []ExternalIdentityClaim
	require.NoError(t, DB.Find(&claims).Error)
	require.Len(t, claims, 2)
	var firstClaim ExternalIdentityClaim
	require.NoError(t, DB.Where(
		"provider = ? AND user_id = ?",
		ExternalIdentityProviderTelegram,
		first.Id,
	).First(&firstClaim).Error)
	assert.Equal(t, "telegram-123", firstClaim.Subject)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ReleaseExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, first.Id)
	}))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ReleaseExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, second.Id)
	}))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, "telegram-123", second.Id)
	}))
}

func TestClearTelegramBindingReleasesIdentityClaim(t *testing.T) {
	truncateTables(t)

	user := User{Username: "telegram-unbind", Password: "password", TelegramId: "telegram-unbind-id"}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, user.TelegramId, user.Id)
	}))

	require.NoError(t, user.ClearBinding(ExternalIdentityProviderTelegram))
	assert.Empty(t, user.TelegramId)

	var count int64
	require.NoError(t, DB.Model(&ExternalIdentityClaim{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestInitializeExternalIdentityClaimsIsIdempotent(t *testing.T) {
	truncateTables(t)

	user := User{
		Username:   "external-legacy",
		Password:   "password",
		GitHubId:   "github-legacy-id",
		DiscordId:  "discord-legacy-id",
		OidcId:     "oidc-legacy-id",
		WeChatId:   "wechat-legacy-id",
		TelegramId: "telegram-legacy-id",
		LinuxDOId:  "linuxdo-legacy-id",
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, InitializeExternalIdentityClaims())
	require.NoError(t, InitializeExternalIdentityClaims())

	expected := map[string]string{
		ExternalIdentityProviderGitHub:   user.GitHubId,
		ExternalIdentityProviderDiscord:  user.DiscordId,
		ExternalIdentityProviderOIDC:     user.OidcId,
		ExternalIdentityProviderWeChat:   user.WeChatId,
		ExternalIdentityProviderTelegram: user.TelegramId,
		ExternalIdentityProviderLinuxDO:  user.LinuxDOId,
	}
	for provider, subject := range expected {
		var claim ExternalIdentityClaim
		require.NoError(t, DB.Where("provider = ? AND subject = ?", provider, subject).
			First(&claim).Error)
		assert.Equal(t, user.Id, claim.UserId)
	}
}

func TestInitializeExternalIdentityClaimsRejectsAmbiguousLegacyBindings(t *testing.T) {
	truncateTables(t)

	first := User{Username: "github-legacy-one", Password: "password", GitHubId: "duplicate-github-id", AffCode: "github-legacy-one"}
	second := User{Username: "github-legacy-two", Password: "password", GitHubId: "duplicate-github-id", AffCode: "github-legacy-two"}
	require.NoError(t, DB.Create(&first).Error)
	require.NoError(t, DB.Create(&second).Error)

	err := InitializeExternalIdentityClaims()
	assert.ErrorIs(t, err, ErrExternalIdentityAlreadyClaimed)

	var count int64
	require.NoError(t, DB.Model(&ExternalIdentityClaim{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestGitHubLegacyIdentityMigrationReplacesClaimAtomically(t *testing.T) {
	truncateTables(t)

	user := User{
		Username: "github-legacy-migration",
		Password: "password",
		GitHubId: "github-login-name",
		AffCode:  "github-legacy-migration",
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, InitializeExternalIdentityClaims())

	require.NoError(t, user.UpdateGitHubId("123456"))

	var oldCount int64
	require.NoError(t, DB.Model(&ExternalIdentityClaim{}).
		Where(
			"provider = ? AND subject = ?",
			ExternalIdentityProviderGitHub,
			"github-login-name",
		).
		Count(&oldCount).Error)
	assert.Zero(t, oldCount)

	var claim ExternalIdentityClaim
	require.NoError(t, DB.Where(
		"provider = ? AND subject = ?",
		ExternalIdentityProviderGitHub,
		"123456",
	).First(&claim).Error)
	assert.Equal(t, user.Id, claim.UserId)
}
