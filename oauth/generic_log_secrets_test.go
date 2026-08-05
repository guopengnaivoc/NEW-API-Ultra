package oauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureGenericOAuthLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	originalDebugEnabled := common.DebugEnabled
	common.DebugEnabled = true
	logBuffer := &bytes.Buffer{}

	common.LogWriterMu.Lock()
	originalErrorWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = logBuffer
	common.LogWriterMu.Unlock()

	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = originalErrorWriter
		common.LogWriterMu.Unlock()
		common.DebugEnabled = originalDebugEnabled
	})

	return logBuffer
}

type genericOAuthReadErrorTransport struct {
	err error
}

func (transport genericOAuthReadErrorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Header:        make(http.Header),
		Body:          io.NopCloser(iotest.ErrReader(transport.err)),
		ContentLength: -1,
		Request:       request,
	}, nil
}

func useGenericOAuthTransport(t *testing.T, transport http.RoundTripper) {
	t.Helper()

	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
	})
}

func TestGenericOAuthLogsExcludeCredentialsAndIdentity(t *testing.T) {
	const (
		authorizationCode = "authorization-code-secret-sentinel"
		accessToken       = "access-token-secret-sentinel"
		refreshToken      = "refresh-token-secret-sentinel"
		idToken           = "id-token-secret-sentinel"
		scope             = "scope-secret-sentinel"
		providerUserID    = "provider-user-id-pii-sentinel"
		username          = "username-pii-sentinel"
		displayName       = "display-name-pii-sentinel"
		email             = "email-pii-sentinel@example.test"
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{"access_token":"`+accessToken+
			`","refresh_token":"`+refreshToken+
			`","id_token":"`+idToken+
			`","token_type":"Bearer","scope":"`+scope+`"}`)
		assert.NoError(t, err)
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{"sub":"`+providerUserID+
			`","username":"`+username+
			`","name":"`+displayName+
			`","email":"`+email+`"}`)
		assert.NoError(t, err)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	logBuffer := captureGenericOAuthLogs(t)
	provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Name:             "Log Boundary Provider",
		Slug:             "log-boundary-provider",
		ClientId:         "client-id",
		ClientSecret:     "client-secret",
		TokenEndpoint:    server.URL + "/token",
		UserInfoEndpoint: server.URL + "/userinfo",
		UserIdField:      "sub",
		UsernameField:    "username",
		DisplayNameField: "name",
		EmailField:       "email",
	})
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())

	token, err := provider.ExchangeToken(
		context.Background(),
		authorizationCode,
		ginContext,
	)
	require.NoError(t, err)
	require.NotNil(t, token)
	user, err := provider.GetUserInfo(context.Background(), token)
	require.NoError(t, err)
	require.NotNil(t, user)

	assert.Equal(t, accessToken, token.AccessToken)
	assert.Equal(t, providerUserID, user.ProviderUserID)
	logs := logBuffer.String()
	assert.Contains(t, logs, "log-boundary-provider")
	assert.Contains(t, logs, "response_bytes=")
	for _, forbidden := range []string{
		authorizationCode,
		authorizationCode[:10],
		accessToken,
		"access-token-secret-",
		refreshToken,
		"refresh-token-secret-",
		idToken,
		"id-token-secret-",
		scope,
		"scope-secret-",
		providerUserID,
		"provider-user-id-pii-",
		username,
		"username-pii-",
		displayName,
		"display-name-pii-",
		email,
		"email-pii-",
		server.URL,
	} {
		assert.NotContains(t, logs, forbidden)
	}
}

func TestGenericOAuthPolicyDenialLogsExcludeComparedValues(t *testing.T) {
	const (
		currentTenant  = "current-tenant-pii-sentinel"
		expectedTenant = "expected-tenant-policy-sentinel"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(
			w,
			`{"sub":"provider-user","tenant":"`+currentTenant+`"}`,
		)
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	logBuffer := captureGenericOAuthLogs(t)
	provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Name:             "Policy Log Boundary Provider",
		Slug:             "policy-log-boundary-provider",
		UserInfoEndpoint: server.URL,
		UserIdField:      "sub",
		AccessPolicy: `{
			"logic":"and",
			"conditions":[{
				"field":"tenant",
				"op":"eq",
				"value":"` + expectedTenant + `"
			}]
		}`,
	})

	user, err := provider.GetUserInfo(context.Background(), &OAuthToken{
		AccessToken: "policy-access-token-secret-sentinel",
		TokenType:   "Bearer",
	})

	assert.Nil(t, user)
	require.Error(t, err)
	var deniedError *AccessDeniedError
	assert.ErrorAs(t, err, &deniedError)
	logs := logBuffer.String()
	assert.Contains(t, logs, "access denied by policy")
	assert.NotContains(t, logs, currentTenant)
	assert.NotContains(t, logs, expectedTenant)
	assert.NotContains(t, logs, server.URL)
}

