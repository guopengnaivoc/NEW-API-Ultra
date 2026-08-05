package middleware

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var distributorTokenLimitTestOnce sync.Once

func initDistributorTokenLimitTestI18n(t *testing.T) {
	t.Helper()
	distributorTokenLimitTestOnce.Do(func() {
		require.NoError(t, i18n.Init())
	})
}

func applyTokenModelLimitContextForTest(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{
		"mj_imagine": true,
		"gpt-4":      true,
	})
}

func configureDistributorTestEncryption(t *testing.T) {
	t.Helper()
	encryptionKey := "k1=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	t.Setenv("DATA_ENCRYPTION_KEYS", encryptionKey)
	t.Setenv("DATA_ENCRYPTION_ACTIVE_KEY_ID", "k1")
	t.Setenv("DATA_ENCRYPTION_ENABLE", "true")
	require.NoError(t, common.InitDataEncryption())
	t.Cleanup(func() {
		require.NoError(t, common.InitDataEncryption())
	})
}

func setupDistributorModelChannel(t *testing.T, modelName string) int {
	t.Helper()
	configureDistributorTestEncryption(t)
	previousDB := model.DB
	previousDatabaseType := common.MainDatabaseType()
	previousRedisEnabled := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Task{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	require.NoError(t, model.InitLogDB())

	channel := model.Channel{
		Type:        constant.ChannelTypeOpenAI,
		Name:        "distributor-token-limit-test",
		Status:      common.ChannelStatusEnabled,
		Key:         "distributor-test-key",
		Models:      modelName,
		Group:       "default",
		CreatedTime: time.Now().Unix(),
	}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousDatabaseType)
		common.RedisEnabled = previousRedisEnabled
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return channel.Id
}

func TestDistributeAllowsFetchRoutesWithoutModelWhenTokenModelLimitEnabled(t *testing.T) {
	initDistributorTokenLimitTestI18n(t)
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(applyTokenModelLimitContextForTest, Distribute())
	router.GET("/mj/task/:id/fetch", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.GET("/suno/fetch/:id", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	mjRequest := httptest.NewRequest(http.MethodGet, "/mj/task/abc/fetch", nil)
	mjResponse := httptest.NewRecorder()
	router.ServeHTTP(mjResponse, mjRequest)
	assert.Equal(t, http.StatusNoContent, mjResponse.Code)

	sunoRequest := httptest.NewRequest(http.MethodGet, "/suno/fetch/xyz", nil)
	sunoResponse := httptest.NewRecorder()
	router.ServeHTTP(sunoResponse, sunoRequest)
	assert.Equal(t, http.StatusNoContent, sunoResponse.Code)
}

func TestDistributeKeepsModelLimitsOnSelectableRoutes(t *testing.T) {
	initDistributorTokenLimitTestI18n(t)
	gin.SetMode(gin.TestMode)
	channelID := setupDistributorModelChannel(t, "gpt-4")

	router := gin.New()
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"gpt-4": true})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Next()
	}, Distribute())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-99"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	require.Equal(t, http.StatusForbidden, response.Code)

	allowedRouter := gin.New()
	allowedRouter.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"gpt-4": true})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(channelID))
		c.Next()
	}, Distribute())
	allowedRouter.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4"}`))
	req.Header.Set("Content-Type", "application/json")
	allowedResponse := httptest.NewRecorder()
	allowedRouter.ServeHTTP(allowedResponse, req)
	assert.Equal(t, http.StatusOK, allowedResponse.Code)
}

// newRemixDistributorRouter builds a /v1/videos/:video_id/remix route behind Distribute()
// with the supplied token model limits. reachedRelay reports whether the downstream relay
// handler ran, and selectedChannel reports whether Distribute ever committed a channel to
// the context — the two observable prerequisites for billing and upstream submission.
func newRemixDistributorRouter(userId int, allowedModels map[string]bool, reachedRelay *bool, selectedChannel *bool) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, allowedModels)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserId, userId)
		c.Next()
		_, hasChannel := common.GetContextKey(c, constant.ContextKeyChannelId)
		_, hasOriginalModel := c.Get("original_model")
		*selectedChannel = hasChannel || hasOriginalModel
	}, Distribute())
	router.POST("/v1/videos/:video_id/remix", func(c *gin.Context) {
		*reachedRelay = true
		c.Status(http.StatusNoContent)
	})
	return router
}

