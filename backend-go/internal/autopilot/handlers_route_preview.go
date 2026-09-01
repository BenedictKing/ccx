package autopilot

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ── Route Preview API（设计 §5：对标 OmniRoute 路由预演）──

// RegisterRoutePreviewRoutes 注册路由预演 API 到给定路由组。
// 挂载到 /api group 后为 /api/autopilot/route-preview。
func RegisterRoutePreviewRoutes(router gin.IRouter, smartRouter *SmartRouter, sch *scheduler.ChannelScheduler) {
	handler := handleRoutePreview(smartRouter, sch)
	router.POST("/autopilot/route-preview", handler)
}

// RoutePreviewRequest 路由预演请求体。
type RoutePreviewRequest struct {
	// ChannelKind 入站协议类型：messages | chat | responses | gemini | images | vectors。
	ChannelKind string `json:"channelKind" binding:"required"`
	// Model 请求的目标模型名；为空时尝试从 body 中解析。
	Model string `json:"model"`
	// Operation 操作类型：completion | count_tokens | image_generation | embedding 等；
	// 为空时按 channelKind 推导默认值。
	Operation string `json:"operation"`
	// Body 原始请求体（任意协议格式，自动识别特征）。
	// 仅内存态用于特征提取，不落 trace、不写日志。
	Body json.RawMessage `json:"body"`
}

// RoutePreviewSchedulerDiagnose 预演响应中的 scheduler 层诊断结果。
// 结构与 /api/{kind}/channels/scheduler/diagnose 对齐。
type RoutePreviewSchedulerDiagnose struct {
	OK       bool                      `json:"ok"`
	Kind     scheduler.ChannelKind     `json:"kind"`
	Reason   string                    `json:"reason,omitempty"`
	Summary  string                    `json:"summary,omitempty"`
	Trace    *scheduler.SelectionTrace `json:"trace,omitempty"`
	Selected *RoutePreviewSelectedInfo `json:"selected,omitempty"`
}

// RoutePreviewSelectedInfo scheduler 层最终选中的渠道信息。
type RoutePreviewSelectedInfo struct {
	ChannelIndex int    `json:"channelIndex"`
	ChannelName  string `json:"channelName"`
	ServiceType  string `json:"serviceType"`
}

// RoutePreviewResponse 路由预演响应体。
type RoutePreviewResponse struct {
	// Plan SmartRouter 层路由计划（候选评分 + 硬约束过滤结果）。
	Plan *RoutingPlan `json:"plan"`
	// Mode 当前生效路由模式。
	Mode string `json:"mode"`
	// ExtractedProfile 从请求体自动提取出的特征（便于用户核对）。
	ExtractedProfile *RequestProfile `json:"extractedProfile"`
	// SchedulerDiagnose 底层 scheduler 层选择过程（逐阶段淘汰原因）。
	SchedulerDiagnose *RoutePreviewSchedulerDiagnose `json:"schedulerDiagnose,omitempty"`
	// Message 提示信息（如 SmartRouter 未初始化等）。
	Message string `json:"message,omitempty"`
}

