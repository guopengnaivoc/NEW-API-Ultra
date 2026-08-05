package vertex

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel/task/gemini"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestVertexTaskAdaptorBuildRequestBodyRejectsInvalidMetadataDurationSeconds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    "veo-3.0-generate-001",
		Prompt:   "A cinematic video",
		Duration: 10,
		Metadata: map[string]interface{}{
			"durationSeconds": relaycommon.MaxTaskDurationSeconds + 1,
		},
	})

	_, err := (&TaskAdaptor{}).BuildRequestBody(context, &relaycommon.RelayInfo{})
	require.EqualError(t, err, "seconds must be between 1 and 3600")
}

func TestVertexTaskAdaptorUsesSameDurationForRequestAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Model:   "veo-3.0-generate-001",
		Prompt:  "A cinematic video",
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
	var payload gemini.VeoRequestPayload
	require.NoError(t, common.Unmarshal(body, &payload))
	require.NotNil(t, payload.Parameters)

	ratios := adaptor.EstimateBilling(context, info)
	require.Equal(t, 6, payload.Parameters.DurationSeconds)
	require.Equal(t, float64(payload.Parameters.DurationSeconds), ratios["seconds"])
}
