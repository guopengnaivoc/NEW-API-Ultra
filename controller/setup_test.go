package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type setupAPIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    Setup  `json:"data"`
}

func useSetupControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousSelfUse := operation_setting.SelfUseModeEnabled
	previousDemo := operation_setting.DemoSiteEnabled
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared&_busy_timeout=5000",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Setup{}, &model.Option{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	operation_setting.SelfUseModeEnabled = false
	operation_setting.DemoSiteEnabled = false
	constant.SetSetupCompleted(false)
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		operation_setting.SelfUseModeEnabled = previousSelfUse
		operation_setting.DemoSiteEnabled = previousDemo
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
		constant.SetSetupCompleted(false)
		sqlDB, dbErr := db.DB()
		require.NoError(t, dbErr)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func performSetupRequest(t *testing.T, body string) (*httptest.ResponseRecorder, setupAPIResponse) {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	PostSetup(context)
	var response setupAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder, response
}

func performGetSetupRequest(t *testing.T) (*httptest.ResponseRecorder, setupAPIResponse) {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/setup", nil)
	GetSetup(context)
	var response setupAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder, response
}

func TestGetSetupReturnsCompletedSnapshot(t *testing.T) {
	useSetupControllerTestDB(t)
	constant.SetSetupCompleted(true)

	recorder, response := performGetSetupRequest(t)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, response.Success)
	assert.True(t, response.Data.Status)
	assert.False(t, response.Data.RootInit)
	assert.Empty(t, response.Data.DatabaseType)
}

func TestGetSetupReturnsUninitializedSnapshot(t *testing.T) {
	useSetupControllerTestDB(t)

	recorder, response := performGetSetupRequest(t)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, response.Success)
	assert.False(t, response.Data.Status)
	assert.False(t, response.Data.RootInit)
	assert.Equal(t, string(common.DatabaseTypeSQLite), response.Data.DatabaseType)
}

func TestGetSetupReadsCompletionOnce(t *testing.T) {
	useSetupControllerTestDB(t)
	previousReader := loadSetupCompleted
	readCount := 0
	loadSetupCompleted = func() bool {
		readCount++
		return readCount > 1
	}
	t.Cleanup(func() {
		loadSetupCompleted = previousReader
	})

	recorder, response := performGetSetupRequest(t)

	assert.Equal(t, 1, readCount)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, response.Success)
	assert.False(t, response.Data.Status)
	assert.False(t, response.Data.RootInit)
	assert.Equal(t, string(common.DatabaseTypeSQLite), response.Data.DatabaseType)
}

func TestPostSetupPublishesRuntimeStateAfterSuccessfulCommit(t *testing.T) {
	db := useSetupControllerTestDB(t)
	recorder, response := performSetupRequest(t, `{
		"username":"root-owner",
		"password":"strong-pass",
		"confirmPassword":"strong-pass",
		"SelfUseModeEnabled":true,
		"DemoSiteEnabled":false
	}`)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, response.Success)
	assert.Equal(t, "系统初始化成功", response.Message)
	assert.True(t, constant.SetupCompleted())
	assert.True(t, operation_setting.SelfUseModeEnabled)
	assert.False(t, operation_setting.DemoSiteEnabled)

	var setupCount, rootCount int64
	require.NoError(t, db.Model(&model.Setup{}).Count(&setupCount).Error)
	require.NoError(t, db.Model(&model.User{}).
		Where("role = ?", common.RoleRootUser).Count(&rootCount).Error)
	assert.Equal(t, int64(1), setupCount)
	assert.Equal(t, int64(1), rootCount)
	var root model.User
	require.NoError(t, db.Where("role = ?", common.RoleRootUser).First(&root).Error)
	assert.True(t, common.ValidatePasswordAndHash("strong-pass", root.Password))
}

