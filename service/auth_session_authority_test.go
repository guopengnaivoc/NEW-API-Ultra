package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateLoginSessionRejectsRemoteRevocationWithoutCacheExpiry(
	t *testing.T,
) {
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	_, clientA, _, clientB := useIndependentAuthSessionRedis(t)

	common.RDB = clientA
	bundle, err := CreateLoginSession(
		user.Id,
		"password",
		"127.0.0.1",
		"node-a",
	)
	require.NoError(t, err)
	identity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)

	common.RDB = clientB
	_, err = model.GetUserSessionCached(identity.SessionID)
	require.NoError(t, err, "node B must prime its independent cache")

	common.RDB = clientA
	require.NoError(
		t,
		RevokeByRefreshToken(
			bundle.RefreshToken,
			bundle.Session.SID,
			"logout",
		),
	)

	common.RDB = clientB
	_, _, err = ValidateLoginSession(identity)
	assert.ErrorIs(t, err, ErrLoginSessionRevoked)
}

func TestValidateLoginSessionUsesRemoteAuthVersionWithoutCacheExpiry(
	t *testing.T,
) {
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	_, clientA, _, clientB := useIndependentAuthSessionRedis(t)

	common.RDB = clientA
	bundle, err := CreateLoginSession(
		user.Id,
		"password",
		"127.0.0.1",
		"node-a",
	)
	require.NoError(t, err)
	oldIdentity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)

	common.RDB = clientB
	_, err = model.GetUserSessionCached(oldIdentity.SessionID)
	require.NoError(t, err, "node B must prime its independent cache")

	common.RDB = clientA
	rotated, err := AdvanceCurrentSessionSecurity(
		oldIdentity,
		"security_update",
	)
	require.NoError(t, err)
	newIdentity, err := ParseAccessToken(rotated.AccessToken)
	require.NoError(t, err)
	require.Greater(t, newIdentity.SessionVersion, oldIdentity.SessionVersion)
	require.Greater(
		t,
		newIdentity.UserAuthVersion,
		oldIdentity.UserAuthVersion,
	)

	common.RDB = clientB
	_, authoritativeUser, err := ValidateLoginSession(newIdentity)
	require.NoError(t, err)
	assert.Equal(t, newIdentity.UserAuthVersion, authoritativeUser.AuthVersion)

	_, _, err = ValidateLoginSession(oldIdentity)
	assert.ErrorIs(t, err, ErrLoginSessionRevoked)
}

func TestValidateLoginSessionRejectsRemoteUserAuthVersionWithoutCacheExpiry(
	t *testing.T,
) {
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	_, clientA, _, clientB := useIndependentAuthSessionRedis(t)

	common.RDB = clientA
	bundle, err := CreateLoginSession(
		user.Id,
		"password",
		"127.0.0.1",
		"node-a",
	)
	require.NoError(t, err)
	identity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)

	common.RDB = clientB
	cachedSession, err := model.GetUserSessionCached(identity.SessionID)
	require.NoError(t, err, "node B must prime its independent session cache")
	require.Equal(t, identity.UserAuthVersion, cachedSession.UserAuthVersion)
	cachedUser, err := model.GetUserCache(user.Id)
	require.NoError(t, err, "node B must prime its independent user cache")
	require.Equal(t, identity.UserAuthVersion, cachedUser.AuthVersion)

	common.RDB = clientA
	nextAuthVersion, err := model.BumpUserAuthVersion(user.Id)
	require.NoError(t, err)
	require.Greater(t, nextAuthVersion, identity.UserAuthVersion)
	unchangedSession, err := model.GetUserSessionBySID(identity.SessionID)
	require.NoError(t, err)
	require.Equal(
		t,
		identity.UserAuthVersion,
		unchangedSession.UserAuthVersion,
		"the session row must remain unchanged so this test isolates user-row authority",
	)

	common.RDB = clientB
	staleUser, err := model.GetUserCache(user.Id)
	require.NoError(t, err)
	require.Equal(
		t,
		identity.UserAuthVersion,
		staleUser.AuthVersion,
		"node B must still hold the pre-update user snapshot",
	)
	_, _, err = ValidateLoginSession(identity)
	assert.ErrorIs(t, err, ErrLoginSessionRevoked)
}

func TestValidateSessionReferenceUsesRemoteSessionVersionWithoutCacheExpiry(
	t *testing.T,
) {
	useTestSessionSecret(t)
	user := setupAuthSessionTestDB(t)
	_, clientA, _, clientB := useIndependentAuthSessionRedis(t)

	common.RDB = clientA
	bundle, err := CreateLoginSession(
		user.Id,
		"password",
		"127.0.0.1",
		"node-a",
	)
	require.NoError(t, err)
	oldIdentity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)

	common.RDB = clientB
	_, err = model.GetUserSessionCached(oldIdentity.SessionID)
	require.NoError(t, err, "node B must prime its independent cache")

	common.RDB = clientA
	rotated, err := AdvanceCurrentSessionSecurity(
		oldIdentity,
		"security_update",
	)
	require.NoError(t, err)
	newIdentity, err := ParseAccessToken(rotated.AccessToken)
	require.NoError(t, err)

	common.RDB = clientB
	reference, err := ValidateSessionReference(user.Id, bundle.Session.SID)
	require.NoError(t, err)
	assert.Equal(t, newIdentity, reference)
}
