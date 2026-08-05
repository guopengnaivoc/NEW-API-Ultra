package service

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useTestSessionSecret(t *testing.T) {
	t.Helper()
	previous := common.SessionSecret
	common.SessionSecret = "test-session-secret-with-sufficient-entropy"
	t.Cleanup(func() { common.SessionSecret = previous })
}

func TestCanonicalSecurityProofIDTarget(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "canonical", raw: "17", want: "17"},
		{name: "empty", raw: "", wantErr: true},
		{name: "zero", raw: "0", wantErr: true},
		{name: "negative", raw: "-17", wantErr: true},
		{name: "explicit plus", raw: "+17", wantErr: true},
		{name: "leading zero", raw: "017", wantErr: true},
		{name: "leading whitespace", raw: " 17", wantErr: true},
		{name: "trailing whitespace", raw: "17 ", wantErr: true},
		{name: "nonnumeric", raw: "channel-17", wantErr: true},
		{name: "overflow", raw: strings.Repeat("9", 1024), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CanonicalSecurityProofIDTarget(test.raw)
			if test.wantErr {
				assert.ErrorIs(t, err, ErrProofTarget)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestAccessTokenRoundTrip(t *testing.T) {
	useTestSessionSecret(t)
	identity := AuthIdentity{UserID: 42, SessionID: "session-1", UserAuthVersion: 3, SessionVersion: 2}

	token, expiresAt, err := IssueAccessToken(identity)
	require.NoError(t, err)
	assert.Positive(t, expiresAt)

	parsed, err := ParseAccessToken(token)
	require.NoError(t, err)
	assert.Equal(t, identity, parsed)
}

func TestAccessTokenRejectsTampering(t *testing.T) {
	useTestSessionSecret(t)
	identity := AuthIdentity{UserID: 42, SessionID: "session-1", UserAuthVersion: 1, SessionVersion: 1}
	token, _, err := IssueAccessToken(identity)
	require.NoError(t, err)

	tamperAt := len(token) - 2
	replacement := "x"
	if token[tamperAt] == 'x' {
		replacement = "y"
	}
	tampered := token[:tamperAt] + replacement + token[tamperAt+1:]
	_, err = ParseAccessToken(tampered)
	assert.ErrorIs(t, err, ErrAuthTokenInvalid)

	_, internal, err := ParseDashboardAccessToken(tampered)
	assert.True(t, internal)
	assert.ErrorIs(t, err, ErrAuthTokenInvalid)
}

func TestDashboardAccessTokenClassification(t *testing.T) {
	useTestSessionSecret(t)

	identity, internal, err := ParseDashboardAccessToken("opaque.key.with-dots")
	require.NoError(t, err)
	assert.False(t, internal)
	assert.Empty(t, identity)

	external := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "external-issuer",
		"aud": authTokenAudience,
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	externalRaw, err := external.SignedString([]byte("external-secret"))
	require.NoError(t, err)
	_, internal, err = ParseDashboardAccessToken(externalRaw)
	require.NoError(t, err)
	assert.False(t, internal)

	unknownUse := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":       authTokenIssuer,
		"aud":       authTokenAudience,
		"token_use": "third_party",
		"exp":       time.Now().Add(time.Minute).Unix(),
	})
	unknownUseRaw, err := unknownUse.SignedString([]byte("external-secret"))
	require.NoError(t, err)
	_, internal, err = ParseDashboardAccessToken(unknownUseRaw)
	require.NoError(t, err)
	assert.False(t, internal)

	legacyProof := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":       authTokenIssuer,
		"aud":       authTokenAudience,
		"token_use": securityProofTokenUse,
	})
	proof, err := legacyProof.SignedString(authSigningKey(securityProofTokenUse))
	require.NoError(t, err)
	_, internal, err = ParseDashboardAccessToken(proof)
	assert.True(t, internal)
	assert.ErrorIs(t, err, ErrAuthTokenInvalid)

	expiredClaims := authClaims{
		TokenUse:        accessTokenUse,
		SessionID:       "expired-session",
		UserAuthVersion: 1,
		SessionVersion:  1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    authTokenIssuer,
			Subject:   "42",
			Audience:  jwt.ClaimStrings{authTokenAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			NotBefore: jwt.NewNumericDate(time.Now().Add(-2 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Minute)),
			ID:        "expired-token",
		},
	}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredClaims).SignedString(authSigningKey(accessTokenUse))
	require.NoError(t, err)
	_, internal, err = ParseDashboardAccessToken(expired)
	assert.True(t, internal)
	assert.ErrorIs(t, err, ErrAuthTokenExpired)
}

