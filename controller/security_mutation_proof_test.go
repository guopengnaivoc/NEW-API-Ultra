package controller

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	passkeysvc "github.com/QuantumNous/new-api/service/passkey"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These values come from go-webauthn v0.14.0's public assertion fixture.
// They are deterministic test material and contain no private key or live credential.
const (
	passkeyAssertionCredentialID = "AI7D5q2P0LS-Fal9ZT7CHM2N5BLbUunF92T8b6iYC199bO2kagSuU05-5dZGqb1SP0A0lyTWng"
	passkeyAssertionPublicKey    = "pQMmIAEhWCAoCF-x0dwEhzQo-ABxHIAgr_5WL6cJceREc81oIwFn7iJYIHEHx8ZhBIE42L26-rSC_3l0ZaWEmsHAKyP9rgslApUdAQI"
	passkeyAssertionAAGUID       = "rc4AAjW8xgpkiwsl8fBVAw"
	passkeyAssertionChallenge    = "E4PTcIH_HfX1pC6Sigk1SC9NAlgeztN0439vi8z_c9k"
	passkeyAssertionCredential   = `{
		"id":"AI7D5q2P0LS-Fal9ZT7CHM2N5BLbUunF92T8b6iYC199bO2kagSuU05-5dZGqb1SP0A0lyTWng",
		"rawId":"AI7D5q2P0LS-Fal9ZT7CHM2N5BLbUunF92T8b6iYC199bO2kagSuU05-5dZGqb1SP0A0lyTWng",
		"type":"public-key",
		"response":{
			"authenticatorData":"dKbqkhPJnC90siSSsyDPQCYqlMGpUKA5fyklC2CEHvBFXJJiGa3OAAI1vMYKZIsLJfHwVQMANwCOw-atj9C0vhWpfWU-whzNjeQS21Lpxfdk_G-omAtffWztpGoErlNOfuXWRqm9Uj9ANJck1p6lAQIDJiABIVggKAhfsdHcBIc0KPgAcRyAIK_-Vi-nCXHkRHPNaCMBZ-4iWCBxB8fGYQSBONi9uvq0gv95dGWlhJrBwCsj_a4LJQKVHQ",
			"clientDataJSON":"eyJjaGFsbGVuZ2UiOiJFNFBUY0lIX0hmWDFwQzZTaWdrMVNDOU5BbGdlenROMDQzOXZpOHpfYzlrIiwibmV3X2tleXNfbWF5X2JlX2FkZGVkX2hlcmUiOiJkbyBub3QgY29tcGFyZSBjbGllbnREYXRhSlNPTiBhZ2FpbnN0IGEgdGVtcGxhdGUuIFNlZSBodHRwczovL2dvby5nbC95YWJQZXgiLCJvcmlnaW4iOiJodHRwczovL3dlYmF1dGhuLmlvIiwidHlwZSI6IndlYmF1dGhuLmdldCJ9",
			"signature":"MEUCIBtIVOQxzFYdyWQyxaLR0tik1TnuPhGVhXVSNgFwLmN5AiEAnxXdCq0UeAVGWxOaFcjBZ_mEZoXqNboY5IkQDdlWZYc"
		}
	}`
)

func setupSecurityMutationProofTest(t *testing.T) (*model.User, service.AuthIdentity) {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousType := common.MainDatabaseType()
	previousSecret := common.SessionSecret
	previousRedis := common.RedisEnabled
	previousLogConsume := common.LogConsumeEnabled
	previousIdleTimeout := common.UserSessionIdleTimeoutSeconds
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.DashboardAccessToken{},
		&model.UserSession{},
		&model.AuthFlow{},
		&model.TwoFA{},
		&model.TwoFABackupCode{},
		&model.PasskeyCredential{},
		&model.UserOAuthBinding{},
		&model.ExternalIdentityClaim{},
		&model.Log{},
	))
	model.DB = db
	model.LOG_DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SessionSecret = "security-mutation-proof-test-secret"
	common.RedisEnabled = false
	common.LogConsumeEnabled = false
	common.UserSessionIdleTimeoutSeconds = int64(common.DefaultUserSessionIdleTimeoutSeconds)

	passwordHash, err := common.Password2Hash("CurrentPassword123")
	require.NoError(t, err)
	user := &model.User{
		Username:    "security-proof-user",
		Password:    passwordHash,
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     "security-proof-aff",
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	now := time.Now().Unix()
	session := &model.UserSession{
		SID:             "security-proof-session",
		UserID:          user.Id,
		Version:         2,
		UserAuthVersion: user.AuthVersion,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     "security-proof-refresh",
		CreatedAt:       now,
		LastActiveAt:    now,
		ExpiresAt:       4102444800,
	}
	require.NoError(t, db.Create(session).Error)

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetMainDatabaseType(previousType)
		common.SessionSecret = previousSecret
		common.RedisEnabled = previousRedis
		common.LogConsumeEnabled = previousLogConsume
		common.UserSessionIdleTimeoutSeconds = previousIdleTimeout
	})
	return user, service.AuthIdentity{
		UserID:          user.Id,
		SessionID:       session.SID,
		UserAuthVersion: session.UserAuthVersion,
		SessionVersion:  session.Version,
	}
}

func securityMutationProofContext(
	body string,
	identity service.AuthIdentity,
) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/verify",
		strings.NewReader(body),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("id", identity.UserID)
	context.Set("session_id", identity.SessionID)
	context.Set("auth_version", identity.UserAuthVersion)
	context.Set("session_version", identity.SessionVersion)
	return context, recorder
}

func enablePasskeyVerificationForTest(t *testing.T) {
	t.Helper()
	previous, err := config.ConfigToMap(
		config.Snapshot[system_setting.PasskeySettings]("passkey"),
	)
	require.NoError(t, err)
	updated, err := config.GlobalConfig.Update("passkey", map[string]string{
		"enabled":               "true",
		"rp_display_name":       "Passkey proof test",
		"rp_id":                 "webauthn.io",
		"origins":               "https://webauthn.io",
		"allow_insecure_origin": "false",
		"user_verification":     "preferred",
	})
	require.NoError(t, err)
	require.True(t, updated)
	t.Cleanup(func() {
		restored, restoreErr := config.GlobalConfig.Update("passkey", previous)
		require.NoError(t, restoreErr)
		require.True(t, restored)
	})
}

