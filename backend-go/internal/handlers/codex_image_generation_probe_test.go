package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

type codexImageProbeRequest struct {
	APIKey   string
	Mode     imageGenerationProbeMode
	Model    string
	HasTools bool
}

type codexImageProbeResponse struct {
	StatusCode int
	Body       string
	SSE        bool
}

func newCodexImageProbeServer(
	t *testing.T,
	handle func(codexImageProbeRequest) codexImageProbeResponse,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取探测请求失败: %v", err)
			http.Error(w, "read request", http.StatusInternalServerError)
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("解析探测请求失败: %v", err)
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		mode := imageGenerationProbeHosted
		tools, hasTools := payload["tools"].([]interface{})
		if hasTools && len(tools) > 0 {
			if tool, ok := tools[0].(map[string]interface{}); ok && tool["type"] == "namespace" {
				mode = imageGenerationProbeNamespace
			}
		}
		apiKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		response := handle(codexImageProbeRequest{
			APIKey:   apiKey,
			Mode:     mode,
			Model:    fmt.Sprint(payload["model"]),
			HasTools: hasTools,
		})
		if response.SSE {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		statusCode := response.StatusCode
		if statusCode == 0 {
			statusCode = http.StatusBadRequest
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = io.WriteString(w, response.Body)
	}))
}

func newResponsesConfigManager(t *testing.T, channel config.UpstreamConfig) *config.ConfigManager {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	body, err := json.Marshal(config.Config{ResponsesUpstream: []config.UpstreamConfig{channel}})
	if err != nil {
		t.Fatalf("序列化测试配置失败: %v", err)
	}
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() {
		_ = cfgManager.Close()
	})
	return cfgManager
}

func TestProbeImageGenerationToolModesRequiresBothCodexForms(t *testing.T) {
	tests := []struct {
		name          string
		rejectedMode  imageGenerationProbeMode
		wantHosted    ImageGenerationProbeState
		wantNamespace ImageGenerationProbeState
		wantAggregate ImageGenerationProbeState
	}{
		{
			name:          "hosted 支持但 namespace 拒绝",
			rejectedMode:  imageGenerationProbeNamespace,
			wantHosted:    ImageGenerationProbeSupported,
			wantNamespace: ImageGenerationProbeUnsupported,
			wantAggregate: ImageGenerationProbeUnsupported,
		},
		{
			name:          "hosted 拒绝但 namespace 支持",
			rejectedMode:  imageGenerationProbeHosted,
			wantHosted:    ImageGenerationProbeUnsupported,
			wantNamespace: ImageGenerationProbeSupported,
			wantAggregate: ImageGenerationProbeUnsupported,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newCodexImageProbeServer(t, func(req codexImageProbeRequest) codexImageProbeResponse {
				if req.Model != "actual-review-model" {
					t.Errorf("上游收到模型 %q，期望 actual-review-model", req.Model)
				}
				if req.Mode == tt.rejectedMode {
					return codexImageProbeResponse{
						StatusCode: http.StatusForbidden,
						Body:       `{"error":{"type":"permission_error","message":"Image generation is not enabled for this group"}}`,
					}
				}
				return codexImageProbeResponse{SSE: true}
			})
			defer server.Close()

			channel := &config.UpstreamConfig{ServiceType: "responses", AuthHeader: "bearer"}
			results := probeImageGenerationToolModes(context.Background(), channel, "responses", "sk-probe-key", server.URL, "actual-review-model")
			if got := probeStateForMode(results, imageGenerationProbeHosted); got != tt.wantHosted {
				t.Fatalf("hosted 状态 = %s，期望 %s", got, tt.wantHosted)
			}
			if got := probeStateForMode(results, imageGenerationProbeNamespace); got != tt.wantNamespace {
				t.Fatalf("namespace 状态 = %s，期望 %s", got, tt.wantNamespace)
			}
			if got := aggregateImageGenerationProbeState(results); got != tt.wantAggregate {
				t.Fatalf("汇总状态 = %s，期望 %s", got, tt.wantAggregate)
			}
		})
	}
}

