package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskPollingFetchAdaptor struct {
	mu           sync.Mutex
	taskIDs      []string
	keys         []string
	fetched      chan string
	blockTaskID  string
	blockStarted chan struct{}
	releaseBlock chan struct{}
	blockOnce    sync.Once
	responseBody []byte
	responseCode int
	fetchErr     error
	parseResult  *relaycommon.TaskInfo
	parseErr     error
	parseCalls   int
	adjustQuota  int
}

type sunoFailurePollingAdaptor struct {
	failReason string
}

type fixedPollingResponseAdaptor struct {
	response *http.Response
}

func (a *fixedPollingResponseAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *fixedPollingResponseAdaptor) FetchTask(_ string, _ string, _ map[string]any, _ string) (*http.Response, error) {
	return a.response, nil
}

func (a *fixedPollingResponseAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return nil, errors.New("unexpected task response parse")
}

func (a *fixedPollingResponseAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

type sunoDataPollingAdaptor struct {
	item taskdto.SunoDataResponse
}

func (a *sunoDataPollingAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *sunoDataPollingAdaptor) FetchTask(_ string, _ string, _ map[string]any, _ string) (*http.Response, error) {
	responseBody, err := common.Marshal(taskdto.TaskResponse[[]taskdto.SunoDataResponse]{
		Code: taskdto.TaskSuccessCode,
		Data: []taskdto.SunoDataResponse{a.item},
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func (a *sunoDataPollingAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return nil, nil
}

func (a *sunoDataPollingAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func (a *sunoFailurePollingAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *sunoFailurePollingAdaptor) FetchTask(_ string, _ string, body map[string]any, _ string) (*http.Response, error) {
	taskIDs, _ := body["ids"].([]string)
	items := make([]taskdto.SunoDataResponse, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		items = append(items, taskdto.SunoDataResponse{
			TaskID:     taskID,
			Status:     string(model.TaskStatusFailure),
			FailReason: a.failReason,
			FinishTime: time.Now().Unix(),
		})
	}

	responseBody, err := common.Marshal(taskdto.TaskResponse[[]taskdto.SunoDataResponse]{
		Code: taskdto.TaskSuccessCode,
		Data: items,
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func (a *sunoFailurePollingAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return nil, nil
}

func (a *sunoFailurePollingAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func (a *taskPollingFetchAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *taskPollingFetchAdaptor) FetchTask(_ string, key string, body map[string]any, _ string) (*http.Response, error) {
	taskID, _ := body["task_id"].(string)
	if taskID == a.blockTaskID && a.releaseBlock != nil {
		a.blockOnce.Do(func() {
			if a.blockStarted != nil {
				close(a.blockStarted)
			}
		})
		<-a.releaseBlock
	}

	a.mu.Lock()
	a.taskIDs = append(a.taskIDs, taskID)
	a.keys = append(a.keys, key)
	a.mu.Unlock()
	if a.fetched != nil {
		select {
		case a.fetched <- taskID:
		default:
		}
	}
	if a.fetchErr != nil {
		return nil, a.fetchErr
	}
	if a.responseBody != nil {
		statusCode := a.responseCode
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		return &http.Response{
			StatusCode: statusCode,
			Body: io.NopCloser(
				bytes.NewReader(append([]byte(nil), a.responseBody...)),
			),
		}, nil
	}

	response := taskdto.TaskResponse[model.Task]{
		Code: taskdto.TaskSuccessCode,
		Data: model.Task{
			TaskID:   taskID,
			Status:   model.TaskStatusInProgress,
			Progress: "30%",
		},
	}
	responseBody, err := common.Marshal(response)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func (a *taskPollingFetchAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	a.mu.Lock()
	a.parseCalls++
	parseResult := a.parseResult
	parseErr := a.parseErr
	a.mu.Unlock()
	if parseErr != nil {
		return nil, parseErr
	}
	if parseResult != nil {
		result := *parseResult
		return &result, nil
	}
	return &relaycommon.TaskInfo{Status: model.TaskStatusInProgress}, nil
}

func (a *taskPollingFetchAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return a.adjustQuota
}

func (a *taskPollingFetchAdaptor) fetchCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.taskIDs)
}

func (a *taskPollingFetchAdaptor) fetchedTaskIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.taskIDs...)
}

func (a *taskPollingFetchAdaptor) fetchedKeys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.keys...)
}

func (a *taskPollingFetchAdaptor) parseCallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.parseCalls
}

func captureTaskPollingOutput(t *testing.T) *bytes.Buffer {
	t.Helper()

	output := &bytes.Buffer{}
	common.LogWriterMu.Lock()
	originalWriter := gin.DefaultWriter
	originalErrorWriter := gin.DefaultErrorWriter
	gin.DefaultWriter = output
	gin.DefaultErrorWriter = output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = originalWriter
		gin.DefaultErrorWriter = originalErrorWriter
		common.LogWriterMu.Unlock()
	})
	return output
}

func enableTaskPollingDebug(t *testing.T) {
	t.Helper()
	original := common.DebugEnabled
	common.DebugEnabled = true
	t.Cleanup(func() {
		common.DebugEnabled = original
	})
}

func removeTaskPollingEncryptionKeyring(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		require.NoError(t, common.InitDataEncryption())
	})
	t.Setenv("DATA_ENCRYPTION_KEYS", "")
	t.Setenv("DATA_ENCRYPTION_ACTIVE_KEY_ID", "")
	t.Setenv("DATA_ENCRYPTION_ENABLE", "true")
	require.NoError(t, common.InitDataEncryption())
}

func newGeminiPollingPrivacyTask(
	t *testing.T,
	channel *model.Channel,
	publicTaskID string,
	upstreamTaskID string,
	selectedKey string,
	quota int,
) *model.Task {
	t.Helper()

	task := model.InitTask(
		constant.TaskPlatform("gemini"),
		&relaycommon.RelayInfo{
			UserId:     1,
			UsingGroup: "default",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId:   channel.Id,
				ChannelType: constant.ChannelTypeGemini,
				ApiKey:      selectedKey,
			},
		},
	)
	task.TaskID = publicTaskID
	task.Action = constant.TaskActionGenerate
	task.Status = model.TaskStatusInProgress
	task.Progress = taskcommon.ProgressInProgress
	task.Quota = quota
	task.Data = []byte(`{"done":false}`)
	task.PrivateData.UpstreamTaskID = upstreamTaskID
	require.NoError(t, model.DB.Create(task).Error)

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	return &persisted
}

