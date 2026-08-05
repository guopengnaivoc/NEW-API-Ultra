package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useCodexChannelModelsTestDB(t *testing.T) {
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

func TestFetchCodexChannelModelsMalformedAuthoritativeSettingFailsClosedWithoutStaleWrite(
	t *testing.T,
) {
	useCodexChannelModelsTestDB(t)

	staleRequests := make(chan struct{}, 1)
	staleEndpoint := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		staleRequests <- struct{}{}
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer staleEndpoint.Close()

	retryRequests := make(chan struct{}, 1)
	retryEndpoint := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		retryRequests <- struct{}{}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"models":[{"slug":"must-not-run"}]}`))
	}))
	defer retryEndpoint.Close()

	encodeKey := func(key *CodexOAuthKey) string {
		encoded, err := common.Marshal(key)
		require.NoError(t, err)
		return string(encoded)
	}
	oldKey := encodeKey(&CodexOAuthKey{
		AccessToken:  "access-token-old-malformed-models",
		RefreshToken: "refresh-token-observed-malformed-models",
		AccountID:    "account-old-malformed-models",
		Type:         "codex",
	})
	loserKey := encodeKey(&CodexOAuthKey{
		AccessToken:  "access-token-loser-malformed-models",
		RefreshToken: "refresh-token-loser-malformed-models",
		AccountID:    "account-loser-malformed-models",
		Type:         "codex",
	})
	winnerKey := encodeKey(&CodexOAuthKey{
		AccessToken:  "access-token-winner-malformed-models",
		RefreshToken: "refresh-token-winner-malformed-models",
		AccountID:    "account-winner-malformed-models",
		Type:         "codex",
	})

	channel := &model.Channel{
		Id:      63632,
		Type:    constant.ChannelTypeCodex,
		Key:     oldKey,
		Name:    "codex-models-malformed-setting-test",
		Models:  "gpt-test",
		Group:   "default",
		Status:  common.ChannelStatusEnabled,
		BaseURL: common.GetPointer(retryEndpoint.URL),
		Setting: common.GetPointer(`{"proxy":""}`),
	}
	require.NoError(t, model.DB.Create(channel).Error)

	refreshCalls := 0
	refresh := func(
		_ context.Context,
		channelID int,
		opts CodexCredentialRefreshOptions,
	) (*CodexCredentialRefreshResult, error) {
		refreshCalls++
		assert.Equal(t, channel.Id, channelID)
		assert.Equal(
			t,
			"refresh-token-observed-malformed-models",
			opts.ExpectedRefreshToken,
		)

		require.NoError(t, model.UpdateChannelKey(channelID, loserKey))
		loserSnapshot, err := model.GetChannelById(channelID, true)
		require.NoError(t, err)
		loserSnapshot.Setting = common.GetPointer(`{"proxy":`)
		require.NoError(t, model.UpdateChannelKey(channelID, winnerKey))

		return &CodexCredentialRefreshResult{
			OAuthKey: &CodexOAuthKey{
				AccessToken:  "access-token-loser-malformed-models",
				RefreshToken: "refresh-token-loser-malformed-models",
				AccountID:    "account-loser-malformed-models",
				Type:         "codex",
			},
			Channel:   loserSnapshot,
			Refreshed: false,
		}, nil
	}

	models, err := fetchCodexChannelModelsWithCredentialRefresh(
		context.Background(),
		channel,
		staleEndpoint.URL,
		staleEndpoint.Client(),
		"1.2.3",
		refresh,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid authoritative channel setting")
	assert.Empty(t, models)
	assert.Equal(t, 1, refreshCalls)
	require.Len(t, staleRequests, 1)
	assert.Empty(t, retryRequests)

	durable, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, winnerKey, durable.Key)
}

func TestFetchCodexChannelModelsRejectsEmptyObservedRefreshTokenBeforeAutomaticRefresh(
	t *testing.T,
) {
	var requestCalls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		if requestCalls.Add(1) == 1 {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"models":[{"slug":"must-not-retry"}]}`))
	}))
	defer endpoint.Close()

	encoded, err := common.Marshal(&CodexOAuthKey{
		AccessToken:  "access-token-no-observed-refresh",
		RefreshToken: " \t ",
		AccountID:    "account-no-observed-refresh",
		Type:         "codex",
	})
	require.NoError(t, err)
	channel := &model.Channel{
		Id:      63633,
		Type:    constant.ChannelTypeCodex,
		Key:     string(encoded),
		BaseURL: common.GetPointer(endpoint.URL),
		Setting: common.GetPointer(`{"proxy":""}`),
	}

	var refreshCalls atomic.Int32
	refresh := func(
		_ context.Context,
		_ int,
		_ CodexCredentialRefreshOptions,
	) (*CodexCredentialRefreshResult, error) {
		refreshCalls.Add(1)
		return &CodexCredentialRefreshResult{
			OAuthKey: &CodexOAuthKey{
				AccessToken:  "access-token-unobserved-winner",
				RefreshToken: "refresh-token-unobserved-winner",
				AccountID:    "account-unobserved-winner",
				Type:         "codex",
			},
			Channel: channel,
		}, nil
	}

	models, err := fetchCodexChannelModelsWithCredentialRefresh(
		context.Background(),
		channel,
		endpoint.URL,
		endpoint.Client(),
		"1.2.3",
		refresh,
	)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "refresh_token is required")
	}
	assert.Empty(t, models)
	assert.Zero(t, refreshCalls.Load())
	assert.EqualValues(t, 1, requestCalls.Load())
}

