package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/keypool"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
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
			name:     "messages inherits default enabled",
			kind:     scheduler.ChannelKindMessages,
			upstream: &config.UpstreamConfig{},
			want:     true,
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

func TestNormalizeExecutionRouteDefaultsAndOverrides(t *testing.T) {
	upstream := &config.UpstreamConfig{ChannelUID: "ch-physical"}
	legacy := normalizeExecutionRoute(scheduler.ChannelRouteRef{}, scheduler.ChannelKindMessages, 4, upstream)
	if legacy.Kind != string(scheduler.ChannelKindMessages) || legacy.Index != 4 || legacy.ChannelUID != upstream.ChannelUID {
		t.Fatalf("legacy route = %+v", legacy)
	}

	routed := normalizeExecutionRoute(scheduler.ChannelRouteRef{Kind: string(scheduler.ChannelKindResponses), Index: 2, ChannelUID: "ch-routed"}, scheduler.ChannelKindMessages, 4, upstream)
	if routed.Kind != string(scheduler.ChannelKindResponses) || routed.Index != 2 || routed.ChannelUID != "ch-routed" {
		t.Fatalf("explicit route = %+v", routed)
	}
}

func TestChannelAPITypeUsesExecutionKind(t *testing.T) {
	tests := map[scheduler.ChannelKind]string{
		scheduler.ChannelKindMessages:  "Messages",
		scheduler.ChannelKindChat:      "Chat",
		scheduler.ChannelKindResponses: "Responses",
	}
	for kind, want := range tests {
		if got := ChannelAPIType(kind); got != want {
			t.Fatalf("ChannelAPIType(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestEffortInjectionStylePrefersPhysicalServiceType(t *testing.T) {
	tests := []struct {
		serviceType string
		want        string
	}{
		{serviceType: "openai", want: "reasoning_effort"},
		{serviceType: "responses", want: "reasoning"},
		{serviceType: "claude", want: "thinking"},
	}
	for _, tt := range tests {
		if got := effortInjectionStyle(scheduler.ChannelKindMessages, &config.UpstreamConfig{ServiceType: tt.serviceType}); got != tt.want {
			t.Fatalf("serviceType=%q style=%q, want %q", tt.serviceType, got, tt.want)
		}
	}
}

func TestApplyAdaptiveResponseHeaderTimeoutHonorsModeAndOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	activePolicy := &autopilot.EndpointAttemptPolicy{
		Mode: autopilot.RoutingModeActive,
		ResponseHeaderTimeoutForEndpoint: func(_ string, inheritedMs int, isStream bool) int {
			if inheritedMs != 120_000 {
				t.Fatalf("inheritedMs = %d, want 120000", inheritedMs)
			}
			if !isStream {
				t.Fatal("expected stream flag")
			}
			return 30_000
		},
	}

	tests := []struct {
		name        string
		upstream    *config.UpstreamConfig
		policy      *autopilot.EndpointAttemptPolicy
		wantTimeout int
		wantCopy    bool
	}{
		{
			name:        "active auto-managed applies suggestion",
			upstream:    &config.UpstreamConfig{AutoManaged: true, ChannelUID: "ch-auto"},
			policy:      activePolicy,
			wantTimeout: 30_000,
			wantCopy:    true,
		},
		{
			name:     "shadow only observes suggestion",
			upstream: &config.UpstreamConfig{AutoManaged: true, ChannelUID: "ch-auto"},
			policy: &autopilot.EndpointAttemptPolicy{
				Mode:                             autopilot.RoutingModeShadow,
				ResponseHeaderTimeoutForEndpoint: func(string, int, bool) int { return 30_000 },
			},
		},
		{
			name:     "manual channel keeps inherited timeout",
			upstream: &config.UpstreamConfig{ChannelUID: "ch-manual"},
			policy:   activePolicy,
		},
		{
			name:        "explicit channel timeout wins",
			upstream:    &config.UpstreamConfig{AutoManaged: true, ChannelUID: "ch-auto", ResponseHeaderTimeoutMs: 90_000},
			policy:      activePolicy,
			wantTimeout: 90_000,
		},
		{
			name:     "panicking suggestion fails open",
			upstream: &config.UpstreamConfig{AutoManaged: true, ChannelUID: "ch-auto"},
			policy: &autopilot.EndpointAttemptPolicy{
				Mode: autopilot.RoutingModeActive,
				ResponseHeaderTimeoutForEndpoint: func(string, int, bool) int {
					panic("test panic")
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstreamCopy := tt.upstream.Clone()
			got := applyAdaptiveResponseHeaderTimeout(c, "Messages", tt.policy, tt.upstream, upstreamCopy, "https://example.com", "sk-test", 120_000, true)
			if tt.wantCopy && got != upstreamCopy {
				t.Fatal("expected adaptive runtime copy")
			}
			if !tt.wantCopy && got != tt.upstream {
				t.Fatal("expected original upstream")
			}
			if got.ResponseHeaderTimeoutMs != tt.wantTimeout {
				t.Fatalf("ResponseHeaderTimeoutMs = %d, want %d", got.ResponseHeaderTimeoutMs, tt.wantTimeout)
			}
		})
	}
}

func TestPlainAPIKeySelectionSkipsDisabledModel(t *testing.T) {
	upstream := &config.UpstreamConfig{
		Name:    "plain-keys",
		BaseURL: "https://example.com",
		APIKeys: []string{"sk-disabled", "sk-allowed"},
		DisabledKeyModels: []config.DisabledKeyModelInfo{
			{
				Key:       "sk-disabled",
				Model:     "target-model",
				RecoverAt: time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		},
	}
	fallback := func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
		for _, key := range upstream.APIKeys {
			if !failedKeys[key] {
				return key, nil
			}
		}
		return "", errors.New("no key")
	}

	tests := []struct {
		name   string
		policy *autopilot.EndpointAttemptPolicy
	}{
		{name: "without endpoint policy"},
		{name: "with endpoint policy", policy: &autopilot.EndpointAttemptPolicy{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var key string
			var err error
			if tt.policy == nil {
				_, key, err = selectAttemptAPIKey(nil, scheduler.ChannelKindMessages, 0, upstream, map[string]bool{}, map[string]bool{}, "target-model", fallback, nil, nil)
			} else {
				_, key, err = selectAttemptAPIKeyFiltered(nil, scheduler.ChannelKindMessages, 0, upstream, upstream.BaseURL, map[string]bool{}, map[string]bool{}, "target-model", fallback, tt.policy, "Messages", nil, nil, nil, "")
			}
			if err != nil {
				t.Fatalf("select key error: %v", err)
			}
			if key != "sk-allowed" {
				t.Fatalf("selected key = %q, want sk-allowed", key)
			}
		})
	}
}

func TestPlainAPIKeySelectionSkipsDisabledKey(t *testing.T) {
	upstream := &config.UpstreamConfig{
		Name:    "managed-plain-keys",
		BaseURL: "https://example.com",
		APIKeys: []string{"sk-disabled", "sk-allowed"},
		DisabledAPIKeys: []config.DisabledKeyInfo{
			{
				Key:       "sk-disabled",
				RecoverAt: time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		},
	}
	fallback := func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
		for _, key := range upstream.APIKeys {
			if !failedKeys[key] {
				return key, nil
			}
		}
		return "", errors.New("no key")
	}

	tests := []struct {
		name   string
		policy *autopilot.EndpointAttemptPolicy
	}{
		{name: "without endpoint policy"},
		{name: "with endpoint policy", policy: &autopilot.EndpointAttemptPolicy{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var key string
			var err error
			if tt.policy == nil {
				_, key, err = selectAttemptAPIKey(nil, scheduler.ChannelKindMessages, 0, upstream, map[string]bool{}, map[string]bool{}, "target-model", fallback, nil, nil)
			} else {
				_, key, err = selectAttemptAPIKeyFiltered(nil, scheduler.ChannelKindMessages, 0, upstream, upstream.BaseURL, map[string]bool{}, map[string]bool{}, "target-model", fallback, tt.policy, "Messages", nil, nil, nil, "")
			}
			if err != nil {
				t.Fatalf("select key error: %v", err)
			}
			if key != "sk-allowed" {
				t.Fatalf("selected key = %q, want sk-allowed", key)
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
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

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
	defer errutil.IgnoreDeferred(cfgManager.Close)

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

func TestTryUpstreamWithAllKeysOverloadedCooldownSingleModelNoCircuit(t *testing.T) {
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
			defer func() {
				_ = os.RemoveAll(tmpDir)
			}()

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
			defer errutil.IgnoreDeferred(cfgManager.Close)

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

			upstream := &cfg.Upstream[0]

			// 单一模型过载：触发渠道 cooldown 与 failover，但不熔断整个 Key
			//（模型多样性门槛要求失败跨多个模型才熔断 Key）。
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.5"}`))

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

			// 单一模型过载不熔断 Key，但渠道进入 cooldown
			serviceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindMessages, upstream.ServiceType)
			if got := messagesMetrics.GetKeyCircuitState(upstream.BaseURL, upstream.APIKeys[0], serviceType); got != metrics.CircuitStateClosed {
				t.Fatalf("circuit state = %v, want %v (single-model overload must not open circuit)", got, metrics.CircuitStateClosed)
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

func TestTryUpstreamWithAllKeysLogsFinalRequestModelAndReasoningEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type upstreamRequest struct {
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoning_effort"`
	}
	upstreamRequests := make(chan upstreamRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request upstreamRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode upstream request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		upstreamRequests <- request
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "auto-mapped-log-test",
		ChannelUID:  "ch-auto-mapped-log",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-auto-mapped"},
		Status:      "active",
		ServiceType: "openai",
		AutoManaged: true,
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	originalBody := []byte(`{"model":"claude-opus-4-8","messages":[],"reasoning_effort":"high"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(originalBody))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	policy := &autopilot.EndpointAttemptPolicy{
		ResolvedTargetForBinding: func(channelUID, baseURL, apiKey string) (*autopilot.ResolvedRouteTarget, string) {
			if channelUID != upstream.ChannelUID || baseURL != server.URL || apiKey != "sk-auto-mapped" {
				t.Fatalf("unexpected binding identity: channel=%q url=%q key=%q", channelUID, baseURL, apiKey)
			}
			return &autopilot.ResolvedRouteTarget{Model: "glm-5.2"}, ""
		},
	}

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
		originalBody,
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			actualBody, err := sjson.SetBytes(GetEffectiveRequestBody(c, nil), "reasoning_effort", "max")
			if err != nil {
				return nil, err
			}
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, bytes.NewReader(actualBody))
		},
		func(string) {},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"claude-opus-4-8",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
		WithEndpointAttemptPolicy(policy),
	)

	if !handled || successKey != "sk-auto-mapped" || failoverErr != nil || lastErr != nil {
		t.Fatalf("unexpected failover result: handled=%v key=%q failoverErr=%v lastErr=%v", handled, successKey, failoverErr, lastErr)
	}

	gotRequest := <-upstreamRequests
	if gotRequest.Model != "glm-5.2" || gotRequest.ReasoningEffort != "max" {
		t.Fatalf("upstream request = %+v, want model=glm-5.2 reasoning_effort=max", gotRequest)
	}

	serviceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindMessages, upstream.ServiceType)
	metricsKey := metrics.GenerateMetricsIdentityKey(server.URL, "sk-auto-mapped", serviceType)
	logs := channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages).Get(metricsKey)
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if got := logs[0]; got.Model != "glm-5.2" || got.OriginalModel != "claude-opus-4-8" ||
		got.OriginalReasoningEffort != "high" || got.ActualReasoningEffort != "max" {
		t.Fatalf("channel log = %+v, want final model and both reasoning efforts", got)
	}

	modelStats := messagesMetrics.GetModelStatsHistory(time.Hour, time.Minute)
	if _, ok := modelStats["glm-5.2"]; !ok {
		t.Fatalf("model metrics missing final model: %v", modelStats)
	}
	if _, ok := modelStats["claude-opus-4-8"]; ok {
		t.Fatalf("model metrics should not record requested model: %v", modelStats)
	}

	// 回显头必须在响应写出前设置：非流式 handleSuccess 一旦写出再补头就丢失。
	// 此处 handleSuccess 未写体，但 header 已在调用前落到 recorder 上即可证明时序。
	if got := w.Header().Get("X-CCX-Mapped-Model"); got != "glm-5.2" {
		t.Fatalf("X-CCX-Mapped-Model = %q, want glm-5.2", got)
	}
	if got := w.Header().Get("X-CCX-Original-Model"); got != "claude-opus-4-8" {
		t.Fatalf("X-CCX-Original-Model = %q, want claude-opus-4-8", got)
	}
	if got := w.Header().Get("X-CCX-Mapping-Source"); got != "auto_resolve" {
		t.Fatalf("X-CCX-Mapping-Source = %q, want auto_resolve", got)
	}
}

// TestTryUpstreamWithAllKeysMappedModelSurvivesSystemNormalization 回归：
// auto_resolve 改写模型后，若请求体带 inline system 消息触发归一化（normChanged=true），
// 归一化必须基于已改写的 body 进行。此前改写结果因 := 影子变量只进入 gin context，
// 归一化用外层旧 body 再次 Set 把模型改写覆盖，最终以原模型发出（火山渠道 400 根因）。
func TestTryUpstreamWithAllKeysMappedModelSurvivesSystemNormalization(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamRequests := make(chan map[string]interface{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode upstream request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		upstreamRequests <- request
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "auto-mapped-normalize-test",
		ChannelUID:  "ch-auto-mapped-normalize",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-auto-mapped-norm"},
		Status:      "active",
		ServiceType: "openai",
		AutoManaged: true,
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// inline system 消息确保归一化真正改写 body（normChanged=true），
	// 这是旧遮蔽 bug 下覆盖模型改写的必要条件。
	originalBody := []byte(`{"model":"claude-opus-4-8","messages":[{"role":"system","content":"You are helpful."},{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(originalBody))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	policy := &autopilot.EndpointAttemptPolicy{
		ResolvedTargetForBinding: func(channelUID, baseURL, apiKey string) (*autopilot.ResolvedRouteTarget, string) {
			return &autopilot.ResolvedRouteTarget{Model: "glm-5.2"}, ""
		},
	}

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
		originalBody,
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL,
				bytes.NewReader(GetEffectiveRequestBody(c, nil)))
		},
		func(string) {},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"claude-opus-4-8",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
		WithEndpointAttemptPolicy(policy),
	)

	if !handled || successKey != "sk-auto-mapped-norm" || failoverErr != nil || lastErr != nil {
		t.Fatalf("unexpected failover result: handled=%v key=%q failoverErr=%v lastErr=%v", handled, successKey, failoverErr, lastErr)
	}

	gotRequest := <-upstreamRequests
	if gotRequest["model"] != "glm-5.2" {
		t.Fatalf("upstream request model = %v, want glm-5.2 (模型改写被后续归一化覆盖)", gotRequest["model"])
	}
	// 归一化仍应生效：inline system 被抽到顶层，messages 中不再含 system 角色。
	if gotRequest["system"] == nil {
		t.Fatalf("upstream request missing top-level system: %v", gotRequest)
	}
	if msgs, ok := gotRequest["messages"].([]interface{}); ok {
		for _, m := range msgs {
			if mm, ok := m.(map[string]interface{}); ok && mm["role"] == "system" {
				t.Fatalf("inline system message should have been normalized to top-level: %v", msgs)
			}
		}
	}
}

// TestTryUpstreamWithAllKeysLogsMappingFailReason 自动映射未命中 fail-open 透传时，
// 未命中原因必须落到 channel log（mappingFailReason）且不设置映射回显头。
func TestTryUpstreamWithAllKeysLogsMappingFailReason(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamRequests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode upstream request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		upstreamRequests <- request.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "auto-mapping-fail-test",
		ChannelUID:  "ch-auto-mapping-fail",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-fail-open"},
		Status:      "active",
		ServiceType: "openai",
		AutoManaged: true,
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	originalBody := []byte(`{"model":"claude-opus-4-8","messages":[]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(originalBody))

	policy := &autopilot.EndpointAttemptPolicy{
		ResolvedTargetForBinding: func(channelUID, baseURL, apiKey string) (*autopilot.ResolvedRouteTarget, string) {
			return nil, "no_capable_model"
		},
	}

	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		&cfgManager.GetConfig().Upstream[0],
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		originalBody,
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, bytes.NewReader(GetEffectiveRequestBody(c, nil)))
		},
		func(string) {},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"claude-opus-4-8",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
		WithEndpointAttemptPolicy(policy),
	)

	if !handled || successKey != "sk-fail-open" || failoverErr != nil || lastErr != nil {
		t.Fatalf("unexpected failover result: handled=%v key=%q failoverErr=%v lastErr=%v", handled, successKey, failoverErr, lastErr)
	}

	// fail-open 语义：上游收到的是原始模型名。
	if got := <-upstreamRequests; got != "claude-opus-4-8" {
		t.Fatalf("upstream request model = %q, want original claude-opus-4-8 (fail-open)", got)
	}

	serviceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindMessages, "openai")
	metricsKey := metrics.GenerateMetricsIdentityKey(server.URL, "sk-fail-open", serviceType)
	logs := channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages).Get(metricsKey)
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if got := logs[0]; got.Model != "claude-opus-4-8" || got.OriginalModel != "" || got.MappingFailReason != "no_capable_model" {
		t.Fatalf("channel log = %+v, want passthrough model with mappingFailReason=no_capable_model", got)
	}

	// 未命中映射时不得残留映射回显头。
	for _, header := range []string{"X-CCX-Mapped-Model", "X-CCX-Original-Model", "X-CCX-Mapping-Source"} {
		if got := w.Header().Get(header); got != "" {
			t.Fatalf("%s = %q, want empty on mapping miss", header, got)
		}
	}
}

// (Key,模型) 持久化限制复查：autopilot 自动映射的目标模型命中 DisabledKeyModels 时，
// 发送前复查必须跳过该 Key 并 failover 到同渠道的下一个 Key，而不是照常发出请求。
func TestTryUpstreamWithAllKeysSkipsKeyModelDisabledMappedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamRequests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode upstream request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		upstreamRequests <- request.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	// sk-a 被限制调用映射后模型 glm-5.2（映射前模型 claude-opus-4-8 不在限制内，
	// 选 Key 阶段查不出来）；sk-b 无限制。
	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "auto-mapped-keymodel-test",
		ChannelUID:  "ch-auto-mapped-keymodel",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-a", "sk-b"},
		Status:      "active",
		ServiceType: "openai",
		AutoManaged: true,
		DisabledKeyModels: []config.DisabledKeyModelInfo{
			{Key: "sk-a", Model: "glm-5.2", Reason: "model_not_found", RecoverAt: time.Now().Add(time.Hour).Format(time.RFC3339)},
		},
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	originalBody := []byte(`{"model":"claude-opus-4-8","messages":[]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(originalBody))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	policy := &autopilot.EndpointAttemptPolicy{
		ResolvedTargetForBinding: func(channelUID, baseURL, apiKey string) (*autopilot.ResolvedRouteTarget, string) {
			return &autopilot.ResolvedRouteTarget{Model: "glm-5.2"}, ""
		},
	}

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
		originalBody,
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, bytes.NewReader(GetEffectiveRequestBody(c, nil)))
		},
		func(string) {},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"claude-opus-4-8",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
		WithEndpointAttemptPolicy(policy),
	)

	if !handled || successKey != "sk-b" || failoverErr != nil || lastErr != nil {
		t.Fatalf("unexpected failover result: handled=%v key=%q failoverErr=%v lastErr=%v", handled, successKey, failoverErr, lastErr)
	}

	// 受限的 sk-a 不得发出任何请求：上游只应收到 sk-b 的一次调用。
	select {
	case got := <-upstreamRequests:
		if got != "glm-5.2" {
			t.Fatalf("upstream request model = %q, want glm-5.2", got)
		}
	default:
		t.Fatal("expected exactly one upstream request")
	}
	select {
	case extra := <-upstreamRequests:
		t.Fatalf("unexpected extra upstream request (restricted key was used?): model=%q", extra)
	default:
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

func TestTryUpstreamWithAllKeysRetriesAfterResponseHeaderTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)

	attemptedKeys := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		attemptedKeys <- apiKey
		if apiKey == "sk-slow" {
			select {
			case <-time.After(100 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "response-header-timeout-messages",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-slow", "sk-fast"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"glm-5.2"}`))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	// 直接覆盖测试副本，避免配置校验的生产最小值让单测等待 1 秒。
	upstream.ResponseHeaderTimeoutMs = 20
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
		[]byte(`{"model":"glm-5.2","messages":[]}`),
		nil,
		false,
		func(_ *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			for _, key := range []string{"sk-slow", "sk-fast"} {
				if !failedKeys[key] {
					return key, nil
				}
			}
			return "", errors.New("no available key")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(`{}`))
			if err == nil {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}
			return req, err
		},
		func(string) {},
		func(string) { urlFailures++ },
		func(string) { urlSuccesses++ },
		func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, _ string, _ []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"glm-5.2",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if !handled || successKey != "sk-fast" {
		t.Fatalf("handled = %v, successKey = %q, want true/sk-fast", handled, successKey)
	}
	if failoverErr != nil || lastErr != nil {
		t.Fatalf("failoverErr = %#v, lastErr = %v, want nil/nil", failoverErr, lastErr)
	}
	if urlFailures != 1 || urlSuccesses != 1 {
		t.Fatalf("url failures/successes = %d/%d, want 1/1", urlFailures, urlSuccesses)
	}
	if len(attemptedKeys) != 2 {
		t.Fatalf("attempt count = %d, want 2", len(attemptedKeys))
	}
	if first, second := <-attemptedKeys, <-attemptedKeys; first != "sk-slow" || second != "sk-fast" {
		t.Fatalf("attempted keys = [%s %s], want [sk-slow sk-fast]", first, second)
	}
	if !cfgManager.IsKeyFailed("sk-slow", "Messages") {
		t.Fatal("响应头超时的 Key 应被标记失败")
	}
	serviceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindMessages, upstream.ServiceType)
	stats := messagesMetrics.GetTimeWindowStatsForKey(server.URL, "sk-fast", serviceType, time.Hour)
	if stats.FirstByteSampleCount != 1 || stats.P95FirstByteLatencyMs <= 0 {
		t.Fatalf("successful failover TTFB = samples:%d p95:%dms, want one positive sample",
			stats.FirstByteSampleCount, stats.P95FirstByteLatencyMs)
	}
}

func TestTryUpstreamWithAllKeysStopsOnClientCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	attemptedKeys := make(chan string, 2)
	requestStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		attemptedKeys <- strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		requestStarted <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "client-cancel-messages",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-first", "sk-second"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	requestContext, cancel := context.WithCancel(context.Background())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"glm-5.2"}`)).WithContext(requestContext)
	go func() {
		<-requestStarted
		cancel()
	}()

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	upstream.ResponseHeaderTimeoutMs = 200
	urlFailures := 0

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
		[]byte(`{"model":"glm-5.2","messages":[]}`),
		nil,
		false,
		func(_ *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			for _, key := range []string{"sk-first", "sk-second"} {
				if !failedKeys[key] {
					return key, nil
				}
			}
			return "", errors.New("no available key")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(`{}`))
			if err == nil {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}
			return req, err
		},
		func(string) {},
		func(string) { urlFailures++ },
		nil,
		func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, _ string, _ []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"glm-5.2",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if !handled || successKey != "" {
		t.Fatalf("handled = %v, successKey = %q, want true/empty", handled, successKey)
	}
	if failoverErr != nil || !errors.Is(lastErr, context.Canceled) {
		t.Fatalf("failoverErr = %#v, lastErr = %v, want nil/context.Canceled", failoverErr, lastErr)
	}
	if urlFailures != 0 {
		t.Fatalf("urlFailures = %d, want 0", urlFailures)
	}
	if len(attemptedKeys) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(attemptedKeys))
	}
	if first := <-attemptedKeys; first != "sk-first" {
		t.Fatalf("attempted key = %q, want sk-first", first)
	}
	if cfgManager.IsKeyFailed("sk-first", "Messages") {
		t.Fatal("客户端取消不应标记上游 Key 失败")
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
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configPath, cfgData, 0644); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("写入配置失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		_ = os.RemoveAll(tmpDir)
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
		_ = cfgManager.Close()
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		imagesMetrics.Stop()
		_ = os.RemoveAll(tmpDir)
	}

	return cfgManager, channelScheduler, messagesMetrics, cleanup
}

type accountRateLimitFailoverRun struct {
	handled        bool
	successKey     string
	failoverErr    *FailoverError
	lastErr        error
	attemptedKeys  []string
	accountSignals int
	signalReasons  []string
	cfgManager     *config.ConfigManager
	scheduler      *scheduler.ChannelScheduler
	limiterManager *ratelimit.Manager
	metricsManager *metrics.MetricsManager
	upstream       config.UpstreamConfig
}

func runAccountRateLimitFailover(
	t *testing.T,
	upstream config.UpstreamConfig,
	respond func(apiKey string) (int, string),
) accountRateLimitFailoverRun {
	t.Helper()
	attempted := make(chan string, len(upstream.APIKeys)+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-Test-Key")
		attempted <- apiKey
		status, body := respond(apiKey)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	upstream.BaseURL = server.URL
	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, upstream)
	t.Cleanup(cleanup)
	limiterManager := ratelimit.NewManager()
	t.Cleanup(limiterManager.Stop)
	channelScheduler.SetRateLimitManager(limiterManager)

	originalSignalCallback := ratelimit.UpstreamSignalCallback
	var accountSignals int
	var signalReasons []string
	ratelimit.SetUpstreamSignalCallback(func(_ string, _ string, _ string, _ string, _ string, _ bool, _ int64, _ http.Header, statusCode int, reason string) {
		if statusCode == http.StatusTooManyRequests {
			accountSignals++
			signalReasons = append(signalReasons, reason)
		}
	})
	t.Cleanup(func() { ratelimit.SetUpstreamSignalCallback(originalSignalCallback) })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test-model"}`))
	cfg := cfgManager.GetConfig()
	runtimeUpstream := &cfg.Upstream[0]
	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		runtimeUpstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		[]byte(`{"model":"test-model","messages":[]}`),
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(`{}`))
			if err == nil {
				req.Header.Set("X-Test-Key", apiKey)
			}
			return req, err
		},
		func(string) {},
		nil,
		nil,
		func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, _ string, _ []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"test-model",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	close(attempted)
	attemptedKeys := make([]string, 0, len(attempted))
	for key := range attempted {
		attemptedKeys = append(attemptedKeys, key)
	}
	return accountRateLimitFailoverRun{
		handled:        handled,
		successKey:     successKey,
		failoverErr:    failoverErr,
		lastErr:        lastErr,
		attemptedKeys:  attemptedKeys,
		accountSignals: accountSignals,
		signalReasons:  signalReasons,
		cfgManager:     cfgManager,
		scheduler:      channelScheduler,
		limiterManager: limiterManager,
		metricsManager: messagesMetrics,
		upstream:       *runtimeUpstream,
	}
}

func TestTryUpstreamWithAllKeys_AccountRateLimitRetriesIndependentKey(t *testing.T) {
	upstream := config.UpstreamConfig{
		Name:       "volc-independent-keys",
		ChannelUID: "channel-independent",
		APIKeys:    []string{"sk-account-a", "sk-account-b"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-account-a", Name: "account-a"},
			{Key: "sk-account-b", Name: "account-b"},
		},
		Status:      "active",
		ServiceType: "openai",
	}
	run := runAccountRateLimitFailover(t, upstream, func(apiKey string) (int, string) {
		if apiKey == "sk-account-a" {
			return http.StatusTooManyRequests, `{"error":{"code":"AccountRateLimitExceeded","message":"requests are too frequent"}}`
		}
		return http.StatusOK, `{"ok":true}`
	})

	if !run.handled || run.successKey != "sk-account-b" || run.failoverErr != nil || run.lastErr != nil {
		t.Fatalf("failover result = handled:%v key:%q failover:%v last:%v", run.handled, run.successKey, run.failoverErr, run.lastErr)
	}
	if len(run.attemptedKeys) != 2 || run.attemptedKeys[0] != "sk-account-a" || run.attemptedKeys[1] != "sk-account-b" {
		t.Fatalf("attempted keys = %v, want [sk-account-a sk-account-b]", run.attemptedKeys)
	}
	if run.accountSignals != 1 || len(run.signalReasons) != 1 || run.signalReasons[0] != string(autopilot.RateLimitReasonAccountRateLimitExceeded) {
		t.Fatalf("429 signals = %d reasons=%v, want one precise signal", run.accountSignals, run.signalReasons)
	}

	scopeA := keypool.LimiterScopeFor("sk-account-a", upstream.APIKeyConfigs[0])
	scopeB := keypool.LimiterScopeFor("sk-account-b", upstream.APIKeyConfigs[1])
	if limiter := run.limiterManager.GetScoped("Messages", 0, scopeA); limiter == nil {
		t.Fatal("account A scoped limiter missing")
	} else if inCooldown, _ := limiter.InCooldown(time.Now()); !inCooldown {
		t.Fatal("account A scope should be in cooldown")
	}
	if limiter := run.limiterManager.GetScoped("Messages", 0, scopeB); limiter == nil {
		t.Fatal("account B scoped limiter missing")
	} else if inCooldown, _ := limiter.InCooldown(time.Now()); inCooldown {
		t.Fatal("independent account B scope must not be cooled down")
	}
	if limiter := run.limiterManager.Get("Messages", 0); limiter != nil {
		if inCooldown, _ := limiter.InCooldown(time.Now()); inCooldown {
			t.Fatal("scoped account 429 must not cool down the whole channel")
		}
	}
	if got := run.metricsManager.GetKeyCircuitState(run.upstream.BaseURL, "sk-account-b", scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindMessages, run.upstream.ServiceType)); got != metrics.CircuitStateClosed {
		t.Fatalf("independent successful key circuit = %v, want closed", got)
	}
	for _, disabledKey := range run.cfgManager.GetConfig().Upstream[0].DisabledAPIKeys {
		if disabledKey.Key == "sk-account-a" {
			t.Fatal("account-level 429 must not permanently blacklist the key")
		}
	}
}

func TestTryUpstreamWithAllKeys_AccountRateLimitSkipsQuotaGroup(t *testing.T) {
	upstream := config.UpstreamConfig{
		Name:       "volc-quota-group",
		ChannelUID: "channel-quota-group",
		APIKeys:    []string{"sk-group-a", "sk-group-b", "sk-independent"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-group-a", Name: "group-a", QuotaGroup: "volc-account"},
			{Key: "sk-group-b", Name: "group-b", QuotaGroup: "volc-account"},
			{Key: "sk-independent", Name: "independent"},
		},
		Status:      "active",
		ServiceType: "openai",
	}
	run := runAccountRateLimitFailover(t, upstream, func(apiKey string) (int, string) {
		if apiKey == "sk-group-a" {
			return http.StatusTooManyRequests, `{"error":{"code":"account_rate_limit_exceeded"}}`
		}
		return http.StatusOK, `{"ok":true}`
	})

	if !run.handled || run.successKey != "sk-independent" || run.failoverErr != nil || run.lastErr != nil {
		t.Fatalf("quota failover result = handled:%v key:%q failover:%v last:%v", run.handled, run.successKey, run.failoverErr, run.lastErr)
	}
	if len(run.attemptedKeys) != 2 || run.attemptedKeys[0] != "sk-group-a" || run.attemptedKeys[1] != "sk-independent" {
		t.Fatalf("attempted keys = %v, same quota group key B should be skipped", run.attemptedKeys)
	}
	quotaScope := keypool.LimiterScopeFor("sk-group-a", upstream.APIKeyConfigs[0])
	if got := keypool.LimiterScopeFor("sk-group-b", upstream.APIKeyConfigs[1]); got != quotaScope {
		t.Fatalf("quota scopes differ: %q != %q", got, quotaScope)
	}
	if limiter := run.limiterManager.GetScoped("Messages", 0, quotaScope); limiter == nil {
		t.Fatal("quota group scoped limiter missing")
	} else if inCooldown, _ := limiter.InCooldown(time.Now()); !inCooldown {
		t.Fatal("quota group scope should be in cooldown")
	}
}

func TestTryUpstreamWithAllKeys_AccountRateLimitWithoutScopeFallsBackToChannel(t *testing.T) {
	upstream := config.UpstreamConfig{
		Name:        "volc-no-scope",
		ChannelUID:  "channel-no-scope",
		APIKeys:     []string{"sk-no-scope"},
		Status:      "active",
		ServiceType: "openai",
	}
	run := runAccountRateLimitFailover(t, upstream, func(string) (int, string) {
		return http.StatusTooManyRequests, `{"error":{"code":"AccountRateLimitExceeded"}}`
	})

	if run.handled || run.successKey != "" || run.failoverErr == nil || run.failoverErr.Status != http.StatusTooManyRequests || run.lastErr == nil {
		t.Fatalf("fallback result = handled:%v key:%q failover:%v last:%v", run.handled, run.successKey, run.failoverErr, run.lastErr)
	}
	if run.accountSignals != 1 {
		t.Fatalf("account signals = %d, want 1", run.accountSignals)
	}
	channelLimiter := run.limiterManager.Get("Messages", 0)
	if channelLimiter == nil {
		t.Fatal("scope unavailable should create channel limiter for cooldown")
	}
	if inCooldown, _ := channelLimiter.InCooldown(time.Now()); !inCooldown {
		t.Fatal("scope unavailable should fall back to channel cooldown")
	}
}

// ── EndpointAttemptPolicy 注入不变量测试 ──

// TestTryUpstreamWithAllKeys_NilPolicy_UnchangedBehavior 验证 nil policy 时
// TryUpstreamWithAllKeys 行为与不传 policy 时完全一致。
func TestTryUpstreamWithAllKeys_NilPolicy_UnchangedBehavior(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "nil-policy-test",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-nil-1", "sk-nil-2"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test-model"}`))

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
		[]byte(`{"model":"test-model","messages":[]}`),
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
		"test-model",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
		// nil policy（通过 WithEndpointAttemptPolicy(nil) 或不传）
		WithEndpointAttemptPolicy(nil),
	)

	if !handled {
		t.Fatal("nil policy 时应正常处理请求")
	}
	if successKey == "" {
		t.Fatal("nil policy 时应有成功 key")
	}
	if failoverErr != nil {
		t.Fatalf("nil policy 时 failoverErr 应为 nil: %v", failoverErr)
	}
	if lastErr != nil {
		t.Fatalf("nil policy 时 lastErr 应为 nil: %v", lastErr)
	}
}

// TestTryUpstreamWithAllKeys_ShadowPolicy_PreservesOrder 验证 shadow 模式 policy
// 不改变 URL 和 key 的遍历顺序（shadow 只计算 + 记录，不影响真实排序）。
func TestTryUpstreamWithAllKeys_ShadowPolicy_PreservesOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "shadow-policy-test",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-shadow-1", "sk-shadow-2"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test-model"}`))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]

	// 构建 shadow 模式 policy
	shadowPolicy := autopilot.BuildEndpointPolicy(
		autopilot.EndpointPolicyDeps{},
		&autopilot.RequestProfile{Model: "test-model", ChannelKind: "messages"},
		autopilot.RoutingModeShadow,
	)
	if shadowPolicy == nil {
		t.Fatal("shadow 模式应返回非 nil policy")
	}

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
		[]byte(`{"model":"test-model","messages":[]}`),
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
		"test-model",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
		WithEndpointAttemptPolicy(shadowPolicy),
	)

	if !handled {
		t.Fatal("shadow policy 时应正常处理请求")
	}
	if successKey == "" {
		t.Fatal("shadow policy 时应有成功 key")
	}
	if failoverErr != nil {
		t.Fatalf("shadow policy 时 failoverErr 应为 nil: %v", failoverErr)
	}
	if lastErr != nil {
		t.Fatalf("shadow policy 时 lastErr 应为 nil: %v", lastErr)
	}
}