func createPasskeyAssertionFixture(t *testing.T, userID int) []byte {
	t.Helper()
	credentialID, err := base64.RawURLEncoding.DecodeString(
		passkeyAssertionCredentialID,
	)
	require.NoError(t, err)
	publicKey, err := base64.RawURLEncoding.DecodeString(
		passkeyAssertionPublicKey,
	)
	require.NoError(t, err)
	aaguid, err := base64.RawURLEncoding.DecodeString(passkeyAssertionAAGUID)
	require.NoError(t, err)
	require.NoError(t, model.DB.Create(&model.PasskeyCredential{
		UserID:          userID,
		CredentialID:    base64.StdEncoding.EncodeToString(credentialID),
		PublicKey:       base64.StdEncoding.EncodeToString(publicKey),
		AttestationType: "none",
		AAGUID:          base64.StdEncoding.EncodeToString(aaguid),
	}).Error)
	return credentialID
}

func passkeyVerificationContext(
	path string,
	body string,
	identity service.AuthIdentity,
) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"https://webauthn.io"+path,
		strings.NewReader(body),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("id", identity.UserID)
	context.Set("session_id", identity.SessionID)
	context.Set("auth_version", identity.UserAuthVersion)
	context.Set("session_version", identity.SessionVersion)
	return context, recorder
}

func TestUniversalVerifyPasswordIssuesTargetedOneTimeProof(t *testing.T) {
	_, identity := setupSecurityMutationProofTest(t)
	context, recorder := securityMutationProofContext(
		`{"method":"password","password":"CurrentPassword123","scope":"email.change","target":"next@example.com"}`,
		identity,
	)

	UniversalVerify(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			ProofToken string `json:"proof_token"`
			Method     string `json:"method"`
			Scope      string `json:"scope"`
			Target     string `json:"target"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotEmpty(t, response.Data.ProofToken)
	assert.Equal(t, "password", response.Data.Method)
	assert.Equal(t, "email.change", response.Data.Scope)
	assert.Equal(t, "next@example.com", response.Data.Target)

	method, err := service.ConsumeOneTimeSecurityProof(
		response.Data.ProofToken,
		identity,
		"email.change",
		"next@example.com",
		[]string{"password"},
	)
	require.NoError(t, err)
	assert.Equal(t, "password", method)
}

func TestUniversalVerifyPasswordRejectsWrongPasswordWithoutProof(t *testing.T) {
	_, identity := setupSecurityMutationProofTest(t)
	context, recorder := securityMutationProofContext(
		`{"method":"password","password":"wrong-password","scope":"email.change","target":"next@example.com"}`,
		identity,
	)

	UniversalVerify(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	var count int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).
		Where("purpose = ?", model.AuthFlowPurposeSecurityProof).
		Count(&count).Error)
	assert.Zero(t, count)
}

func TestUniversalVerifyRejectsInvalidChannelKeyTargetsBeforeFactorValidation(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	require.NoError(t, model.DB.Create(&model.TwoFA{
		UserId: user.Id, Secret: "unused-secret", IsEnabled: true,
	}).Error)
	for index, target := range []string{"", "0", "-1", "+17", "017", " 17", "17 ", "abc"} {
		t.Run(strconv.Quote(target), func(t *testing.T) {
			backupCode := fmt.Sprintf("CODE-%04d", index)
			hash, err := common.HashBackupCode(backupCode)
			require.NoError(t, err)
			backup := &model.TwoFABackupCode{
				UserId: user.Id, CodeHash: hash,
			}
			require.NoError(t, model.DB.Create(backup).Error)
			body := fmt.Sprintf(
				`{"method":"2fa","code":%q,"scope":"channel.key.read","target":%q}`,
				backupCode,
				target,
			)
			context, recorder := securityMutationProofContext(body, identity)
			UniversalVerify(context)

			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
			require.NoError(t, model.DB.First(backup, backup.Id).Error)
			assert.False(t, backup.IsUsed)
			var count int64
			require.NoError(t, model.DB.Model(&model.AuthFlow{}).
				Where("purpose = ?", model.AuthFlowPurposeSecurityProof).
				Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestPasskeyVerifyBeginRejectsNonCanonicalChannelTargetWithoutFlow(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	enablePasskeyVerificationForTest(t)
	createPasskeyAssertionFixture(t, user.Id)
	context, recorder := passkeyVerificationContext(
		"/api/user/passkey/verify/begin",
		`{"scope":"channel.key.read","target":"017"}`,
		identity,
	)

	PasskeyVerifyBegin(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	var count int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).
		Where("purpose = ?", model.AuthFlowPurposePasskeyStepUp).
		Count(&count).Error)
	assert.Zero(t, count)
}

func TestPasskeyVerifyBeginPersistsCanonicalChannelTarget(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	enablePasskeyVerificationForTest(t)
	credentialID := createPasskeyAssertionFixture(t, user.Id)
	context, recorder := passkeyVerificationContext(
		"/api/user/passkey/verify/begin",
		`{"scope":"channel.key.read","target":"17"}`,
		identity,
	)

	PasskeyVerifyBegin(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, recorder.Body.String())
	require.NotEmpty(t, response.Data.FlowToken)
	sessionData, scope, target, err := passkeysvc.PopSessionDataFlow(
		response.Data.FlowToken,
		model.AuthFlowPurposePasskeyStepUp,
		user.Id,
		identity.SessionID,
	)
	require.NoError(t, err)
	assert.Equal(t, "channel.key.read", scope)
	assert.Equal(t, "17", target)
	assert.Equal(t, []byte(strconv.Itoa(user.Id)), sessionData.UserID)
	assert.Equal(t, [][]byte{credentialID}, sessionData.AllowedCredentialIDs)
}

func TestPasskeyVerifyFinishIssuesTargetedOneTimeChannelProof(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	enablePasskeyVerificationForTest(t)
	credentialID := createPasskeyAssertionFixture(t, user.Id)
	sessionData := &webauthnlib.SessionData{
		Challenge:            passkeyAssertionChallenge,
		RelyingPartyID:       "webauthn.io",
		UserID:               []byte(strconv.Itoa(user.Id)),
		AllowedCredentialIDs: [][]byte{credentialID},
		UserVerification:     protocol.VerificationPreferred,
	}
	flowToken, _, err := passkeysvc.CreateSessionDataFlow(
		model.AuthFlowPurposePasskeyStepUp,
		user.Id,
		identity.SessionID,
		"channel.key.read",
		"17",
		sessionData,
	)
	require.NoError(t, err)
	body := fmt.Sprintf(
		`{"flow_token":%q,"credential":%s}`,
		flowToken,
		passkeyAssertionCredential,
	)
	context, recorder := passkeyVerificationContext(
		"/api/user/passkey/verify/finish",
		body,
		identity,
	)

	PasskeyVerifyFinish(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			ProofToken string `json:"proof_token"`
			Method     string `json:"method"`
			Scope      string `json:"scope"`
			Target     string `json:"target"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, recorder.Body.String())
	assert.Equal(t, "passkey", response.Data.Method)
	assert.Equal(t, "channel.key.read", response.Data.Scope)
	assert.Equal(t, "17", response.Data.Target)
	assert.NotContains(t, response.Data.ProofToken, ".")
	credential, err := model.GetPasskeyByUserID(user.Id)
	require.NoError(t, err)
	assert.Equal(t, uint32(1553097241), credential.SignCount)

	method, err := service.ConsumeOneTimeSecurityProof(
		response.Data.ProofToken,
		identity,
		"channel.key.read",
		"17",
		[]string{"passkey"},
	)
	require.NoError(t, err)
	assert.Equal(t, "passkey", method)
	_, err = service.ConsumeOneTimeSecurityProof(
		response.Data.ProofToken,
		identity,
		"channel.key.read",
		"17",
		[]string{"passkey"},
	)
	assert.ErrorIs(t, err, service.ErrProofConsumed)
}

