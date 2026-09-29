package autopilot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
)

func TestScoreProviderQualityOutput(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		wantStrict   bool
		wantRequired int
		wantCorrect  int
		wantScoreMin float64
		wantScoreMax float64
	}{
		{
			name:         "严格正确 JSON",
			text:         `{"answer":323,"sequence":13,"checksum":"ABC"}`,
			wantStrict:   true,
			wantRequired: 3,
			wantCorrect:  3,
			wantScoreMin: 1,
			wantScoreMax: 1,
		},
		{
			name:         "markdown 包裹仍可判定但格式扣分",
			text:         "```json\n{\"answer\":323,\"sequence\":13,\"checksum\":\"ABC\"}\n```",
			wantStrict:   false,
			wantRequired: 3,
			wantCorrect:  3,
			wantScoreMin: 0.88,
			wantScoreMax: 0.89,
		},
		{
			name:         "字段齐全但答案错误",
			text:         `{"answer":1,"sequence":2,"checksum":"XYZ"}`,
			wantStrict:   true,
			wantRequired: 3,
			wantCorrect:  0,
			wantScoreMin: 0.70,
			wantScoreMax: 0.70,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, evidence, score := scoreProviderQualityOutput(tt.text, 100)
			if evidence.StrictJSON != tt.wantStrict {
				t.Fatalf("StrictJSON=%v, want %v", evidence.StrictJSON, tt.wantStrict)
			}
			if evidence.RequiredFields != tt.wantRequired || evidence.CorrectFields != tt.wantCorrect {
				t.Fatalf("evidence=%+v", evidence)
			}
			if score < tt.wantScoreMin || score > tt.wantScoreMax {
				t.Fatalf("score=%v, want [%v,%v]", score, tt.wantScoreMin, tt.wantScoreMax)
			}
		})
	}
}

func TestBuildProviderQualityRequestProtocols(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		baseURL     string
		apiKey      string
		wantPath    string
		wantHeader  string
		wantValue   string
	}{
		{"claude", "claude", "https://example.com", "sk-ant-test", "/v1/messages", "x-api-key", "sk-ant-test"},
		{"chat versioned", "openai", "https://example.com/v1", "sk-chat", "/v1/chat/completions", "Authorization", "Bearer sk-chat"},
		{"responses hash suffix", "responses", "https://example.com#", "sk-responses", "/responses", "Authorization", "Bearer sk-responses"},
		{"gemini", "gemini", "https://example.com", "gemini-key", "/v1beta/models/gemini-2.5-pro:generateContent", "x-goog-api-key", "gemini-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := &KeyEndpointProfile{ServiceType: tt.serviceType, BaseURL: tt.baseURL}
			upstream := &config.UpstreamConfig{ServiceType: tt.serviceType}
			req, err := buildProviderQualityRequest(context.Background(), profile, upstream, tt.apiKey, "gemini-2.5-pro")
			if err != nil {
				t.Fatalf("buildProviderQualityRequest: %v", err)
			}
			if req.URL.Path != tt.wantPath {
				t.Fatalf("path=%q, want %q", req.URL.Path, tt.wantPath)
			}
			if got := req.Header.Get(tt.wantHeader); got != tt.wantValue {
				t.Fatalf("header %s=%q, want %q", tt.wantHeader, got, tt.wantValue)
			}
		})
	}
}

