package authz

import (
	"fmt"

	"github.com/QuantumNous/new-api/model"
	"github.com/casbin/casbin/v2"
)

// Can reports whether the subject may perform the permission. A superuser role
// short-circuits to allow. Otherwise a per-user override wins, then the union of
// the subject's role baselines applies.
func Can(userID int, systemRole int, permission Permission) bool {
	roles := resolveSubjectRoles(userID, systemRole)
	if len(roles) == 0 {
		return false
	}
	for _, role := range roles {
		if isSuperuserRole(role) {
			return true
		}
	}
	if !isKnownPermission(permission) {
		return false
	}

	e := currentEnforcer()
	if e == nil {
		return false
	}
	effects, err := loadAuthoritativeUserEffects(userID)
	if err != nil {
		return false
	}
	return canWithUserEffects(e, roles, effects, permission)
}

// Capabilities returns the full resource/action matrix the subject is allowed.
func Capabilities(userID int, systemRole int) PermissionsMap {
	result := make(PermissionsMap, len(registry))
	roles := resolveSubjectRoles(userID, systemRole)
	superuser := false
	for _, role := range roles {
		if isSuperuserRole(role) {
			superuser = true
			break
		}
	}
	var (
		e       *casbin.SyncedEnforcer
		effects map[Permission]string
		err     error
	)
	if !superuser {
		e = currentEnforcer()
		effects, err = loadAuthoritativeUserEffects(userID)
	}
	for _, resource := range registry {
		actions := make(map[string]bool, len(resource.Actions))
		for _, action := range resource.Actions {
			permission := Permission{
				Resource: resource.Resource,
				Action:   action.Action,
			}
			if superuser {
				actions[action.Action] = true
				continue
			}
			if len(roles) == 0 || e == nil || err != nil {
				actions[action.Action] = false
				continue
			}
			actions[action.Action] = canWithUserEffects(
				e,
				roles,
				effects,
				permission,
			)
		}
		result[resource.Resource] = actions
	}
	return result
}

func canWithUserEffects(
	e *casbin.SyncedEnforcer,
	roles []string,
	effects map[Permission]string,
	permission Permission,
) bool {
	for _, role := range roles {
		if isSuperuserRole(role) {
			return true
		}
	}
	if !isKnownPermission(permission) {
		return false
	}
	if effect, ok := effects[permission]; ok {
		return effect == EffectAllow
	}
	for _, role := range roles {
		if roleBaselineAllows(e, role, permission) {
			return true
		}
	}
	return false
}

func loadAuthoritativeUserEffects(userID int) (map[Permission]string, error) {
	if userID <= 0 {
		return map[Permission]string{}, nil
	}
	db := currentEnforcerDB()
	if db == nil {
		return nil, fmt.Errorf("authz policy database is not initialized")
	}

	var rules []model.CasbinRule
	if err := db.
		Select("v1", "v2", "v3").
		Where("ptype = ? AND v0 = ?", "p", UserSubject(userID)).
		Find(&rules).Error; err != nil {
		return nil, err
	}

	effects := make(map[Permission]string, len(rules))
	for _, rule := range rules {
		permission := Permission{Resource: rule.V1, Action: rule.V2}
		if !isKnownPermission(permission) {
			continue
		}
		effect := rule.V3
		if effect == "" {
			effect = EffectAllow
		}
		if effect != EffectAllow && effect != EffectDeny {
			continue
		}
		if current, ok := effects[permission]; ok &&
			current == EffectDeny {
			continue
		}
		effects[permission] = effect
	}
	return effects, nil
}

func roleBaselineAllows(e *casbin.SyncedEnforcer, roleKey string, permission Permission) bool {
	effect, ok := explicitSubjectEffect(e, RoleSubject(roleKey), permission)
	return ok && effect == EffectAllow
}

func explicitSubjectEffect(e *casbin.SyncedEnforcer, subject string, permission Permission) (string, bool) {
	policies, err := e.GetFilteredPolicy(0, subject, permission.Resource, permission.Action)
	if err != nil {
		return "", false
	}
	hasAllow := false
	for _, policy := range policies {
		switch policyEffect(policy) {
		case EffectDeny:
			return EffectDeny, true
		case EffectAllow:
			hasAllow = true
		}
	}
	if hasAllow {
		return EffectAllow, true
	}
	return "", false
}

func policyEffect(policy []string) string {
	if len(policy) < 4 || policy[3] == "" {
		return EffectAllow
	}
	return policy[3]
}
