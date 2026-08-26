package autopilot

import (
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
)

// RequestProfileFeatures 是协议层提取出的脱敏请求特征。
// 这里只接收结构化元数据，不接收或持久化消息正文。
type RequestProfileFeatures struct {
	Model                string
	ChannelKind          string
	Operation            string
	AgentRole            string
	AgentType            string
	HasImage             bool
	HasDocument          bool
	EstTokens            int
	Complexity           TaskComplexity
	ContextNeed          int
	VisionNeed           bool
	DocumentNeed         bool
	ImageGenNeed         bool
	EmbeddingNeed        bool
	ToolUseNeed          bool
	ReasoningNeed        bool
	EmbeddingDimension   int
	SessionID            string
	PromptHash           string
	DomainHints          DomainHints
	ClientEffortRaw      string // 客户端显式声明的 effort 原始值（归一化前）
	ClientEffortExplicit bool   // 客户端是否显式设置了思考等级（区分"显式无"和"未声明"）

	// RoutingScenarioHeader 请求头 X-Routing-Scenario 原始值（空 = 未声明）。
	RoutingScenarioHeader string
	// CostPreferenceHeader 请求头 X-Cost-Preference 原始值（空 = 未声明）。
	CostPreferenceHeader string
	// ScenarioCfg 全局场景配置快照（由 handler 层从 ConfigManager 读取传入；
	// 零值时仅请求头声明的场景可生效）。
	ScenarioCfg config.ScenarioRoutingConfig
}

// BuildRequestProfile 将协议无关特征收敛为 SmartRouter 使用的请求画像。
// 未知字段保持保守零值；图片请求始终要求 vision，实际上下文需求默认取输入估算。
func BuildRequestProfile(features RequestProfileFeatures) RequestProfile {
	contextNeed := features.ContextNeed
	if contextNeed <= 0 {
		contextNeed = features.EstTokens
	}

	qualityNeed := QualityTierLow
	if features.Model != "" {
		family := InferModelFamily(features.Model, "")
		qualityNeed = ModelProfileQualityTier(features.Model, family)
	}

	profile := RequestProfile{
		Model:              features.Model,
		ChannelKind:        features.ChannelKind,
		Operation:          features.Operation,
		AgentRole:          features.AgentRole,
		AgentType:          features.AgentType,
		HasImage:           features.HasImage,
		HasDocument:        features.HasDocument,
		EstTokens:          features.EstTokens,
		Complexity:         features.Complexity,
		QualityNeed:        qualityNeed,
		ContextNeed:        contextNeed,
		VisionNeed:         features.VisionNeed || features.HasImage,
		DocumentNeed:       features.DocumentNeed || features.HasDocument,
		ImageGenNeed:       features.ImageGenNeed,
		EmbeddingNeed:      features.EmbeddingNeed,
		ToolUseNeed:        features.ToolUseNeed,
		ReasoningNeed:      features.ReasoningNeed,
		EmbeddingDimension: features.EmbeddingDimension,
		SessionID:          features.SessionID,
		PromptHash:         features.PromptHash,
	}

	input := BuildClassifierInput(&profile)
	input.DomainHints = features.DomainHints
	ClassifyAndFill(&profile, input)

	// 场景预设解析：请求头 > 全局配置；命中后覆盖 QualityTarget 推导。
	if preset, ok := ResolveScenarioPreset(features.ScenarioCfg, features.RoutingScenarioHeader); ok {
		profile.ScenarioPreset = &preset
	}
	if features.CostPreferenceHeader != "" {
		profile.CostPreferenceOverride = parseCostPreferenceOverride(features.CostPreferenceHeader)
	}
	profile.QualityTarget = ResolveQualityTarget(&profile)

	if features.ClientEffortExplicit {
		profile.ClientEffort = NormalizeEffortLevel(features.ClientEffortRaw)
		profile.ClientEffortExplicit = true
	}

	// IntentEffortPin 始终初始化为空指针载体（Set=false）。
	// RequestProfile 经 context 值拷贝在 SmartRouter（channel 级）与
	// EndpointPolicy（endpoint 级）之间传递时，指针字段共享同一底层对象，
	// SmartRouter 命中带 effort 的手动意图后写入 Set=true，
	// EndpointPolicy 随后通过 BuildCapabilityFloorFromRequestProfile 读取生效。
	profile.IntentEffortPin = &IntentEffortPin{}

	return profile
}

// ResolveQualityTarget 把用户请求模型档位和当前任务难度收敛为跨渠道统一目标。
// 场景预设命中时直接返回预设下限：显式用户意图优先于请求模型档位（QualityNeed）
// 与复杂度推导；能力硬约束（vision/context/tools）在 CapabilityFloor 的独立字段上，不受影响。
func ResolveQualityTarget(profile *RequestProfile) QualityTier {
	if profile == nil {
		return ""
	}
	if profile.ScenarioPreset != nil {
		return profile.ScenarioPreset.MinQualityTier
	}
	if profile.QualityNeed == "" {
		return ""
	}

	target := profile.QualityNeed
	switch profile.TaskClass {
	case TaskClassImageGen:
		target = QualityTierNormal
	case TaskClassEmbedding:
		target = QualityTierLow
	default:
		switch profile.Complexity {
		case TaskComplexityTrivial:
			target = QualityTierLow
		case TaskComplexityRoutine:
			target = QualityTierNormal
		case TaskComplexityComplex:
			if profile.TaskClass == TaskClassWorker {
				target = QualityTierHigh
			} else {
				target = profile.QualityNeed
			}
		default:
			switch profile.TaskClass {
			case TaskClassLightweight:
				target = QualityTierLow
			case TaskClassWorker:
				target = QualityTierNormal
			case TaskClassSupervisor, TaskClassVision, TaskClassLongContext:
				target = QualityTierHigh
			}
		}
	}

	if profile.ReasoningNeed || profile.VisionNeed || profile.HasImage || profile.ContextNeed >= 50_000 {
		if qualityTierRank(target) < qualityTierRank(QualityTierHigh) {
			target = QualityTierHigh
		}
	} else if profile.ToolUseNeed && qualityTierRank(target) < qualityTierRank(QualityTierNormal) {
		target = QualityTierNormal
	}

	if qualityTierRank(target) > qualityTierRank(profile.QualityNeed) {
		return profile.QualityNeed
	}
	return target
}

// parseCostPreferenceOverride 解析请求头价格偏好；仅接受合法枚举，非法/未声明返回空。
// 与 config.NormalizeCostPreferenceMode 的区别：后者把非法值回退 balanced，
// 这里非法必须表现为"未声明"，避免垃圾头静默改变路由行为。
func parseCostPreferenceOverride(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "quality_first", "balanced", "cost_first":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}