func TestPostSetupDoesNotApplyOptionsWhenRootAlreadyExists(t *testing.T) {
	db := useSetupControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Username: "existing-root", Password: "hash",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled,
	}).Error)

	_, response := performSetupRequest(t, `{
		"username":"attacker",
		"password":"strong-pass",
		"confirmPassword":"strong-pass",
		"SelfUseModeEnabled":true,
		"DemoSiteEnabled":true
	}`)

	assert.False(t, response.Success)
	assert.Equal(t, "系统已经初始化完成", response.Message)
	assert.False(t, operation_setting.SelfUseModeEnabled)
	assert.False(t, operation_setting.DemoSiteEnabled)
	var optionCount int64
	require.NoError(t, db.Model(&model.Option{}).Count(&optionCount).Error)
	assert.Zero(t, optionCount)
}

func TestPostSetupAlreadyInitializedPublishesDurableWinnerAndClosesLocalSetup(t *testing.T) {
	useSetupControllerTestDB(t)
	require.NoError(t, model.InitializeSystem(model.SetupInitializationParams{
		RootUser: model.User{
			Username: "winner-root", Password: "winner-hash",
			Role: common.RoleRootUser, Status: common.UserStatusEnabled,
		},
		SelfUseModeEnabled: true,
		DemoSiteEnabled:    false,
	}))
	operation_setting.SelfUseModeEnabled = false
	operation_setting.DemoSiteEnabled = true
	constant.SetSetupCompleted(false)

	_, response := performSetupRequest(t, `{
		"username":"loser-root",
		"password":"strong-pass",
		"confirmPassword":"strong-pass",
		"SelfUseModeEnabled":false,
		"DemoSiteEnabled":true
	}`)

	assert.False(t, response.Success)
	assert.Equal(t, "系统已经初始化完成", response.Message)
	assert.True(t, constant.SetupCompleted())
	assert.True(t, operation_setting.SelfUseModeEnabled)
	assert.False(t, operation_setting.DemoSiteEnabled)
}

func TestPostSetupAfterAlreadyInitializedConvergenceUsesFastPath(t *testing.T) {
	useSetupControllerTestDB(t)
	require.NoError(t, model.InitializeSystem(model.SetupInitializationParams{
		RootUser: model.User{
			Username: "winner-root", Password: "winner-hash",
			Role: common.RoleRootUser, Status: common.UserStatusEnabled,
		},
		SelfUseModeEnabled: true,
		DemoSiteEnabled:    false,
	}))
	constant.SetSetupCompleted(false)
	_, firstResponse := performSetupRequest(t, `{
		"username":"loser-root",
		"password":"strong-pass",
		"confirmPassword":"strong-pass"
	}`)
	require.False(t, firstResponse.Success)
	require.True(t, constant.SetupCompleted())

	_, response := performSetupRequest(t, `{malformed`)

	assert.False(t, response.Success)
	assert.Equal(t, "系统已经初始化完成", response.Message)
}

func TestPostSetupHistoricalRootWithoutOptionsClosesLocalSetup(t *testing.T) {
	db := useSetupControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Username: "existing-root", Password: "hash",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled,
	}).Error)

	_, response := performSetupRequest(t, `{
		"username":"attacker",
		"password":"strong-pass",
		"confirmPassword":"strong-pass",
		"SelfUseModeEnabled":true,
		"DemoSiteEnabled":true
	}`)

	assert.False(t, response.Success)
	assert.Equal(t, "系统已经初始化完成", response.Message)
	assert.True(t, constant.SetupCompleted())
	assert.False(t, operation_setting.SelfUseModeEnabled)
	assert.False(t, operation_setting.DemoSiteEnabled)
}

func TestPostSetupDoesNotExposePersistenceErrors(t *testing.T) {
	db := useSetupControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Username: "duplicate", Password: "hash",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		AffCode: "existing-user-code",
	}).Error)

	_, response := performSetupRequest(t, `{
		"username":"duplicate",
		"password":"strong-pass",
		"confirmPassword":"strong-pass",
		"SelfUseModeEnabled":true,
		"DemoSiteEnabled":true
	}`)

	assert.False(t, response.Success)
	assert.Equal(t, "系统初始化失败", response.Message)
	assert.NotContains(t, strings.ToLower(response.Message), "unique")
	assert.False(t, constant.SetupCompleted())
	assert.False(t, operation_setting.SelfUseModeEnabled)
	assert.False(t, operation_setting.DemoSiteEnabled)
}
