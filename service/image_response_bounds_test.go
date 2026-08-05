package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeURLImageDataBoundsMalformedHeaderRead(t *testing.T) {
	originalFetchSetting, err := config.ConfigToMap(system_setting.GetFetchSetting())
	require.NoError(t, err)
	originalHTTPClient := httpClient
	originalProtectedClient := ssrfProtectedHTTPClient
	originalWorkerURL := system_setting.WorkerUrl
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("fetch_setting", originalFetchSetting)
		require.NoError(t, updateErr)
		require.True(t, updated)
		httpClient = originalHTTPClient
		ssrfProtectedHTTPClient = originalProtectedClient
		system_setting.WorkerUrl = originalWorkerURL
	})

	updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection": "false",
	})
	require.NoError(t, err)
	require.True(t, updated)
	system_setting.WorkerUrl = ""

	body := newTrackingResponseBody(strings.Repeat("x", int(remoteImageConfigMaxBytes*2)))
	httpClient = &http.Client{
		Transport: responseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header: http.Header{
					"Content-Type": []string{"image/png"},
				},
				Body:          body,
				ContentLength: remoteImageConfigMaxBytes * 2,
				Request:       req,
			}, nil
		}),
	}
	ssrfProtectedHTTPClient = newProtectedFetchHTTPClient()

	config, format, err := DecodeUrlImageData("https://image.example/malformed.png")

	require.Error(t, err)
	assert.Zero(t, config.Width)
	assert.Empty(t, format)
	assert.LessOrEqual(t, int64(body.bytesRead), remoteImageConfigMaxBytes)
	assert.True(t, body.closed)
}
