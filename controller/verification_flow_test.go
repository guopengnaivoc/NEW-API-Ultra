package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var verificationCodePattern = regexp.MustCompile(`<strong>([A-Za-z0-9_-]{43})</strong>`)

func setupVerificationFlowControllerTest(t *testing.T) {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousType := common.MainDatabaseType()
	previousSecret := common.SessionSecret
	previousRedis := common.RedisEnabled
	previousLogConsume := common.LogConsumeEnabled
	previousDomainRestriction := common.EmailDomainRestrictionEnabled
	previousAliasRestriction := common.EmailAliasRestrictionEnabled
	previousBodyLimit := constant.AnonymousRequestBodyLimitKB
	previousServerAddress := system_setting.ServerAddress

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.AuthFlow{},
		&model.Token{},
		&model.Log{},
	))
	model.DB = db
	model.LOG_DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SessionSecret = "verification-flow-controller-test-secret"
	common.RedisEnabled = false
	common.LogConsumeEnabled = false
	common.EmailDomainRestrictionEnabled = false
	common.EmailAliasRestrictionEnabled = false
	constant.AnonymousRequestBodyLimitKB = 1
	system_setting.ServerAddress = "https://gateway.example.test"

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetMainDatabaseType(previousType)
		common.SessionSecret = previousSecret
		common.RedisEnabled = previousRedis
		common.LogConsumeEnabled = previousLogConsume
		common.EmailDomainRestrictionEnabled = previousDomainRestriction
		common.EmailAliasRestrictionEnabled = previousAliasRestriction
		constant.AnonymousRequestBodyLimitKB = previousBodyLimit
		system_setting.ServerAddress = previousServerAddress
	})
}

func verificationHandlerContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	return context, recorder
}

func createVerificationControllerUser(t *testing.T, email string) *model.User {
	t.Helper()
	password, err := common.Password2Hash("CurrentPassword123")
	require.NoError(t, err)
	user := &model.User{
		Username:    strings.ReplaceAll(email, "@", "-"),
		Email:       model.NormalizeEmail(email),
		Password:    password,
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AffCode:     common.GetRandomString(8),
		AuthVersion: 1,
	}
	require.NoError(t, model.DB.Create(user).Error)
	return user
}

func extractVerificationCode(t *testing.T, content string) string {
	t.Helper()
	match := verificationCodePattern.FindStringSubmatch(content)
	require.Len(t, match, 2)
	return match[1]
}

