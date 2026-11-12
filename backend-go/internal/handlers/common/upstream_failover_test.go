package common

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestShouldNormalizeMetadataUserIDOnlyMessages(t *testing.T) {
	enabled := true
	disabled := false

	tests := []struct {
		name     string
		kind     scheduler.ChannelKind
		upstream *config.UpstreamConfig
		want     bool
	}{
		{
			name:     "messages preserves metadata by default",
			kind:     scheduler.ChannelKindMessages,
			upstream: &config.UpstreamConfig{},
			want:     false,
		},
		{
			name:     "messages honors disabled switch",
			kind:     scheduler.ChannelKindMessages,
			upstream: &config.UpstreamConfig{NormalizeMetadataUserID: &disabled},
			want:     false,
		},
		{
			name:     "responses ignores enabled switch",
			kind:     scheduler.ChannelKindResponses,
			upstream: &config.UpstreamConfig{NormalizeMetadataUserID: &enabled},
			want:     false,
		},
		{
			name:     "nil upstream",
			kind:     scheduler.ChannelKindMessages,
			upstream: nil,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldNormalizeMetadataUserID(tt.kind, tt.upstream); got != tt.want {
				t.Fatalf("shouldNormalizeMetadataUserID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTryUpstreamWithAllKeysRejectsOversizedVisionFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:                "desktop-compshare-messages",
				BaseURL:             "https://upstream.example.com",
				APIKeys:             []string{"sk-test"},
				Status:              "active",
				ServiceType:         "openai",
				ModelMapping:        map[string]string{"haiku": "deepseek-v4-flash"},
				NoVisionModels:      []string{"deepseek-v4-flash"},
				VisionFallbackModel: "MiniMax-M2.7",
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"deepseek-v4-flash": {ContextWindowTokens: 1000000},
					"MiniMax-M2.7":      {ContextWindowTokens: 200000},
				},
			},
		},
	}

	tmpDir, err := os.MkdirTemp("", "vision-fallback-context-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.json")
	cfgData, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configPath, cfgData, 0644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer cfgManager.Close()

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	defer messagesMetrics.Stop()
	defer responsesMetrics.Stop()
	defer geminiMetrics.Stop()
	defer chatMetrics.Stop()
	defer imagesMetrics.Stop()

	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		messagesMetrics,
		responsesMetrics,
		geminiMetrics,
		chatMetrics,
		imagesMetrics,
		session.NewTraceAffinityManager(),
		warmup.NewURLManager(30*time.Second, 3),
	)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", http.NoBody)

	requestBody := []byte(`{"model":"haiku","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}]}]}`)
	requirement := &scheduler.ContextRequirement{InputTokens: 250000, OutputTokens: 4096, RequiredTokens: 254096}
	upstream := &cfg.Upstream[0]
	buildCalled := false

	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: upstream.BaseURL}},
		requestBody,
		requirement,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return upstream.APIKeys[0], nil
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			buildCalled = true
			return httptest.NewRequest(http.MethodPost, upstreamCopy.BaseURL, http.NoBody), nil
		},
		func(apiKey string) {},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			return nil, nil
		},
		"haiku",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if handled {
		t.Fatal("fallback 上下文不足时不应处理请求")
	}
	if successKey != "" {
		t.Fatalf("successKey = %q, want empty", successKey)
	}
	if failoverErr != nil {
		t.Fatalf("failoverErr = %#v, want nil", failoverErr)
	}
	if lastErr == nil {
		t.Fatal("期望返回上下文校验错误")
	}
	if !strings.Contains(lastErr.Error(), "MiniMax-M2.7") || !strings.Contains(lastErr.Error(), "上下文窗口") {
		t.Fatalf("错误信息未包含 fallback 模型上下文根因: %v", lastErr)
	}
	if buildCalled {
		t.Fatal("fallback 模型上下文不足时不应构建上游请求")
	}
}

