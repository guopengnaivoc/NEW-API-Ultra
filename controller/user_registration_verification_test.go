package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRegistrationVerificationTest(t *testing.T) {
	t.Helper()
	setupVerificationFlowControllerTest(t)
	previousRegisterEnabled := common.RegisterEnabled
	previousPasswordRegisterEnabled := common.PasswordRegisterEnabled
	previousEmailVerificationEnabled := common.EmailVerificationEnabled
	previousGenerateDefaultToken := constant.GenerateDefaultToken
	previousDefaultUseAutoGroup := setting.DefaultUseAutoGroup
	previousNewUserQuota := common.QuotaForNewUser
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.EmailVerificationEnabled = true
	constant.GenerateDefaultToken = false
	setting.DefaultUseAutoGroup = false
	common.QuotaForNewUser = 0
	t.Cleanup(func() {
		common.RegisterEnabled = previousRegisterEnabled
		common.PasswordRegisterEnabled = previousPasswordRegisterEnabled
		common.EmailVerificationEnabled = previousEmailVerificationEnabled
		constant.GenerateDefaultToken = previousGenerateDefaultToken
		setting.DefaultUseAutoGroup = previousDefaultUseAutoGroup
		common.QuotaForNewUser = previousNewUserQuota
	})
}

func registerVerificationRequest(
	username,
	email,
	code string,
) (*gin.Context, *httptest.ResponseRecorder) {
	body, err := common.Marshal(map[string]string{
		"username":          username,
		"password":          "RegisterPassword123",
		"email":             email,
		"verification_code": code,
	})
	if err != nil {
		panic(err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/user/register",
		strings.NewReader(string(body)),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	return context, recorder
}

func assertRegistrationVerificationFlowAvailable(
	t *testing.T,
	token,
	email string,
) {
	t.Helper()
	require.NoError(t, model.ValidateEmailVerificationFlow(token, email))
}

func TestRegisterVerificationConsumesFlowAndRejectsReplay(t *testing.T) {
	setupRegistrationVerificationTest(t)
	email := "new-user@example.com"
	token, _, err := model.CreateEmailVerificationFlow(email)
	require.NoError(t, err)

	context, recorder := registerVerificationRequest("new-user", email, token)
	Register(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var users int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("email = ?", email).Count(&users).Error)
	assert.Equal(t, int64(1), users)
	err = model.ValidateEmailVerificationFlow(token, email)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)

	context, recorder = registerVerificationRequest("new-user-replay", email, token)
	Register(context)

	assert.Contains(t, recorder.Body.String(), `"success":false`)
	require.NoError(t, model.DB.Model(&model.User{}).Where("email = ?", email).Count(&users).Error)
	assert.Equal(t, int64(1), users)
}

func TestRegisterStoresVerifiedEmailWhenVerificationIsOptional(t *testing.T) {
	setupRegistrationVerificationTest(t)
	common.EmailVerificationEnabled = false
	email := "optional-email@example.com"
	token, _, err := model.CreateEmailVerificationFlow(email)
	require.NoError(t, err)

	context, recorder := registerVerificationRequest("optional-user", email, token)
	Register(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	// The address must be persisted rather than silently dropped, otherwise the
	// account can never use password recovery.
	var stored model.User
	require.NoError(t, model.DB.First(&stored, "username = ?", "optional-user").Error)
	assert.Equal(t, email, stored.Email)
	assert.ErrorIs(t, model.ValidateEmailVerificationFlow(token, email), model.ErrAuthFlowConsumed)
}

func TestRegisterWithoutEmailStaysAllowedWhenVerificationIsOptional(t *testing.T) {
	setupRegistrationVerificationTest(t)
	common.EmailVerificationEnabled = false

	context, recorder := registerVerificationRequest("no-email-user", "", "")
	Register(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var stored model.User
	require.NoError(t, model.DB.First(&stored, "username = ?", "no-email-user").Error)
	assert.Empty(t, stored.Email)
}

func TestRegisterRejectsUnverifiedEmailWhenVerificationIsOptional(t *testing.T) {
	setupRegistrationVerificationTest(t)
	common.EmailVerificationEnabled = false

	for _, test := range []struct {
		name     string
		username string
		code     string
	}{
		{name: "no code presented", username: "unverified-user", code: ""},
		{name: "code does not match address", username: "wrong-code-user", code: "not-a-real-code"},
	} {
		t.Run(test.name, func(t *testing.T) {
			context, recorder := registerVerificationRequest(
				test.username, "someone-elses@example.com", test.code,
			)
			Register(context)

			assert.Contains(t, recorder.Body.String(), `"success":false`)
			var count int64
			require.NoError(t, model.DB.Model(&model.User{}).
				Where("username = ?", test.username).Count(&count).Error)
			assert.Zero(t, count, "an unverified address must not create an account")
		})
	}
}

func TestRegisterRejectsDuplicateEmailWhenVerificationIsOptional(t *testing.T) {
	setupRegistrationVerificationTest(t)
	common.EmailVerificationEnabled = false
	email := "shared-mailbox@example.com"
	require.NoError(t, model.DB.Create(&model.User{
		Username:    "mailbox-owner",
		Email:       email,
		Password:    "hash",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     "shared-mailbox-aff",
		AuthVersion: 1,
	}).Error)
	token, _, err := model.CreateEmailVerificationFlow(email)
	require.NoError(t, err)

	context, recorder := registerVerificationRequest("mailbox-contender", email, token)
	Register(context)

	assert.Contains(t, recorder.Body.String(), `"success":false`)
	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("email = ?", email).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	assertRegistrationVerificationFlowAvailable(t, token, email)
}

func TestRegisterVerificationDuplicateUsernameLeavesFlowRetryable(t *testing.T) {
	setupRegistrationVerificationTest(t)
	require.NoError(t, model.DB.Create(&model.User{
		Username:    "taken-name",
		Password:    "hash",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     "taken-name-aff",
		AuthVersion: 1,
	}).Error)
	email := "retry-username@example.com"
	token, _, err := model.CreateEmailVerificationFlow(email)
	require.NoError(t, err)

	context, recorder := registerVerificationRequest("taken-name", email, token)
	Register(context)

	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assertRegistrationVerificationFlowAvailable(t, token, email)

	context, recorder = registerVerificationRequest("available-name", email, token)
	Register(context)

	assert.Contains(t, recorder.Body.String(), `"success":true`)
	err = model.ValidateEmailVerificationFlow(token, email)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
}

func TestRegisterVerificationDuplicateEmailLeavesFlowRetryable(t *testing.T) {
	setupRegistrationVerificationTest(t)
	email := "taken-email@example.com"
	existing := &model.User{
		Username:    "email-owner",
		Email:       email,
		Password:    "hash",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     "taken-email-aff",
		AuthVersion: 1,
	}
	require.NoError(t, model.DB.Create(existing).Error)
	token, _, err := model.CreateEmailVerificationFlow(email)
	require.NoError(t, err)

	context, recorder := registerVerificationRequest("email-contender", email, token)
	Register(context)

	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assertRegistrationVerificationFlowAvailable(t, token, email)

	require.NoError(t, model.DB.Unscoped().Delete(existing).Error)
	context, recorder = registerVerificationRequest("email-contender", email, token)
	Register(context)

	assert.Contains(t, recorder.Body.String(), `"success":true`)
	err = model.ValidateEmailVerificationFlow(token, email)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
}

func TestRegisterVerificationInsertFailureRollsBackFlow(t *testing.T) {
	setupRegistrationVerificationTest(t)
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_selected_user_insert
		BEFORE INSERT ON users
		WHEN NEW.username = 'trigger-failure'
		BEGIN
			SELECT RAISE(ABORT, 'forced user insert failure');
		END
	`).Error)
	email := "retry-insert@example.com"
	token, _, err := model.CreateEmailVerificationFlow(email)
	require.NoError(t, err)

	context, recorder := registerVerificationRequest("trigger-failure", email, token)
	Register(context)

	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assertRegistrationVerificationFlowAvailable(t, token, email)
	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("email = ?", email).Count(&count).Error)
	assert.Zero(t, count)

	context, recorder = registerVerificationRequest("retry-insert", email, token)
	Register(context)

	assert.Contains(t, recorder.Body.String(), `"success":true`)
	err = model.ValidateEmailVerificationFlow(token, email)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
}

func TestRegisterVerificationDefaultTokenFailureRollsBackEverything(t *testing.T) {
	setupRegistrationVerificationTest(t)
	constant.GenerateDefaultToken = true
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_default_token_insert
		BEFORE INSERT ON tokens
		BEGIN
			SELECT RAISE(ABORT, 'forced token insert failure');
		END
	`).Error)
	email := "default-token@example.com"
	token, _, err := model.CreateEmailVerificationFlow(email)
	require.NoError(t, err)

	context, recorder := registerVerificationRequest("default-token", email, token)
	Register(context)

	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assertRegistrationVerificationFlowAvailable(t, token, email)
	var userCount int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("email = ?", email).Count(&userCount).Error)
	assert.Zero(t, userCount)
	var tokenCount int64
	require.NoError(t, model.DB.Model(&model.Token{}).Count(&tokenCount).Error)
	assert.Zero(t, tokenCount)

	require.NoError(t, model.DB.Exec("DROP TRIGGER fail_default_token_insert").Error)
	context, recorder = registerVerificationRequest("default-token", email, token)
	Register(context)

	assert.Contains(t, recorder.Body.String(), `"success":true`)
	require.NoError(t, model.DB.Model(&model.User{}).Where("email = ?", email).Count(&userCount).Error)
	assert.Equal(t, int64(1), userCount)
	require.NoError(t, model.DB.Model(&model.Token{}).Count(&tokenCount).Error)
	assert.Equal(t, int64(1), tokenCount)
	err = model.ValidateEmailVerificationFlow(token, email)
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
}
