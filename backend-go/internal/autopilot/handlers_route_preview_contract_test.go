package autopilot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// ── Route Preview 契约测试（implementation-gap-remediation-plan Phase C）──

// postRoutePreview 向给定 router 发起一次 route-preview 请求。
// 注意 setupRoutePreviewTestRouter 挂载的路径无 /api 前缀（与生产 /api group
// 挂载等价，仅前缀不同）；WebAuth 端到端测试自行构造带 /api 前缀的 router。
func postRoutePreview(t *testing.T, router *gin.Engine, payload string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/autopilot/route-preview", bytes.NewReader([]byte(payload)))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestRoutePreviewHandler_SmartRouterNilReturns503(t *testing.T) {
	router := setupRoutePreviewTestRouter(t, nil, nil)
	w := postRoutePreview(t, router, `{"channelKind":"messages","model":"claude-sonnet-4"}`, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("期望 503（与 diagnose 契约一致），实际=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "SmartRouter 未初始化") {
		t.Fatalf("响应应包含错误说明, body=%s", w.Body.String())
	}
}

func TestRoutePreviewHandler_UnsupportedChannelKindReturns400(t *testing.T) {
	cfg := baseTestConfig()
	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	smartRouter := createTestSmartRouter(t, cfgManager)

	router := setupRoutePreviewTestRouter(t, smartRouter, nil)
	w := postRoutePreview(t, router, `{"channelKind":"grpc","model":"m"}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400（不支持 channelKind 不退化为 messages），实际=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "channelKind") {
		t.Fatalf("错误信息应指明 channelKind 问题, body=%s", w.Body.String())
	}
}

func TestRoutePreviewHandler_InvalidNestedBodyReturns400(t *testing.T) {
	cfg := baseTestConfig()
	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	smartRouter := createTestSmartRouter(t, cfgManager)

	router := setupRoutePreviewTestRouter(t, smartRouter, nil)
	w := postRoutePreview(t, router, `{"channelKind":"messages","body":"{not valid json"}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400（嵌套 body 非法 JSON），实际=%d", w.Code)
	}
	// 数字/数组等非对象形态同样拒绝
	w = postRoutePreview(t, router, `{"channelKind":"messages","body":123}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400（body 非对象形态），实际=%d", w.Code)
	}
}

func TestRoutePreviewHandler_KillSwitchSkipsSchedulerPreview(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "active",
		KillSwitch:  true,
	}

	s, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	smartRouter := createTestSmartRouter(t, cfgManager)

	router := setupRoutePreviewTestRouter(t, smartRouter, s)
	w := postRoutePreview(t, router, `{"channelKind":"messages","model":"claude-sonnet-4","body":{"messages":[{"role":"user","content":"hi"}]}}`, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("kill switch 应保持 200，实际=%d body=%s", w.Code, w.Body.String())
	}
	var resp RoutePreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Message == "" {
		t.Fatal("kill switch 应携带明确 message")
	}
	if resp.Plan != nil {
		t.Fatal("kill switch 时不应计算路由计划")
	}
	if resp.SchedulerDiagnose == nil || resp.SchedulerDiagnose.Selected != nil {
		t.Fatalf("kill switch 时不应执行 scheduler 预演: %+v", resp.SchedulerDiagnose)
	}
	if resp.SchedulerDiagnose == nil || !strings.Contains(resp.SchedulerDiagnose.Reason, "已关闭") {
		t.Fatalf("schedulerDiagnose 应给出结构化跳过原因: %+v", resp.SchedulerDiagnose)
	}
}

func TestRoutePreviewHandler_SchedulerUnavailableKeepsPlanWithReason(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "active",
		KillSwitch:  false,
	}
	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	smartRouter := createTestSmartRouter(t, cfgManager)

	// scheduler 为 nil：plan 保留，schedDiag 给出结构化降级原因
	router := setupRoutePreviewTestRouter(t, smartRouter, nil)
	w := postRoutePreview(t, router, `{"channelKind":"messages","model":"claude-sonnet-4","body":{"messages":[{"role":"user","content":"hi"}]}}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际=%d body=%s", w.Code, w.Body.String())
	}
	var resp RoutePreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Plan == nil {
		t.Fatal("scheduler 不可用时 plan 应保留")
	}
	if resp.SchedulerDiagnose == nil || !strings.Contains(resp.SchedulerDiagnose.Reason, "scheduler 不可用") {
		t.Fatalf("schedulerDiagnose 应给出结构化降级原因: %+v", resp.SchedulerDiagnose)
	}
}

func TestRoutePreviewHandler_RequiresAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{RoutingMode: "active"}
	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	smartRouter := createTestSmartRouter(t, cfgManager)

	envCfg := config.NewEnvConfig()
	envCfg.AdminAccessKey = "test-admin-key"
	envCfg.EnableWebUI = true

	r := gin.New()
	r.Use(middleware.WebAuthMiddleware(envCfg, cfgManager))
	apiGroup := r.Group("/api")
	RegisterRoutePreviewRoutes(apiGroup, smartRouter, nil)

	payload := `{"channelKind":"messages","model":"claude-sonnet-4"}`
	post := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/autopilot/route-preview", bytes.NewReader([]byte(payload)))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("X-Api-Key", key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// 无管理凭证 → 401
	w := post("")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("无管理凭证期望 401，实际=%d", w.Code)
	}

	// 错误凭证 → 401
	w = post("wrong")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("错误凭证期望 401，实际=%d", w.Code)
	}

	// 正确凭证 → 200
	w = post("test-admin-key")
	if w.Code != http.StatusOK {
		t.Fatalf("正确凭证期望 200，实际=%d body=%s", w.Code, w.Body.String())
	}
}