func taskPollingLogEvidence(t *testing.T, output *bytes.Buffer) string {
	t.Helper()
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Order("id").Find(&logs).Error)
	serialized, err := common.Marshal(logs)
	require.NoError(t, err)
	return output.String() + string(serialized)
}

func seedTaskPollingChannel(t *testing.T, id int, disableSleep bool) {
	t.Helper()
	ch := &model.Channel{
		Id:     id,
		Type:   constant.ChannelTypeKling,
		Name:   "polling_channel",
		Key:    "sk-test",
		Status: common.ChannelStatusEnabled,
	}
	if disableSleep {
		ch.SetOtherSettings(dto.ChannelOtherSettings{DisableTaskPollingSleep: true})
	}
	require.NoError(t, model.DB.Create(ch).Error)
}

func seedPollingTask(t *testing.T, channelID int, publicID string, upstreamID string) *model.Task {
	t.Helper()
	task := &model.Task{
		TaskID:    publicID,
		Platform:  constant.TaskPlatform("kling"),
		UserId:    1,
		ChannelId: channelID,
		Action:    constant.TaskActionGenerate,
		Status:    model.TaskStatusInProgress,
		Progress:  "30%",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: upstreamID,
		},
	}
	require.NoError(t, model.DB.Create(task).Error)
	return task
}

func TestUpdateVideoTasksDefaultSleepWaitsBetweenTasks(t *testing.T) {
	truncate(t)

	const channelID = 101
	seedTaskPollingChannel(t, channelID, false)
	first := seedPollingTask(t, channelID, "task_public_1", "upstream_1")
	second := seedPollingTask(t, channelID, "task_public_2", "upstream_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		channelID: {
			first.GetUpstreamTaskID(),
			second.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		first.GetUpstreamTaskID():  first,
		second.GetUpstreamTaskID(): second,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, adaptor.fetchCount())
}

func TestUpdateVideoTasksCanSkipPollingSleepPerChannel(t *testing.T) {
	truncate(t)

	const channelID = 102
	seedTaskPollingChannel(t, channelID, true)
	first := seedPollingTask(t, channelID, "task_public_3", "upstream_3")
	second := seedPollingTask(t, channelID, "task_public_4", "upstream_4")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		channelID: {
			first.GetUpstreamTaskID(),
			second.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		first.GetUpstreamTaskID():  first,
		second.GetUpstreamTaskID(): second,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, adaptor.fetchCount())
}

func TestUpdateVideoTasksResolvesReferencedMultiKeyCredential(t *testing.T) {
	truncate(t)

	const channelID = 103
	channel := &model.Channel{
		Id:     channelID,
		Type:   constant.ChannelTypeGemini,
		Name:   "referenced_multi_key_channel",
		Key:    "provider-key-a\nprovider-key-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		DisableTaskPollingSleep: true,
	})
	require.NoError(t, model.DB.Create(channel).Error)

	task := model.InitTask(
		constant.TaskPlatform("gemini"),
		&relaycommon.RelayInfo{
			UserId:     1,
			UsingGroup: "default",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId:   channelID,
				ChannelType: constant.ChannelTypeGemini,
				ApiKey:      "provider-key-b",
			},
		},
	)
	task.Action = constant.TaskActionGenerate
	task.Status = model.TaskStatusInProgress
	task.Progress = "30%"
	task.PrivateData.UpstreamTaskID = "upstream-referenced-key"
	require.NoError(t, model.DB.Create(task).Error)

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	adaptor := &taskPollingFetchAdaptor{
		responseBody: []byte(`{"done":false}`),
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
		return adaptor
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	require.NoError(t, UpdateVideoTasks(
		context.Background(),
		constant.TaskPlatform("gemini"),
		map[int][]string{
			channelID: {persisted.GetUpstreamTaskID()},
		},
		map[string]*model.Task{
			persisted.GetUpstreamTaskID(): &persisted,
		},
	))
	assert.Equal(t, []string{"provider-key-b"}, adaptor.fetchedKeys())
}

func TestRunTaskPollingOnceGeminiLogsOnlyPublicIdentityAndStableErrorClass(
	t *testing.T,
) {
	truncate(t)
	seedUser(t, 1, 10_000)
	enableTaskPollingDebug(t)
	output := captureTaskPollingOutput(t)

	const (
		channelID        = 104
		publicTaskID     = "task_gemini_public_log_identity"
		rawOperationName = "projects/private-project/locations/private-location/" +
			"publishers/google/models/veo-private/operations/" +
			"operation-private-sentinel"
		selectedKey  = "polling-log-key-private-sentinel"
		rawErrorText = "transport-redirect-error-private-sentinel"
	)
	encodedOperationName := taskcommon.EncodeLocalTaskID(rawOperationName)
	providerURI := "https://provider-private.example.test/operation" +
		"?key=" + selectedKey + "&signed=provider-query-private-sentinel"
	redirectURI := "https://redirect-private.example.test/result" +
		"?token=redirect-query-private-sentinel"

	channel := &model.Channel{
		Id:     channelID,
		Type:   constant.ChannelTypeGemini,
		Name:   "gemini_polling_log_boundary",
		Key:    selectedKey,
		Status: common.ChannelStatusEnabled,
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		DisableTaskPollingSleep: true,
	})
	require.NoError(t, model.DB.Create(channel).Error)

	task := model.InitTask(
		constant.TaskPlatform("gemini"),
		&relaycommon.RelayInfo{
			UserId:     1,
			UsingGroup: "default",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId:   channelID,
				ChannelType: constant.ChannelTypeGemini,
				ApiKey:      selectedKey,
			},
		},
	)
	task.TaskID = publicTaskID
	task.Action = constant.TaskActionGenerate
	task.Status = model.TaskStatusInProgress
	task.Progress = taskcommon.ProgressInProgress
	task.PrivateData.UpstreamTaskID = encodedOperationName
	require.NotEmpty(t, task.PrivateData.ChannelKeyFingerprint)
	fingerprint := task.PrivateData.ChannelKeyFingerprint
	_, err := task.SetProviderResultURI(providerURI)
	require.NoError(t, err)
	require.NotNil(t, task.EncryptedProviderResultURI)
	envelope := *task.EncryptedProviderResultURI
	require.NoError(t, model.DB.Create(task).Error)

	adaptor := &taskPollingFetchAdaptor{
		fetchErr: errors.New(
			rawErrorText + " request=" + providerURI +
				" redirect=" + redirectURI +
				" operation=" + rawOperationName +
				" encoded=" + encodedOperationName +
				" fingerprint=" + fingerprint +
				" envelope=" + envelope,
		),
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(platform constant.TaskPlatform) TaskPollingAdaptor {
		assert.True(t, (&model.Task{Platform: platform}).IsGeminiTask())
		return adaptor
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })
	previousTaskQueryLimit := constant.TaskQueryLimit
	constant.TaskQueryLimit = 100
	t.Cleanup(func() { constant.TaskQueryLimit = previousTaskQueryLimit })

	summary := RunTaskPollingOnce(context.Background(), nil)

	assert.Equal(t, 1, summary.UnfinishedTasks)
	assert.Equal(t, 1, adaptor.fetchCount())
	evidence := output.String()
	assert.Contains(t, evidence, publicTaskID)
	assert.Contains(t, evidence, "upstream_request_failed")
	for _, forbidden := range []string{
		rawOperationName,
		encodedOperationName,
		providerURI,
		redirectURI,
		"provider-query-private-sentinel",
		"redirect-query-private-sentinel",
		selectedKey,
		fingerprint,
		envelope,
		"naenc:v1",
		rawErrorText,
	} {
		assert.NotContains(t, evidence, forbidden)
	}
}

