package common

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

			if state := messagesMetrics.GetKeyCircuitState(server.URL, "sk-test", "claude"); state != metrics.CircuitStateClosed {
				t.Fatalf("GetKeyCircuitState() = %s, want closed", state.String())
			}
		})
	}
}

func TestTryUpstreamWithAllKeys_CooldownStreamErrorContinuesFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	rawConfig := fmt.Sprintf(`{
  "upstream": [
    {
      "name": "stream-cooldown",
      "baseUrl": %q,
      "apiKeys": ["sk-first", "sk-second"],
      "serviceType": "claude"
    }
  ],
  "responsesUpstream": [],
  "geminiUpstream": [],
  "chatUpstream": []
}`, server.URL)
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
		Name:        "stream-cooldown",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-first", "sk-second"},
		ServiceType: "claude",
	}
	requestBody := []byte(`{"model":"claude","messages":[{"role":"user","content":"hi"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))

	var handleCalls int
	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
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
		true,
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
			handleCalls++
			if apiKey == "sk-first" {
				return nil, &ErrCooldownKey{Reason: "rate_limit", Message: "too many requests", Duration: time.Minute}
			}
			return &types.Usage{InputTokens: 1, OutputTokens: 2}, nil
		},
		"claude",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if !handled {
		t.Fatal("handled = false, want true after second key succeeds")
	}
	if successKey != "sk-second" {
		t.Fatalf("successKey = %q, want sk-second", successKey)
	}
	if failoverErr != nil {
		t.Fatalf("failoverErr = %+v, want nil", failoverErr)
	}
	if lastErr != nil {
		t.Fatalf("lastErr = %v, want nil", lastErr)
	}
	if handleCalls != 2 || requests != 2 {
		t.Fatalf("handleCalls=%d requests=%d, want 2 attempts", handleCalls, requests)
	}
	if !cfgManager.IsKeyFailed("sk-first", "Messages") {
		t.Fatal("sk-first should be cooled down after ErrCooldownKey")
	}
	metricsResp := messagesMetrics.ToResponseMultiURL(0, upstream.GetAllBaseURLs(), upstream.APIKeys, "claude", 0)
	if metricsResp.AxonHubForwarding == nil {
		t.Fatal("AxonHubForwarding = nil, want same-format raw stats")
	}
	if metricsResp.AxonHubForwarding.RequestCount != 2 {
		t.Fatalf("AxonHubForwarding requestCount = %d, want 2", metricsResp.AxonHubForwarding.RequestCount)
	}
	if metricsResp.AxonHubForwarding.InputTokens != 1 || metricsResp.AxonHubForwarding.OutputTokens != 2 {
		t.Fatalf("AxonHubForwarding tokens = input:%d output:%d, want 1/2",
			metricsResp.AxonHubForwarding.InputTokens, metricsResp.AxonHubForwarding.OutputTokens)
	}
	if got := metricsResp.AxonHubForwarding.ByRoute[0]; got.InboundFamily != "messages" || got.Mode != metrics.AxonHubForwardingModeSameFormatRaw {
		t.Fatalf("AxonHubForwarding route = %+v, want messages same_format_raw", got)
	}
}

func TestTryUpstreamWithAllKeys_CrossFormatConvertedKeepsFailoverAndUsageStats(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary upstream failure"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer server.Close()

	rawConfig := fmt.Sprintf(`{
  "upstream": [],
  "responsesUpstream": [
    {
      "name": "responses-to-openai",
      "baseUrl": %q,
      "apiKeys": ["sk-first", "sk-second"],
      "serviceType": "openai"
    }
  ],
  "geminiUpstream": [],
  "chatUpstream": []
}`, server.URL)
	cfgManager := newConfigManagerForCommonTest(t, rawConfig)

	responsesMetrics := metrics.NewMetricsManager()
	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		metrics.NewMetricsManager(),
		responsesMetrics,
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		session.NewTraceAffinityManager(),
		nil,
	)

	upstream := &config.UpstreamConfig{
		Name:        "responses-to-openai",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-first", "sk-second"},
		ServiceType: "openai",
	}
	requestBody := []byte(`{"model":"gpt-4o","input":"hi"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))

	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		&config.EnvConfig{RequestTimeout: 1000, LogLevel: "error"},
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindResponses,
		"Responses",
		responsesMetrics,
		upstream,
		BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		requestBody,
		false,
		func(up *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(up, failedKeys, "Responses")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequest(http.MethodPost, upstreamCopy.BaseURL, bytes.NewReader(requestBody))
		},
		nil,
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			defer func() { _ = resp.Body.Close() }()
			return &types.Usage{
				InputTokens:              3,
				OutputTokens:             4,
				CacheCreationInputTokens: 5,
				CacheReadInputTokens:     2,
			}, nil
		},
		"gpt-4o",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses),
	)

	if !handled {
		t.Fatal("handled = false, want true after second key succeeds")
	}
	if successKey != "sk-second" {
		t.Fatalf("successKey = %q, want sk-second", successKey)
	}
	if failoverErr != nil {
		t.Fatalf("failoverErr = %+v, want nil", failoverErr)
	}
	if lastErr != nil {
		t.Fatalf("lastErr = %v, want nil", lastErr)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 attempts", requests)
	}
	if !cfgManager.IsKeyFailed("sk-first", "Responses") {
		t.Fatal("sk-first should be cooled down after retryable upstream error")
	}

	metricsResp := responsesMetrics.ToResponseMultiURL(0, upstream.GetAllBaseURLs(), upstream.APIKeys, "openai", 0)
	if metricsResp.AxonHubForwarding == nil {
		t.Fatal("AxonHubForwarding = nil, want cross-format stats")
	}
	if metricsResp.AxonHubForwarding.RequestCount != 2 {
		t.Fatalf("AxonHubForwarding requestCount = %d, want 2", metricsResp.AxonHubForwarding.RequestCount)
	}
	if metricsResp.AxonHubForwarding.InputTokens != 3 ||
		metricsResp.AxonHubForwarding.OutputTokens != 4 ||
		metricsResp.AxonHubForwarding.CacheCreationTokens != 5 ||
		metricsResp.AxonHubForwarding.CacheReadTokens != 2 {
		t.Fatalf("AxonHubForwarding tokens = input:%d output:%d cacheCreate:%d cacheRead:%d, want 3/4/5/2",
			metricsResp.AxonHubForwarding.InputTokens,
			metricsResp.AxonHubForwarding.OutputTokens,
			metricsResp.AxonHubForwarding.CacheCreationTokens,
			metricsResp.AxonHubForwarding.CacheReadTokens)
	}
	if got := metricsResp.AxonHubForwarding.ByRoute[0]; got.InboundFamily != "responses" || got.Mode != metrics.AxonHubForwardingModeCrossFormatConverted {
		t.Fatalf("AxonHubForwarding route = %+v, want responses cross_format_converted", got)
	}
}
