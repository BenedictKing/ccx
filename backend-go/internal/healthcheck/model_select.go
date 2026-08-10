package healthcheck

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
)

// probeEstInputTokens 与 probeEstOutputTokens 用于模型间相对比较，不代表真实请求大小。
const (
	probeEstInputTokens  = 10000
	probeEstOutputTokens = 1000
)

// l2ModelCheckKind 返回模型级 L2 记录使用的 check_kind。
func l2ModelCheckKind(model string) string {
	return CheckKindL2 + ":" + model
}

// parseL2ModelCheckKind 解析 check_kind 是否为 "l2:<model>" 格式；是则返回模型名。
func parseL2ModelCheckKind(kind string) (string, bool) {
	if !strings.HasPrefix(kind, CheckKindL2+":") {
		return "", false
	}
	model := strings.TrimPrefix(kind, CheckKindL2+":")
	model = strings.TrimSpace(model)
	return model, model != ""
}

// probeCostUnit 表示候选模型成本的物理单位。
type probeCostUnit int

const (
	// probeCostUnitUnknown 表示无可用定价信息，该候选不应被排序选中。
	probeCostUnitUnknown probeCostUnit = iota
	// probeCostUnitAFP 火山 Agent Plan 的 AFP 计费单位。
	probeCostUnitAFP
	// probeCostUnitUSD 非火山渠道的 USD 相对成本（仅用于同渠道内相对比较）。
	probeCostUnitUSD
)

// ProbeUsageResolver 是 healthcheck 包读取火山套餐 AFP 余额的最小抽象接口。
// 由 main.go 注入 ConfigManager 方法或类似实现，避免 healthcheck 直接 import autopilot。
type ProbeUsageResolver interface {
	// ResolveVolcenginePlanUsage 返回指定账号/凭证最近一次获取的火山套餐用量快照。
	// 未找到或快照无效时返回 nil。
	ResolveVolcenginePlanUsage(accountUID, credentialUID string) *config.VolcenginePlanUsage
}

// probeModelCandidate 稀疏 L2 的待探测模型及其排序信号。
type probeModelCandidate struct {
	Model          string
	CostValue      float64       // 按 CostUnit 解释的数值
	CostUnit       probeCostUnit // AFP/USD/unknown
	RecentlyFailed bool          // 熔断中或上次 L2 失败
	LastSuccessAt  time.Time
	IsAlias        bool   // 是否为另一候选模型的别名
	AliasOf        string // 别名指向的规范模型；非空时用于去重
}

