package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	BillingOperationFundingWallet       = "wallet"
	BillingOperationFundingSubscription = "subscription"

	BillingOperationStatusReserved  = "reserved"
	BillingOperationStatusSettled   = "settled"
	BillingOperationStatusRefunded  = "refunded"
	BillingOperationStatusAbandoned = "abandoned"
)

type BillingOperationOutcome string

const (
	BillingOperationOutcomeSettle BillingOperationOutcome = "settle"
	BillingOperationOutcomeRefund BillingOperationOutcome = "refund"
)

var (
	ErrBillingOperationInvalid        = errors.New("invalid billing operation")
	ErrBillingOperationConflict       = errors.New("billing operation conflict")
	ErrBillingOperationTransition     = errors.New("invalid billing operation transition")
	ErrBillingOperationFundingInvalid = errors.New("billing operation funding state invalid")
	ErrBillingOperationFundingQuota   = errors.New("billing operation funding quota insufficient")
	ErrBillingOperationTokenInvalid   = errors.New("billing operation token invalid")
	ErrBillingOperationTokenQuota     = errors.New("billing operation token quota insufficient")
	ErrBillingOperationOwnerMissing   = errors.New("billing operation owner missing")
)

type BillingOperation struct {
	Id                 int64  `json:"id"`
	RequestId          string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	TaskId             int64  `json:"task_id" gorm:"index"`
	UserId             int    `json:"user_id" gorm:"index"`
	TokenId            int    `json:"token_id" gorm:"index"`
	TokenCharged       bool   `json:"token_charged"`
	TokenUnlimited     bool   `json:"token_unlimited"`
	TokenRemainCharged bool   `json:"token_remain_charged"`
	FundingSource      string `json:"funding_source" gorm:"type:varchar(16);index"`
	SubscriptionId     int    `json:"subscription_id" gorm:"index"`
	ReservedQuota      int    `json:"reserved_quota"`
	RequestedQuota     int    `json:"requested_quota"`
	ActualQuota        int    `json:"actual_quota"`
	SettlementLimited  bool   `json:"settlement_limited"`
	FailureReason      string `json:"failure_reason" gorm:"type:varchar(64)"`
	Status             string `json:"status" gorm:"type:varchar(16);index"`
	CreatedAt          int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt          int64  `json:"updated_at" gorm:"bigint;index"`
}

func (operation *BillingOperation) BeforeCreate(*gorm.DB) error {
	now := common.GetTimestamp()
	operation.CreatedAt = now
	operation.UpdatedAt = now
	return nil
}

func (operation *BillingOperation) BeforeUpdate(*gorm.DB) error {
	operation.UpdatedAt = common.GetTimestamp()
	return nil
}

type BillingOperationReserveRequest struct {
	RequestId     string
	UserId        int
	TokenId       int
	TokenKey      string
	TokenCharged  bool
	FundingSource string
	Quota         int
}

type TaskBillingTransitionResult struct {
	Changed           bool
	BillingChanged    bool
	TaskChanged       bool
	PreviousQuota     int
	RequestedQuota    int
	FinalQuota        int
	Delta             int
	OperationId       int64
	SettlementLimited bool
	Abandoned         bool
	FailureReason     string
	LegacyNoRefund    bool
}

type billingOperationRows struct {
	user         *User
	subscription *UserSubscription
	token        *Token
}

type billingOperationOwnerMissingError struct {
	kind string
}

func (err *billingOperationOwnerMissingError) Error() string {
	return fmt.Sprintf("%s: %s", ErrBillingOperationOwnerMissing, err.kind)
}

func (err *billingOperationOwnerMissingError) Unwrap() error {
	return ErrBillingOperationOwnerMissing
}

