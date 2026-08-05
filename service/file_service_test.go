package service

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fileSourceFailureRoundTripper struct{}

func (fileSourceFailureRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport failed with TOPSECRETTRANSPORT")
}

type imageReadFailureBody struct{}

func (imageReadFailureBody) Read([]byte) (int, error) {
	return 0, errors.New("image read failed with TOPSECRETREADCAUSE")
}

func (imageReadFailureBody) Close() error {
	return nil
}

type imageReadFailureRoundTripper struct{}

func (imageReadFailureRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       imageReadFailureBody{},
		Header:     http.Header{"Content-Type": []string{"image/png"}},
	}, nil
}

type staticImageResponseRoundTripper struct {
	response *http.Response
}

func (t staticImageResponseRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return t.response, nil
}

type unsafeIdentifierFileSource struct {
	mu sync.Mutex
}

func (*unsafeIdentifierFileSource) IsURL() bool {
	return false
}

func (*unsafeIdentifierFileSource) GetIdentifier() string {
	return "TOPSECRETIDENTIFIER"
}

func (*unsafeIdentifierFileSource) GetRawData() string {
	return "TOPSECRETRAWDATA"
}

func (*unsafeIdentifierFileSource) ClearRawData() {}

func (*unsafeIdentifierFileSource) SetCache(*types.CachedFileData) {}

func (*unsafeIdentifierFileSource) GetCache() *types.CachedFileData {
	return nil
}

func (*unsafeIdentifierFileSource) HasCache() bool {
	return false
}

func (*unsafeIdentifierFileSource) ClearCache() {}

func (*unsafeIdentifierFileSource) IsRegistered() bool {
	return false
}

func (*unsafeIdentifierFileSource) SetRegistered(bool) {}

func (s *unsafeIdentifierFileSource) Mu() *sync.Mutex {
	return &s.mu
}

func configureFileSourceFailureClient(t *testing.T) {
	t.Helper()

	originalFetchSetting, err := config.ConfigToMap(system_setting.GetFetchSetting())
	require.NoError(t, err)
	originalHTTPClient := httpClient
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("fetch_setting", originalFetchSetting)
		require.NoError(t, updateErr)
		require.True(t, updated)
		httpClient = originalHTTPClient
	})

	updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection": "false",
	})
	require.NoError(t, err)
	require.True(t, updated)
	httpClient = &http.Client{Transport: fileSourceFailureRoundTripper{}}
}

func TestLoadFileSourceDoesNotAliasEqualPrefixBase64Data(t *testing.T) {
	prefix := bytes.Repeat([]byte("A"), 120)
	firstRaw := append(append([]byte{}, prefix...), []byte("first-tail")...)
	secondRaw := append(append([]byte{}, prefix...), []byte("secondtail")...)
	firstBase64 := base64.StdEncoding.EncodeToString(firstRaw)
	secondBase64 := base64.StdEncoding.EncodeToString(secondRaw)
	require.Equal(t, len(firstBase64), len(secondBase64))
	require.Equal(t, firstBase64[:128], secondBase64[:128])

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	t.Cleanup(func() { CleanupFileSources(context) })

	firstSource := types.NewBase64FileSource(firstBase64, "application/octet-stream")
	secondSource := types.NewBase64FileSource(secondBase64, "application/octet-stream")
	firstCached, err := LoadFileSource(context, firstSource)
	require.NoError(t, err)
	secondCached, err := LoadFileSource(context, secondSource)
	require.NoError(t, err)

	firstResult, err := firstCached.GetBase64Data()
	require.NoError(t, err)
	secondResult, err := secondCached.GetBase64Data()
	require.NoError(t, err)
	assert.Equal(t, firstBase64, firstResult)
	assert.Equal(t, secondBase64, secondResult)
	assert.NotSame(t, firstCached, secondCached)
}

