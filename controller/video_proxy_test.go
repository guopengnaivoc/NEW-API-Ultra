package controller

import (
	"bytes"
	stdcontext "context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	geminiVideoProxyTestUserID = 42026
	geminiVideoProxyTestAPIKey = "gemini-video-proxy-secret"
)

type geminiVideoProxyFixture struct {
	taskID string
	userID int
}

func setupGeminiVideoProxyFixture(t *testing.T, videoURL string) geminiVideoProxyFixture {
	return setupVideoProxyFixture(
		t,
		constant.ChannelTypeGemini,
		videoURL,
	)
}

func setupVideoProxyFixture(
	t *testing.T,
	channelType int,
	videoURL string,
) geminiVideoProxyFixture {
	t.Helper()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	previousMemoryCache := common.MemoryCacheEnabled
	previousFetchSetting, err := config.ConfigToMap(system_setting.GetFetchSetting())
	var db *gorm.DB

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.MemoryCacheEnabled = previousMemoryCache
		if previousFetchSetting != nil {
			restored, restoreErr := config.GlobalConfig.Update(
				"fetch_setting",
				previousFetchSetting,
			)
			require.NoError(t, restoreErr)
			require.True(t, restored)
		}
		if db != nil {
			sqlDB, dbErr := db.DB()
			if dbErr == nil {
				require.NoError(t, sqlDB.Close())
			}
		}
	})
	require.NoError(t, err)

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled = false
	updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection": "false",
	})
	require.NoError(t, err)
	require.True(t, updated)

	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Task{}))

	channel := model.Channel{
		Type:   channelType,
		Key:    geminiVideoProxyTestAPIKey,
		Name:   "gemini-video-proxy-test",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, db.Create(&channel).Error)

	task := model.Task{
		TaskID:    "task_gemini_video_proxy",
		UserId:    geminiVideoProxyTestUserID,
		ChannelId: channel.Id,
		Status:    model.TaskStatusSuccess,
		Action:    "generate",
	}
	if channelType == constant.ChannelTypeGemini {
		task.SetData(map[string]any{"uri": videoURL})
		_, err = task.SetProviderResultURI(videoURL)
		require.NoError(t, err)
	} else {
		task.PrivateData.ResultURL = videoURL
	}
	require.NoError(t, db.Create(&task).Error)

	return geminiVideoProxyFixture{
		taskID: task.TaskID,
		userID: task.UserId,
	}
}

func runGeminiVideoProxy(t *testing.T, fixture geminiVideoProxyFixture) *httptest.ResponseRecorder {
	return runGeminiVideoProxyWithHeaders(t, fixture, nil)
}

func runGeminiVideoProxyWithHeaders(
	t *testing.T,
	fixture geminiVideoProxyFixture,
	headers http.Header,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/videos/"+fixture.taskID+"/content",
		nil,
	)
	if headers == nil {
		headers = make(http.Header)
	}
	request.Header = headers.Clone()
	context.Request = request.WithContext(stdcontext.WithValue(
		request.Context(),
		common.RequestIdKey,
		"gemini-video-proxy-test-request",
	))
	context.Params = gin.Params{{Key: "task_id", Value: fixture.taskID}}
	context.Set("id", fixture.userID)

	VideoProxy(context)

	return recorder
}

func captureVideoProxyLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultWriter = &output
	gin.DefaultErrorWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		gin.DefaultErrorWriter = previousErrorWriter
		common.LogWriterMu.Unlock()
	})
	return &output
}

func setGeminiVideoProxyChannelProxy(t *testing.T, rawProxyURL string) {
	t.Helper()
	var channel model.Channel
	require.NoError(
		t,
		model.DB.Where("name = ?", "gemini-video-proxy-test").First(&channel).Error,
	)
	channel.SetSetting(dto.ChannelSettings{Proxy: rawProxyURL})
	require.NoError(t, model.DB.Save(&channel).Error)
}

func TestSetupGeminiVideoProxyFixturePreservesServiceHTTPClient(t *testing.T) {
	clientBeforeSetup := service.GetHttpClient()

	fixture := setupGeminiVideoProxyFixture(t, "https://video.example/content")

	assert.NotEmpty(t, fixture.taskID)
	assert.Same(t, clientBeforeSetup, service.GetHttpClient())
}