func TestRunTaskPollingOnceGeminiChannelTypeDriftFailsBeforePrivateSinks(
	t *testing.T,
) {
	truncate(t)
	seedUser(t, 1, 10_000)
	enableTaskPollingDebug(t)
	output := captureTaskPollingOutput(t)

	const (
		channelID        = 105
		publicTaskID     = "task_gemini_channel_type_drift"
		rawOperationName = "projects/private-drift-project/locations/us-central1/" +
			"publishers/google/models/veo-private/operations/" +
			"channel-drift-operation-private-sentinel"
		selectedKey      = "channel-drift-key-private-sentinel"
		providerSentinel = "channel-drift-provider-private-sentinel"
	)
	encodedOperationName := taskcommon.EncodeLocalTaskID(rawOperationName)
	providerURI := "https://provider-drift.example.test/" + providerSentinel +
		"?key=" + selectedKey + "&signed=channel-drift-query-private-sentinel"

	channel := &model.Channel{
		Id:     channelID,
		Type:   constant.ChannelTypeKling,
		Name:   "mutated_away_from_gemini",
		Key:    selectedKey,
		Status: common.ChannelStatusEnabled,
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		DisableTaskPollingSleep: true,
	})
	require.NoError(t, model.DB.Create(channel).Error)

	task := model.InitTask(
		constant.TaskPlatform("gemini"),
		&relaycommon.RelayInfo{
			UserId:     1,
			UsingGroup: "default",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId:   channelID,
				ChannelType: constant.ChannelTypeGemini,
				ApiKey:      selectedKey,
			},
		},
	)
	task.TaskID = publicTaskID
	task.Action = constant.TaskActionGenerate
	task.Status = model.TaskStatusInProgress
	task.Progress = taskcommon.ProgressInProgress
	task.Data = []byte(`{"done":false}`)
	task.PrivateData.UpstreamTaskID = encodedOperationName
	require.NoError(t, model.DB.Create(task).Error)
	before := task.Snapshot()

	adaptor := &taskPollingFetchAdaptor{
		responseBody: []byte(
			`{"done":false,"uri":"` + providerURI +
				`","metadata":"channel-drift-metadata-private-sentinel"}`,
		),
		parseResult: &relaycommon.TaskInfo{
			Status:   model.TaskStatusInProgress,
			Progress: "50%",
		},
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(platform constant.TaskPlatform) TaskPollingAdaptor {
		assert.True(t, (&model.Task{Platform: platform}).IsGeminiTask())
		return adaptor
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })
	previousTaskQueryLimit := constant.TaskQueryLimit
	constant.TaskQueryLimit = 100
	t.Cleanup(func() { constant.TaskQueryLimit = previousTaskQueryLimit })

	summary := RunTaskPollingOnce(context.Background(), nil)

	assert.Equal(t, 1, summary.UnfinishedTasks)
	assert.Zero(t, adaptor.fetchCount())
	assert.Zero(t, adaptor.parseCallCount())
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.True(t, before.Equal(persisted.Snapshot()))

	evidence := taskPollingLogEvidence(t, output)
	assert.Contains(t, evidence, publicTaskID)
	assert.Contains(t, evidence, "provider_boundary_mismatch")
	for _, forbidden := range []string{
		rawOperationName,
		encodedOperationName,
		selectedKey,
		providerURI,
		providerSentinel,
		"channel-drift-query-private-sentinel",
		"channel-drift-metadata-private-sentinel",
	} {
		assert.NotContains(t, evidence, forbidden)
	}
}

func TestUpdateVideoSingleTaskGeminiUsesExactLegacyCredentialBoundary(t *testing.T) {
	t.Run("fingerprinted task survives key reordering", func(t *testing.T) {
		truncate(t)
		seedUser(t, 1, 10_000)

		channel := &model.Channel{
			Id:     104,
			Type:   constant.ChannelTypeGemini,
			Key:    "provider-key-b\nprovider-key-a",
			Status: common.ChannelStatusEnabled,
			ChannelInfo: model.ChannelInfo{
				IsMultiKey: true,
			},
		}
		task := newGeminiPollingPrivacyTask(
			t,
			channel,
			"task_gemini_reordered",
			"upstream-gemini-reordered",
			"provider-key-b",
			0,
		)
		adaptor := &taskPollingFetchAdaptor{
			responseBody: []byte(`{"done":false}`),
			parseResult: &relaycommon.TaskInfo{
				Status:   model.TaskStatusInProgress,
				Progress: "50%",
			},
		}

		require.NoError(t, updateVideoSingleTask(
			context.Background(),
			adaptor,
			channel,
			task.GetUpstreamTaskID(),
			map[string]*model.Task{task.GetUpstreamTaskID(): task},
		))

		assert.Equal(t, []string{"provider-key-b"}, adaptor.fetchedKeys())
	})

	t.Run("ambiguous legacy task fails before fetch", func(t *testing.T) {
		channel := &model.Channel{
			Id:   105,
			Type: constant.ChannelTypeGemini,
			Key:  "provider-key-a\nprovider-key-b",
			ChannelInfo: model.ChannelInfo{
				IsMultiKey: true,
			},
		}
		task := &model.Task{
			TaskID:    "task_gemini_ambiguous",
			Platform:  constant.TaskPlatform("gemini"),
			ChannelId: channel.Id,
			Status:    model.TaskStatusInProgress,
			Progress:  taskcommon.ProgressInProgress,
			Data:      []byte(`{"done":false}`),
			PrivateData: model.TaskPrivateData{
				UpstreamTaskID: "upstream-gemini-ambiguous",
			},
		}
		adaptor := &taskPollingFetchAdaptor{
			responseBody: []byte(`{"done":false}`),
		}

		err := updateVideoSingleTask(
			context.Background(),
			adaptor,
			channel,
			task.GetUpstreamTaskID(),
			map[string]*model.Task{task.GetUpstreamTaskID(): task},
		)

		require.ErrorIs(t, err, model.ErrTaskChannelCredentialUnavailable)
		assert.Zero(t, adaptor.fetchCount())
	})
}

