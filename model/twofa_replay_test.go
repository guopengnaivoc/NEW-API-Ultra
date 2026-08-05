package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

func prepareTwoFAReplayTest(t *testing.T) *TwoFA {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&TwoFA{}))
	require.NoError(t, DB.Exec("DELETE FROM two_fas").Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM two_fas")
	})

	key, err := common.GenerateTOTPSecret("replay-test-user")
	require.NoError(t, err)

	twoFA := &TwoFA{
		UserId:    9001,
		Secret:    key.Secret(),
		IsEnabled: true,
	}
	require.NoError(t, DB.Create(twoFA).Error)
	return twoFA
}

// A valid TOTP code must be accepted once and rejected when replayed
// within the same time step (NA-ISSUE-0105).
func TestValidateTOTPRejectsSameStepReplay(t *testing.T) {
	twoFA := prepareTwoFAReplayTest(t)

	code, err := totp.GenerateCode(twoFA.Secret, time.Now())
	require.NoError(t, err)

	ok, err := twoFA.ValidateTOTPAndUpdateUsage(code)
	require.NoError(t, err)
	require.True(t, ok, "first use of a fresh code must succeed")

	ok, err = twoFA.ValidateTOTPAndUpdateUsage(code)
	require.NoError(t, err)
	require.False(t, ok, "replaying the same code in the same step must fail")
}

func TestValidateTOTPStillRejectsWrongCode(t *testing.T) {
	twoFA := prepareTwoFAReplayTest(t)

	ok, err := twoFA.ValidateTOTPAndUpdateUsage("000000")
	require.NoError(t, err)
	require.False(t, ok)

	var stored TwoFA
	require.NoError(t, DB.Where("id = ?", twoFA.Id).First(&stored).Error)
	require.Equal(t, 1, stored.FailedAttempts)
}

func TestMatchTOTPCounterAcceptsAdjacentStep(t *testing.T) {
	key, err := common.GenerateTOTPSecret("skew-test-user")
	require.NoError(t, err)

	now := time.Now()
	prev := now.Add(-30 * time.Second)
	code, err := totp.GenerateCode(key.Secret(), prev)
	require.NoError(t, err)

	counter, ok := common.MatchTOTPCounter(key.Secret(), code, now)
	require.True(t, ok, "codes from the previous step must stay valid within skew")
	require.Equal(t, prev.Unix()/30, counter)
}