func TestUniversalVerifyIssuesOneTimeChannelKeyProofForCanonicalTarget(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	const backupCode = "ABCD-1234"
	hash, err := common.HashBackupCode(backupCode)
	require.NoError(t, err)
	require.NoError(t, model.DB.Create(&model.TwoFA{
		UserId: user.Id, Secret: "unused-secret", IsEnabled: true,
	}).Error)
	require.NoError(t, model.DB.Create(&model.TwoFABackupCode{
		UserId: user.Id, CodeHash: hash,
	}).Error)

	context, recorder := securityMutationProofContext(
		`{"method":"2fa","code":"ABCD-1234","scope":"channel.key.read","target":"17"}`,
		identity,
	)
	UniversalVerify(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			ProofToken string `json:"proof_token"`
			Target     string `json:"target"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.Equal(t, "17", response.Data.Target)
	assert.NotContains(t, response.Data.ProofToken, ".")

	method, err := service.ConsumeOneTimeSecurityProof(
		response.Data.ProofToken,
		identity,
		"channel.key.read",
		"17",
		[]string{"2fa", "passkey"},
	)
	require.NoError(t, err)
	assert.Equal(t, "2fa", method)
}

func TestUniversalVerifyRejectsPasswordForChannelKeyProof(t *testing.T) {
	_, identity := setupSecurityMutationProofTest(t)
	context, recorder := securityMutationProofContext(
		`{"method":"password","password":"CurrentPassword123","scope":"channel.key.read","target":"17"}`,
		identity,
	)

	UniversalVerify(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	var count int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).
		Where("purpose = ?", model.AuthFlowPurposeSecurityProof).
		Count(&count).Error)
	assert.Zero(t, count)
}

func TestUniversalVerifyRejectsTargetlessPasskeyMutationProofs(t *testing.T) {
	_, identity := setupSecurityMutationProofTest(t)

	for _, scope := range []string{
		securityProofScopePasskeyRegister,
		securityProofScopePasskeyDelete,
	} {
		t.Run(scope, func(t *testing.T) {
			body := fmt.Sprintf(
				`{"method":"password","password":"CurrentPassword123","scope":%q}`,
				scope,
			)
			context, recorder := securityMutationProofContext(body, identity)

			UniversalVerify(context)

			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
			assert.Contains(t, recorder.Body.String(), "不支持的安全验证范围")
		})
	}
}

func dashboardAccessTokenMutationContext(
	method string,
	identity service.AuthIdentity,
	proof string,
) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(method, "/api/user/token", nil)
	if proof != "" {
		context.Request.Header.Set("X-Security-Proof", proof)
	}
	context.Set("id", identity.UserID)
	context.Set("session_id", identity.SessionID)
	context.Set("auth_version", identity.UserAuthVersion)
	context.Set("session_version", identity.SessionVersion)
	context.Set("auth_identity", identity)
	return context, recorder
}

func TestDashboardAccessTokenRotationRequiresExactOneTimeSessionProof(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)

	missingContext, missingRecorder := dashboardAccessTokenMutationContext(http.MethodPost, identity, "")
	RotateAccessToken(missingContext)
	assert.Equal(t, http.StatusForbidden, missingRecorder.Code)
	assert.Contains(t, missingRecorder.Body.String(), "SECURITY_PROOF_REQUIRED")

	wrongProof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		"password",
		securityProofScopePATRotate,
		"other",
	)
	require.NoError(t, err)
	wrongContext, wrongRecorder := dashboardAccessTokenMutationContext(http.MethodPost, identity, wrongProof)
	RotateAccessToken(wrongContext)
	assert.Equal(t, http.StatusForbidden, wrongRecorder.Code)
	assert.Contains(t, wrongRecorder.Body.String(), "SECURITY_PROOF_TARGET_MISMATCH")

	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		"password",
		securityProofScopePATRotate,
		"self",
	)
	require.NoError(t, err)
	context, recorder := dashboardAccessTokenMutationContext(http.MethodPost, identity, proof)
	RotateAccessToken(context)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Token     string `json:"token"`
			ExpiresAt int64  `json:"expires_at"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotEmpty(t, response.Data.Token)
	assert.Greater(t, response.Data.ExpiresAt, int64(0))

	authenticated, err := model.ValidateDashboardAccessToken(response.Data.Token)
	require.NoError(t, err)
	require.NotNil(t, authenticated)
	assert.Equal(t, user.Id, authenticated.Id)

	replayContext, replayRecorder := dashboardAccessTokenMutationContext(http.MethodPost, identity, proof)
	RotateAccessToken(replayContext)
	assert.Equal(t, http.StatusForbidden, replayRecorder.Code)
	assert.Contains(t, replayRecorder.Body.String(), "SECURITY_PROOF_CONSUMED")
}

func TestDashboardAccessTokenRevocationRequiresSessionProofAndInvalidatesToken(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	raw, _, err := model.RotateDashboardAccessToken(user.Id, user.AuthVersion)
	require.NoError(t, err)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		"2fa",
		securityProofScopePATRotate,
		"self",
	)
	require.NoError(t, err)

	context, recorder := dashboardAccessTokenMutationContext(http.MethodDelete, identity, proof)
	RevokeAccessToken(context)
	assert.Equal(t, http.StatusOK, recorder.Code)

	authenticated, err := model.ValidateDashboardAccessToken(raw)
	require.ErrorIs(t, err, model.ErrDashboardAccessTokenRevoked)
	assert.Nil(t, authenticated)
}