// handleRoutePreview POST /api/autopilot/route-preview。
// 接受原始请求体 + 入站协议，自动提取特征后进 SmartRouter + scheduler 两层预演，
// 返回 RoutingPlan + 逐阶段淘汰原因。零上游请求，请求体仅内存态。
func handleRoutePreview(smartRouter *SmartRouter, sch *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RoutePreviewRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误: " + err.Error()})
			return
		}

		kind := scheduler.ChannelKind(strings.TrimSpace(req.ChannelKind))
		bodyBytes := []byte(req.Body)

		// 从 body 中解析 model 作为兜底（请求显式 model 优先）
		model := strings.TrimSpace(req.Model)
		if model == "" {
			model = extractModelFromBody(bodyBytes)
		}

		// 推导 operation
		operation := normalizeRoutePreviewOperation(kind, strings.TrimSpace(req.Operation), c)

		// 从原始请求体自动提取特征 → RequestProfile
		profile := buildRoutePreviewProfile(c, kind, model, operation, bodyBytes)

		// ── SmartRouter 层预演 ──
		var plan *RoutingPlan
		var mode string
		var message string

		if smartRouter == nil {
			message = "SmartRouter 未初始化"
		} else {
			cfg := smartRouter.ConfigManager().GetConfig()
			autopilotCfg := cfg.AutopilotRouting
			mode = string(autopilotCfg.EffectiveRoutingMode())

			if autopilotCfg.KillSwitch {
				message = "智能路由已关闭（mode=off 或 kill switch 已启用）"
			} else {
				plan = smartRouter.BuildPlan(&profile)
			}
		}

		// ── Scheduler 层预演（DryRun=true，零上游请求、不更新状态）──
		var schedDiag *RoutePreviewSchedulerDiagnose
		if sch != nil {
			selectionCtx := ContextWithRequestProfile(c.Request.Context(), profile)
			result, err := sch.SelectChannelWithOptions(selectionCtx, scheduler.SelectionOptions{
				Kind:             kind,
				Model:            model,
				HasImageContent:  profile.VisionNeed,
				AgentRole:        profile.AgentRole,
				ContextRequirement: &scheduler.ContextRequirement{
					InputTokens: profile.ContextNeed,
				},
				DryRun: true,
			})

			schedDiag = &RoutePreviewSchedulerDiagnose{
				Kind: kind,
			}
			if err != nil {
				schedDiag.OK = false
				schedDiag.Reason = err.Error()
				if trace, ok := scheduler.SelectionTraceFromError(err); ok {
					schedDiag.Trace = trace
					schedDiag.Summary = scheduler.FormatSelectionTraceSummary(trace, 8)
				}
			} else {
				schedDiag.OK = true
				schedDiag.Reason = result.Reason
				schedDiag.Trace = result.Trace
				schedDiag.Summary = scheduler.FormatSelectionTraceSummary(result.Trace, 8)
				if result.Upstream != nil {
					schedDiag.Selected = &RoutePreviewSelectedInfo{
						ChannelIndex: result.ChannelIndex,
						ChannelName:  result.Upstream.Name,
						ServiceType:  result.Upstream.ServiceType,
					}
				}
			}
		}

		// 把 profile 的 Model 固定为最终使用的值，便于前端展示
		profile.Model = model

		c.JSON(http.StatusOK, RoutePreviewResponse{
			Plan:              plan,
			Mode:              mode,
			ExtractedProfile:  &profile,
			SchedulerDiagnose: schedDiag,
			Message:           message,
		})
	}
}

// buildRoutePreviewProfile 从原始请求体 + 元数据构造 RequestProfile。
// 特征提取口径与 handlers/common/AttachAutopilotRequestProfile 对齐，
// 但因包依赖关系（autopilot 不能 import handlers/common）在此独立实现。
func buildRoutePreviewProfile(
	c *gin.Context,
	kind scheduler.ChannelKind,
	model string,
	operation string,
	bodyBytes []byte,
) RequestProfile {
	agentCtx := utils.ExtractAgentContext(c, bodyBytes)
	agentRole := agentCtx.AgentRole
	agentType := agentCtx.AgentType

	hasImage := detectImageInBody(bodyBytes)
	hasDocument := detectDocumentInRoutePreviewBody(bodyBytes)
	estTokens := estimateRoutePreviewTokens(kind, bodyBytes)

	parsedBody := parseRoutePreviewBody(bodyBytes)
	toolUseNeed := routePreviewUsesTools(parsedBody)
	reasoningNeed := routePreviewNeedsReasoning(parsedBody)
	severityClass := SeverityClassRequestShape(bodyBytes)

	var scenarioCfg config.ScenarioRoutingConfig
	routingScenarioHeader := ""
	costPreferenceHeader := ""
	if c != nil {
		routingScenarioHeader = c.GetHeader("X-Routing-Scenario")
		costPreferenceHeader = c.GetHeader("X-Cost-Preference")
		// 注意：这里不读全局 ScenarioCfg，因为需要 ConfigManager；
		// BuildRequestProfile 内部 ScenarioCfg 零值时仅请求头声明的场景可生效。
		// 这与真实路径有细微差别（真实路径有全局场景配置），但预演端点
		// 作为管理工具主要用于特征核对与预演，影响有限。
		// SmartRouter.BuildPlan 会用它自己的 ConfigManager 重新计算
		// 场景解析（见 BuildPlan 内的 ResolveScenarioPreset），所以这里
		// 的 ScenarioCfg 不影响最终 BuildPlan 结果。
	}

	return BuildRequestProfile(RequestProfileFeatures{
		Model:                 model,
		ChannelKind:           string(kind),
		Operation:             operation,
		AgentRole:             agentRole,
		AgentType:             agentType,
		HasImage:              hasImage,
		HasDocument:           hasDocument,
		EstTokens:             estTokens,
		ContextNeed:           estTokens,
		VisionNeed:            hasImage,
		DocumentNeed:          hasDocument,
		ImageGenNeed:          kind == scheduler.ChannelKindImages,
		EmbeddingNeed:         kind == scheduler.ChannelKindVectors,
		ToolUseNeed:           toolUseNeed,
		ReasoningNeed:         reasoningNeed,
		SeverityClassNeed:     severityClass,
		ClientEffortRaw:       extractRoutePreviewClientEffort(bodyBytes, string(kind)),
		ClientEffortExplicit:  routePreviewHasExplicitEffort(bodyBytes, string(kind)),
		RoutingScenarioHeader: routingScenarioHeader,
		CostPreferenceHeader:  costPreferenceHeader,
		ScenarioCfg:           scenarioCfg,
	})
}

