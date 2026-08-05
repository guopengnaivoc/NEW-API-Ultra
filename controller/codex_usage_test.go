package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useCodexUsageTestDB(t *testing.T) {
	t.Helper()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db
	common.SetDatabaseTypes(
		common.DatabaseTypeSQLite,
		common.DatabaseTypeSQLite,
	)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))

	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(
			previousMainDatabaseType,
			previousLogDatabaseType,
		)
	})
}

func TestFetchCodexChannelWhamDataRetriesWithAuthoritativeRefreshWinner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useCodexUsageTestDB(t)
	const channelID = 63621

	encoded, err := common.Marshal(map[string]any{
		"access_token":  "access-token-old",
		"refresh_token": "refresh-token-observed",
		"account_id":    "account-old",
		"type":          "codex",
	})
	require.NoError(t, err)
	channel := &model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeCodex,
		Key:     string(encoded),
		Name:    "codex-usage-test",
		Models:  "gpt-test",
		Group:   "default",
		Status:  common.ChannelStatusEnabled,
		Setting: common.GetPointer(`{"proxy":""}`),
	}
	require.NoError(t, model.DB.Create(channel).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/63621/usage", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(channelID)}}

	fetchCalls := 0
	fetch := func(
		ctx context.Context,
		client *http.Client,
		baseURL string,
		accessToken string,
		accountID string,
	) (int, []byte, error) {
		fetchCalls++
		if fetchCalls == 1 {
			assert.Equal(t, "access-token-old", accessToken)
			assert.Equal(t, "account-old", accountID)
			return http.StatusUnauthorized, []byte(`{"error":"expired"}`), nil
		}
		assert.Equal(t, "access-token-winner", accessToken)
		assert.Equal(t, "account-winner", accountID)
		return http.StatusOK, []byte(`{"remaining":42}`), nil
	}

	refreshCalls := 0
	refresh := func(
		ctx context.Context,
		refreshedChannelID int,
		opts service.CodexCredentialRefreshOptions,
	) (*service.CodexCredentialRefreshResult, error) {
		refreshCalls++
		assert.Equal(t, channelID, refreshedChannelID)
		assert.True(t, opts.ResetCaches)
		assert.Equal(t, "refresh-token-observed", opts.ExpectedRefreshToken)
		return &service.CodexCredentialRefreshResult{
			OAuthKey: &service.CodexOAuthKey{
				AccessToken:  "access-token-winner",
				RefreshToken: "refresh-token-winner",
				AccountID:    "account-winner",
			},
			Channel:   channel,
			Refreshed: false,
		}, nil
	}

	fetchCodexChannelWhamData(
		c,
		fetch,
		refresh,
		"codex usage test",
		"usage failed",
	)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, 2, fetchCalls)
	assert.Equal(t, 1, refreshCalls)

	var response struct {
		Success        bool           `json:"success"`
		UpstreamStatus int            `json:"upstream_status"`
		Data           map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Equal(t, http.StatusOK, response.UpstreamStatus)
	assert.EqualValues(t, 42, response.Data["remaining"])
}

func TestFetchCodexChannelWhamDataMalformedAuthoritativeSettingFailsClosedWithoutStaleWrite(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	useCodexUsageTestDB(t)
	const channelID = 63623

	encodeKey := func(key *service.CodexOAuthKey) string {
		encoded, err := common.Marshal(key)
		require.NoError(t, err)
		return string(encoded)
	}
	oldKey := encodeKey(&service.CodexOAuthKey{
		AccessToken:  "access-token-old-malformed-usage",
		RefreshToken: "refresh-token-observed-malformed-usage",
		AccountID:    "account-old-malformed-usage",
		Type:         "codex",
	})
	loserKey := encodeKey(&service.CodexOAuthKey{
		AccessToken:  "access-token-loser-malformed-usage",
		RefreshToken: "refresh-token-loser-malformed-usage",
		AccountID:    "account-loser-malformed-usage",
		Type:         "codex",
	})
	winnerKey := encodeKey(&service.CodexOAuthKey{
		AccessToken:  "access-token-winner-malformed-usage",
		RefreshToken: "refresh-token-winner-malformed-usage",
		AccountID:    "account-winner-malformed-usage",
		Type:         "codex",
	})

	channel := &model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeCodex,
		Key:     oldKey,
		Name:    "codex-usage-malformed-setting-test",
		Models:  "gpt-test",
		Group:   "default",
		Status:  common.ChannelStatusEnabled,
		Setting: common.GetPointer(`{"proxy":""}`),
	}
	require.NoError(t, model.DB.Create(channel).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/63623/usage", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(channelID)}}

	fetchCalls := 0
	fetch := func(
		_ context.Context,
		_ *http.Client,
		_ string,
		_ string,
		_ string,
	) (int, []byte, error) {
		fetchCalls++
		if fetchCalls == 1 {
			return http.StatusUnauthorized, []byte(`{"error":"expired"}`), nil
		}
		return http.StatusOK, []byte(`{"remaining":42}`), nil
	}

	refreshCalls := 0
	refresh := func(
		_ context.Context,
		refreshedChannelID int,
		opts service.CodexCredentialRefreshOptions,
	) (*service.CodexCredentialRefreshResult, error) {
		refreshCalls++
		assert.Equal(t, channelID, refreshedChannelID)
		assert.Equal(
			t,
			"refresh-token-observed-malformed-usage",
			opts.ExpectedRefreshToken,
		)

		require.NoError(t, model.UpdateChannelKey(channelID, loserKey))
		loserSnapshot, err := model.GetChannelById(channelID, true)
		require.NoError(t, err)
		loserSnapshot.Setting = common.GetPointer(`{"proxy":`)
		require.NoError(t, model.UpdateChannelKey(channelID, winnerKey))

		return &service.CodexCredentialRefreshResult{
			OAuthKey: &service.CodexOAuthKey{
				AccessToken:  "access-token-loser-malformed-usage",
				RefreshToken: "refresh-token-loser-malformed-usage",
				AccountID:    "account-loser-malformed-usage",
				Type:         "codex",
			},
			Channel:   loserSnapshot,
			Refreshed: false,
		}, nil
	}

	fetchCodexChannelWhamData(
		c,
		fetch,
		refresh,
		"codex usage malformed setting test",
		"usage failed",
	)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, 1, fetchCalls)
	assert.Equal(t, 1, refreshCalls)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "usage failed", response.Message)

	durable, err := model.GetChannelById(channelID, true)
	require.NoError(t, err)
	assert.Equal(t, winnerKey, durable.Key)
}

