package model

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createVerificationFlowTestUser(t *testing.T, suffix string) *User {
	t.Helper()
	user := &User{
		Username:    "verification-flow-" + suffix,
		Password:    "unchanged-password-hash",
		Email:       suffix + "@example.com",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
		AffCode:     "verification-flow-aff-" + suffix,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func TestEmailVerificationFlowPersistsOnlyBoundDigests(t *testing.T) {
	truncateTables(t)

	token, created, err := CreateEmailVerificationFlow("  Target.User@Example.COM ")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	var stored AuthFlow
	require.NoError(t, DB.First(&stored, created.Id).Error)
	assert.NotEqual(t, token, stored.TokenHash)
	assert.NotContains(t, stored.Payload, token)
	assert.NotContains(t, strings.ToLower(stored.Payload), "target.user@example.com")

	err = ValidateEmailVerificationFlow(token, "other@example.com")
	assert.ErrorIs(t, err, ErrAuthFlowInvalid)
	require.NoError(t, ValidateEmailVerificationFlow(token, " target.USER@example.com "))
}

func TestEmailVerificationFlowRejectsMalformedBindingPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "unknown version", payload: `{"version":2,"target_hash":"present"}`},
		{name: "empty target hash", payload: `{"version":1,"target_hash":""}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			token, flow, err := CreateEmailVerificationFlow("target@example.com")
			require.NoError(t, err)
			require.NoError(t, DB.Model(&AuthFlow{}).Where("id = ?", flow.Id).Update("payload", test.payload).Error)

			err = ValidateEmailVerificationFlow(token, "target@example.com")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid)
		})
	}
}

func TestEmailVerificationFlowActionFailureRollsBackMutationAndConsumption(t *testing.T) {
	truncateTables(t)
	user := &User{
		Username: "verification-action-rollback",
		Password: "password-hash",
		Status:   common.UserStatusEnabled,
		AffCode:  "verify-action-rollback",
	}
	require.NoError(t, DB.Create(user).Error)
	token, _, err := CreateEmailVerificationFlow("target@example.com")
	require.NoError(t, err)
	actionErr := errors.New("protected mutation failed")

	err = ConsumeEmailVerificationFlowWithAction(token, "target@example.com", func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("display_name", "must-roll-back").Error; err != nil {
			return err
		}
		return actionErr
	})
	assert.ErrorIs(t, err, actionErr)

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Empty(t, stored.DisplayName)
	require.NoError(t, ValidateEmailVerificationFlow(token, "target@example.com"))
}

func TestEmailVerificationFlowConsumesWithMutationAndRejectsReplay(t *testing.T) {
	truncateTables(t)
	user := &User{
		Username: "verification-action-success",
		Password: "password-hash",
		Status:   common.UserStatusEnabled,
		AffCode:  "verify-action-success",
	}
	require.NoError(t, DB.Create(user).Error)
	token, _, err := CreateEmailVerificationFlow("target@example.com")
	require.NoError(t, err)

	err = ConsumeEmailVerificationFlowWithAction(token, "target@example.com", func(tx *gorm.DB) error {
		return tx.Model(&User{}).Where("id = ?", user.Id).Update("display_name", "committed").Error
	})
	require.NoError(t, err)

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, "committed", stored.DisplayName)
	err = ConsumeEmailVerificationFlowWithAction(token, "target@example.com", nil)
	assert.ErrorIs(t, err, ErrAuthFlowConsumed)
}

func TestConcurrentEmailVerificationFlowHasOneCommittedAction(t *testing.T) {
	truncateTables(t)
	user := &User{
		Username: "verification-action-concurrent",
		Password: "password-hash",
		Status:   common.UserStatusEnabled,
		AffCode:  "verify-action-concurrent",
	}
	require.NoError(t, DB.Create(user).Error)
	token, _, err := CreateEmailVerificationFlow("target@example.com")
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- ConsumeEmailVerificationFlowWithAction(token, "target@example.com", func(tx *gorm.DB) error {
				return tx.Model(&User{}).Where("id = ?", user.Id).
					Update("request_count", gorm.Expr("request_count + 1")).Error
			})
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	consumed := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAuthFlowConsumed):
			consumed++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, consumed)

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, 1, stored.RequestCount)
}

func TestDeleteAuthFlowRemovesOnlyExactCredential(t *testing.T) {
	truncateTables(t)
	firstToken, _, err := CreateEmailVerificationFlow("first@example.com")
	require.NoError(t, err)
	secondToken, _, err := CreateEmailVerificationFlow("second@example.com")
	require.NoError(t, err)

	require.NoError(t, DeleteAuthFlow(firstToken, AuthFlowPurposePasswordReset))
	require.NoError(t, ValidateEmailVerificationFlow(firstToken, "first@example.com"))
	require.NoError(t, DeleteAuthFlow(firstToken, AuthFlowPurposeEmailVerification))
	err = ValidateEmailVerificationFlow(firstToken, "first@example.com")
	assert.ErrorIs(t, err, ErrAuthFlowInvalid)
	require.NoError(t, ValidateEmailVerificationFlow(secondToken, "second@example.com"))
}

func TestPasswordResetFlowPersistsOnlyUserBoundDigests(t *testing.T) {
	truncateTables(t)
	user := createVerificationFlowTestUser(t, "persisted-reset")

	before := time.Now()
	token, created, err := CreatePasswordResetFlow(user)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	var stored AuthFlow
	require.NoError(t, DB.First(&stored, created.Id).Error)
	assert.Equal(t, user.Id, stored.UserId)
	assert.Equal(t, AuthFlowPurposePasswordReset, stored.Purpose)
	assert.NotEqual(t, token, stored.TokenHash)
	assert.NotContains(t, stored.Payload, token)
	assert.NotContains(t, strings.ToLower(stored.Payload), user.Email)
	assert.GreaterOrEqual(t, stored.ExpiresAt, before.Add(VerificationFlowTTL-time.Second))
	assert.LessOrEqual(t, stored.ExpiresAt, time.Now().Add(VerificationFlowTTL))
}

func TestPasswordResetFlowRejectsStaleUserSnapshotBeforeCreation(t *testing.T) {
	truncateTables(t)
	stale := createVerificationFlowTestUser(t, "stale-issuance")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", stale.Id).Updates(map[string]any{
		"email":        "moved@example.com",
		"auth_version": stale.AuthVersion + 1,
	}).Error)

	token, flow, err := CreatePasswordResetFlow(stale)

	assert.ErrorIs(t, err, ErrAuthFlowInvalid)
	assert.Empty(t, token)
	assert.Nil(t, flow)
	var count int64
	require.NoError(t, DB.Model(&AuthFlow{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestPasswordResetFlowForEmailBindsNormalizedRecipientAndLockedEpoch(t *testing.T) {
	truncateTables(t)
	user := createVerificationFlowTestUser(t, "email-issuance")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"email":        "Locked.Recipient@Example.COM",
		"auth_version": int64(7),
	}).Error)

	token, recipient, flow, err := CreatePasswordResetFlowForEmail("  LOCKED.recipient@example.com ")

	require.NoError(t, err)
	require.NotEmpty(t, token)
	assert.Equal(t, "locked.recipient@example.com", recipient)
	require.NotNil(t, flow)
	assert.Equal(t, user.Id, flow.UserId)
	binding, err := decodePasswordResetPayload(flow.Payload)
	require.NoError(t, err)
	assert.Equal(t, int64(7), binding.AuthVersion)
	assert.Equal(t, passwordResetEmailHash(recipient), binding.EmailHash)
}

func TestPasswordResetFlowEnforcesPasswordLengthBeforeConsumption(t *testing.T) {
	for _, test := range []struct {
		name     string
		password string
		valid    bool
	}{
		{name: "seven rejected", password: "1234567"},
		{name: "eight accepted", password: "12345678", valid: true},
		{name: "eight including leading space accepted exactly", password: " abcdefg", valid: true},
		{name: "eight unicode characters accepted", password: strings.Repeat("密", 8), valid: true},
		{name: "twenty accepted", password: strings.Repeat("x", 20), valid: true},
		{name: "twenty multibyte characters accepted", password: strings.Repeat("密", 20), valid: true},
		{name: "eighteen four byte runes accepted", password: strings.Repeat("😀", 18), valid: true},
		{name: "nineteen four byte runes rejected", password: strings.Repeat("😀", 19)},
		{name: "twenty four byte runes rejected", password: strings.Repeat("😀", 20)},
		{name: "twenty one rejected", password: strings.Repeat("x", 21)},
	} {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			user := createVerificationFlowTestUser(t, strings.ReplaceAll(test.name, " ", "-"))
			token, _, err := CreatePasswordResetFlow(user)
			require.NoError(t, err)

			err = ResetUserPasswordWithFlow(token, test.password)
			if !test.valid {
				assert.ErrorIs(t, err, ErrAuthFlowInvalid)
				flow, getErr := GetAuthFlow(token, AuthFlowMatch{Purpose: AuthFlowPurposePasswordReset})
				require.NoError(t, getErr)
				assert.Nil(t, flow.ConsumedAt)
				var stored User
				require.NoError(t, DB.First(&stored, user.Id).Error)
				assert.Equal(t, "unchanged-password-hash", stored.Password)
				return
			}

			require.NoError(t, err)
			var stored User
			require.NoError(t, DB.First(&stored, user.Id).Error)
			assert.True(t, common.ValidatePasswordAndHash(test.password, stored.Password))
			assert.Equal(t, int64(2), stored.AuthVersion)
		})
	}
}

func TestPasswordResetFlowRejectsInvalidBindingsWithoutChangingPassword(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(t *testing.T, user *User, token string, flow *AuthFlow) string
		wantErr error
	}{
		{
			name: "wrong token",
			mutate: func(_ *testing.T, _ *User, _ string, _ *AuthFlow) string {
				return "not-the-issued-token"
			},
			wantErr: ErrAuthFlowInvalid,
		},
		{
			name: "expired",
			mutate: func(t *testing.T, _ *User, token string, flow *AuthFlow) string {
				require.NoError(t, DB.Model(&AuthFlow{}).Where("id = ?", flow.Id).
					Update("expires_at", time.Now().Add(-time.Second)).Error)
				return token
			},
			wantErr: ErrAuthFlowExpired,
		},
		{
			name: "consumed",
			mutate: func(t *testing.T, _ *User, token string, _ *AuthFlow) string {
				_, err := ConsumeAuthFlow(token, AuthFlowMatch{Purpose: AuthFlowPurposePasswordReset})
				require.NoError(t, err)
				return token
			},
			wantErr: ErrAuthFlowConsumed,
		},
		{
			name: "stale auth version",
			mutate: func(t *testing.T, user *User, token string, _ *AuthFlow) string {
				require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
					Update("auth_version", user.AuthVersion+1).Error)
				return token
			},
			wantErr: ErrAuthFlowInvalid,
		},
		{
			name: "stale email",
			mutate: func(t *testing.T, user *User, token string, _ *AuthFlow) string {
				require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
					Update("email", "changed@example.com").Error)
				return token
			},
			wantErr: ErrAuthFlowInvalid,
		},
		{
			name: "deleted user",
			mutate: func(t *testing.T, user *User, token string, _ *AuthFlow) string {
				require.NoError(t, DB.Delete(user).Error)
				return token
			},
			wantErr: ErrAuthFlowInvalid,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			user := createVerificationFlowTestUser(t, strings.ReplaceAll(test.name, " ", "-"))
			token, flow, err := CreatePasswordResetFlow(user)
			require.NoError(t, err)
			token = test.mutate(t, user, token, flow)

			err = ResetUserPasswordWithFlow(token, "NewPassword123")
			assert.ErrorIs(t, err, test.wantErr)

			var stored User
			queryErr := DB.Unscoped().First(&stored, user.Id).Error
			require.NoError(t, queryErr)
			assert.Equal(t, "unchanged-password-hash", stored.Password)
			if !errors.Is(test.wantErr, ErrAuthFlowConsumed) {
				var storedFlow AuthFlow
				require.NoError(t, DB.First(&storedFlow, flow.Id).Error)
				assert.Nil(t, storedFlow.ConsumedAt)
			}
		})
	}
}

func TestPasswordResetFlowRejectsMalformedAndStalePayloads(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "unknown version", payload: `{"version":2,"auth_version":1,"email_hash":"present"}`},
		{name: "missing auth version", payload: `{"version":1,"auth_version":0,"email_hash":"present"}`},
		{name: "missing email hash", payload: `{"version":1,"auth_version":1,"email_hash":""}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			user := createVerificationFlowTestUser(t, "malformed-"+strings.ReplaceAll(test.name, " ", "-"))
			token, flow, err := CreatePasswordResetFlow(user)
			require.NoError(t, err)
			require.NoError(t, DB.Model(&AuthFlow{}).Where("id = ?", flow.Id).
				Update("payload", test.payload).Error)

			err = ResetUserPasswordWithFlow(token, "NewPassword123")
			assert.ErrorIs(t, err, ErrAuthFlowInvalid)
			peeked, getErr := GetAuthFlow(token, AuthFlowMatch{Purpose: AuthFlowPurposePasswordReset})
			require.NoError(t, getErr)
			assert.Nil(t, peeked.ConsumedAt)
		})
	}
}

func TestConcurrentPasswordResetFlowHasOneCommittedPassword(t *testing.T) {
	truncateTables(t)
	user := createVerificationFlowTestUser(t, "concurrent-reset")
	token, _, err := CreatePasswordResetFlow(user)
	require.NoError(t, err)
	passwords := []string{"FirstPassword123", "SecondPassword12"}

	start := make(chan struct{})
	results := make(chan error, len(passwords))
	var workers sync.WaitGroup
	for _, password := range passwords {
		password := password
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- ResetUserPasswordWithFlow(token, password)
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	consumed := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAuthFlowConsumed):
			consumed++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, consumed)

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, int64(2), stored.AuthVersion)
	firstWon := common.ValidatePasswordAndHash(passwords[0], stored.Password)
	secondWon := common.ValidatePasswordAndHash(passwords[1], stored.Password)
	assert.NotEqual(t, firstWon, secondWon)
}
