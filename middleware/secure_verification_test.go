package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelKeySecurityProofTest(t *testing.T) service.AuthIdentity {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousSecret := common.SessionSecret
	databaseName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", databaseName)),
		&gorm.Config{},
	)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SessionSecret = "channel-key-security-proof-test-secret"
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.SessionSecret = previousSecret
	})
	return service.AuthIdentity{
		UserID:          42,
		SessionID:       "channel-key-session",
		UserAuthVersion: 3,
		SessionVersion:  2,
	}
}

func channelProofRouter(identity service.AuthIdentity, successes *atomic.Int32) *gin.Engine {
	router := gin.New()
	router.POST(
		"/api/channel/:id/key",
		func(c *gin.Context) {
			c.Set("id", identity.UserID)
			c.Set("session_id", identity.SessionID)
			c.Set("auth_version", identity.UserAuthVersion)
			c.Set("session_version", identity.SessionVersion)
			c.Set("auth_identity", identity)
			c.Next()
		},
		ChannelKeySecurityProofRequired(),
		func(c *gin.Context) {
			successes.Add(1)
			c.Status(http.StatusNoContent)
		},
	)
	return router
}

func channelProofRequest(
	router *gin.Engine,
	target,
	proof string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/channel/"+target+"/key",
		nil,
	)
	request.Header.Set("X-Security-Proof", proof)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestChannelKeySecurityProofRejectsWrongTargetWithoutConsumption(t *testing.T) {
	identity := setupChannelKeySecurityProofTest(t)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		"2fa",
		"channel.key.read",
		"17",
	)
	require.NoError(t, err)
	var successes atomic.Int32
	router := channelProofRouter(identity, &successes)

	wrongTarget := channelProofRequest(router, "18", proof)
	assert.Equal(t, http.StatusForbidden, wrongTarget.Code)
	assert.Contains(t, wrongTarget.Body.String(), "SECURITY_PROOF_TARGET_MISMATCH")

	correctTarget := channelProofRequest(router, "17", proof)
	assert.Equal(t, http.StatusNoContent, correctTarget.Code)
	assert.Equal(t, int32(1), successes.Load())
}

func TestChannelKeySecurityProofRejectsReplay(t *testing.T) {
	identity := setupChannelKeySecurityProofTest(t)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		"passkey",
		"channel.key.read",
		"17",
	)
	require.NoError(t, err)
	var successes atomic.Int32
	router := channelProofRouter(identity, &successes)

	first := channelProofRequest(router, "17", proof)
	assert.Equal(t, http.StatusNoContent, first.Code)

	replay := channelProofRequest(router, "17", proof)
	assert.Equal(t, http.StatusForbidden, replay.Code)
	assert.Contains(t, replay.Body.String(), "SECURITY_PROOF_CONSUMED")
	assert.Equal(t, int32(1), successes.Load())
}

func TestChannelKeySecurityProofRejectsMalformedRouteWithoutConsumption(t *testing.T) {
	identity := setupChannelKeySecurityProofTest(t)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		"2fa",
		"channel.key.read",
		"17",
	)
	require.NoError(t, err)
	var successes atomic.Int32
	router := channelProofRouter(identity, &successes)

	for _, target := range []string{"0", "-17", "+17", "017"} {
		response := channelProofRequest(router, target, proof)
		assert.Equal(t, http.StatusOK, response.Code, target)
		assert.Contains(t, response.Body.String(), `"success":false`, target)
		assert.Contains(t, response.Body.String(), "渠道ID格式错误", target)
		assert.NotContains(t, response.Body.String(), "SECURITY_PROOF_TARGET_MISMATCH", target)
	}

	correctTarget := channelProofRequest(router, "17", proof)
	assert.Equal(t, http.StatusNoContent, correctTarget.Code)
	assert.Equal(t, int32(1), successes.Load())
}

func TestChannelKeySecurityProofRejectsAccessJWT(t *testing.T) {
	identity := setupChannelKeySecurityProofTest(t)
	accessToken, _, err := service.IssueAccessToken(identity)
	require.NoError(t, err)
	var successes atomic.Int32
	router := channelProofRouter(identity, &successes)

	response := channelProofRequest(router, "17", accessToken)

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Contains(t, response.Body.String(), "SECURITY_PROOF_INVALID")
	assert.Zero(t, successes.Load())
}

func TestChannelKeySecurityProofConcurrentConsumptionHasOneWinner(t *testing.T) {
	identity := setupChannelKeySecurityProofTest(t)
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		"2fa",
		"channel.key.read",
		"17",
	)
	require.NoError(t, err)
	var successes atomic.Int32
	router := channelProofRouter(identity, &successes)

	const requestCount = 8
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, requestCount)
	var group sync.WaitGroup
	for range requestCount {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			responses <- channelProofRequest(router, "17", proof)
		}()
	}
	close(start)
	group.Wait()
	close(responses)

	noContent := 0
	consumed := 0
	for response := range responses {
		switch response.Code {
		case http.StatusNoContent:
			noContent++
		case http.StatusForbidden:
			assert.Contains(t, response.Body.String(), "SECURITY_PROOF_CONSUMED")
			consumed++
		default:
			assert.Failf(
				t,
				"unexpected response",
				"status=%d body=%s",
				response.Code,
				response.Body.String(),
			)
		}
	}
	assert.Equal(t, 1, noContent)
	assert.Equal(t, requestCount-1, consumed)
	assert.Equal(t, int32(1), successes.Load())
}
