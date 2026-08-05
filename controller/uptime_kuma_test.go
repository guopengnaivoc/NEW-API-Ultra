package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/console_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setUptimeKumaGroupsForTest(t *testing.T, groups string) {
	t.Helper()

	original := console_setting.GetConsoleSetting().UptimeKumaGroups
	t.Cleanup(func() {
		updated, err := config.GlobalConfig.Update("console_setting", map[string]string{
			"uptime_kuma_groups": original,
		})
		require.NoError(t, err)
		require.True(t, updated)
		uptimeStatusCache.Store(nil)
	})

	updated, err := config.GlobalConfig.Update("console_setting", map[string]string{
		"uptime_kuma_groups": groups,
	})
	require.NoError(t, err)
	require.True(t, updated)
	uptimeStatusCache.Store(nil)
}

func newUptimeKumaTestServer(t *testing.T, requestCount *atomic.Int32) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, apiHeartbeatPath):
			_, _ = io.WriteString(w, `{"heartbeatList":{"1":[{"status":1}]},"uptimeList":{"1_24":0.999}}`)
		case strings.Contains(r.URL.Path, apiStatusPath):
			_, _ = io.WriteString(w, `{"publicGroupList":[{"id":1,"name":"Core","monitorList":[{"id":1,"name":"API"}]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestGetUptimeKumaStatusDoesNotPerformOutboundRequests(t *testing.T) {
	var requestCount atomic.Int32
	server := newUptimeKumaTestServer(t, &requestCount)
	defer server.Close()

	groups, err := common.Marshal([]map[string]interface{}{{
		"url":          server.URL,
		"slug":         "public",
		"categoryName": "Production",
	}})
	require.NoError(t, err)
	setUptimeKumaGroupsForTest(t, string(groups))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/uptime/status", nil)

	GetUptimeKumaStatus(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Zero(t, requestCount.Load(), "an anonymous status request must not trigger upstream egress")

	var response struct {
		Success bool                `json:"success"`
		Data    []UptimeGroupResult `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	require.Len(t, response.Data, 1)
	assert.Equal(t, "Production", response.Data[0].CategoryName)
	assert.Empty(t, response.Data[0].Monitors)
}

func TestRefreshUptimeKumaStatusPublishesSnapshotForRequestHandlers(t *testing.T) {
	var requestCount atomic.Int32
	server := newUptimeKumaTestServer(t, &requestCount)
	defer server.Close()

	groups, err := common.Marshal([]map[string]interface{}{{
		"url":          server.URL,
		"slug":         "public",
		"categoryName": "Production",
	}})
	require.NoError(t, err)
	setUptimeKumaGroupsForTest(t, string(groups))

	require.NoError(t, refreshUptimeKumaStatus(context.Background(), server.Client()))
	assert.Equal(t, int32(2), requestCount.Load())

	for range 2 {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/uptime/status", nil)
		GetUptimeKumaStatus(ctx)

		var response struct {
			Data []UptimeGroupResult `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		require.Len(t, response.Data, 1)
		require.Len(t, response.Data[0].Monitors, 1)
		assert.Equal(t, "API", response.Data[0].Monitors[0].Name)
		assert.Equal(t, 0.999, response.Data[0].Monitors[0].Uptime)
		assert.Equal(t, 1, response.Data[0].Monitors[0].Status)
	}

	assert.Equal(t, int32(2), requestCount.Load(), "reading a snapshot must not refetch upstream data")
}

func TestGetAndDecodeRejectsOversizedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(" ", 1<<20+1)+`{}`)
	}))
	defer server.Close()

	var destination map[string]interface{}
	err := getAndDecode(context.Background(), server.Client(), server.URL, &destination)

	require.Error(t, err)
}

func TestUptimeKumaFetchesUseAGlobalConcurrencyBudget(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}

		time.Sleep(25 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, apiHeartbeatPath) {
			_, _ = io.WriteString(w, `{"heartbeatList":{},"uptimeList":{}}`)
			return
		}
		_, _ = io.WriteString(w, `{"publicGroupList":[]}`)
	}))
	defer server.Close()

	group := map[string]interface{}{
		"url":          server.URL,
		"slug":         "public",
		"categoryName": "Production",
	}
	client := server.Client()

	var waitGroup sync.WaitGroup
	for range 8 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			fetchGroupData(context.Background(), client, group)
		}()
	}
	waitGroup.Wait()

	assert.LessOrEqual(t, maximum.Load(), int32(4))
}
