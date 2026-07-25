package autopilot

import "strings"

// ModelRoutingIntent 描述下游请求是否允许由其他模型承接。
// 未明确列入自适应入口的请求一律保持精确模型语义。
type ModelRoutingIntent string

const (
	ModelRoutingIntentExactOnly         ModelRoutingIntent = "exact_only"
	ModelRoutingIntentClaudeAdaptive    ModelRoutingIntent = "claude_adaptive"
	ModelRoutingIntentResponsesAdaptive ModelRoutingIntent = "responses_adaptive"
)

// AllowsSubstitution 表示该请求允许在满足能力下界后跨模型替代。
func (i ModelRoutingIntent) AllowsSubstitution() bool {
	return i == ModelRoutingIntentClaudeAdaptive || i == ModelRoutingIntentResponsesAdaptive
}

// ClassifyModelRoutingIntent 根据协议和下游模型识别路由意图。
// 安全默认值是 exact-only，避免显式请求第三方模型时发生跨模型族重定向。
func ClassifyModelRoutingIntent(channelKind, requestModel string) ModelRoutingIntent {
	kind := strings.ToLower(strings.TrimSpace(channelKind))
	model := normalizeRoutingModelID(requestModel)

	switch kind {
	case "messages":
		if isClaudeAdaptiveRequest(model) {
			return ModelRoutingIntentClaudeAdaptive
		}
	case "responses":
		if isResponsesAdaptiveRequest(model) {
			return ModelRoutingIntentResponsesAdaptive
		}
	}

	return ModelRoutingIntentExactOnly
}

func normalizeRoutingModelID(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func isClaudeAdaptiveRequest(model string) bool {
	switch model {
	case "fable", "opus", "sonnet", "haiku":
		return true
	}
	if !strings.HasPrefix(model, "claude-") {
		return false
	}
	for _, segment := range strings.Split(model, "-") {
		switch segment {
		case "fable", "opus", "sonnet", "haiku":
			return true
		}
	}
	return false
}

func isResponsesAdaptiveRequest(model string) bool {
	return model == "codex-auto-review" ||
		model == "gpt-5.5" ||
		matchesModelSeries(model, "gpt-5.4") ||
		matchesModelSeries(model, "gpt-5.6")
}

func matchesModelSeries(model, series string) bool {
	return model == series || strings.HasPrefix(model, series+"-")
}

func findExactModelProfile(profiles []ModelProfile, requestModel string) (ModelProfile, bool) {
	normalized := normalizeRoutingModelID(requestModel)
	for _, profile := range profiles {
		if normalizeRoutingModelID(profile.ModelID) == normalized {
			return profile, true
		}
	}
	return ModelProfile{}, false
}

// findEquivalentModelProfile 只接受供应商文档明确声明的兼容模型别名。
// 它仍属于 exact-only 语义，不允许扩展为同模型族内的任意替代。
func findEquivalentModelProfile(profiles []ModelProfile, requestModel string) (ModelProfile, bool) {
	normalized := normalizeRoutingModelID(requestModel)
	canonical := canonicalCompatibilityModelID(normalized)
	for _, profile := range profiles {
		candidate := normalizeRoutingModelID(profile.ModelID)
		if candidate == normalized {
			continue
		}
		if canonicalCompatibilityModelID(candidate) == canonical {
			return profile, true
		}
	}
	return ModelProfile{}, false
}

// canonicalCompatibilityModelID 收敛供应商官方文档中的兼容别名。
// 未列出的模型保持原 ID，确保 exact-only 的安全默认值不变。
func canonicalCompatibilityModelID(model string) string {
	normalized := normalizeRoutingModelID(model)
	if strings.HasPrefix(normalized, "claude-opus-4.8") {
		return strings.Replace(normalized, "claude-opus-4.8", "claude-opus-4-8", 1)
	}
	switch normalized {
	case "deepseek-chat":
		return "deepseek-v4-flash"
	case "deepseek-reasoner":
		return "deepseek-v4-pro"
	default:
		return normalized
	}
}