func TestSendEmailVerificationRejectsMalformedAndOversizedBodiesWithoutFlow(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	gin.SetMode(gin.TestMode)

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"email":`},
		{name: "oversized", body: `{"email":"` + strings.Repeat("a", 2048) + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := gin.New()
			engine.POST("/", middleware.AnonymousRequestBodyLimit(), SendEmailVerification)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")

			engine.ServeHTTP(recorder, request)

			assert.False(t, recorder.Code >= http.StatusOK && recorder.Code < http.StatusMultipleChoices &&
				strings.Contains(recorder.Body.String(), `"success":true`))
			var count int64
			require.NoError(t, model.DB.Model(&model.AuthFlow{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestSendEmailVerificationCreatesNormalizedDurableFlow(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	var receiver string
	var content string

	err := sendEmailVerification(
		"  Target.User@Example.COM ",
		func(_ string, gotReceiver string, gotContent string) error {
			receiver = gotReceiver
			content = gotContent
			return nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, "target.user@example.com", receiver)
	code := extractVerificationCode(t, content)
	require.NoError(t, model.ValidateEmailVerificationFlow(code, receiver))
}

func TestSendEmailVerificationDeliveryFailureDeletesOnlyNewFlow(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	existingToken, existingFlow, err := model.CreateEmailVerificationFlow("existing@example.com")
	require.NoError(t, err)
	deliveryErr := errors.New("delivery failed")

	err = sendEmailVerification(
		"new@example.com",
		func(_, _, _ string) error { return deliveryErr },
	)

	require.ErrorIs(t, err, deliveryErr)
	require.NoError(t, model.ValidateEmailVerificationFlow(existingToken, "existing@example.com"))
	var flows []model.AuthFlow
	require.NoError(t, model.DB.Order("id").Find(&flows).Error)
	require.Len(t, flows, 1)
	assert.Equal(t, existingFlow.Id, flows[0].Id)
}

func TestSendEmailVerificationSenderErrorIsNotExposed(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	context, recorder := verificationHandlerContext(`{"email":"send-error@example.com"}`)
	distinctiveError := "smtp recipient send-error@example.com rejected"

	sendEmailVerificationResponse(
		context,
		func(_, _, _ string) error { return errors.New(distinctiveError) },
	)

	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.NotContains(t, recorder.Body.String(), distinctiveError)
	assert.NotContains(t, recorder.Body.String(), "send-error@example.com")
	var count int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestSendPasswordResetEmailCreatesUserBoundFlowAndSecretFreeLink(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	user := createVerificationControllerUser(t, " Reset.User@Example.COM ")
	var receiver string
	var content string

	err := sendPasswordResetEmail(
		" reset.user@example.com ",
		func(_ string, gotReceiver string, gotContent string) error {
			receiver = gotReceiver
			content = gotContent
			return nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, "reset.user@example.com", receiver)
	code := extractVerificationCode(t, content)
	flow, err := model.GetAuthFlow(code, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposePasswordReset,
		UserId:  user.Id,
	})
	require.NoError(t, err)
	assert.Equal(t, user.Id, flow.UserId)
	assert.Contains(t, content, `href="https://gateway.example.test/user/reset"`)
	assert.NotContains(t, content, "?")
	assert.NotContains(t, content, "#")
	assert.Contains(t, content, code)
	assert.NotContains(t, content, user.Email+"?")
}

func TestSendPasswordResetEmailMissingUserIsGenericSuccessWithoutFlow(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	sent := false

	err := sendPasswordResetEmail(
		"missing@example.com",
		func(_, _, _ string) error {
			sent = true
			return nil
		},
	)

	require.NoError(t, err)
	assert.False(t, sent)
	var count int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestSendPasswordResetEmailFormerAddressAfterMoveDoesNotIssueOrSend(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	user := createVerificationControllerUser(t, "former-reset@example.com")
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"email":        "current-reset@example.com",
		"auth_version": gorm.Expr("auth_version + 1"),
	}).Error)
	sent := false

	err := sendPasswordResetEmail(
		"former-reset@example.com",
		func(_, _, _ string) error {
			sent = true
			return nil
		},
	)

	require.NoError(t, err)
	assert.False(t, sent)
	var count int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestSendPasswordResetEmailDeliveryFailureDeletesOnlyNewFlow(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	existingUser := createVerificationControllerUser(t, "existing-reset@example.com")
	existingToken, existingFlow, err := model.CreatePasswordResetFlow(existingUser)
	require.NoError(t, err)
	createVerificationControllerUser(t, "new-reset@example.com")
	deliveryErr := errors.New("delivery failed")

	err = sendPasswordResetEmail(
		"new-reset@example.com",
		func(_, _, _ string) error { return deliveryErr },
	)

	require.ErrorIs(t, err, deliveryErr)
	flow, err := model.GetAuthFlow(existingToken, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposePasswordReset,
		UserId:  existingUser.Id,
	})
	require.NoError(t, err)
	assert.Equal(t, existingFlow.Id, flow.Id)
	var flows []model.AuthFlow
	require.NoError(t, model.DB.Order("id").Find(&flows).Error)
	require.Len(t, flows, 1)
	assert.Equal(t, existingFlow.Id, flows[0].Id)
}

func TestResetPasswordRequiresTokenAndSubmittedPassword(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	user := createVerificationControllerUser(t, "reset-contract@example.com")
	token, _, err := model.CreatePasswordResetFlow(user)
	require.NoError(t, err)

	context, recorder := verificationHandlerContext(
		`{"email":"reset-contract@example.com","token":"` + token + `"}`,
	)
	ResetPassword(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	_, err = model.GetAuthFlow(token, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposePasswordReset,
	})
	require.NoError(t, err)
}

func TestResetPasswordRejectsUnknownFieldsWithoutFlowConsumption(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	user := createVerificationControllerUser(t, "reset-extra-field@example.com")
	token, _, err := model.CreatePasswordResetFlow(user)
	require.NoError(t, err)

	context, recorder := verificationHandlerContext(
		`{"token":"` + token + `","password":"NewPassword123","email":"reset-extra-field@example.com"}`,
	)
	ResetPassword(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Len(t, response, 2)
	assert.Equal(t, false, response["success"])
	assert.Equal(t, common.TranslateMessage(context, i18n.MsgInvalidParams), response["message"])
	assert.NotContains(t, response, "data")
	_, err = model.GetAuthFlow(token, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposePasswordReset,
	})
	require.NoError(t, err)
}

func TestResetPasswordRequiresSingleJSONDocument(t *testing.T) {
	tests := []struct {
		name    string
		suffix  string
		success bool
	}{
		{
			name:    "trailing whitespace",
			suffix:  " \n\t",
			success: true,
		},
		{
			name:   "trailing empty object",
			suffix: ` {}`,
		},
		{
			name:   "trailing null",
			suffix: ` null`,
		},
		{
			name:   "trailing second object",
			suffix: ` {"token":"other","password":"OtherPassword123"}`,
		},
		{
			name:   "malformed suffix",
			suffix: ` garbage`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupVerificationFlowControllerTest(t)
			user := createVerificationControllerUser(t, "reset-document-"+strings.ReplaceAll(test.name, " ", "-")+"@example.com")
			token, _, err := model.CreatePasswordResetFlow(user)
			require.NoError(t, err)
			newPassword := "NewPassword123"
			context, recorder := verificationHandlerContext(
				`{"token":"` + token + `","password":"` + newPassword + `"}` + test.suffix,
			)

			ResetPassword(context)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response map[string]any
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			var stored model.User
			require.NoError(t, model.DB.First(&stored, user.Id).Error)
			if test.success {
				assert.Equal(t, true, response["success"])
				assert.True(t, common.ValidatePasswordAndHash(newPassword, stored.Password))
				_, getErr := model.GetAuthFlow(token, model.AuthFlowMatch{
					Purpose: model.AuthFlowPurposePasswordReset,
				})
				assert.ErrorIs(t, getErr, model.ErrAuthFlowConsumed)
				return
			}

			assert.Equal(t, false, response["success"])
			assert.Equal(t, common.TranslateMessage(context, i18n.MsgInvalidParams), response["message"])
			assert.True(t, common.ValidatePasswordAndHash("CurrentPassword123", stored.Password))
			flow, getErr := model.GetAuthFlow(token, model.AuthFlowMatch{
				Purpose: model.AuthFlowPurposePasswordReset,
			})
			require.NoError(t, getErr)
			assert.Nil(t, flow.ConsumedAt)
		})
	}
}

func TestResetPasswordValidatesPasswordBeforeFlowConsumption(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	user := createVerificationControllerUser(t, "reset-short@example.com")
	token, _, err := model.CreatePasswordResetFlow(user)
	require.NoError(t, err)

	context, recorder := verificationHandlerContext(
		`{"token":"` + token + `","password":"short"}`,
	)
	ResetPassword(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	_, err = model.GetAuthFlow(token, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposePasswordReset,
	})
	require.NoError(t, err)
}

func TestResetPasswordSuccessReturnsNoSecretDataAndNoStore(t *testing.T) {
	setupVerificationFlowControllerTest(t)
	user := createVerificationControllerUser(t, "reset-success@example.com")
	token, _, err := model.CreatePasswordResetFlow(user)
	require.NoError(t, err)
	newPassword := "NewPassword123"

	context, recorder := verificationHandlerContext(
		`{"token":"` + token + `","password":"` + newPassword + `"}`,
	)
	ResetPassword(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Len(t, response, 2)
	assert.Equal(t, true, response["success"])
	assert.IsType(t, "", response["message"])
	assert.NotContains(t, response, "data")
	assert.NotContains(t, recorder.Body.String(), newPassword)
	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.True(t, common.ValidatePasswordAndHash(newPassword, stored.Password))
}

func TestResetPasswordInvalidFlowVariantsUseGenericResponse(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, user *model.User, token string, flow *model.AuthFlow) string
	}{
		{
			name: "invalid",
			mutate: func(_ *testing.T, _ *model.User, _ string, _ *model.AuthFlow) string {
				return "invalid-token"
			},
		},
		{
			name: "expired",
			mutate: func(t *testing.T, _ *model.User, token string, flow *model.AuthFlow) string {
				require.NoError(t, model.DB.Model(&model.AuthFlow{}).
					Where("id = ?", flow.Id).
					Update("expires_at", time.Now().Add(-time.Minute)).Error)
				return token
			},
		},
		{
			name: "consumed",
			mutate: func(t *testing.T, user *model.User, token string, _ *model.AuthFlow) string {
				require.NoError(t, model.ResetUserPasswordWithFlow(token, "FirstPassword123"))
				return token
			},
		},
		{
			name: "stale",
			mutate: func(t *testing.T, user *model.User, token string, _ *model.AuthFlow) string {
				require.NoError(t, model.DB.Model(user).
					Update("auth_version", gorm.Expr("auth_version + 1")).Error)
				return token
			},
		},
	}

	var genericMessage string
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupVerificationFlowControllerTest(t)
			user := createVerificationControllerUser(t, test.name+"@example.com")
			token, flow, err := model.CreatePasswordResetFlow(user)
			require.NoError(t, err)
			token = test.mutate(t, user, token, flow)
			context, recorder := verificationHandlerContext(
				`{"token":"` + token + `","password":"NewPassword123"}`,
			)

			ResetPassword(context)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			require.NotEmpty(t, response.Message)
			if genericMessage == "" {
				genericMessage = response.Message
			}
			assert.Equal(t, genericMessage, response.Message)
		})
	}
}