func TestVideoProxyRejectsUnpinnedForwardProxy(t *testing.T) {
	requestCount := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		requestCount++
	}))
	t.Cleanup(proxy.Close)
	fixture := setupGeminiVideoProxyFixture(
		t,
		"http://8.8.8.8/content?key="+
			url.QueryEscape(geminiVideoProxyTestAPIKey),
	)
	setGeminiVideoProxyChannelProxy(t, proxy.URL)
	updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection":     "true",
		"allow_private_ip":           "false",
		"domain_filter_mode":         "false",
		"ip_filter_mode":             "false",
		"domain_list":                "null",
		"ip_list":                    "null",
		"allowed_ports":              `["80","443"]`,
		"apply_ip_filter_for_domain": "true",
	})
	require.NoError(t, err)
	require.True(t, updated)

	response := runGeminiVideoProxy(t, fixture)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), "Failed to create proxy client")
	assert.Equal(t, 0, requestCount)
}

func TestVideoProxyGeminiCredentialTransportBoundary(t *testing.T) {
	t.Run("initial request uses header and key-free URI", func(t *testing.T) {
		type observation struct {
			header     string
			requestURI string
		}
		observed := make(chan observation, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			observed <- observation{
				header:     r.Header.Get("x-goog-api-key"),
				requestURI: r.RequestURI,
			}
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("X-Upstream-Video", "preserved")
			_, _ = io.WriteString(w, "video-body")
		}))
		t.Cleanup(server.Close)

		fixture := setupGeminiVideoProxyFixture(
			t,
			server.URL+"/content?key="+
				url.QueryEscape(geminiVideoProxyTestAPIKey)+
				"&key=another-key&sig=A%2Bb",
		)
		response := runGeminiVideoProxy(t, fixture)
		request := <-observed

		assert.Equal(t, geminiVideoProxyTestAPIKey, request.header)
		assert.Equal(t, "/content?key=another-key&sig=A%2Bb", request.requestURI)
		assert.NotContains(t, request.requestURI, geminiVideoProxyTestAPIKey)
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "video-body", response.Body.String())
		assert.Equal(t, "video/mp4", response.Header().Get("Content-Type"))
		assert.Empty(t, response.Header().Get("X-Upstream-Video"))
		assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	})

	t.Run("same-origin redirect retains header", func(t *testing.T) {
		received := make(chan string, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/start":
				http.Redirect(w, r, "/content", http.StatusFound)
			case "/content":
				received <- r.Header.Get("x-goog-api-key")
				_, _ = io.WriteString(w, "same-origin")
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)

		fixture := setupGeminiVideoProxyFixture(
			t,
			server.URL+"/start?key="+url.QueryEscape(geminiVideoProxyTestAPIKey),
		)
		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "same-origin", response.Body.String())
		assert.Equal(t, geminiVideoProxyTestAPIKey, <-received)
	})

	t.Run("cross-origin redirect removes header", func(t *testing.T) {
		received := make(chan string, 2)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			received <- r.Header.Get("x-goog-api-key")
			if r.URL.Path == "/content" {
				http.Redirect(w, r, "/final", http.StatusFound)
				return
			}
			_, _ = io.WriteString(w, "cross-origin")
		}))
		t.Cleanup(target.Close)
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/content", http.StatusFound)
		}))
		t.Cleanup(source.Close)

		fixture := setupGeminiVideoProxyFixture(
			t,
			source.URL+"/start?key="+url.QueryEscape(geminiVideoProxyTestAPIKey),
		)
		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "cross-origin", response.Body.String())
		assert.Empty(t, <-received)
		assert.Empty(t, <-received)
	})

	t.Run("cross-origin hop keeps header removed after returning to origin", func(t *testing.T) {
		received := make(chan string, 2)
		var targetURL string
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/start" {
				http.Redirect(w, r, targetURL+"/middle", http.StatusFound)
				return
			}
			received <- r.Header.Get("x-goog-api-key")
			_, _ = io.WriteString(w, "returned-to-origin")
		}))
		t.Cleanup(source.Close)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			received <- r.Header.Get("x-goog-api-key")
			http.Redirect(w, r, source.URL+"/final", http.StatusFound)
		}))
		t.Cleanup(target.Close)
		targetURL = target.URL

		fixture := setupGeminiVideoProxyFixture(
			t,
			source.URL+"/start?key="+url.QueryEscape(geminiVideoProxyTestAPIKey),
		)
		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "returned-to-origin", response.Body.String())
		assert.Empty(t, <-received)
		assert.Empty(t, <-received)
	})
}