// selectL2ProbeModels 从 L1 返回的模型清单中，按预算与优先级挑选本周期要探测的模型。
//
// 排序规则：
//  1. 最近失败的模型最优先（验证恢复）。
//  2. 无近期成功记录的模型次之。
//  3. 剩余模型按成本升序。
//
// 预算规则：
//   - 最近失败的模型不受成本预算限制（必须验证）。
//   - 其余模型按顺序加入，直到达到 maxModels 或 maxCostAFP。
//   - 当可用模型较多且存在失败模型时，动态放宽数量与成本上限，避免失败模型独占周期后无剩余预算。
//   - 火山 Agent Plan 渠道额外受剩余 AFP 余额比例限制，避免探测蚕食生产额度。
//
// circuit 为 nil 时仅使用持久化的 key_health 记录判断失败/成功。
func (m *Manager) selectL2ProbeModels(
	channelUID string,
	keyHash string,
	u *config.UpstreamConfig,
	models []string,
	circuit *metrics.ModelCircuitTracker,
	prevL2ByModel map[string]metrics.KeyHealthRecord,
	policy config.ResolvedHealthCheckPolicy,
	now time.Time,
) []string {
	if policy.SparseL2MaxModels <= 0 || len(models) == 0 {
		return nil
	}

	maxModels, maxCostAFP := effectiveSparseBudget(policy, len(models), recentlyFailedCount(models, prevL2ByModel, circuit, channelUID, keyHash, now, policy.L2ModelQuietPeriod), 0)
	// 火山 Agent Plan 渠道：用剩余 AFP 余额比例进一步限制成本上限
	if config.IsVolcengineProvider(u) {
		maxCostAFP = m.clampCostByVolcengineBalance(u, maxCostAFP)
	}

	cfg := m.getConfig()
	global := cfg.UpstreamModelCapabilities
	isVolcengine := config.IsVolcengineProvider(u)

	candidates := make([]probeModelCandidate, 0, len(models))
	canonicalSeen := make(map[string]bool)

	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if !u.SupportsModel(model) {
			continue
		}

		candidate := probeModelCandidate{Model: model}
		if isVolcengine {
			candidate.CostValue = volcengineProbeCost(now, model)
			candidate.CostUnit = probeCostUnitAFP
			// 标记别名，后续按规范模型去重
			afpResult := config.ResolveVolcengineAFPCost(now, "agent_plan", model, probeEstInputTokens, probeEstOutputTokens)
			if afpResult.IsAlias {
				candidate.IsAlias = true
				candidate.AliasOf = afpResult.AliasOf
			}
		} else {
			candidate.CostValue = usdProbeCost(model, u, global)
			candidate.CostUnit = probeCostUnitUSD
		}

		// 别名去重：只保留规范模型；若规范模型不在列表中，则保留别名自身
		if candidate.IsAlias && candidate.AliasOf != "" {
			if canonicalSeen[strings.ToLower(candidate.AliasOf)] {
				continue
			}
			// 检查原始模型清单是否包含该规范模型
			hasCanonical := false
			for _, m := range models {
				if strings.EqualFold(strings.TrimSpace(m), candidate.AliasOf) {
					hasCanonical = true
					break
				}
			}
			if hasCanonical {
				continue
			}
		}
		canonicalSeen[strings.ToLower(model)] = true

		prev, hasPrev := prevL2ByModel[candidate.Model]
		if hasPrev {
			switch prev.LastStatus {
			case StatusOK:
				candidate.LastSuccessAt = prev.LastCheckAt
			case StatusError, StatusAuthFailed:
				// 近期失败视为需要优先验证；AuthFailed 一般也会通过 ShouldBlacklistKey 处理，
				// 但这里仍把它当作失败信号，以便快速发现恢复。
				candidate.RecentlyFailed = now.Sub(prev.LastCheckAt) <= policy.L2ModelQuietPeriod
			}
		}

		// 内存态熔断提供更快的失败信号
		if !candidate.RecentlyFailed && circuit != nil && channelUID != "" && keyHash != "" {
			candidate.RecentlyFailed = circuit.IsModelCircuitOpen(channelUID, keyHash, candidate.Model)
		}

		candidates = append(candidates, candidate)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		// 最近失败最优先
		if candidates[i].RecentlyFailed != candidates[j].RecentlyFailed {
			return candidates[i].RecentlyFailed
		}
		// 其次比较是否有近期成功
		hiSuccess := !candidates[i].LastSuccessAt.IsZero() && now.Sub(candidates[i].LastSuccessAt) <= policy.L2ModelQuietPeriod
		hjSuccess := !candidates[j].LastSuccessAt.IsZero() && now.Sub(candidates[j].LastSuccessAt) <= policy.L2ModelQuietPeriod
		if hiSuccess != hjSuccess {
			return !hiSuccess // 无近期成功的更优先
		}
		// 单位不同或任一未知时不按成本比较，保持原始顺序稳定（不跨单位排序）
		if candidates[i].CostUnit != candidates[j].CostUnit {
			return false
		}
		if candidates[i].CostUnit == probeCostUnitUnknown {
			return false
		}
		// 最后按同单位成本升序
		return candidates[i].CostValue < candidates[j].CostValue
	})

	selected := make([]string, 0, maxModels)
	var costSum float64
	var costUnit probeCostUnit = probeCostUnitUnknown
	for _, c := range candidates {
		if len(selected) >= maxModels {
			break
		}
		// 以首个有定价单位的候选确定本渠道的成本单位（同渠道内单位一致）。
		// 失败模型即便不受预算限制，其成本也要计入 costSum，避免后续预算被高估。
		if costUnit == probeCostUnitUnknown && c.CostUnit != probeCostUnitUnknown {
			costUnit = c.CostUnit
		}
		// 非失败候选受成本预算限制；预算只在同单位内累计，避免 AFP/USD 混加
		if !c.RecentlyFailed && maxCostAFP > 0 && c.CostUnit == costUnit && c.CostUnit != probeCostUnitUnknown {
			if costSum+c.CostValue > maxCostAFP {
				continue
			}
		}
		selected = append(selected, c.Model)
		if c.CostUnit == costUnit {
			costSum += c.CostValue
		}
	}
	return selected
}

// clampCostByVolcengineBalance 用剩余 AFP 余额比例限制稀疏 L2 成本上限。
//
// 规则：
//   - 余额未知（无 resolver 或 snapshot 无效）时，直接返回原 maxCost，不放大预算。
//   - 余额为零或负时，将 maxCost 限制为 0（关闭非失败模型的 AFP 探测）。
//   - 否则 maxCost = min(maxCost, remainingAFP * defaultAFPBalanceReserveRatio)。
//
// 剩余 AFP 取 Agent Plan 用量快照中所有窗口 (Quota - Used) 的最小值，代表最紧张的窗口约束。
func (m *Manager) clampCostByVolcengineBalance(u *config.UpstreamConfig, maxCost float64) float64 {
	if maxCost <= 0 {
		return 0
	}
	m.mu.Lock()
	resolver := m.usageResolver
	m.mu.Unlock()
	if resolver == nil {
		return maxCost
	}
	usage := resolver.ResolveVolcenginePlanUsage(u.AccountUID, "")
	if usage == nil {
		return maxCost
	}
	remaining := volcengineRemainingAFP(usage)
	if remaining <= 0 {
		return 0
	}
	const defaultAFPBalanceReserveRatio = 0.05
	capped := remaining * defaultAFPBalanceReserveRatio
	if capped < maxCost {
		return capped
	}
	return maxCost
}

