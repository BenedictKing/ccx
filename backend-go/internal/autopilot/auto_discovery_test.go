package autopilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/presetstore"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/utils"
)

// ── 已有测试（保留原样） ─────────────────────────────────────────────────────

func TestAutoDiscoveryRunner_TriggerRejectsDuplicate(t *testing.T) {
	runner := NewAutoDiscoveryRunner(nil, nil)

	ch := &config.UpstreamConfig{
		ChannelUID: "ch_test_001",
		BaseURL:    "https://example.com",
		APIKeys:    []string{"sk-test"},
	}
	started := runner.TriggerDiscovery("ch_test_001", ch, nil)
	if !started {
		t.Fatal("第一次触发应返回 true")
	}

	started = runner.TriggerDiscovery("ch_test_001", ch, nil)
	if started {
		t.Fatal("重复触发应返回 false")
	}
}

func TestAutoDiscoveryWriteProfilesPreservesProviderQualityEvidence(t *testing.T) {
	db := newTestDB(t)
	profileStore, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	modelStore, err := NewModelProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewModelProfileStoreWithDB: %v", err)
	}

	const (
		channelUID = "ch-discovery-quality"
		baseURL    = "https://quality.example.com"
		apiKey     = "sk-discovery-quality"
		modelID    = "quality-model"
	)
	channel := config.UpstreamConfig{
		ChannelUID:  channelUID,
		BaseURL:     baseURL,
		APIKeys:     []string{apiKey},
		ServiceType: "openai",
		AutoManaged: true,
	}
	cfgManager, cleanup := createTestConfigManager(t, config.Config{Upstream: []config.UpstreamConfig{channel}})
	t.Cleanup(cleanup)

	probedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	discoveredAt := time.Date(2026, 7, 22, 0, 42, 12, 0, time.UTC)
	metricsKey := computeMetricsIdentityKey(baseURL, apiKey, channel.ServiceType)
	if err := modelStore.Upsert(&ModelProfile{
		ChannelUID:                  channelUID,
		ChannelKind:                 "messages",
		ServiceType:                 channel.ServiceType,
		MetricsKey:                  metricsKey,
		ModelID:                     modelID,
		ProviderQualityScore:        0.73,
		ProviderQualitySource:       "probe",
		ProviderQualityConfidence:   0.6,
		ProviderQualityProbeVersion: "pq-test-v1",
		LastProbeAt:                 probedAt,
		ProbeLatencyMs:              1234,
		ProbeConfidence:             0.6,
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewAutoDiscoveryRunner(profileStore, nil)
	runner.ModelProfileStore = modelStore
	runner.writeProfiles(channelUID, &channel, []EndpointDiscoveryResult{
		{
			KeyMask:               utils.MaskAPIKey(apiKey),
			BaseURL:               baseURL,
			Models:                []string{modelID},
			ModelsCount:           1,
			ProtocolOk:            true,
			ModelDiscoverySource:  ModelDiscoverySourceControlPlane,
			ModelDiscoveryMessage: "火山管控面 Coding Plan 模型清单",
			ModelsDiscoveredAt:    &discoveredAt,
		},
	}, cfgManager)
	endpoint := profileStore.Get(GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey)))
	if endpoint == nil || endpoint.ModelDiscoverySource != ModelDiscoverySourceControlPlane ||
		endpoint.ModelDiscoveryMessage == "" || endpoint.ModelsDiscoveredAt == nil ||
		!endpoint.ModelsDiscoveredAt.Equal(discoveredAt) {
		t.Fatalf("模型发现元数据未持久化: %+v", endpoint)
	}

	got := modelStore.Get(channelUID, "messages", metricsKey, modelID)
	if got == nil {
		t.Fatal("模型画像不存在")
	}
	if got.ProviderQualityScore != 0.73 || got.ProviderQualitySource != "probe" || got.ProviderQualityConfidence != 0.6 || got.ProviderQualityProbeVersion != "pq-test-v1" {
		t.Fatalf("ProviderQuality 证据被自动发现覆盖: %+v", got)
	}
	if !got.LastProbeAt.Equal(probedAt) || got.ProbeLatencyMs != 1234 || got.ProbeConfidence != 0.6 {
		t.Fatalf("L3 探测元数据未保留: %+v", got)
	}
}

func TestAutoDiscoveryWriteProfilesUsesUpstreamModelCapabilities(t *testing.T) {
	db := newTestDB(t)
	profileStore, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	modelStore, err := NewModelProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewModelProfileStoreWithDB: %v", err)
	}

	const (
		channelUID = "ch-discovery-glm52"
		baseURL    = "https://open.bigmodel.cn/api/paas/v4#"
		apiKey     = "test.glm52-key"
		modelID    = "glm-5.2"
	)
	channel := config.UpstreamConfig{
		ChannelUID:  channelUID,
		BaseURL:     baseURL,
		APIKeys:     []string{apiKey},
		ServiceType: "openai",
		AutoManaged: true,
	}
	cfgManager, cleanup := createTestConfigManager(t, config.Config{ResponsesUpstream: []config.UpstreamConfig{channel}})
	t.Cleanup(cleanup)

	runner := NewAutoDiscoveryRunner(profileStore, nil)
	runner.ModelProfileStore = modelStore
	runner.writeProfiles(channelUID, &channel, []EndpointDiscoveryResult{{
		KeyMask:     utils.MaskAPIKey(apiKey),
		BaseURL:     baseURL,
		Models:      []string{modelID},
		ModelsCount: 1,
		ProtocolOk:  true,
	}}, cfgManager)

	metricsKey := computeMetricsIdentityKey(baseURL, apiKey, channel.ServiceType)
	got := modelStore.Get(channelUID, "responses", metricsKey, modelID)
	if got == nil {
		t.Fatal("模型画像不存在")
	}
	if got.ContextTokens != 1048576 {
		t.Fatalf("ContextTokens = %d, want 1048576", got.ContextTokens)
	}
	if !got.SupportsToolCalls || !got.SupportsReasoning {
		t.Fatalf("GLM-5.2 上游能力未写入模型画像: %+v", got)
	}
	// glm-5.2 常规口径 22.2 → low 档（此前 high 靠 max 档成绩）
	if got.QualityTier != QualityTierLow {
		t.Fatalf("QualityTier = %q, want low", got.QualityTier)
	}
}

