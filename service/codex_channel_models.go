package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

type codexChannelCredentialRefreshFunc func(
	ctx context.Context,
	channelID int,
	opts CodexCredentialRefreshOptions,
) (*CodexCredentialRefreshResult, error)

func FetchCodexChannelModels(channel *model.Channel) ([]string, error) {
	if channel == nil || channel.Type != constant.ChannelTypeCodex {
		return nil, fmt.Errorf("channel type is not Codex")
	}
	if channel.ChannelInfo.IsMultiKey {
		return nil, fmt.Errorf("codex channel does not support multi-key model discovery")
	}

	client, err := NewProxyHttpClient(channel.GetSetting().Proxy)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	clientVersion, err := GetLatestCodexClientVersion(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get Codex client version: %w", err)
	}

	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[constant.ChannelTypeCodex]
	}
	return fetchCodexChannelModels(ctx, channel, baseURL, client, clientVersion)
}

func fetchCodexChannelModels(
	ctx context.Context,
	channel *model.Channel,
	baseURL string,
	client *http.Client,
	clientVersion string,
) ([]string, error) {
	return fetchCodexChannelModelsWithCredentialRefresh(
		ctx,
		channel,
		baseURL,
		client,
		clientVersion,
		RefreshCodexChannelCredential,
	)
}

func fetchCodexChannelModelsWithCredentialRefresh(
	ctx context.Context,
	channel *model.Channel,
	baseURL string,
	client *http.Client,
	clientVersion string,
	refresh codexChannelCredentialRefreshFunc,
) ([]string, error) {
	oauthKey, err := parseCodexOAuthKey(strings.TrimSpace(channel.Key))
	if err != nil {
		return nil, err
	}

	statusCode, models, err := FetchCodexModels(ctx, client, baseURL, oauthKey, clientVersion)
	if err != nil {
		return nil, err
	}
	if statusCode == http.StatusUnauthorized {
		if channel.Id <= 0 {
			return nil, fmt.Errorf("codex channel credential expired; save the channel before retrying model fetch")
		}
		observedRefreshToken := strings.TrimSpace(oauthKey.RefreshToken)
		if observedRefreshToken == "" {
			return nil, fmt.Errorf(
				"codex channel credential expired; refresh_token is required for automatic refresh",
			)
		}
		refreshResult, refreshErr := refresh(
			ctx,
			channel.Id,
			CodexCredentialRefreshOptions{
				ResetCaches:          true,
				ExpectedRefreshToken: observedRefreshToken,
			},
		)
		if refreshErr != nil {
			return nil, fmt.Errorf("failed to refresh Codex channel credential: %w", refreshErr)
		}
		if refreshResult == nil ||
			refreshResult.OAuthKey == nil ||
			refreshResult.Channel == nil {
			return nil, fmt.Errorf("failed to refresh Codex channel credential: empty result")
		}

		refreshedKey := refreshResult.OAuthKey
		authoritativeChannel := refreshResult.Channel
		var channelSetting dto.ChannelSettings
		if authoritativeChannel.Setting != nil &&
			strings.TrimSpace(*authoritativeChannel.Setting) != "" {
			if settingErr := common.Unmarshal(
				[]byte(*authoritativeChannel.Setting),
				&channelSetting,
			); settingErr != nil {
				return nil, fmt.Errorf(
					"codex channel models: invalid authoritative channel setting: %w",
					settingErr,
				)
			}
		}
		retryClient, err := GetHttpClientWithProxy(
			channelSetting.Proxy,
		)
		if err != nil {
			return nil, err
		}
		retryBaseURL := authoritativeChannel.GetBaseURL()
		if retryBaseURL == "" {
			retryBaseURL = constant.ChannelBaseURLs[constant.ChannelTypeCodex]
		}
		statusCode, models, err = FetchCodexModels(ctx, retryClient, retryBaseURL, &CodexOAuthKey{
			AccessToken: refreshedKey.AccessToken,
			AccountID:   refreshedKey.AccountID,
		}, clientVersion)
		if err != nil {
			return nil, err
		}
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("upstream status: %d", statusCode)
	}
	modelVariants := make([]string, 0, len(models)*2)
	modelVariants = append(modelVariants, models...)
	for _, modelName := range models {
		if modelName == "codex-auto-review" {
			continue
		}
		modelVariants = append(modelVariants, ratio_setting.WithCompactModelSuffix(modelName))
	}
	return modelVariants, nil
}
