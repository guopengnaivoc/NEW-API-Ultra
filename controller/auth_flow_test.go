package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type authFlowTestOAuthProvider struct {
	exchangeErr   error
	userInfoErr   error
	exchangeCalls int
	userInfoCalls int
	isUserIDTaken func(string) bool
}

func (*authFlowTestOAuthProvider) GetName() string { return "Auth Flow Test" }
func (*authFlowTestOAuthProvider) IsEnabled() bool { return true }
func (provider *authFlowTestOAuthProvider) ExchangeToken(context.Context, string, *gin.Context) (*oauth.OAuthToken, error) {
	provider.exchangeCalls++
	if provider.exchangeErr != nil {
		return nil, provider.exchangeErr
	}
	return &oauth.OAuthToken{}, nil
}
func (provider *authFlowTestOAuthProvider) GetUserInfo(context.Context, *oauth.OAuthToken) (*oauth.OAuthUser, error) {
	provider.userInfoCalls++
	if provider.userInfoErr != nil {
		return nil, provider.userInfoErr
	}
	return &oauth.OAuthUser{ProviderUserID: "external-user"}, nil
}
func (provider *authFlowTestOAuthProvider) IsUserIDTaken(providerUserID string) bool {
	if provider.isUserIDTaken != nil {
		return provider.isUserIDTaken(providerUserID)
	}
	return false
}
func (*authFlowTestOAuthProvider) FillUserByProviderID(user *model.User, providerUserID string) error {
	user.GitHubId = providerUserID
	return user.FillUserByGitHubId()
}
func (*authFlowTestOAuthProvider) SetProviderUserID(user *model.User, providerUserID string) {
	user.GitHubId = providerUserID
}
func (*authFlowTestOAuthProvider) GetProviderPrefix() string { return "flow_" }

func setupAuthFlowControllerTest(t *testing.T) *authFlowTestOAuthProvider {
	t.Helper()
	require.NoError(t, i18n.Init())
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.AuthFlow{},
		&model.User{},
		&model.ExternalIdentityClaim{},
	))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	provider := &authFlowTestOAuthProvider{}
	oauth.Register("auth-flow-test", provider)
	t.Cleanup(func() {
		oauth.Unregister("auth-flow-test")
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
	})
	return provider
}

func TestBuiltInOAuthRegistrationClaimsSubjectAtomically(t *testing.T) {
	provider := setupAuthFlowControllerTest(t)
	previousRegisterEnabled := common.RegisterEnabled
	previousRedisEnabled := common.RedisEnabled
	previousNewUserQuota := common.QuotaForNewUser
	common.RegisterEnabled = true
	common.RedisEnabled = false
	common.QuotaForNewUser = 0
	t.Cleanup(func() {
		common.RegisterEnabled = previousRegisterEnabled
		common.RedisEnabled = previousRedisEnabled
		common.QuotaForNewUser = previousNewUserQuota
	})

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	oauthIdentity := &oauth.OAuthUser{ProviderUserID: "github-subject"}
	first, err := findOrCreateOAuthUser(
		context,
		model.ExternalIdentityProviderGitHub,
		provider,
		oauthIdentity,
		"",
	)
	require.NoError(t, err)

	second, err := findOrCreateOAuthUser(
		context,
		model.ExternalIdentityProviderGitHub,
		provider,
		oauthIdentity,
		"",
	)
	require.NoError(t, err)
	assert.Equal(t, first.Id, second.Id)

	var users int64
	require.NoError(t, model.DB.Model(&model.User{}).Count(&users).Error)
	assert.Equal(t, int64(1), users)

	var claim model.ExternalIdentityClaim
	require.NoError(t, model.DB.Where(
		"provider = ? AND subject = ?",
		model.ExternalIdentityProviderGitHub,
		oauthIdentity.ProviderUserID,
	).First(&claim).Error)
	assert.Equal(t, first.Id, claim.UserId)
}