func TestAutoDiscoveryRunner_GetTaskNil(t *testing.T) {
	runner := NewAutoDiscoveryRunner(nil, nil)
	task := runner.GetTask("nonexistent")
	if task != nil {
		t.Fatal("从未触发的渠道应返回 nil")
	}
}

func TestDiscoverEndpointsUsesBoundBaseURLForTwentyKeys(t *testing.T) {
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer serverA.Close()
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-b"}]}`))
	}))
	defer serverB.Close()

	channel := &config.UpstreamConfig{ServiceType: "openai", BaseURLs: []string{serverA.URL, serverB.URL}}
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("sk-%02d", i)
		baseURL := serverA.URL
		if i%2 == 1 {
			baseURL = serverB.URL
		}
		channel.APIKeys = append(channel.APIKeys, key)
		channel.APIKeyConfigs = append(channel.APIKeyConfigs, config.APIKeyConfig{Key: key, BaseURL: baseURL})
	}
	runner := NewAutoDiscoveryRunner(nil, nil)
	results := runner.discoverEndpoints(context.Background(), channel)
	if len(results) != 20 {
		t.Fatalf("绑定后的 20 个 Key 应只探测 20 个 endpoint，实际=%d", len(results))
	}
	for _, result := range results {
		if !result.ProtocolOk {
			t.Fatalf("endpoint 探测失败: %+v", result)
		}
	}
}

func TestAutoDiscoveryRunner_TriggerCreatesTask(t *testing.T) {
	runner := NewAutoDiscoveryRunner(nil, nil)

	ch := &config.UpstreamConfig{
		ChannelUID: "ch_test_002",
		BaseURL:    "https://example.com",
		APIKeys:    []string{"sk-test"},
	}
	runner.TriggerDiscovery("ch_test_002", ch, nil)

	task := runner.GetTask("ch_test_002")
	if task == nil {
		t.Fatal("触发后 GetTask 应返回非 nil")
	}
	if task.ChannelUID != "ch_test_002" {
		t.Fatalf("期望 ChannelUID=ch_test_002, 实际=%s", task.ChannelUID)
	}
	if task.Status != DiscoveryStatusRunning {
		t.Fatalf("初始状态应为 running, 实际=%s", task.Status)
	}
}

func TestParseModelsResponse(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected int
	}{
		{
			name:     "标准 OpenAI 格式",
			body:     `{"data":[{"id":"gpt-4o"},{"id":"gpt-3.5-turbo"}]}`,
			expected: 2,
		},
		{
			name:     "空列表",
			body:     `{"data":[]}`,
			expected: 0,
		},
		{
			name:     "无效 JSON",
			body:     `not json`,
			expected: 0,
		},
		{
			name:     "跳过空 ID",
			body:     `{"data":[{"id":"model-1"},{"id":""},{"id":"model-3"}]}`,
			expected: 2,
		},
		{
			name:     "data 缺失",
			body:     `{"other": "field"}`,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := parseModelsResponse([]byte(tt.body))
			if len(models) != tt.expected {
				t.Fatalf("期望 %d 个模型, 实际 %d", tt.expected, len(models))
			}
		})
	}
}

func TestBuildModelsProbeURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "无版本后缀补 v1",
			baseURL: "https://api.example.com",
			want:    "https://api.example.com/v1/models",
		},
		{
			name:    "已有 v1 不重复补",
			baseURL: "https://api.xiaomimimo.com/v1",
			want:    "https://api.xiaomimimo.com/v1/models",
		},
		{
			name:    "已有 v1 且尾部斜杠",
			baseURL: "https://token-plan-cn.xiaomimimo.com/v1/",
			want:    "https://token-plan-cn.xiaomimimo.com/v1/models",
		},
		{
			name:    "井号跳过版本前缀",
			baseURL: "https://relay.example.com/custom#",
			want:    "https://relay.example.com/custom/models",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildModelsProbeURL(tt.baseURL); got != tt.want {
				t.Fatalf("buildModelsProbeURL(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestDiscoveryStatus_Constants(t *testing.T) {
	// 确保状态常量符合预期字符串
	if DiscoveryStatusIdle != "idle" {
		t.Fatal("DiscoveryStatusIdle 应为 'idle'")
	}
	if DiscoveryStatusRunning != "running" {
		t.Fatal("DiscoveryStatusRunning 应为 'running'")
	}
	if DiscoveryStatusDone != "done" {
		t.Fatal("DiscoveryStatusDone 应为 'done'")
	}
	if DiscoveryStatusFailed != "failed" {
		t.Fatal("DiscoveryStatusFailed 应为 'failed'")
	}
}

func TestAutoDiscoveryRunner_ConcurrentTriggers(t *testing.T) {
	runner := NewAutoDiscoveryRunner(nil, nil)

	ch := &config.UpstreamConfig{
		ChannelUID: "ch_concurrent",
		BaseURL:    "https://example.com",
		APIKeys:    []string{"sk-test"},
	}

	// 并发触发同一渠道，只有第一个应该成功
	results := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func() {
			started := runner.TriggerDiscovery("ch_concurrent", ch, nil)
			results <- started
		}()
	}

	successCount := 0
	for i := 0; i < 5; i++ {
		if <-results {
			successCount++
		}
	}

	if successCount != 1 {
		t.Fatalf("并发触发同一渠道应恰好有1个成功，实际=%d", successCount)
	}
}

func TestEndpointDiscoveryResult_KeyMask(t *testing.T) {
	// 验证 EndpointDiscoveryResult 中 KeyMask 不包含明文 key
	result := EndpointDiscoveryResult{
		KeyMask:     "sk-****abcd",
		BaseURL:     "https://api.example.com",
		ModelsCount: 5,
		ProtocolOk:  true,
	}

	if result.KeyMask == "" {
		t.Fatal("KeyMask 不应为空")
	}
	if len(result.KeyMask) < 4 {
		t.Fatal("KeyMask 长度过短")
	}
}

func TestProbeEndpoint_DisableProbeUsesBuiltinManifest(t *testing.T) {
	modelsRequested := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/anthropic/v1/messages":
			if got := r.Header.Get("authorization"); got != "Bearer sk-test" {
				t.Errorf("authorization = %q, want Bearer sk-test", got)
			}
			w.WriteHeader(http.StatusBadRequest)
		case "/anthropic/v1/models":
			modelsRequested = true
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	withTestBuiltinManifest(t, presetstore.BuiltinModelsManifestEntryPreset{
		BaseURLPattern: trimTestURLScheme(srv.URL) + "/anthropic",
		ServiceType:    "messages",
		PlanHint:       "test_disable_probe",
		ModelIDs:       []string{"mimo-v2.5-pro", "mimo-v2.5"},
		DisableProbe:   true,
	})

	runner := NewAutoDiscoveryRunner(nil, nil)
	channel := &config.UpstreamConfig{
		ServiceType: "claude",
		BaseURL:     srv.URL + "/anthropic",
		APIKeys:     []string{"sk-test"},
	}

	result := runner.probeEndpoint(context.Background(), srv.Client(), channel, channel.BaseURL, "sk-test")
	if modelsRequested {
		t.Fatal("disableProbe=true 时不应请求 /v1/models")
	}
	if !result.ProtocolOk {
		t.Fatalf("ProtocolOk = false, error=%s", result.ErrorMessage)
	}
	if result.ModelsCount != 2 {
		t.Fatalf("ModelsCount = %d, want 2", result.ModelsCount)
	}
	if result.ModelDiscoverySource != ModelDiscoverySourceBuiltinManifest || result.ModelsDiscoveredAt == nil {
		t.Fatalf("静态清单元数据错误: %+v", result)
	}
}

func TestProbeEndpoint_BuiltinManifestDoesNotHideAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	withTestBuiltinManifest(t, presetstore.BuiltinModelsManifestEntryPreset{
		BaseURLPattern: trimTestURLScheme(srv.URL),
		ServiceType:    "messages",
		PlanHint:       "test_fallback",
		ModelIDs:       []string{"claude-test"},
		DisableProbe:   false,
	})

	runner := NewAutoDiscoveryRunner(nil, nil)
	channel := &config.UpstreamConfig{
		ServiceType: "claude",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-test"},
	}

	result := runner.probeEndpoint(context.Background(), srv.Client(), channel, channel.BaseURL, "sk-test")
	if result.ProtocolOk {
		t.Fatal("401 不应回退内置模型清单并标记成功")
	}
	if result.ModelsCount != 0 {
		t.Fatalf("ModelsCount = %d, want 0", result.ModelsCount)
	}
}

func TestProbeEndpoint_ModelsUsesUnifiedAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "sk-ant-test" {
			t.Errorf("x-api-key = %q, want sk-ant-test", got)
		}
		if got := r.Header.Get("authorization"); got != "" {
			t.Errorf("authorization = %q, want empty", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-test"}]}`))
	}))
	defer srv.Close()

	runner := NewAutoDiscoveryRunner(nil, nil)
	channel := &config.UpstreamConfig{
		ServiceType: "claude",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-ant-test"},
	}

	result := runner.probeEndpoint(context.Background(), srv.Client(), channel, channel.BaseURL, "sk-ant-test")
	if !result.ProtocolOk {
		t.Fatalf("ProtocolOk = false, error=%s", result.ErrorMessage)
	}
	if result.ModelsCount != 1 || result.Models[0] != "claude-test" {
		t.Fatalf("models = %v, count=%d", result.Models, result.ModelsCount)
	}
	if result.ModelDiscoverySource != ModelDiscoverySourceModelsAPI || result.ModelsDiscoveredAt == nil {
		t.Fatalf("models API 元数据错误: %+v", result)
	}
}

func TestProbeEndpoint_GeminiModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "gemini-test-key" {
			t.Errorf("x-goog-api-key = %q, want gemini-test-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-3.5-flash"},{"name":"models/gemini-3.1-pro-preview"}]}`))
	}))
	defer srv.Close()

	runner := NewAutoDiscoveryRunner(nil, nil)
	channel := &config.UpstreamConfig{
		ServiceType: "gemini",
		BaseURL:     srv.URL,
		APIKeys:     []string{"gemini-test-key"},
	}

	result := runner.probeEndpoint(context.Background(), srv.Client(), channel, channel.BaseURL, "gemini-test-key")
	if !result.ProtocolOk || result.ModelsCount != 2 {
		t.Fatalf("Gemini models discovery failed: %+v", result)
	}
	if got := strings.Join(result.Models, ","); got != "gemini-3.5-flash,gemini-3.1-pro-preview" {
		t.Fatalf("models=%q", got)
	}
}

func TestProbeEndpoint_ClaudeModelsUsesProbeHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != utils.ClaudeCodeProbeUserAgent {
			t.Errorf("User-Agent = %q, want %q", got, utils.ClaudeCodeProbeUserAgent)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("anthropic-version = %q, want 2023-06-01", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-4-6"}]}`))
	}))
	defer srv.Close()

	runner := NewAutoDiscoveryRunner(nil, nil)
	channel := &config.UpstreamConfig{
		ServiceType: "claude",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-test"},
	}

	result := runner.probeEndpoint(context.Background(), srv.Client(), channel, channel.BaseURL, "sk-test")
	if !result.ProtocolOk || result.ModelsCount != 1 {
		t.Fatalf("claude models probe failed: %+v", result)
	}
	if result.usedClientFingerprint {
		t.Fatal("claude 风格带头是协议行为，不应记为学习")
	}
}