func TestUpdateVideoSingleTaskGeminiSanitizesGeneratedSamplesBeforeSinks(
	t *testing.T,
) {
	truncate(t)
	seedUser(t, 1, 10_000)
	enableTaskPollingDebug(t)
	output := captureTaskPollingOutput(t)

	const (
		selectedKey      = "polling-key-secret-sentinel"
		providerPath     = "provider-path-secret-sentinel"
		signedQuery      = "signed%2Fquery-sentinel"
		operationName    = "operation-name-secret-sentinel"
		metadataSentinel = "metadata-secret-sentinel"
	)
	channel := &model.Channel{
		Id:     106,
		Type:   constant.ChannelTypeGemini,
		Key:    selectedKey,
		Status: common.ChannelStatusEnabled,
	}
	task := newGeminiPollingPrivacyTask(
		t,
		channel,
		"task_gemini_private_result",
		"upstream-gemini-private-result",
		selectedKey,
		100,
	)
	rawProviderURI := "https://video.example.test/" + providerPath +
		"?key=" + selectedKey + "&token=" + signedQuery + "&keep=1"
	filteredProviderURI := "https://video.example.test/" + providerPath +
		"?token=" + signedQuery + "&keep=1"
	task.PrivateData.ResultURL = rawProviderURI
	task.FailReason = metadataSentinel
	require.NoError(t, task.Update())

	responseBody := []byte(
		`{"name":"` + operationName + `","done":true,` +
			`"response":{"generateVideoResponse":{"generatedSamples":[` +
			`{"video":{"uri":"` + rawProviderURI +
			`","mimeType":"video/mp4"}}]},` +
			`"metadata":{"value":"` + metadataSentinel + `"}}}`,
	)
	adaptor := &taskPollingFetchAdaptor{
		responseBody: responseBody,
		parseResult: &relaycommon.TaskInfo{
			Status:   model.TaskStatusSuccess,
			Progress: taskcommon.ProgressComplete,
		},
		adjustQuota: 50,
	}

	require.NoError(t, updateVideoSingleTask(
		context.Background(),
		adaptor,
		channel,
		task.GetUpstreamTaskID(),
		map[string]*model.Task{task.GetUpstreamTaskID(): task},
	))

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusSuccess, persisted.Status)
	assert.Equal(t, taskcommon.ProgressComplete, persisted.Progress)
	assert.JSONEq(t, `{
		"done": true,
		"video": {
			"url": "/v1/videos/task_gemini_private_result/content",
			"mime_type": "video/mp4"
		}
	}`, string(persisted.Data))
	assert.Equal(
		t,
		taskcommon.BuildProxyURL(persisted.TaskID),
		persisted.PrivateData.ResultURL,
	)
	assert.Empty(t, persisted.FailReason)
	require.NotNil(t, persisted.EncryptedProviderResultURI)
	assert.True(
		t,
		common.IsDataEncryptionEnvelope(*persisted.EncryptedProviderResultURI),
	)
	openedProviderURI, err := persisted.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, filteredProviderURI, openedProviderURI)
	assert.Equal(t, 0, adaptor.parseCallCount())
	assert.Equal(t, []string{selectedKey}, adaptor.fetchedKeys())

	privateData, err := common.Marshal(persisted.PrivateData)
	require.NoError(t, err)
	evidence := taskPollingLogEvidence(t, output)
	for _, forbidden := range []string{
		selectedKey,
		providerPath,
		signedQuery,
		operationName,
		metadataSentinel,
		rawProviderURI,
	} {
		assert.NotContains(t, string(persisted.Data), forbidden)
		assert.NotContains(t, string(privateData), forbidden)
		assert.NotContains(t, evidence, forbidden)
	}
	require.NotNil(t, getLastLog(t))
}

