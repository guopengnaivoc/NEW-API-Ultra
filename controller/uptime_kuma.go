package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/console_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	requestTimeout                   = 30 * time.Second
	httpTimeout                      = 10 * time.Second
	uptimeRefreshInterval            = time.Minute
	uptimeResponseLimitBytes   int64 = 1 << 20
	uptimeMaxConcurrentFetches       = 4
	uptimeKeySuffix                  = "_24"
	apiStatusPath                    = "/api/status-page/"
	apiHeartbeatPath                 = "/api/status-page/heartbeat/"
)

var (
	uptimeStatusCache  atomic.Pointer[uptimeStatusSnapshot]
	uptimeRefreshOnce  sync.Once
	uptimeRefreshGroup singleflight.Group
	uptimeFetchSlots   = make(chan struct{}, uptimeMaxConcurrentFetches)
)

type Monitor struct {
	Name   string  `json:"name"`
	Uptime float64 `json:"uptime"`
	Status int     `json:"status"`
	Group  string  `json:"group,omitempty"`
}

type UptimeGroupResult struct {
	CategoryName string    `json:"categoryName"`
	Monitors     []Monitor `json:"monitors"`
}

type uptimeStatusSnapshot struct {
	ConfigKey string
	Results   []UptimeGroupResult
}

func getUptimeKumaConfigSnapshot() (string, []map[string]interface{}, error) {
	configKey := console_setting.GetConsoleSetting().UptimeKumaGroups
	if configKey == "" {
		return "", []map[string]interface{}{}, nil
	}

	var groups []map[string]interface{}
	if err := common.UnmarshalJsonStr(configKey, &groups); err != nil {
		return "", nil, fmt.Errorf("decode Uptime Kuma groups: %w", err)
	}
	return configKey, groups, nil
}

func newUptimeKumaHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = uptimeMaxConcurrentFetches
	transport.MaxIdleConnsPerHost = uptimeMaxConcurrentFetches

	return &http.Client{
		Timeout:   httpTimeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func getAndDecode(ctx context.Context, client *http.Client, url string, dest interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	select {
	case uptimeFetchSlots <- struct{}{}:
		defer func() {
			<-uptimeFetchSlots
		}()
	case <-ctx.Done():
		return ctx.Err()
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected Uptime Kuma status: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, uptimeResponseLimitBytes+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > uptimeResponseLimitBytes {
		return errors.New("Uptime Kuma response exceeds size limit")
	}
	return common.Unmarshal(body, dest)
}

func fetchGroupData(ctx context.Context, client *http.Client, groupConfig map[string]interface{}) UptimeGroupResult {
	url, _ := groupConfig["url"].(string)
	slug, _ := groupConfig["slug"].(string)
	categoryName, _ := groupConfig["categoryName"].(string)

	result := UptimeGroupResult{
		CategoryName: categoryName,
		Monitors:     []Monitor{},
	}

	if url == "" || slug == "" {
		return result
	}

	baseURL := strings.TrimSuffix(url, "/")

	var statusData struct {
		PublicGroupList []struct {
			ID          int    `json:"id"`
			Name        string `json:"name"`
			MonitorList []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"monitorList"`
		} `json:"publicGroupList"`
	}

	var heartbeatData struct {
		HeartbeatList map[string][]struct {
			Status int `json:"status"`
		} `json:"heartbeatList"`
		UptimeList map[string]float64 `json:"uptimeList"`
	}

	g, gCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return getAndDecode(gCtx, client, baseURL+apiStatusPath+slug, &statusData)
	})
	g.Go(func() error {
		return getAndDecode(gCtx, client, baseURL+apiHeartbeatPath+slug, &heartbeatData)
	})

	if g.Wait() != nil {
		return result
	}

	for _, pg := range statusData.PublicGroupList {
		if len(pg.MonitorList) == 0 {
			continue
		}

		for _, m := range pg.MonitorList {
			monitor := Monitor{
				Name:  m.Name,
				Group: pg.Name,
			}

			monitorID := strconv.Itoa(m.ID)

			if uptime, exists := heartbeatData.UptimeList[monitorID+uptimeKeySuffix]; exists {
				monitor.Uptime = uptime
			}

			if heartbeats, exists := heartbeatData.HeartbeatList[monitorID]; exists && len(heartbeats) > 0 {
				monitor.Status = heartbeats[0].Status
			}

			result.Monitors = append(result.Monitors, monitor)
		}
	}

	return result
}

func refreshUptimeKumaStatus(ctx context.Context, client *http.Client) error {
	_, err, _ := uptimeRefreshGroup.Do("refresh", func() (interface{}, error) {
		configKey, groups, err := getUptimeKumaConfigSnapshot()
		if err != nil {
			return nil, err
		}

		results := make([]UptimeGroupResult, len(groups))
		if len(groups) > 0 {
			refreshCtx, cancel := context.WithTimeout(ctx, requestTimeout)
			defer cancel()

			if client == nil {
				client = newUptimeKumaHTTPClient()
			}

			g, gCtx := errgroup.WithContext(refreshCtx)
			for i, group := range groups {
				i, group := i, group
				g.Go(func() error {
					results[i] = fetchGroupData(gCtx, client, group)
					return nil
				})
			}
			_ = g.Wait()
		}

		if console_setting.GetConsoleSetting().UptimeKumaGroups != configKey {
			return nil, nil
		}
		uptimeStatusCache.Store(&uptimeStatusSnapshot{
			ConfigKey: configKey,
			Results:   results,
		})
		return nil, nil
	})
	return err
}

func StartUptimeKumaStatusRefresh() {
	uptimeRefreshOnce.Do(func() {
		gopool.Go(func() {
			client := newUptimeKumaHTTPClient()
			refresh := func() {
				if err := refreshUptimeKumaStatus(context.Background(), client); err != nil {
					common.SysError("failed to refresh Uptime Kuma status: " + err.Error())
				}
			}

			refresh()
			ticker := time.NewTicker(uptimeRefreshInterval)
			defer ticker.Stop()
			for range ticker.C {
				refresh()
			}
		})
	})
}

func GetUptimeKumaStatus(c *gin.Context) {
	configKey, groups, err := getUptimeKumaConfigSnapshot()
	if err != nil {
		common.SysError("failed to read Uptime Kuma status configuration: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": []UptimeGroupResult{}})
		return
	}

	results := make([]UptimeGroupResult, len(groups))
	for i, group := range groups {
		categoryName, _ := group["categoryName"].(string)
		results[i] = UptimeGroupResult{
			CategoryName: categoryName,
			Monitors:     []Monitor{},
		}
	}
	if snapshot := uptimeStatusCache.Load(); snapshot != nil && snapshot.ConfigKey == configKey {
		results = snapshot.Results
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": results})
}
