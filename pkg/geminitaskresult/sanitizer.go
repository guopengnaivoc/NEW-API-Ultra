package geminitaskresult

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const MaxProviderResultURIBytes = 16 << 10

var safeProviderErrorStatus = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

type Phase uint8

const (
	PhaseSubmit Phase = iota + 1
	PhasePoll
	PhasePublicRead
	PhaseBackfill
)

type Options struct {
	Phase              Phase
	PublicTaskID       string
	ResolvedCredential string
	CapturePrivateURI  bool
}

type Result struct {
	PublicData        []byte
	OperationName     string
	ProviderURI       string
	Status            string
	Progress          string
	Done              bool
	Retryable         bool
	Failed            bool
	ErrorCode         int
	ErrorStatus       string
	VideoMIMEType     string
	HadProviderURI    bool
	ExtraVideoResults bool
}

type rawTaskResult struct {
	Name     string          `json:"name"`
	Done     *bool           `json:"done"`
	URI      string          `json:"uri"`
	MIMEType string          `json:"mimeType"`
	Video    json.RawMessage `json:"video"`
	Response rawTaskResponse `json:"response"`
	Error    rawTaskError    `json:"error"`
}

type rawTaskResponse struct {
	URI                   string                   `json:"uri"`
	MIMEType              string                   `json:"mimeType"`
	Video                 json.RawMessage          `json:"video"`
	Videos                []rawVideoCandidate      `json:"videos"`
	GenerateVideoResponse rawGenerateVideoResponse `json:"generateVideoResponse"`
}

type rawGenerateVideoResponse struct {
	GeneratedSamples []rawGeneratedVideo `json:"generatedSamples"`
	GeneratedVideos  []rawGeneratedVideo `json:"generatedVideos"`
}

type rawGeneratedVideo struct {
	Video rawVideoCandidate `json:"video"`
}

type rawVideoCandidate struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType"`
}

