package autopilot

// RequestProfileFeatures 是协议层提取出的脱敏请求特征。
// 这里只接收结构化元数据，不接收或持久化消息正文。
type RequestProfileFeatures struct {
	Model                string
	ChannelKind          string
	Operation            string
	AgentRole            string
	AgentType            string
	HasImage             bool
	EstTokens            int
	Complexity           TaskComplexity
	ContextNeed          int
	VisionNeed           bool
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
		qualityNeed = ModelProfileQualityTierFromFamily(family, features.Model)
	}

	profile := RequestProfile{
		Model:              features.Model,
		ChannelKind:        features.ChannelKind,
		Operation:          features.Operation,
		AgentRole:          features.AgentRole,
		AgentType:          features.AgentType,
		HasImage:           features.HasImage,
		EstTokens:          features.EstTokens,
		Complexity:         features.Complexity,
		QualityNeed:        qualityNeed,
		ContextNeed:        contextNeed,
		VisionNeed:         features.VisionNeed || features.HasImage,
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
	profile.QualityTarget = ResolveQualityTarget(&profile)

	if features.ClientEffortExplicit {
		profile.ClientEffort = NormalizeEffortLevel(features.ClientEffortRaw)
		profile.ClientEffortExplicit = true
	}

	return profile
}

// ResolveQualityTarget 把用户请求模型档位和当前任务难度收敛为跨渠道统一目标。
func ResolveQualityTarget(profile *RequestProfile) QualityTier {
	if profile == nil || profile.QualityNeed == "" {
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