func TestDashboardAccessTokenRotationRejectsPATIdentity(t *testing.T) {
	user, _ := setupSecurityMutationProofTest(t)
	context, recorder := dashboardAccessTokenMutationContext(
		http.MethodPost,
		service.AuthIdentity{UserID: user.Id, UserAuthVersion: user.AuthVersion},
		"untrusted-proof",
	)

	RotateAccessToken(context)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_INVALID")
}

func TestTwoFAStatusReportsConfiguredPasswordWithoutExposingHash(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	context, recorder := securityMutationProofContext("", identity)

	Get2FAStatus(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			HasPassword bool `json:"has_password"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.True(t, response.Data.HasPassword)
	assert.NotContains(t, recorder.Body.String(), user.Password)
}

func TestTwoFAStatusCleansExpiredPendingEnrollment(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	twoFA := model.TwoFA{
		UserId:    user.Id,
		Secret:    "expired-status-secret",
		IsEnabled: false,
		CreatedAt: time.Now().Add(-48 * time.Hour),
	}
	require.NoError(t, model.DB.Create(&twoFA).Error)
	require.NoError(t, model.DB.Create(&model.TwoFABackupCode{
		UserId:   user.Id,
		CodeHash: "expired-status-code",
	}).Error)
	context, recorder := securityMutationProofContext("", identity)

	Get2FAStatus(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"enabled":false`)
	var factorCount int64
	require.NoError(t, model.DB.Unscoped().Model(&model.TwoFA{}).
		Where("id = ?", twoFA.Id).
		Count(&factorCount).Error)
	assert.Zero(t, factorCount)
	var codeCount int64
	require.NoError(t, model.DB.Unscoped().Model(&model.TwoFABackupCode{}).
		Where("user_id = ?", user.Id).
		Count(&codeCount).Error)
	assert.Zero(t, codeCount)
}