// ── 辅助函数：特征提取 ──

func extractModelFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return strings.TrimSpace(gjson.GetBytes(body, "model").String())
}

func parseRoutePreviewBody(body []byte) map[string]interface{} {
	var req map[string]interface{}
	if len(body) == 0 || json.Unmarshal(body, &req) != nil {
		return nil
	}
	return req
}

func routePreviewUsesTools(req map[string]interface{}) bool {
	tools := req["tools"]
	switch v := tools.(type) {
	case nil:
		return false
	case []interface{}:
		return len(v) > 0
	default:
		return false
	}
}

func routePreviewNeedsReasoning(req map[string]interface{}) bool {
	for _, key := range []string{"thinking", "reasoning", "reasoning_effort", "reasoningEffort", "enable_thinking"} {
		if hasNonEmptyRoutePreviewFeature(req[key]) {
			return true
		}
	}
	if generationConfig, ok := req["generationConfig"].(map[string]interface{}); ok {
		return hasNonEmptyRoutePreviewFeature(generationConfig["thinkingConfig"])
	}
	return false
}

func hasNonEmptyRoutePreviewFeature(value interface{}) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		normalized := strings.ToLower(strings.TrimSpace(v))
		return normalized != "" && normalized != "none" && normalized != "off" && normalized != "disabled" && normalized != "false"
	case []interface{}:
		return len(v) > 0
	case map[string]interface{}:
		if rawType, ok := v["type"]; ok {
			return hasNonEmptyRoutePreviewFeature(rawType)
		}
		for _, nested := range v {
			if hasNonEmptyRoutePreviewFeature(nested) {
				return true
			}
		}
		return false
	case float64:
		return v > 0
	default:
		return true
	}
}

func normalizeRoutePreviewOperation(kind scheduler.ChannelKind, operation string, c *gin.Context) string {
	op := strings.ToLower(strings.TrimSpace(operation))
	switch op {
	case "generations", "generation", "image_generation":
		return "image_generation"
	case "edits", "edit", "image_edit":
		return "image_edit"
	case "variations", "variation", "image_variation":
		return "image_variation"
	case "compact", "compaction", "summarize":
		return "summarize"
	case "count_tokens", "title_generation", "classification", "format_conversion", "translation", "completion", "embedding":
		return op
	}

	switch kind {
	case scheduler.ChannelKindImages:
		return "image_generation"
	case scheduler.ChannelKindVectors:
		return "embedding"
	}
	if c != nil && c.Request != nil && c.Request.URL != nil {
		path := strings.ToLower(c.Request.URL.Path)
		if strings.Contains(path, "count_tokens") {
			return "count_tokens"
		}
	}
	return "completion"
}

func estimateRoutePreviewTokens(kind scheduler.ChannelKind, bodyBytes []byte) int {
	switch kind {
	case scheduler.ChannelKindResponses:
		return utils.EstimateResponsesRequestTokens(bodyBytes)
	case scheduler.ChannelKindGemini:
		return utils.EstimateGeminiRequestTokens(bodyBytes)
	case scheduler.ChannelKindMessages, scheduler.ChannelKindChat:
		return utils.EstimateRequestTokens(bodyBytes)
	default:
		return 0
	}
}

func extractRoutePreviewClientEffort(bodyBytes []byte, channelKind string) string {
	raw, _ := extractClientEffortFromBody(bodyBytes, channelKind)
	return raw
}

func routePreviewHasExplicitEffort(bodyBytes []byte, channelKind string) bool {
	_, explicit := extractClientEffortFromBody(bodyBytes, channelKind)
	return explicit
}

