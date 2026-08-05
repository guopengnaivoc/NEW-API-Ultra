package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type CodexCredentialRefreshOptions struct {
	ResetCaches          bool
	ExpectedRefreshToken string
}

type CodexCredentialRefreshResult struct {
	OAuthKey  *CodexOAuthKey
	Channel   *model.Channel
	Refreshed bool
}

type CodexOAuthKey struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`

	AccountID   string `json:"account_id,omitempty"`
	LastRefresh string `json:"last_refresh,omitempty"`
	Email       string `json:"email,omitempty"`
	Type        string `json:"type,omitempty"`
	Expired     string `json:"expired,omitempty"`
}

func parseCodexOAuthKey(raw string) (*CodexOAuthKey, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("codex channel: empty oauth key")
	}
	var key CodexOAuthKey
	if err := common.Unmarshal([]byte(raw), &key); err != nil {
		return nil, errors.New("codex channel: invalid oauth key json")
	}
	return &key, nil
}

type codexOAuthRefreshFunc func(
	ctx context.Context,
	refreshToken string,
	proxyURL string,
) (*CodexOAuthTokenResult, error)

func RefreshCodexChannelCredential(
	ctx context.Context,
	channelID int,
	opts CodexCredentialRefreshOptions,
) (*CodexCredentialRefreshResult, error) {
	return refreshCodexChannelCredential(
		ctx,
		channelID,
		opts,
		RefreshCodexOAuthTokenWithProxy,
	)
}

func refreshCodexChannelCredential(
	ctx context.Context,
	channelID int,
	opts CodexCredentialRefreshOptions,
	refresh codexOAuthRefreshFunc,
) (*CodexCredentialRefreshResult, error) {
	if refresh == nil {
		return nil, errors.New("codex channel: credential refresh function is required")
	}

	var refreshedKey *CodexOAuthKey
	channel, refreshed, err := model.RotateChannelKey(
		ctx,
		channelID,
		func(current *model.Channel) (string, bool, error) {
			if current.Type != constant.ChannelTypeCodex {
				return "", false, fmt.Errorf("channel type is not Codex")
			}
			if current.ChannelInfo.IsMultiKey {
				return "", false, errors.New("codex channel does not support multi-key credential refresh")
			}

			oauthKey, err := parseCodexOAuthKey(strings.TrimSpace(current.Key))
			if err != nil {
				return "", false, err
			}
			currentRefreshToken := strings.TrimSpace(oauthKey.RefreshToken)
			if currentRefreshToken == "" {
				return "", false, errors.New("codex channel: refresh_token is required to refresh credential")
			}

			expectedRefreshToken := strings.TrimSpace(opts.ExpectedRefreshToken)
			if expectedRefreshToken != "" &&
				expectedRefreshToken != currentRefreshToken {
				refreshedKey = oauthKey
				return "", false, nil
			}

			var channelSetting dto.ChannelSettings
			if current.Setting != nil && strings.TrimSpace(*current.Setting) != "" {
				if err := common.Unmarshal(
					[]byte(*current.Setting),
					&channelSetting,
				); err != nil {
					return "", false, fmt.Errorf(
						"codex channel: invalid channel setting: %w",
						err,
					)
				}
			}

			refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			res, err := refresh(
				refreshCtx,
				currentRefreshToken,
				channelSetting.Proxy,
			)
			if err != nil {
				return "", false, err
			}
			if res == nil {
				return "", false, errors.New("codex channel: empty credential refresh response")
			}
			if strings.TrimSpace(res.AccessToken) == "" ||
				strings.TrimSpace(res.RefreshToken) == "" ||
				res.ExpiresAt.IsZero() {
				return "", false, errors.New("codex channel: credential refresh response missing fields")
			}

			oauthKey.AccessToken = strings.TrimSpace(res.AccessToken)
			oauthKey.RefreshToken = strings.TrimSpace(res.RefreshToken)
			oauthKey.LastRefresh = time.Now().Format(time.RFC3339)
			oauthKey.Expired = res.ExpiresAt.Format(time.RFC3339)
			if strings.TrimSpace(oauthKey.Type) == "" {
				oauthKey.Type = "codex"
			}

			if strings.TrimSpace(oauthKey.AccountID) == "" {
				if accountID, ok := ExtractCodexAccountIDFromJWT(
					oauthKey.AccessToken,
				); ok {
					oauthKey.AccountID = accountID
				}
			}
			if strings.TrimSpace(oauthKey.Email) == "" {
				if email, ok := ExtractEmailFromJWT(oauthKey.AccessToken); ok {
					oauthKey.Email = email
				}
			}

			encoded, err := common.Marshal(oauthKey)
			if err != nil {
				return "", false, err
			}
			refreshedKey = oauthKey
			return string(encoded), true, nil
		},
	)
	if err != nil {
		return nil, err
	}
	if refreshedKey == nil {
		return nil, errors.New("codex channel: credential refresh produced no result")
	}

	if opts.ResetCaches {
		if err := model.InitChannelCache(); err != nil {
			return nil, err
		}
	}

	return &CodexCredentialRefreshResult{
		OAuthKey:  refreshedKey,
		Channel:   channel,
		Refreshed: refreshed,
	}, nil
}
