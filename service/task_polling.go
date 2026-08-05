package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/samber/lo"
)

// TaskPollingAdaptor 定义轮询所需的最小适配器接口，避免 service -> relay 的循环依赖
type TaskPollingAdaptor interface {
	Init(info *relaycommon.RelayInfo)
	FetchTask(baseURL string, key string, body map[string]any, proxy string) (*http.Response, error)
	ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error)
	// AdjustBillingOnComplete 在任务到达终态（成功/失败）时由轮询循环调用。
	// 返回正数触发差额结算（补扣/退还），返回 0 保持预扣费金额不变。
	AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int
}

// GetTaskAdaptorFunc 由 main 包注入，用于获取指定平台的任务适配器。
// 打破 service -> relay -> relay/channel -> service 的循环依赖。
var GetTaskAdaptorFunc func(platform constant.TaskPlatform) TaskPollingAdaptor

// sweepTimedOutTasks 在主轮询之前独立清理超时任务。
// 每次最多处理 100 条，剩余的下个周期继续处理。
// 使用 per-task CAS (UpdateWithStatus) 防止覆盖被正常轮询已推进的任务。
func sweepTimedOutTasks(ctx context.Context) {
	if constant.TaskTimeoutMinutes <= 0 {
		return
	}
	cutoff := time.Now().Unix() - int64(constant.TaskTimeoutMinutes)*60
	tasks := model.GetTimedOutUnfinishedTasks(cutoff, 100)
	if len(tasks) == 0 {
		return
	}

	reason := fmt.Sprintf("任务超时（%d分钟）", constant.TaskTimeoutMinutes)
	legacyReason := "任务超时（旧系统遗留任务，不进行退款，请联系管理员）"
	now := time.Now().Unix()
	timedOutCount := 0

	for _, task := range tasks {
		oldStatus := task.Status
		task.Status = model.TaskStatusFailure
		task.Progress = "100%"
		task.FinishTime = now
		if task.SubmitTime > 0 &&
			task.SubmitTime < model.TaskRefundLegacyCutoff {
			task.FailReason = legacyReason
		} else {
			task.FailReason = reason
		}

		result, err := ApplyDurableTaskBillingTransition(
			ctx,
			task,
			oldStatus,
			0,
			model.BillingOperationOutcomeRefund,
			task.FailReason,
		)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf(
				"sweepTimedOutTasks durable transition error for task %s: %v",
				task.TaskID,
				err,
			))
			continue
		}
		if !result.Changed {
			logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: task %s already transitioned, skip", task.TaskID))
			continue
		}
		timedOutCount++
	}

	if timedOutCount > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: timed out %d tasks", timedOutCount))
	}
}

// TaskPollSummary is the result recorded on an async_task_poll system task row,
// summarizing one polling pass.
type TaskPollSummary struct {
	UnfinishedTasks  int `json:"unfinished_tasks"`
	PlatformsScanned int `json:"platforms_scanned"`
	NullTasksFailed  int `json:"null_tasks_failed"`
}

type geminiTaskPollingError struct {
	publicTaskID string
	class        string
	cause        error
}

func (e *geminiTaskPollingError) Error() string {
	return fmt.Sprintf(
		"Gemini task polling failed (task=%s, class=%s)",
		e.publicTaskID,
		e.class,
	)
}

func (e *geminiTaskPollingError) Unwrap() error {
	return e.cause
}

func newGeminiTaskPollingError(
	task *model.Task,
	class string,
	cause error,
) error {
	publicTaskID := ""
	if task != nil {
		publicTaskID = task.TaskID
	}
	return &geminiTaskPollingError{
		publicTaskID: publicTaskID,
		class:        class,
		cause:        cause,
	}
}