func TestLoadFileSourceDebugLogDoesNotExposeUnsafeURLAuthority(t *testing.T) {
	withDebugEnabled(t, true)

	var logBuffer bytes.Buffer
	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	source := types.NewURLFileSource(
		"https://storage.example.com／private／TOPSECRET",
	)
	source.SetCache(types.NewMemoryCachedData("", "image/png", 0))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	t.Cleanup(func() { CleanupFileSources(context) })

	_, err := LoadFileSource(context, source)
	require.NoError(t, err)

	logOutput := strings.ToLower(logBuffer.String())
	assert.Contains(t, logOutput, "[debug]")
	assert.Contains(t, logOutput, "loadfilesource starting for:")
	assert.NotContains(t, logOutput, "storage.example.com")
	assert.NotContains(t, logOutput, "private")
	assert.NotContains(t, logOutput, "topsecret")
	assert.NotContains(t, logOutput, "／")
}

func TestLoadFileSourceErrorsRemainSafeAcrossCountTokenRepresentations(t *testing.T) {
	configureFileSourceFailureClient(t)

	var logBuffer bytes.Buffer
	common.LogWriterMu.Lock()
	originalLogWriter := gin.DefaultWriter
	gin.DefaultWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = originalLogWriter
		common.LogWriterMu.Unlock()
	})

	tests := []struct {
		name             string
		url              string
		expectedCategory types.FileSourceErrorCategory
		forbidden        []string
	}{
		{
			name:             "malformed escape",
			url:              "https://storage.example.com/private/%zz?token=TOPSECRETTOKEN",
			expectedCategory: types.FileSourceErrorCategoryDownloadFailed,
			forbidden: []string{
				"storage.example.com",
				"private",
				"topsecrettoken",
				"%zz",
			},
		},
		{
			name:             "unicode delimiter",
			url:              "https://storage.example.com／private／TOPSECRETUNICODE",
			expectedCategory: types.FileSourceErrorCategoryDownloadFailed,
			forbidden: []string{
				"storage.example.com",
				"private",
				"topsecretunicode",
				"／",
			},
		},
		{
			name:             "ipv6 zone",
			url:              "https://[fe80::1%25TOPSECRETZONE]/private",
			expectedCategory: types.FileSourceErrorCategoryDownloadFailed,
			forbidden: []string{
				"fe80",
				"topsecretzone",
				"private",
				"%25",
			},
		},
		{
			name:             "transport failure",
			url:              "https://storage.example.com/private/TOPSECRETPATH?token=TOPSECRETQUERY",
			expectedCategory: types.FileSourceErrorCategoryDownloadFailed,
			forbidden: []string{
				"private",
				"topsecretpath",
				"topsecretquery",
				"topsecrettransport",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			t.Cleanup(func() { CleanupFileSources(context) })

			_, err := LoadFileSource(context, types.NewURLFileSource(test.url), "token_counter")
			require.Error(t, err)
			var fileSourceError *types.FileSourceError
			require.ErrorAs(t, err, &fileSourceError)
			assert.Equal(t, test.expectedCategory, fileSourceError.Category())

			countTokenError := types.NewError(err, types.ErrorCodeCountTokenFailed)
			messages := map[string]string{
				"generic": countTokenError.Error(),
				"masked":  countTokenError.MaskSensitiveErrorWithStatusCode(),
				"OpenAI":  countTokenError.ToOpenAIError().Message,
				"Claude":  countTokenError.ToClaudeError().Message,
			}
			for representation, message := range messages {
				t.Run(representation, func(t *testing.T) {
					lowerMessage := strings.ToLower(message)
					assert.Contains(t, lowerMessage, "file source")
					for _, forbidden := range test.forbidden {
						assert.NotContains(t, lowerMessage, forbidden)
					}
				})
			}
		})
	}

	logOutput := strings.ToLower(logBuffer.String())
	assert.Contains(t, logOutput, "downloading from origin: url:")
	for _, forbidden := range []string{
		"private",
		"topsecrettoken",
		"topsecretunicode",
		"topsecretzone",
		"topsecretpath",
		"topsecretquery",
		"topsecrettransport",
		"%zz",
		"%25",
		"／",
	} {
		assert.NotContains(t, logOutput, forbidden)
	}
}

