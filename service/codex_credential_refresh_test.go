package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedCodexCredentialRefreshTestChannel(
	t *testing.T,
	channelID int,
	refreshToken string,
	setting string,
) {
	t.Helper()

	require.NoError(t, model.DB.Where("id = ?", channelID).Delete(&model.Channel{}).Error)
	t.Cleanup(func() {
		model.DB.Where("id = ?", channelID).Delete(&model.Channel{})
	})

	encoded, err := common.Marshal(&CodexOAuthKey{
		AccessToken:  "access-token-old",
		RefreshToken: refreshToken,
		AccountID:    "account-old",
		Email:        "owner@example.com",
		Type:         "codex",
		Expired:      "2026-07-29T00:00:00Z",
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeCodex,
		Key:     string(encoded),
		Name:    "codex-refresh-test",
		Models:  "gpt-test",
		Group:   "default",
		Status:  common.ChannelStatusEnabled,
		Setting: common.GetPointer(setting),
	}).Error)
}

func rawCodexCredentialRefreshTestKey(t *testing.T, channelID int) string {
	t.Helper()
	var stored string
	require.NoError(t, model.DB.Table("channels").
		Select("key").
		Where("id = ?", channelID).
		Scan(&stored).Error)
	return stored
}

func TestRefreshCodexChannelCredentialReusesWinnerForStaleExpectedToken(t *testing.T) {
	const (
		channelID = 63611
		oldToken  = "refresh-token-old"
		newToken  = "refresh-token-new"
	)
	seedCodexCredentialRefreshTestChannel(
		t,
		channelID,
		oldToken,
		`{"proxy":"http://proxy.example:8080"}`,
	)

	var providerCalls atomic.Int32
	first, err := refreshCodexChannelCredential(
		context.Background(),
		channelID,
		CodexCredentialRefreshOptions{
			ExpectedRefreshToken: oldToken,
		},
		func(
			ctx context.Context,
			refreshToken string,
			proxyURL string,
		) (*CodexOAuthTokenResult, error) {
			providerCalls.Add(1)
			assert.Equal(t, oldToken, refreshToken)
			assert.Equal(t, "http://proxy.example:8080", proxyURL)
			return &CodexOAuthTokenResult{
				AccessToken:  "access-token-new",
				RefreshToken: newToken,
				ExpiresAt:    time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
			}, nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.True(t, first.Refreshed)
	require.NotNil(t, first.OAuthKey)
	assert.Equal(t, newToken, first.OAuthKey.RefreshToken)
	assert.EqualValues(t, 1, providerCalls.Load())

	second, err := refreshCodexChannelCredential(
		context.Background(),
		channelID,
		CodexCredentialRefreshOptions{
			ExpectedRefreshToken: oldToken,
		},
		func(
			ctx context.Context,
			refreshToken string,
			proxyURL string,
		) (*CodexOAuthTokenResult, error) {
			providerCalls.Add(1)
			return nil, errors.New("stale caller must not invoke provider")
		},
	)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.False(t, second.Refreshed)
	require.NotNil(t, second.OAuthKey)
	assert.Equal(t, newToken, second.OAuthKey.RefreshToken)
	assert.Equal(t, "access-token-new", second.OAuthKey.AccessToken)
	assert.EqualValues(t, 1, providerCalls.Load())
}

func TestRefreshCodexChannelCredentialEmptyExpectedTokenForcesCurrentRotation(t *testing.T) {
	const channelID = 63612
	seedCodexCredentialRefreshTestChannel(
		t,
		channelID,
		"refresh-token-current",
		`{"proxy":""}`,
	)

	result, err := refreshCodexChannelCredential(
		context.Background(),
		channelID,
		CodexCredentialRefreshOptions{},
		func(
			ctx context.Context,
			refreshToken string,
			proxyURL string,
		) (*CodexOAuthTokenResult, error) {
			assert.Equal(t, "refresh-token-current", refreshToken)
			return &CodexOAuthTokenResult{
				AccessToken:  "access-token-forced",
				RefreshToken: "refresh-token-forced",
				ExpiresAt:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
			}, nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Refreshed)
	require.NotNil(t, result.OAuthKey)
	assert.Equal(t, "refresh-token-forced", result.OAuthKey.RefreshToken)
}

func TestRefreshCodexChannelCredentialRejectsMalformedAuthoritativeSetting(t *testing.T) {
	const channelID = 63613
	seedCodexCredentialRefreshTestChannel(
		t,
		channelID,
		"refresh-token-setting",
		`{"proxy":`,
	)
	before := rawCodexCredentialRefreshTestKey(t, channelID)

	var providerCalls atomic.Int32
	_, err := refreshCodexChannelCredential(
		context.Background(),
		channelID,
		CodexCredentialRefreshOptions{},
		func(
			ctx context.Context,
			refreshToken string,
			proxyURL string,
		) (*CodexOAuthTokenResult, error) {
			providerCalls.Add(1)
			return nil, errors.New("provider must not be called")
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid channel setting")
	assert.Zero(t, providerCalls.Load())
	assert.Equal(t, before, rawCodexCredentialRefreshTestKey(t, channelID))

	var storedSetting string
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Select("setting").
		Where("id = ?", channelID).
		Scan(&storedSetting).Error)
	assert.Equal(t, `{"proxy":`, storedSetting)
}

func TestRefreshCodexChannelCredentialProviderFailurePreservesCredential(t *testing.T) {
	const channelID = 63614
	seedCodexCredentialRefreshTestChannel(
		t,
		channelID,
		"refresh-token-provider-error",
		`{"proxy":""}`,
	)
	before := rawCodexCredentialRefreshTestKey(t, channelID)
	providerErr := errors.New("provider rejected rotating token")

	_, err := refreshCodexChannelCredential(
		context.Background(),
		channelID,
		CodexCredentialRefreshOptions{
			ExpectedRefreshToken: "refresh-token-provider-error",
		},
		func(
			ctx context.Context,
			refreshToken string,
			proxyURL string,
		) (*CodexOAuthTokenResult, error) {
			return nil, providerErr
		},
	)
	require.ErrorIs(t, err, providerErr)
	assert.Equal(t, before, rawCodexCredentialRefreshTestKey(t, channelID))
}
