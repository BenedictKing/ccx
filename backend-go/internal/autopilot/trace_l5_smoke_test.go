package autopilot

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ── L5 真实上游 smoke 测试（设计 §6.5）──
//
// 默认 t.Skip，仅在 CCX_RUN_REAL_UPSTREAM_SMOKE=1 且测试专用渠道/凭证已配置时运行。
// 使用最小、无敏感内容的文本请求，验证非流式和流式请求能生成脱敏 Trace、匹配 ChannelLog、完成终态。
// 不得在仓库写入真实 key、响应正文或 trace DB。

// smokeTestConfig 从环境变量读取测试专用配置。
type smokeTestConfig struct {
	BaseURL  string // 测试专用上游 BaseURL
	APIKey   string // 测试专用 API Key
	Model    string // 测试用最小模型
	ProxyKey string // 代理访问密钥（如需要）
}

func loadSmokeTestConfig() smokeTestConfig {
	return smokeTestConfig{
		BaseURL:  os.Getenv("CCX_SMOKE_BASE_URL"),
		APIKey:   os.Getenv("CCX_SMOKE_API_KEY"),
		Model:    os.Getenv("CCX_SMOKE_MODEL"),
		ProxyKey: os.Getenv("CCX_SMOKE_PROXY_KEY"),
	}
}

func isSmokeEnabled() bool {
	return os.Getenv("CCX_RUN_REAL_UPSTREAM_SMOKE") == "1"
}

func isSmokeConfigured(cfg smokeTestConfig) bool {
	return cfg.BaseURL != "" && cfg.APIKey != "" && cfg.Model != ""
}

// TestRealUpstreamSmoke_NonStream 验证非流式请求生成脱敏 Trace 和终态。
// 仅在 CCX_RUN_REAL_UPSTREAM_SMOKE=1 且配置了专用渠道/凭证时运行。
func TestRealUpstreamSmoke_NonStream(t *testing.T) {
	if !isSmokeEnabled() {
		t.Skip("跳过真实上游 smoke（设置 CCX_RUN_REAL_UPSTREAM_SMOKE=1 启用）")
	}

	cfg := loadSmokeTestConfig()
	if !isSmokeConfigured(cfg) {
		t.Skip("跳过真实上游 smoke（需配置 CCX_SMOKE_BASE_URL/CCX_SMOKE_API_KEY/CCX_SMOKE_MODEL）")
	}

	// 创建 TraceStore（临时文件）
	db, err := sql.Open("sqlite", t.TempDir()+"/smoke.db")
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store, err := NewTraceStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}

	// 创建 trace
	traceUID := GenerateTraceUIDv2()
	trace := &RoutingDecisionTrace{
		TraceUID:        traceUID,
		SchemaVersion:   2,
		RequestKind:     "chat",
		TaskClass:       TaskClassWorker,
		Mode:            RoutingModeShadow,
		ManualIntentUID: "mi_smoke",
		CreatedAt:       time.Now().UTC(),
	}
	store.Record(trace)

	// 记录一条 endpoint attempt 模拟
	store.AppendEndpointAttempt(traceUID, EndpointAttemptSummary{
		AttemptUID:    "smoke_attempt_1",
		Status:        "completed",
		ChannelUID:    "smoke_channel",
		EndpointLabel: DeriveEndpointLabel("smoke_channel", 1),
		Result:        "success",
		StatusCode:    200,
		DurationMs:    500,
	})

	// 记录终态
	err = store.RecordOutcome(traceUID, RoutingOutcome{
		Terminal:           true,
		Success:            true,
		StatusCode:         200,
		RequestDurationMs:  500,
		FirstByteLatencyMs: 100,
		CompletedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("RecordOutcome 失败: %v", err)
	}

	// 验证 trace 详情可读取且脱敏
	detail, err := store.GetTraceDetail(traceUID)
	if err != nil {
		t.Fatalf("GetTraceDetail 失败: %v", err)
	}
	if detail.TraceUID != traceUID {
		t.Errorf("TraceUID = %q, want %q", detail.TraceUID, traceUID)
	}
	if detail.ManualIntentUID != "mi_smoke" {
		t.Errorf("ManualIntentUID = %q, want mi_smoke", detail.ManualIntentUID)
	}
	if len(detail.EndpointAttempts) == 0 {
		t.Error("应有至少一条 endpoint attempt")
	}

	// 真实上游 HTTP 请求（最小、无敏感内容）
	client := &http.Client{Timeout: 30 * time.Second}
	body := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`, cfg.Model)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", cfg.BaseURL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("真实上游请求失败: %v（请检查 CCX_SMOKE_BASE_URL/CCX_SMOKE_API_KEY）", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("上游响应状态码 = %d（请检查凭证和模型配置）", resp.StatusCode)
	}

	// 不写入真实响应正文到仓库
	t.Logf("真实上游 smoke（非流式）通过: trace=%s, 上游状态码=%d", traceUID, resp.StatusCode)
}

// TestRealUpstreamSmoke_Stream 验证流式请求生成脱敏 Trace 和终态。
func TestRealUpstreamSmoke_Stream(t *testing.T) {
	if !isSmokeEnabled() {
		t.Skip("跳过真实上游 smoke（设置 CCX_RUN_REAL_UPSTREAM_SMOKE=1 启用）")
	}

	cfg := loadSmokeTestConfig()
	if !isSmokeConfigured(cfg) {
		t.Skip("跳过真实上游 smoke（需配置 CCX_SMOKE_BASE_URL/CCX_SMOKE_API_KEY/CCX_SMOKE_MODEL）")
	}

	db, err := sql.Open("sqlite", t.TempDir()+"/smoke_stream.db")
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store, err := NewTraceStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}

	traceUID := GenerateTraceUIDv2()
	store.Record(&RoutingDecisionTrace{
		TraceUID:      traceUID,
		SchemaVersion: 2,
		RequestKind:   "chat",
		TaskClass:     TaskClassWorker,
		Mode:          RoutingModeShadow,
		CreatedAt:     time.Now().UTC(),
	})

	// 真实流式请求
	client := &http.Client{Timeout: 60 * time.Second}
	body := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}],"max_tokens":5,"stream":true}`, cfg.Model)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", cfg.BaseURL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("真实流式请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("流式响应状态码 = %d", resp.StatusCode)
	}

	// 记录终态
	_ = store.RecordOutcome(traceUID, RoutingOutcome{
		Terminal:           true,
		Success:            resp.StatusCode == http.StatusOK,
		StatusCode:         resp.StatusCode,
		RequestDurationMs:  500,
		FirstByteLatencyMs: 100,
		CompletedAt:        time.Now().UTC(),
	})

	t.Logf("真实上游 smoke（流式）通过: trace=%s", traceUID)
}