func TestBuildProviderQualityRequestReasoningControl(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		modelID     string
		assert      func(t *testing.T, body map[string]any)
	}{
		{
			name:        "MiMo Claude 兼容端点关闭 thinking",
			serviceType: "claude",
			modelID:     "mimo-v2.5-pro",
			assert: func(t *testing.T, body map[string]any) {
				thinking, _ := body["thinking"].(map[string]any)
				if thinking["type"] != "disabled" {
					t.Fatalf("thinking=%#v", thinking)
				}
			},
		},
		{
			name:        "MiMo Chat 使用最低 effort",
			serviceType: "openai",
			modelID:     "mimo-v2.5-pro",
			assert: func(t *testing.T, body map[string]any) {
				if body["reasoning_effort"] != "low" {
					t.Fatalf("reasoning_effort=%#v", body["reasoning_effort"])
				}
			},
		},
		{
			name:        "MiMo Responses 使用最低 effort",
			serviceType: "responses",
			modelID:     "mimo-v2.5-pro",
			assert: func(t *testing.T, body map[string]any) {
				reasoning, _ := body["reasoning"].(map[string]any)
				if reasoning["effort"] != "low" {
					t.Fatalf("reasoning=%#v", reasoning)
				}
			},
		},
		{
			name:        "adaptive-only Claude 不注入 thinking",
			serviceType: "claude",
			modelID:     "claude-opus-4-8",
			assert: func(t *testing.T, body map[string]any) {
				if _, exists := body["thinking"]; exists {
					t.Fatalf("adaptive-only 不应注入 thinking: %#v", body["thinking"])
				}
			},
		},
		{
			name:        "adaptive-always-on Claude 不注入 thinking",
			serviceType: "claude",
			modelID:     "claude-sonnet-5-5",
			assert: func(t *testing.T, body map[string]any) {
				if _, exists := body["thinking"]; exists {
					t.Fatalf("adaptive_always_on 不应注入 thinking: %#v", body["thinking"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := &KeyEndpointProfile{ServiceType: tt.serviceType, BaseURL: "https://example.com"}
			upstream := &config.UpstreamConfig{ServiceType: tt.serviceType}
			req, err := buildProviderQualityRequest(context.Background(), profile, upstream, "sk-test", tt.modelID)
			if err != nil {
				t.Fatal(err)
			}
			defer errutil.IgnoreDeferred(req.Body.Close)
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatal(err)
			}
			tt.assert(t, body)
		})
	}
}

func TestProviderQualityProbePersistsSanitizedEvidence(t *testing.T) {
	const apiKey = "sk-provider-quality-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("Authorization=%q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("请求 JSON 无效: %v", err)
		}
		messages, _ := body["messages"].([]any)
		if len(messages) != 1 || !strings.Contains(messages[0].(map[string]any)["content"].(string), "17*19") {
			t.Errorf("未使用固定 canary: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"answer\":323,\"sequence\":13,\"checksum\":\"ABC\"}"}}]}`))
	}))
	defer server.Close()

	probe, modelStore, endpoint := newProviderQualityProbeFixture(t, server.URL, apiKey, 3)
	result, err := probe.Probe(context.Background(), ProviderQualityProbeRequest{
		EndpointUID: endpoint.EndpointUID,
		ModelID:     "test-quality-model",
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !result.Persisted || result.SuccessCount != 1 || result.Score != 1 || result.Confidence != 0.6 {
		t.Fatalf("result=%+v", result)
	}
	if result.Budget.Used != 1 || result.Budget.Remaining != 2 {
		t.Fatalf("budget=%+v", result.Budget)
	}

	metricsKey := computeMetricsIdentityKey(server.URL, apiKey, "openai")
	stored := modelStore.Get(endpoint.ChannelUID, endpoint.ChannelKind, metricsKey, "test-quality-model")
	if stored == nil {
		t.Fatal("ProviderQuality 画像未写入")
	}
	if stored.ProviderQualitySource != "probe" || stored.ProviderQualityScore != 1 || stored.ProviderQualityConfidence != 0.6 || stored.ProviderQualityProbeVersion != providerQualityCanaryVersion {
		t.Fatalf("stored=%+v", stored)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), apiKey) || strings.Contains(string(encoded), `"answer":323`) {
		t.Fatalf("响应泄漏密钥或原始模型输出: %s", encoded)
	}
}

func TestProviderQualityProbeBudgetReservationIsAtomic(t *testing.T) {
	const apiKey = "sk-budget-test"
	probe, _, endpoint := newProviderQualityProbeFixture(t, "https://example.invalid", apiKey, 2)
	_, err := probe.Probe(context.Background(), ProviderQualityProbeRequest{
		EndpointUID: endpoint.EndpointUID,
		ModelID:     "test-model",
		Repetitions: 3,
	})
	var probeErr *ProviderQualityProbeError
	if err == nil || !errors.As(err, &probeErr) || probeErr.Code != "probe_budget_exhausted" {
		t.Fatalf("err=%v", err)
	}
	if state := probe.BudgetState(); state.Used != 0 || state.Remaining != 2 {
		t.Fatalf("预算不应部分扣减: %+v", state)
	}
}

func TestProviderQualityProbeDoesNotOverrideUserFeedback(t *testing.T) {
	const apiKey = "sk-user-feedback-test"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"answer\":323,\"sequence\":13,\"checksum\":\"ABC\"}"}}]}`))
	}))
	defer server.Close()

	probe, modelStore, endpoint := newProviderQualityProbeFixture(t, server.URL, apiKey, 2)
	metricsKey := computeMetricsIdentityKey(server.URL, apiKey, "openai")
	existing := &ModelProfile{
		ChannelUID:                endpoint.ChannelUID,
		ChannelKind:               endpoint.ChannelKind,
		ServiceType:               endpoint.ServiceType,
		MetricsKey:                metricsKey,
		ModelID:                   "feedback-model",
		ProviderQualityScore:      0.2,
		ProviderQualitySource:     "user_feedback",
		ProviderQualityConfidence: 0.9,
	}
	if err := modelStore.Upsert(existing); err != nil {
		t.Fatal(err)
	}

	result, err := probe.Probe(context.Background(), ProviderQualityProbeRequest{
		EndpointUID: endpoint.EndpointUID,
		ModelID:     existing.ModelID,
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.Persisted || result.PersistNote != "user_feedback_override" {
		t.Fatalf("result=%+v", result)
	}
	stored := modelStore.Get(endpoint.ChannelUID, endpoint.ChannelKind, metricsKey, existing.ModelID)
	if stored.ProviderQualityScore != 0.2 || stored.ProviderQualitySource != "user_feedback" {
		t.Fatalf("用户反馈被覆盖: %+v", stored)
	}
}

func newProviderQualityProbeFixture(
	t *testing.T,
	baseURL string,
	apiKey string,
	dailyBudget int,
) (*ProviderQualityProbe, *ModelProfileStore, *KeyEndpointProfile) {
	t.Helper()
	db := newTestDB(t)
	profileStore, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	modelStore, err := NewModelProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewModelProfileStoreWithDB: %v", err)
	}

	endpoint := &KeyEndpointProfile{
		EndpointUID: GenerateEndpointUID("ch-quality", baseURL, KeyHashFromAPIKey(apiKey)),
		ChannelUID:  "ch-quality",
		ChannelKind: "messages",
		ServiceType: "openai",
		BaseURL:     baseURL,
		KeyHash:     KeyHashFromAPIKey(apiKey),
		MetricsKey:  KeyHashFromAPIKey(apiKey),
	}
	if err := profileStore.Upsert(endpoint); err != nil {
		t.Fatalf("profile Upsert: %v", err)
	}

	cfgManager, cleanup := createTestConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{
			{
				ChannelUID:  endpoint.ChannelUID,
				BaseURL:     baseURL,
				APIKeys:     []string{apiKey},
				ServiceType: endpoint.ServiceType,
			},
		},
	})
	t.Cleanup(cleanup)

	probe := NewProviderQualityProbe(
		profileStore,
		modelStore,
		cfgManager,
		func(channelUID, keyHash string) (string, bool) {
			return apiKey, channelUID == endpoint.ChannelUID && keyHash == endpoint.KeyHash
		},
		ProviderQualityProbeConfig{
			DailyBudget:    dailyBudget,
			RequestTimeout: 2 * time.Second,
		},
	)
	return probe, modelStore, endpoint
}