func emailBindContext(
	identity service.AuthIdentity,
	email,
	code,
	proof string,
) (*gin.Context, *httptest.ResponseRecorder) {
	body, err := common.Marshal(map[string]string{"email": email, "code": code})
	if err != nil {
		panic(err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/email/bind",
		strings.NewReader(string(body)),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	if proof != "" {
		context.Request.Header.Set("X-Security-Proof", proof)
	}
	context.Set("id", identity.UserID)
	context.Set("session_id", identity.SessionID)
	context.Set("auth_version", identity.UserAuthVersion)
	context.Set("session_version", identity.SessionVersion)
	return context, recorder
}

func TestEmailBindRequiresTargetedProof(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	code, _, err := model.CreateEmailVerificationFlow("next@example.com")
	require.NoError(t, err)
	context, recorder := emailBindContext(
		identity,
		"next@example.com",
		code,
		"",
	)

	EmailBind(context)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_REQUIRED")
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Empty(t, stored.Email)
	assert.Equal(t, int64(1), stored.AuthVersion)
	require.NoError(t, model.ValidateEmailVerificationFlow(code, "next@example.com"))
}

func TestEmailBindConsumesProofAndRotatesOnlyCurrentSession(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	now := time.Now().Unix()
	otherSession := &model.UserSession{
		SID:             "security-proof-other-session",
		UserID:          user.Id,
		Version:         1,
		UserAuthVersion: user.AuthVersion,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     "security-proof-other-refresh",
		CreatedAt:       now,
		LastActiveAt:    now,
		ExpiresAt:       4102444800,
	}
	require.NoError(t, model.DB.Create(otherSession).Error)
	code, _, err := model.CreateEmailVerificationFlow("next@example.com")
	require.NoError(t, err)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeEmailChange,
		"next@example.com",
	)
	require.NoError(t, err)
	context, recorder := emailBindContext(
		identity,
		" NEXT@example.com ",
		code,
		proof,
	)

	EmailBind(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken string `json:"access_token"`
			Session     struct {
				SID string `json:"sid"`
			} `json:"session"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.NotEmpty(t, response.Data.AccessToken)
	assert.Equal(t, identity.SessionID, response.Data.Session.SID)

	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, "next@example.com", stored.Email)
	assert.Equal(t, int64(2), stored.AuthVersion)

	var current model.UserSession
	require.NoError(t, model.DB.First(&current, "sid = ?", identity.SessionID).Error)
	assert.Equal(t, int64(2), current.UserAuthVersion)
	assert.Equal(t, int64(3), current.Version)
	assert.Equal(t, model.UserSessionStatusActive, current.Status)

	var revoked model.UserSession
	require.NoError(t, model.DB.First(&revoked, "sid = ?", otherSession.SID).Error)
	assert.Equal(t, model.UserSessionStatusRevoked, revoked.Status)
	err = model.ValidateEmailVerificationFlow(code, "next@example.com")
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)

	_, err = service.ConsumeOneTimeSecurityProof(
		proof,
		identity,
		securityProofScopeEmailChange,
		"next@example.com",
		[]string{secureVerificationMethodPassword},
	)
	assert.ErrorIs(t, err, service.ErrProofConsumed)
}

func TestEmailBindRejectsReplayAndWrongTargetFlow(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	wrongTargetCode, _, err := model.CreateEmailVerificationFlow("other@example.com")
	require.NoError(t, err)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeEmailChange,
		"next@example.com",
	)
	require.NoError(t, err)

	context, recorder := emailBindContext(
		identity,
		"next@example.com",
		wrongTargetCode,
		proof,
	)
	EmailBind(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Empty(t, stored.Email)
	_, err = service.ConsumeOneTimeSecurityProof(
		proof,
		identity,
		securityProofScopeEmailChange,
		"next@example.com",
		[]string{secureVerificationMethodPassword},
	)
	require.NoError(t, err)

	replayCode, _, err := model.CreateEmailVerificationFlow("replay@example.com")
	require.NoError(t, err)
	replayProof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeEmailChange,
		"replay@example.com",
	)
	require.NoError(t, err)
	context, recorder = emailBindContext(identity, "replay@example.com", replayCode, replayProof)
	EmailBind(context)
	require.Equal(t, http.StatusOK, recorder.Code)

	context, recorder = emailBindContext(identity, "replay@example.com", replayCode, replayProof)
	EmailBind(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, "replay@example.com", stored.Email)
	assert.Equal(t, int64(2), stored.AuthVersion)
}

func TestEmailBindDuplicateEmailLeavesVerificationFlowRetryable(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	owner := &model.User{
		Username:    "email-bind-owner",
		Email:       "taken@example.com",
		Password:    "hash",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     "email-bind-owner-aff",
		AuthVersion: 1,
	}
	require.NoError(t, model.DB.Create(owner).Error)
	code, _, err := model.CreateEmailVerificationFlow("taken@example.com")
	require.NoError(t, err)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeEmailChange,
		"taken@example.com",
	)
	require.NoError(t, err)
	context, recorder := emailBindContext(identity, "taken@example.com", code, proof)

	EmailBind(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	require.NoError(t, model.ValidateEmailVerificationFlow(code, "taken@example.com"))
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Empty(t, stored.Email)
	assert.Equal(t, int64(1), stored.AuthVersion)
	_, err = service.ConsumeOneTimeSecurityProof(
		proof,
		identity,
		securityProofScopeEmailChange,
		"taken@example.com",
		[]string{secureVerificationMethodPassword},
	)
	assert.ErrorIs(t, err, service.ErrProofConsumed)
}

func TestEmailBindMissingUserDoesNotConsumeProof(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	code, _, err := model.CreateEmailVerificationFlow("orphaned@example.com")
	require.NoError(t, err)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeEmailChange,
		"orphaned@example.com",
	)
	require.NoError(t, err)
	require.NoError(t, model.DB.Unscoped().Delete(user).Error)
	context, recorder := emailBindContext(identity, "orphaned@example.com", code, proof)

	EmailBind(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.NotContains(t, recorder.Body.String(), "record not found")
	require.NoError(t, model.ValidateEmailVerificationFlow(code, "orphaned@example.com"))
	_, err = service.ConsumeOneTimeSecurityProof(
		proof,
		identity,
		securityProofScopeEmailChange,
		"orphaned@example.com",
		[]string{secureVerificationMethodPassword},
	)
	require.NoError(t, err)
}

func TestEmailBindPostCommitSessionFailureReturnsReauthenticationRequired(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_email_bind_current_session_advance
		BEFORE UPDATE ON user_sessions
		WHEN OLD.sid = 'security-proof-session' AND NEW.user_auth_version = 2
		BEGIN
			SELECT RAISE(ABORT, 'forced session advancement failure');
		END
	`).Error)
	code, _, err := model.CreateEmailVerificationFlow("committed@example.com")
	require.NoError(t, err)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeEmailChange,
		"committed@example.com",
	)
	require.NoError(t, err)
	context, recorder := emailBindContext(identity, "committed@example.com", code, proof)

	EmailBind(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			ReauthenticationRequired bool `json:"reauthentication_required"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.True(t, response.Data.ReauthenticationRequired)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	cookies := recorder.Result().Cookies()
	require.NotEmpty(t, cookies)
	var cleared bool
	for _, cookie := range cookies {
		if cookie.Name == service.RefreshCookieName {
			cleared = cookie.Value == "" && cookie.MaxAge < 0
		}
	}
	assert.True(t, cleared)

	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, "committed@example.com", stored.Email)
	assert.Equal(t, int64(2), stored.AuthVersion)
	err = model.ValidateEmailVerificationFlow(code, "committed@example.com")
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	_, err = service.ConsumeOneTimeSecurityProof(
		proof,
		identity,
		securityProofScopeEmailChange,
		"committed@example.com",
		[]string{secureVerificationMethodPassword},
	)
	assert.ErrorIs(t, err, service.ErrProofConsumed)

	context, recorder = emailBindContext(identity, "committed@example.com", code, proof)
	EmailBind(context)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, int64(2), stored.AuthVersion)
}

func TestDeleteSelfRequiresOneTimeSecurityProof(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodDelete, "/api/user/self", nil)
	context.Set("id", identity.UserID)
	context.Set("session_id", identity.SessionID)
	context.Set("auth_version", identity.UserAuthVersion)
	context.Set("session_version", identity.SessionVersion)

	DeleteSelf(context)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_REQUIRED")
	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count, "a session alone must not be able to destroy the account")

	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeAccountDelete,
		"self",
	)
	require.NoError(t, err)
	recorder = httptest.NewRecorder()
	context, _ = gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodDelete, "/api/user/self", nil)
	context.Request.Header.Set("X-Security-Proof", proof)
	context.Set("id", identity.UserID)
	context.Set("session_id", identity.SessionID)
	context.Set("auth_version", identity.UserAuthVersion)
	context.Set("session_version", identity.SessionVersion)

	DeleteSelf(context)

	assert.Contains(t, recorder.Body.String(), `"success":true`)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func passkeyMutationProofContext(
	identity service.AuthIdentity,
	proof string,
) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/passkey", nil)
	if proof != "" {
		context.Request.Header.Set("X-Security-Proof", proof)
	}
	context.Set("id", identity.UserID)
	context.Set("session_id", identity.SessionID)
	context.Set("auth_version", identity.UserAuthVersion)
	context.Set("session_version", identity.SessionVersion)
	return context, recorder
}

func TestPasskeyRegistrationRequiresOneTimeExistingCredentialProof(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	context, recorder := passkeyMutationProofContext(identity, "")

	assert.False(t, requirePasskeyRegistrationVerification(context, user.Id))
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_REQUIRED")

	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopePasskeyRegister,
		strconv.Itoa(user.Id),
	)
	require.NoError(t, err)
	context, recorder = passkeyMutationProofContext(identity, proof)
	assert.True(t, requirePasskeyRegistrationVerification(context, user.Id))

	context, recorder = passkeyMutationProofContext(identity, proof)
	assert.False(t, requirePasskeyRegistrationVerification(context, user.Id))
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_CONSUMED")
}

func TestPasskeyDeleteRequiresOneTimeExistingCredentialProof(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	context, recorder := passkeyMutationProofContext(identity, "")

	assert.False(t, requirePasskeyDeleteVerification(context, user.Id))
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_REQUIRED")

	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopePasskeyDelete,
		strconv.Itoa(user.Id),
	)
	require.NoError(t, err)
	context, _ = passkeyMutationProofContext(identity, proof)
	assert.True(t, requirePasskeyDeleteVerification(context, user.Id))
}

func TestSetupTwoFARequiresOneTimeExistingCredentialProof(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	context, recorder := passkeyMutationProofContext(identity, "")
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/2fa/setup", nil)

	Setup2FA(context)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_REQUIRED")
	twoFA, err := model.GetTwoFAByUserId(user.Id)
	require.NoError(t, err)
	assert.Nil(t, twoFA)

	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeTwoFASetup,
		strconv.Itoa(user.Id),
	)
	require.NoError(t, err)
	context, recorder = passkeyMutationProofContext(identity, proof)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/2fa/setup", nil)
	context.Request.Header.Set("X-Security-Proof", proof)

	Setup2FA(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	twoFA, err = model.GetTwoFAByUserId(user.Id)
	require.NoError(t, err)
	require.NotNil(t, twoFA)
	assert.False(t, twoFA.IsEnabled)
}

func TestEnableTwoFARejectsAndCleansExpiredEnrollmentBeforeCodeValidation(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	twoFA := &model.TwoFA{
		UserId:    user.Id,
		Secret:    "expired-controller-secret",
		IsEnabled: false,
		CreatedAt: time.Now().Add(-48 * time.Hour),
	}
	require.NoError(t, model.DB.Create(twoFA).Error)
	require.NoError(t, model.DB.Create(&model.TwoFABackupCode{
		UserId:   user.Id,
		CodeHash: "expired-controller-code",
	}).Error)

	context, recorder := securityMutationProofContext(
		`{"code":"not-numeric"}`,
		identity,
	)
	context.Request.URL.Path = "/api/user/2fa/enable"

	Enable2FA(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "2FA设置已过期，请重新初始化设置")
	var factorCount int64
	require.NoError(t, model.DB.Unscoped().Model(&model.TwoFA{}).
		Where("id = ?", twoFA.Id).
		Count(&factorCount).Error)
	assert.Zero(t, factorCount)
	var codeCount int64
	require.NoError(t, model.DB.Unscoped().Model(&model.TwoFABackupCode{}).
		Where("user_id = ?", user.Id).
		Count(&codeCount).Error)
	assert.Zero(t, codeCount)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, int64(1), storedUser.AuthVersion)
}

func TestTelegramBindStartRequiresOneTimeExistingCredentialProof(t *testing.T) {
	_, identity := setupSecurityMutationProofTest(t)
	previousEnabled := common.TelegramOAuthEnabled
	common.TelegramOAuthEnabled = true
	t.Cleanup(func() { common.TelegramOAuthEnabled = previousEnabled })

	context, recorder := passkeyMutationProofContext(identity, "")
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/telegram/bind/start",
		nil,
	)
	TelegramBindStart(context)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_REQUIRED")
	var count int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).
		Where("purpose = ?", model.AuthFlowPurposeTelegramBind).
		Count(&count).Error)
	assert.Zero(t, count)

	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeExternalBind,
		model.ExternalIdentityProviderTelegram,
	)
	require.NoError(t, err)
	context, recorder = passkeyMutationProofContext(identity, proof)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/telegram/bind/start",
		nil,
	)
	context.Request.Header.Set("X-Security-Proof", proof)
	TelegramBindStart(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
}

func TestWeChatBindRequiresOneTimeExistingCredentialProof(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	now := time.Now().Unix()
	otherSession := &model.UserSession{
		SID:             "wechat-other-session",
		UserID:          user.Id,
		Version:         1,
		UserAuthVersion: user.AuthVersion,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     "wechat-other-refresh",
		CreatedAt:       now,
		LastActiveAt:    now,
		ExpiresAt:       4102444800,
	}
	require.NoError(t, model.DB.Create(otherSession).Error)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		assert.Equal(t, "/api/wechat/user", request.URL.Path)
		assert.Equal(t, "bind-code", request.URL.Query().Get("code"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"message":"","data":"wechat-subject"}`))
	}))
	t.Cleanup(server.Close)
	previousEnabled := common.WeChatAuthEnabled
	previousAddress := common.WeChatServerAddress
	previousToken := common.WeChatServerToken
	common.WeChatAuthEnabled = true
	common.WeChatServerAddress = server.URL
	common.WeChatServerToken = "wechat-test-token"
	t.Cleanup(func() {
		common.WeChatAuthEnabled = previousEnabled
		common.WeChatServerAddress = previousAddress
		common.WeChatServerToken = previousToken
	})

	context, recorder := passkeyMutationProofContext(identity, "")
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/wechat/bind",
		strings.NewReader(`{"code":"bind-code"}`),
	)
	WeChatBind(context)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_REQUIRED")
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Empty(t, stored.WeChatId)

	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeExternalBind,
		"wechat",
	)
	require.NoError(t, err)
	context, recorder = passkeyMutationProofContext(identity, proof)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/wechat/bind",
		strings.NewReader(`{"code":"bind-code"}`),
	)
	context.Request.Header.Set("X-Security-Proof", proof)
	WeChatBind(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.NotEmpty(t, response.Data.AccessToken)

	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, "wechat-subject", stored.WeChatId)
	assert.Equal(t, int64(2), stored.AuthVersion)
	var current model.UserSession
	require.NoError(t, model.DB.First(&current, "sid = ?", identity.SessionID).Error)
	assert.Equal(t, int64(2), current.UserAuthVersion)
	assert.Equal(t, int64(3), current.Version)
	require.NoError(t, model.DB.First(otherSession, "sid = ?", otherSession.SID).Error)
	assert.Equal(t, model.UserSessionStatusRevoked, otherSession.Status)
}