func TestFileSourceCacheReadErrorsDoNotExposeDiskPaths(t *testing.T) {
	tests := []struct {
		name string
		run  func(*gin.Context, *types.URLSource) error
	}{
		{
			name: "base64 accessor",
			run: func(context *gin.Context, source *types.URLSource) error {
				_, _, err := GetBase64Data(context, source)
				return err
			},
		},
		{
			name: "image config accessor",
			run: func(context *gin.Context, source *types.URLSource) error {
				_, _, err := GetImageConfig(context, source)
				return err
			},
		},
		{
			name: "legacy URL accessor",
			run: func(context *gin.Context, source *types.URLSource) error {
				context.Set(getContextCacheKey(source.URL), source.GetCache())
				_, err := GetFileBase64FromUrl(context, source.URL)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			secretDiskPath := t.TempDir() + "/private-TOPSECRETDISK"
			source := types.NewURLFileSource(
				"https://storage.example.com/private/TOPSECRETPATH?token=TOPSECRETQUERY",
			)
			source.SetCache(types.NewDiskCachedData(secretDiskPath, "image/png", 1))

			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			t.Cleanup(func() { CleanupFileSources(context) })

			err := test.run(context, source)
			require.Error(t, err)
			var fileSourceError *types.FileSourceError
			require.ErrorAs(t, err, &fileSourceError)
			assert.Equal(t, types.FileSourceErrorCategoryCacheReadFailed, fileSourceError.Category())

			countTokenError := types.NewError(err, types.ErrorCodeCountTokenFailed)
			for representation, message := range map[string]string{
				"generic": countTokenError.Error(),
				"masked":  countTokenError.MaskSensitiveErrorWithStatusCode(),
				"OpenAI":  countTokenError.ToOpenAIError().Message,
				"Claude":  countTokenError.ToClaudeError().Message,
			} {
				t.Run(representation, func(t *testing.T) {
					lowerMessage := strings.ToLower(message)
					assert.Contains(t, lowerMessage, "file source cache read failed")
					assert.NotContains(t, lowerMessage, "private")
					assert.NotContains(t, lowerMessage, "topsecretdisk")
					assert.NotContains(t, lowerMessage, "topsecretpath")
					assert.NotContains(t, lowerMessage, "topsecretquery")
				})
			}
		})
	}
}

func TestWorkerDownloadErrorsDoNotExposeRawFileSources(t *testing.T) {
	configureFileSourceFailureClient(t)

	originalWorkerURL := system_setting.WorkerUrl
	originalWorkerKey := system_setting.WorkerValidKey
	originalAllowHTTP := system_setting.WorkerAllowHttpImageRequestEnabled
	system_setting.WorkerUrl = "https://worker.example.com"
	system_setting.WorkerValidKey = "TOPSECRETWORKERKEY"
	system_setting.WorkerAllowHttpImageRequestEnabled = false
	t.Cleanup(func() {
		system_setting.WorkerUrl = originalWorkerURL
		system_setting.WorkerValidKey = originalWorkerKey
		system_setting.WorkerAllowHttpImageRequestEnabled = originalAllowHTTP
	})

	var logBuffer bytes.Buffer
	common.LogWriterMu.Lock()
	originalLogWriter := gin.DefaultWriter
	gin.DefaultWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = originalLogWriter
		common.LogWriterMu.Unlock()
	})

	rawURL := "https://storage.example.com／private／TOPSECRETUNICODE"
	_, err := DoDownloadRequest(rawURL, "token_counter")
	require.Error(t, err)
	var fileSourceError *types.FileSourceError
	require.ErrorAs(t, err, &fileSourceError)
	assert.Equal(t, types.FileSourceErrorCategoryDownloadFailed, fileSourceError.Category())

	countTokenError := types.NewError(err, types.ErrorCodeCountTokenFailed)
	messages := []string{
		countTokenError.Error(),
		countTokenError.MaskSensitiveErrorWithStatusCode(),
		countTokenError.ToOpenAIError().Message,
		countTokenError.ToClaudeError().Message,
	}
	for _, message := range messages {
		lowerMessage := strings.ToLower(message)
		assert.Contains(t, lowerMessage, "file source")
		assert.NotContains(t, lowerMessage, "storage.example.com")
		assert.NotContains(t, lowerMessage, "private")
		assert.NotContains(t, lowerMessage, "topsecretunicode")
		assert.NotContains(t, lowerMessage, "topsecrettransport")
		assert.NotContains(t, lowerMessage, "topsecretworkerkey")
		assert.NotContains(t, lowerMessage, "／")
	}

	logOutput := strings.ToLower(logBuffer.String())
	assert.Contains(t, logOutput, "downloading file from worker: url:")
	assert.NotContains(t, logOutput, "storage.example.com")
	assert.NotContains(t, logOutput, "private")
	assert.NotContains(t, logOutput, "topsecretunicode")
	assert.NotContains(t, logOutput, "topsecrettransport")
	assert.NotContains(t, logOutput, "topsecretworkerkey")
	assert.NotContains(t, logOutput, "／")
}

