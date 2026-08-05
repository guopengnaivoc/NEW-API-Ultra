package service

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

// BillingSession owns the durable billing operation for one relay request.
// Wallet/subscription and token quota changes are committed together by model.
type BillingSession struct {
	relayInfo *relaycommon.RelayInfo
	operation model.BillingOperation
	trusted   bool
	mu        sync.Mutex
}

// OperationId returns the durable billing operation identifier.
func (s *BillingSession) OperationId() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.operation.Id
}

// Settle atomically changes the reservation to the actual quota.
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	operation, _, err := model.SettleBillingOperation(s.operation.Id, actualQuota)
	if err != nil {
		return err
	}
	s.operation = *operation
	if operation.SettlementLimited {
		noteQuotaClamp(s.relayInfo, &common.QuotaClamp{
			Op:       "BillingOperationSettle",
			Kind:     common.QuotaClampCapacity,
			Original: float64(actualQuota),
			Clamped:  operation.ActualQuota,
		})
	}
	s.syncRelayInfoLocked()
	return nil
}

// Refund atomically reverses the reservation. It remains retryable when the
// database transaction fails and terminalizes as abandoned when an owner was
// hard-deleted.
func (s *BillingSession) Refund(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.operation.Status != model.BillingOperationStatusReserved {
		return
	}
	logger.LogInfo(c, fmt.Sprintf(
		"用户 %d 请求失败, 返还预扣费（quota=%s, funding=%s, operation_id=%d）",
		s.relayInfo.UserId,
		logger.FormatQuota(s.operation.ReservedQuota),
		s.operation.FundingSource,
		s.operation.Id,
	))

	operation, _, err := model.RefundBillingOperation(s.operation.Id)
	if err != nil {
		logger.LogError(c, fmt.Sprintf(
			"billing operation refund failed (operation_id=%d, user_id=%d): %s",
			s.operation.Id,
			s.relayInfo.UserId,
			err.Error(),
		))
		return
	}
	s.operation = *operation
	s.syncRelayInfoLocked()
}

// NeedsRefund reports whether the durable operation still owns a reservation.
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.operation.Status == model.BillingOperationStatusReserved
}

// GetPreConsumedQuota returns the quota currently held by the operation.
func (s *BillingSession) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.operation.ReservedQuota
}

// Reserve atomically increases the reservation to targetQuota. If available
// capacity cannot cover the full target, it preserves the partial reservation
// and returns an error so streaming callers stop before serving more usage.
func (s *BillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.operation.Status != model.BillingOperationStatusReserved ||
		s.trusted ||
		targetQuota <= s.operation.ReservedQuota {
		return nil
	}

	operation, _, err := model.AdjustBillingOperationReservation(
		s.operation.Id,
		targetQuota,
	)
	if err != nil {
		return err
	}
	s.operation = *operation
	if operation.SettlementLimited {
		noteQuotaClamp(s.relayInfo, &common.QuotaClamp{
			Op:       "BillingOperationReserve",
			Kind:     common.QuotaClampCapacity,
			Original: float64(targetQuota),
			Clamped:  operation.ReservedQuota,
		})
	}
	s.syncRelayInfoLocked()
	if operation.SettlementLimited && operation.ReservedQuota < targetQuota {
		return fmt.Errorf(
			"billing reservation capacity insufficient: requested %d, reserved %d",
			targetQuota,
			operation.ReservedQuota,
		)
	}
	return nil
}

// LinkTask creates an accepted asynchronous task and links its reservation in
// the same transaction. The caller must not refund accepted upstream work when
// this persistence step fails.
func (s *BillingSession) LinkTask(task *model.Task, targetQuota int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	limited, err := model.CreateTaskWithBillingOperation(
		task,
		s.operation.Id,
		targetQuota,
	)
	if err != nil {
		return false, err
	}
	s.operation.TaskId = task.ID
	s.operation.ReservedQuota = task.Quota
	s.operation.RequestedQuota = targetQuota
	s.operation.SettlementLimited = limited
	if limited {
		noteQuotaClamp(s.relayInfo, &common.QuotaClamp{
			Op:       "BillingOperationTaskReserve",
			Kind:     common.QuotaClampCapacity,
			Original: float64(targetQuota),
			Clamped:  s.operation.ReservedQuota,
		})
	}
	s.syncRelayInfoLocked()
	return limited, nil
}

