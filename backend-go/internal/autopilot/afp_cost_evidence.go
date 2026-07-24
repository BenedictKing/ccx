package autopilot

import (
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// ────────────────────────────────────────────────────────────────
// 成本证据：描述候选的可比较成本信息。
// AFP 和 USD 是不同单位，不能直接比较数值。
// ────────────────────────────────────────────────────────────────

// CostUnit 表示成本计量单位。
type CostUnit string

const (
	CostUnitAFP CostUnit = "afp" // 火山 Agent Plan AFP
	CostUnitUSD CostUnit = "usd" // 公共按量 USD
	CostUnitUnknown CostUnit = "" // 未知单位
)

// CostConfidence 表示成本估算的置信度。
type CostConfidence int

const (
	CostConfidenceExact     CostConfidence = iota // 完全已知
	CostConfidenceEstimated                       // 部分已知，使用估算
	CostConfidenceUnknown                         // 无法估算
)

// CostEvidence 描述一个候选的成本证据。
// 只有相同 unit + scopeID 的 CostEvidence 才可以直接比较。
type CostEvidence struct {
	Unit      CostUnit       // 计量单位
	ScopeID   string         // 配额作用域标识（AFP 的 scopeID 来自 VolcenginePlanScope）
	Estimated int64          // 估算成本（AFP 或 USD 的归一化值）
	Actual    int64          // 实际成本（请求后回填，0 表示未知）
	Confidence CostConfidence // 置信度
	Source    string         // 来源说明
}

// IsComparableWith 判断两个 CostEvidence 是否可以直接比较。
func (ce CostEvidence) IsComparableWith(other CostEvidence) bool {
	if ce.Unit == CostUnitUnknown || other.Unit == CostUnitUnknown {
		return false
	}
	if ce.Unit != other.Unit {
		return false
	}
	// AFP 必须在同一 scope 内比较
	if ce.Unit == CostUnitAFP {
		return ce.ScopeID != "" && ce.ScopeID == other.ScopeID
	}
	// USD 可以直接比较
	return true
}

// ────────────────────────────────────────────────────────────────
// 定价快照：请求入口冻结的定价评估状态。
// ────────────────────────────────────────────────────────────────

// TokenEstimateSource 表示 token 估算的来源。
type TokenEstimateSource string

const (
	TokenEstimateSourceClient   TokenEstimateSource = "client"   // 客户端显式提供
	TokenEstimateSourceLocal    TokenEstimateSource = "local"    // 本地字符估算
	TokenEstimateSourceUnknown  TokenEstimateSource = "unknown"  // 未知来源
)

// TokenEstimate 描述 token 数量估算及其置信度。
type TokenEstimate struct {
	Tokens int                  // 估算 token 数
	Source TokenEstimateSource  // 来源
}

// PricingSnapshot 是请求入口冻结的定价评估快照。
// 所有候选共享同一入口时钟快照，避免活动边界前后不一致。
type PricingSnapshot struct {
	// EvaluatedAt 是定价评估时刻。
	EvaluatedAt int64 // Unix timestamp

	// InputEstimate 是输入 token 估算。
	InputEstimate TokenEstimate

	// OutputBudget 是输出 token 预算。
	// 客户端显式 max_tokens 时为精确值；未知时为 0。
	OutputBudget TokenEstimate

	// PolicyVersion 是 AFP 政策版本（目前为规则文件的 VerifiedAt）。
	PolicyVersion string
}

// ────────────────────────────────────────────────────────────────
// 请求级扩展：在 RequestProfile 中嵌入定价快照。
// ────────────────────────────────────────────────────────────────

// AFPRequestProfile 包含 AFP 路由所需的请求级扩展信息。
// 嵌入在 RequestProfile 的 AFPProfile 字段中。
type AFPRequestProfile struct {
	// PricingSnapshot 是请求入口冻结的定价快照。
	PricingSnapshot PricingSnapshot

	// EstOutputTokens 是输出 token 估算或预算。
	// 来自客户端 max_tokens 或本地估算。
	EstOutputTokens int

	// AgentPlanScope 是该请求关联的火山套餐作用域。
	// 仅在请求可识别为火山 Agent Plan 时填充。
	AgentPlanScope *config.VolcenginePlanScope
}

// BuildAFPRequestProfile 从 RequestProfile 和外部上下文构建 AFP 扩展信息。
func BuildAFPRequestProfile(profile *RequestProfile, maxTokens int, scope *config.VolcenginePlanScope) *AFPRequestProfile {
	if profile == nil {
		return nil
	}
	afp := &AFPRequestProfile{
		EstOutputTokens: maxTokens,
		AgentPlanScope:  scope,
	}

	// 构建定价快照
	afp.PricingSnapshot.EvaluatedAt = 0 // 将由调用方设置
	afp.PricingSnapshot.PolicyVersion = agentPlanAFPPolicyVersion

	// 输入 token 估算
	inputTokens := profile.EstTokens
	source := TokenEstimateSourceLocal
	if profile.ContextNeed > 0 && profile.ContextNeed > inputTokens {
		inputTokens = profile.ContextNeed
	}
	if maxTokens > 0 && profile.EstTokens == 0 {
		// 有 max_tokens 但无输入估算时，使用 ContextNeed
		source = TokenEstimateSourceLocal
	}
	afp.PricingSnapshot.InputEstimate = TokenEstimate{
		Tokens: inputTokens,
		Source: source,
	}

	// 输出 token 预算
	outSource := TokenEstimateSourceUnknown
	if maxTokens > 0 {
		outSource = TokenEstimateSourceClient
	}
	afp.PricingSnapshot.OutputBudget = TokenEstimate{
		Tokens: maxTokens,
		Source: outSource,
	}

	return afp
}

// agentPlanAFPPolicyVersion 是当前内置 AFP 政策的版本标识。
// 格式为 "volc-afp-" + 核验日期，与规则 VerifiedAt 一致。
const agentPlanAFPPolicyVersion = "volc-afp-2026-07-24"

// ────────────────────────────────────────────────────────────────
// 候选级 AFP 成本计算
// ────────────────────────────────────────────────────────────────

// CandidateAFPCost 是候选级别的 AFP 成本计算结果。
// 在 model、endpoint 和 credential 已知后按候选生成。
type CandidateAFPCost struct {
	// Result 是 AFP 计算结果。
	Result config.AFPCostResult

	// CostEvidence 是可供排序的成本证据。
	Evidence CostEvidence
}

// ComputeCandidateAFPCost 在候选已知后计算 AFP 成本。
// 仅在请求可识别为火山 Agent Plan 时返回有效结果。
func ComputeCandidateAFPCost(
	at int64,
	profile *AFPRequestProfile,
	modelID string,
	inputTokens int,
	outputTokens int,
) *CandidateAFPCost {
	if profile == nil || profile.AgentPlanScope == nil || !profile.AgentPlanScope.AFPComparable {
		return nil
	}

	scope := profile.AgentPlanScope
	t := timeFromUnix(at)
	result := config.ResolveVolcengineAFPCost(t, scope.Plan, modelID, inputTokens, outputTokens)

	if !result.Matched {
		return nil
	}

	cost := &CandidateAFPCost{
		Result: result,
		Evidence: CostEvidence{
			Unit:       CostUnitAFP,
			ScopeID:    scope.ScopeID,
			Estimated:  result.TotalAFP,
			Confidence: costConfidenceFromAFP(result.Confidence),
			Source:     "volcengine_afp_pricing",
		},
	}

	return cost
}

// costConfidenceFromAFP 将 AFP 置信度转换为通用成本置信度。
func costConfidenceFromAFP(c config.AFPCostConfidence) CostConfidence {
	switch c {
	case config.AFPCostConfidenceExact:
		return CostConfidenceExact
	case config.AFPCostConfidenceEstimated:
		return CostConfidenceEstimated
	default:
		return CostConfidenceUnknown
	}
}

// timeFromUnix 从 Unix 时间戳创建 time.Time。
func timeFromUnix(ts int64) time.Time {
	if ts <= 0 {
		return time.Time{} // 零值，让调用方处理
	}
	return time.Unix(ts, 0)
}
