package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

const midjourneyImageFetchTimeout = 30 * time.Second

func RelayMidjourneyImage(c *gin.Context) {
	taskId := c.Param("id")
	userId := c.GetInt("id")
	midjourneyTask := model.GetByMJId(userId, taskId)
	if midjourneyTask == nil {
		c.JSON(400, gin.H{
			"error": "midjourney_task_not_found",
		})
		return
	}
	const bytesPerMegabyte int64 = 1024 * 1024
	configuredMB := int64(constant.MaxFileDownloadMB)
	if configuredMB <= 0 || configuredMB > math.MaxInt64/bytesPerMegabyte {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "upstream_image_invalid_size",
		})
		return
	}
	maxBytes := configuredMB * bytesPerMegabyte

	var httpClient *http.Client
	var proxy string
	if channel, err := model.CacheGetChannel(midjourneyTask.ChannelId); err == nil {
		proxy = channel.GetSetting().Proxy
		if proxy != "" {
			if httpClient, err = service.GetSSRFProtectedHTTPClientWithProxy(
				proxy,
			); err != nil {
				c.JSON(400, gin.H{
					"error": "proxy_url_invalid",
				})
				return
			}
		}
	}
	if httpClient == nil {
		httpClient = service.GetSSRFProtectedHTTPClient()
	}
	validateErr := service.ValidateSSRFProtectedFetchURL(midjourneyTask.ImageUrl)
	if validateErr != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"error": fmt.Sprintf("request blocked: %v", validateErr),
		})
		return
	}

	requestContext, cancel := context.WithTimeout(c.Request.Context(), midjourneyImageFetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestContext,
		http.MethodGet,
		midjourneyTask.ImageUrl,
		nil,
	)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "upstream_image_fetch_failed",
		})
		return
	}
	resp, err := httpClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			c.JSON(http.StatusGatewayTimeout, gin.H{
				"error": "upstream_image_fetch_timeout",
			})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "upstream_image_fetch_failed",
		})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "upstream_image_bad_status",
		})
		return
	}
	if resp.ContentLength > maxBytes {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "upstream_image_too_large",
		})
		return
	}
	imageBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			c.JSON(http.StatusGatewayTimeout, gin.H{
				"error": "upstream_image_fetch_timeout",
			})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "upstream_image_read_failed",
		})
		return
	}
	if int64(len(imageBytes)) > maxBytes {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "upstream_image_too_large",
		})
		return
	}

	contentType := http.DetectContentType(imageBytes)
	var extension string
	switch contentType {
	case "image/jpeg":
		extension = "jpg"
	case "image/png":
		extension = "png"
	case "image/gif":
		extension = "gif"
	case "image/webp":
		extension = "webp"
	default:
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "upstream_image_invalid_type",
		})
		return
	}

	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="midjourney-image.%s"`, extension))
	c.Data(http.StatusOK, contentType, imageBytes)
}

func RelayMidjourneyNotify(c *gin.Context) *dto.MidjourneyResponse {
	var midjRequest dto.MidjourneyDto
	err := common.UnmarshalBodyReusable(c, &midjRequest)
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: "bind_request_body_failed",
			Properties:  nil,
			Result:      "",
		}
	}
	midjourneyTask := model.GetByOnlyMJId(midjRequest.MjId)
	if midjourneyTask == nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: "midjourney_task_not_found",
			Properties:  nil,
			Result:      "",
		}
	}
	midjourneyTask.Progress = midjRequest.Progress
	midjourneyTask.PromptEn = midjRequest.PromptEn
	midjourneyTask.State = midjRequest.State
	midjourneyTask.SubmitTime = midjRequest.SubmitTime
	midjourneyTask.StartTime = midjRequest.StartTime
	midjourneyTask.FinishTime = midjRequest.FinishTime
	midjourneyTask.ImageUrl = midjRequest.ImageUrl
	midjourneyTask.VideoUrl = midjRequest.VideoUrl
	videoUrlsStr, _ := json.Marshal(midjRequest.VideoUrls)
	midjourneyTask.VideoUrls = string(videoUrlsStr)
	midjourneyTask.Status = midjRequest.Status
	midjourneyTask.FailReason = midjRequest.FailReason
	err = midjourneyTask.Update()
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: "update_midjourney_task_failed",
		}
	}

	return nil
}

func coverMidjourneyTaskDto(c *gin.Context, originTask *model.Midjourney) (midjourneyTask dto.MidjourneyDto) {
	midjourneyTask.MjId = originTask.MjId
	midjourneyTask.Progress = originTask.Progress
	midjourneyTask.PromptEn = originTask.PromptEn
	midjourneyTask.State = originTask.State
	midjourneyTask.SubmitTime = originTask.SubmitTime
	midjourneyTask.StartTime = originTask.StartTime
	midjourneyTask.FinishTime = originTask.FinishTime
	midjourneyTask.ImageUrl = ""
	if originTask.ImageUrl != "" && setting.MjForwardUrlEnabled {
		midjourneyTask.ImageUrl = system_setting.ServerAddress + "/mj/image/" + originTask.MjId
		if originTask.Status != "SUCCESS" {
			midjourneyTask.ImageUrl += "?rand=" + strconv.FormatInt(time.Now().UnixNano(), 10)
		}
	} else {
		midjourneyTask.ImageUrl = originTask.ImageUrl
	}
	if originTask.VideoUrl != "" {
		midjourneyTask.VideoUrl = originTask.VideoUrl
	}
	midjourneyTask.Status = originTask.Status
	midjourneyTask.FailReason = originTask.FailReason
	midjourneyTask.Action = originTask.Action
	midjourneyTask.Description = originTask.Description
	midjourneyTask.Prompt = originTask.Prompt
	if originTask.Buttons != "" {
		var buttons []dto.ActionButton
		err := json.Unmarshal([]byte(originTask.Buttons), &buttons)
		if err == nil {
			midjourneyTask.Buttons = buttons
		}
	}
	if originTask.VideoUrls != "" {
		var videoUrls []dto.ImgUrls
		err := json.Unmarshal([]byte(originTask.VideoUrls), &videoUrls)
		if err == nil {
			midjourneyTask.VideoUrls = videoUrls
		}
	}
	if originTask.Properties != "" {
		var properties dto.Properties
		err := json.Unmarshal([]byte(originTask.Properties), &properties)
		if err == nil {
			midjourneyTask.Properties = &properties
		}
	}
	return
}

func reserveMidjourneyQuota(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	quota int,
) (*model.MidjourneyQuotaReservation, *dto.MidjourneyResponse) {
	reservation, created, err := model.ReserveMidjourneyQuota(
		model.MidjourneyQuotaReservationRequest{
			RequestId:    info.RequestId,
			UserId:       info.UserId,
			TokenId:      info.TokenId,
			Quota:        quota,
			TokenCharged: !info.IsPlayground,
		},
	)
	if errors.Is(err, model.ErrMidjourneyWalletQuotaInsufficient) ||
		errors.Is(err, model.ErrMidjourneyTokenQuotaInsufficient) {
		return nil, service.MidjourneyErrorWrapper(
			constant.MjRequestError,
			"quota_not_enough",
		)
	}
	if err != nil {
		logger.LogError(c, fmt.Sprintf(
			"Midjourney quota reservation failed request_id=%s user_id=%d token_id=%d quota=%d: %s",
			info.RequestId,
			info.UserId,
			info.TokenId,
			quota,
			err.Error(),
		))
		return nil, service.MidjourneyErrorWrapper(
			constant.MjRequestError,
			"reserve_midjourney_quota_failed",
		)
	}
	if !created {
		return nil, service.MidjourneyErrorWrapper(
			constant.MjRequestError,
			"duplicate_billing_operation",
		)
	}
	return reservation, nil
}

func handleMidjourneyRequestReservationError(
	c *gin.Context,
	reservation *model.MidjourneyQuotaReservation,
	requestErr error,
) *dto.MidjourneyResponse {
	if reservation == nil {
		return nil
	}
	if service.MidjourneyRequestWasDispatched(requestErr) {
		logger.LogWarn(c, fmt.Sprintf(
			"Midjourney upstream outcome is ambiguous; reservation retained request_id=%s reservation_id=%d user_id=%d token_id=%d quota=%d: %s",
			reservation.RequestId,
			reservation.Id,
			reservation.UserId,
			reservation.TokenId,
			reservation.Quota,
			requestErr.Error(),
		))
		return nil
	}

	if _, err := model.RefundMidjourneyQuotaReservation(reservation.Id); err != nil {
		logger.LogError(c, fmt.Sprintf(
			"Midjourney local request failure refund failed request_id=%s reservation_id=%d: %s",
			reservation.RequestId,
			reservation.Id,
			err.Error(),
		))
		return service.MidjourneyErrorWrapper(
			constant.MjRequestError,
			"refund_midjourney_quota_failed",
		)
	}
	return nil
}

func logAcceptedMidjourneyTaskPersistenceFailure(
	c *gin.Context,
	reservation *model.MidjourneyQuotaReservation,
	err error,
) {
	if reservation == nil || err == nil {
		return
	}
	logger.LogError(c, fmt.Sprintf(
		"Midjourney accepted task persistence failed; reservation retained request_id=%s reservation_id=%d user_id=%d token_id=%d quota=%d: %s",
		reservation.RequestId,
		reservation.Id,
		reservation.UserId,
		reservation.TokenId,
		reservation.Quota,
		err.Error(),
	))
}

func RelaySwapFace(c *gin.Context, info *relaycommon.RelayInfo) *dto.MidjourneyResponse {
	var swapFaceRequest dto.SwapFaceRequest
	err := common.UnmarshalBodyReusable(c, &swapFaceRequest)
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "bind_request_body_failed")
	}

	info.InitChannelMeta(c)

	if swapFaceRequest.SourceBase64 == "" || swapFaceRequest.TargetBase64 == "" {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "sour_base64_and_target_base64_is_required")
	}
	modelName := service.CovertMjpActionToModelName(constant.MjActionSwapFace)

	priceData, err := helper.ModelPriceHelperPerCall(c, info)
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: err.Error(),
		}
	}

	var reservation *model.MidjourneyQuotaReservation
	if priceData.Quota > 0 {
		var reservationErr *dto.MidjourneyResponse
		reservation, reservationErr = reserveMidjourneyQuota(c, info, priceData.Quota)
		if reservationErr != nil {
			return reservationErr
		}
	}

	requestURL := getMjRequestPath(c.Request.URL.String())
	baseURL := c.GetString("base_url")
	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)
	mjResp, _, err := service.DoMidjourneyHttpRequest(c, time.Second*60, fullRequestURL)
	if err != nil {
		if reservationErr := handleMidjourneyRequestReservationError(c, reservation, err); reservationErr != nil {
			return reservationErr
		}
		return &mjResp.Response
	}

	midjResponse := &mjResp.Response
	accepted := mjResp.StatusCode == http.StatusOK && midjResponse.Code == 1
	midjourneyTask := &model.Midjourney{
		UserId:      info.UserId,
		Code:        midjResponse.Code,
		Action:      constant.MjActionSwapFace,
		MjId:        midjResponse.Result,
		Prompt:      "InsightFace",
		PromptEn:    "",
		Description: midjResponse.Description,
		State:       "",
		SubmitTime:  info.StartTime.UnixNano() / int64(time.Millisecond),
		StartTime:   time.Now().UnixNano() / int64(time.Millisecond),
		FinishTime:  0,
		ImageUrl:    "",
		Status:      "",
		Progress:    "0%",
		FailReason:  "",
		ChannelId:   c.GetInt("channel_id"),
		Quota:       priceData.Quota,
	}
	outcome := model.MidjourneyBillingOutcomeFailure
	if accepted {
		outcome = model.MidjourneyBillingOutcomePending
	} else {
		midjourneyTask.Status = "FAILURE"
		midjourneyTask.Progress = "100%"
		midjourneyTask.FailReason = midjResponse.Description
	}

	if reservation != nil {
		_, err = model.CreateMidjourneyTaskWithReservation(
			midjourneyTask,
			reservation.Id,
			outcome,
		)
	} else {
		err = midjourneyTask.Insert()
	}
	if err != nil {
		if reservation != nil {
			if accepted {
				logAcceptedMidjourneyTaskPersistenceFailure(c, reservation, err)
			} else {
				if _, refundErr := model.RefundMidjourneyQuotaReservation(reservation.Id); refundErr != nil {
					logger.LogError(c, fmt.Sprintf(
						"Midjourney rejected SwapFace task refund failed request_id=%s reservation_id=%d: %s",
						reservation.RequestId,
						reservation.Id,
						refundErr.Error(),
					))
					return service.MidjourneyErrorWrapper(constant.MjRequestError, "refund_midjourney_quota_failed")
				}
			}
		}
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "insert_midjourney_task_failed")
	}
	if accepted {
		tokenName := c.GetString("token_name")
		logContent := fmt.Sprintf("模型固定价格 %.2f，分组倍率 %.2f，操作 %s", priceData.ModelPrice, priceData.GroupRatioInfo.GroupRatio, constant.MjActionSwapFace)
		other := service.GenerateMjOtherInfo(info, priceData)
		model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
			ChannelId: info.ChannelId,
			ModelName: modelName,
			TokenName: tokenName,
			Quota:     priceData.Quota,
			Content:   logContent,
			TokenId:   info.TokenId,
			Group:     info.UsingGroup,
			Other:     other,
		})
		model.UpdateUserUsedQuotaAndRequestCount(info.UserId, priceData.Quota)
		model.UpdateChannelUsedQuota(info.ChannelId, priceData.Quota)
	}

	c.Writer.WriteHeader(mjResp.StatusCode)
	respBody, err := json.Marshal(midjResponse)
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "unmarshal_response_body_failed")
	}
	_, err = io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "copy_response_body_failed")
	}
	return nil
}

func RelayMidjourneyTaskImageSeed(c *gin.Context) *dto.MidjourneyResponse {
	taskId := c.Param("id")
	userId := c.GetInt("id")
	originTask := model.GetByMJId(userId, taskId)
	if originTask == nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_no_found")
	}
	channel, err := model.GetChannelById(originTask.ChannelId, true)
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "get_channel_info_failed")
	}
	if channel.Status != common.ChannelStatusEnabled {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "该任务所属渠道已被禁用")
	}
	c.Set("channel_id", originTask.ChannelId)
	c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", channel.Key))

	requestURL := getMjRequestPath(c.Request.URL.String())
	fullRequestURL := fmt.Sprintf("%s%s", channel.GetBaseURL(), requestURL)
	midjResponseWithStatus, _, err := service.DoMidjourneyHttpRequest(c, time.Second*30, fullRequestURL)
	if err != nil {
		return &midjResponseWithStatus.Response
	}
	midjResponse := &midjResponseWithStatus.Response
	c.Writer.WriteHeader(midjResponseWithStatus.StatusCode)
	respBody, err := json.Marshal(midjResponse)
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "unmarshal_response_body_failed")
	}
	service.IOCopyBytesGracefully(c, nil, respBody)
	return nil
}

func RelayMidjourneyTask(c *gin.Context, relayMode int) *dto.MidjourneyResponse {
	userId := c.GetInt("id")
	var err error
	var respBody []byte
	switch relayMode {
	case relayconstant.RelayModeMidjourneyTaskFetch:
		taskId := c.Param("id")
		originTask := model.GetByMJId(userId, taskId)
		if originTask == nil {
			return &dto.MidjourneyResponse{
				Code:        4,
				Description: "task_no_found",
			}
		}
		midjourneyTask := coverMidjourneyTaskDto(c, originTask)
		respBody, err = json.Marshal(midjourneyTask)
		if err != nil {
			return &dto.MidjourneyResponse{
				Code:        4,
				Description: "unmarshal_response_body_failed",
			}
		}
	case relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		var condition = struct {
			IDs []string `json:"ids"`
		}{}
		err = c.BindJSON(&condition)
		if err != nil {
			return &dto.MidjourneyResponse{
				Code:        4,
				Description: "do_request_failed",
			}
		}
		var tasks []dto.MidjourneyDto
		if len(condition.IDs) != 0 {
			originTasks := model.GetByMJIds(userId, condition.IDs)
			for _, originTask := range originTasks {
				midjourneyTask := coverMidjourneyTaskDto(c, originTask)
				tasks = append(tasks, midjourneyTask)
			}
		}
		if tasks == nil {
			tasks = make([]dto.MidjourneyDto, 0)
		}
		respBody, err = json.Marshal(tasks)
		if err != nil {
			return &dto.MidjourneyResponse{
				Code:        4,
				Description: "unmarshal_response_body_failed",
			}
		}
	}

	c.Writer.Header().Set("Content-Type", "application/json")

	_, err = io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: "copy_response_body_failed",
		}
	}
	return nil
}

func RelayMidjourneySubmit(c *gin.Context, relayInfo *relaycommon.RelayInfo) *dto.MidjourneyResponse {
	consumeQuota := true
	var midjRequest dto.MidjourneyRequest
	err := common.UnmarshalBodyReusable(c, &midjRequest)
	if err != nil {
		return service.MidjourneyErrorWrapper(constant.MjRequestError, "bind_request_body_failed")
	}

	relayInfo.InitChannelMeta(c)

	if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyAction { // midjourney plus，需要从customId中获取任务信息
		mjErr := service.CoverPlusActionToNormalAction(&midjRequest)
		if mjErr != nil {
			return mjErr
		}
		relayInfo.RelayMode = relayconstant.RelayModeMidjourneyChange
	}
	if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyVideo {
		midjRequest.Action = constant.MjActionVideo
	}

	if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyImagine { //绘画任务，此类任务可重复
		if midjRequest.Prompt == "" {
			return service.MidjourneyErrorWrapper(constant.MjRequestError, "prompt_is_required")
		}
		midjRequest.Action = constant.MjActionImagine
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyDescribe { //按图生文任务，此类任务可重复
		midjRequest.Action = constant.MjActionDescribe
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyEdits { //编辑任务，此类任务可重复
		midjRequest.Action = constant.MjActionEdits
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyShorten { //缩短任务，此类任务可重复，plus only
		midjRequest.Action = constant.MjActionShorten
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyBlend { //绘画任务，此类任务可重复
		midjRequest.Action = constant.MjActionBlend
	} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyUpload { //绘画任务，此类任务可重复
		midjRequest.Action = constant.MjActionUpload
	} else if midjRequest.TaskId != "" { //放大、变换任务，此类任务，如果重复且已有结果，远端api会直接返回最终结果
		mjId := ""
		if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyChange {
			if midjRequest.TaskId == "" {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_id_is_required")
			} else if midjRequest.Action == "" {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "action_is_required")
			} else if midjRequest.Index == 0 {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "index_is_required")
			}
			//action = midjRequest.Action
			mjId = midjRequest.TaskId
		} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneySimpleChange {
			if midjRequest.Content == "" {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "content_is_required")
			}
			params := service.ConvertSimpleChangeParams(midjRequest.Content)
			if params == nil {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "content_parse_failed")
			}
			mjId = params.TaskId
			midjRequest.Action = params.Action
		} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyModal {
			//if midjRequest.MaskBase64 == "" {
			//	return service.MidjourneyErrorWrapper(constant.MjRequestError, "mask_base64_is_required")
			//}
			mjId = midjRequest.TaskId
			midjRequest.Action = constant.MjActionModal
		} else if relayInfo.RelayMode == relayconstant.RelayModeMidjourneyVideo {
			midjRequest.Action = constant.MjActionVideo
			if midjRequest.TaskId == "" {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_id_is_required")
			} else if midjRequest.Action == "" {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "action_is_required")
			}
			mjId = midjRequest.TaskId
		}

		originTask := model.GetByMJId(relayInfo.UserId, mjId)
		if originTask == nil {
			return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_not_found")
		} else { //原任务的Status=SUCCESS，则可以做放大UPSCALE、变换VARIATION等动作，此时必须使用原来的请求地址才能正确处理
			if setting.MjActionCheckSuccessEnabled {
				if originTask.Status != "SUCCESS" && relayInfo.RelayMode != relayconstant.RelayModeMidjourneyModal {
					return service.MidjourneyErrorWrapper(constant.MjRequestError, "task_status_not_success")
				}
			}
			channel, err := model.GetChannelById(originTask.ChannelId, true)
			if err != nil {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "get_channel_info_failed")
			}
			if channel.Status != common.ChannelStatusEnabled {
				return service.MidjourneyErrorWrapper(constant.MjRequestError, "该任务所属渠道已被禁用")
			}
			c.Set("base_url", channel.GetBaseURL())
			c.Set("channel_id", originTask.ChannelId)
			c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", channel.Key))
			logger.LogDebug(c, "Midjourney action uses origin channel: id=%s, base_url=%s", strconv.Itoa(originTask.ChannelId), channel.GetBaseURL())
		}
		midjRequest.Prompt = originTask.Prompt

		//if channelType == common.ChannelTypeMidjourneyPlus {
		//	// plus
		//} else {
		//	// 普通版渠道
		//
		//}
	}

	if midjRequest.Action == constant.MjActionInPaint || midjRequest.Action == constant.MjActionCustomZoom {
		consumeQuota = false
	}

	//baseURL := common.ChannelBaseURLs[channelType]
	requestURL := getMjRequestPath(c.Request.URL.String())

	baseURL := c.GetString("base_url")

	//midjRequest.NotifyHook = "http://127.0.0.1:3000/mj/notify"

	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)

	modelName := service.CovertMjpActionToModelName(midjRequest.Action)

	priceData, err := helper.ModelPriceHelperPerCall(c, relayInfo)
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: err.Error(),
		}
	}

	var reservation *model.MidjourneyQuotaReservation
	if consumeQuota && priceData.Quota > 0 {
		var reservationErr *dto.MidjourneyResponse
		reservation, reservationErr = reserveMidjourneyQuota(c, relayInfo, priceData.Quota)
		if reservationErr != nil {
			return reservationErr
		}
	}

	midjResponseWithStatus, responseBody, err := service.DoMidjourneyHttpRequest(c, time.Second*60, fullRequestURL)
	if err != nil {
		if reservationErr := handleMidjourneyRequestReservationError(c, reservation, err); reservationErr != nil {
			return reservationErr
		}
		return &midjResponseWithStatus.Response
	}
	midjResponse := &midjResponseWithStatus.Response
	accepted := midjResponseWithStatus.StatusCode == http.StatusOK &&
		(midjResponse.Code == 1 || midjResponse.Code == 21 || midjResponse.Code == 22)

	// 文档：https://github.com/novicezk/midjourney-proxy/blob/main/docs/api.md
	//1-提交成功
	// 21-任务已存在（处理中或者有结果了） {"code":21,"description":"任务已存在","result":"0741798445574458","properties":{"status":"SUCCESS","imageUrl":"https://xxxx"}}
	// 22-排队中 {"code":22,"description":"排队中，前面还有1个任务","result":"0741798445574458","properties":{"numberOfQueues":1,"discordInstanceId":"1118138338562560102"}}
	// 23-队列已满，请稍后再试 {"code":23,"description":"队列已满，请稍后尝试","result":"14001929738841620","properties":{"discordInstanceId":"1118138338562560102"}}
	// 24-prompt包含敏感词 {"code":24,"description":"可能包含敏感词","properties":{"promptEn":"nude body","bannedWord":"nude"}}
	// other: 提交错误，description为错误描述
	midjourneyTask := &model.Midjourney{
		UserId:      relayInfo.UserId,
		Code:        midjResponse.Code,
		Action:      midjRequest.Action,
		MjId:        midjResponse.Result,
		Prompt:      midjRequest.Prompt,
		PromptEn:    "",
		Description: midjResponse.Description,
		State:       "",
		SubmitTime:  time.Now().UnixNano() / int64(time.Millisecond),
		StartTime:   0,
		FinishTime:  0,
		ImageUrl:    "",
		Status:      "",
		Progress:    "0%",
		FailReason:  "",
		ChannelId:   c.GetInt("channel_id"),
		Quota:       priceData.Quota,
	}
	if midjResponse.Code == 3 {
		//无实例账号自动禁用渠道（No available account instance）
		channel, err := model.GetChannelById(midjourneyTask.ChannelId, true)
		if err != nil {
			common.SysLog("get_channel_null: " + err.Error())
		}
		if channel.GetAutoBan() && common.AutomaticDisableChannelEnabled {
			model.UpdateChannelStatus(midjourneyTask.ChannelId, "", 2, "No available account instance")
		}
	}
	if !accepted {
		midjourneyTask.FailReason = midjResponse.Description
		midjourneyTask.Status = "FAILURE"
		midjourneyTask.Progress = "100%"
	}

	if midjResponse.Code == 21 { //21-任务已存在（处理中或者有结果了）
		// 将 properties 转换为一个 map
		properties, ok := midjResponse.Properties.(map[string]interface{})
		if ok {
			imageUrl, ok1 := properties["imageUrl"].(string)
			status, ok2 := properties["status"].(string)
			if ok1 && ok2 {
				midjourneyTask.ImageUrl = imageUrl
				midjourneyTask.Status = status
				if status == "SUCCESS" {
					midjourneyTask.Progress = "100%"
					midjourneyTask.StartTime = time.Now().UnixNano() / int64(time.Millisecond)
					midjourneyTask.FinishTime = time.Now().UnixNano() / int64(time.Millisecond)
					midjResponse.Code = 1
				}
			}
		}
		//修改返回值
		if midjRequest.Action != constant.MjActionInPaint && midjRequest.Action != constant.MjActionCustomZoom {
			newBody := strings.Replace(string(responseBody), `"code":21`, `"code":1`, -1)
			responseBody = []byte(newBody)
		}
	}
	if midjResponse.Code == 1 && midjRequest.Action == "UPLOAD" {
		midjourneyTask.Progress = "100%"
		midjourneyTask.Status = "SUCCESS"
	}

	outcome := model.MidjourneyBillingOutcomeFailure
	if accepted {
		outcome = model.MidjourneyBillingOutcomePending
		if midjourneyTask.Progress == "100%" && midjourneyTask.Status == "SUCCESS" {
			outcome = model.MidjourneyBillingOutcomeSuccess
		}
	}
	if reservation != nil {
		_, err = model.CreateMidjourneyTaskWithReservation(
			midjourneyTask,
			reservation.Id,
			outcome,
		)
	} else {
		err = midjourneyTask.Insert()
	}
	if err != nil {
		if reservation != nil {
			if accepted {
				logAcceptedMidjourneyTaskPersistenceFailure(c, reservation, err)
			} else {
				if _, refundErr := model.RefundMidjourneyQuotaReservation(reservation.Id); refundErr != nil {
					logger.LogError(c, fmt.Sprintf(
						"Midjourney rejected task refund failed request_id=%s reservation_id=%d: %s",
						reservation.RequestId,
						reservation.Id,
						refundErr.Error(),
					))
					return service.MidjourneyErrorWrapper(constant.MjRequestError, "refund_midjourney_quota_failed")
				}
			}
		}
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: "insert_midjourney_task_failed",
		}
	}
	if accepted && consumeQuota {
		tokenName := c.GetString("token_name")
		logContent := fmt.Sprintf("模型固定价格 %.2f，分组倍率 %.2f，操作 %s，ID %s", priceData.ModelPrice, priceData.GroupRatioInfo.GroupRatio, midjRequest.Action, midjResponse.Result)
		other := service.GenerateMjOtherInfo(relayInfo, priceData)
		model.RecordConsumeLog(c, relayInfo.UserId, model.RecordConsumeLogParams{
			ChannelId: relayInfo.ChannelId,
			ModelName: modelName,
			TokenName: tokenName,
			Quota:     priceData.Quota,
			Content:   logContent,
			TokenId:   relayInfo.TokenId,
			Group:     relayInfo.UsingGroup,
			Other:     other,
		})
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, priceData.Quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, priceData.Quota)
	}

	if midjResponse.Code == 22 { //22-排队中，说明任务已存在
		//修改返回值
		newBody := strings.Replace(string(responseBody), `"code":22`, `"code":1`, -1)
		responseBody = []byte(newBody)
	}
	//resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))
	bodyReader := io.NopCloser(bytes.NewBuffer(responseBody))

	//for k, v := range resp.Header {
	//	c.Writer.Header().Set(k, v[0])
	//}
	c.Writer.WriteHeader(midjResponseWithStatus.StatusCode)

	_, err = io.Copy(c.Writer, bodyReader)
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: "copy_response_body_failed",
		}
	}
	err = bodyReader.Close()
	if err != nil {
		return &dto.MidjourneyResponse{
			Code:        4,
			Description: "close_response_body_failed",
		}
	}
	return nil
}

type taskChangeParams struct {
	ID     string
	Action string
	Index  int
}

func getMjRequestPath(path string) string {
	requestURL := path
	if strings.Contains(requestURL, "/mj-") {
		urls := strings.Split(requestURL, "/mj/")
		if len(urls) < 2 {
			return requestURL
		}
		requestURL = "/mj/" + urls[1]
	}
	return requestURL
}
