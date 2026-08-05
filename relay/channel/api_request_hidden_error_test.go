package channel

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	rootcommon "github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoRequestDoesNotLogHiddenOriginError(t *testing.T) {
	for _, debugEnabled := range []string{"false", "true"} {
		t.Run("debug="+debugEnabled, func(t *testing.T) {
			cmd := exec.Command(
				os.Args[0],
				"-test.run=^TestDoRequestHiddenOriginErrorHelper$",
			)
			cmd.Env = append(
				os.Environ(),
				"NEWAPI_TEST_DO_REQUEST_HIDE_ERROR_HELPER=1",
				"NEWAPI_TEST_DO_REQUEST_DEBUG="+debugEnabled,
			)

			output, err := cmd.CombinedOutput()

			require.NoError(t, err, string(output))
			assert.NotContains(t, string(output), "TOPSECRETSIGNATURE")
			assert.NotContains(t, string(output), "private/customer-123")
		})
	}
}

func TestDoRequestHiddenOriginErrorHelper(t *testing.T) {
	if os.Getenv("NEWAPI_TEST_DO_REQUEST_HIDE_ERROR_HELPER") != "1" {
		return
	}
	rootcommon.DebugEnabled = os.Getenv("NEWAPI_TEST_DO_REQUEST_DEBUG") == "true"
	service.InitHttpClient()

	var logOutput bytes.Buffer
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logOutput
	t.Cleanup(func() {
		gin.DefaultErrorWriter = previousErrorWriter
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstreamURL := upstream.URL
	upstream.Close()

	secretURL := upstreamURL +
		"/private/customer-123?X-Amz-Signature=TOPSECRETSIGNATURE"
	upstreamRequest, err := http.NewRequest(http.MethodGet, secretURL, nil)
	require.NoError(t, err)
	inboundRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginContext.Request = inboundRequest

	response, err := DoRequest(ginContext, upstreamRequest, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
	})

	require.Nil(t, response)
	require.Error(t, err)
	assert.Equal(t, "upstream error: do request failed", err.Error())
	assert.NotContains(t, logOutput.String(), "TOPSECRETSIGNATURE")
	assert.NotContains(t, logOutput.String(), "private/customer-123")

	fmt.Print(logOutput.String())
}
