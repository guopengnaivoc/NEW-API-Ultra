package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	MidjourneyQuotaReservationStatusReserved = "reserved"
	MidjourneyQuotaReservationStatusSettled  = "settled"
	MidjourneyQuotaReservationStatusRefunded = "refunded"
)

type MidjourneyQuotaReservation struct {
	Id               int    `json:"id"`
	RequestId        string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId           int    `json:"user_id" gorm:"index"`
	TokenId          int    `json:"token_id" gorm:"index"`
	Quota            int    `json:"quota"`
	TokenUnlimited   bool   `json:"token_unlimited"`
	TokenCharged     bool   `json:"token_charged"`
	MidjourneyTaskId int    `json:"midjourney_task_id" gorm:"index"`
	Status           string `json:"status" gorm:"type:varchar(16);index"`
	CreatedAt        int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt        int64  `json:"updated_at" gorm:"bigint;index"`
}

func (reservation *MidjourneyQuotaReservation) BeforeCreate(*gorm.DB) error {
	now := common.GetTimestamp()
	reservation.CreatedAt = now
	reservation.UpdatedAt = now
	return nil
}

func (reservation *MidjourneyQuotaReservation) BeforeUpdate(*gorm.DB) error {
	reservation.UpdatedAt = common.GetTimestamp()
	return nil
}

type MidjourneyQuotaReservationRequest struct {
	RequestId    string
	UserId       int
	TokenId      int
	Quota        int
	TokenCharged bool
}

type MidjourneyBillingOutcome string

const (
	MidjourneyBillingOutcomePending MidjourneyBillingOutcome = "pending"
	MidjourneyBillingOutcomeSuccess MidjourneyBillingOutcome = "success"
	MidjourneyBillingOutcomeFailure MidjourneyBillingOutcome = "failure"
)

var (
	ErrMidjourneyQuotaReservationInvalid      = errors.New("invalid Midjourney quota reservation")
	ErrMidjourneyWalletQuotaInsufficient      = errors.New("Midjourney wallet quota insufficient")
	ErrMidjourneyTokenQuotaInsufficient       = errors.New("Midjourney token quota insufficient")
	ErrMidjourneyQuotaReservationConflict     = errors.New("Midjourney quota reservation conflict")
	ErrMidjourneyQuotaReservationTransition   = errors.New("invalid Midjourney quota reservation transition")
	ErrMidjourneyQuotaReservationTokenInvalid = errors.New("Midjourney quota reservation token invalid")
)

func ReserveMidjourneyQuota(input MidjourneyQuotaReservationRequest) (*MidjourneyQuotaReservation, bool, error) {
	input.RequestId = strings.TrimSpace(input.RequestId)
	if input.RequestId == "" || len(input.RequestId) > 64 || input.UserId <= 0 ||
		input.Quota <= 0 || (input.TokenCharged && input.TokenId <= 0) {
		return nil, false, ErrMidjourneyQuotaReservationInvalid
	}

	var reservation MidjourneyQuotaReservation
	created := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing MidjourneyQuotaReservation
		result := lockForUpdate(tx).
			Where("request_id = ?", input.RequestId).
			Limit(1).
			Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			if !sameMidjourneyQuotaReservation(&existing, input) {
				return ErrMidjourneyQuotaReservationConflict
			}
			reservation = existing
			return nil
		}

		reservation = MidjourneyQuotaReservation{
			RequestId:    input.RequestId,
			UserId:       input.UserId,
			TokenId:      input.TokenId,
			Quota:        input.Quota,
			TokenCharged: input.TokenCharged,
			Status:       MidjourneyQuotaReservationStatusReserved,
		}
		if err := tx.Create(&reservation).Error; err != nil {
			return err
		}

		userUpdate := tx.Model(&User{}).
			Where("id = ? AND quota >= ?", input.UserId, input.Quota).
			Update("quota", gorm.Expr("quota - ?", input.Quota))
		if userUpdate.Error != nil {
			return userUpdate.Error
		}
		if userUpdate.RowsAffected != 1 {
			return ErrMidjourneyWalletQuotaInsufficient
		}

		if input.TokenCharged {
			var token Token
			if err := lockForUpdate(tx.Unscoped()).
				Where("id = ? AND user_id = ?", input.TokenId, input.UserId).
				First(&token).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrMidjourneyQuotaReservationTokenInvalid
				}
				return err
			}
			if token.Status != common.TokenStatusEnabled ||
				(token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp()) {
				return ErrMidjourneyQuotaReservationTokenInvalid
			}

			updates := map[string]any{
				"used_quota":    gorm.Expr("used_quota + ?", input.Quota),
				"accessed_time": common.GetTimestamp(),
			}
			tokenUpdate := tx.Unscoped().Model(&Token{}).
				Where("id = ? AND user_id = ?", input.TokenId, input.UserId)
			if !token.UnlimitedQuota {
				tokenUpdate = tokenUpdate.Where("remain_quota >= ?", input.Quota)
				updates["remain_quota"] = gorm.Expr("remain_quota - ?", input.Quota)
			}
			result = tokenUpdate.Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrMidjourneyTokenQuotaInsufficient
			}

			reservation.TokenUnlimited = token.UnlimitedQuota
			if err := tx.Model(&reservation).
				Update("token_unlimited", token.UnlimitedQuota).Error; err != nil {
				return err
			}
		}

		created = true
		return nil
	})
	if err != nil {
		var existing MidjourneyQuotaReservation
		if lookupErr := DB.Where("request_id = ?", input.RequestId).First(&existing).Error; lookupErr == nil {
			if !sameMidjourneyQuotaReservation(&existing, input) {
				return nil, false, ErrMidjourneyQuotaReservationConflict
			}
			return &existing, false, nil
		}
		return nil, false, err
	}
	if !created {
		return &reservation, false, nil
	}

	invalidateMidjourneyQuotaReservationCaches(&reservation)
	return &reservation, true, nil
}