func TestEstimateRequestTokenDoesNotReinvokeUnsafeFileIdentifiers(t *testing.T) {
	configureFileSourceFailureClient(t)

	originalCountToken := constant.CountToken
	originalGetMediaToken := constant.GetMediaToken
	originalGetMediaTokenNotStream := constant.GetMediaTokenNotStream
	constant.CountToken = true
	constant.GetMediaToken = true
	constant.GetMediaTokenNotStream = true
	t.Cleanup(func() {
		constant.CountToken = originalCountToken
		constant.GetMediaToken = originalGetMediaToken
		constant.GetMediaTokenNotStream = originalGetMediaTokenNotStream
	})

	var nilBase64Source *types.Base64Source
	tests := []struct {
		name             string
		source           types.FileSource
		expectedCategory types.FileSourceErrorCategory
		forbidden        []string
	}{
		{
			name:             "typed nil",
			source:           nilBase64Source,
			expectedCategory: types.FileSourceErrorCategoryInvalidSource,
		},
		{
			name:             "custom source",
			source:           &unsafeIdentifierFileSource{},
			expectedCategory: types.FileSourceErrorCategoryUnsupported,
			forbidden:        []string{"topsecretidentifier", "topsecretrawdata"},
		},
		{
			name: "URL transport failure",
			source: types.NewURLFileSource(
				"https://storage.example.com/private/TOPSECRETPATH?token=TOPSECRETQUERY",
			),
			expectedCategory: types.FileSourceErrorCategoryDownloadFailed,
			forbidden: []string{
				"private",
				"topsecretpath",
				"topsecretquery",
				"topsecrettransport",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			common.SetContextKey(context, constant.ContextKeyOriginalModel, "gpt-4o")

			meta := &types.TokenCountMeta{
				Files: []*types.FileMeta{
					types.NewImageFileMeta(test.source, "high"),
				},
			}
			info := &relaycommon.RelayInfo{
				IsStream:    true,
				RelayFormat: types.RelayFormatOpenAI,
			}

			var countErr error
			require.NotPanics(t, func() {
				_, countErr = EstimateRequestToken(context, meta, info)
			})
			require.Error(t, countErr)
			var fileSourceError *types.FileSourceError
			require.ErrorAs(t, countErr, &fileSourceError)
			assert.Equal(t, test.expectedCategory, fileSourceError.Category())

			newAPIError := types.NewError(countErr, types.ErrorCodeCountTokenFailed)
			for representation, message := range map[string]string{
				"generic": newAPIError.Error(),
				"masked":  newAPIError.MaskSensitiveErrorWithStatusCode(),
				"OpenAI":  newAPIError.ToOpenAIError().Message,
				"Claude":  newAPIError.ToClaudeError().Message,
			} {
				t.Run(representation, func(t *testing.T) {
					lowerMessage := strings.ToLower(message)
					assert.Contains(t, lowerMessage, "file source")
					for _, forbidden := range test.forbidden {
						assert.NotContains(t, lowerMessage, forbidden)
					}
				})
			}
		})
	}
}

