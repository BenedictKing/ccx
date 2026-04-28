package common

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestTryUpstreamWithAllKeys_ModelRouteUnavailableSkipsBreakerAndCooldown(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, fuzzyMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("fuzzy_%v", fuzzyMode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"No available channel for model gpt-5.5 under group codex (distributor)","type":"new_api_error"}}`))
			}))
			defer server.Close()

			rawConfig := fmt.Sprintf(`{
  "fuzzyModeEnabled": %t,
  "upstream": [
    {
      "name": "codex-route",
      "baseUrl": %q,
      "apiKeys": ["sk-test"],
      "serviceType": "openai"
    }
  ],
  "responsesUpstream": [],
  "geminiUpstream": [],
  "chatUpstream": []
}`, fuzzyMode, server.URL)
			cfgManager := newConfigManagerForCommonTest(t, rawConfig)

			messagesMetrics := metrics.NewMetricsManager()
			channelScheduler := scheduler.NewChannelScheduler(
				cfgManager,
				messagesMetrics,
				metrics.NewMetricsManager(),
				metrics.NewMetricsManager(),
				metrics.NewMetricsManager(),
				session.NewTraceAffinityManager(),
				nil,
			)

			upstream := &config.UpstreamConfig{
				Name:        "codex-route",
				BaseURL:     server.URL,
				APIKeys:     []string{"sk-test"},
				ServiceType: "openai",
			}
			requestBody := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))

			handled, _, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
				c,
				&config.EnvConfig{RequestTimeout: 1000, LogLevel: "error"},
				cfgManager,
				channelScheduler,
				scheduler.ChannelKindMessages,
				"Messages",
				messagesMetrics,
				upstream,
				BuildDefaultURLResults(upstream.GetAllBaseURLs()),
				requestBody,
				false,
				func(up *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
					return cfgManager.GetNextAPIKey(up, failedKeys, "Messages")
				},
				func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
					return http.NewRequest(http.MethodPost, upstreamCopy.BaseURL, bytes.NewReader(requestBody))
				},
				nil,
				nil,
				nil,
				func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
					resp.Body.Close()
					return nil, nil
				},
				"gpt-5.5",
				0,
				channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
			)

			if handled {
				t.Fatal("handled = true, want false so outer failover can continue")
			}
			if failoverErr == nil {
				t.Fatal("failoverErr = nil, want upstream error for outer failover")
			}
			if lastErr == nil {
				t.Fatal("lastErr = nil, want non-nil")
			}

			if cooldownKeys := cfgManager.GetCooldownKeys("Messages", 0); len(cooldownKeys) != 0 {
				t.Fatalf("GetCooldownKeys() = %v, want no cooldown keys", cooldownKeys)
			}

			if state := messagesMetrics.GetKeyCircuitState(server.URL, "sk-test"); state != metrics.CircuitStateClosed {
				t.Fatalf("GetKeyCircuitState() = %s, want closed", state.String())
			}
		})
	}
}
