package relay

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
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

type midjourneyProxyResponse struct {
	status            int
	contentType       string
	body              []byte
	declaredLength    int64
	delay             time.Duration
	delayAfterHeaders time.Duration
}

type midjourneyImageFixture struct {
	channelID    int
	requestCount *atomic.Int64
}

func setupMidjourneyImageProxyTest(t *testing.T, response midjourneyProxyResponse) *midjourneyImageFixture {
	t.Helper()

	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousRedis := common.RedisEnabled
	previousMemoryCache := common.MemoryCacheEnabled
	previousMaxMB := constant.MaxFileDownloadMB
	originalFetchSetting, err := config.ConfigToMap(system_setting.GetFetchSetting())
	require.NoError(t, err)

	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	constant.MaxFileDownloadMB = 1

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}, &model.Channel{}))

	updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection":     "false",
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

	requestCount := &atomic.Int64{}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		if response.delay > 0 {
			time.Sleep(response.delay)
		}
		if response.contentType != "" {
			w.Header().Set("Content-Type", response.contentType)
		}
		if response.declaredLength > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(response.declaredLength, 10))
		}
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if response.delayAfterHeaders > 0 {
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(response.delayAfterHeaders)
		}
		_, _ = w.Write(response.body)
	}))

	channel := model.Channel{Name: "midjourney-image-test", Key: "unused"}
	channel.SetSetting(dto.ChannelSettings{Proxy: proxy.URL})
	require.NoError(t, db.Create(&channel).Error)

	t.Cleanup(func() {
		proxy.Close()
		service.ResetProxyClientCache()
		restored, restoreErr := config.GlobalConfig.Update("fetch_setting", originalFetchSetting)
		require.NoError(t, restoreErr)
		require.True(t, restored)
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled = previousRedis
		common.MemoryCacheEnabled = previousMemoryCache
		constant.MaxFileDownloadMB = previousMaxMB
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	return &midjourneyImageFixture{
		channelID:    channel.Id,
		requestCount: requestCount,
	}
}

func validMidjourneyPNG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	return buffer.Bytes()
}

func performMidjourneyImageRequest(t *testing.T, userID int, task model.Midjourney) *httptest.ResponseRecorder {
	t.Helper()
	return performMidjourneyImageRequestWithContext(t, context.Background(), userID, task)
}

func performMidjourneyImageRequestWithContext(
	t *testing.T,
	requestContext context.Context,
	userID int,
	task model.Midjourney,
) *httptest.ResponseRecorder {
	t.Helper()
	require.NoError(t, model.DB.Create(&task).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/mj/image/"+task.MjId, nil).WithContext(requestContext)
	context.Params = gin.Params{{Key: "id", Value: task.MjId}}
	context.Set("id", userID)
	RelayMidjourneyImage(context)
	return recorder
}

func TestRelayMidjourneyImageRejectsForeignTask(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := setupMidjourneyImageProxyTest(t, midjourneyProxyResponse{
		status:      http.StatusOK,
		contentType: "image/png",
		body:        validMidjourneyPNG(t),
	})

	recorder := performMidjourneyImageRequest(t, 202, model.Midjourney{
		UserId:    101,
		MjId:      "victim-task",
		ImageUrl:  "http://upstream.example/image",
		ChannelId: fixture.channelID,
	})

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "midjourney_task_not_found")
	assert.Equal(t, int64(0), fixture.requestCount.Load())
}

func TestRelayMidjourneyImageRejectsUnpinnedForwardProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := setupMidjourneyImageProxyTest(t, midjourneyProxyResponse{
		status:      http.StatusOK,
		contentType: "image/png",
		body:        validMidjourneyPNG(t),
	})
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

	recorder := performMidjourneyImageRequest(t, 101, model.Midjourney{
		UserId:    101,
		MjId:      "protected-forward-proxy",
		ImageUrl:  "http://8.8.8.8/image",
		ChannelId: fixture.channelID,
	})

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "proxy_url_invalid")
	assert.Equal(t, int64(0), fixture.requestCount.Load())
}

