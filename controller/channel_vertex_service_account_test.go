package controller

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	testVertexMaxKeys       = 100
	testVertexMaxKeyBytes   = 64 * 1024
	testVertexMaxTotalBytes = 1024 * 1024
)

type vertexCredentialFixture struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	Padding      string `json:"padding"`
	TokenURI     string `json:"token_uri,omitempty"`
	PrivateKeyID string `json:"private_key_id,omitempty"`
}

type channelMutationResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func vertexCredentialJSON(t *testing.T, suffix string, targetBytes int) string {
	t.Helper()

	credential := vertexCredentialFixture{
		Type:        "service_account",
		ProjectID:   "project-" + suffix,
		PrivateKey:  "private-" + suffix,
		ClientEmail: "account-" + suffix + "@example.com",
		Padding:     "",
	}
	raw, err := common.Marshal(credential)
	require.NoError(t, err)
	require.LessOrEqual(t, len(raw), targetBytes)

	credential.Padding = strings.Repeat("x", targetBytes-len(raw))
	raw, err = common.Marshal(credential)
	require.NoError(t, err)
	require.Len(t, raw, targetBytes)
	return string(raw)
}

func vertexCredentialSetWithSerializedTotal(
	t *testing.T,
	count int,
	totalBytes int,
) []string {
	t.Helper()
	require.Positive(t, count)

	keys := make([]string, count)
	remaining := totalBytes
	for index := range keys {
		itemsLeft := count - index
		target := remaining / itemsLeft
		keys[index] = vertexCredentialJSON(t, fmt.Sprintf("%03d", index), target)
		remaining -= len(keys[index])
	}
	require.Zero(t, remaining)
	return keys
}

func vertexBatchWithTotalBytes(t *testing.T, count int, totalBytes int) string {
	t.Helper()
	require.GreaterOrEqual(t, totalBytes, count+1)

	keys := vertexCredentialSetWithSerializedTotal(
		t,
		count,
		totalBytes-2-(count-1),
	)
	batch := "[" + strings.Join(keys, ",") + "]"
	require.Len(t, batch, totalBytes)
	return batch
}

func vertexTestChannel(key string) *model.Channel {
	settings, _ := common.Marshal(dto.ChannelOtherSettings{
		VertexKeyType: dto.VertexKeyTypeJSON,
	})
	return &model.Channel{
		Type:          constant.ChannelTypeVertexAi,
		Key:           key,
		Status:        common.ChannelStatusEnabled,
		Name:          "Vertex",
		Models:        "gemini-2.5-pro",
		Group:         "default",
		Other:         `{"default":"us-central1"}`,
		OtherSettings: string(settings),
	}
}