func ReserveBillingOperation(input BillingOperationReserveRequest) (*BillingOperation, bool, error) {
	input.RequestId = strings.TrimSpace(input.RequestId)
	if input.RequestId == "" || len(input.RequestId) > 64 || input.UserId <= 0 ||
		input.Quota < 0 || input.Quota > common.MaxQuota ||
		!validBillingFundingSource(input.FundingSource) ||
		(input.FundingSource == BillingOperationFundingSubscription && input.Quota <= 0) ||
		(input.TokenCharged && (input.TokenId <= 0 || strings.TrimSpace(input.TokenKey) == "")) {
		return nil, false, ErrBillingOperationInvalid
	}

	var operation BillingOperation
	created := false
	now := GetDBTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing BillingOperation
		result := lockForUpdate(tx).
			Where("request_id = ?", input.RequestId).
			Limit(1).
			Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			if !sameBillingOperationRequest(&existing, input) {
				return ErrBillingOperationConflict
			}
			operation = existing
			return nil
		}

		operation = BillingOperation{
			RequestId:     input.RequestId,
			UserId:        input.UserId,
			TokenId:       input.TokenId,
			TokenCharged:  input.TokenCharged,
			FundingSource: input.FundingSource,
			ReservedQuota: input.Quota,
			Status:        BillingOperationStatusReserved,
		}
		if err := tx.Create(&operation).Error; err != nil {
			return err
		}

		var subscription *UserSubscription
		if input.FundingSource == BillingOperationFundingSubscription {
			selected, err := selectBillingSubscriptionTx(tx, input.UserId, input.Quota, now)
			if err != nil {
				return err
			}
			subscription = selected
			operation.SubscriptionId = selected.Id
		}

		var user User
		if err := lockForUpdate(tx).
			Where("id = ?", input.UserId).
			First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return &billingOperationOwnerMissingError{kind: "user"}
			}
			return err
		}

		if input.FundingSource == BillingOperationFundingWallet && input.Quota > 0 {
			result = tx.Model(&User{}).
				Where("id = ? AND quota >= ?", input.UserId, input.Quota).
				Update("quota", gorm.Expr("quota - ?", input.Quota))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrBillingOperationFundingQuota
			}
		}
		if subscription != nil {
			currentUsed := subscription.AmountUsed
			update := tx.Model(&UserSubscription{}).
				Where("id = ? AND amount_used = ?", subscription.Id, currentUsed).
				Updates(map[string]any{
					"amount_used": gorm.Expr("amount_used + ?", input.Quota),
					"updated_at":  now,
				})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrBillingOperationFundingQuota
			}
			subscription.AmountUsed += int64(input.Quota)
		}

		if input.TokenCharged {
			var token Token
			if err := lockForUpdate(tx).
				Where(map[string]any{
					"id":      input.TokenId,
					"user_id": input.UserId,
					"key":     HashTokenKey(input.TokenKey),
				}).
				First(&token).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrBillingOperationTokenInvalid
				}
				return err
			}
			if token.Status != common.TokenStatusEnabled ||
				(token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp()) {
				return ErrBillingOperationTokenInvalid
			}

			operation.TokenUnlimited = token.UnlimitedQuota
			operation.TokenRemainCharged = !token.UnlimitedQuota
			if input.Quota > 0 {
				updates := map[string]any{
					"used_quota":    gorm.Expr("used_quota + ?", input.Quota),
					"accessed_time": common.GetTimestamp(),
				}
				tokenUpdate := tx.Model(&Token{}).
					Where(
						"id = ? AND user_id = ? AND used_quota <= ?",
						input.TokenId,
						input.UserId,
						common.MaxQuota-input.Quota,
					)
				if operation.TokenRemainCharged {
					tokenUpdate = tokenUpdate.Where("remain_quota >= ?", input.Quota)
					updates["remain_quota"] = gorm.Expr("remain_quota - ?", input.Quota)
				}
				result = tokenUpdate.Updates(updates)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrBillingOperationTokenQuota
				}
			}
		}

		if err := tx.Save(&operation).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		var existing BillingOperation
		if lookupErr := DB.Where("request_id = ?", input.RequestId).First(&existing).Error; lookupErr == nil {
			if !sameBillingOperationRequest(&existing, input) {
				return nil, false, ErrBillingOperationConflict
			}
			return &existing, false, nil
		}
		return nil, false, err
	}
	return &operation, created, nil
}

func AdjustBillingOperationReservation(id int64, targetQuota int) (*BillingOperation, bool, error) {
	if id <= 0 || targetQuota < 0 || targetQuota > common.MaxQuota {
		return nil, false, ErrBillingOperationInvalid
	}

	var operation BillingOperation
	changed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ?", id).First(&operation).Error; err != nil {
			return err
		}
		var err error
		changed, err = adjustBillingOperationReservationTx(tx, &operation, targetQuota)
		return err
	})
	return &operation, changed, err
}

