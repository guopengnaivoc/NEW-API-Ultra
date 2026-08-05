package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type receivedWebhookPayload struct {
	payload WebhookPayload
	err     error
}

func TestRenderNotifyContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		values  []interface{}
		want    string
		wantErr bool
	}{
		{
			name:    "no placeholders",
			content: "quota warning",
			want:    "quota warning",
		},
		{
			name:    "quota notification",
			content: "{{value}} has {{value}} remaining; <a href='{{value}}'>{{value}}</a>",
			values:  []interface{}{"account", "$2.00", "/wallet", "/wallet"},
			want:    "account has $2.00 remaining; <a href='/wallet'>/wallet</a>",
		},
		{
			name:    "literal percent and placeholder in value",
			content: "{{value}}: 100%; marker={{value}}",
			values:  []interface{}{"status", "{{value}}"},
			want:    "status: 100%; marker={{value}}",
		},
		{
			name:    "missing value",
			content: "{{value}} {{value}}",
			values:  []interface{}{"only one"},
			wantErr: true,
		},
		{
			name:    "extra value",
			content: "no placeholder",
			values:  []interface{}{"unexpected"},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content, err := renderNotifyContent(dto.NewNotify("", "", test.content, test.values))
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, content)
		})
	}
}

func TestSendWebhookNotifyRendersValuePlaceholders(t *testing.T) {
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

	payloads := make(chan receivedWebhookPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload WebhookPayload
		decodeErr := common.DecodeJson(r.Body, &payload)
		payloads <- receivedWebhookPayload{payload: payload, err: decodeErr}
		if decodeErr != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	httpClient = server.Client()
	ssrfProtectedHTTPClient = server.Client()

	notification := dto.NewNotify(
		dto.NotifyTypeQuotaExceed,
		"quota warning",
		"{{value}} has {{value}} remaining; top up at <a href='{{value}}'>{{value}}</a>",
		[]interface{}{"account", "$2.00", "https://example.com/wallet", "https://example.com/wallet"},
	)

	require.NoError(t, SendWebhookNotify(server.URL, "", notification))
	received := <-payloads
	require.NoError(t, received.err)
	payload := received.payload
	require.Equal(
		t,
		"account has $2.00 remaining; top up at <a href='https://example.com/wallet'>https://example.com/wallet</a>",
		payload.Content,
	)
}