func callAddVertexChannel(
	t *testing.T,
	mode string,
	key string,
) channelMutationResponse {
	t.Helper()
	requestBody, err := common.Marshal(AddChannelRequest{
		Mode:    mode,
		Channel: vertexTestChannel(key),
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/channel/",
		bytes.NewReader(requestBody),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	AddChannel(context)

	var response channelMutationResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func callUpdateVertexChannel(
	t *testing.T,
	request map[string]any,
) channelMutationResponse {
	t.Helper()
	return callUpdateVertexChannelWithRole(t, request, common.RoleRootUser)
}

func callUpdateVertexChannelWithRole(
	t *testing.T,
	request map[string]any,
	role int,
) channelMutationResponse {
	t.Helper()
	requestBody, err := common.Marshal(request)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/channel/",
		bytes.NewReader(requestBody),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("id", 1)
	context.Set("role", role)
	UpdateChannel(context)

	var response channelMutationResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func insertVertexMultiKeyChannel(
	t *testing.T,
	db *gorm.DB,
	keys []string,
) *model.Channel {
	t.Helper()
	channel := vertexTestChannel(strings.Join(keys, "\n"))
	channel.ChannelInfo = model.ChannelInfo{
		IsMultiKey:   true,
		MultiKeySize: len(keys),
		MultiKeyMode: constant.MultiKeyModeRandom,
	}
	require.NoError(t, db.Create(channel).Error)
	return channel
}

func storedChannel(t *testing.T, id int) *model.Channel {
	t.Helper()
	channel, err := model.GetChannelById(id, true)
	require.NoError(t, err)
	return channel
}

func setupVertexChannelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	return db
}

func assertKeyUnchanged(t *testing.T, expected string, actual string) {
	t.Helper()
	assert.Equal(t, sha256.Sum256([]byte(expected)), sha256.Sum256([]byte(actual)))
}

func vertexProjectIDs(t *testing.T, channel *model.Channel) []string {
	t.Helper()
	projectIDs := make([]string, 0, len(channel.GetKeys()))
	for _, key := range channel.GetKeys() {
		var credential struct {
			ProjectID string `json:"project_id"`
		}
		require.NoError(t, common.Unmarshal([]byte(key), &credential))
		projectIDs = append(projectIDs, credential.ProjectID)
	}
	return projectIDs
}

func TestGetVertexArrayKeysEnforcesCredentialBoundaries(t *testing.T) {
	t.Run("valid credential preserves additional fields", func(t *testing.T) {
		raw, err := common.Marshal([]vertexCredentialFixture{{
			Type:         "service_account",
			ProjectID:    "project",
			PrivateKey:   "private",
			ClientEmail:  "account@example.com",
			TokenURI:     "https://oauth2.googleapis.com/token",
			PrivateKeyID: "key-id",
		}})
		require.NoError(t, err)

		keys, err := getVertexArrayKeys(string(raw))
		require.NoError(t, err)
		require.Len(t, keys, 1)
		assert.Contains(t, keys[0], `"token_uri":"https://oauth2.googleapis.com/token"`)
		assert.Contains(t, keys[0], `"private_key_id":"key-id"`)
	})

	t.Run("accepts exact count limit", func(t *testing.T) {
		keys := vertexCredentialSetWithSerializedTotal(t, testVertexMaxKeys, 24*1024)
		parsed, err := getVertexArrayKeys("[" + strings.Join(keys, ",") + "]")
		require.NoError(t, err)
		assert.Len(t, parsed, testVertexMaxKeys)
	})

	t.Run("rejects first count above limit", func(t *testing.T) {
		keys := vertexCredentialSetWithSerializedTotal(t, testVertexMaxKeys+1, 24*1024)
		_, err := getVertexArrayKeys("[" + strings.Join(keys, ",") + "]")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), keys[0])
	})

	t.Run("accepts exact per-key byte limit", func(t *testing.T) {
		key := vertexCredentialJSON(t, "exact-key", testVertexMaxKeyBytes)
		parsed, err := getVertexArrayKeys("[" + key + "]")
		require.NoError(t, err)
		require.Len(t, parsed, 1)
		assert.Len(t, parsed[0], testVertexMaxKeyBytes)
	})

	t.Run("rejects first byte above per-key limit", func(t *testing.T) {
		key := vertexCredentialJSON(t, "oversized-key", testVertexMaxKeyBytes+1)
		_, err := getVertexArrayKeys("[" + key + "]")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-oversized-key")
	})

	t.Run("accepts exact aggregate byte limit", func(t *testing.T) {
		batch := vertexBatchWithTotalBytes(t, 17, testVertexMaxTotalBytes)
		parsed, err := getVertexArrayKeys(batch)
		require.NoError(t, err)
		assert.Len(t, parsed, 17)
	})

	t.Run("rejects first byte above aggregate limit", func(t *testing.T) {
		batch := vertexBatchWithTotalBytes(t, 17, testVertexMaxTotalBytes+1)
		_, err := getVertexArrayKeys(batch)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-000")
	})

	for _, testCase := range []struct {
		name  string
		input string
	}{
		{name: "malformed JSON", input: `[{"type":]`},
		{name: "top-level primitive", input: `"credential"`},
		{name: "non-object element", input: `["credential"]`},
		{name: "wrong credential type", input: `[{"type":"authorized_user","project_id":"p","private_key":"k","client_email":"e"}]`},
		{name: "missing project", input: `[{"type":"service_account","private_key":"k","client_email":"e"}]`},
		{name: "blank project", input: `[{"type":"service_account","project_id":" ","private_key":"k","client_email":"e"}]`},
		{name: "missing private key", input: `[{"type":"service_account","project_id":"p","client_email":"e"}]`},
		{name: "blank private key", input: `[{"type":"service_account","project_id":"p","private_key":" ","client_email":"e"}]`},
		{name: "missing client email", input: `[{"type":"service_account","project_id":"p","private_key":"k"}]`},
		{name: "blank client email", input: `[{"type":"service_account","project_id":"p","private_key":"k","client_email":" "}]`},
	} {
		t.Run("rejects "+testCase.name, func(t *testing.T) {
			_, err := getVertexArrayKeys(testCase.input)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "credential")
		})
	}
}