func SettleBillingOperation(id int64, requestedQuota int) (*BillingOperation, bool, error) {
	return transitionBillingOperation(id, requestedQuota, BillingOperationOutcomeSettle)
}

func RefundBillingOperation(id int64) (*BillingOperation, bool, error) {
	return transitionBillingOperation(id, 0, BillingOperationOutcomeRefund)
}

func transitionBillingOperation(
	id int64,
	requestedQuota int,
	outcome BillingOperationOutcome,
) (*BillingOperation, bool, error) {
	if id <= 0 || requestedQuota < 0 || requestedQuota > common.MaxQuota ||
		!validBillingOperationOutcome(outcome) {
		return nil, false, ErrBillingOperationInvalid
	}

	var operation BillingOperation
	changed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ?", id).First(&operation).Error; err != nil {
			return err
		}
		var err error
		changed, _, err = transitionBillingOperationTx(
			tx,
			&operation,
			requestedQuota,
			outcome,
			false,
		)
		return err
	})
	return &operation, changed, err
}

func CreateTaskWithBillingOperation(
	task *Task,
	operationID int64,
	targetQuota int,
) (bool, error) {
	if task == nil || task.ID != 0 || operationID <= 0 ||
		targetQuota < 0 || targetQuota > common.MaxQuota {
		return false, ErrBillingOperationInvalid
	}

	limited := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var operation BillingOperation
		if err := lockForUpdate(tx).
			Where("id = ?", operationID).
			First(&operation).Error; err != nil {
			return err
		}
		if operation.Status != BillingOperationStatusReserved ||
			operation.TaskId != 0 ||
			operation.UserId != task.UserId ||
			operation.TokenId != task.PrivateData.TokenId ||
			operation.FundingSource != normalizedTaskFundingSource(task) ||
			(operation.FundingSource == BillingOperationFundingSubscription &&
				operation.SubscriptionId != task.PrivateData.SubscriptionId) {
			return ErrBillingOperationConflict
		}

		if _, err := adjustBillingOperationReservationTx(tx, &operation, targetQuota); err != nil {
			return err
		}
		limited = operation.SettlementLimited

		task.BillingOperationId = operation.Id
		task.Quota = operation.ReservedQuota
		if err := tx.Create(task).Error; err != nil {
			return err
		}

		operation.TaskId = task.ID
		if err := tx.Save(&operation).Error; err != nil {
			return err
		}
		return nil
	})
	return limited, sanitizeDBError(err)
}

