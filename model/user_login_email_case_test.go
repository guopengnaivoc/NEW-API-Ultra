package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Registration stores the address lowercased, so a login attempt must match
// the stored address case-insensitively. PostgreSQL and SQLite compare `=` on
// text case-sensitively, so a plain equality lookup made a legitimate mixed
// case address fail to log in on those two databases while succeeding on
// MySQL's default case-insensitive collation.
func TestValidateAndFillMatchesStoredEmailCaseInsensitively(t *testing.T) {
	setupUserUpdateTestState(t)

	hashed, err := common.Password2Hash("CorrectHorse123")
	require.NoError(t, err)
	require.NoError(t, DB.Create(&User{
		Username: "case-user",
		Password: hashed,
		Email:    "mixed.case@example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	for _, supplied := range []string{
		"mixed.case@example.com",
		"Mixed.Case@Example.com",
		"MIXED.CASE@EXAMPLE.COM",
		"  Mixed.Case@Example.com  ",
	} {
		login := User{Username: supplied, Password: "CorrectHorse123"}
		require.NoErrorf(t, login.ValidateAndFill(), "login with %q", supplied)
		assert.Equal(t, "case-user", login.Username)
	}
}

// Usernames are stored verbatim and are a distinct namespace from email, so
// the case-insensitive relaxation must not leak into username matching.
func TestValidateAndFillKeepsUsernameMatchingCaseSensitive(t *testing.T) {
	setupUserUpdateTestState(t)

	hashed, err := common.Password2Hash("CorrectHorse123")
	require.NoError(t, err)
	require.NoError(t, DB.Create(&User{
		Username: "CaseUser",
		Password: hashed,
		Status:   common.UserStatusEnabled,
	}).Error)

	login := User{Username: "caseuser", Password: "CorrectHorse123"}
	require.ErrorIs(t, login.ValidateAndFill(), ErrInvalidCredentials)
}

// FillUserByEmail is the recovery-side lookup and must agree with the
// normalized address that registration wrote.
func TestFillUserByEmailMatchesStoredEmailCaseInsensitively(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "recovery-user",
		Email:    "recover.me@example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	lookup := User{Email: "Recover.Me@Example.COM"}
	require.NoError(t, lookup.FillUserByEmail())
	assert.Equal(t, "recovery-user", lookup.Username)
}