func TestTryUpstreamWithAllKeysOverloadedOpensCircuitAndCooldown(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name         string
		upstreamBody string
	}{
		{
			name:         "system_cpu_overloaded",
			upstreamBody: `{"error":{"message":"system cpu overloaded (current: 92.4%, threshold: 90%)","type":"new_api_error","param":"","code":"system_cpu_overloaded"}}`,
		},
		{
			name:         "no_available_account",
			upstreamBody: `{"error":{"message":"The service is temporarily unavailable. Please try again later.","type":"server_error","param":"","code":"no_available_account"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(tt.upstreamBody))
			}))
			defer server.Close()

			cfg := config.Config{
				Upstream: []config.UpstreamConfig{
					{
						Name:        "overloaded-messages",
						BaseURL:     server.URL,
						APIKeys:     []string{"sk-overloaded"},
						Status:      "active",
						ServiceType: "openai",
					},
				},
			}

			tmpDir, err := os.MkdirTemp("", "overloaded-failover-test-*")
			if err != nil {
				t.Fatalf("创建临时目录失败: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			configPath := filepath.Join(tmpDir, "config.json")
			cfgData, err := json.Marshal(cfg)
			if err != nil {
				t.Fatalf("序列化配置失败: %v", err)
			}
			if err := os.WriteFile(configPath, cfgData, 0644); err != nil {
				t.Fatalf("写入配置失败: %v", err)
			}

			cfgManager, err := config.NewConfigManager(configPath, "")
			if err != nil {
				t.Fatalf("创建配置管理器失败: %v", err)
			}
			defer cfgManager.Close()

			messagesMetrics := metrics.NewMetricsManager()
			responsesMetrics := metrics.NewMetricsManager()
			geminiMetrics := metrics.NewMetricsManager()
			chatMetrics := metrics.NewMetricsManager()
			imagesMetrics := metrics.NewMetricsManager()
			defer messagesMetrics.Stop()
			defer responsesMetrics.Stop()
			defer geminiMetrics.Stop()
			defer chatMetrics.Stop()
			defer imagesMetrics.Stop()

			channelScheduler := scheduler.NewChannelScheduler(
				cfgManager,
				messagesMetrics,
				responsesMetrics,
				geminiMetrics,
				chatMetrics,
				imagesMetrics,
				session.NewTraceAffinityManager(),
				warmup.NewURLManager(30*time.Second, 3),
			)
			rateLimitManager := ratelimit.NewManager()
			defer rateLimitManager.Stop()
			channelScheduler.SetRateLimitManager(rateLimitManager)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.5"}`))

			upstream := &cfg.Upstream[0]
			handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
				c,
				config.NewEnvConfig(),
				cfgManager,
				channelScheduler,
				scheduler.ChannelKindMessages,
				"Messages",
				messagesMetrics,
				upstream,
				[]warmup.URLLatencyResult{{URL: upstream.BaseURL}},
				[]byte(`{"model":"gpt-5.5","messages":[]}`),
				nil,
				false,
				func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
					if failedKeys[upstream.APIKeys[0]] {
						return "", nil
					}
					return upstream.APIKeys[0], nil
				},
				func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
					return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(`{}`))
				},
				func(apiKey string) {},
				nil,
				nil,
				func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
					t.Fatal("overloaded response should not call handleSuccess")
					return nil, nil
				},
				"gpt-5.5",
				"",
				0,
				channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
			)

			if handled {
				t.Fatal("overloaded channel should return unhandled to allow channel failover")
			}
			if successKey != "" {
				t.Fatalf("successKey = %q, want empty", successKey)
			}
			if failoverErr == nil || failoverErr.Status != http.StatusServiceUnavailable || string(failoverErr.Body) != tt.upstreamBody {
				t.Fatalf("failoverErr = %#v, want original 503 body", failoverErr)
			}
			if lastErr == nil {
				t.Fatal("lastErr should record upstream 503")
			}

			serviceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindMessages, upstream.ServiceType)
			if got := messagesMetrics.GetKeyCircuitState(upstream.BaseURL, upstream.APIKeys[0], serviceType); got != metrics.CircuitStateOpen {
				t.Fatalf("circuit state = %v, want %v", got, metrics.CircuitStateOpen)
			}
			if deferred, _, cooldown := channelScheduler.ShouldDeferForRateLimit(scheduler.ChannelKindMessages, 0, "", ratelimit.Config{}, time.Now()); !deferred || !cooldown {
				t.Fatalf("channel cooldown deferred=%v cooldown=%v, want both true", deferred, cooldown)
			}
		})
	}
}