func TestProbeEndpoint_ClientFingerprintRejectionLearned(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requestCount, 1) == 1 {
			if got := r.Header.Get("User-Agent"); got == utils.ClaudeCodeProbeUserAgent {
				t.Errorf("首次请求应裸发（不带伪装头），User-Agent = %q", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"unauthorized client detected"},"type":"unauthorized_client_error"}`))
			return
		}
		if got := r.Header.Get("User-Agent"); got != utils.ClaudeCodeProbeUserAgent {
			t.Errorf("重试 User-Agent = %q, want %q", got, utils.ClaudeCodeProbeUserAgent)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.4"}]}`))
	}))
	defer srv.Close()

	runner := NewAutoDiscoveryRunner(nil, nil)
	// openai/chat 风格首发裸请求，命中风控后带伪装头重试成功。
	channel := &config.UpstreamConfig{
		ServiceType: "openai",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-test"},
	}

	result := runner.probeEndpoint(context.Background(), srv.Client(), channel, channel.BaseURL, "sk-test")
	if !result.ProtocolOk || result.ModelsCount != 1 {
		t.Fatalf("models probe failed: %+v", result)
	}
	if !result.usedClientFingerprint {
		t.Fatal("风控重试成功应记 usedClientFingerprint=true")
	}
	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("server received %d requests, want 2", got)
	}
}

func TestProbeEndpoint_LearnedFingerprintSkipsBareRequest(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		if got := r.Header.Get("User-Agent"); got != utils.ClaudeCodeProbeUserAgent {
			t.Errorf("已学习渠道应首发带伪装头，User-Agent = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.4"}]}`))
	}))
	defer srv.Close()

	runner := NewAutoDiscoveryRunner(nil, nil)
	channel := &config.UpstreamConfig{
		ServiceType:              "openai",
		BaseURL:                  srv.URL,
		APIKeys:                  []string{"sk-test"},
		LearnedClientFingerprint: true,
	}

	result := runner.probeEndpoint(context.Background(), srv.Client(), channel, channel.BaseURL, "sk-test")
	if !result.ProtocolOk || result.ModelsCount != 1 {
		t.Fatalf("models probe failed: %+v", result)
	}
	if got := atomic.LoadInt32(&requestCount); got != 1 {
		t.Fatalf("server received %d requests, want 1", got)
	}
}

func TestDiscoverEndpoints_ProbesAdditionalProtocols(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.4"}]}`))
		case "/v1/messages", "/v1/chat/completions":
			if r.Method != http.MethodPost {
				t.Errorf("%s method=%s", r.URL.Path, r.Method)
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1beta/models/gpt-5.4:generateContent":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"unsupported protocol"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	runner := NewAutoDiscoveryRunner(nil, nil)
	runner.client = srv.Client()
	channel := &config.UpstreamConfig{
		ChannelUID:  "ch-multi",
		ServiceType: "responses",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-test"},
		AutoManaged: true,
	}

	results := runner.discoverEndpoints(context.Background(), channel)
	if len(results) != 1 {
		t.Fatalf("results=%d", len(results))
	}
	result := results[0]
	for _, protocol := range []string{"messages", "chat", "responses"} {
		if got := strings.Join(result.ProtocolModels[protocol], ","); got != "gpt-5.4" {
			t.Fatalf("%s models=%q", protocol, got)
		}
	}
	if result.ProtocolDiscoverySource["chat"] != "protocol_model_probe" {
		t.Fatalf("chat source=%q", result.ProtocolDiscoverySource["chat"])
	}
	if result.ProtocolDiscoveryError["gemini"] == "" {
		t.Fatal("Gemini 探测失败应记录原因")
	}
}

func TestProbeEndpoint_ManifestExcludePatternsFilterModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[` +
			`{"id":"mimo-v2.5-tts-voicedesign"},` +
			`{"id":"mimo-v2.5-tts-voiceclone"},` +
			`{"id":"mimo-v2.5-tts"},` +
			`{"id":"mimo-v2.5-pro"},` +
			`{"id":"mimo-v2.5-asr"},` +
			`{"id":"mimo-v2.5"}` +
			`]}`))
	}))
	defer srv.Close()

	withTestBuiltinManifest(t, presetstore.BuiltinModelsManifestEntryPreset{
		BaseURLPattern:       trimTestURLScheme(srv.URL),
		ServiceType:          "messages",
		PlanHint:             "test_mimo_models_filter",
		ModelIDs:             []string{"mimo-v2.5-pro", "mimo-v2.5"},
		ExcludeModelPatterns: []string{`^mimo-v2\.5-(?:asr|tts(?:-.+)?)$`},
		DisableProbe:         false,
	})

	runner := NewAutoDiscoveryRunner(nil, nil)
	channel := &config.UpstreamConfig{
		ServiceType: "claude",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-test"},
	}

	result := runner.probeEndpoint(context.Background(), srv.Client(), channel, channel.BaseURL, "sk-test")
	if !result.ProtocolOk {
		t.Fatalf("ProtocolOk = false, error=%s", result.ErrorMessage)
	}
	expected := []string{"mimo-v2.5-pro", "mimo-v2.5"}
	if len(result.Models) != len(expected) {
		t.Fatalf("models = %v, want %v", result.Models, expected)
	}
	for i, modelID := range expected {
		if result.Models[i] != modelID {
			t.Fatalf("models[%d] = %q, want %q; full=%v", i, result.Models[i], modelID, result.Models)
		}
	}
}

func TestLookupDiscoveryBuiltinManifestMatchesCanonicalOpenAIBaseURL(t *testing.T) {
	channel := &config.UpstreamConfig{ServiceType: "openai"}
	manifest, ok := lookupDiscoveryBuiltinManifest(channel, "https://token-plan-sgp.xiaomimimo.com")
	if !ok {
		t.Fatal("规范化后不含 /v1 的 MiMo OpenAI BaseURL 应命中内置清单")
	}
	if len(manifest.ExcludeModelPatterns) == 0 {
		t.Fatal("MiMo OpenAI 清单缺少 ASR/TTS 排除规则")
	}
}

func TestWriteProfilesSetsEndpointIdentity(t *testing.T) {
	store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatalf("创建 ProfileStore 失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store.Close)
	modelStore, err := NewModelProfileStoreWithDB(store.DB())
	if err != nil {
		t.Fatalf("创建 ModelProfileStore 失败: %v", err)
	}

	runner := NewAutoDiscoveryRunner(store, nil)
	runner.ModelProfileStore = modelStore
	channelUID := "ch_profile_uid"
	baseURL := "https://api.example.com"
	apiKey := "sk-test-key"
	channel := &config.UpstreamConfig{
		AccountUID:  "acct-profile",
		ChannelUID:  channelUID,
		ServiceType: "claude",
		BaseURL:     baseURL,
		APIKeys:     []string{apiKey},
		AutoManaged: true,
	}
	cfgManager := setupTestConfigManagerForDiscovery(t, channelUID, nil, nil)
	defer errutil.IgnoreDeferred(cfgManager.Close)
	runner.writeProfiles(channelUID, channel, []EndpointDiscoveryResult{{
		KeyMask:     utils.MaskAPIKey(apiKey),
		BaseURL:     baseURL,
		Models:      []string{"mimo-v2.5-pro"},
		ModelsCount: 1,
		ProtocolOk:  true,
	}}, cfgManager)

	endpointUID := GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey))
	profile := store.Get(endpointUID)
	if profile == nil {
		t.Fatalf("未写入 endpoint profile: %s", endpointUID)
	}
	if profile.EndpointUID != endpointUID {
		t.Fatalf("EndpointUID = %q, want %q", profile.EndpointUID, endpointUID)
	}
	if profile.ChannelKind != "messages" {
		t.Fatalf("ChannelKind = %q, want messages", profile.ChannelKind)
	}
	if profile.ServiceType != "claude" {
		t.Fatalf("ServiceType = %q, want claude", profile.ServiceType)
	}
	expectedMetricsKey := computeMetricsIdentityKey(baseURL, apiKey, channel.ServiceType)
	if profile.KeyHash != KeyHashFromAPIKey(apiKey) || profile.MetricsKey != expectedMetricsKey {
		t.Fatalf("endpoint identity 不完整: keyHash=%q metricsKey=%q", profile.KeyHash, profile.MetricsKey)
	}
	if profile.IdentityBaseURL != utils.MetricsIdentityBaseURL(baseURL, channel.ServiceType) {
		t.Fatalf("IdentityBaseURL = %q", profile.IdentityBaseURL)
	}
	modelProfile := modelStore.Get(channelUID, "messages", expectedMetricsKey, "mimo-v2.5-pro")
	if modelProfile == nil || modelProfile.MetricsKey != profile.MetricsKey {
		t.Fatalf("endpoint/model profile metrics identity 漂移: endpoint=%q model=%+v", profile.MetricsKey, modelProfile)
	}
	if profile.QualityTier != QualityTierNormal || profile.StabilityTier != StabilityTierNormal ||
		profile.SpeedTier != SpeedTierNormal || profile.CostTier != CostTierNormal {
		t.Fatalf("discovery profile should use neutral tiers: %+v", profile)
	}
	var persistedCount int
	if err := store.db.QueryRow(`