// NewBillingSession creates the durable operation according to the user's
// billing preference. Subscription and wallet fallback decisions retain the
// existing API behavior while each selected reserve is a single transaction.
func NewBillingSession(
	c *gin.Context,
	relayInfo *relaycommon.RelayInfo,
	preConsumedQuota int,
) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(
			fmt.Errorf("relayInfo is nil"),
			types.ErrorCodeInvalidRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if preConsumedQuota < 0 || preConsumedQuota > common.MaxQuota {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("invalid pre-consume quota: %d", preConsumedQuota),
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if relayInfo.RequestId == "" {
		relayInfo.RequestId = common.NewRequestId()
	}

	preference := common.NormalizeBillingPreference(
		relayInfo.UserSetting.BillingPreference,
	)
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return nil, types.NewError(
				err,
				types.ErrorCodeQueryDataError,
				types.ErrOptionWithSkipRetry(),
			)
		}
		relayInfo.UserQuota = userQuota
		if userQuota <= 0 || userQuota < preConsumedQuota {
			return nil, insufficientWalletQuotaError(userQuota, preConsumedQuota)
		}

		effectiveQuota := preConsumedQuota
		trusted := shouldTrustBillingSession(c, relayInfo, BillingSourceWallet)
		if trusted {
			effectiveQuota = 0
			logger.LogInfo(c, fmt.Sprintf(
				"用户 %d 额度充足, 信任且不需要预扣费 (funding=%s)",
				relayInfo.UserId,
				BillingSourceWallet,
			))
		} else if effectiveQuota > 0 {
			logger.LogInfo(c, fmt.Sprintf(
				"用户 %d 需要预扣费 %s (funding=%s)",
				relayInfo.UserId,
				logger.FormatQuota(effectiveQuota),
				BillingSourceWallet,
			))
		}

		return reserveBillingSession(relayInfo, BillingSourceWallet, effectiveQuota, trusted)
	}
	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subscriptionQuota := preConsumedQuota
		if subscriptionQuota <= 0 {
			subscriptionQuota = 1
		}
		logger.LogInfo(c, fmt.Sprintf(
			"用户 %d 需要预扣费 %s (funding=%s)",
			relayInfo.UserId,
			logger.FormatQuota(subscriptionQuota),
			BillingSourceSubscription,
		))
		return reserveBillingSession(
			relayInfo,
			BillingSourceSubscription,
			subscriptionQuota,
			false,
		)
	}

	switch preference {
	case "subscription_only":
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, apiErr := tryWallet()
		if apiErr != nil &&
			apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
			return trySubscription()
		}
		return session, apiErr
	case "subscription_first":
		fallthrough
	default:
		hasSubscription, err := model.HasActiveUserSubscription(relayInfo.UserId)
		if err != nil {
			return nil, types.NewError(
				err,
				types.ErrorCodeQueryDataError,
				types.ErrOptionWithSkipRetry(),
			)
		}
		if !hasSubscription {
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr == nil ||
			apiErr.GetErrorCode() != types.ErrorCodeInsufficientUserQuota {
			return session, apiErr
		}
		allowWallet, err := model.UserActiveSubscriptionsAllowWalletOverflow(
			relayInfo.UserId,
		)
		if err != nil {
			return nil, types.NewError(
				err,
				types.ErrorCodeQueryDataError,
				types.ErrOptionWithSkipRetry(),
			)
		}
		if allowWallet {
			return tryWallet()
		}
		return nil, apiErr
	}
}

func reserveBillingSession(
	relayInfo *relaycommon.RelayInfo,
	fundingSource string,
	quota int,
	trusted bool,
) (*BillingSession, *types.NewAPIError) {
	operation, _, err := model.ReserveBillingOperation(
		model.BillingOperationReserveRequest{
			RequestId:     relayInfo.RequestId,
			UserId:        relayInfo.UserId,
			TokenId:       relayInfo.TokenId,
			TokenKey:      relayInfo.TokenKey,
			TokenCharged:  !relayInfo.IsPlayground,
			FundingSource: fundingSource,
			Quota:         quota,
		},
	)
	if err != nil {
		return nil, billingOperationAPIError(err)
	}

	session := &BillingSession{
		relayInfo: relayInfo,
		operation: *operation,
		trusted:   trusted,
	}
	session.syncRelayInfoLocked()
	return session, nil
}