func TestAddVertexChannelRejectsInvalidCredentialsBeforePersistence(t *testing.T) {
	for _, testCase := range []struct {
		name string
		mode string
		key  string
	}{
		{
			name: "single",
			mode: "single",
			key:  `{"type":"service_account","project_id":"p","private_key":"SENSITIVE_SINGLE_MARKER","client_email":""}`,
		},
		{
			name: "batch",
			mode: "batch",
			key:  `[{"type":"service_account","project_id":"p","private_key":"SENSITIVE_BATCH_MARKER","client_email":""}]`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db := setupVertexChannelTestDB(t)

			response := callAddVertexChannel(t, testCase.mode, testCase.key)

			assert.False(t, response.Success)
			assert.NotContains(t, response.Message, "SENSITIVE")
			var count int64
			require.NoError(t, db.Model(&model.Channel{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestAddVertexChannelRejectsNonObjectSettingsBeforePersistence(t *testing.T) {
	db := setupVertexChannelTestDB(t)
	channel := vertexTestChannel(vertexCredentialJSON(t, "invalid-settings", 256))
	channel.OtherSettings = "null"
	requestBody, err := common.Marshal(AddChannelRequest{
		Mode:    "single",
		Channel: channel,
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/channel/",
		bytes.NewReader(requestBody),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	AddChannel(context)

	var response channelMutationResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	var count int64
	require.NoError(t, db.Model(&model.Channel{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAddVertexSingleKeyEnforcesExactByteBoundary(t *testing.T) {
	t.Run("accepts exact limit", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		key := vertexCredentialJSON(t, "single-exact", testVertexMaxKeyBytes)

		response := callAddVertexChannel(t, "single", key)

		assert.True(t, response.Success)
		var channels []model.Channel
		require.NoError(t, db.Find(&channels).Error)
		require.Len(t, channels, 1)
		assert.Len(t, channels[0].Key, testVertexMaxKeyBytes)
	})

	t.Run("rejects first byte above limit", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		key := vertexCredentialJSON(t, "single-oversized", testVertexMaxKeyBytes+1)

		response := callAddVertexChannel(t, "single", key)

		assert.False(t, response.Success)
		assert.NotContains(t, response.Message, "private-single-oversized")
		var count int64
		require.NoError(t, db.Model(&model.Channel{}).Count(&count).Error)
		assert.Zero(t, count)
	})
}

func TestUpdateVertexChannelRejectsNonObjectSettingsWithoutMutation(t *testing.T) {
	db := setupVertexChannelTestDB(t)
	channel := vertexTestChannel(vertexCredentialJSON(t, "preserve-settings", 256))
	channel.Name = "Persisted Vertex"
	channel.Remark = common.GetPointer("preserve remark")
	require.NoError(t, db.Create(channel).Error)
	before := storedChannel(t, channel.Id)

	response := callUpdateVertexChannel(t, map[string]any{
		"id":       channel.Id,
		"name":     "Must Not Persist",
		"settings": "null",
	})

	assert.False(t, response.Success)
	after := storedChannel(t, channel.Id)
	assert.Equal(t, before, after)
}

func TestUpdateVertexChannelRepairsMalformedStoredSettings(t *testing.T) {
	for _, storedSettings := range []string{"{", "null"} {
		t.Run(storedSettings, func(t *testing.T) {
			db := setupVertexChannelTestDB(t)
			channel := vertexTestChannel(
				vertexCredentialJSON(t, "repair-settings", 256),
			)
			channel.Name = "Vertex Before Repair"
			channel.OtherSettings = storedSettings
			require.NoError(t, db.Create(channel).Error)
			before := storedChannel(t, channel.Id)

			response := callUpdateVertexChannel(t, map[string]any{
				"id":       channel.Id,
				"name":     "Vertex After Repair",
				"settings": "{}",
			})

			assert.True(t, response.Success)
			after := storedChannel(t, channel.Id)
			expected := *before
			expected.Name = "Vertex After Repair"
			expected.OtherSettings = "{}"
			assert.Equal(t, &expected, after)
		})
	}
}

func TestUpdateVertexChannelRepairsMalformedSettingsDuringAPIKeyTransition(t *testing.T) {
	db := setupVertexChannelTestDB(t)
	channel := vertexTestChannel(
		vertexCredentialJSON(t, "repair-api-key", 256),
	)
	channel.OtherSettings = "{"
	require.NoError(t, db.Create(channel).Error)
	apiKeySettings, err := common.Marshal(dto.ChannelOtherSettings{
		VertexKeyType: dto.VertexKeyTypeAPIKey,
	})
	require.NoError(t, err)

	response := callUpdateVertexChannel(t, map[string]any{
		"id":       channel.Id,
		"key":      "repaired-api-key",
		"settings": string(apiKeySettings),
	})

	assert.True(t, response.Success)
	after := storedChannel(t, channel.Id)
	assert.Equal(t, "repaired-api-key", after.Key)
	assert.Equal(t, string(apiKeySettings), after.OtherSettings)
	assert.Equal(
		t,
		model.ChannelInfo{MultiKeyMode: constant.MultiKeyModeRandom},
		after.ChannelInfo,
	)
}

func TestUpdateNonVertexChannelDoesNotParseMalformedVertexSettings(t *testing.T) {
	db := setupVertexChannelTestDB(t)
	channel := &model.Channel{
		Type:          constant.ChannelTypeOpenAI,
		Key:           "sk-existing",
		Status:        common.ChannelStatusEnabled,
		Name:          "OpenAI Before Rename",
		Models:        "gpt-4o",
		Group:         "default",
		OtherSettings: "{",
	}
	require.NoError(t, db.Create(channel).Error)
	before := storedChannel(t, channel.Id)

	response := callUpdateVertexChannel(t, map[string]any{
		"id":   channel.Id,
		"name": "OpenAI After Rename",
	})

	assert.True(t, response.Success)
	after := storedChannel(t, channel.Id)
	expected := *before
	expected.Name = "OpenAI After Rename"
	assert.Equal(t, &expected, after)
}

func TestUpdateVertexChannelRenameDoesNotParseMalformedSettings(t *testing.T) {
	db := setupVertexChannelTestDB(t)
	channel := vertexTestChannel(
		vertexCredentialJSON(t, "rename-malformed", 256),
	)
	channel.Name = "Vertex Before Rename"
	channel.OtherSettings = "{"
	require.NoError(t, db.Create(channel).Error)
	before := storedChannel(t, channel.Id)

	response := callUpdateVertexChannel(t, map[string]any{
		"id":   channel.Id,
		"name": "Vertex After Rename",
	})

	assert.True(t, response.Success)
	after := storedChannel(t, channel.Id)
	expected := *before
	expected.Name = "Vertex After Rename"
	assert.Equal(t, &expected, after)
}

func TestUpdateInvalidSettingsRepairLeavesMalformedRowUnchanged(t *testing.T) {
	db := setupVertexChannelTestDB(t)
	channel := vertexTestChannel(
		vertexCredentialJSON(t, "invalid-repair", 256),
	)
	channel.Name = "Vertex Before Invalid Repair"
	channel.OtherSettings = "{"
	require.NoError(t, db.Create(channel).Error)
	before := storedChannel(t, channel.Id)

	response := callUpdateVertexChannel(t, map[string]any{
		"id":       channel.Id,
		"name":     "Must Not Persist",
		"settings": "null",
	})

	assert.False(t, response.Success)
	after := storedChannel(t, channel.Id)
	assert.Equal(t, before, after)
}

func TestUpdateMalformedVertexSettingsAuthorizesBeforeStoredParsing(t *testing.T) {
	db := setupVertexChannelTestDB(t)
	channel := vertexTestChannel(
		vertexCredentialJSON(t, "unauthorized-repair", 256),
	)
	channel.OtherSettings = "{"
	require.NoError(t, db.Create(channel).Error)
	before := storedChannel(t, channel.Id)

	response := callUpdateVertexChannelWithRole(
		t,
		map[string]any{
			"id":       channel.Id,
			"settings": "{}",
		},
		common.RoleAdminUser,
	)

	assert.False(t, response.Success)
	normalizedMessage := strings.ReplaceAll(
		strings.ToLower(response.Message),
		"_",
		" ",
	)
	assert.Contains(t, normalizedMessage, "insufficient privilege")
	assert.NotEqual(t, "Stored channel settings are invalid", response.Message)
	after := storedChannel(t, channel.Id)
	assert.Equal(t, before, after)
}

func TestUpdateVertexRejectedRequestsDoNotRepairStoredProxySettings(t *testing.T) {
	apiKeySettings, err := common.Marshal(dto.ChannelOtherSettings{
		VertexKeyType: dto.VertexKeyTypeAPIKey,
	})
	require.NoError(t, err)

	tests := []struct {
		name    string
		request func(t *testing.T, channel *model.Channel) map[string]any
		role    int
	}{
		{
			name: "missing replacement API key",
			request: func(_ *testing.T, channel *model.Channel) map[string]any {
				return map[string]any{
					"id":       channel.Id,
					"settings": string(apiKeySettings),
				}
			},
			role: common.RoleRootUser,
		},
		{
			name: "denied sensitive update",
			request: func(t *testing.T, channel *model.Channel) map[string]any {
				return map[string]any{
					"id":  channel.Id,
					"key": vertexCredentialJSON(t, "denied-update", 256),
				}
			},
			role: common.RoleAdminUser,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupVertexChannelTestDB(t)
			channel := vertexTestChannel(
				vertexCredentialJSON(t, "malformed-proxy-setting", 256),
			)
			channel.Setting = common.GetPointer("{")
			require.NoError(t, db.Create(channel).Error)
			before := storedChannel(t, channel.Id)

			response := callUpdateVertexChannelWithRole(
				t,
				test.request(t, channel),
				test.role,
			)

			assert.False(t, response.Success)
			after := storedChannel(t, channel.Id)
			assert.Equal(t, before, after)
		})
	}
}

func TestUpdateVertexChannelValidatesReplacementAndOmittedKeys(t *testing.T) {
	t.Run("invalid direct replacement is rejected without persistence", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		original := vertexCredentialJSON(t, "original", 256)
		channel := vertexTestChannel(original)
		require.NoError(t, db.Create(channel).Error)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":  channel.Id,
			"key": `{"type":"service_account","project_id":"p","private_key":"SENSITIVE_REPLACE_MARKER","client_email":""}`,
		})

		assert.False(t, response.Success)
		assert.NotContains(t, response.Message, "SENSITIVE_REPLACE_MARKER")
		assertKeyUnchanged(t, original, storedChannel(t, channel.Id).Key)
	})

	t.Run("omitted key leaves a legacy credential untouched", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		channel := vertexTestChannel(`{"legacy":"credential"}`)
		require.NoError(t, db.Create(channel).Error)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":   channel.Id,
			"name": "Renamed Vertex",
		})

		assert.True(t, response.Success)
		stored := storedChannel(t, channel.Id)
		assert.Equal(t, `{"legacy":"credential"}`, stored.Key)
		assert.Equal(t, "Renamed Vertex", stored.Name)
	})
}

func TestUpdateVertexChannelRejectsPresentWhitespaceKey(t *testing.T) {
	t.Run("single-key channel", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		original := vertexCredentialJSON(t, "single-whitespace", 256)
		channel := vertexTestChannel(original)
		require.NoError(t, db.Create(channel).Error)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":  channel.Id,
			"key": " \t\n ",
		})

		assert.False(t, response.Success)
		assertKeyUnchanged(t, original, storedChannel(t, channel.Id).Key)
	})

	t.Run("multi-key channel", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		keys := []string{
			vertexCredentialJSON(t, "multi-whitespace-a", 256),
			vertexCredentialJSON(t, "multi-whitespace-b", 256),
		}
		channel := insertVertexMultiKeyChannel(t, db, keys)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":       channel.Id,
			"key":      " \t\n ",
			"key_mode": "append",
		})

		assert.False(t, response.Success)
		stored := storedChannel(t, channel.Id)
		assertKeyUnchanged(t, strings.Join(keys, "\n"), stored.Key)
		assert.Equal(t, len(keys), stored.ChannelInfo.MultiKeySize)
	})
}

