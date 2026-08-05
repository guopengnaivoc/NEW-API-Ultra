package controller

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

type subscriptionPlanAPIResponse struct {
	Success bool                  `json:"success"`
	Data    []SubscriptionPlanDTO `json:"data"`
}

func useSubscriptionPlanControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		sqlDB, dbErr := db.DB()
		require.NoError(t, dbErr)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestGetSubscriptionPlansTreatsLegacyNullEnabledAsEnabled(t *testing.T) {
	db := useSubscriptionPlanControllerTestDB(t)
	confirmPaymentComplianceForTest(t)
	enabled := true
	disabled := false
	plans := []model.SubscriptionPlan{
		{Title: "legacy-null", Enabled: &enabled},
		{Title: "enabled", Enabled: &enabled},
		{Title: "disabled", Enabled: &disabled},
	}
	for index := range plans {
		require.NoError(t, db.Create(&plans[index]).Error)
	}
	require.NoError(t, db.Exec(
		"UPDATE subscription_plans SET enabled = NULL WHERE id = ?",
		plans[0].Id,
	).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/subscription/plans", nil)
	GetSubscriptionPlans(context)

	var response subscriptionPlanAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data, 2)
	titles := make([]string, 0, len(response.Data))
	for _, item := range response.Data {
		require.NotNil(t, item.Plan.Enabled)
		assert.True(t, *item.Plan.Enabled)
		titles = append(titles, item.Plan.Title)
	}
	assert.ElementsMatch(t, []string{"legacy-null", "enabled"}, titles)
}

func TestAdminUpdateSubscriptionPlanPreservesEnabledWhenOmitted(t *testing.T) {
	db := useSubscriptionPlanControllerTestDB(t)
	confirmPaymentComplianceForTest(t)
	disabled := false
	plan := model.SubscriptionPlan{
		Title:         "disabled",
		PriceAmount:   1,
		DurationUnit:  model.SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       &disabled,
	}
	require.NoError(t, db.Create(&plan).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", plan.Id)}}
	context.Request = httptest.NewRequest(http.MethodPut, "/api/subscription/plans", strings.NewReader(`{
		"plan": {
			"title": "updated",
			"price_amount": 2,
			"duration_unit": "month",
			"duration_value": 1
		}
	}`))
	context.Request.Header.Set("Content-Type", "application/json")
	AdminUpdateSubscriptionPlan(context)

	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	var stored model.SubscriptionPlan
	require.NoError(t, db.First(&stored, plan.Id).Error)
	require.NotNil(t, stored.Enabled)
	assert.False(t, *stored.Enabled)
	assert.Equal(t, "updated", stored.Title)
}
