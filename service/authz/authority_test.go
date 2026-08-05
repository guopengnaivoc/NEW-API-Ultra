package authz

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPersistedPermissionRevocationOverridesStaleEnforcer(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	const userID = 4200

	require.NoError(t, SetUserPermissions(userID, PermissionsMap{
		ResourceChannel: {
			ActionSensitiveWrite: true,
		},
	}))
	require.True(
		t,
		Can(userID, common.RoleAdminUser, ChannelSensitiveWrite),
	)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ClearUserAuthorizationInTx(tx, userID)
	}))

	effect, ok := explicitSubjectEffect(
		currentEnforcer(),
		UserSubject(userID),
		ChannelSensitiveWrite,
	)
	require.True(t, ok, "the node-local enforcer must remain deliberately stale")
	require.Equal(t, EffectAllow, effect)

	assert.False(
		t,
		Can(userID, common.RoleAdminUser, ChannelSensitiveWrite),
		"the persisted revocation must win without a local policy reload",
	)
	assert.Empty(t, ExplicitUserOverrides(userID))
}

func TestDuplicatePermissionEffectsPreferDeny(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	const userID = 4202
	subject := UserSubject(userID)

	require.NoError(t, db.Create(&[]model.CasbinRule{
		{
			Ptype: "p",
			V0:    subject,
			V1:    ResourceChannel,
			V2:    ActionSensitiveWrite,
			V3:    EffectDeny,
		},
		{
			Ptype: "p",
			V0:    subject,
			V1:    ResourceChannel,
			V2:    ActionSensitiveWrite,
			V3:    EffectAllow,
		},
	}).Error)

	assert.False(t, Can(userID, common.RoleAdminUser, ChannelSensitiveWrite))
	assert.False(
		t,
		Capabilities(userID, common.RoleAdminUser)[ResourceChannel][ActionSensitiveWrite],
	)
	overrides := ExplicitUserOverrides(userID)
	require.Contains(t, overrides, ResourceChannel)
	require.Contains(t, overrides[ResourceChannel], ActionSensitiveWrite)
	assert.False(t, overrides[ResourceChannel][ActionSensitiveWrite])
}

func TestPermissionDatabaseFailureFailsClosedDespiteStaleAllow(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	const userID = 4201

	require.NoError(t, SetUserPermissions(userID, PermissionsMap{
		ResourceChannel: {
			ActionSensitiveWrite: true,
		},
	}))
	require.True(
		t,
		Can(userID, common.RoleAdminUser, ChannelSensitiveWrite),
	)

	forcedErr := errors.New("forced authoritative policy read failure")
	const callbackName = "test:fail_authoritative_policy_read"
	registered := true
	require.NoError(
		t,
		db.Callback().Query().Before("gorm:query").Register(
			callbackName,
			func(tx *gorm.DB) {
				if tx.Statement != nil &&
					tx.Statement.Table == "casbin_rule" {
					tx.AddError(forcedErr)
				}
			},
		),
	)
	t.Cleanup(func() {
		if registered {
			_ = db.Callback().Query().Remove(callbackName)
		}
	})

	assert.False(
		t,
		Can(userID, common.RoleAdminUser, ChannelSensitiveWrite),
	)
	assert.Empty(t, ExplicitUserOverrides(userID))
	nonRootCapabilities := Capabilities(userID, common.RoleAdminUser)
	require.NotEmpty(t, nonRootCapabilities)
	for _, actions := range nonRootCapabilities {
		for _, allowed := range actions {
			assert.False(
				t,
				allowed,
				"non-root capabilities must fail closed on a policy read error",
			)
		}
	}
	for _, actions := range Capabilities(userID, common.RoleRootUser) {
		for _, allowed := range actions {
			assert.True(
				t,
				allowed,
				"root baseline must not depend on a per-user policy read",
			)
		}
	}

	require.NoError(t, db.Callback().Query().Remove(callbackName))
	registered = false
}
