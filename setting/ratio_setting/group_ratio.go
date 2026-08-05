package ratio_setting

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/types"
)

var defaultGroupRatio = map[string]float64{
	"default": 1,
	"vip":     1,
	"svip":    1,
}

var defaultGroupGroupRatio = map[string]map[string]float64{
	"vip": {
		"edit_this": 0.9,
	},
}

var defaultGroupSpecialUsableGroup = map[string]map[string]string{}

type GroupRatioSetting struct {
	GroupRatio              *types.RWMap[string, float64]            `json:"group_ratio"`
	GroupGroupRatio         *types.RWMap[string, map[string]float64] `json:"group_group_ratio"`
	GroupSpecialUsableGroup *types.RWMap[string, map[string]string]  `json:"group_special_usable_group"`
}

var groupRatioSetting GroupRatioSetting

func init() {
	groupSpecialUsableGroup := types.NewRWMap[string, map[string]string]()
	groupSpecialUsableGroup.AddAll(defaultGroupSpecialUsableGroup)

	groupRatioMap := types.NewRWMap[string, float64]()
	groupGroupRatioMap := types.NewRWMap[string, map[string]float64]()
	groupRatioMap.AddAll(defaultGroupRatio)
	groupGroupRatioMap.AddAll(defaultGroupGroupRatio)

	groupRatioSetting = GroupRatioSetting{
		GroupSpecialUsableGroup: groupSpecialUsableGroup,
		GroupRatio:              groupRatioMap,
		GroupGroupRatio:         groupGroupRatioMap,
	}

	config.GlobalConfig.Register("group_ratio_setting", &groupRatioSetting)
}

func GetGroupRatioSetting() *GroupRatioSetting {
	current := config.Snapshot[GroupRatioSetting]("group_ratio_setting")
	if current.GroupRatio != nil &&
		current.GroupGroupRatio != nil &&
		current.GroupSpecialUsableGroup != nil {
		return current
	}

	derived := *current
	if derived.GroupRatio == nil {
		derived.GroupRatio = types.NewRWMap[string, float64]()
		derived.GroupRatio.AddAll(defaultGroupRatio)
	}
	if derived.GroupGroupRatio == nil {
		derived.GroupGroupRatio = types.NewRWMap[string, map[string]float64]()
		derived.GroupGroupRatio.AddAll(defaultGroupGroupRatio)
	}
	if derived.GroupSpecialUsableGroup == nil {
		derived.GroupSpecialUsableGroup = types.NewRWMap[string, map[string]string]()
		derived.GroupSpecialUsableGroup.AddAll(defaultGroupSpecialUsableGroup)
	}
	return &derived
}

func GetGroupRatioCopy() map[string]float64 {
	return GetGroupRatioSetting().GroupRatio.ReadAll()
}

func ContainsGroupRatio(name string) bool {
	_, ok := GetGroupRatioSetting().GroupRatio.Get(name)
	return ok
}

func GroupRatio2JSONString() string {
	return GetGroupRatioSetting().GroupRatio.MarshalJSONString()
}

func UpdateGroupRatioByJSONString(jsonStr string) error {
	_, err := config.GlobalConfig.Update("group_ratio_setting", map[string]string{
		"group_ratio": jsonStr,
	})
	return err
}

func GetGroupRatio(name string) float64 {
	ratio, ok := GetGroupRatioSetting().GroupRatio.Get(name)
	if !ok {
		common.SysLog("group ratio not found: " + name)
		return 1
	}
	return ratio
}

func GetGroupGroupRatio(userGroup, usingGroup string) (float64, bool) {
	gp, ok := GetGroupRatioSetting().GroupGroupRatio.Get(userGroup)
	if !ok {
		return -1, false
	}
	ratio, ok := gp[usingGroup]
	if !ok {
		return -1, false
	}
	return ratio, true
}

func GroupGroupRatio2JSONString() string {
	return GetGroupRatioSetting().GroupGroupRatio.MarshalJSONString()
}

func UpdateGroupGroupRatioByJSONString(jsonStr string) error {
	_, err := config.GlobalConfig.Update("group_ratio_setting", map[string]string{
		"group_group_ratio": jsonStr,
	})
	return err
}

func CheckGroupRatio(jsonStr string) error {
	checkGroupRatio := make(map[string]float64)
	err := common.Unmarshal([]byte(jsonStr), &checkGroupRatio)
	if err != nil {
		return err
	}
	for name, ratio := range checkGroupRatio {
		if ratio < 0 {
			return errors.New("group ratio must be not less than 0: " + name)
		}
	}
	return nil
}