func TestConcurrentBuiltInOAuthRegistrationConvergesOnClaimOwner(t *testing.T) {
	provider := setupAuthFlowControllerTest(t)
	previousRegisterEnabled := common.RegisterEnabled
	previousRedisEnabled := common.RedisEnabled
	previousNewUserQuota := common.QuotaForNewUser
	common.RegisterEnabled = true
	common.RedisEnabled = false
	common.QuotaForNewUser = 0
	t.Cleanup(func() {
		common.RegisterEnabled = previousRegisterEnabled
		common.RedisEnabled = previousRedisEnabled
		common.QuotaForNewUser = previousNewUserQuota
	})

	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	provider.isUserIDTaken = func(string) bool {
		arrived <- struct{}{}
		<-release
		return false
	}

	type registrationResult struct {
		user *model.User
		err  error
	}
	results := make(chan registrationResult, 2)
	for _, username := range []string{"oauth-race-one", "oauth-race-two"} {
		username := username
		go func() {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			user, registrationErr := findOrCreateOAuthUser(
				context,
				model.ExternalIdentityProviderGitHub,
				provider,
				&oauth.OAuthUser{
					ProviderUserID: "github-concurrent-registration",
					Username:       username,
				},
				"",
			)
			results <- registrationResult{user: user, err: registrationErr}
		}()
	}

	<-arrived
	<-arrived
	close(release)
	first := <-results
	second := <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.NotNil(t, first.user)
	require.NotNil(t, second.user)
	assert.Equal(t, first.user.Id, second.user.Id)

	var users int64
	require.NoError(t, model.DB.Model(&model.User{}).Count(&users).Error)
	assert.Equal(t, int64(1), users)

	var claim model.ExternalIdentityClaim
	require.NoError(t, model.DB.Where(
		"provider = ? AND subject = ?",
		model.ExternalIdentityProviderGitHub,
		"github-concurrent-registration",
	).First(&claim).Error)
	assert.Equal(t, first.user.Id, claim.UserId)
}

func TestGenerateOAuthCodeCarriesAffiliateInLoginFlow(t *testing.T) {
	setupAuthFlowControllerTest(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/state", strings.NewReader(`{"provider":"auth-flow-test","intent":"login","aff":"invite-code"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	GenerateOAuthCode(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	flow, err := model.GetAuthFlow(response.Data.FlowToken, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
	})
	require.NoError(t, err)
	var payload oauthFlowPayload
	require.NoError(t, common.UnmarshalJsonStr(flow.Payload, &payload))
	assert.Equal(t, "invite-code", payload.AffiliateCode)
	assert.Zero(t, flow.UserId)
	assert.Empty(t, flow.SessionId)
}

func TestGenerateOAuthCodeBindsFlowToAuthenticatedSession(t *testing.T) {
	setupAuthFlowControllerTest(t)
	identity := service.AuthIdentity{
		UserID: 42, SessionID: "session-42", UserAuthVersion: 3, SessionVersion: 2,
	}
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		secureVerificationMethodPassword,
		securityProofScopeExternalBind,
		"auth-flow-test",
	)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/state", strings.NewReader(`{"provider":"auth-flow-test","intent":"bind"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Security-Proof", proof)
	c.Set("id", identity.UserID)
	c.Set("session_id", identity.SessionID)
	c.Set("auth_version", identity.UserAuthVersion)
	c.Set("session_version", identity.SessionVersion)

	GenerateOAuthCode(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	flow, err := model.GetAuthFlow(response.Data.FlowToken, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentBind,
		UserId: 42, SessionId: "session-42",
	})
	require.NoError(t, err)
	assert.Equal(t, 42, flow.UserId)
	assert.Equal(t, "session-42", flow.SessionId)
}

func TestGenerateOAuthCodeRejectsBindWithoutStepUpProof(t *testing.T) {
	setupAuthFlowControllerTest(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/state", strings.NewReader(`{"provider":"auth-flow-test","intent":"bind"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 42)
	c.Set("session_id", "session-42")
	c.Set("auth_version", int64(3))
	c.Set("session_version", int64(2))

	GenerateOAuthCode(c)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_REQUIRED")
	var count int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).
		Where("purpose = ?", model.AuthFlowPurposeOAuth).
		Count(&count).Error)
	assert.Zero(t, count)
}