// TestRoutePreview_DoesNotRecordTrace 验证 preview 不写 trace（R2）：
// 使用真实 TraceStore，调用前后记录数保持不变；
// 普通 BuildPlan（dry-run API 契约）仍按原样记录。
func TestRoutePreview_DoesNotRecordTrace(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{RoutingMode: "active"}
	s, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	traceStore, err := NewTraceStoreWithDB(nil)
	if err != nil {
		t.Fatalf("NewTraceStoreWithDB: %v", err)
	}
	smartRouter := &SmartRouter{
		configManager: cfgManager,
		traceStore:    traceStore,
	}

	router := setupRoutePreviewTestRouter(t, smartRouter, s)
	payload := `{"channelKind":"messages","model":"claude-sonnet-4","body":{"messages":[{"role":"user","content":"hello trace"}]}}`

	before := len(traceStore.ListRecent(100))
	for i := 0; i < 3; i++ {
		w := postRoutePreview(t, router, payload, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次调用失败: %d %s", i+1, w.Code, w.Body.String())
		}
	}
	after := len(traceStore.ListRecent(100))
	if after != before {
		t.Fatalf("Route Preview 不得产生 trace: before=%d after=%d", before, after)
	}

	// 对照：普通 BuildPlan（RecordTrace=true，dry-run API 原契约）仍记录
	profile := buildRoutePreviewProfile(nil, scheduler.ChannelKindMessages, "claude-sonnet-4", "completion",
		[]byte(`{"messages":[{"role":"user","content":"hello trace"}]}`), config.ScenarioRoutingConfig{})
	smartRouter.BuildPlan(&profile)
	if got := len(traceStore.ListRecent(100)); got != before+1 {
		t.Fatalf("普通 BuildPlan 应记录 trace: before=%d after=%d", before, got)
	}
}

