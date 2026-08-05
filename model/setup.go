package model

import (
	"errors"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Setup struct {
	ID            uint   `json:"id" gorm:"primaryKey"`
	Version       string `json:"version" gorm:"type:varchar(50);not null"`
	InitializedAt int64  `json:"initialized_at" gorm:"type:bigint;not null"`
}

var ErrSystemAlreadyInitialized = errors.New("system already initialized")

type SetupInitializationParams struct {
	RootUser           User
	SelfUseModeEnabled bool
	DemoSiteEnabled    bool
}

func (params SetupInitializationParams) OptionValues() map[string]string {
	return map[string]string{
		"SelfUseModeEnabled": strconv.FormatBool(params.SelfUseModeEnabled),
		"DemoSiteEnabled":    strconv.FormatBool(params.DemoSiteEnabled),
	}
}

func systemInitializationExists(db *gorm.DB) (bool, error) {
	var setup Setup
	err := db.Select("id").First(&setup).Error
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}

	var root User
	err = db.Unscoped().Select("id").Where("role = ?", common.RoleRootUser).First(&root).Error
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, err
}

func InitializeSystem(params SetupInitializationParams) error {
	setupClaimFailed := false
	transactionErr := DB.Transaction(func(tx *gorm.DB) error {
		initialized, err := systemInitializationExists(tx)
		if err != nil {
			return err
		}
		if initialized {
			return ErrSystemAlreadyInitialized
		}

		setup := Setup{
			ID:            1,
			Version:       common.Version,
			InitializedAt: time.Now().Unix(),
		}
		if err := tx.Create(&setup).Error; err != nil {
			setupClaimFailed = true
			return err
		}

		rootUser := params.RootUser
		if rootUser.AffCode == "" {
			affCode, err := common.GenerateInviteCode(common.InviteCodeLength)
			if err != nil {
				return err
			}
			rootUser.AffCode = affCode
		}
		if err := tx.Create(&rootUser).Error; err != nil {
			return err
		}

		for key, value := range params.OptionValues() {
			option := Option{Key: key, Value: value}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value"}),
			}).Create(&option).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if transactionErr == nil || errors.Is(transactionErr, ErrSystemAlreadyInitialized) {
		return transactionErr
	}
	if !setupClaimFailed {
		return transactionErr
	}

	attempts := 1
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		attempts = 5
	}
	for attempt := 0; attempt < attempts; attempt++ {
		initialized, stateErr := systemInitializationExists(DB)
		if stateErr == nil && initialized {
			return ErrSystemAlreadyInitialized
		}
		if attempt+1 < attempts {
			time.Sleep(10 * time.Millisecond)
		}
	}
	return transactionErr
}

func GetSetup() *Setup {
	var setup Setup
	err := DB.First(&setup).Error
	if err != nil {
		return nil
	}
	return &setup
}
