package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestUpstreamFirstContentLatencyFollowsAttemptOutcome(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, outcome := range []string{"success", "failure", "unobserved", "cancelled", "without live logs"} {
		t.Run(outcome, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: content\n\n")
			}))
			defer server.Close()
			cfg := config.Config{Upstream: []config.UpstreamConfig{{Name: "first-content", BaseURL: server.URL, APIKeys: []string{"sk-test"}, Status: "active", ServiceType: "openai"}}}
			path := filepath.Join(t.TempDir(), "config.json")
			data, _ := json.Marshal(cfg)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cfgManager, err := config.NewConfigManager(path, "")
			if err != nil {
				t.Fatal(err)
			}
			defer cfgManager.Close()
			managers := make([]*metrics.MetricsManager, 5)
			for i := range managers {
				managers[i] = metrics.NewMetricsManager()
				defer managers[i].Stop()
			}
			channelScheduler := scheduler.NewChannelScheduler(cfgManager, managers[0], managers[1], managers[2], managers[3], managers[4], session.NewTraceAffinityManager(), warmup.NewURLManager(30*time.Second, 3))
			logStore := channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages)
			if outcome == "without live logs" {
				logStore = nil
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"model","stream":true}`))
			upstream := &cfg.Upstream[0]
			TryUpstreamWithAllKeys(c, config.NewEnvConfig(), cfgManager, channelScheduler,
				scheduler.ChannelKindMessages, "Messages", managers[0], upstream,
				[]warmup.URLLatencyResult{{URL: server.URL}}, []byte(`{"model":"model","stream":true}`), nil, true,
				func(_ *config.UpstreamConfig, _ map[string]bool) (string, error) { return "sk-test", nil },
				func(c *gin.Context, _ *config.UpstreamConfig, _ string) (*http.Request, error) {
					return http.NewRequestWithContext(c.Request.Context(), "POST", server.URL, strings.NewReader(`{}`))
				}, nil, nil, nil,
				func(c *gin.Context, resp *http.Response, _ *config.UpstreamConfig, _ string, _ []byte) (*types.Usage, error) {
					defer resp.Body.Close()
					_, _ = io.ReadAll(resp.Body)
					observer := GetStreamTimeoutObserver(c)
					if observer == nil {
						t.Fatal("missing stream observer")
					}
					if outcome != "unobserved" {
						observer.MarkFirstContent(observer.startedAt.Add(250 * time.Millisecond))
						// 即使实时日志被淘汰，采样仍来自本次 observer。
						if logStore != nil {
							for i := 0; i < 60; i++ {
								logStore.Record(observer.metricsKey, &metrics.ChannelLog{RequestID: fmt.Sprint(i)})
							}
						}
					}
					if outcome == "failure" {
						return nil, errors.New("upstream stream interrupted")
					}
					if outcome == "cancelled" {
						return nil, context.Canceled
					}
					return nil, nil
				}, "model", "", 0, logStore)
			stats := managers[0].GetTimeWindowStatsForKey(server.URL, "sk-test", "openai", time.Hour)
			want := int64(1)
			if outcome == "unobserved" || outcome == "cancelled" {
				want = 0
			}
			if stats.FirstContentLatencySamples != want || (want > 0 && stats.FirstContentLatencyP95Ms != 250) {
				t.Fatalf("%s: unexpected content stats: %+v", outcome, stats)
			}
		})
	}
}