// RunTaskPollingOnce performs one async-task (Suno/video) polling pass
// synchronously. It honors ctx cancellation (the system-task runner cancels it
// when the lease is lost) and, when report is non-nil, reports progress as
// (processedPlatforms, totalPlatforms). It returns immediately if the task
// adaptor factory has not been wired yet, to avoid a nil call during startup.
func RunTaskPollingOnce(ctx context.Context, report func(processed, total int)) TaskPollSummary {
	summary := TaskPollSummary{}
	if GetTaskAdaptorFunc == nil {
		return summary
	}
	if ctx == nil {
		ctx = context.Background()
	}

	common.SysLog("任务进度轮询开始")
	sweepTimedOutTasks(ctx)
	allTasks := model.GetAllUnFinishSyncTasks(constant.TaskQueryLimit)
	summary.UnfinishedTasks = len(allTasks)
	platformTask := make(map[constant.TaskPlatform][]*model.Task)
	for _, t := range allTasks {
		platformTask[t.Platform] = append(platformTask[t.Platform], t)
	}

	totalPlatforms := len(platformTask)
	processedPlatforms := 0
	for platform, tasks := range platformTask {
		if ctx.Err() != nil {
			break
		}
		if report != nil {
			report(processedPlatforms, totalPlatforms)
		}
		processedPlatforms++
		if len(tasks) == 0 {
			continue
		}
		summary.PlatformsScanned++
		taskChannelM := make(map[int][]string)
		taskM := make(map[string]*model.Task)
		nullTasks := make([]*model.Task, 0)
		for _, task := range tasks {
			upstreamID := task.GetUpstreamTaskID()
			if upstreamID == "" {
				nullTasks = append(nullTasks, task)
				continue
			}
			pollingID := upstreamID
			if task.IsGeminiTask() {
				pollingID = task.TaskID
			}
			taskM[pollingID] = task
			taskChannelM[task.ChannelId] = append(
				taskChannelM[task.ChannelId],
				pollingID,
			)
		}
		if len(nullTasks) > 0 {
			for _, task := range nullTasks {
				fromStatus := task.Status
				task.Status = model.TaskStatusFailure
				task.Progress = "100%"
				task.FailReason = "任务缺少上游任务 ID"
				task.FinishTime = time.Now().Unix()
				result, err := ApplyDurableTaskBillingTransition(
					ctx,
					task,
					fromStatus,
					0,
					model.BillingOperationOutcomeRefund,
					task.FailReason,
				)
				if err != nil {
					logger.LogError(ctx, fmt.Sprintf(
						"fail task without upstream ID error (task=%s): %v",
						task.TaskID,
						err,
					))
					continue
				}
				if result.Changed {
					summary.NullTasksFailed++
				}
			}
		}
		if len(taskChannelM) == 0 {
			continue
		}

		DispatchPlatformUpdate(ctx, platform, taskChannelM, taskM)
	}
	if report != nil && ctx.Err() == nil {
		report(totalPlatforms, totalPlatforms)
	}
	common.SysLog("任务进度轮询完成")
	return summary
}

// DispatchPlatformUpdate 按平台分发轮询更新
func DispatchPlatformUpdate(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch platform {
	case constant.TaskPlatformMidjourney:
		// MJ 轮询由其自身处理，这里预留入口
	case constant.TaskPlatformSuno:
		_ = UpdateSunoTasks(ctx, taskChannelM, taskM)
	default:
		if err := UpdateVideoTasks(ctx, platform, taskChannelM, taskM); err != nil {
			common.SysLog(fmt.Sprintf("UpdateVideoTasks fail: %s", err))
		}
	}
}

// UpdateSunoTasks 按渠道更新所有 Suno 任务
func UpdateSunoTasks(ctx context.Context, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	for channelId, taskIds := range taskChannelM {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := updateSunoTasks(ctx, channelId, taskIds, taskM)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("渠道 #%d 更新异步任务失败: %s", channelId, err.Error()))
		}
	}
	return nil
}

