package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMidjourneyImageRoutesRequireToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}))
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	engine := gin.New()
	registerMjRouterGroup(engine.Group("/mj"))
	registerMjRouterGroup(engine.Group("/:mode/mj"))

	for _, path := range []string{
		"/mj/image/missing-task",
		"/relay/mj/image/missing-task",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)

		assert.Equal(t, http.StatusUnauthorized, response.Code, path)
		assert.NotContains(t, response.Body.String(), "midjourney_task_not_found", path)
	}
}
