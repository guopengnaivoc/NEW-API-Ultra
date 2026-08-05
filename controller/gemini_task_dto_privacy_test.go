package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiTaskListAndDetailResponsesNeverExposePrivateResult(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	db := useGeminiTaskSubmitTestDB(t)
	require.NoError(t, model.MigrateGeminiTaskResultPrivacy())

	const (
		providerPath      = "gemini-endpoint-provider-path-sentinel"
		providerQuery     = "gemini-endpoint-provider-query-sentinel"
		providerMessage   = "gemini-endpoint-provider-message-sentinel"
		privateResultPath = "gemini-endpoint-private-result-sentinel"
	)
	user := model.User{
		Username: "gemini-endpoint-user",
		Password: "not-used",
		Quota:    common.MaxQuota,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)

	task := &model.Task{
		TaskID: "task_gemini_endpoint_privacy",
		UserId: user.Id,
		Group:  "default",
		Platform: constant.TaskPlatform(
			strconv.Itoa(constant.ChannelTypeGemini),
		),
		Status:     model.TaskStatusSuccess,
		Progress:   taskcommon.ProgressComplete,
		FailReason: providerMessage,
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://private.example.test/" +
				privateResultPath + "?sig=" + providerQuery,
		},
		Data: []byte(`{
			"done": true,
			"response": {
				"generateVideoResponse": {
					"generatedVideos": [{
						"video": {
							"uri": "https://video.example.test/` +
			providerPath + `?sig=` + providerQuery + `",
							"mimeType": "video/webm"
						}
					}]
				}
			}
		}`),
	}
	_, err := task.SetProviderResultURI(
		"https://encrypted.example.test/" + providerPath +
			"?sig=" + providerQuery,
	)
	require.NoError(t, err)
	envelope := *task.EncryptedProviderResultURI
	require.NoError(t, db.Create(task).Error)

	runList := func(handler gin.HandlerFunc, userID int) string {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(
			http.MethodGet,
			"/api/task/?p=1&page_size=10",
			nil,
		)
		if userID != 0 {
			context.Set("id", userID)
		}
		handler(context)
		require.Equal(t, http.StatusOK, recorder.Code)
		return recorder.Body.String()
	}

	userList := runList(GetUserTask, user.Id)
	adminList := runList(GetAllTask, 0)

	detailRecorder := httptest.NewRecorder()
	detailContext, _ := gin.CreateTestContext(detailRecorder)
	detailContext.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/task/"+task.TaskID,
		nil,
	)
	detailContext.Params = gin.Params{
		{Key: "task_id", Value: task.TaskID},
	}
	detailContext.Set("id", user.Id)
	taskErr := relay.RelayTaskFetch(
		detailContext,
		relayconstant.RelayModeVideoFetchByID,
	)
	require.Nil(t, taskErr)
	require.Equal(t, http.StatusOK, detailRecorder.Code)
	detail := detailRecorder.Body.String()

	for name, body := range map[string]string{
		"user list":  userList,
		"admin list": adminList,
		"detail":     detail,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Contains(
				t,
				body,
				taskcommon.BuildProxyURL(task.TaskID),
			)
			assert.Contains(t, body, `"mime_type":"video/webm"`)
			assert.NotContains(t, body, providerMessage)
			for _, sentinel := range []string{
				providerPath,
				providerQuery,
				privateResultPath,
				envelope,
				"naenc:v1",
				"generatedVideos",
				"generateVideoResponse",
			} {
				assert.NotContains(
					t,
					body,
					sentinel,
					fmt.Sprintf("%s leaked %s", name, sentinel),
				)
			}
			assert.False(
				t,
				strings.Contains(body, "encrypted.example.test"),
			)
		})
	}
}