func TestFetchCodexChannelModelsRetryUsesAuthoritativeEndpointAndProxy(t *testing.T) {
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
		_, _ = writer.Write([]byte(`{"models":[{"slug":"gpt-winner"}]}`))
	}))
	defer authoritativeProxy.Close()
	t.Cleanup(func() {
		InvalidateProxyClient(authoritativeProxy.URL)
	})

	encoded, err := common.Marshal(&CodexOAuthKey{
		AccessToken:  "access-token-old-models",
		RefreshToken: "refresh-token-observed-models",
		AccountID:    "account-old-models",
		Type:         "codex",
	})
	require.NoError(t, err)
	channel := &model.Channel{
		Id:      63631,
		Type:    constant.ChannelTypeCodex,
		Key:     string(encoded),
		BaseURL: common.GetPointer(staleEndpoint.URL),
		Setting: common.GetPointer(`{"proxy":""}`),
	}
	authoritativeSetting, err := common.Marshal(map[string]string{
		"proxy": authoritativeProxy.URL,
	})
	require.NoError(t, err)
	authoritativeChannel := &model.Channel{
		Id:      channel.Id,
		Type:    constant.ChannelTypeCodex,
		BaseURL: common.GetPointer("http://authoritative.invalid"),
		Setting: common.GetPointer(string(authoritativeSetting)),
	}

	refreshCalls := 0
	refresh := func(
		ctx context.Context,
		channelID int,
		opts CodexCredentialRefreshOptions,
	) (*CodexCredentialRefreshResult, error) {
		refreshCalls++
		assert.Equal(t, channel.Id, channelID)
		assert.True(t, opts.ResetCaches)
		assert.Equal(
			t,
			"refresh-token-observed-models",
			opts.ExpectedRefreshToken,
		)
		return &CodexCredentialRefreshResult{
			OAuthKey: &CodexOAuthKey{
				AccessToken:  "access-token-winner-models",
				RefreshToken: "refresh-token-winner-models",
				AccountID:    "account-winner-models",
			},
			Channel:   authoritativeChannel,
			Refreshed: false,
		}, nil
	}

	models, err := fetchCodexChannelModelsWithCredentialRefresh(
		context.Background(),
		channel,
		staleEndpoint.URL,
		staleEndpoint.Client(),
		"1.2.3",
		refresh,
	)
	require.NoError(t, err)
	assert.Contains(t, models, "gpt-winner")
	assert.Equal(t, 1, refreshCalls)
	require.Len(t, staleRequests, 1)
	require.Len(t, authoritativeRequests, 1)

	staleRequest := <-staleRequests
	assert.Equal(t, "/backend-api/codex/models", staleRequest.path)
	assert.Equal(
		t,
		"Bearer access-token-old-models",
		staleRequest.authorization,
	)
	assert.Equal(t, "account-old-models", staleRequest.accountID)

	authoritativeRequest := <-authoritativeRequests
	assert.Equal(t, "authoritative.invalid", authoritativeRequest.host)
	assert.Equal(t, "/backend-api/codex/models", authoritativeRequest.path)
	assert.Equal(
		t,
		"Bearer access-token-winner-models",
		authoritativeRequest.authorization,
	)
	assert.Equal(t, "account-winner-models", authoritativeRequest.accountID)
}
