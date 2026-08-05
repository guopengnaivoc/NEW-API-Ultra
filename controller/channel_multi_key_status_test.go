package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type multiKeyStatusEnvelope struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    MultiKeyStatusResponse `json:"data"`
}

func insertMultiKeyStatusChannel(
	t *testing.T,
	keys []string,
	channelInfo model.ChannelInfo,
) *model.Channel {
	t.Helper()

	channelInfo.IsMultiKey = true
	channelInfo.MultiKeySize = len(keys)
	channelInfo.MultiKeyMode = constant.MultiKeyModeRandom
	channel := &model.Channel{
		Type:        constant.ChannelTypeOpenAI,
		Key:         strings.Join(keys, "\n"),
		Status:      common.ChannelStatusEnabled,
		Name:        "multi-key-status",
		Models:      "gpt-test",
		Group:       "default",
		ChannelInfo: channelInfo,
	}
	require.NoError(t, model.DB.Create(channel).Error)
	return channel
}

func requestMultiKeyStatus(
	t *testing.T,
	channelID int,
	requestFields map[string]any,
) (*httptest.ResponseRecorder, multiKeyStatusEnvelope) {
	t.Helper()

	requestFields["channel_id"] = channelID
	requestFields["action"] = "get_key_status"
	body, err := common.Marshal(requestFields)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/channel/multi_key/manage",
		bytes.NewReader(body),
	)
	context.Request.Header.Set("Content-Type", "application/json")

	ManageMultiKeys(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response multiKeyStatusEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder, response
}

func TestManageMultiKeysStatusOmitsCredentialMaterial(t *testing.T) {
	setupModelListControllerTestDB(t)
	keys := []string{
		"sk-short",
		"provider-secret-long-value",
	}
	channel := insertMultiKeyStatusChannel(t, keys, model.ChannelInfo{
		MultiKeyStatusList: map[int]int{
			1: common.ChannelStatusManuallyDisabled,
		},
	})

	recorder, response := requestMultiKeyStatus(t, channel.Id, map[string]any{
		"page":      1,
		"page_size": 50,
	})

	require.True(t, response.Success, response.Message)
	require.Len(t, response.Data.Keys, len(keys))
	assert.Equal(t, 0, response.Data.Keys[0].Index)
	assert.Equal(t, 1, response.Data.Keys[1].Index)
	assert.NotContains(t, recorder.Body.String(), "key_preview")
	for _, key := range keys {
		assert.NotContains(t, recorder.Body.String(), key)
		assert.NotContains(t, recorder.Body.String(), key[:min(7, len(key))])
	}
}

func TestManageMultiKeysStatusDoesNotLoadCredential(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	channel := insertMultiKeyStatusChannel(
		t,
		[]string{"provider-secret"},
		model.ChannelInfo{},
	)
	require.NoError(t, db.Table("channels").
		Where("id = ?", channel.Id).
		Update("key", "undecryptable-credential-record").Error)

	_, err := model.GetChannelById(channel.Id, true)
	require.ErrorContains(t, err, "legacy plaintext is not permitted")

	_, response := requestMultiKeyStatus(t, channel.Id, map[string]any{})

	require.True(t, response.Success, response.Message)
	assert.Equal(t, []KeyStatus{{Index: 0, Status: 1}}, response.Data.Keys)
}

func TestManageMultiKeysStatusPreservesMetadataIndexesCountsAndFilteredPagination(
	t *testing.T,
) {
	setupModelListControllerTestDB(t)
	channel := insertMultiKeyStatusChannel(
		t,
		[]string{"key-0", "key-1", "key-2", "key-3", "key-4"},
		model.ChannelInfo{
			MultiKeyStatusList: map[int]int{
				1: 2,
				2: 3,
				3: 2,
			},
			MultiKeyDisabledTime: map[int]int64{
				1: 101,
				2: 202,
				3: 303,
			},
			MultiKeyDisabledReason: map[int]string{
				1: "manual-one",
				2: "automatic-two",
				3: "manual-three",
			},
		},
	)

	tests := []struct {
		name          string
		requestFields map[string]any
		expected      MultiKeyStatusResponse
	}{
		{
			name: "all statuses retain their original indexes and metadata",
			requestFields: map[string]any{
				"page":      1,
				"page_size": 5,
			},
			expected: MultiKeyStatusResponse{
				Keys: []KeyStatus{
					{Index: 0, Status: 1},
					{
						Index:        1,
						Status:       2,
						DisabledTime: 101,
						Reason:       "manual-one",
					},
					{
						Index:        2,
						Status:       3,
						DisabledTime: 202,
						Reason:       "automatic-two",
					},
					{
						Index:        3,
						Status:       2,
						DisabledTime: 303,
						Reason:       "manual-three",
					},
					{Index: 4, Status: 1},
				},
				Total:               5,
				Page:                1,
				PageSize:            5,
				TotalPages:          1,
				EnabledCount:        2,
				ManualDisabledCount: 2,
				AutoDisabledCount:   1,
			},
		},
		{
			name: "filtered pagination keeps global counts and original indexes",
			requestFields: map[string]any{
				"page":      2,
				"page_size": 1,
				"status":    2,
			},
			expected: MultiKeyStatusResponse{
				Keys: []KeyStatus{
					{
						Index:        3,
						Status:       2,
						DisabledTime: 303,
						Reason:       "manual-three",
					},
				},
				Total:               2,
				Page:                2,
				PageSize:            1,
				TotalPages:          2,
				EnabledCount:        2,
				ManualDisabledCount: 2,
				AutoDisabledCount:   1,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, response := requestMultiKeyStatus(
				t,
				channel.Id,
				test.requestFields,
			)

			require.True(t, response.Success, response.Message)
			assert.Equal(t, test.expected, response.Data)
		})
	}
}

func TestManageMultiKeysStatusBoundsPageSize(t *testing.T) {
	setupModelListControllerTestDB(t)
	keys := make([]string, 101)
	for index := range keys {
		keys[index] = fmt.Sprintf("provider-key-%03d-secret", index)
	}
	channel := insertMultiKeyStatusChannel(t, keys, model.ChannelInfo{})

	tests := []struct {
		name             string
		requestFields    map[string]any
		expectedPageSize int
	}{
		{
			name:             "omitted page size uses default",
			requestFields:    map[string]any{},
			expectedPageSize: 50,
		},
		{
			name:             "zero page size uses default",
			requestFields:    map[string]any{"page_size": 0},
			expectedPageSize: 50,
		},
		{
			name:             "negative page size uses default",
			requestFields:    map[string]any{"page_size": -1},
			expectedPageSize: 50,
		},
		{
			name:             "exact maximum is accepted",
			requestFields:    map[string]any{"page_size": 100},
			expectedPageSize: 100,
		},
		{
			name:             "over maximum is clamped",
			requestFields:    map[string]any{"page_size": 1_000_000},
			expectedPageSize: 100,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, response := requestMultiKeyStatus(
				t,
				channel.Id,
				test.requestFields,
			)

			require.True(t, response.Success, response.Message)
			assert.Equal(t, test.expectedPageSize, response.Data.PageSize)
			assert.Len(t, response.Data.Keys, test.expectedPageSize)
			assert.Equal(t, 101, response.Data.Total)
		})
	}
}