func ApplyTaskBillingTransition(
	task *Task,
	fromStatus TaskStatus,
	requestedQuota int,
	outcome BillingOperationOutcome,
) (*TaskBillingTransitionResult, error) {
	if task == nil || task.ID <= 0 || requestedQuota < 0 ||
		requestedQuota > common.MaxQuota || !validBillingOperationOutcome(outcome) ||
		(outcome == BillingOperationOutcomeSettle && task.Status != TaskStatusSuccess) ||
		(outcome == BillingOperationOutcomeRefund && task.Status != TaskStatusFailure) {
		return nil, ErrBillingOperationInvalid
	}

	result := &TaskBillingTransitionResult{RequestedQuota: requestedQuota}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current Task
		if err := lockForUpdate(tx).Where("id = ?", task.ID).First(&current).Error; err != nil {
			return err
		}
		result.PreviousQuota = current.Quota

		currentTerminal := isTerminalTaskStatus(current.Status)
		if currentTerminal && current.Status != task.Status {
			return ErrBillingOperationTransition
		}
		if !currentTerminal && current.Status != fromStatus {
			return ErrBillingOperationConflict
		}

		if current.BillingOperationId == 0 &&
			outcome == BillingOperationOutcomeRefund &&
			current.SubmitTime > 0 &&
			current.SubmitTime < TaskRefundLegacyCutoff {
			if currentTerminal && current.Quota == 0 {
				*task = current
				result.FinalQuota = 0
				result.LegacyNoRefund = true
				return nil
			}
			updated := current
			if !currentTerminal {
				updated = *task
			}
			updated.ID = current.ID
			updated.CreatedAt = current.CreatedAt
			updated.BillingOperationId = 0
			updated.Quota = 0
			update := tx.Model(&Task{}).
				Where("id = ? AND status = ?", current.ID, current.Status).
				Select("*").
				Updates(&updated)
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrBillingOperationConflict
			}
			*task = updated
			result.Changed = true
			result.TaskChanged = true
			result.FinalQuota = 0
			result.LegacyNoRefund = true
			return nil
		}

		operation, err := loadOrAdoptTaskBillingOperationTx(tx, &current)
		if err != nil {
			return err
		}
		if operation.TaskId != 0 && operation.TaskId != current.ID {
			return ErrBillingOperationConflict
		}

		billingChanged, delta, err := transitionBillingOperationTx(
			tx,
			operation,
			requestedQuota,
			outcome,
			true,
		)
		if err != nil {
			return err
		}

		finalQuota := operation.ActualQuota
		if operation.Status == BillingOperationStatusAbandoned {
			finalQuota = operation.ReservedQuota
		}
		updated := current
		if !currentTerminal {
			updated = *task
		}
		updated.ID = current.ID
		updated.CreatedAt = current.CreatedAt
		updated.BillingOperationId = operation.Id
		updated.Quota = finalQuota

		taskNeedsWrite := !currentTerminal ||
			current.BillingOperationId != updated.BillingOperationId ||
			current.Quota != updated.Quota ||
			!current.Snapshot().Equal(updated.Snapshot())
		if taskNeedsWrite {
			update := tx.Model(&Task{}).
				Where("id = ? AND status = ?", current.ID, current.Status).
				Select("*").
				Updates(&updated)
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrBillingOperationConflict
			}
		}

		*task = updated
		result.Changed = billingChanged || taskNeedsWrite
		result.BillingChanged = billingChanged
		result.TaskChanged = taskNeedsWrite
		result.FinalQuota = finalQuota
		result.Delta = delta
		result.OperationId = operation.Id
		result.SettlementLimited = operation.SettlementLimited
		result.Abandoned = operation.Status == BillingOperationStatusAbandoned
		result.FailureReason = operation.FailureReason
		return nil
	})
	return result, sanitizeDBError(err)
}

func adjustBillingOperationReservationTx(
	tx *gorm.DB,
	operation *BillingOperation,
	targetQuota int,
) (bool, error) {
	if tx == nil || operation == nil || targetQuota < 0 || targetQuota > common.MaxQuota {
		return false, ErrBillingOperationInvalid
	}
	if operation.Status != BillingOperationStatusReserved {
		return false, ErrBillingOperationTransition
	}
	if targetQuota == operation.ReservedQuota {
		return false, nil
	}

	rows, err := loadBillingOperationRowsTx(tx, operation)
	if err != nil {
		if !errors.Is(err, ErrBillingOperationOwnerMissing) {
			return false, err
		}
		operation.RequestedQuota = targetQuota
		operation.SettlementLimited = true
		operation.FailureReason = billingOperationFailureReason(err)
		if err := tx.Save(operation).Error; err != nil {
			return false, err
		}
		return true, nil
	}

	requestedDelta := targetQuota - operation.ReservedQuota
	appliedDelta := requestedDelta
	if requestedDelta > 0 {
		capacity := billingOperationAdditionalCapacity(operation, rows)
		if appliedDelta > capacity {
			appliedDelta = capacity
		}
	}
	if err := applyBillingOperationDeltaTx(tx, operation, rows, appliedDelta); err != nil {
		return false, err
	}

	operation.ReservedQuota += appliedDelta
	operation.RequestedQuota = targetQuota
	operation.SettlementLimited = appliedDelta != requestedDelta
	if operation.SettlementLimited {
		operation.FailureReason = "reservation_capacity_limited"
	} else {
		operation.FailureReason = ""
	}
	if err := tx.Save(operation).Error; err != nil {
		return false, err
	}
	return true, nil
}

