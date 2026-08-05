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

func TestRunCodexCredentialAutoRefreshConvergesDirtyCacheBeforeLaterPageError(
	t *testing.T,
) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	encoded, err := common.Marshal(&CodexOAuthKey{
		AccessToken:  "access-token-task-query-error",
		RefreshToken: "refresh-token-task-query-error",
		AccountID:    "account-task-query-error",
		Type:         "codex",
		Expired:      now.Add(time.Hour).Format(time.RFC3339),
	})
	require.NoError(t, err)
	channel := &model.Channel{
		Id:     63641,
		Type:   constant.ChannelTypeCodex,
		Key:    string(encoded),
		Name:   "codex-task-query-error",
		Status: common.ChannelStatusEnabled,
	}

	queryErr := errors.New("later page query failed")
	var pageCalls atomic.Int32
	loadPage := func(offset int) ([]*model.Channel, error) {
		switch pageCalls.Add(1) {
		case 1:
			assert.Zero(t, offset)
			return []*model.Channel{channel}, nil
		case 2:
			assert.Equal(t, codexCredentialRefreshBatchSize, offset)
			return nil, queryErr
		default:
			return nil, errors.New("unexpected extra page query")
		}
	}

	var refreshCalls atomic.Int32
	refresh := func(
		_ context.Context,
		channelID int,
		opts CodexCredentialRefreshOptions,
	) (*CodexCredentialRefreshResult, error) {
		refreshCalls.Add(1)
		assert.Equal(t, channel.Id, channelID)
		assert.False(t, opts.ResetCaches)
		assert.Equal(
			t,
			"refresh-token-task-query-error",
			opts.ExpectedRefreshToken,
		)
		return &CodexCredentialRefreshResult{
			OAuthKey: &CodexOAuthKey{
				AccessToken:  "access-token-task-winner",
				RefreshToken: "refresh-token-task-winner",
				AccountID:    "account-task-winner",
				Type:         "codex",
			},
			Channel:   channel,
			Refreshed: false,
		}, nil
	}

	var cacheRefreshCalls atomic.Int32
	err = runCodexCredentialAutoRefresh(
		context.Background(),
		now,
		loadPage,
		refresh,
		func() error {
			cacheRefreshCalls.Add(1)
			return nil
		},
	)

	require.ErrorIs(t, err, queryErr)
	assert.EqualValues(t, 2, pageCalls.Load())
	assert.EqualValues(t, 1, refreshCalls.Load())
	assert.EqualValues(t, 1, cacheRefreshCalls.Load())
}