// TestRoutePreview_LoadsGlobalScenario 验证 preview 画像加载全局场景配置（R1）：
// handler 从 SmartRouter 的 ConfigManager 读取 Scenario 并传入画像构建，
// 场景预设（ScenarioPreset/QualityTarget）与真实入口同源生效。
// ScenarioPreset 字段不序列化（json:"-"），此处直接对画像构建函数断言。
func TestRoutePreview_LoadsGlobalScenario(t *testing.T) {
	bodyBytes := []byte(`{"messages":[{"role":"user","content":"refactor this module"}]}`)

	// 零值场景配置（旧行为）：无预设
	profile := buildRoutePreviewProfile(nil, scheduler.ChannelKindMessages, "claude-sonnet-4", "completion",
		bodyBytes, config.ScenarioRoutingConfig{})
	if profile.ScenarioPreset != nil {
		t.Fatalf("零值场景配置时不应有 ScenarioPreset: %+v", profile.ScenarioPreset)
	}
	withoutPreset := profile.QualityNeed

	// 全局 daily_dev 场景（与真实路径 scenarioConfigProvider 同源的配置形态）
	profile = buildRoutePreviewProfile(nil, scheduler.ChannelKindMessages, "claude-sonnet-4", "completion",
		bodyBytes, config.ScenarioRoutingConfig{Mode: "daily_dev"})
	if profile.ScenarioPreset == nil {
		t.Fatal("全局场景配置生效时画像应携带 ScenarioPreset（R1：与真实入口同源）")
	}
	if profile.ScenarioPreset.Key != "daily_dev" {
		t.Fatalf("ScenarioPreset.Key = %q, want daily_dev", profile.ScenarioPreset.Key)
	}
	// QualityNeed 推导链（模型默认/场景/请求头合成）在 BuildRequestProfile
	// 内部，此处只验证预设接线生效；withoutPreset 仅供人工对照。
	_ = withoutPreset
}

// TestRoutePreview_PromptSignalsAlignedWithSharedAnalyzer 验证 preview 画像的
// 复杂度与领域提示来自共用分析器（R1 对拍）：
// buildRoutePreviewProfile 的 Complexity/DomainHints 与 AnalyzePromptSignals
// 直接调用结果一致，且非零值（此前 preview 完全缺失这两个字段）。
func TestRoutePreview_PromptSignalsAlignedWithSharedAnalyzer(t *testing.T) {
	body := `{"model":"claude-sonnet-4","system":"You are a senior Go engineer.","messages":[{"role":"user","content":"Refactor the parser.go module to handle nested diff --git patches and run gofmt.go"}],"tools":[{"name":"edit_file"}]}`
	bodyBytes := []byte(body)

	parsed := parseRoutePreviewBody(bodyBytes)
	analysis := AnalyzePromptSignals(parsed, "")

	profile := buildRoutePreviewProfile(nil, scheduler.ChannelKindMessages, "claude-sonnet-4", "completion",
		bodyBytes, config.ScenarioRoutingConfig{})

	if profile.Complexity != analysis.Complexity {
		t.Fatalf("Complexity 不一致: preview=%v shared=%v", profile.Complexity, analysis.Complexity)
	}
	if profile.Complexity == TaskComplexityUnknown {
		t.Fatal("带明确任务描述的请求不应是 unknown 复杂度（R1：此前 preview 缺失该字段）")
	}
	// DomainHints 在 BuildRequestProfile 内被消费为 TaskDomain，对拍推导结果
	sharedProfile := BuildRequestProfile(RequestProfileFeatures{
		Model:       "claude-sonnet-4",
		ChannelKind: "messages",
		Operation:   "completion",
		EstTokens:   profile.EstTokens,
		ContextNeed: profile.ContextNeed,
		Complexity:  analysis.Complexity,
		DomainHints: analysis.DomainHints,
		ToolUseNeed: profile.ToolUseNeed,
		VisionNeed:  profile.VisionNeed,
	})
	if profile.TaskDomain != sharedProfile.TaskDomain {
		t.Fatalf("TaskDomain 不一致: preview=%v shared=%v", profile.TaskDomain, sharedProfile.TaskDomain)
	}
	if len(analysis.DomainHints.ToolNames) == 0 {
		t.Fatal("带 tools 的请求应提取出工具名领域提示")
	}
}
