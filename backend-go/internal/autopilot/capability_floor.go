package autopilot

// CandidateCapabilities 候选渠道/endpoint 的能力子集，供硬约束检查使用。
type CandidateCapabilities struct {
	SupportsToolCalls     bool
	SupportsReasoning     bool
	SupportsSeverityClass bool // 能否完成格式约束型安全分类请求（未知=true，仅实测负结论收紧）
	ContextWindowTokens   int  // 0 = 未知，未知时不做上下文过滤（避免误杀无画像新渠道）
}

// CapabilityFloorReasons 检查候选是否满足请求的能力下界，返回不满足的原因列表（空=通过）。
// 与现有 vision 硬约束风格一致：不满足时返回非空 reasons，调用方据此过滤掉该候选。
func CapabilityFloorReasons(caps CandidateCapabilities, profile *RequestProfile) []string {
	var reasons []string

	// 工具调用硬约束
	if profile.ToolUseNeed && !caps.SupportsToolCalls {
		reasons = append(reasons, "工具调用能力不满足")
	}

	// 推理硬约束
	if profile.ReasoningNeed && !caps.SupportsReasoning {
		reasons = append(reasons, "推理能力不满足")
	}

	// 安全分类硬约束：格式约束型分类请求实测无法正确完成的渠道×模型。
	// SupportsSeverityClass 默认 true（静态注册表无此维度），只有运行期学到
	// 负结论（TraitNoSeverityClass）才会变 false，因此该约束零记忆时零影响。
	if profile.SeverityClassNeed && !caps.SupportsSeverityClass {
		reasons = append(reasons, "安全分类格式能力不满足")
	}

	// 上下文窗口硬约束：只在双方都有明确数值时检查。
	// ContextNeed 与 ContextWindowTokens 均表示输入 token；输出上限已由前置 scheduler
	// 单独校验，此处不得再次叠加输出预留，否则会造成二次扣减。
	// profile.ContextNeed=0 表示请求无上下文需求，跳过检查
	// caps.ContextWindowTokens=0 表示候选画像未知，跳过检查（避免误杀）
	if profile.ContextNeed > 0 && caps.ContextWindowTokens > 0 && caps.ContextWindowTokens < profile.ContextNeed {
		reasons = append(reasons, "上下文窗口不满足")
	}

	return reasons
}

// MinQualityTierReasons 检查候选质量档位是否满足最低要求。
// candidate < minRequired 时返回原因列表（空=通过）。
// 相等（刚好满足）不算不满足，返回空。
// 使用 channel_profile.go 中已有的 qualityTierRank 进行档位比较。
func MinQualityTierReasons(candidateQuality QualityTier, minRequired QualityTier) []string {
	if qualityTierRank(candidateQuality) < qualityTierRank(minRequired) {
		return []string{"质量档位不满足最低要求"}
	}
	return nil
}