func TestExecuteCodexImageGenerationCapabilityTestUsesMappedModelAndAllKeys(t *testing.T) {
	const (
		supportedKey   = "sk-supported-123456"
		unsupportedKey = "sk-unsupported-654321"
	)
	seen := make(map[string]int)
	var mu sync.Mutex
	server := newCodexImageProbeServer(t, func(req codexImageProbeRequest) codexImageProbeResponse {
		mu.Lock()
		seen[req.APIKey]++
		mu.Unlock()
		if req.Model != "actual-review-model" {
			t.Errorf("上游收到模型 %q，期望 actual-review-model", req.Model)
		}
		if req.APIKey == unsupportedKey {
			return codexImageProbeResponse{
				StatusCode: http.StatusForbidden,
				Body:       `{"error":{"type":"permission_error","message":"Image generation is not enabled for this group"}}`,
			}
		}
		return codexImageProbeResponse{SSE: true}
	})
	defer server.Close()

	channel := &config.UpstreamConfig{
		Name:        "multi-key-responses",
		BaseURL:     server.URL,
		APIKeys:     []string{supportedKey, unsupportedKey},
		AuthHeader:  "bearer",
		ServiceType: "responses",
		ModelMapping: map[string]string{
			codexAutoReviewModel: "actual-review-model",
		},
	}
	result := executeCodexImageGenerationCapabilityTest(context.Background(), channel, codexAutoReviewModel, 5*time.Second, nil, 0, "responses")
	if !result.Success || result.ActualModel != "actual-review-model" {
		t.Fatalf("探测结果 = %+v", result)
	}
	if result.CodexImageGeneration.SupportedKeys != 1 || result.CodexImageGeneration.UnsupportedKeys != 1 {
		t.Fatalf("Key 汇总 = %+v", result.CodexImageGeneration)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen[supportedKey] != 2 || seen[unsupportedKey] != 2 {
		t.Fatalf("每个 Key 应探测两种工具形态，实际请求数 = %#v", seen)
	}
}

func TestExecuteCodexImageGenerationCapabilityTestRestrictsOnlyRejectedKeyModel(t *testing.T) {
	const (
		supportedKey   = "sk-supported-123456"
		unsupportedKey = "sk-unsupported-654321"
		actualModel    = "actual-review-model"
	)
	server := newCodexImageProbeServer(t, func(req codexImageProbeRequest) codexImageProbeResponse {
		if req.APIKey == unsupportedKey {
			return codexImageProbeResponse{
				StatusCode: http.StatusForbidden,
				Body:       `{"error":{"type":"permission_error","message":"Image generation is not enabled for this group"}}`,
			}
		}
		return codexImageProbeResponse{SSE: true}
	})
	defer server.Close()

	channel := config.UpstreamConfig{
		Name:        "responses-restriction",
		BaseURL:     server.URL,
		APIKeys:     []string{unsupportedKey, supportedKey},
		AuthHeader:  "bearer",
		ServiceType: "responses",
		ModelMapping: map[string]string{
			codexAutoReviewModel: actualModel,
		},
	}
	cfgManager := newResponsesConfigManager(t, channel)
	configured := cfgManager.GetConfig().ResponsesUpstream[0]
	result := executeCodexImageGenerationCapabilityTest(context.Background(), &configured, codexAutoReviewModel, 5*time.Second, cfgManager, 0, "responses")
	if !result.Success {
		t.Fatalf("至少一个 Key 支持时渠道应成功，结果 = %+v", result)
	}

	updated := cfgManager.GetConfig().ResponsesUpstream[0]
	if !updated.IsKeyModelDisabledNow(unsupportedKey, actualModel, time.Now()) {
		t.Fatal("拒绝生图工具的 Key×实际模型组合未被限制")
	}
	if updated.IsKeyModelDisabledNow(supportedKey, actualModel, time.Now()) {
		t.Fatal("支持生图工具的 Key 不应被限制")
	}
	if updated.IsKeyModelDisabledNow(unsupportedKey, "another-model", time.Now()) {
		t.Fatal("同一 Key 的其他模型不应受影响")
	}
	if len(updated.DisabledKeyModels) != 1 || updated.DisabledKeyModels[0].Reason != codexImageGenerationRestrictionReason {
		t.Fatalf("组合限制 = %#v", updated.DisabledKeyModels)
	}
}

func TestExecuteCodexImageGenerationCapabilityTestStripFallback(t *testing.T) {
	const apiKey = "sk-strip-fallback-123456"
	var plainRequests int
	server := newCodexImageProbeServer(t, func(req codexImageProbeRequest) codexImageProbeResponse {
		if !req.HasTools {
			plainRequests++
			return codexImageProbeResponse{SSE: true}
		}
		return codexImageProbeResponse{
			StatusCode: http.StatusForbidden,
			Body:       `{"error":{"type":"permission_error","message":"Image generation is not enabled for this group"}}`,
		}
	})
	defer server.Close()

	channel := config.UpstreamConfig{
		Name:                     "responses-strip",
		BaseURL:                  server.URL,
		APIKeys:                  []string{apiKey},
		AuthHeader:               "bearer",
		ServiceType:              "responses",
		StripImageGenerationTool: config.BoolPtr(true),
	}
	cfgManager := newResponsesConfigManager(t, channel)
	configured := cfgManager.GetConfig().ResponsesUpstream[0]
	result := executeCodexImageGenerationCapabilityTest(context.Background(), &configured, codexAutoReviewModel, 5*time.Second, cfgManager, 0, "responses")
	if !result.Success || !result.CodexImageGeneration.CompatibleViaStrip {
		t.Fatalf("剥离兼容探测结果 = %+v", result)
	}
	if plainRequests != 1 {
		t.Fatalf("普通 Responses 兼容探测次数 = %d，期望 1", plainRequests)
	}
	if got := cfgManager.GetConfig().ResponsesUpstream[0].DisabledKeyModels; len(got) != 0 {
		t.Fatalf("开启工具剥离时不应写入组合限制: %#v", got)
	}
}

func TestExecuteCodexImageGenerationCapabilityTestRestoresProbeRestriction(t *testing.T) {
	const (
		apiKey      = "sk-restored-123456"
		actualModel = "actual-review-model"
	)
	server := newCodexImageProbeServer(t, func(codexImageProbeRequest) codexImageProbeResponse {
		return codexImageProbeResponse{SSE: true}
	})
	defer server.Close()

	channel := config.UpstreamConfig{
		Name:        "responses-restore",
		BaseURL:     server.URL,
		APIKeys:     []string{apiKey},
		AuthHeader:  "bearer",
		ServiceType: "responses",
		ModelMapping: map[string]string{
			codexAutoReviewModel: actualModel,
		},
		DisabledKeyModels: []config.DisabledKeyModelInfo{{
			Key:        apiKey,
			Model:      actualModel,
			Reason:     codexImageGenerationRestrictionReason,
			DisabledAt: time.Now().Add(-time.Minute).Format(time.RFC3339),
			RecoverAt:  time.Now().Add(time.Hour).Format(time.RFC3339),
		}},
	}
	cfgManager := newResponsesConfigManager(t, channel)
	configured := cfgManager.GetConfig().ResponsesUpstream[0]
	result := executeCodexImageGenerationCapabilityTest(context.Background(), &configured, codexAutoReviewModel, 5*time.Second, cfgManager, 0, "responses")
	if !result.Success {
		t.Fatalf("复测应成功，结果 = %+v", result)
	}
	if got := cfgManager.GetConfig().ResponsesUpstream[0].DisabledKeyModels; len(got) != 0 {
		t.Fatalf("探针创建的旧限制未恢复: %#v", got)
	}
}

func TestImageGenerationProbeTransientErrorsStayInconclusive(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{
			name:       "认证错误",
			statusCode: http.StatusUnauthorized,
			body:       `{"error":{"type":"authentication_error","message":"Invalid API key"}}`,
		},
		{
			name:       "余额不足",
			statusCode: http.StatusPaymentRequired,
			body:       `{"error":{"type":"insufficient_quota","message":"Insufficient balance"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newCodexImageProbeServer(t, func(codexImageProbeRequest) codexImageProbeResponse {
				return codexImageProbeResponse{StatusCode: tt.statusCode, Body: tt.body}
			})
			defer server.Close()
			result := probeImageGenerationToolMode(
				context.Background(),
				&config.UpstreamConfig{ServiceType: "responses", AuthHeader: "bearer"},
				"responses",
				"sk-transient-123456",
				server.URL,
				"actual-review-model",
				imageGenerationProbeHosted,
			)
			if result.State != ImageGenerationProbeInconclusive {
				t.Fatalf("状态 = %s，期望 inconclusive；诊断 = %s", result.State, result.Diagnostic)
			}
		})
	}

	t.Run("超时", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			http.Error(w, "late response", http.StatusGatewayTimeout)
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		result := probeImageGenerationToolMode(
			ctx,
			&config.UpstreamConfig{ServiceType: "responses", AuthHeader: "bearer"},
			"responses",
			"sk-timeout-123456",
			server.URL,
			"actual-review-model",
			imageGenerationProbeHosted,
		)
		if result.State != ImageGenerationProbeInconclusive {
			t.Fatalf("状态 = %s，期望 inconclusive", result.State)
		}
	})
}
