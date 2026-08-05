package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// LogTaskConsumption 记录任务消费日志和统计信息（仅记录，不涉及实际扣费）。
// 实际扣费已由 BillingSession（PreConsumeBilling + SettleBilling）完成。
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo) {
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("操作 %s", info.Action)
	// 支持任务仅按次计费
	if common.StringsContains(constant.TaskPricePatches, info.OriginModelName) {
		logContent = fmt.Sprintf("%s，按次计费", logContent)
	} else {
		if otherRatios := info.PriceData.OtherRatios(); len(otherRatios) > 0 {
			var contents []string
			for key, ra := range otherRatios {
				if 1.0 != ra {
					contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
				}
			}
			if len(contents) > 0 {
				logContent = fmt.Sprintf("%s, 计算参数：%s", logContent, strings.Join(contents, ", "))
			}
		}
	}
	other := make(map[string]interface{})
	other["is_task"] = true
	other["request_path"] = c.Request.URL.Path
	other["model_price"] = info.PriceData.ModelPrice
	if info.PriceData.ModelRatio > 0 {
		other["model_ratio"] = info.PriceData.ModelRatio
	}
	other["group_ratio"] = info.PriceData.GroupRatioInfo.GroupRatio
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = info.PriceData.GroupRatioInfo.GroupSpecialRatio
	}
	if info.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = info.UpstreamModelName
	}
	AttachQuotaSaturation(c, info, other)
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId,
		ModelName: info.OriginModelName,
		TokenName: tokenName,
		Quota:     info.PriceData.Quota,
		Content:   logContent,
		TokenId:   info.TokenId,
		Group:     info.UsingGroup,
		Other:     other,
	})
	model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
	model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)
}

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// ApplyDurableTaskBillingTransition commits the terminal task state and its
// billing outcome in one database transaction, then emits post-commit logs and
// aggregate updates only for the winning transition.
func ApplyDurableTaskBillingTransition(
	ctx context.Context,
	task *model.Task,
	fromStatus model.TaskStatus,
	requestedQuota int,
	outcome model.BillingOperationOutcome,
	reason string,
	clamps ...*common.QuotaClamp,
) (*model.TaskBillingTransitionResult, error) {
	result, err := model.ApplyTaskBillingTransition(
		task,
		fromStatus,
		requestedQuota,
		outcome,
	)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf(
			"durable task billing transition failed (task=%s, from=%s, to=%s): %s",
			task.TaskID,
			fromStatus,
			task.Status,
			err.Error(),
		))
		return nil, err
	}
	if !result.Changed {
		return result, nil
	}
	if result.LegacyNoRefund {
		logger.LogWarn(ctx, fmt.Sprintf(
			"legacy task failed without refund (task=%s, submitted_at=%d)",
			task.TaskID,
			task.SubmitTime,
		))
		return result, nil
	}
	if !result.BillingChanged {
		return result, nil
	}
	if result.Abandoned {
		logger.LogWarn(ctx, fmt.Sprintf(
			"billing operation abandoned (task=%s, operation_id=%d, reason=%s)",
			task.TaskID,
			result.OperationId,
			result.FailureReason,
		))
		return result, nil
	}
	if result.Delta == 0 && !result.SettlementLimited {
		return result, nil
	}

	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["billing_operation_id"] = result.OperationId
	other["pre_consumed_quota"] = result.PreviousQuota
	other["requested_quota"] = result.RequestedQuota
	other["actual_quota"] = result.FinalQuota
	other["reason"] = reason
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}
	if result.SettlementLimited {
		clamp := &common.QuotaClamp{
			Op:       "BillingOperationSettle",
			Kind:     common.QuotaClampCapacity,
			Original: float64(result.RequestedQuota),
			Clamped:  result.FinalQuota,
		}
		attachQuotaSaturationToOther(other, clamp)
		logger.LogWarn(ctx, fmt.Sprintf(
			"quota saturation on task settlement: op=%s kind=%s original=%g clamped=%d task=%s operation_id=%d",
			clamp.Op,
			clamp.Kind,
			clamp.Original,
			clamp.Clamped,
			task.TaskID,
			result.OperationId,
		))
	}

	logType := model.LogTypeConsume
	logQuota := result.Delta
	if result.Delta < 0 {
		logType = model.LogTypeRefund
		logQuota = -result.Delta
	}
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   logType,
		Content:   reason,
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     logQuota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
		NodeName:  task.PrivateData.NodeName,
	})
	if result.Delta > 0 {
		model.UpdateUserUsedQuotaAndRequestCount(task.UserId, result.Delta)
		model.UpdateChannelUsedQuota(task.ChannelId, result.Delta)
	}
	return result, nil
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) map[string]interface{} {
	other := make(map[string]interface{})
	if bc := task.PrivateData.BillingContext; bc != nil {
		other["model_price"] = bc.ModelPrice
		if bc.ModelRatio > 0 {
			other["model_ratio"] = bc.ModelRatio
		}
		other["group_ratio"] = bc.GroupRatio
		if priceData := taskBillingContextPriceData(bc); priceData != nil {
			for k, v := range priceData.OtherRatios() {
				other[k] = v
			}
		}
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = props.UpstreamModelName
	}
	return other
}