func RefundMidjourneyQuotaReservation(id int) (bool, error) {
	if id <= 0 {
		return false, ErrMidjourneyQuotaReservationInvalid
	}

	var reservation MidjourneyQuotaReservation
	refunded := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).
			Where("id = ?", id).
			First(&reservation).Error; err != nil {
			return err
		}
		var err error
		refunded, err = applyMidjourneyReservationOutcomeTx(
			tx,
			&reservation,
			MidjourneyBillingOutcomeFailure,
		)
		return err
	})
	if err != nil {
		return false, err
	}
	if refunded {
		invalidateMidjourneyQuotaReservationCaches(&reservation)
	}
	return refunded, nil
}

func SettleMidjourneyQuotaReservation(id int) (bool, error) {
	if id <= 0 {
		return false, ErrMidjourneyQuotaReservationInvalid
	}

	settled := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var reservation MidjourneyQuotaReservation
		if err := lockForUpdate(tx).
			Where("id = ?", id).
			First(&reservation).Error; err != nil {
			return err
		}
		var err error
		settled, err = applyMidjourneyReservationOutcomeTx(
			tx,
			&reservation,
			MidjourneyBillingOutcomeSuccess,
		)
		return err
	})
	return settled, err
}

func CreateMidjourneyTaskWithReservation(
	task *Midjourney,
	reservationID int,
	outcome MidjourneyBillingOutcome,
) (bool, error) {
	if task == nil || task.Id != 0 || reservationID <= 0 || !validMidjourneyBillingOutcome(outcome) {
		return false, ErrMidjourneyQuotaReservationInvalid
	}

	var reservation MidjourneyQuotaReservation
	refunded := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).
			Where("id = ?", reservationID).
			First(&reservation).Error; err != nil {
			return err
		}
		if reservation.Status != MidjourneyQuotaReservationStatusReserved ||
			reservation.MidjourneyTaskId != 0 ||
			reservation.UserId != task.UserId ||
			reservation.Quota != task.Quota {
			return ErrMidjourneyQuotaReservationConflict
		}

		task.QuotaReservationId = reservation.Id
		if err := tx.Create(task).Error; err != nil {
			return err
		}

		reservation.MidjourneyTaskId = task.Id
		if err := tx.Save(&reservation).Error; err != nil {
			return err
		}

		changed, err := applyMidjourneyReservationOutcomeTx(tx, &reservation, outcome)
		refunded = outcome == MidjourneyBillingOutcomeFailure && changed
		return err
	})
	if err != nil {
		return false, err
	}
	if refunded {
		invalidateMidjourneyQuotaReservationCaches(&reservation)
	}
	return refunded, nil
}