func TestGenericOAuthFailureLogsExcludeProviderPayloadAndEndpoint(t *testing.T) {
	const (
		authorizationCode  = "failure-authorization-code-secret-sentinel"
		providerError      = "provider-error-secret-sentinel"
		providerErrorDesc  = "provider-error-description-secret-sentinel"
		invalidPolicyValue = "invalid-policy-value-secret-sentinel"
	)

	t.Run("provider error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := io.WriteString(
				w,
				`{"error":"`+providerError+`","error_description":"`+providerErrorDesc+`"}`,
			)
			assert.NoError(t, err)
		}))
		t.Cleanup(server.Close)

		logBuffer := captureGenericOAuthLogs(t)
		provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
			Name:          "Provider Error Log Boundary",
			Slug:          "provider-error-log-boundary",
			ClientId:      "client-id",
			ClientSecret:  "client-secret",
			TokenEndpoint: server.URL,
		})
		ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())

		token, err := provider.ExchangeToken(
			context.Background(),
			authorizationCode,
			ginContext,
		)

		assert.Nil(t, token)
		require.Error(t, err)
		logs := logBuffer.String()
		assert.Contains(t, logs, "category=provider_error")
		assert.NotContains(t, logs, authorizationCode[:10])
		assert.NotContains(t, logs, providerError)
		assert.NotContains(t, logs, providerErrorDesc)
		assert.NotContains(t, logs, server.URL)
	})

	t.Run("transport error", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		endpoint := server.URL + "/token-endpoint-secret-sentinel"
		server.Close()

		logBuffer := captureGenericOAuthLogs(t)
		provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
			Name:          "Transport Error Log Boundary",
			Slug:          "transport-error-log-boundary",
			ClientId:      "client-id",
			ClientSecret:  "client-secret",
			TokenEndpoint: endpoint,
		})
		ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())

		token, err := provider.ExchangeToken(
			context.Background(),
			authorizationCode,
			ginContext,
		)

		assert.Nil(t, token)
		require.Error(t, err)
		logs := logBuffer.String()
		assert.Contains(t, logs, "category=transport")
		assert.NotContains(t, logs, authorizationCode[:10])
		assert.NotContains(t, logs, server.URL)
		assert.NotContains(t, logs, "token-endpoint-secret-sentinel")
	})

	t.Run("invalid policy", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := io.WriteString(w, `{"sub":"provider-user"}`)
			assert.NoError(t, err)
		}))
		t.Cleanup(server.Close)

		logBuffer := captureGenericOAuthLogs(t)
		provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
			Name:             "Invalid Policy Log Boundary",
			Slug:             "invalid-policy-log-boundary",
			UserInfoEndpoint: server.URL,
			UserIdField:      "sub",
			AccessPolicy: `{
				"logic":"and",
				"conditions":[{
					"field":"tenant",
					"op":"` + invalidPolicyValue + `",
					"value":"expected"
				}]
			}`,
		})

		user, err := provider.GetUserInfo(context.Background(), &OAuthToken{
			AccessToken: "invalid-policy-access-token-secret-sentinel",
			TokenType:   "Bearer",
		})

		assert.Nil(t, user)
		require.Error(t, err)
		logs := logBuffer.String()
		assert.Contains(t, logs, "category=invalid_access_policy")
		assert.NotContains(t, logs, invalidPolicyValue)
		assert.NotContains(t, logs, server.URL)
	})
}

func TestGenericOAuthTokenResponseReadLogsExcludeRawError(t *testing.T) {
	const (
		readErrorSentinel = "token-response-read-error-secret-sentinel"
		partialSentinel   = "token-response-read-error-secret-"
	)

	useGenericOAuthTransport(t, genericOAuthReadErrorTransport{
		err: errors.New(readErrorSentinel),
	})
	logBuffer := captureGenericOAuthLogs(t)
	provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Name:          "Token Response Read Log Boundary",
		Slug:          "token-response-read-log-boundary",
		ClientId:      "client-id",
		ClientSecret:  "client-secret",
		TokenEndpoint: "https://token-response-read.example.test",
	})
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())

	token, err := provider.ExchangeToken(
		context.Background(),
		"token-response-read-authorization-code-secret-sentinel",
		ginContext,
	)

	assert.Nil(t, token)
	require.Error(t, err)
	logs := logBuffer.String()
	assert.Contains(t, logs, "category=response_read")
	assert.NotContains(t, logs, readErrorSentinel)
	assert.NotContains(t, logs, partialSentinel)
}

