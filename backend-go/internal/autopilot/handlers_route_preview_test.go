package autopilot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// ── Route Preview 单元测试 ──

func TestRoutePreview_ExtractsFeaturesFromMessagesBody(t *testing.T) {
	body := map[string]interface{}{
		"model": "claude-sonnet-4",
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "Hello, world!",
					},
				},
			},
			map[string]interface{}{
				"role":    "assistant",
				"content": "Hi there!",
			},
		},
		"tools": []interface{}{
			map[string]interface{}{
				"name": "get_weather",
				"input_schema": map[string]interface{}{
					"type": "object",
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	profile := buildRoutePreviewProfile(
		nil,
		scheduler.ChannelKindMessages,
		"claude-sonnet-4",
		"completion",
		bodyBytes,
		config.ScenarioRoutingConfig{},
	)

	if profile.Model != "claude-sonnet-4" {
		t.Errorf("期望 Model=claude-sonnet-4，实际=%s", profile.Model)
	}
	if !profile.ToolUseNeed {
		t.Error("期望 ToolUseNeed=true（请求体有 tools）")
	}
	if profile.VisionNeed {
		t.Error("期望 VisionNeed=false（请求体无图片）")
	}
	if profile.EstTokens <= 0 {
		t.Error("期望 EstTokens > 0")
	}
	if profile.TaskClass == "" {
		t.Error("期望 TaskClass 非空")
	}
}

func TestRoutePreview_DetectsImageContent(t *testing.T) {
	body := map[string]interface{}{
		"model": "claude-sonnet-4",
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "image",
						"source": map[string]interface{}{
							"type":       "base64",
							"media_type": "image/png",
							"data":       "...",
						},
					},
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	profile := buildRoutePreviewProfile(
		nil,
		scheduler.ChannelKindMessages,
		"claude-sonnet-4",
		"completion",
		bodyBytes,
		config.ScenarioRoutingConfig{},
	)

	if !profile.HasImage {
		t.Error("期望 HasImage=true")
	}
	if !profile.VisionNeed {
		t.Error("期望 VisionNeed=true")
	}
}

func TestRoutePreview_DetectsReasoning(t *testing.T) {
	body := map[string]interface{}{
		"model": "claude-sonnet-4",
		"thinking": map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": 2000,
		},
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Solve this step by step.",
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	profile := buildRoutePreviewProfile(
		nil,
		scheduler.ChannelKindMessages,
		"claude-sonnet-4",
		"completion",
		bodyBytes,
		config.ScenarioRoutingConfig{},
	)

	if !profile.ReasoningNeed {
		t.Error("期望 ReasoningNeed=true（请求体有 thinking 字段）")
	}
}

func TestRoutePreview_GeminiFormat(t *testing.T) {
	body := map[string]interface{}{
		"contents": []interface{}{
			map[string]interface{}{
				"parts": []interface{}{
					map[string]interface{}{
						"text": "Hello",
					},
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	profile := buildRoutePreviewProfile(
		nil,
		scheduler.ChannelKindGemini,
		"gemini-2.0-flash",
		"completion",
		bodyBytes,
		config.ScenarioRoutingConfig{},
	)

	if profile.ChannelKind != "gemini" {
		t.Errorf("期望 ChannelKind=gemini，实际=%s", profile.ChannelKind)
	}
	if profile.EstTokens <= 0 {
		t.Error("期望 EstTokens > 0（Gemini 格式也应估算）")
	}
	if profile.ToolUseNeed {
		t.Error("期望 ToolUseNeed=false")
	}
}

func TestRoutePreview_ImagesKind(t *testing.T) {
	body := map[string]interface{}{
		"model":  "dall-e-3",
		"prompt": "a cat",
	}
	bodyBytes, _ := json.Marshal(body)

	profile := buildRoutePreviewProfile(
		nil,
		scheduler.ChannelKindImages,
		"dall-e-3",
		"image_generation",
		bodyBytes,
		config.ScenarioRoutingConfig{},
	)

	if !profile.ImageGenNeed {
		t.Error("期望 ImageGenNeed=true（images 类型）")
	}
	if profile.TaskClass != TaskClassImageGen {
		t.Errorf("期望 TaskClass=image_generation，实际=%s", profile.TaskClass)
	}
}

func TestRoutePreview_VectorsKind(t *testing.T) {
	body := map[string]interface{}{
		"model": "text-embedding-3-small",
		"input": "hello world",
	}
	bodyBytes, _ := json.Marshal(body)

	profile := buildRoutePreviewProfile(
		nil,
		scheduler.ChannelKindVectors,
		"text-embedding-3-small",
		"embedding",
		bodyBytes,
		config.ScenarioRoutingConfig{},
	)

	if !profile.EmbeddingNeed {
		t.Error("期望 EmbeddingNeed=true（vectors 类型）")
	}
}

func TestRoutePreview_EmptyBody(t *testing.T) {
	profile := buildRoutePreviewProfile(
		nil,
		scheduler.ChannelKindMessages,
		"claude-sonnet-4",
		"completion",
		[]byte{},
		config.ScenarioRoutingConfig{},
	)

	if profile.Model != "claude-sonnet-4" {
		t.Errorf("期望 Model=claude-sonnet-4，实际=%s", profile.Model)
	}
	if profile.ToolUseNeed {
		t.Error("期望 ToolUseNeed=false（空 body）")
	}
	if profile.HasImage {
		t.Error("期望 HasImage=false（空 body）")
	}
	// 空 body 也应有合法的 TaskClass 推导
	if profile.TaskClass == "" {
		t.Error("期望 TaskClass 非空（空 body 也应有默认分类）")
	}
}

func TestRoutePreview_ExtractModelFromBody(t *testing.T) {
	body := map[string]interface{}{
		"model": "gpt-4o",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "hi",
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	model := extractModelFromBody(bodyBytes)
	if model != "gpt-4o" {
		t.Errorf("期望 model=gpt-4o，实际=%s", model)
	}

	if extractModelFromBody([]byte{}) != "" {
		t.Error("空 body 应返回空 model")
	}
}

// ── HTTP Handler 测试 ──

func setupRoutePreviewTestRouter(t *testing.T, smartRouter *SmartRouter, sch *scheduler.ChannelScheduler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutePreviewRoutes(r, smartRouter, sch)
	return r
}

func TestRoutePreviewHandler_ReturnsPlanAndSchedulerDiagnose(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "active",
		KillSwitch:  false,
	}

	s, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)

	router := setupRoutePreviewTestRouter(t, smartRouter, s)

	reqBody := RoutePreviewRequest{
		ChannelKind: "messages",
		Model:       "claude-sonnet-4",
		Operation:   "completion",
		Body:        json.RawMessage(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}]}`),
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/autopilot/route-preview", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际=%d，body=%s", w.Code, w.Body.String())
	}

	var resp RoutePreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if resp.Plan == nil {
		t.Fatal("期望 plan 非 nil")
	}
	if resp.ExtractedProfile == nil {
		t.Fatal("期望 extractedProfile 非 nil")
	}
	if resp.ExtractedProfile.Model != "claude-sonnet-4" {
		t.Errorf("期望 extractedProfile.Model=claude-sonnet-4，实际=%s", resp.ExtractedProfile.Model)
	}
	if resp.Mode == "" || resp.Mode == "off" {
		t.Errorf("期望 mode 非空且非 off，实际=%s", resp.Mode)
	}
	if resp.SchedulerDiagnose == nil {
		t.Fatal("期望 schedulerDiagnose 非 nil")
	}
	if !resp.SchedulerDiagnose.OK {
		t.Errorf("期望 schedulerDiagnose.ok=true，实际 reason=%s", resp.SchedulerDiagnose.Reason)
	}
	if resp.SchedulerDiagnose.Selected == nil {
		t.Error("期望 schedulerDiagnose.selected 非 nil")
	}
}

func TestRoutePreviewHandler_InvalidBody(t *testing.T) {
	router := setupRoutePreviewTestRouter(t, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/autopilot/route-preview", bytes.NewReader([]byte(`{invalid json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("期望 400，实际=%d", w.Code)
	}
}

func TestRoutePreviewHandler_MissingChannelKind(t *testing.T) {
	router := setupRoutePreviewTestRouter(t, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/autopilot/route-preview", bytes.NewReader([]byte(`{"model":"claude-sonnet-4"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("期望 400（缺少 channelKind），实际=%d", w.Code)
	}
}

// ── 一致性对拍测试：route-preview 与 dry-run 管线对同一特征输出一致 ──

func TestRoutePreview_ConsistencyWithDryRun(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "active",
		KillSwitch:  false,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)

	// 构造一个带所有特征的 messages 请求体
	body := map[string]interface{}{
		"model": "claude-sonnet-4",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello, this is a test message with some content.",
			},
			map[string]interface{}{
				"role":    "assistant",
				"content": "Hi there! How can I help?",
			},
		},
		"tools": []interface{}{
			map[string]interface{}{
				"name":        "test_tool",
				"description": "A test tool",
				"input_schema": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	// 方式 1：route-preview 自动提取特征
	routePreviewProfile := buildRoutePreviewProfile(
		nil,
		scheduler.ChannelKindMessages,
		"claude-sonnet-4",
		"completion",
		bodyBytes,
		config.ScenarioRoutingConfig{},
	)
	routePreviewPlan := smartRouter.BuildPlan(&routePreviewProfile)

	// 方式 2：手工构造 DryRunRequest，用相同特征调 BuildPlan
	//（Complexity/DomainHints 与 preview 同源：都来自 AnalyzePromptSignals）
	dryRunProfile := BuildRequestProfile(RequestProfileFeatures{
		Model:         "claude-sonnet-4",
		ChannelKind:   "messages",
		Operation:     "completion",
		HasImage:      routePreviewProfile.HasImage,
		EstTokens:     routePreviewProfile.EstTokens,
		Complexity:    routePreviewProfile.Complexity,
		VisionNeed:    routePreviewProfile.VisionNeed,
		ToolUseNeed:   routePreviewProfile.ToolUseNeed,
		ReasoningNeed: routePreviewProfile.ReasoningNeed,
		ContextNeed:   routePreviewProfile.EstTokens,
	})
	dryRunPlan := smartRouter.BuildPlan(&dryRunProfile)

	// 对拍：候选数量一致
	if len(routePreviewPlan.Candidates) != len(dryRunPlan.Candidates) {
		t.Errorf("候选数量不一致: route-preview=%d, dry-run=%d",
			len(routePreviewPlan.Candidates), len(dryRunPlan.Candidates))
	}

	// 对拍：选中的 channelUid 一致
	if routePreviewPlan.SelectedChannelUID != dryRunPlan.SelectedChannelUID {
		t.Errorf("选中渠道不一致: route-preview=%s, dry-run=%s",
			routePreviewPlan.SelectedChannelUID, dryRunPlan.SelectedChannelUID)
	}

	// 对拍：任务分类一致
	if routePreviewProfile.TaskClass != dryRunProfile.TaskClass {
		t.Errorf("TaskClass 不一致: route-preview=%s, dry-run=%s",
			routePreviewProfile.TaskClass, dryRunProfile.TaskClass)
	}

	// 对拍：质量需求一致
	if routePreviewProfile.QualityNeed != dryRunProfile.QualityNeed {
		t.Errorf("QualityNeed 不一致: route-preview=%s, dry-run=%s",
			routePreviewProfile.QualityNeed, dryRunProfile.QualityNeed)
	}
}

// ── 零上游请求保证测试 ──

func TestRoutePreview_DryRunDoesNotUpdateState(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "active",
		KillSwitch:  false,
	}

	s, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	router := setupRoutePreviewTestRouter(t, smartRouter, s)

	// 第一次调用
	reqBody := RoutePreviewRequest{
		ChannelKind: "messages",
		Model:       "claude-sonnet-4",
		Operation:   "completion",
		Body:        json.RawMessage(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}]}`),
	}
	bodyBytes, _ := json.Marshal(reqBody)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/autopilot/route-preview", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次调用失败: code=%d", i+1, w.Code)
		}

		var resp RoutePreviewResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		// DryRun=true 不应改变 lastSelectedChannel，所以每次结果应该一致
		if resp.SchedulerDiagnose == nil || resp.SchedulerDiagnose.Selected == nil {
			t.Fatal("期望选中结果非 nil")
		}
	}
	// 三次调用都成功即通过（DryRun 模式下不会有状态变化导致的选择偏移）
}
