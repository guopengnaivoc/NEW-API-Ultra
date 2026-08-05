package passkey

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	webauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSessionDataFlowPreservesTargetedStepUpRequest(t *testing.T) {
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousSecret := common.SessionSecret
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SessionSecret = "passkey-session-flow-test-secret"
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.SessionSecret = previousSecret
	})

	sessionData := &webauthn.SessionData{Challenge: "challenge"}
	token, expiresAt, err := CreateSessionDataFlow(
		model.AuthFlowPurposePasskeyStepUp,
		42,
		"session-42",
		"email.change",
		"next@example.com",
		sessionData,
	)
	require.NoError(t, err)
	assert.Positive(t, expiresAt)

	got, scope, target, err := PopSessionDataFlow(
		token,
		model.AuthFlowPurposePasskeyStepUp,
		42,
		"session-42",
	)
	require.NoError(t, err)
	assert.Equal(t, sessionData.Challenge, got.Challenge)
	assert.Equal(t, "email.change", scope)
	assert.Equal(t, "next@example.com", target)
}