func TestUpdateVideoSingleTaskGeminiFailureOmitsProviderMessage(t *testing.T) {
	t.Run("terminal failure is sanitized before persistence and logs", func(t *testing.T) {
		truncate(t)
		seedUser(t, 1, 10_000)
		enableTaskPollingDebug(t)
		output := captureTaskPollingOutput(t)

		const (
			selectedKey     = "failure-key-secret-sentinel"
			providerPath    = "failure-provider-path-sentinel"
			providerMessage = "failure-provider-message-sentinel"
		)
		channel := &model.Channel{
			Id:     107,
			Type:   constant.ChannelTypeGemini,
			Key:    selectedKey,
			Status: common.ChannelStatusEnabled,
		}
		task := newGeminiPollingPrivacyTask(
			t,
			channel,
			"task_gemini_failure",
			"upstream-gemini-failure",
			selectedKey,
			100,
		)
		task.Data = []byte(`{"legacy":"` + providerMessage + `"}`)
		task.PrivateData.ResultURL = "https://video.example.test/" +
			providerPath + "?key=" + selectedKey
		task.FailReason = providerMessage
		require.NoError(t, task.Update())

		responseBody := []byte(`{
			"done": true,
			"error": {
				"code": 13,
				"status": "INTERNAL",
				"message": "` + providerMessage + ` https://video.example.test/` +
			providerPath + `?key=` + selectedKey + `"
			}
		}`)
		adaptor := &taskPollingFetchAdaptor{
			responseBody: responseBody,
			parseResult: &relaycommon.TaskInfo{
				Status:   model.TaskStatusFailure,
				Progress: taskcommon.ProgressComplete,
				Reason:   providerMessage,
			},
		}

		require.NoError(t, updateVideoSingleTask(
			context.Background(),
			adaptor,
			channel,
			task.GetUpstreamTaskID(),
			map[string]*model.Task{task.GetUpstreamTaskID(): task},
		))

		var persisted model.Task
		require.NoError(t, model.DB.First(&persisted, task.ID).Error)
		assert.EqualValues(t, model.TaskStatusFailure, persisted.Status)
		assert.Equal(t, taskcommon.ProgressComplete, persisted.Progress)
		assert.Equal(t, "upstream task failed", persisted.FailReason)
		assert.Empty(t, persisted.PrivateData.ResultURL)
		assert.Nil(t, persisted.EncryptedProviderResultURI)
		assert.JSONEq(
			t,
			`{"done":true,"error":{"code":13,"status":"INTERNAL"}}`,
			string(persisted.Data),
		)
		assert.Equal(t, 0, adaptor.parseCallCount())

		privateData, err := common.Marshal(persisted.PrivateData)
		require.NoError(t, err)
		evidence := taskPollingLogEvidence(t, output)
		for _, forbidden := range []string{
			selectedKey,
			providerPath,
			providerMessage,
		} {
			assert.NotContains(t, string(persisted.Data), forbidden)
			assert.NotContains(t, string(privateData), forbidden)
			assert.NotContains(t, evidence, forbidden)
		}
		require.NotNil(t, getLastLog(t))
	})

	t.Run("retryable failure leaves state and billing unchanged", func(t *testing.T) {
		truncate(t)
		seedUser(t, 1, 10_000)
		enableTaskPollingDebug(t)
		output := captureTaskPollingOutput(t)

		const (
			selectedKey     = "retry-key-secret-sentinel"
			providerMessage = "retry-provider-message-sentinel"
		)
		channel := &model.Channel{
			Id:     108,
			Type:   constant.ChannelTypeGemini,
			Key:    selectedKey,
			Status: common.ChannelStatusEnabled,
		}
		task := newGeminiPollingPrivacyTask(
			t,
			channel,
			"task_gemini_retry",
			"upstream-gemini-retry",
			selectedKey,
			100,
		)
		before := task.Snapshot()
		adaptor := &taskPollingFetchAdaptor{
			responseBody: []byte(`{
				"done": true,
				"error": {
					"code": 429,
					"status": "RESOURCE_EXHAUSTED",
					"message": "` + providerMessage + `"
				}
			}`),
			parseResult: &relaycommon.TaskInfo{
				Status:   model.TaskStatusFailure,
				Progress: taskcommon.ProgressComplete,
				Reason:   providerMessage,
			},
		}

		require.NoError(t, updateVideoSingleTask(
			context.Background(),
			adaptor,
			channel,
			task.GetUpstreamTaskID(),
			map[string]*model.Task{task.GetUpstreamTaskID(): task},
		))

		var persisted model.Task
		require.NoError(t, model.DB.First(&persisted, task.ID).Error)
		assert.True(t, before.Equal(persisted.Snapshot()))
		assert.Zero(t, countLogs(t))
		assert.Equal(t, 0, adaptor.parseCallCount())
		assert.NotContains(t, output.String(), selectedKey)
		assert.NotContains(t, output.String(), providerMessage)
	})
}

func TestUpdateVideoSingleTaskGeminiEncryptionFailureDoesNotCommitSuccess(
	t *testing.T,
) {
	truncate(t)
	seedUser(t, 1, 10_000)
	enableTaskPollingDebug(t)
	output := captureTaskPollingOutput(t)

	const (
		selectedKey  = "encryption-key-secret-sentinel"
		providerPath = "encryption-provider-path-sentinel"
	)
	channel := &model.Channel{
		Id:     109,
		Type:   constant.ChannelTypeGemini,
		Key:    selectedKey,
		Status: common.ChannelStatusEnabled,
	}
	task := newGeminiPollingPrivacyTask(
		t,
		channel,
		"task_gemini_encryption_failure",
		"upstream-gemini-encryption-failure",
		selectedKey,
		100,
	)
	before := task.Snapshot()
	rawProviderURI := "https://video.example.test/" + providerPath +
		"?key=" + selectedKey
	adaptor := &taskPollingFetchAdaptor{
		responseBody: []byte(`{
			"done": true,
			"response": {
				"generateVideoResponse": {
					"generatedSamples": [{
						"video": {
							"uri": "` + rawProviderURI + `",
							"mimeType": "video/mp4"
						}
					}]
				}
			}
		}`),
		parseResult: &relaycommon.TaskInfo{
			Status:   model.TaskStatusSuccess,
			Progress: taskcommon.ProgressComplete,
		},
	}
	removeTaskPollingEncryptionKeyring(t)

	err := updateVideoSingleTask(
		context.Background(),
		adaptor,
		channel,
		task.GetUpstreamTaskID(),
		map[string]*model.Task{task.GetUpstreamTaskID(): task},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "class=result_protection_failed")
	assert.NotContains(t, err.Error(), selectedKey)
	assert.NotContains(t, err.Error(), providerPath)
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.True(t, before.Equal(persisted.Snapshot()))
	assert.Zero(t, countLogs(t))
	assert.Equal(t, 0, adaptor.parseCallCount())
	assert.NotContains(t, output.String(), selectedKey)
	assert.NotContains(t, output.String(), providerPath)
}

func TestUpdateVideoSingleTaskGeminiMalformedBodyHasNoRawFallback(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 10_000)
	enableTaskPollingDebug(t)
	output := captureTaskPollingOutput(t)

	const (
		selectedKey      = "malformed-key-secret-sentinel"
		malformedPayload = "malformed-provider-payload-sentinel"
	)
	channel := &model.Channel{
		Id:     110,
		Type:   constant.ChannelTypeGemini,
		Key:    selectedKey,
		Status: common.ChannelStatusEnabled,
	}
	task := newGeminiPollingPrivacyTask(
		t,
		channel,
		"task_gemini_malformed",
		"upstream-gemini-malformed",
		selectedKey,
		100,
	)
	before := task.Snapshot()
	adaptor := &taskPollingFetchAdaptor{
		responseBody: []byte(
			`{"done":true,"uri":"https://video.example.test/` +
				malformedPayload + `?key=` + selectedKey + `"`,
		),
		parseErr: errors.New("legacy parser failed"),
	}

	err := updateVideoSingleTask(
		context.Background(),
		adaptor,
		channel,
		task.GetUpstreamTaskID(),
		map[string]*model.Task{task.GetUpstreamTaskID(): task},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "class=invalid_upstream_response")
	assert.NotContains(t, err.Error(), selectedKey)
	assert.NotContains(t, err.Error(), malformedPayload)
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.True(t, before.Equal(persisted.Snapshot()))
	assert.Zero(t, countLogs(t))
	assert.Equal(t, 0, adaptor.parseCallCount())
	assert.NotContains(t, output.String(), selectedKey)
	assert.NotContains(t, output.String(), malformedPayload)
}