func TestTryUpstreamWithAllKeysRecordsSelectionTrace(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "trace-messages",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-trace"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-trace"}`))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		[]byte(`{"model":"gpt-trace","messages":[]}`),
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(`{}`))
		},
		func(apiKey string) {},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"gpt-trace",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
		WithSelectionTrace(&scheduler.SelectionResult{
			Reason: "priority_order",
			Trace: &scheduler.SelectionTrace{
				Stages: []scheduler.SelectionTraceStage{
					{Name: "active_model_filter", Count: 1},
				},
				Selected: &scheduler.SelectionTraceSelection{
					ChannelIndex: 0,
					ChannelName:  "trace-messages",
					Reason:       "priority_order",
				},
			},
		}),
	)

	if !handled {
		t.Fatal("successful upstream response should be handled")
	}
	if successKey != "sk-trace" {
		t.Fatalf("successKey = %q, want sk-trace", successKey)
	}
	if failoverErr != nil {
		t.Fatalf("failoverErr = %#v, want nil", failoverErr)
	}
	if lastErr != nil {
		t.Fatalf("lastErr = %v, want nil", lastErr)
	}

	serviceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindMessages, upstream.ServiceType)
	logs := channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages).Get(metrics.GenerateMetricsIdentityKey(server.URL, "sk-trace", serviceType))
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].SelectionReason != "priority_order" {
		t.Fatalf("selectionReason = %q, want priority_order", logs[0].SelectionReason)
	}
	if !strings.Contains(logs[0].SelectionTraceSummary, "selected=0:trace-messages/priority_order") {
		t.Fatalf("selectionTraceSummary = %q, want selected channel summary", logs[0].SelectionTraceSummary)
	}
}

func TestTryUpstreamWithAllKeysRetriesShortEmptyResponseOnSameKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "short-empty-messages",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-empty-1", "sk-empty-2"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"glm-5.1"}`))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	var handleCalls int
	usedKeys := make([]string, 0, 2)
	urlFailures := 0
	urlSuccesses := 0

	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		[]byte(`{"model":"glm-5.1","messages":[]}`),
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(`{}`))
		},
		func(apiKey string) {},
		func(url string) { urlFailures++ },
		func(url string) { urlSuccesses++ },
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			handleCalls++
			usedKeys = append(usedKeys, apiKey)
			if handleCalls == 1 {
				return nil, ErrEmptyNonStreamResponse
			}
			return nil, nil
		},
		"glm-5.1",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if !handled {
		t.Fatal("短空响应同 Key 重试成功后应处理完成")
	}
	if successKey != "sk-empty-1" {
		t.Fatalf("successKey = %q, want sk-empty-1", successKey)
	}
	if failoverErr != nil {
		t.Fatalf("failoverErr = %#v, want nil", failoverErr)
	}
	if lastErr != nil {
		t.Fatalf("lastErr = %v, want nil", lastErr)
	}
	if handleCalls != 2 {
		t.Fatalf("handleCalls = %d, want 2", handleCalls)
	}
	if len(usedKeys) != 2 || usedKeys[0] != "sk-empty-1" || usedKeys[1] != "sk-empty-1" {
		t.Fatalf("usedKeys = %v, want same key retry", usedKeys)
	}
	if urlFailures != 0 {
		t.Fatalf("urlFailures = %d, want 0", urlFailures)
	}
	if urlSuccesses != 1 {
		t.Fatalf("urlSuccesses = %d, want 1", urlSuccesses)
	}
	if cfgManager.IsKeyFailed("sk-empty-1", "Messages") {
		t.Fatal("第一次短空响应内部重试不应标记 Key 失败")
	}

	serviceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindMessages, upstream.ServiceType)
	keyMetrics := messagesMetrics.GetKeyMetrics(server.URL, "sk-empty-1", serviceType)
	if keyMetrics == nil {
		t.Fatal("expected key metrics")
	}
	if keyMetrics.RequestCount != 1 || keyMetrics.SuccessCount != 1 || keyMetrics.FailureCount != 0 {
		t.Fatalf("metrics = requests:%d success:%d failure:%d, want 1/1/0",
			keyMetrics.RequestCount, keyMetrics.SuccessCount, keyMetrics.FailureCount)
	}
}

func TestTryUpstreamWithAllKeysMarksKeyFailedAfterRepeatedShortEmptyResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "repeated-empty-messages",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-empty-1"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"glm-5.1"}`))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	handleCalls := 0
	urlFailures := 0

	handled, successKey, _, _, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		[]byte(`{"model":"glm-5.1","messages":[]}`),
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(`{}`))
		},
		func(apiKey string) {},
		func(url string) { urlFailures++ },
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			handleCalls++
			return nil, ErrEmptyNonStreamResponse
		},
		"glm-5.1",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if handled {
		t.Fatal("连续短空响应不应处理完成，应交给外层渠道 failover")
	}
	if successKey != "" {
		t.Fatalf("successKey = %q, want empty", successKey)
	}
	if !errors.Is(lastErr, ErrEmptyNonStreamResponse) {
		t.Fatalf("lastErr = %v, want ErrEmptyNonStreamResponse", lastErr)
	}
	if handleCalls != 2 {
		t.Fatalf("handleCalls = %d, want 2", handleCalls)
	}
	if urlFailures != 1 {
		t.Fatalf("urlFailures = %d, want 1", urlFailures)
	}
	if !cfgManager.IsKeyFailed("sk-empty-1", "Messages") {
		t.Fatal("连续短空响应后应标记 Key 失败")
	}

	serviceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindMessages, upstream.ServiceType)
	keyMetrics := messagesMetrics.GetKeyMetrics(server.URL, "sk-empty-1", serviceType)
	if keyMetrics == nil {
		t.Fatal("expected key metrics")
	}
	if keyMetrics.RequestCount != 1 || keyMetrics.SuccessCount != 0 || keyMetrics.FailureCount != 1 {
		t.Fatalf("metrics = requests:%d success:%d failure:%d, want 1/0/1",
			keyMetrics.RequestCount, keyMetrics.SuccessCount, keyMetrics.FailureCount)
	}
}

func TestClientWriteDisconnectDoesNotFailKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {}\n\n"))
	}))
	defer server.Close()

	for _, tc := range []struct {
		name        string
		err         error
		wantFailure bool
	}{
		{"client write", &net.OpError{Op: "write", Err: syscall.EPIPE}, false},
		{"upstream read", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgManager, sch, mm, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
				Name: "disconnect-test", BaseURL: server.URL, APIKeys: []string{"sk-test"}, Status: "active", ServiceType: "openai",
			})
			defer cleanup()
			cfg := cfgManager.GetConfig()
			upstream := &cfg.Upstream[0]
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.5","stream":true}`))
			calls := 0
			TryUpstreamWithAllKeys(c, config.NewEnvConfig(), cfgManager, sch, scheduler.ChannelKindMessages,
				"Messages", mm, upstream, []warmup.URLLatencyResult{{URL: server.URL}},
				[]byte(`{"model":"gpt-5.5","messages":[],"stream":true}`), nil, true,
				func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
					return cfgManager.GetNextAPIKey(up, failed, "Messages")
				},
				func(c *gin.Context, up *config.UpstreamConfig, _ string) (*http.Request, error) {
					return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, up.BaseURL, strings.NewReader(`{}`))
				}, nil, nil, nil,
				func(c *gin.Context, _ *http.Response, _ *config.UpstreamConfig, _ string, _ []byte) (*types.Usage, error) {
					calls++
					c.Status(http.StatusOK)
					return nil, tc.err
				}, "gpt-5.5", "", 0, sch.GetChannelLogStore(scheduler.ChannelKindMessages))
			if calls != 1 {
				t.Fatalf("response handler called %d times, want 1", calls)
			}
			if got := cfgManager.IsKeyFailed("sk-test", "Messages"); got != tc.wantFailure {
				t.Fatalf("key failed = %v, want %v", got, tc.wantFailure)
			}
		})
	}
}

func TestDisabledCircuitBreakerStillAttemptsOpenKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {}\n\n"))
	}))
	defer server.Close()
	cfgManager, sch, mm, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name: "unstable", BaseURL: server.URL, APIKeys: []string{"sk-test"}, Status: "active",
		ServiceType: "openai", CircuitBreakerDisabled: true,
	})
	defer cleanup()
	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	serviceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindMessages, upstream.ServiceType)
	for i := 0; i < 6; i++ {
		mm.RecordFailure(server.URL, "sk-test", serviceType)
	}
	if mm.GetKeyCircuitState(server.URL, "sk-test", serviceType) != metrics.CircuitStateOpen {
		t.Fatal("test setup should have an open key circuit")
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.5"}`))
	handled, _, _, _, _, err := TryUpstreamWithAllKeys(c, config.NewEnvConfig(), cfgManager, sch,
		scheduler.ChannelKindMessages, "Messages", mm, upstream,
		[]warmup.URLLatencyResult{{URL: server.URL}}, []byte(`{"model":"gpt-5.5","messages":[]}`), nil, false,
		func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(up, failed, "Messages")
		},
		func(c *gin.Context, up *config.UpstreamConfig, _ string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, up.BaseURL, strings.NewReader(`{}`))
		}, nil, nil, nil,
		func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, _ string, _ []byte) (*types.Usage, error) {
			defer resp.Body.Close()
			return nil, nil
		}, "gpt-5.5", "", 0, sch.GetChannelLogStore(scheduler.ChannelKindMessages))
	if err != nil || !handled {
		t.Fatalf("disabled breaker should attempt open key: handled=%v err=%v", handled, err)
	}
}

func newTestFailoverDependencies(t *testing.T, upstream config.UpstreamConfig) (*config.ConfigManager, *scheduler.ChannelScheduler, *metrics.MetricsManager, func()) {
	t.Helper()

	cfg := config.Config{Upstream: []config.UpstreamConfig{upstream}}
	tmpDir, err := os.MkdirTemp("", "upstream-failover-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}

	configPath := filepath.Join(tmpDir, "config.json")
	cfgData, err := json.Marshal(cfg)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configPath, cfgData, 0644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("写入配置失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("创建配置管理器失败: %v", err)
	}

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()

	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		messagesMetrics,
		responsesMetrics,
		geminiMetrics,
		chatMetrics,
		imagesMetrics,
		session.NewTraceAffinityManager(),
		warmup.NewURLManager(30*time.Second, 3),
	)

	cleanup := func() {
		cfgManager.Close()
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		imagesMetrics.Stop()
		os.RemoveAll(tmpDir)
	}

	return cfgManager, channelScheduler, messagesMetrics, cleanup
}
