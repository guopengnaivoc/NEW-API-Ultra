package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserSettingTestDB(t *testing.T) *model.User {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}))

	user := &model.User{
		Username: "notification-setting-user",
		Password: "password-placeholder",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
	}
	require.NoError(t, db.Create(user).Error)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled = previousRedis
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return user
}

func performUpdateUserSettingRequest(t *testing.T, userID int, email string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := common.Marshal(gin.H{
		"notify_type":             dto.NotifyTypeEmail,
		"quota_warning_threshold": 100,
		"notification_email":      email,
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPut, "/api/user/setting", bytes.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("id", userID)
	UpdateUserSetting(context)
	return recorder
}

func TestUpdateUserSettingRejectsMultipleNotificationRecipients(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupUserSettingTestDB(t)

	for _, email := range []string{
		"victim@example.com;attacker@example.com",
		"victim@example.com\r\nBcc: attacker@example.com",
	} {
		recorder := performUpdateUserSettingRequest(t, user.Id, email)
		var response struct {
			Success bool `json:"success"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		assert.False(t, response.Success)

		updated, err := model.GetUserById(user.Id, true)
		require.NoError(t, err)
		assert.Empty(t, updated.GetSetting().NotificationEmail)
	}
}

func TestUpdateUserSettingStoresParsedMailbox(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := setupUserSettingTestDB(t)

	recorder := performUpdateUserSettingRequest(t, user.Id, "Example User <user@example.com>")

	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	updated, err := model.GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "user@example.com", updated.GetSetting().NotificationEmail)
}