func TestUpdateVideoSingleTaskDoesNotSubstituteRemovedCredential(t *testing.T) {
	channel := &model.Channel{
		Id:   104,
		Type: constant.ChannelTypeGemini,
		Key:  "provider-key-a\nprovider-key-b",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	task := model.InitTask(
		constant.TaskPlatform("gemini"),
		&relaycommon.RelayInfo{
			UserId:     1,
			UsingGroup: "default",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId:   channel.Id,
				ChannelType: constant.ChannelTypeGemini,
				ApiKey:      "removed-provider-key",
			},
		},
	)
	task.Action = constant.TaskActionGenerate
	task.Status = model.TaskStatusInProgress
	task.PrivateData.UpstreamTaskID = "upstream-removed-key"

	adaptor := &taskPollingFetchAdaptor{}
	err := updateVideoSingleTask(
		context.Background(),
		adaptor,
		channel,
		task.GetUpstreamTaskID(),
		map[string]*model.Task{
			task.GetUpstreamTaskID(): task,
		},
	)

	require.ErrorIs(t, err, model.ErrTaskChannelCredentialUnavailable)
	assert.Zero(t, adaptor.fetchCount())
}

func TestUpdateVideoTasksDefaultSleepDoesNotBlockOtherChannels(t *testing.T) {
	truncate(t)

	const firstChannelID = 201
	const secondChannelID = 202
	seedTaskPollingChannel(t, firstChannelID, false)
	seedTaskPollingChannel(t, secondChannelID, false)
	firstChannelFirst := seedPollingTask(t, firstChannelID, "task_public_5", "upstream_a_1")
	firstChannelSecond := seedPollingTask(t, firstChannelID, "task_public_6", "upstream_a_2")
	secondChannelFirst := seedPollingTask(t, secondChannelID, "task_public_7", "upstream_b_1")
	secondChannelSecond := seedPollingTask(t, secondChannelID, "task_public_8", "upstream_b_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		firstChannelID: {
			firstChannelFirst.GetUpstreamTaskID(),
			firstChannelSecond.GetUpstreamTaskID(),
		},
		secondChannelID: {
			secondChannelFirst.GetUpstreamTaskID(),
			secondChannelSecond.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		firstChannelFirst.GetUpstreamTaskID():   firstChannelFirst,
		firstChannelSecond.GetUpstreamTaskID():  firstChannelSecond,
		secondChannelFirst.GetUpstreamTaskID():  secondChannelFirst,
		secondChannelSecond.GetUpstreamTaskID(): secondChannelSecond,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ElementsMatch(t, []string{"upstream_a_1", "upstream_b_1"}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksSlowChannelDoesNotBlockOtherChannels(t *testing.T) {
	truncate(t)

	const slowChannelID = 251
	const fastChannelID = 252
	seedTaskPollingChannel(t, slowChannelID, false)
	seedTaskPollingChannel(t, fastChannelID, true)
	slowTask := seedPollingTask(t, slowChannelID, "task_public_slow", "upstream_slow_1")
	fastFirst := seedPollingTask(t, fastChannelID, "task_public_fast_1", "upstream_fast_parallel_1")
	fastSecond := seedPollingTask(t, fastChannelID, "task_public_fast_2", "upstream_fast_parallel_2")
	slowTaskID := slowTask.GetUpstreamTaskID()
	fastFirstID := fastFirst.GetUpstreamTaskID()
	fastSecondID := fastSecond.GetUpstreamTaskID()

	adaptor := &taskPollingFetchAdaptor{
		fetched:      make(chan string, 4),
		blockTaskID:  slowTaskID,
		blockStarted: make(chan struct{}),
		releaseBlock: make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseBlockedTask := func() {
		releaseOnce.Do(func() {
			close(adaptor.releaseBlock)
		})
	}
	t.Cleanup(releaseBlockedTask)
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	errCh := make(chan error, 1)
	gopool.Go(func() {
		errCh <- UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"), map[int][]string{
			slowChannelID: {
				slowTaskID,
			},
			fastChannelID: {
				fastFirstID,
				fastSecondID,
			},
		}, map[string]*model.Task{
			slowTaskID:   slowTask,
			fastFirstID:  fastFirst,
			fastSecondID: fastSecond,
		})
	})

	select {
	case <-adaptor.blockStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("slow channel did not start blocking")
	}

	require.Eventually(t, func() bool {
		fetchedTaskIDs := adaptor.fetchedTaskIDs()
		return len(fetchedTaskIDs) == 2 &&
			fetchedTaskIDs[0] == fastFirstID &&
			fetchedTaskIDs[1] == fastSecondID
	}, 500*time.Millisecond, 10*time.Millisecond)

	releaseBlockedTask()
	require.NoError(t, <-errCh)
	assert.ElementsMatch(t, []string{
		slowTaskID,
		fastFirstID,
		fastSecondID,
	}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksMixedChannelSleepSettings(t *testing.T) {
	truncate(t)

	const sleepyChannelID = 301
	const fastChannelID = 302
	seedTaskPollingChannel(t, sleepyChannelID, false)
	seedTaskPollingChannel(t, fastChannelID, true)
	sleepyFirst := seedPollingTask(t, sleepyChannelID, "task_public_9", "upstream_sleepy_1")
	sleepySecond := seedPollingTask(t, sleepyChannelID, "task_public_10", "upstream_sleepy_2")
	fastFirst := seedPollingTask(t, fastChannelID, "task_public_11", "upstream_fast_1")
	fastSecond := seedPollingTask(t, fastChannelID, "task_public_12", "upstream_fast_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		sleepyChannelID: {
			sleepyFirst.GetUpstreamTaskID(),
			sleepySecond.GetUpstreamTaskID(),
		},
		fastChannelID: {
			fastFirst.GetUpstreamTaskID(),
			fastSecond.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		sleepyFirst.GetUpstreamTaskID():  sleepyFirst,
		sleepySecond.GetUpstreamTaskID(): sleepySecond,
		fastFirst.GetUpstreamTaskID():    fastFirst,
		fastSecond.GetUpstreamTaskID():   fastSecond,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ElementsMatch(t, []string{"upstream_sleepy_1", "upstream_fast_1", "upstream_fast_2"}, adaptor.fetchedTaskIDs())
}

func TestUpdateSunoTasksStalePollsRefundExactlyOnce(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 401, 401, 401
	const initialUserQuota, initialTokenQuota, taskQuota = 10_000, 6_000, 2_500
	const publicTaskID, upstreamTaskID = "suno_public_refund_once", "suno_upstream_refund_once"

	seedUser(t, userID, initialUserQuota)
	seedToken(t, tokenID, userID, "sk-suno-refund-once", initialTokenQuota)
	require.NoError(t, model.DB.Model(&model.Token{}).
		Where("id = ?", tokenID).
		Update("used_quota", taskQuota).Error)
	baseURL := "https://suno.invalid"
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeSunoAPI,
		Name:    "suno_refund_once",
		Key:     "sk-suno-channel",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
	}).Error)

	task := makeTask(userID, channelID, taskQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = publicTaskID
	task.Platform = constant.TaskPlatformSuno
	task.Status = model.TaskStatusInProgress
	task.Progress = "50%"
	task.SubmitTime = time.Now().Unix()
	task.PrivateData.UpstreamTaskID = upstreamTaskID
	require.NoError(t, model.DB.Create(task).Error)

	var firstPollTask model.Task
	var staleSecondPollTask model.Task
	require.NoError(t, model.DB.First(&firstPollTask, task.ID).Error)
	require.NoError(t, model.DB.First(&staleSecondPollTask, task.ID).Error)

	adaptor := &sunoFailurePollingAdaptor{failReason: "upstream failed"}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	require.NoError(t, updateSunoTasks(context.Background(), channelID, []string{upstreamTaskID}, map[string]*model.Task{
		upstreamTaskID: &firstPollTask,
	}))
	require.NoError(t, updateSunoTasks(context.Background(), channelID, []string{upstreamTaskID}, map[string]*model.Task{
		upstreamTaskID: &staleSecondPollTask,
	}))

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	assert.Zero(t, reloaded.Quota)
	assert.Equal(t, initialUserQuota+taskQuota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota+taskQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestTaskNeedsUpdateComparesDataSemantically(t *testing.T) {
	tests := []struct {
		name       string
		oldData    json.RawMessage
		newData    json.RawMessage
		wantUpdate bool
		wantErr    bool
	}{
		{
			name:       "byte multiset collision requires update",
			oldData:    json.RawMessage(`{"a":1,"b":2}`),
			newData:    json.RawMessage(`{"a":2,"b":1}`),
			wantUpdate: true,
		},
		{
			name:    "reordered equivalent object does not require update",
			oldData: json.RawMessage(`{"title":"first","count":2}`),
			newData: json.RawMessage("{\n\"count\":2,\n\"title\":\"first\"\n}"),
		},
		{
			name:    "nil and explicit null are equivalent",
			oldData: nil,
			newData: json.RawMessage(`null`),
		},
		{
			name:    "empty and nil are equivalent",
			oldData: json.RawMessage{},
			newData: nil,
		},
		{
			name:    "whitespace only and null are equivalent",
			oldData: json.RawMessage(" \n\t"),
			newData: json.RawMessage(`null`),
		},
		{
			name:       "malformed stored data is repaired",
			oldData:    json.RawMessage(`{"value":`),
			newData:    json.RawMessage(`{"value":"valid"}`),
			wantUpdate: true,
			wantErr:    true,
		},
		{
			name:       "invalid UTF-8 stored data is repaired",
			oldData:    json.RawMessage{'"', 'h', 'e', 'l', 'l', 'o', 0xff, '"'},
			newData:    json.RawMessage(`"hello\uFFFD"`),
			wantUpdate: true,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldTask := &model.Task{
				SubmitTime: 11,
				StartTime:  12,
				Status:     model.TaskStatusInProgress,
				Progress:   "50%",
				Data:       test.oldData,
			}
			newTask := taskdto.SunoDataResponse{
				SubmitTime: 11,
				StartTime:  12,
				Status:     string(model.TaskStatusInProgress),
				Data:       test.newData,
			}

			update, err := taskNeedsUpdate(oldTask, newTask)

			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, test.wantUpdate, update)
		})
	}
}

func TestUpdateSunoTasksPersistsDistinctCollidingData(t *testing.T) {
	truncate(t)

	const channelID = 403
	const publicTaskID = "suno_public_collision"
	const upstreamTaskID = "suno_upstream_collision"
	baseURL := "https://suno.invalid"
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeSunoAPI,
		Name:    "suno_collision",
		Key:     "sk-suno-channel",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
	}).Error)

	oldData := json.RawMessage(`{"a":1,"b":2}`)
	newData := json.RawMessage(`{"a":2,"b":1}`)
	task := &model.Task{
		TaskID:    publicTaskID,
		Platform:  constant.TaskPlatformSuno,
		ChannelId: channelID,
		Status:    model.TaskStatusInProgress,
		Progress:  "50%",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		Data:      oldData,
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: upstreamTaskID,
		},
	}
	require.NoError(t, model.DB.Create(task).Error)

	adaptor := &sunoDataPollingAdaptor{
		item: taskdto.SunoDataResponse{
			TaskID: upstreamTaskID,
			Status: string(model.TaskStatusInProgress),
			Data:   newData,
		},
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	require.NoError(t, updateSunoTasks(
		context.Background(),
		channelID,
		[]string{upstreamTaskID},
		map[string]*model.Task{upstreamTaskID: task},
	))

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.JSONEq(t, string(newData), string(reloaded.Data))
}

