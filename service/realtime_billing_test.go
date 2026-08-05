package service

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func realtimeBillingUsage(tokens int) *dto.RealtimeUsage {
	return &dto.RealtimeUsage{
		TotalTokens: tokens,
		InputTokens: tokens,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens: tokens,
		},
	}
}

func realtimeBillingQuota(usage *dto.RealtimeUsage) int {
	quota, _ := calculateAudioQuota(QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens: usage.InputTokenDetails.TextTokens,
		},
		ModelRatio:           1,
		CompletionRatio:      1,
		AudioRatio:           1,
		AudioCompletionRatio: 1,
		GroupRatio:           1,
	})
	return quota
}

func setRealtimeRatioPricing(
	info *relaycommon.RelayInfo,
	modelRatio float64,
	groupRatio float64,
) {
	info.PriceData = types.PriceData{
		ModelRatio:           modelRatio,
		CompletionRatio:      1,
		AudioRatio:           1,
		AudioCompletionRatio: 1,
		GroupRatioInfo: types.GroupRatioInfo{
			GroupRatio: groupRatio,
		},
	}
}

func TestRealtimeProgressiveWalletChargeHasOneBillingOwner(t *testing.T) {
	truncate(t)
	require.NoError(t, model.MigrateTokenKeys())
	const userID, tokenID = 506, 506
	firstUsage := realtimeBillingUsage(10)
	usage := realtimeBillingUsage(20)
	actualQuota := realtimeBillingQuota(usage)
	initialReservation := realtimeBillingQuota(firstUsage)
	initialQuota := actualQuota * 4
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, "realtime-wallet-owner", initialQuota)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"realtime-wallet-single-owner",
		userID,
		tokenID,
		"realtime-wallet-owner",
		"wallet_only",
	)
	info.OriginModelName = "gpt-4.1"
	info.UsingGroup = "default"
	setRealtimeRatioPricing(info, 1, 1)

	require.Nil(t, PreConsumeBilling(context, initialReservation, info))
	require.NoError(t, PreWssConsumeQuota(context, info, firstUsage))
	require.NoError(t, PreWssConsumeQuota(context, info, usage))
	require.NoError(t, SettleBilling(context, info, actualQuota))

	assert.Equal(t, initialQuota-actualQuota, getUserQuota(t, userID))
	assert.Equal(t, initialQuota-actualQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, actualQuota, getTokenUsedQuota(t, tokenID))

	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, model.BillingOperationStatusSettled, operation.Status)
	assert.Equal(t, actualQuota, operation.ActualQuota)
}

func TestRealtimeProgressiveSubscriptionChargeHasOneBillingOwner(t *testing.T) {
	truncate(t)
	require.NoError(t, model.MigrateTokenKeys())
	const userID, tokenID, subscriptionID = 507, 507, 507
	firstUsage := realtimeBillingUsage(10)
	usage := realtimeBillingUsage(20)
	actualQuota := realtimeBillingQuota(usage)
	initialReservation := realtimeBillingQuota(firstUsage)
	initialQuota := actualQuota * 4
	seedUser(t, userID, 0)
	seedToken(
		t,
		tokenID,
		userID,
		"realtime-subscription-owner",
		initialQuota,
	)

	plan := model.SubscriptionPlan{
		Title:            "realtime-subscription-plan",
		TotalAmount:      int64(initialQuota),
		Enabled:          common.GetPointer(true),
		QuotaResetPeriod: model.SubscriptionResetNever,
	}
	require.NoError(t, model.DB.Create(&plan).Error)
	subscription := model.UserSubscription{
		Id:          subscriptionID,
		UserId:      userID,
		PlanId:      plan.Id,
		AmountTotal: int64(initialQuota),
		Status:      "active",
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(&subscription).Error)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"realtime-subscription-single-owner",
		userID,
		tokenID,
		"realtime-subscription-owner",
		"subscription_only",
	)
	info.OriginModelName = "gpt-4.1"
	info.UsingGroup = "default"
	setRealtimeRatioPricing(info, 1, 1)

	require.Nil(t, PreConsumeBilling(context, initialReservation, info))
	require.NoError(t, PreWssConsumeQuota(context, info, firstUsage))
	require.NoError(t, PreWssConsumeQuota(context, info, usage))
	require.NoError(t, SettleBilling(context, info, actualQuota))

	assert.EqualValues(t, actualQuota, getSubscriptionUsed(t, subscriptionID))
	assert.Equal(
		t,
		initialQuota-actualQuota,
		getTokenRemainQuota(t, tokenID),
	)
	assert.Equal(t, actualQuota, getTokenUsedQuota(t, tokenID))

	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, model.BillingOperationStatusSettled, operation.Status)
	assert.Equal(t, actualQuota, operation.ActualQuota)
}