// volcengineRemainingAFP 计算火山套餐用量快照中各窗口剩余 AFP 的最小值。
// 仅对 Quota > 0 的窗口计算；若全部窗口无 Quota，返回 MaxFloat64（表示未知）。
func volcengineRemainingAFP(usage *config.VolcenginePlanUsage) float64 {
	if usage == nil {
		return 0
	}
	windows := []*config.VolcenginePlanUsageWindow{usage.FiveHour, usage.Daily, usage.Weekly, usage.Monthly}
	minRemaining := math.MaxFloat64
	hasQuota := false
	for _, w := range windows {
		if w == nil || w.Quota <= 0 {
			continue
		}
		hasQuota = true
		remaining := w.Quota - w.Used
		if remaining < minRemaining {
			minRemaining = remaining
		}
	}
	if !hasQuota {
		return math.MaxFloat64
	}
	if minRemaining < 0 {
		return 0
	}
	return minRemaining
}

// effectiveSparseBudget 根据模型总量、近期失败数量与负载动态计算稀疏 L2 预算上限。
//
// 规则：
//   - 返回的 maxModels 下界始终为 policy.SparseL2MaxModels；上界为 policy 值 + f(modelCount)，
//     其中 f = min(max(0, modelCount/3 - 1), 5)，即每 3 个可用模型额外放宽 1 个名额，最多放宽 5 个。
//   - 返回的 maxCostAFP 下界为 policy.SparseL2MaxCostAFP；上界为 2x policy 值（0 表示无成本限制）。
//   - 最近失败的模型不受成本预算限制，但预算上限的放宽会受失败数量约束：
//     当最近失败模型数大于等于放宽后的名额时，不再额外加宽，避免失败模型独占周期。
//   - loadRatio 预留接口：>1 时可按比例收缩上限；默认 0 表示不收缩（no-op）。
func effectiveSparseBudget(
	policy config.ResolvedHealthCheckPolicy,
	modelCount int,
	recentlyFailedCount int,
	loadRatio float64,
) (maxModels int, maxCostAFP float64) {
	baseModels := policy.SparseL2MaxModels
	if baseModels <= 0 {
		return 0, policy.SparseL2MaxCostAFP
	}

	extra := 0
	if modelCount > 0 {
		extra = modelCount/3 - 1
		if extra < 0 {
			extra = 0
		}
		if extra > 5 {
			extra = 5
		}
	}

	maxModels = baseModels + extra
	if recentlyFailedCount >= maxModels {
		maxModels = baseModels
	}

	maxCostAFP = policy.SparseL2MaxCostAFP
	if maxCostAFP > 0 {
		maxCostAFP *= 2
	}

	if loadRatio > 1 {
		scale := 1.0 / loadRatio
		maxModels = int(float64(maxModels) * scale)
		if maxModels < baseModels {
			maxModels = baseModels
		}
		if maxCostAFP > 0 {
			maxCostAFP *= scale
		}
	}

	return maxModels, maxCostAFP
}

// recentlyFailedCount 统计候选模型中最近失败的模型数量，用于动态预算决策。
// 逻辑与 selectL2ProbeModels 中的 RecentlyFailed 判定保持一致。
func recentlyFailedCount(
	models []string,
	prevL2ByModel map[string]metrics.KeyHealthRecord,
	circuit *metrics.ModelCircuitTracker,
	channelUID string,
	keyHash string,
	now time.Time,
	quietPeriod time.Duration,
) int {
	count := 0
	seen := make(map[string]bool)
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || seen[strings.ToLower(model)] {
			continue
		}
		seen[strings.ToLower(model)] = true

		failed := false
		if prev, ok := prevL2ByModel[model]; ok {
			switch prev.LastStatus {
			case StatusError, StatusAuthFailed:
				failed = now.Sub(prev.LastCheckAt) <= quietPeriod
			}
		}
		if !failed && circuit != nil && channelUID != "" && keyHash != "" {
			failed = circuit.IsModelCircuitOpen(channelUID, keyHash, model)
		}
		if failed {
			count++
		}
	}
	return count
}

// volcengineProbeCost 估算火山 Agent Plan 模型小 token 探针的 AFP 成本。
// 未知模型返回 MaxFloat64，使其不会被选中。
func volcengineProbeCost(now time.Time, model string) float64 {
	result := config.ResolveVolcengineAFPCost(now, "agent_plan", model, probeEstInputTokens, probeEstOutputTokens)
	if !result.Matched {
		return math.MaxFloat64
	}
	return float64(result.TotalAFP)
}

// usdProbeCost 估算非火山模型的相对 USD 成本。
// 无定价信息时返回 MaxFloat64。
func usdProbeCost(model string, u *config.UpstreamConfig, global map[string]config.UpstreamModelCapability) float64 {
	resolved := config.ResolveUpstreamCapability(model, u, global)
	cost, ok := pricingCost(resolved.Capability.Pricing)
	if !ok {
		return math.MaxFloat64
	}
	return cost
}
