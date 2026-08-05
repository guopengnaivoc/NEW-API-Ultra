package replicate

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const replicateSecretImageURL = "https://storage.example.com/private/%zz?X-Amz-Signature=TOPSECRETSIGNATURE"

func assertReplicateFileErrorSafe(t *testing.T, err error) *types.FileSourceError {
	t.Helper()

	var fileSourceError *types.FileSourceError
	require.ErrorAs(t, err, &fileSourceError)

	newAPIError := types.NewError(err, types.ErrorCodeBadResponse)
	for representation, message := range map[string]string{
		"generic": newAPIError.Error(),
		"masked":  newAPIError.MaskSensitiveErrorWithStatusCode(),
		"OpenAI":  newAPIError.ToOpenAIError().Message,
		"Claude":  newAPIError.ToClaudeError().Message,
	} {
		t.Run(representation, func(t *testing.T) {
			lowerMessage := strings.ToLower(message)
			assert.Contains(t, lowerMessage, "file source")
			assert.NotContains(t, lowerMessage, "storage.example.com")
			assert.NotContains(t, lowerMessage, "private")
			assert.NotContains(t, lowerMessage, "topsecretsignature")
			assert.NotContains(t, lowerMessage, "%zz")
		})
	}
	return fileSourceError
}

func TestDownloadImagesToBase64DoesNotRewrapRawOutputURL(t *testing.T) {
	_, err := downloadImagesToBase64([]string{replicateSecretImageURL})
	require.Error(t, err)

	fileSourceError := assertReplicateFileErrorSafe(t, err)
	assert.Same(t, fileSourceError, err)
}

func TestDoResponseBase64DownloadFailureDoesNotExposeRawOutputURL(t *testing.T) {
	responseBody, err := common.Marshal(PredictionResponse{
		Status: "succeeded",
		Output: replicateSecretImageURL,
	})
	require.NoError(t, err)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
		Header:     make(http.Header),
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		Request: &dto.ImageRequest{ResponseFormat: "b64_json"},
	}

	usage, newAPIError := (&Adaptor{}).DoResponse(context, response, info)

	require.Nil(t, usage)
	require.NotNil(t, newAPIError)
	fileSourceError := assertReplicateFileErrorSafe(t, newAPIError)
	assert.Same(t, fileSourceError, newAPIError.Err)
	assert.Empty(t, recorder.Body.String())
}

func TestDoResponseURLFormatPreservesUpstreamOutputURL(t *testing.T) {
	responseBody, err := common.Marshal(PredictionResponse{
		Status: "succeeded",
		Output: replicateSecretImageURL,
	})
	require.NoError(t, err)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
		Header:     make(http.Header),
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		Request: &dto.ImageRequest{ResponseFormat: "url"},
	}

	usage, newAPIError := (&Adaptor{}).DoResponse(context, response, info)

	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	var imageResponse dto.ImageResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &imageResponse))
	require.Len(t, imageResponse.Data, 1)
	assert.Equal(t, replicateSecretImageURL, imageResponse.Data[0].Url)
	assert.Empty(t, imageResponse.Data[0].B64Json)
}
