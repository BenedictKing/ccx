package autopilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// TestDiscoverEndpointProtocolsRecordsOnlyVerifiedModels 验证逐模型探测只把
// 探测成功的模型写入 ProtocolModels，而不是沿用整份 models 清单。
func TestDiscoverEndpointProtocolsRecordsOnlyVerifiedModels(t *testing.T) {
	var mu sync.Mutex
	probedModels := make(map[string]struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		probedModels[body.Model] = struct{}{}
		mu.Unlock()
		if body.Model == "gpt-supported" {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model is unavailable"}`))
	}))
	defer srv.Close()

	runner := NewAutoDiscoveryRunner(nil, nil)
	runner.client = srv.Client()
	result := EndpointDiscoveryResult{
		ProtocolOk: true,
		Models:     []string{"gpt-supported", "gpt-unsupported"},
	}
	// serviceType=responses 让 responses 是原生协议（用 models API 权威清单，不逐模型探测），
	// chat 是非原生协议，走逐模型探测路径。
	channel := &config.UpstreamConfig{ServiceType: "responses"}

	runner.discoverEndpointProtocols(context.Background(), channel, srv.URL, "sk-test", &result)

	if got := strings.Join(result.ProtocolModels["responses"], ","); got != "gpt-supported,gpt-unsupported" {
		t.Fatalf("原生协议 responses 应沿用完整 models 清单，got=%q", got)
	}
	if got := strings.Join(result.ProtocolModels["chat"], ","); got != "gpt-supported" {
		t.Fatalf("chat 协议应只保留探测成功的模型子集，got=%q", got)
	}
	mu.Lock()
	_, supportedProbed := probedModels["gpt-supported"]
	_, unsupportedProbed := probedModels["gpt-unsupported"]
	mu.Unlock()
	if !supportedProbed || !unsupportedProbed {
		t.Fatalf("两个候选模型都应被逐个探测: supported=%v unsupported=%v", supportedProbed, unsupportedProbed)
	}
	if result.ProtocolDiscoverySource["chat"] != "protocol_model_probe" {
		t.Fatalf("chat source=%q, want protocol_model_probe", result.ProtocolDiscoverySource["chat"])
	}
	if !strings.Contains(result.ProtocolDiscoveryMessage["chat"], "1") {
		t.Fatalf("chat message 应包含成功计数, got=%q", result.ProtocolDiscoveryMessage["chat"])
	}
}

// TestDiscoverEndpointProtocolsRateLimitedNotCountedAsFailure 验证 429 不计入失败，
// 且不会污染成功模型子集。
func TestDiscoverEndpointProtocolsRateLimitedNotCountedAsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	runner := NewAutoDiscoveryRunner(nil, nil)
	runner.client = srv.Client()
	result := EndpointDiscoveryResult{
		ProtocolOk: true,
		Models:     []string{"gpt-a"},
	}
	channel := &config.UpstreamConfig{ServiceType: "responses"}

	runner.discoverEndpointProtocols(context.Background(), channel, srv.URL, "sk-test", &result)

	if _, ok := result.ProtocolModels["chat"]; ok {
		t.Fatalf("限流不应产生任何已验证模型，got=%v", result.ProtocolModels["chat"])
	}
	if result.ProtocolDiscoveryError["chat"] == "" {
		t.Fatal("全部限流时应记录错误说明，避免误判为已探测成功")
	}
}

// TestPrioritizeProtocolProbeModelsCapsCandidateCount 验证候选模型截断按优先级前缀排序，
// 并遵守 limit 上限。
func TestPrioritizeProtocolProbeModelsCapsCandidateCount(t *testing.T) {
	models := []string{"unrelated-1", "gpt-5.4", "unrelated-2", "gpt-5.6-luna"}
	got := prioritizeProtocolProbeModels("chat", models, 2)
	if len(got) != 2 {
		t.Fatalf("截断后应剩 2 个模型, got=%v", got)
	}
	for _, model := range got {
		if !strings.HasPrefix(model, "gpt-") {
			t.Fatalf("chat 协议优先级前缀应优先选中 gpt- 模型, got=%v", got)
		}
	}
}