func transitionBillingOperationTx(
	tx *gorm.DB,
	operation *BillingOperation,
	requestedQuota int,
	outcome BillingOperationOutcome,
	abandonOnInconsistent bool,
) (bool, int, error) {
	if tx == nil || operation == nil || requestedQuota < 0 ||
		requestedQuota > common.MaxQuota || !validBillingOperationOutcome(outcome) {
		return false, 0, ErrBillingOperationInvalid
	}

	switch outcome {
	case BillingOperationOutcomeSettle:
		switch operation.Status {
		case BillingOperationStatusSettled:
			if operation.RequestedQuota != requestedQuota {
				return false, 0, ErrBillingOperationConflict
			}
			return false, 0, nil
		case BillingOperationStatusRefunded, BillingOperationStatusAbandoned:
			return false, 0, ErrBillingOperationTransition
		case BillingOperationStatusReserved:
		default:
			return false, 0, ErrBillingOperationTransition
		}

		rows, err := loadBillingOperationRowsTx(tx, operation)
		if err != nil {
			if !errors.Is(err, ErrBillingOperationOwnerMissing) {
				return false, 0, err
			}
			abandonBillingOperation(operation, requestedQuota, err)
			if err := tx.Save(operation).Error; err != nil {
				return false, 0, err
			}
			return true, 0, nil
		}

		requestedDelta := requestedQuota - operation.ReservedQuota
		appliedDelta := requestedDelta
		if requestedDelta > 0 {
			capacity := billingOperationAdditionalCapacity(operation, rows)
			if appliedDelta > capacity {
				appliedDelta = capacity
			}
		}
		if err := validateBillingOperationDelta(operation, rows, appliedDelta); err != nil {
			if !abandonOnInconsistent {
				return false, 0, err
			}
			abandonBillingOperation(operation, requestedQuota, err)
			if err := tx.Save(operation).Error; err != nil {
				return false, 0, err
			}
			return true, 0, nil
		}
		if err := applyBillingOperationDeltaTx(tx, operation, rows, appliedDelta); err != nil {
			return false, 0, err
		}

		operation.RequestedQuota = requestedQuota
		operation.ActualQuota = operation.ReservedQuota + appliedDelta
		operation.SettlementLimited = appliedDelta != requestedDelta
		if operation.SettlementLimited {
			operation.FailureReason = "settlement_capacity_limited"
		} else {
			operation.FailureReason = ""
		}
		operation.Status = BillingOperationStatusSettled
		if err := tx.Save(operation).Error; err != nil {
			return false, 0, err
		}
		return true, appliedDelta, nil

	case BillingOperationOutcomeRefund:
		switch operation.Status {
		case BillingOperationStatusRefunded, BillingOperationStatusAbandoned:
			return false, 0, nil
		case BillingOperationStatusSettled:
			return false, 0, ErrBillingOperationTransition
		case BillingOperationStatusReserved:
		default:
			return false, 0, ErrBillingOperationTransition
		}

		rows, err := loadBillingOperationRowsTx(tx, operation)
		if err != nil {
			if !errors.Is(err, ErrBillingOperationOwnerMissing) {
				return false, 0, err
			}
			abandonBillingOperation(operation, 0, err)
			if err := tx.Save(operation).Error; err != nil {
				return false, 0, err
			}
			return true, 0, nil
		}

		delta := -operation.ReservedQuota
		if err := validateBillingOperationDelta(operation, rows, delta); err != nil {
			if !abandonOnInconsistent {
				return false, 0, err
			}
			abandonBillingOperation(operation, 0, err)
			if err := tx.Save(operation).Error; err != nil {
				return false, 0, err
			}
			return true, 0, nil
		}
		if err := applyBillingOperationDeltaTx(tx, operation, rows, delta); err != nil {
			return false, 0, err
		}
		operation.RequestedQuota = 0
		operation.ActualQuota = 0
		operation.SettlementLimited = false
		operation.FailureReason = ""
		operation.Status = BillingOperationStatusRefunded
		if err := tx.Save(operation).Error; err != nil {
			return false, 0, err
		}
		return true, delta, nil
	default:
		return false, 0, ErrBillingOperationInvalid
	}
}