// detectImageInBody 检测请求体是否包含图片内容。
// 与 handlers/common/vision_detect.go 的 detectImageInBody 逻辑一致，
// 因包依赖关系在此独立实现。
func detectImageInBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	hasImageBlock := func(block gjson.Result) bool {
		return block.Get("type").String() == "image" ||
			block.Get("type").String() == "image_url" ||
			block.Get("type").String() == "input_image"
	}

	var hasImageInContent func(gjson.Result) bool
	hasImageInContent = func(content gjson.Result) bool {
		if !content.IsArray() {
			return false
		}
		for _, block := range content.Array() {
			if hasImageBlock(block) {
				return true
			}
			if hasImageInContent(block.Get("content")) {
				return true
			}
		}
		return false
	}

	// Claude Messages / OpenAI Chat: messages[*].content[*]
	messages := gjson.GetBytes(body, "messages")
	if messages.Exists() && messages.IsArray() {
		for _, msg := range messages.Array() {
			if hasImageInContent(msg.Get("content")) {
				return true
			}
		}
	}

	// Responses API: input[*]
	input := gjson.GetBytes(body, "input")
	if input.Exists() && input.IsArray() {
		for _, item := range input.Array() {
			if hasImageBlock(item) || hasImageInContent(item.Get("content")) {
				return true
			}
		}
	}

	// Gemini: contents[*].parts[*].inlineData / fileData
	contents := gjson.GetBytes(body, "contents")
	if contents.Exists() && contents.IsArray() {
		for _, c := range contents.Array() {
			parts := c.Get("parts")
			if parts.IsArray() {
				for _, part := range parts.Array() {
					if part.Get("inlineData").Exists() || part.Get("fileData").Exists() {
						return true
					}
				}
			}
		}
	}

	return false
}

// detectDocumentInRoutePreviewBody 检测请求体是否包含文档附件。
// 与 handlers/common/document_detect.go 的 detectDocumentInBody 逻辑一致。
func detectDocumentInRoutePreviewBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	hasDocumentBlock := func(block gjson.Result) bool {
		return block.Get("type").String() == "document" ||
			block.Get("type").String() == "input_file"
	}

	var hasDocumentInContent func(gjson.Result) bool
	hasDocumentInContent = func(content gjson.Result) bool {
		if !content.IsArray() {
			return false
		}
		for _, block := range content.Array() {
			if hasDocumentBlock(block) {
				return true
			}
			if hasDocumentInContent(block.Get("content")) {
				return true
			}
		}
		return false
	}

	// Claude Messages / OpenAI Chat: messages[*].content[*]
	messages := gjson.GetBytes(body, "messages")
	if messages.Exists() && messages.IsArray() {
		for _, msg := range messages.Array() {
			if hasDocumentInContent(msg.Get("content")) {
				return true
			}
		}
	}

	// Responses API: input[*]
	input := gjson.GetBytes(body, "input")
	if input.Exists() && input.IsArray() {
		for _, item := range input.Array() {
			if hasDocumentBlock(item) || hasDocumentInContent(item.Get("content")) {
				return true
			}
		}
	}

	// Gemini: contents[*].parts[*]，mimeType 非图非音
	contents := gjson.GetBytes(body, "contents")
	if contents.Exists() && contents.IsArray() {
		for _, c := range contents.Array() {
			parts := c.Get("parts")
			if parts.IsArray() {
				for _, part := range parts.Array() {
					mime := part.Get("inlineData").Get("mimeType").String()
					if mime == "" {
						mime = part.Get("fileData").Get("mimeType").String()
					}
					if mime != "" && !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "audio/") {
						return true
					}
				}
			}
		}
	}

	return false
}

// extractClientEffortFromBody 提取客户端显式声明的思考等级。
// 与 handlers/common/autopilot_request_profile.go 的 ExtractClientEffortExplicit 对齐。
func extractClientEffortFromBody(bodyBytes []byte, channelKind string) (raw string, explicit bool) {
	if len(bodyBytes) == 0 {
		return "", false
	}

	var req map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return "", false
	}

	// Claude / Codex / OpenAI 通用字段
	for _, key := range []string{"thinking", "reasoning", "reasoningEffort", "reasoning_effort"} {
		if val, ok := req[key]; ok {
			switch v := val.(type) {
			case string:
				return v, true
			case map[string]interface{}:
				if t, ok := v["type"].(string); ok {
					return t, true
				}
			}
		}
	}

	// Gemini generationConfig.thinkingConfig
	if genConfig, ok := req["generationConfig"].(map[string]interface{}); ok {
		if thinkingConfig, ok := genConfig["thinkingConfig"].(map[string]interface{}); ok {
			if include, ok := thinkingConfig["thinkingInclude"].(string); ok {
				return include, true
			}
		}
	}

	return "", false
}
