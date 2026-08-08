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

// probeModelCandidate 稀疏 L2 的待探测模型及其排序信号。
type probeModelCandidate struct {
	Model          string
	CostAFP        float64 // 估算 AFP 成本；非火山渠道使用 USD 等价成本
	RecentlyFailed bool    // 熔断中或上次 L2 失败
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
			candidate.CostAFP = volcengineProbeCost(now, model)
			// 标记别名，后续按规范模型去重
			afpResult := config.ResolveVolcengineAFPCost(now, "agent_plan", model, probeEstInputTokens, probeEstOutputTokens)
			if afpResult.IsAlias {
				candidate.IsAlias = true
				candidate.AliasOf = afpResult.AliasOf
			}
		} else {
			candidate.CostAFP = usdProbeCost(model, u, global)
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

		prev, hasPrev := prevL2ByModel[model]
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
			candidate.RecentlyFailed = circuit.IsModelCircuitOpen(channelUID, keyHash, model)
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
		// 最后按成本升序
		return candidates[i].CostAFP < candidates[j].CostAFP
	})

	selected := make([]string, 0, policy.SparseL2MaxModels)
	var costSum float64
	for _, c := range candidates {
		if len(selected) >= policy.SparseL2MaxModels {
			break
		}
		if !c.RecentlyFailed && policy.SparseL2MaxCostAFP > 0 && costSum+c.CostAFP > policy.SparseL2MaxCostAFP {
			continue
		}
		selected = append(selected, c.Model)
		costSum += c.CostAFP
	}
	return selected
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