func TestUpdateVertexChannelUsesPersistedTypeForZeroValuePatches(t *testing.T) {
	tests := []struct {
		name        string
		requestType any
		replacement string
	}{
		{
			name:        "numeric zero with invalid JSON",
			requestType: 0,
			replacement: "not-json",
		},
		{
			name:        "null with oversized key",
			requestType: nil,
			replacement: strings.Repeat("x", testVertexMaxKeyBytes+1),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupVertexChannelTestDB(t)
			original := vertexCredentialJSON(t, "zero-type-patch", 256)
			channel := vertexTestChannel(original)
			require.NoError(t, db.Create(channel).Error)

			response := callUpdateVertexChannel(t, map[string]any{
				"id":   channel.Id,
				"type": test.requestType,
				"key":  test.replacement,
			})

			assert.False(t, response.Success)
			stored := storedChannel(t, channel.Id)
			assert.Equal(t, constant.ChannelTypeVertexAi, stored.Type)
			assertKeyUnchanged(t, original, stored.Key)
		})
	}
}

func TestUpdateVertexAPIKeyUsesPersistedSettingsForEmptyPatches(t *testing.T) {
	tests := []struct {
		name            string
		requestSettings any
	}{
		{
			name:            "null settings",
			requestSettings: nil,
		},
		{
			name:            "empty settings",
			requestSettings: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupVertexChannelTestDB(t)
			channel := vertexTestChannel("existing-api-key")
			settings, err := common.Marshal(dto.ChannelOtherSettings{
				VertexKeyType: dto.VertexKeyTypeAPIKey,
			})
			require.NoError(t, err)
			channel.OtherSettings = string(settings)
			require.NoError(t, db.Create(channel).Error)

			response := callUpdateVertexChannel(t, map[string]any{
				"id":       channel.Id,
				"key":      "rotated-api-key",
				"settings": test.requestSettings,
			})

			assert.True(t, response.Success)
			stored := storedChannel(t, channel.Id)
			assert.Equal(t, "rotated-api-key", stored.Key)
			assert.Equal(t, dto.VertexKeyTypeAPIKey, stored.GetOtherSettings().VertexKeyType)
		})
	}
}

