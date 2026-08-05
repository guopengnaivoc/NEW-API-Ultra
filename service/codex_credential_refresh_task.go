package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	codexCredentialRefreshTickInterval = 10 * time.Minute
	codexCredentialRefreshThreshold    = 24 * time.Hour
	codexCredentialRefreshBatchSize    = 200
	codexCredentialRefreshTimeout      = 15 * time.Second
)

var (
	codexCredentialRefreshOnce    sync.Once
	codexCredentialRefreshRunning atomic.Bool
)

type codexCredentialRefreshPageFunc func(
	offset int,
) ([]*model.Channel, error)

type codexCredentialCacheRefreshFunc func() error

func shouldAutoRefreshCodexChannelStatus(status int) bool {
	return status == common.ChannelStatusEnabled || status == common.ChannelStatusAutoDisabled
}

func StartCodexCredentialAutoRefreshTask() {
	codexCredentialRefreshOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("codex credential auto-refresh task started: tick=%s threshold=%s", codexCredentialRefreshTickInterval, codexCredentialRefreshThreshold))

			ticker := time.NewTicker(codexCredentialRefreshTickInterval)
			defer ticker.Stop()

			runCodexCredentialAutoRefreshOnce()
			for range ticker.C {
				runCodexCredentialAutoRefreshOnce()
			}
		})
	})
}

func runCodexCredentialAutoRefreshOnce() {
	if !codexCredentialRefreshRunning.CompareAndSwap(false, true) {
		return
	}
	defer codexCredentialRefreshRunning.Store(false)

	ctx := context.Background()
	now := time.Now()
	_ = runCodexCredentialAutoRefresh(
		ctx,
		now,
		func(offset int) ([]*model.Channel, error) {
			var channels []*model.Channel
			err := model.DB.
				Select("id", "name", "key", "status", "channel_info").
				Where("type = ? AND (status = ? OR status = ?)",
					constant.ChannelTypeCodex,
					common.ChannelStatusEnabled,
					common.ChannelStatusAutoDisabled,
				).
				Order("id asc").
				Limit(codexCredentialRefreshBatchSize).
				Offset(offset).
				Find(&channels).Error
			return channels, err
		},
		RefreshCodexChannelCredential,
		model.InitChannelCache,
	)
}

func runCodexCredentialAutoRefresh(
	ctx context.Context,
	now time.Time,
	loadPage codexCredentialRefreshPageFunc,
	refresh codexChannelCredentialRefreshFunc,
	refreshCache codexCredentialCacheRefreshFunc,
) error {
	var refreshed int
	var scanned int
	var cacheDirty bool
	defer func() {
		if !cacheDirty {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				logger.LogWarn(ctx, fmt.Sprintf("codex credential auto-refresh: InitChannelCache panic: %v", r))
			}
		}()
		if err := refreshCache(); err != nil {
			logger.LogWarn(ctx, "codex credential auto-refresh: channel cache refresh failed: "+err.Error())
		}
	}()

	offset := 0
	for {
		channels, err := loadPage(offset)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("codex credential auto-refresh: query channels failed: %v", err))
			return err
		}
		if len(channels) == 0 {
			break
		}
		offset += codexCredentialRefreshBatchSize

		for _, ch := range channels {
			if ch == nil {
				continue
			}
			scanned++
			if ch.ChannelInfo.IsMultiKey {
				continue
			}

			rawKey := strings.TrimSpace(ch.Key)
			if rawKey == "" {
				continue
			}

			oauthKey, err := parseCodexOAuthKey(rawKey)
			if err != nil {
				continue
			}

			refreshToken := strings.TrimSpace(oauthKey.RefreshToken)
			if refreshToken == "" {
				continue
			}

			expiredAtRaw := strings.TrimSpace(oauthKey.Expired)
			expiredAt, err := time.Parse(time.RFC3339, expiredAtRaw)
			if err == nil && !expiredAt.IsZero() && expiredAt.Sub(now) > codexCredentialRefreshThreshold {
				continue
			}

			refreshCtx, cancel := context.WithTimeout(ctx, codexCredentialRefreshTimeout)
			result, err := refresh(
				refreshCtx,
				ch.Id,
				CodexCredentialRefreshOptions{
					ResetCaches:          false,
					ExpectedRefreshToken: refreshToken,
				},
			)
			cancel()
			if err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("codex credential auto-refresh: channel_id=%d name=%s refresh failed: %v", ch.Id, ch.Name, err))
				continue
			}

			cacheDirty = true
			if result.Refreshed {
				refreshed++
				logger.LogInfo(ctx, fmt.Sprintf("codex credential auto-refresh: channel_id=%d name=%s refreshed, expires_at=%s", ch.Id, ch.Name, result.OAuthKey.Expired))
			} else if common.DebugEnabled {
				logger.LogDebug(ctx, "codex credential auto-refresh: channel_id=%d name=%s already refreshed by another actor", ch.Id, ch.Name)
			}
		}
	}

	if common.DebugEnabled {
		logger.LogDebug(ctx, "codex credential auto-refresh: scanned=%d refreshed=%d", scanned, refreshed)
	}
	return nil
}
