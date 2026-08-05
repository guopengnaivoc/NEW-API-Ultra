package gemini

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiTaskAdaptorDoResponseReturnsPublicProjection(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		publicTaskID  = "task_public_submit"
		operationName = "models/veo-3.0-generate-001/operations/operation-name-sentinel"
		rawSentinel   = "raw-submit-metadata-sentinel"
	)
	rawBody := fmt.Sprintf(
		`{"name":%q,"done":false,"metadata":%q}`,
		operationName,
		rawSentinel,
	)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		OriginModelName: "veo-3.0-generate-001",
		ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "submit-credential-sentinel"},
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: publicTaskID},
	}
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(rawBody)),
	}

	taskID, taskData, taskErr := (&TaskAdaptor{}).DoResponse(
		context,
		response,
		info,
	)

	require.Nil(t, taskErr)
	assert.Equal(t, taskcommon.EncodeLocalTaskID(operationName), taskID)
	assert.Equal(
		t,
		string(geminitaskresult.EmptyPublicProjection(false)),
		string(taskData),
	)

	assert.Empty(t, info.ProviderResultURI)

	assert.False(t, context.Writer.Written())
	require.NotNil(t, info.TaskRelayInfo.SuccessResponse)
	transientInfoJSON, err := common.Marshal(info.TaskRelayInfo)
	require.NoError(t, err)
	assert.NotContains(
		t,
		string(transientInfoJSON),
		"veo-3.0-generate-001",
	)
	deferredResponseJSON, err := common.Marshal(
		info.TaskRelayInfo.SuccessResponse,
	)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(deferredResponseJSON))

	info.TaskRelayInfo.SuccessResponse.Write(context)
	publicBody := recorder.Body.String()
	assert.Contains(t, publicBody, publicTaskID)
	assert.NotContains(t, publicBody, operationName)
	assert.NotContains(t, publicBody, rawSentinel)
	assert.NotContains(t, string(taskData), operationName)
	assert.NotContains(t, string(taskData), rawSentinel)
}

func TestGeminiTaskAdaptorDoResponseSupportsImmediateGeneratedSamples(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)

	const (
		publicTaskID   = "task_public_immediate"
		operationName  = "models/veo-3.0-generate-001/operations/immediate-operation-sentinel"
		credential     = "immediate-credential-sentinel"
		providerPath   = "provider-path-sentinel"
		signedSentinel = "signed-query-sentinel"
	)
	rawProviderURI := "https://video.example.test/" + providerPath +
		"?key=" + credential + "&sig=" + signedSentinel
	filteredProviderURI := "https://video.example.test/" + providerPath +
		"?sig=" + signedSentinel
	rawBody := fmt.Sprintf(
		`{"name":%q,"done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":%q,"mimeType":"video/mp4"}}]}}}`,
		operationName,
		rawProviderURI,
	)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		OriginModelName: "veo-3.0-generate-001",
		ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: credential},
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: publicTaskID},
	}
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(rawBody)),
	}

	taskID, taskData, taskErr := (&TaskAdaptor{}).DoResponse(
		context,
		response,
		info,
	)

	require.Nil(t, taskErr)
	assert.Equal(t, taskcommon.EncodeLocalTaskID(operationName), taskID)
	assert.Equal(
		t,
		`{"done":true,"video":{"url":"/v1/videos/task_public_immediate/content","mime_type":"video/mp4"}}`,
		string(taskData),
	)

	assert.Equal(t, filteredProviderURI, info.ProviderResultURI)
	transientInfoJSON, err := common.Marshal(info.TaskRelayInfo)
	require.NoError(t, err)
	assert.NotContains(t, string(transientInfoJSON), providerPath)
	assert.NotContains(t, string(transientInfoJSON), signedSentinel)
	assert.NotContains(t, string(transientInfoJSON), credential)

	assert.False(t, context.Writer.Written())
	require.NotNil(t, info.TaskRelayInfo.SuccessResponse)
	info.TaskRelayInfo.SuccessResponse.Write(context)
	publicSinks := recorder.Body.String() + string(taskData)
	assert.Contains(t, recorder.Body.String(), publicTaskID)
	assert.NotContains(t, publicSinks, operationName)
	assert.NotContains(t, publicSinks, providerPath)
	assert.NotContains(t, publicSinks, signedSentinel)
	assert.NotContains(t, publicSinks, credential)
}