func applyMidjourneyReservationOutcomeTx(
	tx *gorm.DB,
	reservation *MidjourneyQuotaReservation,
	outcome MidjourneyBillingOutcome,
) (bool, error) {
	if tx == nil || reservation == nil || !validMidjourneyBillingOutcome(outcome) {
		return false, ErrMidjourneyQuotaReservationInvalid
	}

	switch outcome {
	case MidjourneyBillingOutcomePending:
		if reservation.Status != MidjourneyQuotaReservationStatusReserved {
			return false, ErrMidjourneyQuotaReservationTransition
		}
		return false, nil
	case MidjourneyBillingOutcomeSuccess:
		switch reservation.Status {
		case MidjourneyQuotaReservationStatusSettled:
			return false, nil
		case MidjourneyQuotaReservationStatusRefunded:
			return false, ErrMidjourneyQuotaReservationTransition
		case MidjourneyQuotaReservationStatusReserved:
			reservation.Status = MidjourneyQuotaReservationStatusSettled
			if err := tx.Save(reservation).Error; err != nil {
				return false, err
			}
			return true, nil
		default:
			return false, ErrMidjourneyQuotaReservationTransition
		}
	case MidjourneyBillingOutcomeFailure:
		switch reservation.Status {
		case MidjourneyQuotaReservationStatusRefunded:
			return false, nil
		case MidjourneyQuotaReservationStatusSettled:
			return false, ErrMidjourneyQuotaReservationTransition
		case MidjourneyQuotaReservationStatusReserved:
		default:
			return false, ErrMidjourneyQuotaReservationTransition
		}

		userUpdate := tx.Unscoped().Model(&User{}).
			Where("id = ?", reservation.UserId).
			Update("quota", gorm.Expr("quota + ?", reservation.Quota))
		if userUpdate.Error != nil {
			return false, userUpdate.Error
		}
		if userUpdate.RowsAffected != 1 {
			return false, ErrMidjourneyQuotaReservationInvalid
		}

		if reservation.TokenCharged {
			updates := map[string]any{
				"used_quota": gorm.Expr("used_quota - ?", reservation.Quota),
			}
			if !reservation.TokenUnlimited {
				updates["remain_quota"] = gorm.Expr("remain_quota + ?", reservation.Quota)
			}
			tokenUpdate := tx.Unscoped().Model(&Token{}).
				Where(
					"id = ? AND user_id = ? AND used_quota >= ?",
					reservation.TokenId,
					reservation.UserId,
					reservation.Quota,
				).
				Updates(updates)
			if tokenUpdate.Error != nil {
				return false, tokenUpdate.Error
			}
			if tokenUpdate.RowsAffected != 1 {
				return false, ErrMidjourneyQuotaReservationTokenInvalid
			}
			if !reservation.TokenUnlimited && reservation.Quota > 0 {
				if err := RestoreExhaustedTokenIfFunded(tx.Unscoped(), reservation.TokenId); err != nil {
					return false, err
				}
			}
		}

		reservation.Status = MidjourneyQuotaReservationStatusRefunded
		if err := tx.Save(reservation).Error; err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, ErrMidjourneyQuotaReservationInvalid
	}
}

func validMidjourneyBillingOutcome(outcome MidjourneyBillingOutcome) bool {
	switch outcome {
	case MidjourneyBillingOutcomePending,
		MidjourneyBillingOutcomeSuccess,
		MidjourneyBillingOutcomeFailure:
		return true
	default:
		return false
	}
}

func sameMidjourneyQuotaReservation(
	existing *MidjourneyQuotaReservation,
	input MidjourneyQuotaReservationRequest,
) bool {
	return existing != nil &&
		existing.RequestId == input.RequestId &&
		existing.UserId == input.UserId &&
		existing.TokenId == input.TokenId &&
		existing.Quota == input.Quota &&
		existing.TokenCharged == input.TokenCharged
}

func invalidateMidjourneyQuotaReservationCaches(reservation *MidjourneyQuotaReservation) {
	if reservation == nil || !common.RedisEnabled {
		return
	}
	if err := invalidateUserCache(reservation.UserId); err != nil {
		common.SysError(fmt.Sprintf(
			"failed to invalidate Midjourney quota user cache request_id=%s user_id=%d: %s",
			reservation.RequestId,
			reservation.UserId,
			err.Error(),
		))
	}
	if !reservation.TokenCharged || reservation.TokenId <= 0 {
		return
	}

	var token Token
	if err := DB.Unscoped().
		Select(commonKeyCol).
		Where("id = ?", reservation.TokenId).
		First(&token).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.SysError(fmt.Sprintf(
				"failed to load Midjourney quota token cache key request_id=%s token_id=%d: %s",
				reservation.RequestId,
				reservation.TokenId,
				err.Error(),
			))
		}
		return
	}
	if token.KeyHash == "" {
		return
	}
	if err := cacheDeleteTokenHash(token.KeyHash); err != nil {
		common.SysError(fmt.Sprintf(
			"failed to invalidate Midjourney quota token cache request_id=%s token_id=%d: %s",
			reservation.RequestId,
			reservation.TokenId,
			err.Error(),
		))
	}
}