func TestUpdateVertexMultiKeyServiceAccountToAPIKeyResetsMetadata(t *testing.T) {
	db := setupVertexChannelTestDB(t)
	keys := []string{
		vertexCredentialJSON(t, "convert-a", 256),
		vertexCredentialJSON(t, "convert-b", 256),
	}
	channel := vertexTestChannel(strings.Join(keys, "\n"))
	channel.ChannelInfo = model.ChannelInfo{
		IsMultiKey:             true,
		MultiKeySize:           len(keys),
		MultiKeyMode:           constant.MultiKeyModeRandom,
		MultiKeyStatusList:     map[int]int{1: common.ChannelStatusManuallyDisabled},
		MultiKeyDisabledReason: map[int]string{1: "disabled"},
		MultiKeyDisabledTime:   map[int]int64{1: 123456},
	}
	require.NoError(t, db.Create(channel).Error)
	apiKeySettings, err := common.Marshal(dto.ChannelOtherSettings{
		VertexKeyType: dto.VertexKeyTypeAPIKey,
	})
	require.NoError(t, err)

	response := callUpdateVertexChannel(t, map[string]any{
		"id":       channel.Id,
		"key":      "replacement-api-key",
		"key_mode": "replace",
		"settings": string(apiKeySettings),
	})

	assert.True(t, response.Success)
	stored := storedChannel(t, channel.Id)
	assert.Equal(t, "replacement-api-key", stored.Key)
	assert.Equal(t, dto.VertexKeyTypeAPIKey, stored.GetOtherSettings().VertexKeyType)
	assert.False(t, stored.ChannelInfo.IsMultiKey)
	assert.Zero(t, stored.ChannelInfo.MultiKeySize)
	assert.Empty(t, stored.ChannelInfo.MultiKeyStatusList)
	assert.Empty(t, stored.ChannelInfo.MultiKeyDisabledReason)
	assert.Empty(t, stored.ChannelInfo.MultiKeyDisabledTime)
}