func TestOAuthLoginConsumesFlowBeforeProviderRequests(t *testing.T) {
	provider := setupAuthFlowControllerTest(t)

	tests := []struct {
		name        string
		exchangeErr error
		userInfoErr error
		exchanges   int
		userInfos   int
	}{
		{name: "exchange failure", exchangeErr: errors.New("exchange failed"), exchanges: 1},
		{name: "user info failure", userInfoErr: errors.New("user info failed"), exchanges: 1, userInfos: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider.exchangeErr = test.exchangeErr
			provider.userInfoErr = test.userInfoErr
			provider.exchangeCalls = 0
			provider.userInfoCalls = 0
			token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
				Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
				Payload: `{}`, ExpiresAt: time.Now().Add(time.Minute),
			})
			require.NoError(t, err)

			router := gin.New()
			router.GET("/api/oauth/:provider", HandleOAuth)
			request := httptest.NewRequest(http.MethodGet, "/api/oauth/auth-flow-test?state="+token+"&code=test", nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			_, err = model.GetAuthFlow(token, model.AuthFlowMatch{
				Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
			})
			assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
			assert.Equal(t, test.exchanges, provider.exchangeCalls)
			assert.Equal(t, test.userInfos, provider.userInfoCalls)

			request = httptest.NewRequest(http.MethodGet, "/api/oauth/auth-flow-test?state="+token+"&code=retry", nil)
			response = httptest.NewRecorder()
			router.ServeHTTP(response, request)

			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Equal(t, test.exchanges, provider.exchangeCalls)
			assert.Equal(t, test.userInfos, provider.userInfoCalls)
		})
	}
}

func TestOAuthBindConsumesFlowBeforeProviderRequests(t *testing.T) {
	provider := setupAuthFlowControllerTest(t)
	provider.exchangeErr = errors.New("exchange failed")
	flowToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentBind,
		UserId: 42, SessionId: "session-42", Payload: `{}`, ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", 42)
		c.Set("session_id", "session-42")
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
		c.Next()
	})
	router.GET("/api/oauth/:provider", HandleOAuth)
	request := httptest.NewRequest(http.MethodGet, "/api/oauth/auth-flow-test?state="+flowToken+"&code=test", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	_, err = model.GetAuthFlow(flowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	assert.Equal(t, 1, provider.exchangeCalls)
	assert.Zero(t, provider.userInfoCalls)
}

func TestOAuthLoginConsumesFlowAfterProviderIdentityAndOnProviderError(t *testing.T) {
	provider := setupAuthFlowControllerTest(t)

	provider.exchangeErr = nil
	provider.userInfoErr = nil
	successToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
		Payload: `{invalid`, ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	router := gin.New()
	router.GET("/api/oauth/:provider", HandleOAuth)
	request := httptest.NewRequest(http.MethodGet, "/api/oauth/auth-flow-test?state="+successToken+"&code=test", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	_, err = model.GetAuthFlow(successToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	assert.Equal(t, 1, provider.exchangeCalls)
	assert.Equal(t, 1, provider.userInfoCalls)

	providerErrorToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
		Payload: `{}`, ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	request = httptest.NewRequest(http.MethodGet, "/api/oauth/auth-flow-test?state="+providerErrorToken+"&error=access_denied", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	_, err = model.GetAuthFlow(providerErrorToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	assert.Equal(t, 1, provider.exchangeCalls)
	assert.Equal(t, 1, provider.userInfoCalls)
}

func TestOAuthBindProviderErrorConsumesSessionBoundFlow(t *testing.T) {
	provider := setupAuthFlowControllerTest(t)
	flowToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentBind,
		UserId: 42, SessionId: "session-42", Payload: `{}`, ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", 42)
		c.Set("session_id", "session-42")
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
		c.Next()
	})
	router.GET("/api/oauth/:provider", HandleOAuth)
	request := httptest.NewRequest(http.MethodGet, "/api/oauth/auth-flow-test?state="+flowToken+"&error=access_denied&error_description=cancelled", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	_, err = model.GetAuthFlow(flowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	assert.Zero(t, provider.exchangeCalls)
	assert.Zero(t, provider.userInfoCalls)
}