type rawTaskError struct {
	Code    int    `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type publicProjection struct {
	Done  bool         `json:"done"`
	Video *publicVideo `json:"video,omitempty"`
	Error *publicError `json:"error,omitempty"`
}

type publicVideo struct {
	URL      string `json:"url"`
	MIMEType string `json:"mime_type,omitempty"`
}

type publicError struct {
	Code   int    `json:"code,omitempty"`
	Status string `json:"status,omitempty"`
}

type providerURIComponents struct {
	BaseURI  string
	RawQuery string
	Fragment string
	HasQuery bool
}

func EmptyPublicProjection(done bool) []byte {
	data, err := common.Marshal(publicProjection{Done: done})
	if err != nil {
		return nil
	}
	return data
}

func ProxyPath(publicTaskID string) string {
	return "/v1/videos/" + url.PathEscape(publicTaskID) + "/content"
}

func StripExactCredentialQuery(rawURI string, resolvedCredential string) (string, error) {
	if resolvedCredential == "" {
		return "", errors.New("Gemini task result credential is unavailable")
	}
	components, err := parseProviderResultURI(rawURI)
	if err != nil {
		return "", err
	}

	if !components.HasQuery {
		return rawURI, nil
	}

	rawFields := strings.Split(components.RawQuery, "&")
	filteredFields := make([]string, 0, len(rawFields))
	for _, rawField := range rawFields {
		rawName := rawField
		rawValue := ""
		if equalsIndex := strings.IndexByte(rawField, '='); equalsIndex >= 0 {
			rawName = rawField[:equalsIndex]
			rawValue = rawField[equalsIndex+1:]
		}

		name, nameErr := url.QueryUnescape(rawName)
		value, valueErr := url.QueryUnescape(rawValue)
		if nameErr == nil && valueErr == nil && name == "key" && value == resolvedCredential {
			continue
		}
		filteredFields = append(filteredFields, rawField)
	}

	filteredURI := components.BaseURI
	if len(filteredFields) > 0 {
		filteredURI += "?" + strings.Join(filteredFields, "&")
	} else if components.RawQuery == "" {
		filteredURI += "?"
	}
	filteredURI += components.Fragment
	if len(filteredURI) > MaxProviderResultURIBytes {
		return "", errors.New("Gemini task result URI is invalid")
	}
	return filteredURI, nil
}

func Sanitize(raw []byte, options Options) (Result, error) {
	result := Result{
		PublicData: EmptyPublicProjection(false),
		Status:     "IN_PROGRESS",
		Progress:   "50%",
	}

	var envelope rawTaskResult
	if err := common.Unmarshal(raw, &envelope); err != nil {
		return result, newSanitizerError(options.Phase, "malformed response")
	}

	canonicalVideo, legacyTopVideo, err := parseTopLevelVideo(envelope.Video)
	if err != nil {
		return result, newSanitizerError(options.Phase, "invalid video field")
	}
	if canonicalVideo != nil {
		if options.PublicTaskID == "" || canonicalVideo.URL != ProxyPath(options.PublicTaskID) {
			return result, newSanitizerError(options.Phase, "invalid public projection")
		}
		canonicalVideo.MIMEType = validateVideoMIMEType(canonicalVideo.MIMEType)
	}

	candidates := make([]rawVideoCandidate, 0, 8)
	for _, generated := range envelope.Response.GenerateVideoResponse.GeneratedSamples {
		if generated.Video.URI != "" {
			candidates = append(candidates, generated.Video)
		}
	}
	for _, generated := range envelope.Response.GenerateVideoResponse.GeneratedVideos {
		if generated.Video.URI != "" {
			candidates = append(candidates, generated.Video)
		}
	}
	for _, candidate := range envelope.Response.Videos {
		if candidate.URI != "" {
			candidates = append(candidates, candidate)
		}
	}
	if responseVideo, parseErr := parseLegacyVideo(envelope.Response.Video, envelope.Response.MIMEType); parseErr != nil {
		return result, newSanitizerError(options.Phase, "invalid response video")
	} else if responseVideo.URI != "" {
		candidates = append(candidates, responseVideo)
	}
	if envelope.Response.URI != "" {
		candidates = append(candidates, rawVideoCandidate{
			URI:      envelope.Response.URI,
			MIMEType: envelope.Response.MIMEType,
		})
	}
	if envelope.URI != "" {
		candidates = append(candidates, rawVideoCandidate{
			URI:      envelope.URI,
			MIMEType: envelope.MIMEType,
		})
	}
	if legacyTopVideo.URI != "" {
		candidates = append(candidates, legacyTopVideo)
	}

	errorPresent := envelope.Error.Code != 0 ||
		envelope.Error.Status != "" ||
		envelope.Error.Message != ""
	validShape := envelope.Name != "" ||
		envelope.Done != nil ||
		errorPresent ||
		canonicalVideo != nil ||
		len(candidates) > 0
	if !validShape {
		return result, newSanitizerError(options.Phase, "unrecognized response")
	}

	result.OperationName = envelope.Name
	result.HadProviderURI = len(candidates) > 0
	result.ExtraVideoResults = len(candidates) > 1

	var publicVideoValue *publicVideo
	if canonicalVideo != nil {
		publicVideoValue = canonicalVideo
	}
	if len(candidates) > 0 {
		if options.PublicTaskID == "" {
			return result, newSanitizerError(options.Phase, "missing public task identifier")
		}
		if _, parseErr := parseProviderResultURI(candidates[0].URI); parseErr != nil {
			return result, newSanitizerError(options.Phase, "unsafe provider URI")
		}
		if options.CapturePrivateURI {
			filteredURI, filterErr := StripExactCredentialQuery(
				candidates[0].URI,
				options.ResolvedCredential,
			)
			if filterErr != nil {
				return result, newSanitizerError(options.Phase, "provider URI filtering failed")
			}
			result.ProviderURI = filteredURI
		}
		result.VideoMIMEType = validateVideoMIMEType(candidates[0].MIMEType)
		publicVideoValue = &publicVideo{
			URL:      ProxyPath(options.PublicTaskID),
			MIMEType: result.VideoMIMEType,
		}
	} else if publicVideoValue != nil {
		result.VideoMIMEType = publicVideoValue.MIMEType
	}

	result.Done = envelope.Done != nil && *envelope.Done
	if len(candidates) > 0 {
		result.Done = true
	}
	if errorPresent {
		result.Done = true
		result.Failed = true
		result.Status = "FAILURE"
		result.Progress = "100%"
	} else if result.Done {
		result.Status = "SUCCESS"
		result.Progress = "100%"
	}

	var publicErrorValue *publicError
	if errorPresent {
		if envelope.Error.Code > 0 && envelope.Error.Code <= 999999 {
			result.ErrorCode = envelope.Error.Code
		}
		if safeProviderErrorStatus.MatchString(envelope.Error.Status) {
			result.ErrorStatus = envelope.Error.Status
		}
		result.Retryable = result.ErrorCode == 429
		if result.ErrorCode != 0 || result.ErrorStatus != "" {
			publicErrorValue = &publicError{
				Code:   result.ErrorCode,
				Status: result.ErrorStatus,
			}
		}
	}

	publicData, err := common.Marshal(publicProjection{
		Done:  result.Done,
		Video: publicVideoValue,
		Error: publicErrorValue,
	})
	if err != nil {
		result.PublicData = EmptyPublicProjection(false)
		return result, newSanitizerError(options.Phase, "public projection failed")
	}
	result.PublicData = publicData
	return result, nil
}

func parseProviderResultURI(rawURI string) (providerURIComponents, error) {
	if rawURI == "" || len(rawURI) > MaxProviderResultURIBytes {
		return providerURIComponents{}, errors.New("Gemini task result URI is invalid")
	}

	components := providerURIComponents{}
	uriWithoutFragment := rawURI
	if fragmentIndex := strings.IndexByte(rawURI, '#'); fragmentIndex >= 0 {
		uriWithoutFragment = rawURI[:fragmentIndex]
		components.Fragment = rawURI[fragmentIndex:]
	}

	components.BaseURI = uriWithoutFragment
	if queryIndex := strings.IndexByte(uriWithoutFragment, '?'); queryIndex >= 0 {
		components.BaseURI = uriWithoutFragment[:queryIndex]
		components.RawQuery = uriWithoutFragment[queryIndex+1:]
		components.HasQuery = true
	}

	parsedBase, err := url.Parse(components.BaseURI)
	if err != nil ||
		parsedBase.Opaque != "" ||
		parsedBase.Host == "" ||
		(!strings.EqualFold(parsedBase.Scheme, "http") &&
			!strings.EqualFold(parsedBase.Scheme, "https")) {
		return providerURIComponents{}, errors.New("Gemini task result URI is invalid")
	}
	return components, nil
}

func parseTopLevelVideo(raw json.RawMessage) (*publicVideo, rawVideoCandidate, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, rawVideoCandidate{}, nil
	}

	var legacyURI string
	if err := common.Unmarshal(raw, &legacyURI); err == nil {
		return nil, rawVideoCandidate{URI: legacyURI}, nil
	}

	var projection publicVideo
	if err := common.Unmarshal(raw, &projection); err != nil {
		return nil, rawVideoCandidate{}, err
	}
	if projection.URL == "" && projection.MIMEType == "" {
		return nil, rawVideoCandidate{}, nil
	}
	return &projection, rawVideoCandidate{}, nil
}

func parseLegacyVideo(raw json.RawMessage, mimeType string) (rawVideoCandidate, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return rawVideoCandidate{}, nil
	}

	var uri string
	if err := common.Unmarshal(raw, &uri); err == nil {
		return rawVideoCandidate{URI: uri, MIMEType: mimeType}, nil
	}

	var candidate rawVideoCandidate
	if err := common.Unmarshal(raw, &candidate); err != nil {
		return rawVideoCandidate{}, err
	}
	return candidate, nil
}

func validateVideoMIMEType(raw string) string {
	if raw == "" || len(raw) > 128 {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "video/") {
		return ""
	}
	return strings.ToLower(mediaType)
}

func newSanitizerError(phase Phase, category string) error {
	return fmt.Errorf("Gemini task result phase %d: %s", phase, category)
}