func TestUpdateVertexMultiKeyServiceAccountToAPIKeyRequiresReplacement(t *testing.T) {
	tests := []struct {
		name    string
		request map[string]any
	}{
		{
			name: "omitted replacement key",
			request: map[string]any{
				"key_mode": "replace",
			},
		},
		{
			name: "append mode",
			request: map[string]any{
				"key":      "replacement-api-key",
				"key_mode": "append",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupVertexChannelTestDB(t)
			keys := []string{
				vertexCredentialJSON(t, "reject-convert-a", 256),
				vertexCredentialJSON(t, "reject-convert-b", 256),
			}
			channel := insertVertexMultiKeyChannel(t, db, keys)
			apiKeySettings, err := common.Marshal(dto.ChannelOtherSettings{
				VertexKeyType: dto.VertexKeyTypeAPIKey,
			})
			require.NoError(t, err)
			test.request["id"] = channel.Id
			test.request["settings"] = string(apiKeySettings)

			response := callUpdateVertexChannel(t, test.request)

			assert.False(t, response.Success)
			stored := storedChannel(t, channel.Id)
			assertKeyUnchanged(t, strings.Join(keys, "\n"), stored.Key)
			assert.NotEqual(t, dto.VertexKeyTypeAPIKey, stored.GetOtherSettings().VertexKeyType)
			assert.True(t, stored.ChannelInfo.IsMultiKey)
			assert.Equal(t, len(keys), stored.ChannelInfo.MultiKeySize)
		})
	}
}

func TestUpdateVertexServiceAccountToNonVertexRequiresReplacement(t *testing.T) {
	t.Run("omitted replacement leaves the full row unchanged", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		channel := vertexTestChannel(
			vertexCredentialJSON(t, "non-vertex-omitted", 256),
		)
		channel.Name = "Persisted Vertex"
		require.NoError(t, db.Create(channel).Error)
		before := storedChannel(t, channel.Id)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":   channel.Id,
			"name": "Must Not Persist",
			"type": constant.ChannelTypeOpenAI,
		})

		assert.False(t, response.Success)
		after := storedChannel(t, channel.Id)
		assert.Equal(t, before, after)
	})

	t.Run("append replacement leaves a multi-key row unchanged", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		keys := []string{
			vertexCredentialJSON(t, "non-vertex-append-a", 256),
			vertexCredentialJSON(t, "non-vertex-append-b", 256),
		}
		channel := insertVertexMultiKeyChannel(t, db, keys)
		before := storedChannel(t, channel.Id)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":       channel.Id,
			"type":     constant.ChannelTypeOpenAI,
			"key":      "sk-must-not-append",
			"key_mode": "append",
		})

		assert.False(t, response.Success)
		after := storedChannel(t, channel.Id)
		assert.Equal(t, before, after)
	})
}

func TestUpdateVertexServiceAccountToNonVertexReplacesCredentialAndMetadata(t *testing.T) {
	db := setupVertexChannelTestDB(t)
	keys := []string{
		vertexCredentialJSON(t, "non-vertex-replace-a", 256),
		vertexCredentialJSON(t, "non-vertex-replace-b", 256),
	}
	channel := vertexTestChannel(strings.Join(keys, "\n"))
	channel.ChannelInfo = model.ChannelInfo{
		IsMultiKey:             true,
		MultiKeySize:           len(keys),
		MultiKeyMode:           constant.MultiKeyModePolling,
		MultiKeyStatusList:     map[int]int{1: common.ChannelStatusManuallyDisabled},
		MultiKeyDisabledReason: map[int]string{1: "disabled"},
		MultiKeyDisabledTime:   map[int]int64{1: 123456},
		MultiKeyPollingIndex:   1,
	}
	require.NoError(t, db.Create(channel).Error)
	before := storedChannel(t, channel.Id)

	response := callUpdateVertexChannel(t, map[string]any{
		"id":       channel.Id,
		"type":     constant.ChannelTypeOpenAI,
		"key":      "sk-openai-replacement",
		"key_mode": "replace",
		"settings": "{}",
	})

	assert.True(t, response.Success)
	stored := storedChannel(t, channel.Id)
	expected := *before
	expected.Type = constant.ChannelTypeOpenAI
	expected.Key = "sk-openai-replacement"
	expected.EncryptedKey = stored.EncryptedKey
	expected.OtherSettings = "{}"
	expected.ChannelInfo = model.ChannelInfo{
		MultiKeyMode: constant.MultiKeyModeRandom,
	}
	assert.Equal(t, &expected, stored)
	assert.NotEqual(t, before.EncryptedKey, stored.EncryptedKey)
	assert.NotContains(t, stored.EncryptedKey, "sk-openai-replacement")
}