func TestRealtimeFixedPriceDoesNotAddTokenBasedReservation(t *testing.T) {
	truncate(t)
	require.NoError(t, model.MigrateTokenKeys())
	const userID, tokenID = 508, 508
	usage := realtimeBillingUsage(20)
	tokenBasedQuota := realtimeBillingQuota(usage)
	fixedQuota := tokenBasedQuota / 2
	initialQuota := tokenBasedQuota * 4
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, "realtime-fixed-price", initialQuota)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"realtime-fixed-price",
		userID,
		tokenID,
		"realtime-fixed-price",
		"wallet_only",
	)
	info.OriginModelName = "gpt-4.1"
	info.UsingGroup = "default"
	info.PriceData.UsePrice = true

	require.Nil(t, PreConsumeBilling(context, fixedQuota, info))
	require.NoError(t, PreWssConsumeQuota(context, info, usage))

	assert.Equal(t, initialQuota-fixedQuota, getUserQuota(t, userID))
	assert.Equal(t, initialQuota-fixedQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, fixedQuota, getTokenUsedQuota(t, tokenID))
}

func TestRealtimeFreeModelDoesNotRequireBillingReservation(t *testing.T) {
	context := newBillingSessionTestContext()
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			FreeModel: true,
		},
	}

	require.NoError(t, PreWssConsumeQuota(context, info, realtimeBillingUsage(10)))
	assert.Nil(t, info.Billing)
}

func TestRealtimeProgressiveTieredReservationUsesFrozenSnapshot(t *testing.T) {
	truncate(t)
	require.NoError(t, model.MigrateTokenKeys())
	const userID, tokenID = 514, 514
	seedUser(t, userID, 100)
	seedToken(t, tokenID, userID, "realtime-tiered-snapshot", 100)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"realtime-tiered-snapshot",
		userID,
		tokenID,
		"realtime-tiered-snapshot",
		"wallet_only",
	)
	tieredInfo := makeRelayInfo(flatExpr, 1, 1, 0)
	info.TieredBillingSnapshot = tieredInfo.TieredBillingSnapshot
	info.FinalPreConsumedQuota = tieredInfo.FinalPreConsumedQuota
	info.OriginModelName = "tiered-realtime"
	info.UsingGroup = "default"

	require.Nil(t, PreConsumeBilling(context, tieredInfo.FinalPreConsumedQuota, info))
	require.NoError(t, PreWssConsumeQuota(context, info, realtimeBillingUsage(10)))

	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, 10, operation.ReservedQuota)
	assert.False(t, operation.SettlementLimited)
}