SELECT COUNT(*) FROM autopilot_endpoint_profiles
WHERE endpoint_uid = ? AND account_uid = ? AND service_type = ?
  AND json_extract(profile_json, '$.channelKind') = ?
  AND json_array_length(json_extract(profile_json, '$.availableModels')) = 1
`, endpointUID, "acct-profile", "claude", "messages").Scan(&persistedCount); err != nil {
		t.Fatalf("查询持久化画像失败: %v", err)
	}
	if persistedCount != 1 {
		t.Fatal("自动发现返回前应持久化账号、协议和模型列表")
	}
}

func TestWriteProfilesBackfillsLegacyChannelKindForRouting(t *testing.T) {
	store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatalf("创建 ProfileStore 失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store.Close)

	const (
		channelUID = "ch_legacy_profile"
		baseURL    = "https://legacy.example.com"
		apiKey     = "sk-legacy"
	)
	endpointUID := GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey))
	if err := store.Upsert(&KeyEndpointProfile{
		EndpointUID: endpointUID, ChannelUID: channelUID, ServiceType: "messages",
		BaseURL: baseURL, KeyHash: KeyHashFromAPIKey(apiKey),
		HealthState: HealthStateHealthy, QualityTier: QualityTierHigh,
		StabilityTier: StabilityTierStable, SpeedTier: SpeedTierFast, CostTier: CostTierCheap,
	}); err != nil {
		t.Fatalf("写入 legacy profile 失败: %v", err)
	}

	cfgManager := setupTestConfigManagerForDiscovery(t, channelUID, nil, nil)
	defer errutil.IgnoreDeferred(cfgManager.Close)
	channel := &config.UpstreamConfig{
		ChannelUID: channelUID, ServiceType: "claude", BaseURL: baseURL, APIKeys: []string{apiKey},
	}
	runner := NewAutoDiscoveryRunner(store, nil)
	runner.writeProfiles(channelUID, channel, []EndpointDiscoveryResult{{
		KeyMask: utils.MaskAPIKey(apiKey), BaseURL: baseURL,
		Models: []string{"claude-test"}, ModelsCount: 1, ProtocolOk: true,
	}}, cfgManager)

	profile := store.Get(endpointUID)
	if profile == nil || profile.ChannelKind != "messages" || profile.ServiceType != "claude" {
		t.Fatalf("legacy profile identity not backfilled: %+v", profile)
	}
	processed := cfgManager.GetConfig()
	entry := NewSmartRouter(store, nil, nil, cfgManager).buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: processed.Upstream[0].Name, Status: "active"},
		&processed.Upstream[0], "messages", "", nil,
	)
	if entry.ScoringCandidate.QualityTier != QualityTierHigh {
		t.Fatalf("discovery profile was not used by routing: quality=%s", entry.ScoringCandidate.QualityTier)
	}
}

// TestWriteProfilesCanonicalBaseURLMatch 回归测试：BaseURL 带默认版本前缀（如 /v1）时，
// writeProfiles 生成的 endpointUID 必须与 buildEndpointInventory 一致，
// 确保 ListActiveByChannel 能正确命中画像。
func TestWriteProfilesCanonicalBaseURLMatch(t *testing.T) {
	store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatalf("创建 ProfileStore 失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store.Close)

	const (
		channelUID = "ch_localhost_8990"
		apiKey     = "sk-local"
	)
	// 原始 BaseURL 带 /v1 后缀，CanonicalBaseURL 会把它截掉
	rawBaseURL := "http://localhost:8990/v1"
	canonicalBaseURL := utils.CanonicalBaseURL(rawBaseURL, "openai")
	if canonicalBaseURL != "http://localhost:8990" {
		t.Fatalf("测试前提失败: CanonicalBaseURL(%q) = %q, want %q", rawBaseURL, canonicalBaseURL, "http://localhost:8990")
	}

	channel := &config.UpstreamConfig{
		ChannelUID:  channelUID,
		ServiceType: "openai",
		BaseURL:     rawBaseURL,
		APIKeys:     []string{apiKey},
		AutoManaged: false,
	}

	// 写入发现画像（使用原始 baseURL）
	runner := NewAutoDiscoveryRunner(store, nil)
	runner.writeProfiles(channelUID, channel, []EndpointDiscoveryResult{{
		KeyMask:     utils.MaskAPIKey(apiKey),
		BaseURL:     rawBaseURL,
		Models:      []string{"model-a", "model-b"},
		ModelsCount: 2,
		ProtocolOk:  true,
	}}, nil)

	// 验证写入时使用了规范化后的 baseURL
	expectedEndpointUID := GenerateEndpointUID(channelUID, canonicalBaseURL, KeyHashFromAPIKey(apiKey))
	profile := store.Get(expectedEndpointUID)
	if profile == nil {
		var keys []string
		for _, p := range store.ListAll() {
			keys = append(keys, p.EndpointUID)
		}
		t.Fatalf("期望 endpointUID=%s 的画像不存在，store 中的 key: %v", expectedEndpointUID, keys)
	}
	if profile.BaseURL != canonicalBaseURL {
		t.Fatalf("profile.BaseURL = %q, want %q", profile.BaseURL, canonicalBaseURL)
	}

	// 模拟 L1 Worker 的 buildEndpointInventory，验证能匹配到画像
	// 关键点：buildEndpointInventory 从配置构造 endpointUID，必须和 writeProfiles 一致
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			ChannelUID:  channelUID,
			ServiceType: "openai",
			BaseURL:     rawBaseURL,
			APIKeys:     []string{apiKey},
			Status:      "active",
		}},
	}
	inventory := buildEndpointInventory(cfg)
	found := false
	for uid := range inventory.EndpointUIDs {
		if uid == expectedEndpointUID {
			found = true
			break
		}
	}
	if !found {
		var inventoryUIDs []string
		for uid := range inventory.EndpointUIDs {
			inventoryUIDs = append(inventoryUIDs, uid)
		}
		t.Fatalf("buildEndpointInventory 未生成期望的 endpointUID=%s，生成的 UIDs: %v", expectedEndpointUID, inventoryUIDs)
	}

	// ReplaceActiveEndpointUIDs 被 Manager 周期性调用；手动模拟以验证 ListActiveByChannel
	store.ReplaceActiveEndpointUIDs(inventory.EndpointUIDs)

	// 验证 ListActiveByChannel 能返回画像（这才是前端展示的真正数据来源）
	activeProfiles := store.ListActiveByChannel(channelUID)
	if len(activeProfiles) != 1 {
		t.Fatalf("ListActiveByChannel 返回 %d 条画像，期望 1 条", len(activeProfiles))
	}
	if len(activeProfiles[0].AvailableModels) != 2 {
		t.Fatalf("可用模型数 = %d，期望 2", len(activeProfiles[0].AvailableModels))
	}
}

func withTestBuiltinManifest(t *testing.T, manifest presetstore.BuiltinModelsManifestEntryPreset) {
	t.Helper()
	previous := presetstore.Default()
	bundle := previous.Get()
	bundle.BuiltinModelsManifests = &presetstore.BuiltinModelsManifestPreset{
		SchemaVersion: 1,
		Manifests:     []presetstore.BuiltinModelsManifestEntryPreset{manifest},
	}
	presetstore.SetDefault(presetstore.NewPresetStore(bundle))
	t.Cleanup(func() {
		presetstore.SetDefault(previous)
	})
}

func trimTestURLScheme(rawURL string) string {
	return strings.TrimPrefix(strings.TrimPrefix(rawURL, "http://"), "https://")
}

// ── maybeAutoWriteChannelConfig 测试 ──────────────────────────────────────────

// setupTestConfigManagerForDiscovery 创建带指定 messages 渠道的临时 ConfigManager。
func setupTestConfigManagerForDiscovery(t *testing.T, channelUID string, supportedModels []string, modelMapping map[string]string) *config.ConfigManager {
	t.Helper()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				ChannelUID:      channelUID,
				Name:            "test-channel",
				ServiceType:     "claude",
				BaseURL:         "https://api.example.com",
				BaseURLs:        []string{"https://api.example.com"},
				APIKeys:         []string{"sk-test-key"},
				SupportedModels: supportedModels,
				ModelMapping:    modelMapping,
				Status:          "active",
			},
		},
		AutopilotRouting: config.AutopilotRoutingConfig{
			RoutingMode: "shadow",
			KillSwitch:  false,
		},
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")
	err := os.WriteFile(configPath, data, 0600)
	if err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("创建 ConfigManager 失败: %v", err)
	}
	return cfgManager
}

// getChannelSupportedModels 从 ConfigManager 读取指定渠道的 SupportedModels。
func getChannelSupportedModels(t *testing.T, cfgManager *config.ConfigManager, channelUID string) []string {
	t.Helper()
	cfg := cfgManager.GetConfig()
	for _, ch := range cfg.Upstream {
		if ch.ChannelUID == channelUID {
			return ch.SupportedModels
		}
	}
	return nil
}

func TestMaybeAutoWriteChannelConfig_TableDriven(t *testing.T) {
	tests := []struct {
		name            string
		channelUID      string
		supportedModels []string          // 渠道当前 SupportedModels
		modelMapping    map[string]string // 渠道当前 ModelMapping
		endpoints       []EndpointDiscoveryResult
		wantWritten     bool     // 是否应写入
		wantModels      []string // 期望写入的模型（wantWritten=true 时检查）
	}{
		{
			name:            "全部一致且配置为空 -> 写入 SupportedModels",
			channelUID:      "ch_auto_write_001",
			supportedModels: nil,
			modelMapping:    nil,
			endpoints: []EndpointDiscoveryResult{
				{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{"gpt-4o", "gpt-3.5-turbo"}, ModelsCount: 2},
				{KeyMask: "sk-****b", BaseURL: "https://b.example.com", ProtocolOk: true, Models: []string{"gpt-3.5-turbo", "gpt-4o"}, ModelsCount: 2},
			},
			wantWritten: true,
			wantModels:  []string{"gpt-3.5-turbo", "gpt-4o"}, // 字母排序
		},
		{
			name:            "模型列表不一致 -> 不写入",
			channelUID:      "ch_auto_write_002",
			supportedModels: nil,
			modelMapping:    nil,
			endpoints: []EndpointDiscoveryResult{
				{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{"gpt-4o"}, ModelsCount: 1},
				{KeyMask: "sk-****b", BaseURL: "https://b.example.com", ProtocolOk: true, Models: []string{"gpt-4o-mini"}, ModelsCount: 1},
			},
			wantWritten: false,
		},
		{
			name:            "用户已有 SupportedModels -> 即使新探测一致也不覆盖",
			channelUID:      "ch_auto_write_003",
			supportedModels: []string{"old-model-1"},
			modelMapping:    nil,
			endpoints: []EndpointDiscoveryResult{
				{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{"gpt-4o"}, ModelsCount: 1},
				{KeyMask: "sk-****b", BaseURL: "https://b.example.com", ProtocolOk: true, Models: []string{"gpt-4o"}, ModelsCount: 1},
			},
			wantWritten: false,
		},
		{
			name:            "用户已有 ModelMapping -> 即使新探测一致也不写 SupportedModels",
			channelUID:      "ch_auto_write_004",
			supportedModels: nil,
			modelMapping:    map[string]string{"old-model": "new-model"},
			endpoints: []EndpointDiscoveryResult{
				{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{"gpt-4o"}, ModelsCount: 1},
				{KeyMask: "sk-****b", BaseURL: "https://b.example.com", ProtocolOk: true, Models: []string{"gpt-4o"}, ModelsCount: 1},
			},
			wantWritten: false,
		},
		{
			name:            "单 endpoint 渠道（天然一致） -> 正确写入",
			channelUID:      "ch_auto_write_005",
			supportedModels: nil,
			modelMapping:    nil,
			endpoints: []EndpointDiscoveryResult{
				{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{"claude-3-opus", "claude-3-sonnet"}, ModelsCount: 2},
			},
			wantWritten: true,
			wantModels:  []string{"claude-3-opus", "claude-3-sonnet"}, // 已排序
		},
		{
			name:            "所有 endpoint 不可达 -> 不写入",
			channelUID:      "ch_auto_write_006",
			supportedModels: nil,
			modelMapping:    nil,
			endpoints: []EndpointDiscoveryResult{
				{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: false, ErrorMessage: "连接失败"},
				{KeyMask: "sk-****b", BaseURL: "https://b.example.com", ProtocolOk: false, ErrorMessage: "连接失败"},
			},
			wantWritten: false,
		},
		{
			name:            "部分 endpoint 不可达但可达的一致 -> 正确写入",
			channelUID:      "ch_auto_write_007",
			supportedModels: nil,
			modelMapping:    nil,
			endpoints: []EndpointDiscoveryResult{
				{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{"gpt-4o", "gpt-3.5-turbo"}, ModelsCount: 2},
				{KeyMask: "sk-****b", BaseURL: "https://b.example.com", ProtocolOk: false, ErrorMessage: "连接失败"},
			},
			wantWritten: true,
			wantModels:  []string{"gpt-3.5-turbo", "gpt-4o"}, // 字母排序
		},
		{
			name:            "endpoint 模型列表为空 -> 不写入",
			channelUID:      "ch_auto_write_008",
			supportedModels: nil,
			modelMapping:    nil,
			endpoints: []EndpointDiscoveryResult{
				{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{}, ModelsCount: 0},
			},
			wantWritten: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgManager := setupTestConfigManagerForDiscovery(t, tt.channelUID, tt.supportedModels, tt.modelMapping)
			runner := NewAutoDiscoveryRunner(nil, nil)
			channel := &config.UpstreamConfig{
				ChannelUID:      tt.channelUID,
				SupportedModels: tt.supportedModels,
				ModelMapping:    tt.modelMapping,
			}

			runner.maybeAutoWriteChannelConfig(tt.channelUID, channel, tt.endpoints, cfgManager)

			// 从配置中读取实际 SupportedModels
			actualModels := getChannelSupportedModels(t, cfgManager, tt.channelUID)

			if tt.wantWritten {
				if len(actualModels) == 0 {
					t.Fatal("期望写入 SupportedModels，但实际为空")
				}
				if len(actualModels) != len(tt.wantModels) {
					t.Fatalf("模型数量不匹配: 期望 %v, 实际 %v", tt.wantModels, actualModels)
				}
				for i := range tt.wantModels {
					if actualModels[i] != tt.wantModels[i] {
						t.Fatalf("模型列表不匹配: 期望 %v, 实际 %v", tt.wantModels, actualModels)
					}
				}
			} else {
				if tt.supportedModels == nil && len(actualModels) > 0 {
					t.Fatalf("不期望写入，但 SupportedModels 非空: %v", actualModels)
				}
				if tt.supportedModels != nil && len(actualModels) != len(tt.supportedModels) {
					t.Fatalf("不应覆盖已有 SupportedModels: 期望 %v, 实际 %v", tt.supportedModels, actualModels)
				}
			}
		})
	}
}

func TestModelsSetConsistent(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []EndpointDiscoveryResult
		wantNil   bool
		wantLen   int // 非 nil 时返回的列表长度
	}{
		{
			name:      "空输入",
			endpoints: nil,
			wantNil:   true,
		},
		{
			name: "单端点",
			endpoints: []EndpointDiscoveryResult{
				{Models: []string{"m1", "m2"}},
			},
			wantNil: false,
			wantLen: 2,
		},
		{
			name: "多端点一致（顺序不同）",
			endpoints: []EndpointDiscoveryResult{
				{Models: []string{"m1", "m2", "m3"}},
				{Models: []string{"m3", "m1", "m2"}},
				{Models: []string{"m2", "m3", "m1"}},
			},
			wantNil: false,
			wantLen: 3,
		},
		{
			name: "多端点不一致",
			endpoints: []EndpointDiscoveryResult{
				{Models: []string{"m1", "m2"}},
				{Models: []string{"m1", "m3"}},
			},
			wantNil: true,
		},
		{
			name: "数量不同",
			endpoints: []EndpointDiscoveryResult{
				{Models: []string{"m1"}},
				{Models: []string{"m1", "m2"}},
			},
			wantNil: true,
		},
		{
			name: "完全相同",
			endpoints: []EndpointDiscoveryResult{
				{Models: []string{"a", "b"}},
				{Models: []string{"a", "b"}},
			},
			wantNil: false,
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := modelsSetConsistent(tt.endpoints)
			if tt.wantNil {
				if result != nil {
					t.Fatalf("期望 nil, 实际 %v", result)
				}
			} else {
				if result == nil {
					t.Fatal("期望非 nil, 实际 nil")
				}
				if len(result) != tt.wantLen {
					t.Fatalf("长度不匹配: 期望 %d, 实际 %d", tt.wantLen, len(result))
				}
			}
		})
	}
}

func TestSortModels(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "已排序",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "逆序",
			input:    []string{"c", "b", "a"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "空列表",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "单元素",
			input:    []string{"model-x"},
			expected: []string{"model-x"},
		},
		{
			name:     "不修改原切片",
			input:    []string{"z", "a"},
			expected: []string{"a", "z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sortModels(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("长度不匹配: 期望 %d, 实际 %d", len(tt.expected), len(result))
			}
			for i := range tt.expected {
				if result[i] != tt.expected[i] {
					t.Fatalf("结果不匹配: 期望 %v, 实际 %v", tt.expected, result)
				}
			}
		})
	}
}

func TestFindChannelIndexAndKind(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.Config
		channelUID string
		wantIndex  int
		wantKind   string
	}{
		{
			name: "找到 messages 渠道",
			cfg: config.Config{
				Upstream: []config.UpstreamConfig{
					{ChannelUID: "ch_001"},
				},
			},
			channelUID: "ch_001",
			wantIndex:  0,
			wantKind:   "messages",
		},
		{
			name: "找到 chat 渠道",
			cfg: config.Config{
				ChatUpstream: []config.UpstreamConfig{
					{ChannelUID: "ch_chat_001"},
				},
			},
			channelUID: "ch_chat_001",
			wantIndex:  0,
			wantKind:   "chat",
		},
		{
			name: "找到 responses 渠道",
			cfg: config.Config{
				ResponsesUpstream: []config.UpstreamConfig{
					{ChannelUID: "ch_resp_001"},
				},
			},
			channelUID: "ch_resp_001",
			wantIndex:  0,
			wantKind:   "responses",
		},
		{
			name: "找到 vectors 渠道",
			cfg: config.Config{
				VectorsUpstream: []config.UpstreamConfig{
					{ChannelUID: "ch_vec_001"},
				},
			},
			channelUID: "ch_vec_001",
			wantIndex:  0,
			wantKind:   "vectors",
		},
		{
			name: "渠道在多个列表中",
			cfg: config.Config{
				Upstream: []config.UpstreamConfig{
					{ChannelUID: "ch_001"},
				},
				ChatUpstream: []config.UpstreamConfig{
					{ChannelUID: "ch_chat_001"},
				},
			},
			channelUID: "ch_chat_001",
			wantIndex:  0,
			wantKind:   "chat",
		},
		{
			name:       "未找到",
			cfg:        config.Config{},
			channelUID: "ch_nonexistent",
			wantIndex:  -1,
			wantKind:   "",
		},
		{
			name: "多个渠道时找到正确索引",
			cfg: config.Config{
				Upstream: []config.UpstreamConfig{
					{ChannelUID: "ch_a"},
					{ChannelUID: "ch_b"},
					{ChannelUID: "ch_c"},
				},
			},
			channelUID: "ch_b",
			wantIndex:  1,
			wantKind:   "messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index, kind := findChannelIndexAndKind(tt.cfg, tt.channelUID)
			if index != tt.wantIndex {
				t.Fatalf("index: 期望 %d, 实际 %d", tt.wantIndex, index)
			}
			if kind != tt.wantKind {
				t.Fatalf("kind: 期望 %q, 实际 %q", tt.wantKind, kind)
			}
		})
	}
}

func TestMaybeAutoWriteChannelConfig_NilCfgManager(t *testing.T) {
	// cfgManager 为 nil 时不 panic，不写入（runDiscovery 入口已 guard，此处测直接调用行为）
	runner := NewAutoDiscoveryRunner(nil, nil)
	channel := &config.UpstreamConfig{
		ChannelUID: "ch_nil_cfg",
	}
	endpoints := []EndpointDiscoveryResult{
		{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{"gpt-4o"}, ModelsCount: 1},
	}

	// 不应 panic
	runner.maybeAutoWriteChannelConfig("ch_nil_cfg", channel, endpoints, nil)
}

func TestStringSetsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    map[string]bool
		b    map[string]bool
		want bool
	}{
		{"两个空 set", map[string]bool{}, map[string]bool{}, true},
		{"相同内容", map[string]bool{"a": true, "b": true}, map[string]bool{"b": true, "a": true}, true},
		{"不同内容", map[string]bool{"a": true}, map[string]bool{"b": true}, false},
		{"不同长度", map[string]bool{"a": true}, map[string]bool{"a": true, "b": true}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringSetsEqual(tt.a, tt.b)
			if got != tt.want {
				t.Fatalf("期望 %v, 实际 %v", tt.want, got)
			}
		})
	}
}
