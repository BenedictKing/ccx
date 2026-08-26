package common

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// scenarioConfigProvider 返回全局场景配置快照。
// 由 main.go 注入（configManager 热重载安全）；nil 时仅请求头声明的场景可生效。
var scenarioConfigProvider func() config.ScenarioRoutingConfig

// SetScenarioConfigProvider 注入全局场景配置读取器（main.go 启动时调用一次）。
func SetScenarioConfigProvider(provider func() config.ScenarioRoutingConfig) {
	scenarioConfigProvider = provider
}

// AttachAutopilotRequestProfile 从已校验的请求体提取脱敏特征，并绑定到请求 context。
// channel 级 SmartRouter 与 endpoint policy 必须读取这一份画像，避免能力下界漂移。
func AttachAutopilotRequestProfile(
	c *gin.Context,
	kind scheduler.ChannelKind,
	model string,
	operation string,
	sessionID string,
	bodyBytes []byte,
	embeddingDimension int,
) autopilot.RequestProfile {
	agentRole, agentType := "", ""
	if agentCtx := AgentContextFromGin(c); agentCtx != nil {
		agentRole = agentCtx.AgentRole
		agentType = agentCtx.AgentType
	} else if c != nil {
		agentCtx := utils.ExtractAgentContext(c, bodyBytes)
		c.Set("agentContext", agentCtx)
		agentRole = agentCtx.AgentRole
		agentType = agentCtx.AgentType
	}

	hasImage := c != nil && HasImageContent(c, bodyBytes)
	hasDocument := c != nil && HasDocumentContent(c, bodyBytes)
	estTokens := estimateAutopilotInputTokens(kind, bodyBytes)
	req := decodeAutopilotRequest(bodyBytes)
	explicitDomain, routingScenarioHeader, costPreferenceHeader := "", "", ""
	if c != nil {
		explicitDomain = c.GetHeader("X-Task-Domain")
		routingScenarioHeader = c.GetHeader("X-Routing-Scenario")
		costPreferenceHeader = c.GetHeader("X-Cost-Preference")
	}
	var scenarioCfg config.ScenarioRoutingConfig
	if scenarioConfigProvider != nil {
		scenarioCfg = scenarioConfigProvider()
	}
	promptAnalysis := analyzeAutopilotPrompt(req, explicitDomain)
	clientEffortRaw, clientEffortExplicit := ExtractClientEffortExplicit(bodyBytes, string(kind))
	profile := autopilot.BuildRequestProfile(autopilot.RequestProfileFeatures{
		Model:                model,
		ChannelKind:          string(kind),
		Operation:            normalizeAutopilotOperation(kind, operation, c),
		AgentRole:            agentRole,
		AgentType:            agentType,
		HasImage:             hasImage,
		HasDocument:          hasDocument,
		EstTokens:            estTokens,
		Complexity:           promptAnalysis.Complexity,
		ContextNeed:          estTokens,
		VisionNeed:           hasImage,
		DocumentNeed:         hasDocument,
		ImageGenNeed:         kind == scheduler.ChannelKindImages,
		EmbeddingNeed:        kind == scheduler.ChannelKindVectors,
		ToolUseNeed:          autopilotRequestUsesTools(req),
		ReasoningNeed:        autopilotRequestNeedsReasoning(req),
		EmbeddingDimension:   embeddingDimension,
		SessionID:            sessionID,
		PromptHash:           hashAutopilotRequest(bodyBytes),
		DomainHints:          promptAnalysis.DomainHints,
		ClientEffortRaw:      clientEffortRaw,
		ClientEffortExplicit: clientEffortExplicit,
		RoutingScenarioHeader: routingScenarioHeader,
		CostPreferenceHeader:  costPreferenceHeader,
		ScenarioCfg:           scenarioCfg,
	})

	if c != nil && c.Request != nil {
		ctx := autopilot.ContextWithRequestProfile(c.Request.Context(), profile)
		c.Request = c.Request.WithContext(ctx)
	}
	return profile
}

// EnsureAutopilotRequestProfile 为未显式接线的多渠道入口提供保守兜底。
func EnsureAutopilotRequestProfile(
	c *gin.Context,
	kind scheduler.ChannelKind,
	model string,
	operation string,
	sessionID string,
	bodyBytes []byte,
) autopilot.RequestProfile {
	if c != nil && c.Request != nil {
		if profile, ok := autopilot.RequestProfileFromContext(c.Request.Context()); ok {
			return profile
		}
	}
	return AttachAutopilotRequestProfile(c, kind, model, operation, sessionID, bodyBytes, 0)
}

func estimateAutopilotInputTokens(kind scheduler.ChannelKind, bodyBytes []byte) int {
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

func normalizeAutopilotOperation(kind scheduler.ChannelKind, operation string, c *gin.Context) string {
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
		if strings.Contains(path, "/compact") {
			return "summarize"
		}
	}
	return "completion"
}

func autopilotRequestUsesTools(req map[string]interface{}) bool {
	return hasNonEmptyAutopilotFeature(req["tools"])
}

func autopilotRequestNeedsReasoning(req map[string]interface{}) bool {
	for _, key := range []string{"thinking", "reasoning", "reasoning_effort", "reasoningEffort", "enable_thinking"} {
		if hasNonEmptyAutopilotFeature(req[key]) {
			return true
		}
	}
	if generationConfig, ok := req["generationConfig"].(map[string]interface{}); ok {
		return hasNonEmptyAutopilotFeature(generationConfig["thinkingConfig"])
	}
	return false
}

func decodeAutopilotRequest(bodyBytes []byte) map[string]interface{} {
	var req map[string]interface{}
	if len(bodyBytes) == 0 || json.Unmarshal(bodyBytes, &req) != nil {
		return nil
	}
	return req
}

func hasNonEmptyAutopilotFeature(value interface{}) bool {
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
			return hasNonEmptyAutopilotFeature(rawType)
		}
		for _, nested := range v {
			if hasNonEmptyAutopilotFeature(nested) {
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

func hashAutopilotRequest(bodyBytes []byte) string {
	if len(bodyBytes) == 0 {
		return ""
	}
	sum := sha256.Sum256(bodyBytes)
	return hex.EncodeToString(sum[:8])
}

// extractClientEffort 从请求体提取客户端显式声明的思考等级。
// 复用 reasoning_log.go 的多协议覆盖路径逻辑，但增加 explicit 标志：
//   - "explicit none"（客户端发送了 thinking.type=disabled 或 reasoning_effort=none）→ raw="none", explicit=true
//   - "not declared"（请求中无任何 effort 字段）→ raw="", explicit=false