func TestRealtimeProgressiveWalletReservationFailsWhenCapacityIsShort(t *testing.T) {
	truncate(t)
	require.NoError(t, model.MigrateTokenKeys())
	const userID, tokenID = 509, 509
	cumulativeUsage := realtimeBillingUsage(10)
	initialReservation := 4
	availableQuota := 7
	seedUser(t, userID, availableQuota)
	seedToken(t, tokenID, userID, "realtime-wallet-capacity", 100)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"realtime-wallet-capacity",
		userID,
		tokenID,
		"realtime-wallet-capacity",
		"wallet_only",
	)
	info.OriginModelName = "gpt-4.1"
	info.UsingGroup = "default"
	setRealtimeRatioPricing(info, 1, 1)

	require.Nil(t, PreConsumeBilling(context, initialReservation, info))
	err := PreWssConsumeQuota(context, info, cumulativeUsage)

	require.Error(t, err)
	assert.Zero(t, getUserQuota(t, userID))
	assert.Equal(t, 100-availableQuota, getTokenRemainQuota(t, tokenID))
	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, availableQuota, operation.ReservedQuota)
	assert.Equal(t, 10, operation.RequestedQuota)
	assert.True(t, operation.SettlementLimited)
}

func TestRealtimeProgressiveSubscriptionReservationFailsWhenCapacityIsShort(t *testing.T) {
	truncate(t)
	require.NoError(t, model.MigrateTokenKeys())
	const userID, tokenID, subscriptionID = 510, 510, 510
	cumulativeUsage := realtimeBillingUsage(10)
	initialReservation := 4
	availableQuota := 7
	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "realtime-subscription-capacity", 100)

	plan := model.SubscriptionPlan{
		Title:            "realtime-subscription-capacity",
		TotalAmount:      int64(availableQuota),
		Enabled:          common.GetPointer(true),
		QuotaResetPeriod: model.SubscriptionResetNever,
	}
	require.NoError(t, model.DB.Create(&plan).Error)
	subscription := model.UserSubscription{
		Id:          subscriptionID,
		UserId:      userID,
		PlanId:      plan.Id,
		AmountTotal: int64(availableQuota),
		Status:      "active",
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(&subscription).Error)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"realtime-subscription-capacity",
		userID,
		tokenID,
		"realtime-subscription-capacity",
		"subscription_only",
	)
	info.OriginModelName = "gpt-4.1"
	info.UsingGroup = "default"
	setRealtimeRatioPricing(info, 1, 1)

	require.Nil(t, PreConsumeBilling(context, initialReservation, info))
	err := PreWssConsumeQuota(context, info, cumulativeUsage)

	require.Error(t, err)
	assert.EqualValues(t, availableQuota, getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, 100-availableQuota, getTokenRemainQuota(t, tokenID))
	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, availableQuota, operation.ReservedQuota)
	assert.Equal(t, 10, operation.RequestedQuota)
	assert.True(t, operation.SettlementLimited)
}

func TestRealtimeProgressiveReservationFailsWhenTokenCapacityIsShort(t *testing.T) {
	truncate(t)
	require.NoError(t, model.MigrateTokenKeys())
	const userID, tokenID = 511, 511
	cumulativeUsage := realtimeBillingUsage(10)
	initialReservation := 4
	availableQuota := 7
	seedUser(t, userID, 100)
	seedToken(t, tokenID, userID, "realtime-token-capacity", availableQuota)

	context := newBillingSessionTestContext()
	info := billingSessionRelayInfo(
		"realtime-token-capacity",
		userID,
		tokenID,
		"realtime-token-capacity",
		"wallet_only",
	)
	info.OriginModelName = "gpt-4.1"
	info.UsingGroup = "default"
	setRealtimeRatioPricing(info, 1, 1)

	require.Nil(t, PreConsumeBilling(context, initialReservation, info))
	err := PreWssConsumeQuota(context, info, cumulativeUsage)

	require.Error(t, err)
	assert.Equal(t, 100-availableQuota, getUserQuota(t, userID))
	assert.Zero(t, getTokenRemainQuota(t, tokenID))
	operation := loadBillingOperationByRequestID(t, info.RequestId)
	assert.Equal(t, availableQuota, operation.ReservedQuota)
	assert.Equal(t, 10, operation.RequestedQuota)
	assert.True(t, operation.SettlementLimited)
}