func loadBillingOperationRowsTx(
	tx *gorm.DB,
	operation *BillingOperation,
) (*billingOperationRows, error) {
	if tx == nil || operation == nil {
		return nil, ErrBillingOperationInvalid
	}
	rows := &billingOperationRows{}

	if operation.FundingSource == BillingOperationFundingSubscription {
		if operation.SubscriptionId <= 0 {
			return nil, ErrBillingOperationFundingInvalid
		}
		var subscription UserSubscription
		if err := lockForUpdate(tx).
			Where("id = ?", operation.SubscriptionId).
			First(&subscription).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &billingOperationOwnerMissingError{kind: "subscription"}
			}
			return nil, err
		}
		rows.subscription = &subscription
	}

	var user User
	if err := lockForUpdate(tx.Unscoped()).
		Where("id = ?", operation.UserId).
		First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &billingOperationOwnerMissingError{kind: "user"}
		}
		return nil, err
	}
	rows.user = &user

	if operation.TokenCharged {
		if operation.TokenId <= 0 {
			return nil, ErrBillingOperationTokenInvalid
		}
		var token Token
		if err := lockForUpdate(tx.Unscoped()).
			Where("id = ? AND user_id = ?", operation.TokenId, operation.UserId).
			First(&token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &billingOperationOwnerMissingError{kind: "token"}
			}
			return nil, err
		}
		rows.token = &token
	}
	return rows, nil
}

func validateBillingOperationDelta(
	operation *BillingOperation,
	rows *billingOperationRows,
	delta int,
) error {
	if operation == nil || rows == nil {
		return ErrBillingOperationInvalid
	}
	if delta == 0 {
		return nil
	}

	if operation.FundingSource == BillingOperationFundingSubscription {
		if rows.subscription == nil {
			return ErrBillingOperationFundingInvalid
		}
		next := rows.subscription.AmountUsed + int64(delta)
		if next < 0 {
			return ErrBillingOperationFundingInvalid
		}
		if rows.subscription.AmountTotal > 0 &&
			next > rows.subscription.AmountTotal {
			return ErrBillingOperationFundingQuota
		}
	} else {
		if rows.user == nil {
			return ErrBillingOperationFundingInvalid
		}
		if delta > 0 && rows.user.Quota < delta {
			return ErrBillingOperationFundingQuota
		}
		if delta < 0 && rows.user.Quota > common.MaxQuota+delta {
			return ErrBillingOperationFundingInvalid
		}
	}

	if !operation.TokenCharged {
		return nil
	}
	if rows.token == nil {
		return ErrBillingOperationTokenInvalid
	}
	if delta > 0 {
		if rows.token.UsedQuota > common.MaxQuota-delta {
			return ErrBillingOperationTokenInvalid
		}
		if operation.TokenRemainCharged &&
			rows.token.RemainQuota < delta {
			return ErrBillingOperationTokenQuota
		}
		return nil
	}

	refund := -delta
	if rows.token.UsedQuota < refund {
		return ErrBillingOperationTokenInvalid
	}
	if operation.TokenRemainCharged &&
		rows.token.RemainQuota > common.MaxQuota-refund {
		return ErrBillingOperationTokenInvalid
	}
	return nil
}