func TestUpdateSunoTasksRepairsInvalidUTF8StoredData(t *testing.T) {
	truncate(t)

	const channelID = 404
	const publicTaskID = "suno_public_invalid_utf8"
	const upstreamTaskID = "suno_upstream_invalid_utf8"
	baseURL := "https://suno.invalid"
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeSunoAPI,
		Name:    "suno_invalid_utf8",
		Key:     "sk-suno-channel",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
	}).Error)

	oldData := json.RawMessage{'"', 'h', 'e', 'l', 'l', 'o', 0xff, '"'}
	newData := json.RawMessage(`"hello\uFFFD"`)
	task := &model.Task{
		TaskID:    publicTaskID,
		Platform:  constant.TaskPlatformSuno,
		ChannelId: channelID,
		Status:    model.TaskStatusInProgress,
		Progress:  "50%",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		Data:      oldData,
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: upstreamTaskID,
		},
	}
	require.NoError(t, model.DB.Create(task).Error)

	adaptor := &sunoDataPollingAdaptor{
		item: taskdto.SunoDataResponse{
			TaskID: upstreamTaskID,
			Status: string(model.TaskStatusInProgress),
			Data:   newData,
		},
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	require.NoError(t, updateSunoTasks(
		context.Background(),
		channelID,
		[]string{upstreamTaskID},
		map[string]*model.Task{upstreamTaskID: task},
	))

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.True(t, utf8.Valid(reloaded.Data))
	assert.JSONEq(t, string(newData), string(reloaded.Data))
}