func TestVideoProxyGeminiOwnerBoundary(t *testing.T) {
	tests := []struct {
		name   string
		userID int
	}{
		{name: "owner id remains required", userID: 0},
		{name: "another user receives not found", userID: geminiVideoProxyTestUserID + 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var targetRequests int
			target := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					targetRequests++
					_, _ = io.WriteString(w, "must-not-be-fetched")
				},
			))
			t.Cleanup(target.Close)
			fixture := setupGeminiVideoProxyFixture(
				t,
				target.URL+"/content?key="+
					url.QueryEscape(geminiVideoProxyTestAPIKey),
			)
			fixture.userID = test.userID

			response := runGeminiVideoProxy(t, fixture)

			assert.Equal(t, http.StatusNotFound, response.Code)
			assert.Contains(t, response.Body.String(), "Task not found")
			assert.Zero(t, targetRequests)
		})
	}

	t.Run("success status remains required", func(t *testing.T) {
		var targetRequests int
		target := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				targetRequests++
				_, _ = io.WriteString(w, "must-not-be-fetched")
			},
		))
		t.Cleanup(target.Close)
		fixture := setupGeminiVideoProxyFixture(t, target.URL+"/content")
		require.NoError(
			t,
			model.DB.Model(&model.Task{}).
				Where("task_id = ?", fixture.taskID).
				UpdateColumn("status", model.TaskStatusInProgress).Error,
		)

		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Contains(t, response.Body.String(), "Task is not completed yet")
		assert.Zero(t, targetRequests)
	})
}

func TestVideoProxyGeminiUsesExactResolvedCredential(t *testing.T) {
	const (
		exactKey   = "fingerprint-selected-gemini-key"
		unusedKey  = "unused-gemini-key"
		queryOther = "unrelated-query-key"
	)
	type observation struct {
		header     string
		requestURI string
	}
	observed := make(chan observation, 1)
	target := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			observed <- observation{
				header:     r.Header.Get(geminiVideoAPIKeyHeader),
				requestURI: r.RequestURI,
			}
			_, _ = io.WriteString(w, "exact-credential-content")
		},
	))
	t.Cleanup(target.Close)
	privateURI := target.URL + "/content?key=" + url.QueryEscape(exactKey) +
		"&key=" + url.QueryEscape(queryOther) + "&sig=A%2Bb"
	fixture := setupGeminiVideoProxyFixture(t, privateURI)

	var channel model.Channel
	require.NoError(
		t,
		model.DB.Where("name = ?", "gemini-video-proxy-test").
			First(&channel).Error,
	)
	channel.Key = unusedKey + "\n" + exactKey
	channel.ChannelInfo.IsMultiKey = true
	channel.ChannelInfo.MultiKeySize = 2
	require.NoError(t, model.DB.Save(&channel).Error)

	var task model.Task
	require.NoError(t, model.DB.Where("task_id = ?", fixture.taskID).First(&task).Error)
	fingerprint := sha256.Sum256([]byte(exactKey))
	task.PrivateData.ChannelKeyFingerprint = fmt.Sprintf("%x", fingerprint)
	task.SetData(map[string]any{"uri": privateURI})
	_, err := task.SetProviderResultURI(privateURI)
	require.NoError(t, err)
	require.NoError(t, model.DB.Save(&task).Error)

	response := runGeminiVideoProxy(t, fixture)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "exact-credential-content", response.Body.String())
	select {
	case request := <-observed:
		assert.Equal(t, exactKey, request.header)
		assert.Equal(
			t,
			"/content?key="+url.QueryEscape(queryOther)+"&sig=A%2Bb",
			request.requestURI,
		)
		assert.NotContains(t, request.requestURI, exactKey)
		assert.NotContains(t, request.requestURI, unusedKey)
	default:
		t.Fatal("content target was not requested")
	}
}

