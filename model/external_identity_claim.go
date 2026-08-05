package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ExternalIdentityProviderGitHub    = "github"
	ExternalIdentityProviderDiscord   = "discord"
	ExternalIdentityProviderOIDC      = "oidc"
	ExternalIdentityProviderWeChat    = "wechat"
	ExternalIdentityProviderTelegram  = "telegram"
	ExternalIdentityProviderLinuxDO   = "linuxdo"
	externalIdentityProviderMaxLength = 32
	externalIdentitySubjectMaxLength  = 256
)

var builtInExternalIdentityProviders = []string{
	ExternalIdentityProviderGitHub,
	ExternalIdentityProviderDiscord,
	ExternalIdentityProviderOIDC,
	ExternalIdentityProviderWeChat,
	ExternalIdentityProviderTelegram,
	ExternalIdentityProviderLinuxDO,
}

var builtInAuthenticationBindingColumns = map[string]string{
	ExternalIdentityProviderGitHub:   "github_id",
	ExternalIdentityProviderDiscord:  "discord_id",
	ExternalIdentityProviderOIDC:     "oidc_id",
	ExternalIdentityProviderWeChat:   "wechat_id",
	ExternalIdentityProviderTelegram: "telegram_id",
	ExternalIdentityProviderLinuxDO:  "linux_do_id",
}

var ErrExternalIdentityAlreadyClaimed = errors.New("external identity is already claimed")

// ExternalIdentityClaim is the durable ownership record for an identity issued
// by an external provider. The two unique indexes make both the provider
// subject and the user's provider slot single-owner without relying on a
// check-then-update sequence.
type ExternalIdentityClaim struct {
	Id        int64     `json:"id" gorm:"primaryKey"`
	Provider  string    `json:"provider" gorm:"type:varchar(32);not null;uniqueIndex:idx_external_identity_subject,priority:1;uniqueIndex:idx_external_identity_user,priority:1"`
	Subject   string    `json:"subject" gorm:"type:varchar(256);not null;uniqueIndex:idx_external_identity_subject,priority:2"`
	UserId    int       `json:"user_id" gorm:"not null;index;uniqueIndex:idx_external_identity_user,priority:2"`
	CreatedAt time.Time `json:"created_at"`
}

func (ExternalIdentityClaim) TableName() string {
	return "external_identity_claims"
}

// ClaimExternalIdentityWithTx atomically claims a provider subject for one
// user. Repeating the exact mapping is idempotent; every competing subject or
// user is rejected. Ownership is read back instead of trusting RowsAffected,
// whose duplicate-key semantics differ between supported databases.
func ClaimExternalIdentityWithTx(tx *gorm.DB, provider, subject string, userId int) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	subject = strings.TrimSpace(subject)
	if tx == nil || provider == "" || len(provider) > externalIdentityProviderMaxLength ||
		subject == "" || len(subject) > externalIdentitySubjectMaxLength || userId == 0 {
		return errors.New("external identity claim is invalid")
	}

	claim := ExternalIdentityClaim{Provider: provider, Subject: subject, UserId: userId}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&claim)
	if result.Error != nil {
		return result.Error
	}
	var subjectOwner ExternalIdentityClaim
	if err := tx.Where("provider = ? AND subject = ?", provider, subject).First(&subjectOwner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrExternalIdentityAlreadyClaimed
		}
		return err
	}
	if subjectOwner.UserId != userId {
		return ErrExternalIdentityAlreadyClaimed
	}

	var userClaim ExternalIdentityClaim
	if err := tx.Where("provider = ? AND user_id = ?", provider, userId).First(&userClaim).Error; err != nil {
		return err
	}
	if userClaim.Subject != subject {
		return ErrExternalIdentityAlreadyClaimed
	}
	return nil
}

// ReplaceExternalIdentityWithTx atomically replaces a user's subject for one
// provider. A conflicting owner leaves the previous claim unchanged because
// both release and claim run in the caller's transaction.
func ReplaceExternalIdentityWithTx(tx *gorm.DB, provider, subject string, userId int) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	subject = strings.TrimSpace(subject)
	if tx == nil || provider == "" || len(provider) > externalIdentityProviderMaxLength ||
		subject == "" || len(subject) > externalIdentitySubjectMaxLength || userId == 0 {
		return errors.New("external identity claim is invalid")
	}

	var current ExternalIdentityClaim
	err := lockForUpdate(tx).
		Where("provider = ? AND user_id = ?", provider, userId).
		First(&current).Error
	if err == nil {
		if current.Subject == subject {
			return nil
		}
		if err := tx.Delete(&current).Error; err != nil {
			return err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return ClaimExternalIdentityWithTx(tx, provider, subject, userId)
}

func ReleaseExternalIdentityWithTx(tx *gorm.DB, provider string, userId int) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if tx == nil || provider == "" || len(provider) > externalIdentityProviderMaxLength || userId == 0 {
		return errors.New("external identity release is invalid")
	}
	return tx.Where("provider = ? AND user_id = ?", provider, userId).
		Delete(&ExternalIdentityClaim{}).Error
}

func releaseAllExternalIdentitiesWithTx(tx *gorm.DB, userId int) error {
	if tx == nil || userId == 0 {
		return errors.New("external identity release is invalid")
	}
	return tx.Where("user_id = ?", userId).Delete(&ExternalIdentityClaim{}).Error
}

// InitializeExternalIdentityClaims imports legacy built-in provider bindings
// after the claim table is migrated. Existing duplicate ownership fails
// migration rather than preserving an ambiguous login identity.
func InitializeExternalIdentityClaims() error {
	var users []User
	if err := DB.Unscoped().
		Select("id", "github_id", "discord_id", "oidc_id", "wechat_id", "telegram_id", "linux_do_id").
		Where(
			"github_id <> ? OR discord_id <> ? OR oidc_id <> ? OR wechat_id <> ? OR telegram_id <> ? OR linux_do_id <> ?",
			"", "", "", "", "", "",
		).
		Find(&users).Error; err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		for _, user := range users {
			for _, provider := range builtInExternalIdentityProviders {
				subject := ""
				switch provider {
				case ExternalIdentityProviderGitHub:
					subject = user.GitHubId
				case ExternalIdentityProviderDiscord:
					subject = user.DiscordId
				case ExternalIdentityProviderOIDC:
					subject = user.OidcId
				case ExternalIdentityProviderWeChat:
					subject = user.WeChatId
				case ExternalIdentityProviderTelegram:
					subject = user.TelegramId
				case ExternalIdentityProviderLinuxDO:
					subject = user.LinuxDOId
				}
				if strings.TrimSpace(subject) == "" {
					continue
				}
				if err := ClaimExternalIdentityWithTx(tx, provider, subject, user.Id); err != nil {
					return fmt.Errorf(
						"backfill %s identity for user %d: %w",
						provider,
						user.Id,
						err,
					)
				}
			}
		}
		return nil
	})
}
