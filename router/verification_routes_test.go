package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var verificationRouteClientSequence atomic.Uint32

func TestVerificationOpenAPIContract(t *testing.T) {
	artifact, err := os.ReadFile(filepath.Join("..", "docs", "openapi", "api.json"))
	require.NoError(t, err)

	var document struct {
		Paths map[string]map[string]map[string]any `json:"paths"`
	}
	require.NoError(t, common.Unmarshal(artifact, &document))

	for _, path := range []string{"/api/verification", "/api/reset_password"} {
		operations, ok := document.Paths[path]
		require.True(t, ok, "missing %s", path)
		assert.Contains(t, operations, "post")
		assert.NotContains(t, operations, "get")

		operation, ok := operations["post"]
		require.True(t, ok, "missing POST %s", path)
		security, ok := operation["security"].([]any)
		require.True(t, ok, "POST %s must explicitly define operation security", path)
		assert.Empty(t, security, "POST %s must be anonymous", path)
		requestBody, ok := operation["requestBody"].(map[string]any)
		require.True(t, ok, "%s must define a request body", path)
		assert.Equal(t, true, requestBody["required"])
		content, ok := requestBody["content"].(map[string]any)
		require.True(t, ok, "%s request body must define content", path)
		jsonContent, ok := content["application/json"].(map[string]any)
		require.True(t, ok, "%s request body must accept application/json", path)
		schema, ok := jsonContent["schema"].(map[string]any)
		require.True(t, ok, "%s JSON body must define a schema", path)
		properties, ok := schema["properties"].(map[string]any)
		require.True(t, ok, "%s JSON body must define properties", path)
		assert.Contains(t, properties, "email")
		emailSchema, ok := properties["email"].(map[string]any)
		require.True(t, ok, "%s email property must define a schema", path)
		assert.Equal(t, "string", emailSchema["type"])
		required, ok := schema["required"].([]any)
		require.True(t, ok, "%s JSON body must explicitly require fields", path)
		assert.ElementsMatch(t, []any{"email"}, required)

		parameters, ok := operation["parameters"].([]any)
		require.True(t, ok, "%s must define parameters", path)
		turnstileHeaderFound := false
		for _, rawParameter := range parameters {
			parameter, ok := rawParameter.(map[string]any)
			require.True(t, ok, "%s parameter must be an object", path)
			name, _ := parameter["name"].(string)
			location, _ := parameter["in"].(string)
			if location == "query" {
				assert.NotContains(t, []string{"email", "X-Turnstile-Token"}, name)
			}
			if name == "X-Turnstile-Token" && location == "header" {
				turnstileHeaderFound = true
				assert.Equal(t, false, parameter["required"])
				headerSchema, ok := parameter["schema"].(map[string]any)
				require.True(t, ok, "%s Turnstile header must define a schema", path)
				assert.Equal(t, "string", headerSchema["type"])
			}
		}
		assert.True(t, turnstileHeaderFound, "%s must document X-Turnstile-Token", path)
	}

	resetOperations, ok := document.Paths["/api/user/reset"]
	require.True(t, ok, "missing /api/user/reset")
	resetOperation, ok := resetOperations["post"]
	require.True(t, ok, "missing POST /api/user/reset")
	resetSecurity, ok := resetOperation["security"].([]any)
	require.True(t, ok, "POST /api/user/reset must explicitly define operation security")
	assert.Empty(t, resetSecurity, "POST /api/user/reset must be anonymous")
	resetRequestBody, ok := resetOperation["requestBody"].(map[string]any)
	require.True(t, ok, "/api/user/reset must define a request body")
	assert.Equal(t, true, resetRequestBody["required"])
	resetContent, ok := resetRequestBody["content"].(map[string]any)
	require.True(t, ok, "/api/user/reset request body must define content")
	resetJSONContent, ok := resetContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/user/reset request body must accept application/json")
	resetSchema, ok := resetJSONContent["schema"].(map[string]any)
	require.True(t, ok, "/api/user/reset JSON body must define a schema")
	assert.Equal(t, false, resetSchema["additionalProperties"])
	resetProperties, ok := resetSchema["properties"].(map[string]any)
	require.True(t, ok, "/api/user/reset JSON body must define properties")
	assert.Len(t, resetProperties, 2)
	assert.Contains(t, resetProperties, "token")
	assert.Contains(t, resetProperties, "password")
	assert.NotContains(t, resetProperties, "email")
	resetRequired, ok := resetSchema["required"].([]any)
	require.True(t, ok, "/api/user/reset JSON body must explicitly require fields")
	assert.ElementsMatch(t, []any{"token", "password"}, resetRequired)
	passwordSchema, ok := resetProperties["password"].(map[string]any)
	require.True(t, ok, "/api/user/reset password must define a schema")
	assert.Equal(t, "password", passwordSchema["format"])
	assert.Equal(t, float64(8), passwordSchema["minLength"])
	assert.Equal(t, float64(20), passwordSchema["maxLength"])
	assert.Equal(
		t,
		"新密码必须为 8–20 个 Unicode 字符，且 UTF-8 编码不得超过 72 字节。",
		passwordSchema["description"],
	)
	assert.Equal(t, float64(72), passwordSchema["x-max-utf8-bytes"])

	responses, ok := resetOperation["responses"].(map[string]any)
	require.True(t, ok, "/api/user/reset must document responses")
	successResponse, ok := responses["200"].(map[string]any)
	require.True(t, ok, "/api/user/reset must document a 200 response")
	successContent, ok := successResponse["content"].(map[string]any)
	require.True(t, ok, "/api/user/reset 200 response must define content")
	successJSONContent, ok := successContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/user/reset 200 response must define application/json")
	successSchema, ok := successJSONContent["schema"].(map[string]any)
	require.True(t, ok, "/api/user/reset 200 response must define a schema")
	assert.Equal(t, "object", successSchema["type"])
	assert.Equal(t, false, successSchema["additionalProperties"])
	successRequired, ok := successSchema["required"].([]any)
	require.True(t, ok, "/api/user/reset 200 response must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "message"}, successRequired)
	successProperties, ok := successSchema["properties"].(map[string]any)
	require.True(t, ok, "/api/user/reset 200 response must define properties")
	assert.Len(t, successProperties, 2)
	successValue, ok := successProperties["success"].(map[string]any)
	require.True(t, ok, "/api/user/reset 200 response must define success")
	assert.Equal(t, "boolean", successValue["type"])
	messageValue, ok := successProperties["message"].(map[string]any)
	require.True(t, ok, "/api/user/reset 200 response must define message")
	assert.Equal(t, "string", messageValue["type"])
	assert.NotContains(t, successProperties, "data")
	assert.NotContains(t, successProperties, "password")
	assert.NotContains(t, successProperties, "generated_password")
	assert.NotContains(t, successProperties, "secret")
}

func TestChannelKeySecurityProofOpenAPIContract(t *testing.T) {
	artifact, err := os.ReadFile(filepath.Join("..", "docs", "openapi", "api.json"))
	require.NoError(t, err)

	var document struct {
		Paths map[string]map[string]map[string]any `json:"paths"`
		Components struct {
			SecuritySchemes map[string]map[string]any `json:"securitySchemes"`
		} `json:"components"`
	}
	require.NoError(t, common.Unmarshal(artifact, &document))

	verifyOperations, ok := document.Paths["/api/verify"]
	require.True(t, ok, "missing /api/verify")
	verify, ok := verifyOperations["post"]
	require.True(t, ok, "missing POST /api/verify")
	verifyRequestBody, ok := verify["requestBody"].(map[string]any)
	require.True(t, ok, "/api/verify must define a request body")
	assert.Equal(t, true, verifyRequestBody["required"])
	verifyContent, ok := verifyRequestBody["content"].(map[string]any)
	require.True(t, ok, "/api/verify request body must define content")
	verifyJSON, ok := verifyContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/verify must accept application/json")
	verifySchema, ok := verifyJSON["schema"].(map[string]any)
	require.True(t, ok, "/api/verify JSON body must define a schema")
	verifyRequired, ok := verifySchema["required"].([]any)
	require.True(t, ok, "/api/verify JSON body must explicitly require fields")
	assert.Contains(t, verifyRequired, "method")
	assert.Contains(t, verifyRequired, "scope")
	assert.Contains(t, verifyRequired, "target")
	verifyProperties, ok := verifySchema["properties"].(map[string]any)
	require.True(t, ok, "/api/verify JSON body must define properties")
	verifyTarget, ok := verifyProperties["target"].(map[string]any)
	require.True(t, ok, "/api/verify target must define a schema")
	assert.Equal(t, "string", verifyTarget["type"])
	assert.Equal(t, float64(1), verifyTarget["minLength"])
	verifyResponses, ok := verify["responses"].(map[string]any)
	require.True(t, ok, "/api/verify must define responses")
	verifySuccessResponse, ok := verifyResponses["200"].(map[string]any)
	require.True(t, ok, "/api/verify must document a 200 response")
	verifySuccessContent, ok := verifySuccessResponse["content"].(map[string]any)
	require.True(t, ok, "/api/verify 200 response must define content")
	verifySuccessJSON, ok := verifySuccessContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/verify 200 response must define application/json")
	verifyResponseSchema, ok := verifySuccessJSON["schema"].(map[string]any)
	require.True(t, ok, "/api/verify 200 response must define a schema")
	verifyResponseVariants, ok := verifyResponseSchema["oneOf"].([]any)
	require.True(t, ok, "/api/verify 200 response must define success and failure variants")
	require.Len(t, verifyResponseVariants, 2)
	verifySuccessSchema, ok := verifyResponseVariants[0].(map[string]any)
	require.True(t, ok, "/api/verify 200 success variant must be an object")
	verifySuccessProperties, ok := verifySuccessSchema["properties"].(map[string]any)
	require.True(t, ok, "/api/verify 200 success variant must define properties")
	verifySuccessRequired, ok := verifySuccessSchema["required"].([]any)
	require.True(t, ok, "/api/verify 200 success variant must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "message", "data"}, verifySuccessRequired)
	verifySuccessFlag, ok := verifySuccessProperties["success"].(map[string]any)
	require.True(t, ok, "/api/verify 200 success flag must define a schema")
	assert.ElementsMatch(t, []any{true}, verifySuccessFlag["enum"])
	assert.NotContains(t, verifySuccessFlag, "const")
	verifyProofData, ok := verifySuccessProperties["data"].(map[string]any)
	require.True(t, ok, "/api/verify 200 response must define proof data")
	verifyProofProperties, ok := verifyProofData["properties"].(map[string]any)
	require.True(t, ok, "/api/verify proof data must define properties")
	for _, field := range []string{"proof_token", "expires_at", "method", "scope", "target"} {
		assert.Contains(t, verifyProofProperties, field)
	}
	verifyProofRequired, ok := verifyProofData["required"].([]any)
	require.True(t, ok, "/api/verify proof data must explicitly require fields")
	assert.ElementsMatch(t, []any{"proof_token", "expires_at", "method", "scope", "target"}, verifyProofRequired)
	verifyProofToken, ok := verifyProofProperties["proof_token"].(map[string]any)
	require.True(t, ok, "/api/verify proof token must define a schema")
	assert.Equal(t, "string", verifyProofToken["type"])
	assert.Equal(t, float64(1), verifyProofToken["minLength"])
	verifyProofExpiry, ok := verifyProofProperties["expires_at"].(map[string]any)
	require.True(t, ok, "/api/verify proof expiry must define a schema")
	assert.Equal(t, "integer", verifyProofExpiry["type"])
	verifyFailureSchema, ok := verifyResponseVariants[1].(map[string]any)
	require.True(t, ok, "/api/verify 200 failure variant must be an object")
	verifyFailureRequired, ok := verifyFailureSchema["required"].([]any)
	require.True(t, ok, "/api/verify 200 failure variant must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "message"}, verifyFailureRequired)
	assert.NotContains(t, verifyFailureRequired, "data")
	verifyFailureProperties, ok := verifyFailureSchema["properties"].(map[string]any)
	require.True(t, ok, "/api/verify 200 failure variant must define properties")
	verifyFailureFlag, ok := verifyFailureProperties["success"].(map[string]any)
	require.True(t, ok, "/api/verify 200 failure flag must define a schema")
	assert.ElementsMatch(t, []any{false}, verifyFailureFlag["enum"])
	assert.NotContains(t, verifyFailureFlag, "const")
	verifyFailureMessage, ok := verifyFailureProperties["message"].(map[string]any)
	require.True(t, ok, "/api/verify 200 failure message must define a schema")
	assert.Equal(t, "string", verifyFailureMessage["type"])

	verifyUnauthorized, ok := verifyResponses["401"].(map[string]any)
	require.True(t, ok, "/api/verify must document a 401 response")
	assert.NotEmpty(t, verifyUnauthorized["description"])
	verifyUnauthorizedContent, ok := verifyUnauthorized["content"].(map[string]any)
	require.True(t, ok, "/api/verify 401 response must define content")
	verifyUnauthorizedJSON, ok := verifyUnauthorizedContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/verify 401 response must define application/json")
	verifyUnauthorizedSchema, ok := verifyUnauthorizedJSON["schema"].(map[string]any)
	require.True(t, ok, "/api/verify 401 response must define a schema")
	verifyUnauthorizedRequired, ok := verifyUnauthorizedSchema["required"].([]any)
	require.True(t, ok, "/api/verify 401 response must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "message"}, verifyUnauthorizedRequired)
	verifyUnauthorizedProperties, ok := verifyUnauthorizedSchema["properties"].(map[string]any)
	require.True(t, ok, "/api/verify 401 response must define properties")
	verifyUnauthorizedFlag, ok := verifyUnauthorizedProperties["success"].(map[string]any)
	require.True(t, ok, "/api/verify 401 success flag must define a schema")
	assert.ElementsMatch(t, []any{false}, verifyUnauthorizedFlag["enum"])
	assert.Contains(t, verifyUnauthorizedProperties, "code")
	assert.Contains(t, verifyUnauthorizedProperties, "message")

	verifyRateLimited, ok := verifyResponses["429"].(map[string]any)
	require.True(t, ok, "/api/verify must document a 429 response")
	assert.NotEmpty(t, verifyRateLimited["description"])
	assert.NotContains(t, verifyRateLimited, "content")
	verifyRateLimitHeaders, ok := verifyRateLimited["headers"].(map[string]any)
	require.True(t, ok, "/api/verify 429 response must define headers")
	verifyRetryAfter, ok := verifyRateLimitHeaders["Retry-After"].(map[string]any)
	require.True(t, ok, "/api/verify 429 response must document Retry-After")
	verifyRetryAfterSchema, ok := verifyRetryAfter["schema"].(map[string]any)
	require.True(t, ok, "/api/verify Retry-After must define a schema")
	assert.Equal(t, "integer", verifyRetryAfterSchema["type"])
	assert.Equal(t, float64(1), verifyRetryAfterSchema["minimum"])

	passkeyBeginOperations, ok := document.Paths["/api/user/passkey/verify/begin"]
	require.True(t, ok, "missing /api/user/passkey/verify/begin")
	passkeyBegin, ok := passkeyBeginOperations["post"]
	require.True(t, ok, "missing POST /api/user/passkey/verify/begin")
	passkeyBeginRequestBody, ok := passkeyBegin["requestBody"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/begin must define a request body")
	assert.Equal(t, true, passkeyBeginRequestBody["required"])
	passkeyBeginContent, ok := passkeyBeginRequestBody["content"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/begin request body must define content")
	passkeyBeginJSON, ok := passkeyBeginContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/begin must accept application/json")
	passkeyBeginSchema, ok := passkeyBeginJSON["schema"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/begin JSON body must define a schema")
	passkeyBeginRequired, ok := passkeyBeginSchema["required"].([]any)
	require.True(t, ok, "/api/user/passkey/verify/begin JSON body must explicitly require fields")
	assert.Contains(t, passkeyBeginRequired, "scope")
	assert.Contains(t, passkeyBeginRequired, "target")

	passkeyFinishOperations, ok := document.Paths["/api/user/passkey/verify/finish"]
	require.True(t, ok, "missing /api/user/passkey/verify/finish")
	passkeyFinish, ok := passkeyFinishOperations["post"]
	require.True(t, ok, "missing POST /api/user/passkey/verify/finish")
	passkeyFinishResponses, ok := passkeyFinish["responses"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish must define responses")
	passkeyFinishSuccessResponse, ok := passkeyFinishResponses["200"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish must document a 200 response")
	passkeyFinishSuccessContent, ok := passkeyFinishSuccessResponse["content"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 response must define content")
	passkeyFinishSuccessJSON, ok := passkeyFinishSuccessContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 response must define application/json")
	passkeyFinishResponseSchema, ok := passkeyFinishSuccessJSON["schema"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 response must define a schema")
	passkeyFinishResponseVariants, ok := passkeyFinishResponseSchema["oneOf"].([]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 response must define success and failure variants")
	require.Len(t, passkeyFinishResponseVariants, 2)
	passkeyFinishSuccessSchema, ok := passkeyFinishResponseVariants[0].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 success variant must be an object")
	passkeyFinishSuccessProperties, ok := passkeyFinishSuccessSchema["properties"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 success variant must define properties")
	passkeyFinishSuccessRequired, ok := passkeyFinishSuccessSchema["required"].([]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 success variant must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "message", "data"}, passkeyFinishSuccessRequired)
	passkeyFinishSuccessFlag, ok := passkeyFinishSuccessProperties["success"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 success flag must define a schema")
	assert.ElementsMatch(t, []any{true}, passkeyFinishSuccessFlag["enum"])
	assert.NotContains(t, passkeyFinishSuccessFlag, "const")
	passkeyFinishProofData, ok := passkeyFinishSuccessProperties["data"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 response must define proof data")
	passkeyFinishProofProperties, ok := passkeyFinishProofData["properties"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish proof data must define properties")
	for _, field := range []string{"proof_token", "expires_at", "method", "scope", "target"} {
		assert.Contains(t, passkeyFinishProofProperties, field)
	}
	passkeyFinishProofRequired, ok := passkeyFinishProofData["required"].([]any)
	require.True(t, ok, "/api/user/passkey/verify/finish proof data must explicitly require fields")
	assert.ElementsMatch(t, []any{"proof_token", "expires_at", "method", "scope", "target"}, passkeyFinishProofRequired)
	passkeyFinishProofToken, ok := passkeyFinishProofProperties["proof_token"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish proof token must define a schema")
	assert.Equal(t, "string", passkeyFinishProofToken["type"])
	assert.Equal(t, float64(1), passkeyFinishProofToken["minLength"])
	passkeyFinishProofExpiry, ok := passkeyFinishProofProperties["expires_at"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish proof expiry must define a schema")
	assert.Equal(t, "integer", passkeyFinishProofExpiry["type"])
	passkeyFinishFailureSchema, ok := passkeyFinishResponseVariants[1].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 failure variant must be an object")
	passkeyFinishFailureRequired, ok := passkeyFinishFailureSchema["required"].([]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 failure variant must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "message"}, passkeyFinishFailureRequired)
	assert.NotContains(t, passkeyFinishFailureRequired, "data")
	passkeyFinishFailureProperties, ok := passkeyFinishFailureSchema["properties"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 failure variant must define properties")
	passkeyFinishFailureFlag, ok := passkeyFinishFailureProperties["success"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 failure flag must define a schema")
	assert.ElementsMatch(t, []any{false}, passkeyFinishFailureFlag["enum"])
	assert.NotContains(t, passkeyFinishFailureFlag, "const")
	passkeyFinishFailureMessage, ok := passkeyFinishFailureProperties["message"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 200 failure message must define a schema")
	assert.Equal(t, "string", passkeyFinishFailureMessage["type"])

	passkeyFinishUnauthorized, ok := passkeyFinishResponses["401"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish must document a 401 response")
	assert.NotEmpty(t, passkeyFinishUnauthorized["description"])
	passkeyFinishUnauthorizedContent, ok := passkeyFinishUnauthorized["content"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 401 response must define content")
	passkeyFinishUnauthorizedJSON, ok := passkeyFinishUnauthorizedContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 401 response must define application/json")
	passkeyFinishUnauthorizedSchema, ok := passkeyFinishUnauthorizedJSON["schema"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 401 response must define a schema")
	passkeyFinishUnauthorizedRequired, ok := passkeyFinishUnauthorizedSchema["required"].([]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 401 response must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "message"}, passkeyFinishUnauthorizedRequired)
	passkeyFinishUnauthorizedProperties, ok := passkeyFinishUnauthorizedSchema["properties"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 401 response must define properties")
	passkeyFinishUnauthorizedFlag, ok := passkeyFinishUnauthorizedProperties["success"].(map[string]any)
	require.True(t, ok, "/api/user/passkey/verify/finish 401 success flag must define a schema")
	assert.ElementsMatch(t, []any{false}, passkeyFinishUnauthorizedFlag["enum"])
	assert.Contains(t, passkeyFinishUnauthorizedProperties, "code")
	assert.Contains(t, passkeyFinishUnauthorizedProperties, "message")

	channelKeyOperations, ok := document.Paths["/api/channel/{id}/key"]
	require.True(t, ok, "missing /api/channel/{id}/key")
	channelKey, ok := channelKeyOperations["post"]
	require.True(t, ok, "missing POST /api/channel/{id}/key")
	channelKeyDescription, ok := channelKey["description"].(string)
	require.True(t, ok, "/api/channel/{id}/key must define a description")
	assert.Contains(t, channelKeyDescription, "Root")
	assert.Contains(t, channelKeyDescription, "super-admin")
	assert.Contains(t, channelKeyDescription, "one-time")
	assert.Contains(t, channelKeyDescription, "channel.key.read")
	assert.Contains(t, channelKeyDescription, "exact positive canonical decimal channel ID")
	assert.Contains(t, channelKeyDescription, "2FA")
	assert.Contains(t, channelKeyDescription, "Passkey")

	channelKeyParameters, ok := channelKey["parameters"].([]any)
	require.True(t, ok, "/api/channel/{id}/key must define parameters")
	var idParameter, proofParameter map[string]any
	for _, rawParameter := range channelKeyParameters {
		parameter, ok := rawParameter.(map[string]any)
		require.True(t, ok, "/api/channel/{id}/key parameter must be an object")
		switch parameter["name"] {
		case "id":
			idParameter = parameter
		case "X-Security-Proof":
			proofParameter = parameter
		}
	}
	require.NotNil(t, idParameter, "/api/channel/{id}/key must define id")
	assert.Equal(t, "path", idParameter["in"])
	assert.Equal(t, true, idParameter["required"])
	idSchema, ok := idParameter["schema"].(map[string]any)
	require.True(t, ok, "channel ID must define a schema")
	assert.Equal(t, "integer", idSchema["type"])
	assert.Equal(t, float64(1), idSchema["minimum"])
	require.NotNil(t, proofParameter, "/api/channel/{id}/key must require X-Security-Proof")
	assert.Equal(t, "header", proofParameter["in"])
	assert.Equal(t, true, proofParameter["required"])
	proofSchema, ok := proofParameter["schema"].(map[string]any)
	require.True(t, ok, "X-Security-Proof must define a schema")
	assert.Equal(t, "string", proofSchema["type"])

	channelKeyResponses, ok := channelKey["responses"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key must define responses")
	channelKeyOK, ok := channelKeyResponses["200"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key must document a 200 response")
	channelKeyOKContent, ok := channelKeyOK["content"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 response must define content")
	channelKeyOKJSON, ok := channelKeyOKContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 response must define application/json")
	channelKeyOKSchema, ok := channelKeyOKJSON["schema"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 response must define a schema")
	channelKeyOKVariants, ok := channelKeyOKSchema["oneOf"].([]any)
	require.True(t, ok, "/api/channel/{id}/key 200 response must define success and failure variants")
	require.Len(t, channelKeyOKVariants, 2)
	channelKeySuccess, ok := channelKeyOKVariants[0].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 success variant must be an object")
	channelKeySuccessRequired, ok := channelKeySuccess["required"].([]any)
	require.True(t, ok, "/api/channel/{id}/key 200 success variant must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "message", "data"}, channelKeySuccessRequired)
	channelKeySuccessProperties, ok := channelKeySuccess["properties"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 success variant must define properties")
	channelKeySuccessFlag, ok := channelKeySuccessProperties["success"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 success flag must define a schema")
	assert.ElementsMatch(t, []any{true}, channelKeySuccessFlag["enum"])
	channelKeySuccessData, ok := channelKeySuccessProperties["data"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 success data must define a schema")
	channelKeySuccessDataRequired, ok := channelKeySuccessData["required"].([]any)
	require.True(t, ok, "/api/channel/{id}/key 200 success data must explicitly require fields")
	assert.ElementsMatch(t, []any{"key"}, channelKeySuccessDataRequired)
	channelKeySuccessDataProperties, ok := channelKeySuccessData["properties"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 success data must define properties")
	channelKeyValue, ok := channelKeySuccessDataProperties["key"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 key must define a schema")
	assert.Equal(t, "string", channelKeyValue["type"])
	channelKeyFailure, ok := channelKeyOKVariants[1].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 failure variant must be an object")
	channelKeyFailureRequired, ok := channelKeyFailure["required"].([]any)
	require.True(t, ok, "/api/channel/{id}/key 200 failure variant must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "message"}, channelKeyFailureRequired)
	assert.NotContains(t, channelKeyFailureRequired, "data")
	channelKeyFailureProperties, ok := channelKeyFailure["properties"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 failure variant must define properties")
	channelKeyFailureFlag, ok := channelKeyFailureProperties["success"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 failure flag must define a schema")
	assert.ElementsMatch(t, []any{false}, channelKeyFailureFlag["enum"])
	channelKeyFailureMessage, ok := channelKeyFailureProperties["message"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 200 failure message must define a schema")
	assert.Equal(t, "string", channelKeyFailureMessage["type"])

	channelKeyUnauthorized, ok := channelKeyResponses["401"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key must document a 401 response")
	assert.NotEmpty(t, channelKeyUnauthorized["description"])
	channelKeyUnauthorizedContent, ok := channelKeyUnauthorized["content"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 401 response must define content")
	channelKeyUnauthorizedJSON, ok := channelKeyUnauthorizedContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 401 response must define application/json")
	channelKeyUnauthorizedSchema, ok := channelKeyUnauthorizedJSON["schema"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 401 response must define a schema")
	channelKeyUnauthorizedRequired, ok := channelKeyUnauthorizedSchema["required"].([]any)
	require.True(t, ok, "/api/channel/{id}/key 401 response must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "code", "message"}, channelKeyUnauthorizedRequired)
	channelKeyUnauthorizedProperties, ok := channelKeyUnauthorizedSchema["properties"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 401 response must define properties")
	channelKeyUnauthorizedFlag, ok := channelKeyUnauthorizedProperties["success"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 401 success flag must define a schema")
	assert.ElementsMatch(t, []any{false}, channelKeyUnauthorizedFlag["enum"])
	channelKeyUnauthorizedCode, ok := channelKeyUnauthorizedProperties["code"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 401 code must define a schema")
	assert.ElementsMatch(t, []any{
		"AUTH_TOKEN_EXPIRED",
		"AUTH_SESSION_REVOKED",
		"AUTH_UNAUTHORIZED",
		"AUTH_USER_DISABLED",
		"AUTH_USER_INVALID",
	}, channelKeyUnauthorizedCode["enum"])

	channelKeyForbidden, ok := channelKeyResponses["403"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key must document a 403 response")
	assert.NotEmpty(t, channelKeyForbidden["description"])
	channelKeyForbiddenContent, ok := channelKeyForbidden["content"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 403 response must define content")
	channelKeyForbiddenJSON, ok := channelKeyForbiddenContent["application/json"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 403 response must define application/json")
	channelKeyForbiddenSchema, ok := channelKeyForbiddenJSON["schema"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 403 response must define a schema")
	channelKeyForbiddenRequired, ok := channelKeyForbiddenSchema["required"].([]any)
	require.True(t, ok, "/api/channel/{id}/key 403 response must explicitly require fields")
	assert.ElementsMatch(t, []any{"success", "code", "message"}, channelKeyForbiddenRequired)
	channelKeyForbiddenProperties, ok := channelKeyForbiddenSchema["properties"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 403 response must define properties")
	channelKeyForbiddenFlag, ok := channelKeyForbiddenProperties["success"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 403 success flag must define a schema")
	assert.ElementsMatch(t, []any{false}, channelKeyForbiddenFlag["enum"])
	channelKeyForbiddenCode, ok := channelKeyForbiddenProperties["code"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 403 code must define a schema")
	assert.ElementsMatch(t, []any{
		"AUTH_INSUFFICIENT_PRIVILEGE",
		"SECURITY_PROOF_REQUIRED",
		"SECURITY_PROOF_EXPIRED",
		"SECURITY_PROOF_SCOPE_MISMATCH",
		"SECURITY_PROOF_TARGET_MISMATCH",
		"SECURITY_PROOF_METHOD_MISMATCH",
		"SECURITY_PROOF_CONSUMED",
		"SECURITY_PROOF_INVALID",
	}, channelKeyForbiddenCode["enum"])

	channelKeyRateLimited, ok := channelKeyResponses["429"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key must document a 429 response")
	assert.NotEmpty(t, channelKeyRateLimited["description"])
	assert.NotContains(t, channelKeyRateLimited, "content")
	channelKeyRateLimitHeaders, ok := channelKeyRateLimited["headers"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 429 response must define headers")
	channelKeyRetryAfter, ok := channelKeyRateLimitHeaders["Retry-After"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key 429 response must document Retry-After")
	channelKeyRetryAfterSchema, ok := channelKeyRetryAfter["schema"].(map[string]any)
	require.True(t, ok, "/api/channel/{id}/key Retry-After must define a schema")
	assert.Equal(t, "integer", channelKeyRetryAfterSchema["type"])
	assert.Equal(t, float64(1), channelKeyRetryAfterSchema["minimum"])

	channelKeySecurity, ok := channelKey["security"].([]any)
	require.True(t, ok, "/api/channel/{id}/key must define security alternatives")
	require.Len(t, channelKeySecurity, 2)
	acceptedSessionAlternatives := make(map[string]struct{}, len(channelKeySecurity))
	for _, rawAlternative := range channelKeySecurity {
		alternative, ok := rawAlternative.(map[string]any)
		require.True(t, ok, "channel-key security alternative must be an object")
		require.Len(t, alternative, 2)
		proofScopes, ok := alternative["SecurityProof"].([]any)
		require.True(t, ok, "channel-key security alternative must require SecurityProof")
		assert.Empty(t, proofScopes)
		sessionScheme := ""
		for scheme, rawScopes := range alternative {
			if scheme != "SecurityProof" {
				sessionScheme = scheme
				scopes, ok := rawScopes.([]any)
				require.True(t, ok, "session security requirement must define scopes")
				assert.Empty(t, scopes)
			}
		}
		require.NotEmpty(t, sessionScheme, "channel-key security alternative must include one session scheme")
		_, duplicate := acceptedSessionAlternatives[sessionScheme]
		assert.False(t, duplicate, "channel-key session security alternatives must be unique")
		acceptedSessionAlternatives[sessionScheme] = struct{}{}
	}
	assert.Len(t, acceptedSessionAlternatives, 2)

	securityProof, ok := document.Components.SecuritySchemes["SecurityProof"]
	require.True(t, ok, "missing SecurityProof security scheme")
	assert.Equal(t, "apiKey", securityProof["type"])
	assert.Equal(t, "header", securityProof["in"])
	assert.Equal(t, "X-Security-Proof", securityProof["name"])
	securityProofDescription, ok := securityProof["description"].(string)
	require.True(t, ok, "SecurityProof must define a description")
	assert.Contains(t, securityProofDescription, "exact-target")
	assert.Contains(t, securityProofDescription, "database-backed")
	assert.Contains(t, securityProofDescription, "one-time")
}

func TestVerificationRoutesUsePostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	assert.Contains(t, routes, "POST /api/verification")
	assert.NotContains(t, routes, "GET /api/verification")
	assert.Contains(t, routes, "POST /api/reset_password")
	assert.NotContains(t, routes, "GET /api/reset_password")
	assert.Contains(t, routes, "POST /api/user/reset")
}

func TestVerificationRoutesRejectOversizedBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousLimit := constant.AnonymousRequestBodyLimitKB
	previousTurnstile := common.TurnstileCheckEnabled
	constant.AnonymousRequestBodyLimitKB = 1
	common.TurnstileCheckEnabled = false
	t.Cleanup(func() {
		constant.AnonymousRequestBodyLimitKB = previousLimit
		common.TurnstileCheckEnabled = previousTurnstile
	})
	engine := gin.New()
	SetApiRouter(engine)
	body := strings.Repeat("x", 2048)

	for _, path := range []string{
		"/api/verification",
		"/api/reset_password",
		"/api/user/reset",
	} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.RemoteAddr = fmt.Sprintf(
				"192.0.2.%d:1234",
				verificationRouteClientSequence.Add(1),
			)

			engine.ServeHTTP(recorder, request)

			assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
		})
	}
}