func TestVideoProxyGeminiRepollUsesFingerprintResolvedCredential(t *testing.T) {
	const (
		selectedKey  = "repoll-fingerprint-selected-key"
		unusedKey    = "repoll-fingerprint-unused-key"
		unrelatedKey = "repoll-unrelated-query-key"
	)
	type observation struct {
		header     string
		requestURI string
	}
	contentRequests := make(chan observation, 1)
	contentTarget := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			contentRequests <- observation{
				header:     r.Header.Get(geminiVideoAPIKeyHeader),
				requestURI: r.RequestURI,
			}
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = io.WriteString(w, "fingerprint-repoll-content")
		},
	))
	t.Cleanup(contentTarget.Close)

	pollRequests := make(chan observation, 1)
	pollServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			pollRequests <- observation{
				header:     r.Header.Get(geminiVideoAPIKeyHeader),
				requestURI: r.RequestURI,
			}
			_, _ = io.WriteString(
				w,
				`{"done":true,"response":{"generateVideoResponse":{`+
					`"generatedVideos":[{"video":{"uri":"`+
					contentTarget.URL+`/content?key=`+
					url.QueryEscape(selectedKey)+`&key=`+
					url.QueryEscape(unrelatedKey)+`&sig=A%2Bb"}}]}}}`,
			)
		},
	))
	t.Cleanup(pollServer.Close)

	fixture := setupGeminiVideoProxyFixture(
		t,
		contentTarget.URL+"/discarded-initial-result",
	)
	var channel model.Channel
	require.NoError(
		t,
		model.DB.Where("name = ?", "gemini-video-proxy-test").
			First(&channel).Error,
	)
	channel.Key = unusedKey + "\n" + selectedKey
	channel.ChannelInfo.IsMultiKey = true
	channel.ChannelInfo.MultiKeySize = 2
	channel.BaseURL = &pollServer.URL
	require.NoError(t, model.DB.Save(&channel).Error)

	var task model.Task
	require.NoError(t, model.DB.Where("task_id = ?", fixture.taskID).First(&task).Error)
	fingerprint := sha256.Sum256([]byte(selectedKey))
	task.PrivateData.ChannelKeyFingerprint = fmt.Sprintf("%x", fingerprint)
	task.PrivateData.UpstreamTaskID = taskcommon.EncodeLocalTaskID(
		"models/veo-3.1/operations/fingerprint-repoll",
	)
	task.ClearProviderResultURI()
	task.SetData(map[string]any{
		"done": false,
		"video": map[string]any{
			"url": "/v1/videos/" + fixture.taskID + "/content",
		},
	})
	require.NoError(t, model.DB.Save(&task).Error)

	response := runGeminiVideoProxy(t, fixture)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "fingerprint-repoll-content", response.Body.String())
	select {
	case request := <-pollRequests:
		assert.Equal(t, selectedKey, request.header)
		assert.NotContains(t, request.header, unusedKey)
		assert.NotContains(t, request.header, "\n")
		assert.NotContains(t, request.requestURI, selectedKey)
		assert.NotContains(t, request.requestURI, unusedKey)
	default:
		t.Fatal("fingerprint-resolved poll target was not requested")
	}
	select {
	case request := <-contentRequests:
		assert.Equal(t, selectedKey, request.header)
		assert.Equal(
			t,
			"/content?key="+url.QueryEscape(unrelatedKey)+"&sig=A%2Bb",
			request.requestURI,
		)
		assert.NotContains(t, request.requestURI, selectedKey)
		assert.NotContains(t, request.requestURI, unusedKey)
	default:
		t.Fatal("sanitized generatedVideos content target was not requested")
	}
}

func TestVideoProxyGeminiRejectsUnavailableCredentialBeforeResultAccess(
	t *testing.T,
) {
	tests := []struct {
		name        string
		fingerprint string
	}{
		{name: "ambiguous legacy task"},
		{
			name: "removed fingerprint",
			fingerprint: fmt.Sprintf(
				"%x",
				sha256.Sum256([]byte("removed-gemini-key")),
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var contentRequests int
			target := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					contentRequests++
					_, _ = io.WriteString(w, "must-not-be-fetched")
				},
			))
			t.Cleanup(target.Close)
			fixture := setupGeminiVideoProxyFixture(
				t,
				target.URL+"/credential-access-sentinel",
			)

			var channel model.Channel
			require.NoError(
				t,
				model.DB.Where("name = ?", "gemini-video-proxy-test").
					First(&channel).Error,
			)
			channel.Key = "configured-key-a\nconfigured-key-b"
			channel.ChannelInfo.IsMultiKey = true
			channel.ChannelInfo.MultiKeySize = 2
			require.NoError(t, model.DB.Save(&channel).Error)

			var task model.Task
			require.NoError(
				t,
				model.DB.Where("task_id = ?", fixture.taskID).First(&task).Error,
			)
			task.PrivateData.ChannelKeyFingerprint = test.fingerprint
			corruptEnvelope := "provider-result-envelope-must-not-be-opened"
			task.EncryptedProviderResultURI = &corruptEnvelope
			require.NoError(t, model.DB.Save(&task).Error)

			response := runGeminiVideoProxy(t, fixture)

			assert.Equal(t, http.StatusInternalServerError, response.Code)
			assert.Contains(
				t,
				response.Body.String(),
				"API key not available for task",
			)
			assert.NotContains(
				t,
				response.Body.String(),
				"provider-result-envelope-must-not-be-opened",
			)
			assert.Zero(t, contentRequests)
		})
	}
}