func TestGetImageFromURLReturnsOnlySafeTypedFailures(t *testing.T) {
	configureFileSourceFailureClient(t)
	originalMaxFileDownloadMB := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	t.Cleanup(func() {
		constant.MaxFileDownloadMB = originalMaxFileDownloadMB
	})

	tests := []struct {
		name             string
		transport        http.RoundTripper
		expectedCategory types.FileSourceErrorCategory
		forbidden        []string
	}{
		{
			name:             "transport failure",
			transport:        fileSourceFailureRoundTripper{},
			expectedCategory: types.FileSourceErrorCategoryDownloadFailed,
			forbidden:        []string{"topsecrettransport"},
		},
		{
			name:             "body read failure",
			transport:        imageReadFailureRoundTripper{},
			expectedCategory: types.FileSourceErrorCategoryReadFailed,
			forbidden:        []string{"topsecretreadcause"},
		},
		{
			name: "unexpected status",
			transport: staticImageResponseRoundTripper{response: &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}},
			expectedCategory: types.FileSourceErrorCategoryUnexpectedStatus,
		},
		{
			name: "invalid content type",
			transport: staticImageResponseRoundTripper{response: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header: http.Header{
					"Content-Type": []string{"text/plain; token=TOPSECRETCONTENTTYPE"},
				},
			}},
			expectedCategory: types.FileSourceErrorCategoryInvalidContent,
			forbidden:        []string{"topsecretcontenttype"},
		},
		{
			name: "oversized content length",
			transport: staticImageResponseRoundTripper{response: &http.Response{
				StatusCode:    http.StatusOK,
				Body:          io.NopCloser(strings.NewReader("")),
				Header:        http.Header{"Content-Type": []string{"image/png"}},
				ContentLength: int64(constant.MaxFileDownloadMB*1024*1024 + 1),
			}},
			expectedCategory: types.FileSourceErrorCategoryTooLarge,
		},
		{
			name: "read reaches size limit",
			transport: staticImageResponseRoundTripper{response: &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader(
					bytes.Repeat([]byte("A"), constant.MaxFileDownloadMB*1024*1024),
				)),
				Header: http.Header{"Content-Type": []string{"image/png"}},
			}},
			expectedCategory: types.FileSourceErrorCategoryTooLarge,
		},
		{
			name: "invalid octet stream image",
			transport: staticImageResponseRoundTripper{response: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("TOPSECRETIMAGEPAYLOAD")),
				Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
			}},
			expectedCategory: types.FileSourceErrorCategoryInvalidContent,
			forbidden:        []string{"topsecretimagepayload"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpClient = &http.Client{Transport: test.transport}
			rawURL := "https://storage.example.com/private/TOPSECRETPATH?X-Amz-Signature=TOPSECRETSIGNATURE"

			_, _, err := GetImageFromUrl(rawURL)
			require.Error(t, err)
			var fileSourceError *types.FileSourceError
			require.ErrorAs(t, err, &fileSourceError)
			assert.Same(t, fileSourceError, err)
			assert.Equal(t, test.expectedCategory, fileSourceError.Category())

			newAPIError := types.NewError(err, types.ErrorCodeBadResponse)
			for representation, message := range map[string]string{
				"generic": newAPIError.Error(),
				"masked":  newAPIError.MaskSensitiveErrorWithStatusCode(),
				"OpenAI":  newAPIError.ToOpenAIError().Message,
				"Claude":  newAPIError.ToClaudeError().Message,
			} {
				t.Run(representation, func(t *testing.T) {
					lowerMessage := strings.ToLower(message)
					assert.Contains(t, lowerMessage, "file source")
					assert.NotContains(t, lowerMessage, "private")
					assert.NotContains(t, lowerMessage, "topsecretpath")
					assert.NotContains(t, lowerMessage, "topsecretsignature")
					for _, forbidden := range test.forbidden {
						assert.NotContains(t, lowerMessage, forbidden)
					}
				})
			}
		})
	}
}