func TestGeminiTaskAdaptorDoResponseRejectsMalformedBodyWithoutLeak(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)

	const rawSentinel = "malformed-response-sentinel"
	rawBody := `{"name":"operation-name-sentinel","broken":"` + rawSentinel
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		OriginModelName: "veo-3.0-generate-001",
		ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "malformed-credential-sentinel"},
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public_malformed"},
	}
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(rawBody)),
	}

	taskID, taskData, taskErr := (&TaskAdaptor{}).DoResponse(
		context,
		response,
		info,
	)

	assert.Empty(t, taskID)
	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_response", taskErr.Code)
	assert.Equal(
		t,
		string(geminitaskresult.EmptyPublicProjection(false)),
		string(taskData),
	)
	assert.NotContains(t, taskErr.Message, rawSentinel)
	assert.NotContains(t, fmt.Sprint(taskErr.Error), rawSentinel)
	assert.Nil(t, info.TaskRelayInfo.SuccessResponse)
	assert.NotContains(t, recorder.Body.String(), rawSentinel)
}

func TestGeminiTaskAdaptorDoResponseRejectsMissingOperationName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		OriginModelName: "veo-3.0-generate-001",
		ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "missing-name-credential"},
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public_missing_name"},
	}
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"done":false}`)),
	}

	taskID, taskData, taskErr := (&TaskAdaptor{}).DoResponse(
		context,
		response,
		info,
	)

	assert.Empty(t, taskID)
	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_response", taskErr.Code)
	assert.Equal(
		t,
		string(geminitaskresult.EmptyPublicProjection(false)),
		string(taskData),
	)
	assert.Empty(t, info.ProviderResultURI)
	assert.Nil(t, info.TaskRelayInfo.SuccessResponse)
	assert.Empty(t, recorder.Body.String())
}

func TestGeminiTaskAdaptorParseTaskResultDoesNotReturnProviderURI(t *testing.T) {
	const (
		operationName = "models/veo-3.0-generate-001/operations/poll-operation"
		providerPath  = "parse-provider-path-sentinel"
		credential    = "parse-credential-sentinel"
	)
	rawProviderURI := "https://video.example.test/" + providerPath +
		"?key=" + credential
	rawBody := fmt.Sprintf(
		`{"name":%q,"done":true,"response":{"generateVideoResponse":{"generatedVideos":[{"video":{"uri":%q}}]}}}`,
		operationName,
		rawProviderURI,
	)

	taskInfo, err := (&TaskAdaptor{}).ParseTaskResult([]byte(rawBody))

	require.NoError(t, err)
	require.NotNil(t, taskInfo)
	assert.Equal(t, string(model.TaskStatusSuccess), taskInfo.Status)
	assert.Equal(t, "100%", taskInfo.Progress)
	assert.Equal(t, taskcommon.EncodeLocalTaskID(operationName), taskInfo.TaskID)
	assert.Empty(t, taskInfo.RemoteUrl)
	assert.Empty(t, taskInfo.Url)
	taskInfoJSON, err := common.Marshal(taskInfo)
	require.NoError(t, err)
	assert.NotContains(t, string(taskInfoJSON), providerPath)
	assert.NotContains(t, string(taskInfoJSON), credential)
}

func TestGeminiTaskAdaptorBuildRequestBodyRejectsInvalidMetadataDurationSeconds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    "veo-3.0-generate-001",
		Prompt:   "A scenic video",
		Duration: 10,
		Metadata: map[string]interface{}{
			"durationSeconds": relaycommon.MaxTaskDurationSeconds + 1,
		},
	})
	info := &relaycommon.RelayInfo{OriginModelName: "veo-3.0-generate-001"}

	_, err := (&TaskAdaptor{}).BuildRequestBody(context, info)

	require.EqualError(t, err, "seconds must be between 1 and 3600")
}

func TestGeminiTaskAdaptorUsesSameDurationForRequestAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Model:   "veo-3.0-generate-001",
		Prompt:  "A scenic video",
		Seconds: "6",
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "veo-3.0-generate-001"},
	}
	adaptor := &TaskAdaptor{}

	requestBody, err := adaptor.BuildRequestBody(context, info)
	require.NoError(t, err)
	body, err := io.ReadAll(requestBody)
	require.NoError(t, err)
	var payload VeoRequestPayload
	require.NoError(t, common.Unmarshal(body, &payload))
	require.NotNil(t, payload.Parameters)

	ratios := adaptor.EstimateBilling(context, info)
	require.Equal(t, 6, payload.Parameters.DurationSeconds)
	require.Equal(t, float64(payload.Parameters.DurationSeconds), ratios["seconds"])
}
