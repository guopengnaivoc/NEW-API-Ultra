package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type userResponseSecretFixture struct {
	commonUser *model.User
	secrets    []string
}

func setupUserResponseSecretFixture(t *testing.T) *userResponseSecretFixture {
	t.Helper()
	db := setupManageUserTestDB(t)

	type seededUser struct {
		name   string
		role   int
		delete bool
	}
	seeds := []seededUser{
		{name: "common-response-target", role: common.RoleCommonUser},
		{name: "peer-admin-response-target", role: common.RoleAdminUser},
		{name: "root-response-target", role: common.RoleRootUser},
		{name: "deleted-response-target", role: common.RoleCommonUser, delete: true},
	}

	fixture := &userResponseSecretFixture{}
	for index, seed := range seeds {
		webhookSecret := fmt.Sprintf("%s-webhook-secret", seed.name)
		gotifyToken := fmt.Sprintf("%s-gotify-token", seed.name)
		user := &model.User{
			Username:    seed.name,
			DisplayName: seed.name,
			Password:    "password-placeholder",
			Role:        seed.role,
			Status:      common.UserStatusEnabled,
			Group:       "default",
			AffCode:     fmt.Sprintf("response-secret-aff-%d", index),
			AuthVersion: 1,
		}
		user.SetSetting(dto.UserSetting{
			Language:      "en",
			WebhookSecret: webhookSecret,
			GotifyToken:   gotifyToken,
		})
		require.NoError(t, db.Create(user).Error)
		if seed.delete {
			require.NoError(t, db.Delete(user).Error)
		}
		if seed.role == common.RoleCommonUser && !seed.delete {
			fixture.commonUser = user
		}
		fixture.secrets = append(fixture.secrets, webhookSecret, gotifyToken)
	}

	require.NotNil(t, fixture.commonUser)
	return fixture
}

func TestAdminUserReadResponsesExcludeDecryptedSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := setupUserResponseSecretFixture(t)

	tests := []struct {
		name          string
		target        string
		targetID      int
		expectedUsers []string
		handler       func(*gin.Context)
	}{
		{
			name:   "list",
			target: "/api/user/?p=1&page_size=100",
			expectedUsers: []string{
				"common-response-target",
				"peer-admin-response-target",
				"root-response-target",
				"deleted-response-target",
			},
			handler: GetAllUsers,
		},
		{
			name:   "search",
			target: "/api/user/search?keyword=&p=1&page_size=100",
			expectedUsers: []string{
				"common-response-target",
				"peer-admin-response-target",
				"root-response-target",
				"deleted-response-target",
			},
			handler: SearchUsers,
		},
		{
			name:          "single user",
			target:        fmt.Sprintf("/api/user/%d", fixture.commonUser.Id),
			targetID:      fixture.commonUser.Id,
			expectedUsers: []string{"common-response-target"},
			handler:       GetUser,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, test.target, nil)
			context.Set("role", common.RoleAdminUser)
			if test.targetID != 0 {
				context.Params = gin.Params{{
					Key:   "id",
					Value: strconv.Itoa(test.targetID),
				}}
			}

			test.handler(context)

			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"success":true`)
			for _, username := range test.expectedUsers {
				assert.Contains(t, recorder.Body.String(), username)
			}
			assert.NotContains(t, recorder.Body.String(), `"setting":`)
			for _, secret := range fixture.secrets {
				assert.NotContains(t, recorder.Body.String(), secret)
			}
		})
	}
}

func TestSelfUserResponseRetainsOwnerSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := setupUserResponseSecretFixture(t)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
	context.Set("id", fixture.commonUser.Id)
	context.Set("role", fixture.commonUser.Role)

	GetSelf(context)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Setting string `json:"setting"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotEmpty(t, response.Data.Setting)

	var setting dto.UserSetting
	require.NoError(t, common.Unmarshal([]byte(response.Data.Setting), &setting))
	assert.Equal(t, "common-response-target-webhook-secret", setting.WebhookSecret)
	assert.Equal(t, "common-response-target-gotify-token", setting.GotifyToken)
}