func TestSelectAttemptAPIKeyFilteredUsesBindingIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	upstream := &config.UpstreamConfig{
		Name:       "binding-test",
		ChannelUID: "ch-binding",
		BaseURL:    "https://default.example.com",
		APIKeys:    []string{"sk-a", "sk-b"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-a"},
			{Key: "sk-b"},
		},
	}
	const currentBaseURL = "https://current.example.com"
	var gotChannelUID, gotBaseURL string
	policy := &autopilot.EndpointAttemptPolicy{
		FilterKeyBindings: func(channelUID, baseURL string, apiKeys []string) []string {
			gotChannelUID, gotBaseURL = channelUID, baseURL
			return []string{"sk-b"}
		},
		SortKeyBindings: func(channelUID, baseURL string, apiKeys []string) ([]string, []autopilot.EndpointCandidate) {
			return apiKeys, nil
		},
	}

	_, key, err := selectAttemptAPIKeyFiltered(
		nil,
		scheduler.ChannelKindMessages,
		0,
		upstream,
		currentBaseURL,
		map[string]bool{},
		map[string]bool{},
		"model-b",
		func(_ *config.UpstreamConfig, _ map[string]bool) (string, error) { return "sk-a", nil },
		policy,
		"Messages",
		c,
		nil,
		nil,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-b" {
		t.Fatalf("选择了 %q，期望 binding 过滤后的 sk-b", key)
	}
	if gotChannelUID != upstream.ChannelUID || gotBaseURL != currentBaseURL {
		t.Fatalf("binding 身份 = (%q, %q)，期望 (%q, %q)", gotChannelUID, gotBaseURL, upstream.ChannelUID, currentBaseURL)
	}
}