func TestRelayMidjourneyImageValidatesUpstreamContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	validPNG := validMidjourneyPNG(t)
	tests := []struct {
		name       string
		response   midjourneyProxyResponse
		wantStatus int
		wantType   string
		notContain string
	}{
		{
			name: "detects valid png despite spoofed header",
			response: midjourneyProxyResponse{
				status:      http.StatusOK,
				contentType: "text/html",
				body:        validPNG,
			},
			wantStatus: http.StatusOK,
			wantType:   "image/png",
		},
		{
			name: "rejects html despite image header",
			response: midjourneyProxyResponse{
				status:      http.StatusOK,
				contentType: "image/png",
				body:        []byte("<html>active</html>"),
			},
			wantStatus: http.StatusBadGateway,
			notContain: "<html>active</html>",
		},
		{
			name: "rejects oversized body without partial bytes",
			response: midjourneyProxyResponse{
				status: http.StatusOK,
				body:   bytes.Repeat([]byte("oversized-sentinel"), 70000),
			},
			wantStatus: http.StatusBadGateway,
			notContain: "oversized-sentinel",
		},
		{
			name: "does not echo upstream error body",
			response: midjourneyProxyResponse{
				status: http.StatusForbidden,
				body:   []byte("upstream-secret-sentinel"),
			},
			wantStatus: http.StatusBadGateway,
			notContain: "upstream-secret-sentinel",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := setupMidjourneyImageProxyTest(t, test.response)
			recorder := performMidjourneyImageRequest(t, 101, model.Midjourney{
				UserId:    101,
				MjId:      "owner-task",
				ImageUrl:  "http://upstream.example/image",
				ChannelId: fixture.channelID,
			})

			assert.Equal(t, test.wantStatus, recorder.Code)
			if test.wantType != "" {
				assert.Equal(t, test.wantType, recorder.Header().Get("Content-Type"))
				assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
				assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
				assert.Contains(t, recorder.Header().Get("Content-Disposition"), "midjourney-image.png")
				assert.Equal(t, test.response.body, recorder.Body.Bytes())
			}
			if test.notContain != "" {
				assert.NotContains(t, recorder.Body.String(), test.notContain)
			}
		})
	}
}

func TestRelayMidjourneyImageRejectsDeclaredOversize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := setupMidjourneyImageProxyTest(t, midjourneyProxyResponse{
		status:         http.StatusOK,
		declaredLength: 2 * 1024 * 1024,
	})

	recorder := performMidjourneyImageRequest(t, 101, model.Midjourney{
		UserId:    101,
		MjId:      "declared-oversize",
		ImageUrl:  "http://upstream.example/image",
		ChannelId: fixture.channelID,
	})

	assert.Equal(t, http.StatusBadGateway, recorder.Code)
}

func TestRelayMidjourneyImageRejectsInvalidSizeConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := setupMidjourneyImageProxyTest(t, midjourneyProxyResponse{
		status: http.StatusOK,
		body:   validMidjourneyPNG(t),
	})
	constant.MaxFileDownloadMB = 0

	recorder := performMidjourneyImageRequest(t, 101, model.Midjourney{
		UserId:    101,
		MjId:      "invalid-size-configuration",
		ImageUrl:  "http://upstream.example/image",
		ChannelId: fixture.channelID,
	})

	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), string(validMidjourneyPNG(t)))
}

func TestRelayMidjourneyImageHonorsRequestDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	validPNG := validMidjourneyPNG(t)
	fixture := setupMidjourneyImageProxyTest(t, midjourneyProxyResponse{
		status: http.StatusOK,
		delay:  100 * time.Millisecond,
		body:   validPNG,
	})
	requestContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	recorder := performMidjourneyImageRequestWithContext(t, requestContext, 101, model.Midjourney{
		UserId:    101,
		MjId:      "timeout-task",
		ImageUrl:  "http://upstream.example/image",
		ChannelId: fixture.channelID,
	})

	assert.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), string(validPNG))
}

func TestRelayMidjourneyImageTimesOutWhileReadingBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	validPNG := validMidjourneyPNG(t)
	fixture := setupMidjourneyImageProxyTest(t, midjourneyProxyResponse{
		status:            http.StatusOK,
		delayAfterHeaders: 100 * time.Millisecond,
		body:              validPNG,
	})
	requestContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	recorder := performMidjourneyImageRequestWithContext(t, requestContext, 101, model.Midjourney{
		UserId:    101,
		MjId:      "body-timeout-task",
		ImageUrl:  "http://upstream.example/image",
		ChannelId: fixture.channelID,
	})

	assert.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), string(validPNG))
}