func TestVideoProxyGeminiResolutionFailureSurfacesArePrivate(t *testing.T) {
	assertSafe := func(
		t *testing.T,
		logs string,
		response *httptest.ResponseRecorder,
		forbidden ...string,
	) {
		t.Helper()
		surface := logs + "\n" + response.Body.String()
		assert.NotContains(t, surface, geminiVideoProxyTestAPIKey)
		for _, value := range forbidden {
			assert.NotContains(t, surface, value)
		}
	}

	t.Run("corrupt encrypted result has no fallback", func(t *testing.T) {
		logs := captureVideoProxyLogs(t)
		var legacyRequests int
		legacyTarget := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				legacyRequests++
				_, _ = io.WriteString(w, "legacy-content")
			},
		))
		t.Cleanup(legacyTarget.Close)
		fixture := setupGeminiVideoProxyFixture(
			t,
			legacyTarget.URL+"/legacy-provider-uri-sentinel?"+
				"legacy-query-sentinel=1",
		)
		corruptEnvelope := "naenc:v1:test:corrupt-envelope-query-sentinel:" +
			"corrupt-envelope-payload-sentinel"
		require.NoError(
			t,
			model.DB.Model(&model.Task{}).
				Where("task_id = ?", fixture.taskID).
				UpdateColumn("provider_result_uri", corruptEnvelope).Error,
		)

		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusBadGateway, response.Code)
		assert.Contains(t, logs.String(), "phase=gemini_url_resolution")
		assertSafe(
			t,
			logs.String(),
			response,
			"legacy-provider-uri-sentinel",
			"legacy-query-sentinel",
			"corrupt-envelope-query-sentinel",
			"corrupt-envelope-payload-sentinel",
			corruptEnvelope,
		)
		assert.Zero(t, legacyRequests)
	})

	t.Run("malformed poll body has no raw fallback", func(t *testing.T) {
		logs := captureVideoProxyLogs(t)
		var contentRequests int
		contentTarget := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				contentRequests++
				_, _ = io.WriteString(w, "raw-fallback-content")
			},
		))
		t.Cleanup(contentTarget.Close)
		const rawSentinel = "poll-raw-body-sentinel"
		pollServer := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(
					w,
					`{"done":true,"response":{"videos":[{"uri":17},`+
						`{"uri":"`+contentTarget.URL+`/`+rawSentinel+`"}]}}`,
				)
			},
		))
		t.Cleanup(pollServer.Close)
		fixture := setupGeminiVideoProxyFixture(
			t,
			contentTarget.URL+"/initial-result",
		)

		var channel model.Channel
		require.NoError(
			t,
			model.DB.Where("name = ?", "gemini-video-proxy-test").
				First(&channel).Error,
		)
		channel.BaseURL = &pollServer.URL
		require.NoError(t, model.DB.Save(&channel).Error)

		var task model.Task
		require.NoError(
			t,
			model.DB.Where("task_id = ?", fixture.taskID).First(&task).Error,
		)
		task.ClearProviderResultURI()
		task.SetData(map[string]any{
			"done": false,
			"video": map[string]any{
				"url": "/v1/videos/" + fixture.taskID + "/content",
			},
		})
		require.NoError(t, model.DB.Save(&task).Error)

		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusBadGateway, response.Code)
		assert.Contains(t, logs.String(), "phase=gemini_url_resolution")
		assertSafe(
			t,
			logs.String(),
			response,
			rawSentinel,
			contentTarget.URL,
			"\"uri\":17",
		)
		assert.Zero(t, contentRequests)
	})
}

func TestVideoProxyNonGeminiBranchesRemainUnchanged(t *testing.T) {
	for _, channelType := range []int{
		constant.ChannelTypeOpenAI,
		constant.ChannelTypeSora,
	} {
		t.Run(fmt.Sprintf("channel type %d", channelType), func(t *testing.T) {
			type observation struct {
				authorization string
				path          string
			}
			observed := make(chan observation, 1)
			target := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					observed <- observation{
						authorization: r.Header.Get("Authorization"),
						path:          r.URL.EscapedPath(),
					}
					_, _ = io.WriteString(w, "non-gemini-content")
				},
			))
			t.Cleanup(target.Close)
			fixture := setupVideoProxyFixture(
				t,
				channelType,
				"https://unused.example/result",
			)

			var channel model.Channel
			require.NoError(
				t,
				model.DB.Where("name = ?", "gemini-video-proxy-test").
					First(&channel).Error,
			)
			channel.BaseURL = &target.URL
			require.NoError(t, model.DB.Save(&channel).Error)

			response := runGeminiVideoProxy(t, fixture)

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, "non-gemini-content", response.Body.String())
			select {
			case request := <-observed:
				assert.Equal(
					t,
					"Bearer "+geminiVideoProxyTestAPIKey,
					request.authorization,
				)
				assert.Equal(
					t,
					"/v1/videos/"+fixture.taskID+"/content",
					request.path,
				)
			default:
				t.Fatal("non-Gemini target was not requested")
			}
		})
	}

	t.Run("default provider uses private result URL", func(t *testing.T) {
		var requests int
		target := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				requests++
				assert.Empty(t, r.Header.Get("Authorization"))
				assert.Empty(t, r.Header.Get(geminiVideoAPIKeyHeader))
				_, _ = io.WriteString(w, "default-provider-content")
			},
		))
		t.Cleanup(target.Close)
		fixture := setupVideoProxyFixture(
			t,
			constant.ChannelTypeSunoAPI,
			target.URL+"/default-content",
		)

		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "default-provider-content", response.Body.String())
		assert.Equal(t, 1, requests)
	})
}