// TestTryUpstreamWithAllKeys_PanicPolicy_DoesNotBreakRequest 验证 policy 函数 panic 时
// TryUpstreamWithAllKeys 不中断请求，正常完成（fail-open）。
func TestTryUpstreamWithAllKeys_PanicPolicy_DoesNotBreakRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "panic-policy-test",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-panic-1"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test-model"}`))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]

	// 构建会 panic 的 policy
	panicPolicy := &autopilot.EndpointAttemptPolicy{
		FilterURLs: func(urls []string) []string {
			panic("FilterURLs panic")
		},
		SortURLs: func(urls []string) ([]string, []autopilot.EndpointCandidate) {
			panic("SortURLs panic")
		},
		FilterKeys: func(baseURL string, apiKeys []string) []string {
			panic("FilterKeys panic")
		},
		SortKeys: func(baseURL string, apiKeys []string) ([]string, []autopilot.EndpointCandidate) {
			panic("SortKeys panic")
		},
		RequestModel: "test-model",
		Mode:         autopilot.RoutingModeShadow,
	}

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
		[]byte(`{"model":"test-model","messages":[]}`),
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
		"test-model",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
		WithEndpointAttemptPolicy(panicPolicy),
	)

	// panic policy 不应中断请求：fail-open 回退到原始顺序
	if !handled {
		t.Fatal("panic policy 时应正常处理请求（fail-open）")
	}
	if successKey == "" {
		t.Fatal("panic policy 时应有成功 key")
	}
	if failoverErr != nil {
		t.Fatalf("panic policy 时 failoverErr 应为 nil: %v", failoverErr)
	}
	if lastErr != nil {
		t.Fatalf("panic policy 时 lastErr 应为 nil: %v", lastErr)
	}
}

// ── callPolicyFilterKeyBindings 全灭 fail-open 测试 ──

// 过滤全灭时回退原列表（暴露真实上游信号）；输入本身为空不放行；局部过滤语义不变。
func TestCallPolicyFilterKeyBindings_FailOpenOnAllFiltered(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	allFiltered := &autopilot.EndpointAttemptPolicy{
		FilterKeyBindings: func(channelUID, baseURL string, apiKeys []string) []string {
			return nil
		},
	}
	keys := []string{"sk-a", "sk-b"}

	got := callPolicyFilterKeyBindings(allFiltered, "ch1", "https://a.com", keys, "messages", c)
	if len(got) != len(keys) {
		t.Fatalf("全灭时应 fail-open 返回原列表: got %v, want %v", got, keys)
	}

	got = callPolicyFilterKeyBindings(allFiltered, "ch1", "https://a.com", nil, "messages", c)
	if len(got) != 0 {
		t.Fatalf("空输入不应放行: got %v", got)
	}

	partial := &autopilot.EndpointAttemptPolicy{
		FilterKeyBindings: func(channelUID, baseURL string, apiKeys []string) []string {
			return apiKeys[:1]
		},
	}
	got = callPolicyFilterKeyBindings(partial, "ch1", "https://a.com", keys, "messages", c)
	if len(got) != 1 || got[0] != "sk-a" {
		t.Fatalf("局部过滤语义应保持: got %v", got)
	}
}