func TestUpdateChannelRejectsInvalidKeyModeWithoutMutation(t *testing.T) {
	tests := []struct {
		name    string
		keyMode any
	}{
		{name: "typo", keyMode: "apend"},
		{name: "empty", keyMode: ""},
		{name: "null", keyMode: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupVertexChannelTestDB(t)
			keys := []string{
				vertexCredentialJSON(t, "invalid-mode-a", 256),
				vertexCredentialJSON(t, "invalid-mode-b", 256),
			}
			channel := insertVertexMultiKeyChannel(t, db, keys)
			before := storedChannel(t, channel.Id)
			requestBody, err := common.Marshal(map[string]any{
				"id":       channel.Id,
				"key":      vertexCredentialJSON(t, "invalid-mode-replacement", 256),
				"key_mode": test.keyMode,
			})
			require.NoError(t, err)

			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(
				http.MethodPut,
				"/api/channel/",
				bytes.NewReader(requestBody),
			)
			context.Request.Header.Set("Content-Type", "application/json")
			context.Set("id", 1)
			context.Set("role", common.RoleRootUser)
			UpdateChannel(context)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			var response channelMutationResponse
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			after := storedChannel(t, channel.Id)
			assert.Equal(t, before, after)
		})
	}
}

func TestUpdateRequiresKeyWhenEnteringVertexServiceAccountMode(t *testing.T) {
	t.Run("from another channel type", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		channel := &model.Channel{
			Type:          constant.ChannelTypeOpenAI,
			Key:           "sk-existing",
			Status:        common.ChannelStatusEnabled,
			Name:          "OpenAI",
			Models:        "gpt-4o",
			Group:         "default",
			OtherSettings: "{}",
		}
		require.NoError(t, db.Create(channel).Error)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":       channel.Id,
			"type":     constant.ChannelTypeVertexAi,
			"other":    `{"default":"us-central1"}`,
			"settings": `{"vertex_key_type":"json"}`,
		})

		assert.False(t, response.Success)
		stored := storedChannel(t, channel.Id)
		assert.Equal(t, constant.ChannelTypeOpenAI, stored.Type)
		assert.Equal(t, "sk-existing", stored.Key)
	})

	t.Run("from Vertex API-key mode", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		channel := vertexTestChannel("existing-api-key")
		apiKeySettings, err := common.Marshal(dto.ChannelOtherSettings{
			VertexKeyType: dto.VertexKeyTypeAPIKey,
		})
		require.NoError(t, err)
		channel.OtherSettings = string(apiKeySettings)
		require.NoError(t, db.Create(channel).Error)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":       channel.Id,
			"settings": `{"vertex_key_type":"json"}`,
		})

		assert.False(t, response.Success)
		stored := storedChannel(t, channel.Id)
		assert.Equal(t, "existing-api-key", stored.Key)
		assert.Equal(t, dto.VertexKeyTypeAPIKey, stored.GetOtherSettings().VertexKeyType)
	})
}

func TestUpdateNonVertexToVertexServiceAccountPersistsEffectiveSettings(t *testing.T) {
	tests := []struct {
		name           string
		storedSettings string
	}{
		{
			name:           "replaces stale API key mode",
			storedSettings: `{"vertex_key_type":"api_key"}`,
		},
		{
			name:           "repairs malformed stored settings",
			storedSettings: "{",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupVertexChannelTestDB(t)
			channel := &model.Channel{
				Type:          constant.ChannelTypeOpenAI,
				Key:           "sk-existing",
				Status:        common.ChannelStatusEnabled,
				Name:          "OpenAI Before Vertex Transition",
				Models:        "gemini-2.5-pro",
				Group:         "default",
				OtherSettings: test.storedSettings,
			}
			require.NoError(t, db.Create(channel).Error)

			serviceAccountKey := vertexCredentialJSON(t, "direct-transition", 256)
			normalizedKey, err := normalizeVertexServiceAccountKey(
				[]byte(serviceAccountKey),
			)
			require.NoError(t, err)
			response := callUpdateVertexChannel(t, map[string]any{
				"id":    channel.Id,
				"type":  constant.ChannelTypeVertexAi,
				"key":   serviceAccountKey,
				"other": `{"default":"us-central1"}`,
			})

			require.True(t, response.Success)
			stored := storedChannel(t, channel.Id)
			assert.Equal(t, constant.ChannelTypeVertexAi, stored.Type)
			assert.Equal(t, normalizedKey, stored.Key)
			assert.Equal(t, "{}", stored.OtherSettings)
			assert.True(t, isVertexServiceAccountChannel(
				stored.Type,
				stored.GetOtherSettings(),
			))
		})
	}
}