func updateSunoTasks(ctx context.Context, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("渠道 #%d 未完成的任务有: %d", channelId, len(taskIds)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(taskIds) == 0 {
		return nil
	}
	ch, err := model.CacheGetChannel(channelId)
	if err != nil {
		common.SysLog(fmt.Sprintf("CacheGetChannel: %v", err))
		for _, upstreamID := range taskIds {
			if t, ok := taskM[upstreamID]; ok {
				fromStatus := t.Status
				t.Status = model.TaskStatusFailure
				t.Progress = "100%"
				t.FailReason = fmt.Sprintf(
					"获取渠道信息失败，请联系管理员，渠道ID：%d",
					channelId,
				)
				t.FinishTime = time.Now().Unix()
				if _, transitionErr := ApplyDurableTaskBillingTransition(
					ctx,
					t,
					fromStatus,
					0,
					model.BillingOperationOutcomeRefund,
					t.FailReason,
				); transitionErr != nil {
					logger.LogError(ctx, fmt.Sprintf(
						"fail Suno task after channel lookup error (task=%s): %v",
						t.TaskID,
						transitionErr,
					))
				}
			}
		}
		return err
	}
	adaptor := GetTaskAdaptorFunc(constant.TaskPlatformSuno)
	if adaptor == nil {
		return errors.New("adaptor not found")
	}
	proxy := ch.GetSetting().Proxy
	resp, err := adaptor.FetchTask(*ch.BaseURL, ch.Key, map[string]any{
		"ids": taskIds,
	}, proxy)
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Task Do req error: %v", err))
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logger.LogError(ctx, fmt.Sprintf("Get Task status code: %d", resp.StatusCode))
		return fmt.Errorf("Get Task status code: %d", resp.StatusCode)
	}
	responseBody, err := readServiceResponseBody(resp, sunoTaskPollingResponseMaxBytes)
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Suno Task parse body error: %v", err))
		return err
	}
	var responseItems taskdto.TaskResponse[[]taskdto.SunoDataResponse]
	err = common.Unmarshal(responseBody, &responseItems)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Get Suno Task parse body error2: %v, body: %s", err, string(responseBody)))
		return err
	}
	if !responseItems.IsSuccess() {
		common.SysLog(fmt.Sprintf("渠道 #%d 未完成的任务有: %d, 成功获取到任务数: %s", channelId, len(taskIds), string(responseBody)))
		return err
	}

	for _, responseItem := range responseItems.Data {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		task := taskM[responseItem.TaskID]
		if task == nil {
			logger.LogWarn(ctx, fmt.Sprintf("Suno task response ignored: unknown task_id=%s", responseItem.TaskID))
			continue
		}
		needsUpdate, compareErr := taskNeedsUpdate(task, responseItem)
		if compareErr != nil {
			logger.LogWarn(ctx, fmt.Sprintf(
				"Suno task data comparison failed for task %s: %v",
				task.TaskID,
				compareErr,
			))
		}
		if !needsUpdate {
			continue
		}

		prevStatus := task.Status
		task.Status = lo.If(model.TaskStatus(responseItem.Status) != "", model.TaskStatus(responseItem.Status)).Else(task.Status)
		task.FailReason = lo.If(responseItem.FailReason != "", responseItem.FailReason).Else(task.FailReason)
		task.SubmitTime = lo.If(responseItem.SubmitTime != 0, responseItem.SubmitTime).Else(task.SubmitTime)
		task.StartTime = lo.If(responseItem.StartTime != 0, responseItem.StartTime).Else(task.StartTime)
		task.FinishTime = lo.If(responseItem.FinishTime != 0, responseItem.FinishTime).Else(task.FinishTime)
		isFailure := responseItem.FailReason != "" || task.Status == model.TaskStatusFailure
		if isFailure {
			logger.LogInfo(ctx, task.TaskID+" 构建失败，"+task.FailReason)
			task.Status = model.TaskStatusFailure
			task.Progress = "100%"
		}
		if responseItem.Status == model.TaskStatusSuccess {
			task.Progress = "100%"
		}
		task.Data = responseItem.Data

		if task.Status == model.TaskStatusFailure ||
			task.Status == model.TaskStatusSuccess {
			outcome := model.BillingOperationOutcomeSettle
			requestedQuota := task.Quota
			reason := "Suno task completed"
			if task.Status == model.TaskStatusFailure {
				outcome = model.BillingOperationOutcomeRefund
				requestedQuota = 0
				reason = task.FailReason
			}
			if _, err := ApplyDurableTaskBillingTransition(
				ctx,
				task,
				prevStatus,
				requestedQuota,
				outcome,
				reason,
			); err != nil {
				logger.LogError(ctx, fmt.Sprintf(
					"UpdateSunoTask durable transition for task %s failed: %v",
					task.TaskID,
					err,
				))
			}
			continue
		}

		won, err := task.UpdateWithStatus(prevStatus)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("UpdateSunoTask task %s error: %v", task.TaskID, err))
		} else if !won {
			logger.LogWarn(ctx, fmt.Sprintf("Task %s CAS lost or no-op update", task.TaskID))
		}
	}
	return nil
}