func TestFetchCodexChannelWhamDataRetryUsesAuthoritativeEndpointAndProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useCodexUsageTestDB(t)
	const channelID = 63622

	type requestObservation struct {
		host          string
		path          string
		authorization string
		accountID     string
	}
	staleRequests := make(chan requestObservation, 2)
	staleEndpoint := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		staleRequests <- requestObservation{
			host:          request.Host,
			path:          request.URL.Path,
			authorization: request.Header.Get("Authorization"),
			accountID:     request.Header.Get("ChatGPT-Account-Id"),
		}
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":"stale endpoint"}`))
	}))
	defer staleEndpoint.Close()

	authoritativeRequests := make(chan requestObservation, 1)
	authoritativeProxy := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		authoritativeRequests <- requestObservation{
			host:          request.URL.Host,
			path:          request.URL.Path,
			authorization: request.Header.Get("Authorization"),
			accountID:     request.Header.Get("ChatGPT-Account-Id"),
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"remaining":42}`))
	}))
	defer authoritativeProxy.Close()
	t.Cleanup(func() {
		service.InvalidateProxyClient(authoritativeProxy.URL)
	})

	encoded, err := common.Marshal(map[string]any{
		"access_token":  "access-token-old-endpoint",
		"refresh_token": "refresh-token-observed-endpoint",
		"account_id":    "account-old-endpoint",
		"type":          "codex",
	})
	require.NoError(t, err)
	channel := &model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeCodex,
		Key:     string(encoded),
		Name:    "codex-usage-endpoint-test",
		Models:  "gpt-test",
		Group:   "default",
		Status:  common.ChannelStatusEnabled,
		BaseURL: common.GetPointer(staleEndpoint.URL),
		Setting: common.GetPointer(`{"proxy":""}`),
	}
	require.NoError(t, model.DB.Create(channel).Error)

	authoritativeSetting, err := common.Marshal(map[string]string{
		"proxy": authoritativeProxy.URL,
	})
	require.NoError(t, err)
	authoritativeChannel := &model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeCodex,
		BaseURL: common.GetPointer("http://authoritative.invalid"),
		Setting: common.GetPointer(string(authoritativeSetting)),
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/63622/usage", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(channelID)}}

	refreshCalls := 0
	refresh := func(
		ctx context.Context,
		refreshedChannelID int,
		opts service.CodexCredentialRefreshOptions,
	) (*service.CodexCredentialRefreshResult, error) {
		refreshCalls++
		assert.Equal(t, channelID, refreshedChannelID)
		assert.True(t, opts.ResetCaches)
		assert.Equal(
			t,
			"refresh-token-observed-endpoint",
			opts.ExpectedRefreshToken,
		)
		return &service.CodexCredentialRefreshResult{
			OAuthKey: &service.CodexOAuthKey{
				AccessToken:  "access-token-winner-endpoint",
				RefreshToken: "refresh-token-winner-endpoint",
				AccountID:    "account-winner-endpoint",
			},
			Channel:   authoritativeChannel,
			Refreshed: false,
		}, nil
	}

	fetchCodexChannelWhamData(
		c,
		service.FetchCodexWhamUsage,
		refresh,
		"codex usage endpoint test",
		"usage failed",
	)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, 1, refreshCalls)
	require.Len(t, staleRequests, 1)
	require.Len(t, authoritativeRequests, 1)

	staleRequest := <-staleRequests
	assert.Equal(t, "/backend-api/wham/usage", staleRequest.path)
	assert.Equal(
		t,
		"Bearer access-token-old-endpoint",
		staleRequest.authorization,
	)
	assert.Equal(t, "account-old-endpoint", staleRequest.accountID)

	authoritativeRequest := <-authoritativeRequests
	assert.Equal(t, "authoritative.invalid", authoritativeRequest.host)
	assert.Equal(t, "/backend-api/wham/usage", authoritativeRequest.path)
	assert.Equal(
		t,
		"Bearer access-token-winner-endpoint",
		authoritativeRequest.authorization,
	)
	assert.Equal(t, "account-winner-endpoint", authoritativeRequest.accountID)

	var response struct {
		Success        bool           `json:"success"`
		UpstreamStatus int            `json:"upstream_status"`
		Data           map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Equal(t, http.StatusOK, response.UpstreamStatus)
	assert.EqualValues(t, 42, response.Data["remaining"])
}