func TestAppendVertexChannelEnforcesFinalDeduplicatedLimits(t *testing.T) {
	t.Run("preserves legacy duplicate indexes and disabled metadata", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		keyA := vertexCredentialJSON(t, "legacy-a", 256)
		keyB := vertexCredentialJSON(t, "legacy-b", 256)
		keyC := vertexCredentialJSON(t, "legacy-c", 256)
		channel := vertexTestChannel(strings.Join([]string{keyA, keyA, keyB}, "\n"))
		channel.ChannelInfo = model.ChannelInfo{
			IsMultiKey:             true,
			MultiKeySize:           3,
			MultiKeyMode:           constant.MultiKeyModeRandom,
			MultiKeyStatusList:     map[int]int{2: common.ChannelStatusManuallyDisabled},
			MultiKeyDisabledReason: map[int]string{2: "legacy-disabled"},
			MultiKeyDisabledTime:   map[int]int64{2: 123456},
		}
		require.NoError(t, db.Create(channel).Error)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":       channel.Id,
			"key":      keyC,
			"key_mode": "append",
		})

		assert.True(t, response.Success)
		stored := storedChannel(t, channel.Id)
		assert.Equal(
			t,
			[]string{
				"project-legacy-a",
				"project-legacy-a",
				"project-legacy-b",
				"project-legacy-c",
			},
			vertexProjectIDs(t, stored),
		)
		assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.ChannelInfo.MultiKeyStatusList[2])
		assert.Equal(t, "legacy-disabled", stored.ChannelInfo.MultiKeyDisabledReason[2])
		assert.Equal(t, int64(123456), stored.ChannelInfo.MultiKeyDisabledTime[2])
		assert.NotContains(t, stored.ChannelInfo.MultiKeyStatusList, 3)
	})

	t.Run("duplicate at count limit remains allowed", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		keys := vertexCredentialSetWithSerializedTotal(t, testVertexMaxKeys, 24*1024)
		channel := insertVertexMultiKeyChannel(t, db, keys)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":       channel.Id,
			"type":     channel.Type,
			"key":      keys[0],
			"key_mode": "append",
			"other":    channel.Other,
			"settings": channel.OtherSettings,
		})

		assert.True(t, response.Success)
		stored := storedChannel(t, channel.Id)
		storedKeys := stored.GetKeys()
		uniqueKeys := make(map[string]struct{}, len(storedKeys))
		for _, key := range storedKeys {
			uniqueKeys[key] = struct{}{}
		}
		assert.Len(t, storedKeys, testVertexMaxKeys)
		assert.Len(t, uniqueKeys, testVertexMaxKeys)
		assert.Equal(t, testVertexMaxKeys, stored.ChannelInfo.MultiKeySize)
	})

	t.Run("unique key above count limit is rejected", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		keys := vertexCredentialSetWithSerializedTotal(t, testVertexMaxKeys, 24*1024)
		channel := insertVertexMultiKeyChannel(t, db, keys)
		original := channel.Key
		newKey := vertexCredentialJSON(t, "unique", 256)

		response := callUpdateVertexChannel(t, map[string]any{
			"id":       channel.Id,
			"type":     channel.Type,
			"key":      newKey,
			"key_mode": "append",
			"other":    channel.Other,
			"settings": channel.OtherSettings,
		})

		assert.False(t, response.Success)
		assert.NotContains(t, response.Message, "private-unique")
		assertKeyUnchanged(t, original, storedChannel(t, channel.Id).Key)
	})

	t.Run("unique key above aggregate limit is rejected", func(t *testing.T) {
		db := setupVertexChannelTestDB(t)
		newKey := vertexCredentialJSON(t, "aggregate-new", 256)
		existingTotal := testVertexMaxTotalBytes - len(newKey) + 1
		keys := vertexCredentialSetWithSerializedTotal(t, 17, existingTotal)
		channel := insertVertexMultiKeyChannel(t, db, keys)
		original := channel.Key

		response := callUpdateVertexChannel(t, map[string]any{
			"id":       channel.Id,
			"type":     channel.Type,
			"key":      newKey,
			"key_mode": "append",
			"other":    channel.Other,
			"settings": channel.OtherSettings,
		})

		assert.False(t, response.Success)
		assert.NotContains(t, response.Message, "private-aggregate-new")
		assertKeyUnchanged(t, original, storedChannel(t, channel.Id).Key)
	})
}