// taskNeedsUpdate 检查 Suno 任务是否需要更新
func taskNeedsUpdate(oldTask *model.Task, newTask taskdto.SunoDataResponse) (bool, error) {
	if oldTask.SubmitTime != newTask.SubmitTime {
		return true, nil
	}
	if oldTask.StartTime != newTask.StartTime {
		return true, nil
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true, nil
	}
	if string(oldTask.Status) != newTask.Status {
		return true, nil
	}
	if oldTask.FailReason != newTask.FailReason {
		return true, nil
	}

	if (oldTask.Status == model.TaskStatusFailure || oldTask.Status == model.TaskStatusSuccess) && oldTask.Progress != "100%" {
		return true, nil
	}

	oldData := oldTask.Data
	if len(bytes.TrimSpace(oldData)) == 0 {
		oldData = []byte("null")
	}
	newData := newTask.Data
	if len(bytes.TrimSpace(newData)) == 0 {
		newData = []byte("null")
	}

	equal, err := common.JSONSemanticEqual(oldData, newData)
	if err != nil {
		return true, err
	}
	return !equal, nil
}

// UpdateVideoTasks 按渠道更新所有视频任务
func UpdateVideoTasks(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	channelIDs := make([]int, 0, len(taskChannelM))
	for channelID := range taskChannelM {
		channelIDs = append(channelIDs, channelID)
	}
	sort.Ints(channelIDs)

	var wg sync.WaitGroup
	for _, channelId := range channelIDs {
		taskIds := taskChannelM[channelId]
		if len(taskIds) == 0 {
			continue
		}
		taskIds = append([]string(nil), taskIds...)

		wg.Add(1)
		gopool.Go(func() {
			defer wg.Done()
			if err := updateVideoTasks(ctx, platform, channelId, taskIds, taskM); err != nil {
				logger.LogError(ctx, fmt.Sprintf("Channel #%d failed to update video async tasks: %s", channelId, err.Error()))
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func updateVideoTasks(ctx context.Context, platform constant.TaskPlatform, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("Channel #%d pending video tasks: %d", channelId, len(taskIds)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(taskIds) == 0 {
		return nil
	}
	cacheGetChannel, err := model.CacheGetChannel(channelId)
	if err != nil {
		for _, upstreamID := range taskIds {
			if t, ok := taskM[upstreamID]; ok {
				fromStatus := t.Status
				t.Status = model.TaskStatusFailure
				t.Progress = "100%"
				t.FailReason = fmt.Sprintf(
					"Failed to get channel info, channel ID: %d",
					channelId,
				)
				t.FinishTime = time.Now().Unix()
				if _, transitionErr := ApplyDurableTaskBillingTransition(
					ctx,
					t,
					fromStatus,
					0,
					model.BillingOperationOutcomeRefund,
					t.FailReason,
				); transitionErr != nil {
					logger.LogError(ctx, fmt.Sprintf(
						"fail video task after channel lookup error (task=%s): %v",
						t.TaskID,
						transitionErr,
					))
				}
			}
		}
		return fmt.Errorf("CacheGetChannel failed: %w", err)
	}
	adaptor := GetTaskAdaptorFunc(platform)
	if adaptor == nil {
		return fmt.Errorf("video adaptor not found")
	}
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelBaseUrl: cacheGetChannel.GetBaseURL(),
	}
	info.ApiKey = cacheGetChannel.Key
	adaptor.Init(info)
	disablePollingSleep := cacheGetChannel.GetOtherSettings().DisableTaskPollingSleep
	for i, taskId := range taskIds {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := updateVideoSingleTask(ctx, adaptor, cacheGetChannel, taskId, taskM); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Failed to update video task %s: %s", taskId, err.Error()))
		}
		if disablePollingSleep || i == len(taskIds)-1 {
			continue
		}

		// sleep 1 second between tasks for this channel only.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return nil
}

func updateVideoSingleTask(ctx context.Context, adaptor TaskPollingAdaptor, ch *model.Channel, taskId string, taskM map[string]*model.Task) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	baseURL := constant.ChannelBaseURLs[ch.Type]
	if ch.GetBaseURL() != "" {
		baseURL = ch.GetBaseURL()
	}
	proxy := ch.GetSetting().Proxy

	task := taskM[taskId]
	if task == nil {
		logger.LogError(ctx, fmt.Sprintf("Task %s not found in taskM", taskId))
		return fmt.Errorf("task %s not found", taskId)
	}
	isGeminiTask := task.IsGeminiTask()
	if isGeminiTask != (ch.Type == constant.ChannelTypeGemini) {
		return newGeminiTaskPollingError(
			task,
			"provider_boundary_mismatch",
			nil,
		)
	}
	key := ch.Key

	privateData := task.PrivateData
	if isGeminiTask ||
		privateData.Key != "" ||
		privateData.ChannelKeyFingerprint != "" {
		resolvedKey, resolveErr := model.ResolveTaskChannelKey(ch, task)
		if resolveErr != nil {
			if isGeminiTask {
				return newGeminiTaskPollingError(
					task,
					"credential_resolution_failed",
					resolveErr,
				)
			}
			return fmt.Errorf(
				"resolve channel credential for task %s: %w",
				taskId,
				resolveErr,
			)
		}
		key = resolvedKey
	}
	resp, err := adaptor.FetchTask(baseURL, key, map[string]any{
		"task_id": task.GetUpstreamTaskID(),
		"action":  task.Action,
	}, proxy)
	if err != nil {
		if isGeminiTask {
			return newGeminiTaskPollingError(
				task,
				"upstream_request_failed",
				err,
			)
		}
		return fmt.Errorf("fetchTask failed for task %s: %w", taskId, err)
	}
	defer resp.Body.Close()
	responseBody, err := readServiceResponseBody(resp, videoTaskPollingResponseMaxBytes)
	if err != nil {
		if isGeminiTask {
			return newGeminiTaskPollingError(
				task,
				"upstream_response_read_failed",
				err,
			)
		}
		return fmt.Errorf("read task response for task %s: %w", taskId, err)
	}

	if isGeminiTask {
		return applyGeminiVideoPollingResult(
			ctx,
			adaptor,
			task,
			key,
			responseBody,
		)
	}

	logger.LogDebug(ctx, "updateVideoSingleTask response: %s", responseBody)

	snap := task.Snapshot()

	taskResult := &relaycommon.TaskInfo{}
	// try parse as New API response format
	var responseItems taskdto.TaskResponse[model.Task]
	if err = common.Unmarshal(responseBody, &responseItems); err == nil && responseItems.IsSuccess() {
		logger.LogDebug(ctx, "updateVideoSingleTask parsed as new api response format: %+v", responseItems)
		t := responseItems.Data
		taskResult.TaskID = t.TaskID
		taskResult.Status = string(t.Status)
		taskResult.Url = t.GetResultURL()
		taskResult.Progress = t.Progress
		taskResult.Reason = t.FailReason
		task.Data = t.Data
	} else if taskResult, err = adaptor.ParseTaskResult(responseBody); err != nil {
		return fmt.Errorf("parseTaskResult failed for task %s: %w", taskId, err)
	}

	task.Data = redactVideoResponseBody(responseBody)

	logger.LogDebug(ctx, "updateVideoSingleTask taskResult: %+v", taskResult)

	now := time.Now().Unix()
	if taskResult.Status == "" {
		//taskResult = relaycommon.FailTaskInfo("upstream returned empty status")
		errorResult := &dto.GeneralErrorResponse{}
		if err = common.Unmarshal(responseBody, &errorResult); err == nil {
			openaiError := errorResult.TryToOpenAIError()
			if openaiError != nil {
				// 返回规范的 OpenAI 错误格式，提取错误信息，判断错误是否为任务失败
				if openaiError.Code == "429" {
					// 429 错误通常表示请求过多或速率限制，暂时不认为是任务失败，保持原状态等待下一轮轮询
					return nil
				}

				// 其他错误认为是任务失败，记录错误信息并更新任务状态
				taskResult = relaycommon.FailTaskInfo("upstream returned error")
			} else {
				// unknown error format, log original response
				logger.LogError(ctx, fmt.Sprintf("Task %s returned empty status with unrecognized error format, response: %s", taskId, string(responseBody)))
				taskResult = relaycommon.FailTaskInfo("upstream returned unrecognized message")
			}
		}
	}

	task.Status = model.TaskStatus(taskResult.Status)
	switch taskResult.Status {
	case model.TaskStatusSubmitted:
		task.Progress = taskcommon.ProgressSubmitted
	case model.TaskStatusQueued:
		task.Progress = taskcommon.ProgressQueued
	case model.TaskStatusInProgress:
		task.Progress = taskcommon.ProgressInProgress
		if task.StartTime == 0 {
			task.StartTime = now
		}
	case model.TaskStatusSuccess:
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		if strings.HasPrefix(taskResult.Url, "data:") {
			// data: URI (e.g. Vertex base64 encoded video) — keep in Data, not in ResultURL
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		} else if taskResult.Url != "" {
			// Direct upstream URL (e.g. Kling, Ali, Doubao, etc.)
			task.PrivateData.ResultURL = taskResult.Url
		} else {
			// No URL from adaptor — construct proxy URL using public task ID
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
	case model.TaskStatusFailure:
		logger.LogJson(ctx, fmt.Sprintf("Task %s failed", taskId), task)
		task.Status = model.TaskStatusFailure
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		task.FailReason = taskResult.Reason
		logger.LogInfo(ctx, fmt.Sprintf("Task %s failed: %s", task.TaskID, task.FailReason))
		taskResult.Progress = taskcommon.ProgressComplete
	default:
		return fmt.Errorf("unknown task status %s for task %s", taskResult.Status, task.TaskID)
	}
	if taskResult.Progress != "" {
		task.Progress = taskResult.Progress
	}

	isDone := task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
	if isDone {
		outcome := model.BillingOperationOutcomeRefund
		requestedQuota := 0
		reason := task.FailReason
		var clamp *common.QuotaClamp
		if task.Status == model.TaskStatusSuccess {
			outcome = model.BillingOperationOutcomeSettle
			requestedQuota, reason, clamp = CalculateTaskBillingOnComplete(
				ctx,
				adaptor,
				task,
				taskResult,
			)
		}
		_, err := ApplyDurableTaskBillingTransition(
			ctx,
			task,
			snap.Status,
			requestedQuota,
			outcome,
			reason,
			clamp,
		)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf(
				"durable terminal update failed for task %s: %s",
				task.TaskID,
				err.Error(),
			))
		}
	} else if !snap.Equal(task.Snapshot()) {
		if _, err := task.UpdateWithStatus(snap.Status); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Failed to update task %s: %s", task.TaskID, err.Error()))
		}
	} else {
		// No changes, skip update
		logger.LogDebug(ctx, "No update needed for task %s", task.TaskID)
	}

	return nil
}

func applyGeminiVideoPollingResult(
	ctx context.Context,
	adaptor TaskPollingAdaptor,
	task *model.Task,
	resolvedKey string,
	responseBody []byte,
) error {
	result, err := geminitaskresult.Sanitize(
		responseBody,
		geminitaskresult.Options{
			Phase:              geminitaskresult.PhasePoll,
			PublicTaskID:       task.TaskID,
			ResolvedCredential: resolvedKey,
			CapturePrivateURI:  true,
		},
	)
	if err != nil {
		return newGeminiTaskPollingError(
			task,
			"invalid_upstream_response",
			err,
		)
	}

	logger.LogDebug(
		ctx,
		"Gemini task poll sanitized: task=%s status=%s retryable=%t code=%d error_status=%s provider_uri_present=%t extra_video_results=%t",
		task.TaskID,
		result.Status,
		result.Retryable,
		result.ErrorCode,
		result.ErrorStatus,
		result.HadProviderURI,
		result.ExtraVideoResults,
	)
	if result.Retryable {
		return nil
	}

	updated := *task
	if _, err := updated.SetProviderResultURI(result.ProviderURI); err != nil {
		return newGeminiTaskPollingError(
			task,
			"result_protection_failed",
			err,
		)
	}

	updated.Data = append(updated.Data[:0:0], result.PublicData...)
	updated.PrivateData.ResultURL = ""
	updated.FailReason = ""

	taskResult := &relaycommon.TaskInfo{
		Code:     result.ErrorCode,
		Status:   result.Status,
		Progress: result.Progress,
	}
	if result.Failed {
		taskResult.Status = model.TaskStatusFailure
		taskResult.Reason = "upstream task failed"
	}

	snapshot := task.Snapshot()
	now := time.Now().Unix()
	updated.Status = model.TaskStatus(taskResult.Status)
	switch updated.Status {
	case model.TaskStatusSubmitted:
		updated.Progress = taskcommon.ProgressSubmitted
	case model.TaskStatusQueued:
		updated.Progress = taskcommon.ProgressQueued
	case model.TaskStatusInProgress:
		updated.Progress = taskcommon.ProgressInProgress
		if updated.StartTime == 0 {
			updated.StartTime = now
		}
	case model.TaskStatusSuccess:
		updated.Progress = taskcommon.ProgressComplete
		if updated.FinishTime == 0 {
			updated.FinishTime = now
		}
		updated.PrivateData.ResultURL =
			taskcommon.BuildProxyURL(updated.TaskID)
	case model.TaskStatusFailure:
		updated.Progress = taskcommon.ProgressComplete
		if updated.FinishTime == 0 {
			updated.FinishTime = now
		}
		updated.FailReason = "upstream task failed"
	default:
		return newGeminiTaskPollingError(
			task,
			"unsupported_upstream_status",
			nil,
		)
	}
	if taskResult.Progress != "" {
		updated.Progress = taskResult.Progress
	}

	isDone := updated.Status == model.TaskStatusSuccess ||
		updated.Status == model.TaskStatusFailure
	if isDone {
		outcome := model.BillingOperationOutcomeRefund
		requestedQuota := 0
		reason := updated.FailReason
		var clamp *common.QuotaClamp
		if updated.Status == model.TaskStatusSuccess {
			outcome = model.BillingOperationOutcomeSettle
			requestedQuota, reason, clamp = CalculateTaskBillingOnComplete(
				ctx,
				adaptor,
				&updated,
				taskResult,
			)
		}
		if _, err := ApplyDurableTaskBillingTransition(
			ctx,
			&updated,
			snapshot.Status,
			requestedQuota,
			outcome,
			reason,
			clamp,
		); err != nil {
			return newGeminiTaskPollingError(
				task,
				"terminal_commit_failed",
				err,
			)
		}
		*task = updated
		return nil
	}

	if snapshot.Equal(updated.Snapshot()) {
		logger.LogDebug(ctx, "No update needed for Gemini task %s", task.TaskID)
		return nil
	}

	changed, err := updated.UpdateWithStatus(snapshot.Status)
	if err != nil {
		return newGeminiTaskPollingError(
			task,
			"progress_commit_failed",
			err,
		)
	}
	if changed {
		*task = updated
	}
	return nil
}

func redactVideoResponseBody(body []byte) []byte {
	var m map[string]any
	if err := common.Unmarshal(body, &m); err != nil {
		return body
	}
	resp, _ := m["response"].(map[string]any)
	if resp != nil {
		delete(resp, "bytesBase64Encoded")
		if v, ok := resp["video"].(string); ok {
			resp["video"] = truncateBase64(v)
		}
		if vs, ok := resp["videos"].([]any); ok {
			for i := range vs {
				if vm, ok := vs[i].(map[string]any); ok {
					delete(vm, "bytesBase64Encoded")
				}
			}
		}
	}
	b, err := common.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

func truncateBase64(s string) string {
	const maxKeep = 256
	if len(s) <= maxKeep {
		return s
	}
	return s[:maxKeep] + "..."
}

// CalculateTaskBillingOnComplete determines the terminal quota without
// mutating billing state. The result is committed with the task status by
// ApplyDurableTaskBillingTransition.
//
// Priority:
//  1. per-call billing keeps the reservation
//  2. adaptor adjustment
//  3. token recalculation
//  4. unchanged reservation
func CalculateTaskBillingOnComplete(
	ctx context.Context,
	adaptor TaskPollingAdaptor,
	task *model.Task,
	taskResult *relaycommon.TaskInfo,
) (int, string, *common.QuotaClamp) {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.PerCallBilling {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 按次计费，跳过差额结算", task.TaskID))
		return task.Quota, "按次计费", nil
	}
	if actualQuota := adaptor.AdjustBillingOnComplete(task, taskResult); actualQuota > 0 {
		return actualQuota, "adaptor计费调整", nil
	}
	if taskResult.TotalTokens > 0 {
		if quota, reason, clamp, ok := calculateTaskQuotaByTokens(
			task,
			taskResult.TotalTokens,
		); ok {
			return quota, reason, clamp
		}
	}
	return task.Quota, "保持预扣额度", nil
}