func TestWeChatRegistrationCreatesExternalIdentityClaim(t *testing.T) {
	setupSecurityMutationProofTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		assert.Equal(t, "/api/wechat/user", request.URL.Path)
		assert.Equal(t, "register-code", request.URL.Query().Get("code"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"message":"","data":"wechat-register-subject"}`))
	}))
	t.Cleanup(server.Close)

	previousEnabled := common.WeChatAuthEnabled
	previousAddress := common.WeChatServerAddress
	previousToken := common.WeChatServerToken
	previousRegisterEnabled := common.RegisterEnabled
	previousNewUserQuota := common.QuotaForNewUser
	common.WeChatAuthEnabled = true
	common.WeChatServerAddress = server.URL
	common.WeChatServerToken = "wechat-test-token"
	common.RegisterEnabled = true
	common.QuotaForNewUser = 0
	t.Cleanup(func() {
		common.WeChatAuthEnabled = previousEnabled
		common.WeChatServerAddress = previousAddress
		common.WeChatServerToken = previousToken
		common.RegisterEnabled = previousRegisterEnabled
		common.QuotaForNewUser = previousNewUserQuota
	})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/oauth/wechat?code=register-code",
		nil,
	)
	WeChatAuth(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)

	var user model.User
	require.NoError(t, model.DB.Where("wechat_id = ?", "wechat-register-subject").First(&user).Error)
	var claim model.ExternalIdentityClaim
	require.NoError(t, model.DB.Where(
		"provider = ? AND subject = ?",
		model.ExternalIdentityProviderWeChat,
		"wechat-register-subject",
	).First(&claim).Error)
	assert.Equal(t, user.Id, claim.UserId)
}

func TestCustomOAuthUnbindRequiresOneTimeExistingCredentialProof(t *testing.T) {
	user, identity := setupSecurityMutationProofTest(t)
	binding := &model.UserOAuthBinding{
		UserId:         user.Id,
		ProviderId:     9,
		ProviderUserId: "custom-subject",
	}
	require.NoError(t, model.DB.Create(binding).Error)

	context, recorder := passkeyMutationProofContext(identity, "")
	context.Request = httptest.NewRequest(
		http.MethodDelete,
		"/api/user/oauth/bindings/9",
		nil,
	)
	context.Params = gin.Params{{Key: "provider_id", Value: "9"}}
	UnbindCustomOAuth(context)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_REQUIRED")
	_, err := model.GetUserOAuthBinding(user.Id, 9)
	require.NoError(t, err)

	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeExternalUnbind,
		"9",
	)
	require.NoError(t, err)
	context, recorder = passkeyMutationProofContext(identity, proof)
	context.Request = httptest.NewRequest(
		http.MethodDelete,
		"/api/user/oauth/bindings/9",
		nil,
	)
	context.Request.Header.Set("X-Security-Proof", proof)
	context.Params = gin.Params{{Key: "provider_id", Value: "9"}}
	UnbindCustomOAuth(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.NotEmpty(t, response.Data.AccessToken)
	_, err = model.GetUserOAuthBinding(user.Id, 9)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAdminClearBindingRequiresSessionProofBoundToTarget(t *testing.T) {
	admin, identity := setupSecurityMutationProofTest(t)
	require.NoError(t, model.DB.Model(admin).
		Update("role", common.RoleAdminUser).Error)
	target := &model.User{
		Username:    "admin-binding-target",
		Password:    "password",
		Email:       "target@example.com",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     "admin-binding-target-aff",
		AuthVersion: 1,
	}
	require.NoError(t, model.DB.Create(target).Error)
	now := time.Now().Unix()
	targetSession := &model.UserSession{
		SID:             "admin-binding-target-session",
		UserID:          target.Id,
		Version:         1,
		UserAuthVersion: target.AuthVersion,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     "admin-binding-target-refresh",
		CreatedAt:       now,
		LastActiveAt:    now,
		ExpiresAt:       4102444800,
	}
	require.NoError(t, model.DB.Create(targetSession).Error)

	context, recorder := passkeyMutationProofContext(identity, "")
	context.Request = httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("/api/user/%d/bindings/email", target.Id),
		nil,
	)
	context.Params = gin.Params{
		{Key: "id", Value: strconv.Itoa(target.Id)},
		{Key: "binding_type", Value: "email"},
	}
	context.Set("role", common.RoleAdminUser)
	AdminClearUserBinding(context)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_REQUIRED")
	var stored model.User
	require.NoError(t, model.DB.First(&stored, target.Id).Error)
	assert.Equal(t, "target@example.com", stored.Email)
	assert.Equal(t, int64(1), stored.AuthVersion)

	proofTarget := fmt.Sprintf("%d:email", target.Id)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeAdminBindingClear,
		proofTarget,
	)
	require.NoError(t, err)
	context, recorder = passkeyMutationProofContext(identity, proof)
	context.Request = httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("/api/user/%d/bindings/email", target.Id),
		nil,
	)
	context.Request.Header.Set("X-Security-Proof", proof)
	context.Params = gin.Params{
		{Key: "id", Value: strconv.Itoa(target.Id)},
		{Key: "binding_type", Value: "email"},
	}
	context.Set("role", common.RoleAdminUser)
	AdminClearUserBinding(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	require.NoError(t, model.DB.First(&stored, target.Id).Error)
	assert.Empty(t, stored.Email)
	assert.Equal(t, int64(2), stored.AuthVersion)
	require.NoError(t, model.DB.First(targetSession, "sid = ?", targetSession.SID).Error)
	assert.Equal(t, model.UserSessionStatusRevoked, targetSession.Status)
}

func TestAdminFactorResetsRequireSessionProofBoundToTarget(t *testing.T) {
	admin, identity := setupSecurityMutationProofTest(t)
	require.NoError(t, model.DB.Model(admin).
		Update("role", common.RoleAdminUser).Error)
	target := &model.User{
		Username:    "admin-factor-target",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     "admin-factor-target-aff",
		AuthVersion: 1,
	}
	require.NoError(t, model.DB.Create(target).Error)
	require.NoError(t, model.DB.Create(&model.TwoFA{
		UserId: target.Id, Secret: "totp-secret", IsEnabled: true,
	}).Error)
	require.NoError(t, model.DB.Create(&model.PasskeyCredential{
		UserID: target.Id, CredentialID: "Y3JlZGVudGlhbA==", PublicKey: "cHVibGljLWtleQ==",
	}).Error)

	twoFAContext, twoFARecorder := passkeyMutationProofContext(identity, "")
	twoFAContext.Request = httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("/api/user/%d/2fa", target.Id),
		nil,
	)
	twoFAContext.Params = gin.Params{{Key: "id", Value: strconv.Itoa(target.Id)}}
	twoFAContext.Set("role", common.RoleAdminUser)
	AdminDisable2FA(twoFAContext)

	assert.Equal(t, http.StatusForbidden, twoFARecorder.Code)
	assert.Contains(t, twoFARecorder.Body.String(), "SECURITY_PROOF_REQUIRED")
	twoFA, err := model.GetTwoFAByUserId(target.Id)
	require.NoError(t, err)
	require.NotNil(t, twoFA)
	assert.True(t, twoFA.IsEnabled)

	passkeyContext, passkeyRecorder := passkeyMutationProofContext(identity, "")
	passkeyContext.Request = httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("/api/user/%d/reset_passkey", target.Id),
		nil,
	)
	passkeyContext.Params = gin.Params{{Key: "id", Value: strconv.Itoa(target.Id)}}
	passkeyContext.Set("role", common.RoleAdminUser)
	AdminResetPasskey(passkeyContext)

	assert.Equal(t, http.StatusForbidden, passkeyRecorder.Code)
	assert.Contains(t, passkeyRecorder.Body.String(), "SECURITY_PROOF_REQUIRED")
	_, err = model.GetPasskeyByUserID(target.Id)
	require.NoError(t, err)
}

func TestAdminFactorResetsConsumeProofAndRevokeTargetSessions(t *testing.T) {
	admin, identity := setupSecurityMutationProofTest(t)
	require.NoError(t, model.DB.Model(admin).
		Update("role", common.RoleAdminUser).Error)
	target := &model.User{
		Username:    "admin-factor-success-target",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     "admin-factor-success-aff",
		AuthVersion: 1,
	}
	require.NoError(t, model.DB.Create(target).Error)
	require.NoError(t, model.DB.Create(&model.TwoFA{
		UserId: target.Id, Secret: "totp-secret", IsEnabled: true,
	}).Error)
	require.NoError(t, model.DB.Create(&model.PasskeyCredential{
		UserID: target.Id, CredentialID: "Y3JlZGVudGlhbA==", PublicKey: "cHVibGljLWtleQ==",
	}).Error)
	now := time.Now().Unix()
	twoFASession := &model.UserSession{
		SID:             "admin-twofa-target-session",
		UserID:          target.Id,
		Version:         1,
		UserAuthVersion: target.AuthVersion,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     "admin-twofa-target-refresh",
		CreatedAt:       now,
		LastActiveAt:    now,
		ExpiresAt:       4102444800,
	}
	require.NoError(t, model.DB.Create(twoFASession).Error)

	targetID := strconv.Itoa(target.Id)
	twoFAProof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeAdminTwoFADisable,
		targetID,
	)
	require.NoError(t, err)
	twoFAContext, twoFARecorder := passkeyMutationProofContext(identity, twoFAProof)
	twoFAContext.Request = httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("/api/user/%d/2fa", target.Id),
		nil,
	)
	twoFAContext.Request.Header.Set("X-Security-Proof", twoFAProof)
	twoFAContext.Params = gin.Params{{Key: "id", Value: targetID}}
	twoFAContext.Set("role", common.RoleAdminUser)

	AdminDisable2FA(twoFAContext)

	require.Equal(t, http.StatusOK, twoFARecorder.Code)
	assert.Contains(t, twoFARecorder.Body.String(), `"success":true`)
	twoFA, err := model.GetTwoFAByUserId(target.Id)
	require.NoError(t, err)
	assert.Nil(t, twoFA)
	var stored model.User
	require.NoError(t, model.DB.First(&stored, target.Id).Error)
	assert.Equal(t, int64(2), stored.AuthVersion)
	require.NoError(t, model.DB.First(twoFASession, "sid = ?", twoFASession.SID).Error)
	assert.Equal(t, model.UserSessionStatusRevoked, twoFASession.Status)

	passkeySession := &model.UserSession{
		SID:             "admin-passkey-target-session",
		UserID:          target.Id,
		Version:         1,
		UserAuthVersion: stored.AuthVersion,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     "admin-passkey-target-refresh",
		CreatedAt:       now,
		LastActiveAt:    now,
		ExpiresAt:       4102444800,
	}
	require.NoError(t, model.DB.Create(passkeySession).Error)
	passkeyProof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeAdminPasskeyReset,
		targetID,
	)
	require.NoError(t, err)
	passkeyContext, passkeyRecorder := passkeyMutationProofContext(identity, passkeyProof)
	passkeyContext.Request = httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("/api/user/%d/reset_passkey", target.Id),
		nil,
	)
	passkeyContext.Request.Header.Set("X-Security-Proof", passkeyProof)
	passkeyContext.Params = gin.Params{{Key: "id", Value: targetID}}
	passkeyContext.Set("role", common.RoleAdminUser)

	AdminResetPasskey(passkeyContext)

	require.Equal(t, http.StatusOK, passkeyRecorder.Code)
	assert.Contains(t, passkeyRecorder.Body.String(), `"success":true`)
	_, err = model.GetPasskeyByUserID(target.Id)
	assert.ErrorIs(t, err, model.ErrPasskeyNotFound)
	require.NoError(t, model.DB.First(&stored, target.Id).Error)
	assert.Equal(t, int64(3), stored.AuthVersion)
	require.NoError(t, model.DB.First(passkeySession, "sid = ?", passkeySession.SID).Error)
	assert.Equal(t, model.UserSessionStatusRevoked, passkeySession.Status)
}