func TestRealtimeConcurrentReservationsFailTheCapacityLimitedSession(t *testing.T) {
	truncate(t)
	require.NoError(t, model.MigrateTokenKeys())
	const userID, tokenID = 515, 515
	seedUser(t, userID, 7)
	seedToken(t, tokenID, userID, "realtime-concurrent-capacity", 100)

	context := newBillingSessionTestContext()
	firstInfo := billingSessionRelayInfo(
		"realtime-concurrent-capacity-first",
		userID,
		tokenID,
		"realtime-concurrent-capacity",
		"wallet_only",
	)
	secondInfo := billingSessionRelayInfo(
		"realtime-concurrent-capacity-second",
		userID,
		tokenID,
		"realtime-concurrent-capacity",
		"wallet_only",
	)
	for _, info := range []*relaycommon.RelayInfo{firstInfo, secondInfo} {
		info.OriginModelName = "gpt-4.1"
		info.UsingGroup = "default"
		setRealtimeRatioPricing(info, 1, 1)
		require.Nil(t, PreConsumeBilling(context, 1, info))
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	contexts := []*gin.Context{
		newBillingSessionTestContext(),
		newBillingSessionTestContext(),
	}
	var workers sync.WaitGroup
	for index, info := range []*relaycommon.RelayInfo{firstInfo, secondInfo} {
		workers.Add(1)
		go func(
			relayInfo *relaycommon.RelayInfo,
			workerContext *gin.Context,
		) {
			defer workers.Done()
			<-start
			results <- PreWssConsumeQuota(
				workerContext,
				relayInfo,
				realtimeBillingUsage(5),
			)
		}(info, contexts[index])
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, failures)
	assert.Zero(t, getUserQuota(t, userID))
	assert.Equal(t, 93, getTokenRemainQuota(t, tokenID))
	assert.Equal(
		t,
		7,
		firstInfo.Billing.GetPreConsumedQuota()+
			secondInfo.Billing.GetPreConsumedQuota(),
	)
}

func TestRealtimeProgressiveReservationUsesFrozenPricingSnapshot(t *testing.T) {
	const customModel = "realtime-frozen-ratio-model"
	testCases := []struct {
		name                   string
		modelName              string
		usage                  *dto.RealtimeUsage
		completionRatio        float64
		audioRatio             float64
		audioCompletionRatio   float64
		expectedQuota          int
		mutatedAutoGroup       string
		changeLivePricingRatio func(t *testing.T)
	}{
		{
			name:                 "model ratio",
			modelName:            "gpt-4.1",
			usage:                realtimeBillingUsage(10),
			completionRatio:      1,
			audioRatio:           1,
			audioCompletionRatio: 1,
			expectedQuota:        10,
			changeLivePricingRatio: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(
					`{"gpt-4.1":0.5,"realtime-frozen-ratio-model":1}`,
				))
			},
		},
		{
			name:                 "group ratio",
			modelName:            "gpt-4.1",
			usage:                realtimeBillingUsage(10),
			completionRatio:      1,
			audioRatio:           1,
			audioCompletionRatio: 1,
			expectedQuota:        10,
			mutatedAutoGroup:     "realtime-mutated-auto",
			changeLivePricingRatio: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
					`{"default":0.5,"realtime-mutated-auto":0.25}`,
				))
			},
		},
		{
			name:      "completion ratio",
			modelName: customModel,
			usage: &dto.RealtimeUsage{
				TotalTokens:  10,
				OutputTokens: 10,
				OutputTokenDetails: dto.OutputTokenDetails{
					TextTokens: 10,
				},
			},
			completionRatio:      2,
			audioRatio:           3,
			audioCompletionRatio: 4,
			expectedQuota:        20,
			changeLivePricingRatio: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(
					`{"realtime-frozen-ratio-model":0.5}`,
				))
			},
		},
		{
			name:      "audio input ratio",
			modelName: customModel,
			usage: &dto.RealtimeUsage{
				TotalTokens: 10,
				InputTokens: 10,
				InputTokenDetails: dto.InputTokenDetails{
					AudioTokens: 10,
				},
			},
			completionRatio:      2,
			audioRatio:           3,
			audioCompletionRatio: 4,
			expectedQuota:        30,
			changeLivePricingRatio: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(
					`{"realtime-frozen-ratio-model":0.5}`,
				))
			},
		},
		{
			name:      "audio completion ratio",
			modelName: customModel,
			usage: &dto.RealtimeUsage{
				TotalTokens:  10,
				OutputTokens: 10,
				OutputTokenDetails: dto.OutputTokenDetails{
					AudioTokens: 10,
				},
			},
			completionRatio:      2,
			audioRatio:           3,
			audioCompletionRatio: 4,
			expectedQuota:        120,
			changeLivePricingRatio: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(
					`{"realtime-frozen-ratio-model":0.5}`,
				))
			},
		},
	}

	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			truncate(t)
			require.NoError(t, model.MigrateTokenKeys())
			originalModelRatios := ratio_setting.ModelRatio2JSONString()
			originalGroupRatios := ratio_setting.GroupRatio2JSONString()
			originalCompletionRatios := ratio_setting.CompletionRatio2JSONString()
			originalAudioRatios := ratio_setting.AudioRatio2JSONString()
			originalAudioCompletionRatios := ratio_setting.AudioCompletionRatio2JSONString()
			t.Cleanup(func() {
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModelRatios))
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
				require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(originalCompletionRatios))
				require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(originalAudioRatios))
				require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(originalAudioCompletionRatios))
			})
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(
				`{"gpt-4.1":1,"realtime-frozen-ratio-model":1}`,
			))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
			require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(
				`{"realtime-frozen-ratio-model":2}`,
			))
			require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(
				`{"realtime-frozen-ratio-model":3}`,
			))
			require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(
				`{"realtime-frozen-ratio-model":4}`,
			))

			userID := 512 + index
			tokenID := 512 + index
			channelID := 612 + index
			seedUser(t, userID, 10_000_000)
			seedToken(t, tokenID, userID, "realtime-frozen-"+testCase.name, 10_000_000)
			seedChannel(t, channelID)

			context := newBillingSessionTestContext()
			info := billingSessionRelayInfo(
				"realtime-frozen-"+testCase.name,
				userID,
				tokenID,
				"realtime-frozen-"+testCase.name,
				"wallet_only",
			)
			info.OriginModelName = testCase.modelName
			info.UsingGroup = "default"
			info.StartTime = time.Now()
			info.ChannelMeta = &relaycommon.ChannelMeta{
				ChannelId: channelID,
			}
			setRealtimeRatioPricing(info, 1, 1)
			info.PriceData.CompletionRatio = testCase.completionRatio
			info.PriceData.AudioRatio = testCase.audioRatio
			info.PriceData.AudioCompletionRatio = testCase.audioCompletionRatio

			require.Nil(t, PreConsumeBilling(context, 1, info))
			testCase.changeLivePricingRatio(t)
			if testCase.mutatedAutoGroup != "" {
				context.Set("auto_group", testCase.mutatedAutoGroup)
			}
			require.NoError(t, PreWssConsumeQuota(context, info, testCase.usage))

			operation := loadBillingOperationByRequestID(t, info.RequestId)
			assert.Equal(t, testCase.expectedQuota, operation.ReservedQuota)
			assert.False(t, operation.SettlementLimited)
			if testCase.mutatedAutoGroup != "" {
				assert.Equal(t, "default", info.UsingGroup)
			}

			PostWssConsumeQuota(
				context,
				info,
				testCase.modelName,
				testCase.usage,
				"",
			)
			operation = loadBillingOperationByRequestID(t, info.RequestId)
			assert.Equal(t, model.BillingOperationStatusSettled, operation.Status)
			assert.Equal(t, testCase.expectedQuota, operation.ActualQuota)
			assert.False(t, operation.SettlementLimited)
		})
	}
}
