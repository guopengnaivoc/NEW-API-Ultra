package controller

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	"github.com/QuantumNous/new-api/relay"
)

var errGeminiVideoURLUnavailable = errors.New(
	"Gemini video URL is unavailable",
)

func getGeminiVideoURL(channel *model.Channel, task *model.Task, apiKey string) (string, error) {
	if channel == nil ||
		task == nil ||
		task.TaskID == "" ||
		apiKey == "" {
		return "", errGeminiVideoURLUnavailable
	}

	privateURI, err := task.OpenProviderResultURI()
	if err != nil {
		return "", errGeminiVideoURLUnavailable
	}
	if privateURI != "" {
		privateURI, err = geminitaskresult.StripExactCredentialQuery(
			privateURI,
			apiKey,
		)
		if err != nil || !isGeminiVideoResultURIAllowed(privateURI, task.TaskID) {
			return "", errGeminiVideoURLUnavailable
		}
		return privateURI, nil
	}

	sanitizeOptions := geminitaskresult.Options{
		Phase:              geminitaskresult.PhasePoll,
		PublicTaskID:       task.TaskID,
		ResolvedCredential: apiKey,
		CapturePrivateURI:  true,
	}
	if len(task.Data) > 0 {
		sanitized, sanitizeErr := geminitaskresult.Sanitize(
			task.Data,
			sanitizeOptions,
		)
		if sanitizeErr != nil {
			return "", errGeminiVideoURLUnavailable
		}
		if sanitized.ProviderURI != "" {
			if !isGeminiVideoResultURIAllowed(
				sanitized.ProviderURI,
				task.TaskID,
			) {
				return "", errGeminiVideoURLUnavailable
			}
			return sanitized.ProviderURI, nil
		}
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	adaptor := relay.GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(channel.Type)))
	if adaptor == nil {
		return "", errGeminiVideoURLUnavailable
	}

	proxy := channel.GetSetting().Proxy
	resp, err := adaptor.FetchTask(baseURL, apiKey, map[string]any{
		"task_id": task.GetUpstreamTaskID(),
		"action":  task.Action,
	}, proxy)
	if err != nil {
		return "", errGeminiVideoURLUnavailable
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errGeminiVideoURLUnavailable
	}

	sanitized, sanitizeErr := geminitaskresult.Sanitize(
		body,
		sanitizeOptions,
	)
	if sanitizeErr != nil ||
		sanitized.ProviderURI == "" ||
		!isGeminiVideoResultURIAllowed(sanitized.ProviderURI, task.TaskID) {
		return "", errGeminiVideoURLUnavailable
	}
	return sanitized.ProviderURI, nil
}

func isGeminiVideoResultURIAllowed(rawURI string, taskID string) bool {
	uriWithoutFragment := rawURI
	if fragmentIndex := strings.IndexByte(uriWithoutFragment, '#'); fragmentIndex >= 0 {
		uriWithoutFragment = uriWithoutFragment[:fragmentIndex]
	}
	uriBase := uriWithoutFragment
	if queryIndex := strings.IndexByte(uriBase, '?'); queryIndex >= 0 {
		uriBase = uriBase[:queryIndex]
	}

	parsed, err := url.Parse(uriBase)
	if err != nil ||
		parsed.Opaque != "" ||
		parsed.Host == "" ||
		(!strings.EqualFold(parsed.Scheme, "http") &&
			!strings.EqualFold(parsed.Scheme, "https")) {
		return false
	}
	return parsed.EscapedPath() != geminitaskresult.ProxyPath(taskID)
}

func getVertexVideoURL(channel *model.Channel, task *model.Task) (string, error) {
	if channel == nil || task == nil {
		return "", fmt.Errorf("invalid channel or task")
	}
	if url := strings.TrimSpace(task.GetResultURL()); url != "" && !isTaskProxyContentURL(url, task.TaskID) {
		return url, nil
	}
	if url := extractVertexVideoURLFromTaskData(task); url != "" {
		return url, nil
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	adaptor := relay.GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(channel.Type)))
	if adaptor == nil {
		return "", fmt.Errorf("vertex task adaptor not found")
	}

	key, err := model.ResolveTaskChannelKey(channel, task)
	if err != nil {
		return "", fmt.Errorf("vertex key not available for task: %w", err)
	}

	resp, err := adaptor.FetchTask(baseURL, key, map[string]any{
		"task_id": task.GetUpstreamTaskID(),
		"action":  task.Action,
	}, channel.GetSetting().Proxy)
	if err != nil {
		return "", fmt.Errorf("fetch task failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read task response failed: %w", err)
	}

	taskInfo, parseErr := adaptor.ParseTaskResult(body)
	if parseErr == nil && taskInfo != nil && strings.TrimSpace(taskInfo.Url) != "" {
		return taskInfo.Url, nil
	}
	if url := extractVertexVideoURLFromPayload(body); url != "" {
		return url, nil
	}
	if parseErr != nil {
		return "", fmt.Errorf("parse task result failed: %w", parseErr)
	}
	return "", fmt.Errorf("vertex video url not found")
}

func isTaskProxyContentURL(url string, taskID string) bool {
	if strings.TrimSpace(url) == "" || strings.TrimSpace(taskID) == "" {
		return false
	}
	return strings.Contains(url, "/v1/videos/"+taskID+"/content")
}

func extractVertexVideoURLFromTaskData(task *model.Task) string {
	if task == nil || len(task.Data) == 0 {
		return ""
	}
	return extractVertexVideoURLFromPayload(task.Data)
}

func extractVertexVideoURLFromPayload(body []byte) string {
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return ""
	}
	resp, ok := payload["response"].(map[string]any)
	if !ok || resp == nil {
		return ""
	}

	if videos, ok := resp["videos"].([]any); ok && len(videos) > 0 {
		if video, ok := videos[0].(map[string]any); ok && video != nil {
			if b64, _ := video["bytesBase64Encoded"].(string); strings.TrimSpace(b64) != "" {
				mime, _ := video["mimeType"].(string)
				enc, _ := video["encoding"].(string)
				return buildVideoDataURL(mime, enc, b64)
			}
		}
	}
	if b64, _ := resp["bytesBase64Encoded"].(string); strings.TrimSpace(b64) != "" {
		enc, _ := resp["encoding"].(string)
		return buildVideoDataURL("", enc, b64)
	}
	if video, _ := resp["video"].(string); strings.TrimSpace(video) != "" {
		if strings.HasPrefix(video, "data:") || strings.HasPrefix(video, "http://") || strings.HasPrefix(video, "https://") {
			return video
		}
		enc, _ := resp["encoding"].(string)
		return buildVideoDataURL("", enc, video)
	}
	return ""
}

func buildVideoDataURL(mimeType string, encoding string, base64Data string) string {
	mime := strings.TrimSpace(mimeType)
	if mime == "" {
		enc := strings.TrimSpace(encoding)
		if enc == "" {
			enc = "mp4"
		}
		if strings.Contains(enc, "/") {
			mime = enc
		} else {
			mime = "video/" + enc
		}
	}
	return "data:" + mime + ";base64," + base64Data
}