func TestRunTaskPollingOnceDoesNotRefundHistoricalFailedTask(t *testing.T) {
	truncate(t)

	const userID, initialQuota, taskQuota = 402, 10_000, 1_200
	seedUser(t, userID, initialQuota)

	task := makeTask(userID, 0, taskQuota, 0, BillingSourceWallet, 0)
	task.TaskID = "historical_failed_already_refunded"
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.SubmitTime = time.Now().Add(-90 * 24 * time.Hour).Unix()
	task.UpdatedAt = time.Now().Add(-time.Minute).Unix()
	require.NoError(t, model.DB.Create(task).Error)

	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
		return &taskPollingFetchAdaptor{}
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	summary := RunTaskPollingOnce(context.Background(), nil)

	assert.Zero(t, summary.UnfinishedTasks)
	assert.Equal(t, initialQuota, getUserQuota(t, userID))
	assert.Equal(t, taskQuota, getTaskQuota(t, task.ID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSweepTimedOutTasksHonorsRefundRolloutBoundary(t *testing.T) {
	truncate(t)

	const (
		userID          = 403
		initialQuota    = 10_000
		legacyTaskQuota = 1_800
		modernTaskQuota = 1_200
	)
	seedUser(t, userID, initialQuota)

	legacyTask := makeTask(userID, 0, legacyTaskQuota, 0, BillingSourceWallet, 0)
	legacyTask.TaskID = "legacy_timeout_without_refund"
	legacyTask.Progress = "50%"
	legacyTask.SubmitTime = 1771718399 // 2026-02-21 23:59:59 UTC
	require.NoError(t, model.DB.Create(legacyTask).Error)

	modernTask := makeTask(userID, 0, modernTaskQuota, 0, BillingSourceWallet, 0)
	modernTask.TaskID = "modern_timeout_with_refund"
	modernTask.Progress = "50%"
	modernTask.SubmitTime = 1771718400 // 2026-02-22 00:00:00 UTC
	require.NoError(t, model.DB.Create(modernTask).Error)

	previousTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 1
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previousTimeout })

	sweepTimedOutTasks(context.Background())

	var reloadedLegacy model.Task
	var reloadedModern model.Task
	require.NoError(t, model.DB.First(&reloadedLegacy, legacyTask.ID).Error)
	require.NoError(t, model.DB.First(&reloadedModern, modernTask.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloadedLegacy.Status)
	assert.EqualValues(t, model.TaskStatusFailure, reloadedModern.Status)
	assert.Zero(t, reloadedLegacy.Quota)
	assert.Zero(t, reloadedModern.Quota)
	assert.Contains(t, reloadedLegacy.FailReason, "旧系统遗留任务")
	assert.Contains(t, reloadedModern.FailReason, "任务超时")
	assert.Equal(t, initialQuota+modernTaskQuota, getUserQuota(t, userID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestUpdateVideoSingleTaskRejectsOversizedResponse(t *testing.T) {
	responseBody := newTrackingResponseBody(`{"code":"success"}`)
	adaptor := &fixedPollingResponseAdaptor{
		response: &http.Response{
			StatusCode:    http.StatusOK,
			Body:          responseBody,
			ContentLength: videoTaskPollingResponseMaxBytes + 1,
		},
	}
	baseURL := "https://video.example"
	channel := &model.Channel{
		Id:      801,
		Type:    constant.ChannelTypeKling,
		Key:     "channel-key",
		BaseURL: &baseURL,
	}
	task := &model.Task{
		TaskID:    "public-video-task",
		ChannelId: channel.Id,
		Action:    constant.TaskActionGenerate,
		Status:    model.TaskStatusInProgress,
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "upstream-video-task",
		},
	}

	err := updateVideoSingleTask(
		context.Background(),
		adaptor,
		channel,
		task.GetUpstreamTaskID(),
		map[string]*model.Task{task.GetUpstreamTaskID(): task},
	)

	require.ErrorIs(t, err, errServiceResponseTooLarge)
	assert.True(t, responseBody.closed)
	assert.Zero(t, responseBody.reads)
}

func TestUpdateSunoTasksRejectsOversizedResponse(t *testing.T) {
	truncate(t)

	const channelID = 802
	baseURL := "https://suno.example"
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeSunoAPI,
		Name:    "suno_response_bound",
		Key:     "channel-key",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
	}).Error)

	responseBody := newTrackingResponseBody(`{"code":"success","data":[]}`)
	adaptor := &fixedPollingResponseAdaptor{
		response: &http.Response{
			StatusCode:    http.StatusOK,
			Body:          responseBody,
			ContentLength: sunoTaskPollingResponseMaxBytes + 1,
		},
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	err := updateSunoTasks(
		context.Background(),
		channelID,
		[]string{"upstream-suno-task"},
		map[string]*model.Task{},
	)

	require.ErrorIs(t, err, errServiceResponseTooLarge)
	assert.True(t, responseBody.closed)
	assert.Zero(t, responseBody.reads)
}

func TestUpdateSunoTasksClosesNonSuccessResponse(t *testing.T) {
	truncate(t)

	const channelID = 803
	baseURL := "https://suno.example"
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeSunoAPI,
		Name:    "suno_response_close",
		Key:     "channel-key",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
	}).Error)

	responseBody := newTrackingResponseBody("upstream unavailable")
	adaptor := &fixedPollingResponseAdaptor{
		response: &http.Response{
			StatusCode:    http.StatusBadGateway,
			Body:          responseBody,
			ContentLength: int64(len("upstream unavailable")),
		},
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	err := updateSunoTasks(
		context.Background(),
		channelID,
		[]string{"upstream-suno-task"},
		map[string]*model.Task{},
	)

	require.Error(t, err)
	assert.True(t, responseBody.closed)
}