func createRemixOriginTask(t *testing.T, taskId string, userId int, channelId int, originModel string) {
	t.Helper()
	task := model.Task{
		TaskID:     taskId,
		Platform:   constant.TaskPlatformSuno,
		UserId:     userId,
		ChannelId:  channelId,
		Properties: model.Properties{OriginModelName: originModel},
	}
	require.NoError(t, model.DB.Create(&task).Error)
}

func TestDistributeEnforcesTokenModelLimitOnVideoRemix(t *testing.T) {
	initDistributorTokenLimitTestI18n(t)
	gin.SetMode(gin.TestMode)
	channelID := setupDistributorModelChannel(t, "sora-2")

	const ownerUserId = 7
	createRemixOriginTask(t, "video-owned", ownerUserId, channelID, "sora-2")
	// Same user, different token: the origin task carries no token identity, so a
	// restricted token must still be judged against the origin task's model.
	createRemixOriginTask(t, "video-other-token", ownerUserId, channelID, "sora-2")

	cases := []struct {
		name           string
		videoId        string
		userId         int
		allowedModels  map[string]bool
		expectedStatus int
		expectRelay    bool
	}{
		{
			name:           "forbidden origin model is rejected before relay",
			videoId:        "video-owned",
			userId:         ownerUserId,
			allowedModels:  map[string]bool{"gpt-4": true},
			expectedStatus: http.StatusForbidden,
			expectRelay:    false,
		},
		{
			name:           "same-user task created by another token is still checked",
			videoId:        "video-other-token",
			userId:         ownerUserId,
			allowedModels:  map[string]bool{"gpt-4": true},
			expectedStatus: http.StatusForbidden,
			expectRelay:    false,
		},
		{
			name:           "unresolvable origin task fails closed",
			videoId:        "video-missing",
			userId:         ownerUserId,
			allowedModels:  map[string]bool{"sora-2": true},
			expectedStatus: http.StatusForbidden,
			expectRelay:    false,
		},
		{
			name:           "task owned by another user fails closed",
			videoId:        "video-owned",
			userId:         ownerUserId + 1,
			allowedModels:  map[string]bool{"sora-2": true},
			expectedStatus: http.StatusForbidden,
			expectRelay:    false,
		},
		{
			name:           "allowed origin model reaches relay",
			videoId:        "video-owned",
			userId:         ownerUserId,
			allowedModels:  map[string]bool{"sora-2": true},
			expectedStatus: http.StatusNoContent,
			expectRelay:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reachedRelay := false
			selectedChannel := false
			router := newRemixDistributorRouter(tc.userId, tc.allowedModels, &reachedRelay, &selectedChannel)

			req := httptest.NewRequest(http.MethodPost, "/v1/videos/"+tc.videoId+"/remix", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)

			assert.Equal(t, tc.expectedStatus, response.Code)
			assert.Equal(t, tc.expectRelay, reachedRelay)
			// A rejected remix must not have committed a channel, so no quota can be
			// pre-consumed and no upstream submission can be built.
			assert.Equal(t, tc.expectRelay, selectedChannel)
		})
	}
}

func TestDistributeRecoversRemixModelFromTaskDataFallback(t *testing.T) {
	initDistributorTokenLimitTestI18n(t)
	gin.SetMode(gin.TestMode)
	channelID := setupDistributorModelChannel(t, "sora-2")

	const ownerUserId = 11
	task := model.Task{
		TaskID:     "video-data-only",
		Platform:   constant.TaskPlatformSuno,
		UserId:     ownerUserId,
		ChannelId:  channelID,
		Properties: model.Properties{},
	}
	task.SetData(map[string]any{"model": "sora-2"})
	require.NoError(t, model.DB.Create(&task).Error)

	reachedRelay := false
	selectedChannel := false
	router := newRemixDistributorRouter(ownerUserId, map[string]bool{"gpt-4": true}, &reachedRelay, &selectedChannel)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/video-data-only/remix", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	require.Equal(t, http.StatusForbidden, response.Code)
	assert.False(t, reachedRelay)
	assert.False(t, selectedChannel)
}
