package model

import (
	"crypto/hmac"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	VerificationFlowTTL = 10 * time.Minute

	emailVerificationPayloadVersion = 1
	emailVerificationTargetDomain   = "email-verification-target-v1:"
	passwordResetPayloadVersion     = 1
	passwordResetEmailDomain        = "password-reset-email-v1:"
	bcryptPasswordMaxBytes          = 72
)

type emailVerificationPayload struct {
	Version    int    `json:"version"`
	TargetHash string `json:"target_hash"`
}

type passwordResetPayload struct {
	Version     int    `json:"version"`
	AuthVersion int64  `json:"auth_version"`
	EmailHash   string `json:"email_hash"`
}

func emailVerificationTargetHash(email string) string {
	return common.GenerateHMACWithKey(
		[]byte(emailVerificationTargetDomain+common.SessionSecret),
		NormalizeEmail(email),
	)
}

func validateEmailVerificationPayload(payload, email string) error {
	var binding emailVerificationPayload
	if err := common.UnmarshalJsonStr(payload, &binding); err != nil {
		return ErrAuthFlowInvalid
	}
	if binding.Version != emailVerificationPayloadVersion || binding.TargetHash == "" {
		return ErrAuthFlowInvalid
	}
	expected := emailVerificationTargetHash(email)
	if !hmac.Equal([]byte(binding.TargetHash), []byte(expected)) {
		return ErrAuthFlowInvalid
	}
	return nil
}

func passwordResetEmailHash(email string) string {
	return common.GenerateHMACWithKey(
		[]byte(passwordResetEmailDomain+common.SessionSecret),
		NormalizeEmail(email),
	)
}

func decodePasswordResetPayload(payload string) (passwordResetPayload, error) {
	var binding passwordResetPayload
	if err := common.UnmarshalJsonStr(payload, &binding); err != nil {
		return passwordResetPayload{}, ErrAuthFlowInvalid
	}
	if binding.Version != passwordResetPayloadVersion || binding.AuthVersion <= 0 || binding.EmailHash == "" {
		return passwordResetPayload{}, ErrAuthFlowInvalid
	}
	return binding, nil
}

func CreateEmailVerificationFlow(email string) (string, *AuthFlow, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return "", nil, ErrAuthFlowInvalid
	}
	payload, err := common.Marshal(emailVerificationPayload{
		Version:    emailVerificationPayloadVersion,
		TargetHash: emailVerificationTargetHash(email),
	})
	if err != nil {
		return "", nil, err
	}
	return CreateAuthFlow(AuthFlowCreate{
		Purpose:   AuthFlowPurposeEmailVerification,
		Payload:   string(payload),
		ExpiresAt: time.Now().Add(VerificationFlowTTL),
	})
}

func ValidateEmailVerificationFlow(token, email string) error {
	token = strings.TrimSpace(token)
	email = NormalizeEmail(email)
	if token == "" || email == "" {
		return ErrAuthFlowInvalid
	}
	flow, err := GetAuthFlow(token, AuthFlowMatch{Purpose: AuthFlowPurposeEmailVerification})
	if err != nil {
		return err
	}
	return validateEmailVerificationPayload(flow.Payload, email)
}

func ConsumeEmailVerificationFlowWithAction(
	token, email string,
	action func(tx *gorm.DB) error,
) error {
	token = strings.TrimSpace(token)
	email = NormalizeEmail(email)
	if token == "" || email == "" {
		return ErrAuthFlowInvalid
	}
	_, err := ConsumeAuthFlowWithAction(
		token,
		AuthFlowMatch{Purpose: AuthFlowPurposeEmailVerification},
		func(tx *gorm.DB, flow *AuthFlow) error {
			if err := validateEmailVerificationPayload(flow.Payload, email); err != nil {
				return err
			}
			if action != nil {
				return action(tx)
			}
			return nil
		},
	)
	return err
}

func createPasswordResetFlowWithTx(tx *gorm.DB, user *User) (string, *AuthFlow, error) {
	if tx == nil || user == nil || user.Id <= 0 {
		return "", nil, ErrAuthFlowInvalid
	}
	email := NormalizeEmail(user.Email)
	if email == "" || user.AuthVersion <= 0 || user.Status != common.UserStatusEnabled {
		return "", nil, ErrAuthFlowInvalid
	}
	payload, err := common.Marshal(passwordResetPayload{
		Version:     passwordResetPayloadVersion,
		AuthVersion: user.AuthVersion,
		EmailHash:   passwordResetEmailHash(email),
	})
	if err != nil {
		return "", nil, err
	}
	return CreateAuthFlowWithTx(tx, AuthFlowCreate{
		Purpose:   AuthFlowPurposePasswordReset,
		UserId:    user.Id,
		Payload:   string(payload),
		ExpiresAt: time.Now().Add(VerificationFlowTTL),
	})
}

