package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBuildAlphaSearchRequestBodyPreservesUnknownFields(t *testing.T) {
	raw := []byte(`{
		"id":"req_1",
		"model":"gpt-5.1",
		"input":[{"role":"user","content":"hi"}],
		"commands":{"search_query":[{"q":"weather","recency":1}]},
		"settings":{"locale":"en"},
		"future_field":{"nested":true}
	}`)

	out, err := buildAlphaSearchRequestBody(raw, "gpt-5.1", "gpt-5.1-mapped")
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, common.Unmarshal(out, &body))
	assert.Equal(t, "gpt-5.1-mapped", body["model"])
	assert.Equal(t, "req_1", body["id"])
	require.Contains(t, body, "commands")
	require.Contains(t, body, "settings")
	require.Contains(t, body, "future_field")
	require.Contains(t, body, "input")

	commands, ok := body["commands"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, commands, "search_query")

	future, ok := body["future_field"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, future["nested"])
}

func TestBuildAlphaSearchRequestBodyNoMappingKeepsRawBytes(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.1","commands":{"search_query":[{"q":"x"}]},"future_field":1}`)
	out, err := buildAlphaSearchRequestBody(raw, "gpt-5.1", "gpt-5.1")
	require.NoError(t, err)
	assert.Equal(t, raw, out)
}

func TestAlphaSearchHelperFiltersUpstreamResponseHeaders(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousLogConsume := common.LogConsumeEnabled

	common.LogConsumeEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Log{}))

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.LogConsumeEnabled = previousLogConsume
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	tests := []struct {
		name             string
		connection       string
		wantContentType  string
		wantRequestID    string
		wantUpstreamPath string
		wantUpstreamBody []byte
	}{
		{
			name:             "preserves safe relay metadata",
			wantContentType:  "application/json",
			wantRequestID:    "upstream-request-id",
			wantUpstreamPath: "/v1/alpha/search",
			wantUpstreamBody: []byte(`{"model":"gpt-5","input":"find cats"}`),
		},
		{
			name:             "suppresses connection nominated metadata",
			connection:       "Content-Type, Request-Id",
			wantUpstreamPath: "/v1/alpha/search",
			wantUpstreamBody: []byte(`{"model":"gpt-5","input":"find cats"}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var receivedPath string
			var receivedBody []byte
			readErrs := make(chan error, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedPath = r.URL.Path
				var readErr error
				receivedBody, readErr = io.ReadAll(r.Body)
				readErrs <- readErr
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Request-Id", "upstream-request-id")
				w.Header().Set("Set-Cookie", "session=upstream-secret")
				if test.connection != "" {
					w.Header().Set("Connection", test.connection)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"result":"ok"}`))
			}))
			defer upstream.Close()

			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", bytes.NewReader(test.wantUpstreamBody))
			context.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(context, constant.ContextKeyChannelType, constant.ChannelTypeNewAPI)
			common.SetContextKey(context, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(context, constant.ContextKeyChannelKey, "test-key")
			common.SetContextKey(context, constant.ContextKeyOriginalModel, "gpt-5")

			request := &dto.AlphaSearchRequest{
				Model:   "gpt-5",
				RawBody: test.wantUpstreamBody,
			}
			info := &relaycommon.RelayInfo{
				OriginModelName: "gpt-5",
				RelayMode:       relayconstant.RelayModeAlphaSearch,
				Request:         request,
			}

			relayErr := AlphaSearchHelper(context, info)
			require.Nil(t, relayErr, "%#v", relayErr)
			require.NoError(t, <-readErrs)
			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Equal(t, `{"result":"ok"}`, recorder.Body.String())
			assert.Equal(t, test.wantUpstreamPath, receivedPath)
			assert.Equal(t, test.wantUpstreamBody, receivedBody)
			assert.Equal(t, test.wantContentType, recorder.Header().Get("Content-Type"))
			assert.Equal(t, test.wantRequestID, recorder.Header().Get("Request-Id"))
			assert.Empty(t, recorder.Header().Get("Set-Cookie"))
		})
	}
}