func TestVideoProxyFiltersUpstreamResponseHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Disposition", `inline; filename="video.mp4"`)
		w.Header().Set("ETag", `"video-etag"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Set-Cookie", "session=attacker")
		w.Header().Set("Authorization", "Bearer response-secret")
		w.Header().Set("X-Goog-Api-Key", "credential-sentinel")
		w.Header().Set("Location", "https://example.invalid/?key=credential-sentinel")
		w.Header().Set("Content-Location", "https://example.invalid/?key=credential-sentinel")
		w.Header().Set("Link", "<https://example.invalid/?key=credential-sentinel>")
		w.Header().Set("Refresh", "0;url=https://example.invalid/?key=credential-sentinel")
		w.Header().Set("X-Secret", "credential-sentinel")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("Expires", "Wed, 21 Oct 2030 07:28:00 GMT")
		w.Header().Set("Vary", "Authorization")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Security-Policy", "default-src *")
		w.Header().Set("Connection", "X-Internal")
		w.Header().Set("X-Internal", "credential-sentinel")
		_, _ = io.WriteString(w, "video-body")
	}))
	t.Cleanup(server.Close)

	fixture := setupGeminiVideoProxyFixture(t, server.URL+"/content")

	response := runGeminiVideoProxy(t, fixture)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "video-body", response.Body.String())
	assert.Equal(t, "video/mp4", response.Header().Get("Content-Type"))
	assert.Equal(t, `inline; filename="video.mp4"`, response.Header().Get("Content-Disposition"))
	assert.Equal(t, `"video-etag"`, response.Header().Get("ETag"))
	assert.Equal(t, "Wed, 21 Oct 2015 07:28:00 GMT", response.Header().Get("Last-Modified"))
	for _, header := range []string{
		"Set-Cookie",
		"Authorization",
		"X-Goog-Api-Key",
		"Location",
		"Content-Location",
		"Link",
		"Refresh",
		"X-Secret",
		"Expires",
		"Vary",
		"Access-Control-Allow-Origin",
		"Content-Security-Policy",
		"Connection",
		"X-Internal",
	} {
		assert.Empty(t, response.Header().Get(header), header)
	}
	var serializedHeaders strings.Builder
	require.NoError(t, response.Header().Write(&serializedHeaders))
	assert.NotContains(t, serializedHeaders.String(), "credential-sentinel")
	assert.NotContains(t, serializedHeaders.String(), "session=attacker")
	assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
}

func TestVideoProxyDataURLUsesPrivateNoStoreCache(t *testing.T) {
	fixture := setupVideoProxyFixture(
		t,
		constant.ChannelTypeVertexAi,
		"data:video/mp4;base64,dmlkZW8=",
	)

	response := runGeminiVideoProxy(t, fixture)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "video", response.Body.String())
	assert.Equal(t, "video/mp4", response.Header().Get("Content-Type"))
	assert.Equal(
		t,
		"private, no-store",
		response.Header().Get("Cache-Control"),
	)
}

func TestVideoProxyDataURLDoesNotRequireForwardProxy(t *testing.T) {
	fixture := setupVideoProxyFixture(
		t,
		constant.ChannelTypeVertexAi,
		"data:video/mp4;base64,dmlkZW8=",
	)
	setGeminiVideoProxyChannelProxy(t, "http://proxy.example:3128")
	updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection":     "true",
		"allow_private_ip":           "false",
		"domain_filter_mode":         "false",
		"ip_filter_mode":             "false",
		"domain_list":                "null",
		"ip_list":                    "null",
		"allowed_ports":              `["80","443"]`,
		"apply_ip_filter_for_domain": "true",
	})
	require.NoError(t, err)
	require.True(t, updated)

	response := runGeminiVideoProxy(t, fixture)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "video", response.Body.String())
	assert.Equal(t, "video/mp4", response.Header().Get("Content-Type"))
	assert.Equal(
		t,
		"private, no-store",
		response.Header().Get("Cache-Control"),
	)
}

func TestVideoProxyDoesNotAdvertiseOrForwardRangeSupport(t *testing.T) {
	upstreamRange := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRange <- r.Header.Get("Range")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 0-3/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "vide")
	}))
	t.Cleanup(server.Close)

	fixture := setupGeminiVideoProxyFixture(t, server.URL+"/content")
	response := runGeminiVideoProxyWithHeaders(t, fixture, http.Header{
		"Range": {"bytes=0-3"},
	})

	select {
	case forwardedRange := <-upstreamRange:
		assert.Empty(t, forwardedRange)
	default:
		t.Fatal("upstream handler was not reached")
	}
	assert.Equal(t, http.StatusBadGateway, response.Code)
	assert.Empty(t, response.Header().Get("Accept-Ranges"))
	assert.Empty(t, response.Header().Get("Content-Range"))
}

func TestWithVideoProxyCredentialRedirectPolicyPreservesClient(t *testing.T) {
	redirectErr := errors.New("existing redirect policy")
	redirectCalls := 0
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	base := &http.Client{
		Transport: http.DefaultTransport,
		Timeout:   17 * time.Second,
		Jar:       jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			redirectCalls++
			return redirectErr
		},
	}

	wrapped := withVideoProxyCredentialRedirectPolicy(base, "x-goog-api-key")

	require.NotSame(t, base, wrapped)
	assert.Same(t, base.Transport, wrapped.Transport)
	assert.Equal(t, base.Timeout, wrapped.Timeout)
	assert.Same(t, base.Jar, wrapped.Jar)
	initial := httptest.NewRequest(http.MethodGet, "https://video.example/start", nil)
	redirect := httptest.NewRequest(http.MethodGet, "https://other.example/content", nil)
	redirect.Header.Set("x-goog-api-key", geminiVideoProxyTestAPIKey)
	err = wrapped.CheckRedirect(redirect, []*http.Request{initial})

	require.ErrorIs(t, err, redirectErr)
	assert.Equal(t, 1, redirectCalls)
	assert.Empty(t, redirect.Header.Get("x-goog-api-key"))
	assert.NotNil(t, base.CheckRedirect)
}

func TestVideoProxyGeminiFailureSurfacesOmitCredentialAndRawURL(t *testing.T) {
	assertSafe := func(t *testing.T, logs string, response *httptest.ResponseRecorder, sentinels ...string) {
		t.Helper()
		surface := logs + "\n" + response.Body.String()
		assert.NotContains(t, surface, geminiVideoProxyTestAPIKey)
		for _, sentinel := range sentinels {
			assert.NotContains(t, surface, sentinel)
		}
	}

	t.Run("malformed decrypted private URL is terminal", func(t *testing.T) {
		logs := captureVideoProxyLogs(t)
		const (
			privateSentinel = "malformed-private-result-sentinel"
			legacySentinel  = "valid-legacy-result-sentinel"
			pollSentinel    = "valid-poll-result-sentinel"
		)
		legacyRequests := make(chan struct{}, 1)
		legacyTarget := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				legacyRequests <- struct{}{}
				_, _ = io.WriteString(w, "legacy-fallback-content")
			},
		))
		t.Cleanup(legacyTarget.Close)
		pollResultRequests := make(chan struct{}, 1)
		pollResultTarget := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				pollResultRequests <- struct{}{}
				_, _ = io.WriteString(w, "poll-fallback-content")
			},
		))
		t.Cleanup(pollResultTarget.Close)
		pollRequests := make(chan struct{}, 1)
		pollServer := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				pollRequests <- struct{}{}
				_, _ = io.WriteString(
					w,
					`{"done":true,"response":{"generateVideoResponse":{`+
						`"generatedVideos":[{"video":{"uri":"`+
						pollResultTarget.URL+`/`+pollSentinel+`?key=`+
						url.QueryEscape(geminiVideoProxyTestAPIKey)+
						`&poll-query-sentinel=1"}}]}}}`,
				)
			},
		))
		t.Cleanup(pollServer.Close)
		privateURI := "https://private.example/%zz/" + privateSentinel +
			"?key=" + url.QueryEscape(geminiVideoProxyTestAPIKey) +
			"&private-query-sentinel=1"
		fixture := setupGeminiVideoProxyFixture(
			t,
			privateURI,
		)
		var channel model.Channel
		require.NoError(
			t,
			model.DB.Where("name = ?", "gemini-video-proxy-test").
				First(&channel).Error,
		)
		channel.BaseURL = &pollServer.URL
		require.NoError(t, model.DB.Save(&channel).Error)

		var task model.Task
		require.NoError(
			t,
			model.DB.Where("task_id = ?", fixture.taskID).First(&task).Error,
		)
		require.NotNil(t, task.EncryptedProviderResultURI)
		encryptedEnvelope := *task.EncryptedProviderResultURI
		task.SetData(map[string]any{
			"uri": legacyTarget.URL + "/" + legacySentinel +
				"?key=" + url.QueryEscape(geminiVideoProxyTestAPIKey) +
				"&legacy-query-sentinel=1",
		})
		task.PrivateData.UpstreamTaskID = taskcommon.EncodeLocalTaskID(
			"models/veo-3.1/operations/malformed-private-terminal",
		)
		require.NoError(t, model.DB.Save(&task).Error)

		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusBadGateway, response.Code)
		assert.Contains(
			t,
			response.Body.String(),
			"Failed to resolve Gemini video URL",
		)
		assert.Contains(t, logs.String(), "phase=gemini_url_resolution")
		assert.Regexp(t, `error_type=\S+`, logs.String())
		assert.NotContains(t, logs.String(), "error_type=<nil>")
		assertSafe(
			t,
			logs.String(),
			response,
			privateSentinel,
			"private-query-sentinel",
			legacySentinel,
			"legacy-query-sentinel",
			pollSentinel,
			"poll-query-sentinel",
			privateURI,
			legacyTarget.URL,
			pollServer.URL,
			pollResultTarget.URL,
			encryptedEnvelope,
		)
		select {
		case <-legacyRequests:
			t.Fatal("valid legacy fallback was requested")
		default:
		}
		select {
		case <-pollRequests:
			t.Fatal("provider was re-polled after private URI failure")
		default:
		}
		select {
		case <-pollResultRequests:
			t.Fatal("provider poll fallback content was requested")
		default:
		}
	})

	t.Run("URL validation rejection", func(t *testing.T) {
		logs := captureVideoProxyLogs(t)
		fixture := setupGeminiVideoProxyFixture(
			t,
			"http://127.0.0.1:80/content?key="+
				url.QueryEscape(geminiVideoProxyTestAPIKey)+
				"&trace=validation-query-sentinel",
		)
		updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
			"enable_ssrf_protection": "true",
			"allow_private_ip":       "false",
		})
		require.NoError(t, err)
		require.True(t, updated)

		response := runGeminiVideoProxy(t, fixture)

		var responseBody struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &responseBody))
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Equal(t, "server_error", responseBody.Error.Type)
		assert.Equal(
			t,
			"request blocked by URL security policy",
			responseBody.Error.Message,
		)
		assert.Contains(t, logs.String(), "phase=url_validation")
		assert.Regexp(t, `error_type=\S+`, logs.String())
		assert.NotContains(t, logs.String(), "error_type=<nil>")
		assertSafe(t, logs.String(), response, "validation-query-sentinel", "trace=")
	})

	t.Run("transport failure", func(t *testing.T) {
		logs := captureVideoProxyLogs(t)
		closedServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		closedURL := closedServer.URL
		closedServer.Close()
		fixture := setupGeminiVideoProxyFixture(
			t,
			closedURL+"/content?key="+
				url.QueryEscape(geminiVideoProxyTestAPIKey)+
				"&trace=transport-query-sentinel",
		)

		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusBadGateway, response.Code)
		assert.Contains(t, logs.String(), "phase=upstream_fetch")
		assert.Regexp(t, `error_type=\S+`, logs.String())
		assert.NotContains(t, logs.String(), "error_type=<nil>")
		assertSafe(t, logs.String(), response, "transport-query-sentinel", "trace=")
	})

	t.Run("redirect failure", func(t *testing.T) {
		logs := captureVideoProxyLogs(t)
		closedTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		closedTargetURL := closedTarget.URL
		closedTarget.Close()
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(
				w,
				r,
				closedTargetURL+"/content?trace=redirect-target-query-sentinel",
				http.StatusFound,
			)
		}))
		t.Cleanup(source.Close)
		fixture := setupGeminiVideoProxyFixture(
			t,
			source.URL+"/start?key="+
				url.QueryEscape(geminiVideoProxyTestAPIKey)+
				"&trace=redirect-source-query-sentinel",
		)

		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusBadGateway, response.Code)
		assert.Contains(t, logs.String(), "phase=upstream_fetch")
		assert.Regexp(t, `error_type=\S+`, logs.String())
		assert.NotContains(t, logs.String(), "error_type=<nil>")
		assertSafe(
			t,
			logs.String(),
			response,
			"redirect-source-query-sentinel",
			"redirect-target-query-sentinel",
			"trace=",
		)
	})

	t.Run("upstream non-200", func(t *testing.T) {
		logs := captureVideoProxyLogs(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(server.Close)
		fixture := setupGeminiVideoProxyFixture(
			t,
			server.URL+"/content?key="+
				url.QueryEscape(geminiVideoProxyTestAPIKey)+
				"&trace=status-query-sentinel",
		)

		response := runGeminiVideoProxy(t, fixture)

		assert.Equal(t, http.StatusBadGateway, response.Code)
		assert.Contains(t, logs.String(), "phase=upstream_status")
		assert.Contains(t, logs.String(), "status=503")
		assertSafe(t, logs.String(), response, "status-query-sentinel", "trace=")
	})
}