func TestOneTimeSecurityProofBindsIdentityScopeTargetAndConsumption(t *testing.T) {
	useTestSessionSecret(t)
	require.NoError(t, model.DB.Exec("DELETE FROM auth_flows").Error)

	identity := AuthIdentity{
		UserID:          42,
		SessionID:       "session-1",
		UserAuthVersion: 3,
		SessionVersion:  2,
	}
	proof, expiresAt, err := IssueOneTimeSecurityProof(
		identity,
		"password",
		"email.change",
		"next@example.com",
	)
	require.NoError(t, err)
	assert.Positive(t, expiresAt)

	_, err = ConsumeOneTimeSecurityProof(
		proof,
		identity,
		"email.change",
		"other@example.com",
		[]string{"password", "2fa", "passkey"},
	)
	assert.ErrorIs(t, err, ErrProofTarget)

	method, err := ConsumeOneTimeSecurityProof(
		proof,
		identity,
		"email.change",
		"next@example.com",
		[]string{"password", "2fa", "passkey"},
	)
	require.NoError(t, err)
	assert.Equal(t, "password", method)

	_, err = ConsumeOneTimeSecurityProof(
		proof,
		identity,
		"email.change",
		"next@example.com",
		[]string{"password", "2fa", "passkey"},
	)
	assert.ErrorIs(t, err, ErrProofConsumed)
}

func TestOneTimeSecurityProofPreservesExactTargetBytes(t *testing.T) {
	useTestSessionSecret(t)
	require.NoError(t, model.DB.Exec("DELETE FROM auth_flows").Error)

	identity := AuthIdentity{
		UserID:          42,
		SessionID:       "session-exact-target",
		UserAuthVersion: 3,
		SessionVersion:  2,
	}
	proof, _, err := IssueOneTimeSecurityProof(
		identity,
		"2fa",
		"external.bind",
		" github ",
	)
	require.NoError(t, err)

	_, err = ConsumeOneTimeSecurityProof(
		proof,
		identity,
		"external.bind",
		"github",
		[]string{"2fa"},
	)
	assert.ErrorIs(t, err, ErrProofTarget)

	method, err := ConsumeOneTimeSecurityProof(
		proof,
		identity,
		"external.bind",
		" github ",
		[]string{"2fa"},
	)
	require.NoError(t, err)
	assert.Equal(t, "2fa", method)
}

func TestOneTimeSecurityProofRejectsChangedSessionState(t *testing.T) {
	useTestSessionSecret(t)
	require.NoError(t, model.DB.Exec("DELETE FROM auth_flows").Error)

	identity := AuthIdentity{
		UserID:          42,
		SessionID:       "session-1",
		UserAuthVersion: 3,
		SessionVersion:  2,
	}
	proof, _, err := IssueOneTimeSecurityProof(
		identity,
		"2fa",
		"external.bind",
		"github",
	)
	require.NoError(t, err)

	changed := identity
	changed.SessionVersion++
	_, err = ConsumeOneTimeSecurityProof(
		proof,
		changed,
		"external.bind",
		"github",
		[]string{"2fa"},
	)
	assert.ErrorIs(t, err, ErrAuthTokenInvalid)

	_, err = ConsumeOneTimeSecurityProof(
		proof,
		identity,
		"external.bind",
		"github",
		[]string{"passkey"},
	)
	assert.ErrorIs(t, err, ErrProofMethod)

	_, err = ConsumeOneTimeSecurityProof(
		proof,
		identity,
		"external.unbind",
		"github",
		[]string{"2fa"},
	)
	assert.ErrorIs(t, err, ErrProofScope)

	_, err = ConsumeOneTimeSecurityProof(
		proof,
		identity,
		"external.bind",
		"github",
		[]string{"2fa"},
	)
	require.NoError(t, err)
}

func TestOneTimeSecurityProofConcurrentConsumptionHasOneWinner(t *testing.T) {
	useTestSessionSecret(t)
	require.NoError(t, model.DB.Exec("DELETE FROM auth_flows").Error)

	identity := AuthIdentity{
		UserID:          42,
		SessionID:       "session-1",
		UserAuthVersion: 3,
		SessionVersion:  2,
	}
	proof, _, err := IssueOneTimeSecurityProof(
		identity,
		"passkey",
		"passkey.delete",
		"42",
	)
	require.NoError(t, err)

	const consumers = 8
	results := make(chan error, consumers)
	var group sync.WaitGroup
	for range consumers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, consumeErr := ConsumeOneTimeSecurityProof(
				proof,
				identity,
				"passkey.delete",
				"42",
				[]string{"passkey"},
			)
			results <- consumeErr
		}()
	}
	group.Wait()
	close(results)

	successes := 0
	for consumeErr := range results {
		if consumeErr == nil {
			successes++
			continue
		}
		assert.ErrorIs(t, consumeErr, ErrProofConsumed)
	}
	assert.Equal(t, 1, successes)
}