func applyBillingOperationDeltaTx(
	tx *gorm.DB,
	operation *BillingOperation,
	rows *billingOperationRows,
	delta int,
) error {
	if tx == nil || operation == nil || rows == nil {
		return ErrBillingOperationInvalid
	}
	if delta == 0 {
		return nil
	}

	if operation.FundingSource == BillingOperationFundingSubscription {
		if rows.subscription == nil {
			return ErrBillingOperationFundingInvalid
		}
		current := rows.subscription.AmountUsed
		next := current + int64(delta)
		if next < 0 {
			return ErrBillingOperationFundingInvalid
		}
		if rows.subscription.AmountTotal > 0 && next > rows.subscription.AmountTotal {
			return ErrBillingOperationFundingQuota
		}
		update := tx.Model(&UserSubscription{}).
			Where(
				"id = ? AND amount_used = ?",
				rows.subscription.Id,
				current,
			).
			Updates(map[string]any{
				"amount_used": next,
				"updated_at":  getDBTimestamp(tx),
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrBillingOperationFundingInvalid
		}
		rows.subscription.AmountUsed = next
	} else {
		if rows.user == nil {
			return ErrBillingOperationFundingInvalid
		}
		var result *gorm.DB
		if delta > 0 {
			result = tx.Unscoped().Model(&User{}).
				Where("id = ? AND quota >= ?", operation.UserId, delta).
				Update("quota", gorm.Expr("quota - ?", delta))
		} else {
			refund := -delta
			result = tx.Unscoped().Model(&User{}).
				Where("id = ? AND quota <= ?", operation.UserId, common.MaxQuota-refund).
				Update("quota", gorm.Expr("quota + ?", refund))
		}
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrBillingOperationFundingInvalid
		}
	}

	if !operation.TokenCharged {
		return nil
	}
	if rows.token == nil {
		return ErrBillingOperationTokenInvalid
	}

	updates := map[string]any{
		"accessed_time": common.GetTimestamp(),
	}
	tokenUpdate := tx.Unscoped().Model(&Token{}).
		Where("id = ? AND user_id = ?", operation.TokenId, operation.UserId)
	if delta > 0 {
		tokenUpdate = tokenUpdate.Where("used_quota <= ?", common.MaxQuota-delta)
		updates["used_quota"] = gorm.Expr("used_quota + ?", delta)
		if operation.TokenRemainCharged {
			tokenUpdate = tokenUpdate.Where("remain_quota >= ?", delta)
			updates["remain_quota"] = gorm.Expr("remain_quota - ?", delta)
		}
	} else {
		refund := -delta
		tokenUpdate = tokenUpdate.Where("used_quota >= ?", refund)
		updates["used_quota"] = gorm.Expr("used_quota - ?", refund)
		if operation.TokenRemainCharged {
			tokenUpdate = tokenUpdate.Where("remain_quota <= ?", common.MaxQuota-refund)
			updates["remain_quota"] = gorm.Expr("remain_quota + ?", refund)
		}
	}
	result := tokenUpdate.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrBillingOperationTokenInvalid
	}
	if delta < 0 && operation.TokenRemainCharged {
		return RestoreExhaustedTokenIfFunded(tx.Unscoped(), operation.TokenId)
	}
	return nil
}

func billingOperationAdditionalCapacity(
	operation *BillingOperation,
	rows *billingOperationRows,
) int {
	capacity := common.MaxQuota
	if operation.FundingSource == BillingOperationFundingSubscription {
		if rows.subscription == nil {
			return 0
		}
		if rows.subscription.AmountTotal > 0 {
			remaining := rows.subscription.AmountTotal - rows.subscription.AmountUsed
			if remaining <= 0 {
				return 0
			}
			if remaining < int64(capacity) {
				capacity = int(remaining)
			}
		}
	} else {
		if rows.user == nil || rows.user.Quota <= 0 {
			return 0
		}
		if rows.user.Quota < capacity {
			capacity = rows.user.Quota
		}
	}
	if operation.TokenCharged {
		if rows.token == nil {
			return 0
		}
		usedCapacity := common.MaxQuota - rows.token.UsedQuota
		if usedCapacity <= 0 {
			return 0
		}
		if usedCapacity < capacity {
			capacity = usedCapacity
		}
		if operation.TokenRemainCharged &&
			rows.token.RemainQuota <= 0 {
			return 0
		}
		if operation.TokenRemainCharged &&
			rows.token.RemainQuota < capacity {
			capacity = rows.token.RemainQuota
		}
	}
	return capacity
}