func shouldTrustBillingSession(
	c *gin.Context,
	relayInfo *relaycommon.RelayInfo,
	fundingSource string,
) bool {
	if relayInfo.ForcePreConsume ||
		fundingSource != BillingSourceWallet {
		return false
	}
	trustQuota := common.GetTrustQuota()
	if trustQuota <= 0 {
		return false
	}
	tokenTrusted := relayInfo.TokenUnlimited
	if !tokenTrusted {
		tokenTrusted = c.GetInt("token_quota") > trustQuota
	}
	return tokenTrusted && relayInfo.UserQuota > trustQuota
}

func insufficientWalletQuotaError(
	userQuota int,
	requiredQuota int,
) *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		fmt.Errorf(
			"预扣费额度失败, 用户剩余额度: %s, 需要预扣费额度: %s",
			logger.FormatQuota(userQuota),
			logger.FormatQuota(requiredQuota),
		),
		types.ErrorCodeInsufficientUserQuota,
		http.StatusForbidden,
		types.ErrOptionWithSkipRetry(),
		types.ErrOptionWithNoRecordErrorLog(),
	)
}

func billingOperationAPIError(err error) *types.NewAPIError {
	switch {
	case errors.Is(err, model.ErrBillingOperationFundingQuota):
		return types.NewErrorWithStatusCode(
			fmt.Errorf("订阅或钱包额度不足: %w", err),
			types.ErrorCodeInsufficientUserQuota,
			http.StatusForbidden,
			types.ErrOptionWithSkipRetry(),
			types.ErrOptionWithNoRecordErrorLog(),
		)
	case errors.Is(err, model.ErrBillingOperationTokenInvalid),
		errors.Is(err, model.ErrBillingOperationTokenQuota):
		return types.NewErrorWithStatusCode(
			err,
			types.ErrorCodePreConsumeTokenQuotaFailed,
			http.StatusForbidden,
			types.ErrOptionWithSkipRetry(),
			types.ErrOptionWithNoRecordErrorLog(),
		)
	case errors.Is(err, model.ErrBillingOperationOwnerMissing):
		return types.NewError(
			err,
			types.ErrorCodeQueryDataError,
			types.ErrOptionWithSkipRetry(),
		)
	default:
		return types.NewError(
			err,
			types.ErrorCodeUpdateDataError,
			types.ErrOptionWithSkipRetry(),
		)
	}
}

func (s *BillingSession) syncRelayInfoLocked() {
	info := s.relayInfo
	operation := &s.operation
	info.FinalPreConsumedQuota = operation.ReservedQuota
	info.BillingSource = operation.FundingSource

	if operation.FundingSource != BillingSourceSubscription {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
		info.SubscriptionPostDelta = 0
		info.SubscriptionAmountTotal = 0
		info.SubscriptionAmountUsedAfterPreConsume = 0
		info.SubscriptionPlanId = 0
		info.SubscriptionPlanTitle = ""
		return
	}

	info.SubscriptionId = operation.SubscriptionId
	info.SubscriptionPreConsumed = int64(operation.ReservedQuota)
	finalQuota := operation.ReservedQuota
	switch operation.Status {
	case model.BillingOperationStatusSettled:
		finalQuota = operation.ActualQuota
	case model.BillingOperationStatusRefunded:
		finalQuota = 0
	}
	info.SubscriptionPostDelta = int64(finalQuota - operation.ReservedQuota)

	var subscription model.UserSubscription
	if err := model.DB.Where("id = ?", operation.SubscriptionId).
		First(&subscription).Error; err != nil {
		common.SysLog(fmt.Sprintf(
			"error loading billing operation subscription metadata (operation_id=%d): %s",
			operation.Id,
			err.Error(),
		))
		return
	}
	info.SubscriptionAmountTotal = subscription.AmountTotal
	info.SubscriptionAmountUsedAfterPreConsume =
		subscription.AmountUsed - info.SubscriptionPostDelta

	planInfo, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(
		operation.SubscriptionId,
	)
	if err != nil {
		common.SysLog(fmt.Sprintf(
			"error loading billing operation plan metadata (operation_id=%d): %s",
			operation.Id,
			err.Error(),
		))
		return
	}
	info.SubscriptionPlanId = planInfo.PlanId
	info.SubscriptionPlanTitle = planInfo.PlanTitle
}