func CreatePasswordResetFlow(user *User) (string, *AuthFlow, error) {
	if user == nil || user.Id <= 0 {
		return "", nil, ErrAuthFlowInvalid
	}
	snapshotEmail := NormalizeEmail(user.Email)
	if snapshotEmail == "" || user.AuthVersion <= 0 || user.Status != common.UserStatusEnabled {
		return "", nil, ErrAuthFlowInvalid
	}

	var token string
	var flow *AuthFlow
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current User
		if err := lockForUpdate(tx).
			Select("id", "email", "auth_version", "status").
			First(&current, user.Id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAuthFlowInvalid
			}
			return err
		}
		if NormalizeEmail(current.Email) != snapshotEmail ||
			current.AuthVersion != user.AuthVersion ||
			current.Status != user.Status ||
			current.Status != common.UserStatusEnabled {
			return ErrAuthFlowInvalid
		}
		var err error
		token, flow, err = createPasswordResetFlowWithTx(tx, &current)
		return err
	})
	if err != nil {
		return "", nil, err
	}
	return token, flow, nil
}

func CreatePasswordResetFlowForEmail(email string) (string, string, *AuthFlow, error) {
	email = NormalizeEmail(email)
	if err := common.Validate.Var(email, "required,email"); err != nil {
		return "", "", nil, ErrAuthFlowInvalid
	}

	var token string
	var recipient string
	var flow *AuthFlow
	err := DB.Transaction(func(tx *gorm.DB) error {
		var candidates []User
		if err := tx.
			Select("id", "email", "auth_version", "status").
			Where("LOWER(email) = ?", email).
			Limit(2).
			Find(&candidates).Error; err != nil {
			return err
		}
		if len(candidates) != 1 {
			return nil
		}

		snapshot := candidates[0]
		var current User
		if err := lockForUpdate(tx).
			Select("id", "email", "auth_version", "status").
			First(&current, snapshot.Id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		currentEmail := NormalizeEmail(current.Email)
		if currentEmail != email ||
			currentEmail != NormalizeEmail(snapshot.Email) ||
			current.AuthVersion != snapshot.AuthVersion ||
			current.Status != snapshot.Status ||
			current.Status != common.UserStatusEnabled ||
			current.AuthVersion <= 0 {
			return nil
		}

		var err error
		token, flow, err = createPasswordResetFlowWithTx(tx, &current)
		if err != nil {
			return err
		}
		recipient = currentEmail
		return nil
	})
	if err != nil {
		return "", "", nil, err
	}
	return token, recipient, flow, nil
}

func ResetUserPasswordWithFlow(token, password string) error {
	token = strings.TrimSpace(token)
	passwordLength := utf8.RuneCountInString(password)
	if token == "" || passwordLength < 8 || passwordLength > 20 || len(password) > bcryptPasswordMaxBytes {
		return ErrAuthFlowInvalid
	}

	peeked, err := GetAuthFlow(token, AuthFlowMatch{Purpose: AuthFlowPurposePasswordReset})
	if err != nil {
		return err
	}
	if peeked.UserId <= 0 {
		return ErrAuthFlowInvalid
	}
	if _, err := decodePasswordResetPayload(peeked.Payload); err != nil {
		return err
	}

	hashedPassword, err := common.Password2Hash(password)
	if err != nil {
		return err
	}

	subjectUserID := peeked.UserId
	_, err = ConsumeAuthFlowWithAction(
		token,
		AuthFlowMatch{
			Purpose: AuthFlowPurposePasswordReset,
			UserId:  subjectUserID,
		},
		func(tx *gorm.DB, flow *AuthFlow) error {
			binding, err := decodePasswordResetPayload(flow.Payload)
			if err != nil {
				return err
			}
			var current User
			if err := lockForUpdate(tx).
				Select("id", "email", "auth_version").
				First(&current, flow.UserId).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAuthFlowInvalid
				}
				return err
			}
			if current.AuthVersion != binding.AuthVersion {
				return ErrAuthFlowInvalid
			}
			expectedEmailHash := passwordResetEmailHash(current.Email)
			if !hmac.Equal([]byte(binding.EmailHash), []byte(expectedEmailHash)) {
				return ErrAuthFlowInvalid
			}
			if _, err := IncrementUserAuthVersionWithTx(tx, current.Id); err != nil {
				return err
			}
			result := tx.Model(&User{}).Where("id = ?", current.Id).Update("password", hashedPassword)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrAuthFlowInvalid
			}
			return nil
		},
	)
	if err != nil {
		return err
	}

	if err := PublishUserAuthCache(subjectUserID); err != nil {
		common.SysError(fmt.Sprintf("failed to publish password reset auth cache for user %d: %v", subjectUserID, err))
	}
	if _, err := RevokeAllUserSessions(subjectUserID, "password_reset"); err != nil {
		common.SysError(fmt.Sprintf("failed to revoke sessions after password reset for user %d: %v", subjectUserID, err))
	}
	return nil
}