func loadOrAdoptTaskBillingOperationTx(
	tx *gorm.DB,
	task *Task,
) (*BillingOperation, error) {
	if task.BillingOperationId > 0 {
		var operation BillingOperation
		if err := lockForUpdate(tx).
			Where("id = ?", task.BillingOperationId).
			First(&operation).Error; err != nil {
			return nil, err
		}
		if operation.UserId != task.UserId ||
			(operation.TaskId != 0 && operation.TaskId != task.ID) {
			return nil, ErrBillingOperationConflict
		}
		return &operation, nil
	}

	requestID := fmt.Sprintf("legacy-task:%d", task.ID)
	var existing BillingOperation
	result := lockForUpdate(tx).
		Where("request_id = ?", requestID).
		Limit(1).
		Find(&existing)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected > 0 {
		if existing.TaskId != task.ID || existing.UserId != task.UserId {
			return nil, ErrBillingOperationConflict
		}
		return &existing, nil
	}

	source := normalizedTaskFundingSource(task)
	operation := &BillingOperation{
		RequestId:          requestID,
		TaskId:             task.ID,
		UserId:             task.UserId,
		TokenId:            task.PrivateData.TokenId,
		TokenCharged:       task.PrivateData.TokenId > 0,
		TokenRemainCharged: task.PrivateData.TokenId > 0,
		FundingSource:      source,
		SubscriptionId:     task.PrivateData.SubscriptionId,
		ReservedQuota:      task.Quota,
		Status:             BillingOperationStatusReserved,
	}
	if operation.TokenCharged {
		var token Token
		if err := tx.Unscoped().
			Select("id", "unlimited_quota").
			Where("id = ? AND user_id = ?", operation.TokenId, operation.UserId).
			First(&token).Error; err == nil {
			operation.TokenUnlimited = token.UnlimitedQuota
		}
	}
	if err := tx.Create(operation).Error; err != nil {
		return nil, err
	}
	return operation, nil
}

func selectBillingSubscriptionTx(
	tx *gorm.DB,
	userID int,
	quota int,
	now int64,
) (*UserSubscription, error) {
	var subscriptions []UserSubscription
	if err := lockForUpdate(tx).
		Where("user_id = ? AND status = ? AND end_time > ?", userID, "active", now).
		Order("end_time asc, id asc").
		Find(&subscriptions).Error; err != nil {
		return nil, err
	}
	for i := range subscriptions {
		subscription := &subscriptions[i]
		plan, err := getSubscriptionPlanByIdTx(tx, subscription.PlanId)
		if err != nil {
			return nil, err
		}
		if _, err := maybeResetUserSubscriptionWithPlanTx(tx, subscription, plan, now); err != nil {
			return nil, err
		}
		if subscription.AmountTotal > 0 &&
			subscription.AmountTotal-subscription.AmountUsed < int64(quota) {
			continue
		}
		return subscription, nil
	}
	return nil, ErrBillingOperationFundingQuota
}

func abandonBillingOperation(
	operation *BillingOperation,
	requestedQuota int,
	cause error,
) {
	operation.RequestedQuota = requestedQuota
	operation.ActualQuota = operation.ReservedQuota
	operation.SettlementLimited = true
	operation.FailureReason = billingOperationFailureReason(cause)
	operation.Status = BillingOperationStatusAbandoned
}

func billingOperationFailureReason(err error) string {
	var missing *billingOperationOwnerMissingError
	switch {
	case errors.As(err, &missing):
		return missing.kind + "_missing"
	case errors.Is(err, ErrBillingOperationTokenInvalid),
		errors.Is(err, ErrBillingOperationTokenQuota):
		return "token_balance_inconsistent"
	case errors.Is(err, ErrBillingOperationFundingInvalid),
		errors.Is(err, ErrBillingOperationFundingQuota):
		return "funding_balance_inconsistent"
	}
	return "owner_missing"
}

func sameBillingOperationRequest(
	existing *BillingOperation,
	input BillingOperationReserveRequest,
) bool {
	return existing != nil &&
		existing.RequestId == input.RequestId &&
		existing.UserId == input.UserId &&
		existing.TokenId == input.TokenId &&
		existing.TokenCharged == input.TokenCharged &&
		existing.FundingSource == input.FundingSource
}

func normalizedTaskFundingSource(task *Task) string {
	if task != nil &&
		task.PrivateData.BillingSource == BillingOperationFundingSubscription &&
		task.PrivateData.SubscriptionId > 0 {
		return BillingOperationFundingSubscription
	}
	return BillingOperationFundingWallet
}

func validBillingFundingSource(source string) bool {
	return source == BillingOperationFundingWallet ||
		source == BillingOperationFundingSubscription
}

func validBillingOperationOutcome(outcome BillingOperationOutcome) bool {
	return outcome == BillingOperationOutcomeSettle ||
		outcome == BillingOperationOutcomeRefund
}

func isTerminalTaskStatus(status TaskStatus) bool {
	return status == TaskStatusSuccess || status == TaskStatusFailure
}