func TestGenericOAuthTokenResponseParseLogsExcludeRawError(t *testing.T) {
	const (
		responseBody    = "%ZZ-token-response-parse-secret-sentinel"
		partialSentinel = "token-response-parse-secret-"
		rawParseError   = "invalid character '%'"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, responseBody)
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	logBuffer := captureGenericOAuthLogs(t)
	provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Name:          "Token Response Parse Log Boundary",
		Slug:          "token-response-parse-log-boundary",
		ClientId:      "client-id",
		ClientSecret:  "client-secret",
		TokenEndpoint: server.URL,
	})
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())

	token, err := provider.ExchangeToken(
		context.Background(),
		"token-response-parse-authorization-code-secret-sentinel",
		ginContext,
	)

	assert.Nil(t, token)
	require.Error(t, err)
	logs := logBuffer.String()
	assert.Contains(t, logs, "category=response_parse")
	assert.NotContains(t, logs, responseBody)
	assert.NotContains(t, logs, partialSentinel)
	assert.NotContains(t, logs, rawParseError)
}

func TestGenericOAuthUserInfoTransportLogsExcludeRawErrorAndEndpoint(t *testing.T) {
	const (
		endpointSentinel = "userinfo-transport-endpoint-secret-sentinel"
		partialSentinel  = "userinfo-transport-endpoint-secret-"
	)

	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL + "/" + endpointSentinel
	server.Close()

	logBuffer := captureGenericOAuthLogs(t)
	provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Name:             "UserInfo Transport Log Boundary",
		Slug:             "userinfo-transport-log-boundary",
		UserInfoEndpoint: endpoint,
		UserIdField:      "sub",
	})

	user, err := provider.GetUserInfo(context.Background(), &OAuthToken{
		AccessToken: "userinfo-transport-access-token-secret-sentinel",
		TokenType:   "Bearer",
	})

	assert.Nil(t, user)
	require.Error(t, err)
	logs := logBuffer.String()
	assert.Contains(t, logs, "category=transport")
	assert.NotContains(t, logs, endpointSentinel)
	assert.NotContains(t, logs, partialSentinel)
	assert.NotContains(t, logs, server.URL)
}

func TestGenericOAuthUserInfoResponseReadLogsExcludeRawError(t *testing.T) {
	const (
		readErrorSentinel = "userinfo-response-read-error-secret-sentinel"
		partialSentinel   = "userinfo-response-read-error-secret-"
	)

	useGenericOAuthTransport(t, genericOAuthReadErrorTransport{
		err: errors.New(readErrorSentinel),
	})
	logBuffer := captureGenericOAuthLogs(t)
	provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Name:             "UserInfo Response Read Log Boundary",
		Slug:             "userinfo-response-read-log-boundary",
		UserInfoEndpoint: "https://userinfo-response-read.example.test",
		UserIdField:      "sub",
	})

	user, err := provider.GetUserInfo(context.Background(), &OAuthToken{
		AccessToken: "userinfo-response-read-access-token-secret-sentinel",
		TokenType:   "Bearer",
	})

	assert.Nil(t, user)
	require.Error(t, err)
	logs := logBuffer.String()
	assert.Contains(t, logs, "category=response_read")
	assert.NotContains(t, logs, readErrorSentinel)
	assert.NotContains(t, logs, partialSentinel)
}

func TestGenericOAuthEmptyUserIDLogsExcludeConfiguredField(t *testing.T) {
	const (
		userIDFieldSentinel = "user-id-field-secret-sentinel"
		partialSentinel     = "user-id-field-secret-"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{}`)
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	logBuffer := captureGenericOAuthLogs(t)
	provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Name:             "Empty User ID Log Boundary",
		Slug:             "empty-user-id-log-boundary",
		UserInfoEndpoint: server.URL,
		UserIdField:      userIDFieldSentinel,
	})

	user, err := provider.GetUserInfo(context.Background(), &OAuthToken{
		AccessToken: "empty-user-id-access-token-secret-sentinel",
		TokenType:   "Bearer",
	})

	assert.Nil(t, user)
	require.Error(t, err)
	logs := logBuffer.String()
	assert.Contains(t, logs, "category=empty_user_id")
	assert.NotContains(t, logs, userIDFieldSentinel)
	assert.NotContains(t, logs, partialSentinel)
}