func taskBillingContextPriceData(bc *model.TaskBillingContext) *types.PriceData {
	if bc == nil || len(bc.OtherRatios) == 0 {
		return nil
	}
	priceData := &types.PriceData{}
	if !priceData.ReplaceOtherRatios(bc.OtherRatios) {
		return nil
	}
	return priceData
}

// taskModelName 从 BillingContext 或 Properties 中获取模型名称。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

// RefundTaskQuota is a compatibility wrapper for callers that already have a
// persisted task. It now delegates the task state and refund to the durable
// transaction instead of mutating funding and token quota separately.
func RefundTaskQuota(
	ctx context.Context,
	task *model.Task,
	reason string,
) bool {
	if task == nil || task.ID <= 0 {
		return task != nil && task.Quota == 0
	}
	fromStatus := task.Status
	if task.Status != model.TaskStatusFailure {
		task.Status = model.TaskStatusFailure
		task.Progress = "100%"
		task.FailReason = reason
	}
	if _, err := ApplyDurableTaskBillingTransition(
		ctx,
		task,
		fromStatus,
		0,
		model.BillingOperationOutcomeRefund,
		reason,
	); err != nil {
		task.Status = fromStatus
		return false
	}
	return true
}

// RecalculateTaskQuota is a compatibility wrapper that commits a successful
// terminal task and its settlement atomically.
func RecalculateTaskQuota(
	ctx context.Context,
	task *model.Task,
	actualQuota int,
	reason string,
	clamps ...*common.QuotaClamp,
) {
	if task == nil || task.ID <= 0 || actualQuota <= 0 {
		return
	}
	fromStatus := task.Status
	task.Status = model.TaskStatusSuccess
	task.Progress = "100%"
	if _, err := ApplyDurableTaskBillingTransition(
		ctx,
		task,
		fromStatus,
		actualQuota,
		model.BillingOperationOutcomeSettle,
		reason,
		clamps...,
	); err != nil {
		task.Status = fromStatus
	}
}

// RecalculateTaskQuotaByTokens is the durable compatibility entry point for
// token-based task settlement.
func RecalculateTaskQuotaByTokens(
	ctx context.Context,
	task *model.Task,
	totalTokens int,
) {
	actualQuota, reason, clamp, ok := calculateTaskQuotaByTokens(task, totalTokens)
	if !ok {
		return
	}
	RecalculateTaskQuota(ctx, task, actualQuota, reason, clamp)
}

func calculateTaskQuotaByTokens(
	task *model.Task,
	totalTokens int,
) (int, string, *common.QuotaClamp, bool) {
	if totalTokens <= 0 {
		return 0, "", nil, false
	}

	modelName := taskModelName(task)

	// 获取模型价格和倍率
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
	// 只有配置了倍率(非固定价格)时才按 token 重新计费
	if !hasRatioSetting || modelRatio <= 0 {
		return 0, "", nil, false
	}

	// 获取用户和组的倍率信息
	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, false)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return 0, "", nil, false
	}

	groupRatio := ratio_setting.GetGroupRatio(group)
	userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)

	var finalGroupRatio float64
	if hasUserGroupRatio {
		finalGroupRatio = userGroupRatio
	} else {
		finalGroupRatio = groupRatio
	}

	// 计算 OtherRatios 乘积（视频折扣、时长等）
	otherMultiplier := 1.0
	if priceData := taskBillingContextPriceData(task.PrivateData.BillingContext); priceData != nil {
		otherMultiplier = priceData.OtherRatioMultiplier()
	}

	// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio * otherMultiplier（饱和转换，防止溢出成负数）
	actualQuota, clamp := common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)

	reason := fmt.Sprintf("token重算：tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f", totalTokens, modelRatio, finalGroupRatio, otherMultiplier)
	return actualQuota, reason, clamp, true
}
