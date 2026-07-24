package config

import (
	"crypto/sha256"
	"fmt"
)

// ────────────────────────────────────────────────────────────────
// 火山套餐作用域解析
//
// 通过 AccountUID + CredentialUID 解析火山 Agent/Coding Plan 的
// 套餐身份、配额作用域和 AFP 可比性。
// ────────────────────────────────────────────────────────────────

// VolcenginePlanScope 解析后的火山套餐作用域。
// 不包含任何密钥或原始 URL，仅用于路由决策和成本比较。
type VolcenginePlanScope struct {
	// ScopeID 是匿名稳定的配额作用域标识，格式为 "vp_" + sha256(AccountUID + CredentialUID + Plan)[:12]。
	// 同一 scope 内的候选可以直接比较 AFP 成本。
	ScopeID string

	// Plan 是套餐类型："agent_plan" / "coding_plan" / ""。
	Plan string

	// PlanTier 是套餐层级："Small" / "Medium" / "Large" / ""。
	PlanTier string

	// PlanStatus 是套餐状态："Running" / "Expired" / "Suspended" / ""。
	PlanStatus string

	// AFPComparable 表示该 scope 的 AFP 成本是否可以用于候选间比较。
	// 仅 agent_plan 且状态为 Running 时为 true。
	AFPComparable bool

	// AFPComparableReason 是 AFPComparable=false 时的原因说明。
	AFPComparableReason string

	// UsageSnapshot 是用量快照时间，用于判断数据新鲜度。
	UsageSnapshot *VolcenginePlanUsage

	// UsageExhausted 表示当前配额窗口是否已耗尽。
	// Agent Plan 有 Quota+Used 可判断；Coding Plan 仅 UsedPercent，保守返回 false。
	UsageExhausted bool
}

// ResolveVolcenginePlanScope 通过 ConfigManager 解析火山套餐作用域。
//
// 解析优先级：AccountUID + CredentialUID → VolcengineAccessKeyPair → 套餐元数据。
// 仅在凭证关联缺失时返回空 scope，不从渠道名称、API Key 或自由文本猜测套餐。
func ResolveVolcenginePlanScope(cm *ConfigManager, accountUID, credentialUID string) VolcenginePlanScope {
	scope := VolcenginePlanScope{}

	if cm == nil || accountUID == "" || credentialUID == "" {
		scope.AFPComparableReason = "missing ConfigManager, AccountUID or CredentialUID"
		return scope
	}

	cred, ok := cm.GetManagedAccountCredential(accountUID, credentialUID)
	if !ok {
		scope.AFPComparableReason = fmt.Sprintf("credential %s not found in account %s", credentialUID, accountUID)
		return scope
	}

	pair := cred.VolcengineAccessKey
	if pair == nil {
		scope.AFPComparableReason = "no VolcengineAccessKey bound to credential"
		return scope
	}

	scope.Plan = pair.Plan
	scope.PlanTier = pair.PlanTier
	scope.PlanStatus = pair.PlanStatus
	scope.UsageSnapshot = pair.Usage

	// 生成匿名 scopeID
	scope.ScopeID = generateVolcengineScopeID(accountUID, credentialUID, pair.Plan)

	// AFP 可比性判断
	switch pair.Plan {
	case "agent_plan":
		if pair.PlanStatus == "Running" {
			scope.AFPComparable = true
		} else {
			scope.AFPComparableReason = fmt.Sprintf("agent_plan status is %q (not Running)", pair.PlanStatus)
		}
	case "coding_plan":
		scope.AFPComparable = false
		scope.AFPComparableReason = "coding_plan AFP rules not verified; only agent_plan supported"
	default:
		scope.AFPComparable = false
		if pair.Plan == "" {
			scope.AFPComparableReason = "plan type unknown (empty)"
		} else {
			scope.AFPComparableReason = fmt.Sprintf("unsupported plan type: %q", pair.Plan)
		}
	}

	// 判断配额是否耗尽
	if scope.AFPComparable && pair.Usage != nil {
		scope.UsageExhausted = checkUsageExhausted(pair.Usage)
		if scope.UsageExhausted {
			scope.AFPComparable = false
			scope.AFPComparableReason = "current quota window exhausted"
		}
	}

	return scope
}

// ResolveVolcenginePlanScopeFromUpstream 从 UpstreamConfig 解析火山套餐作用域。
// 自动从 APIKeyConfigs 或 CredentialUIDForKey 提取凭证身份。
func ResolveVolcenginePlanScopeFromUpstream(cm *ConfigManager, upstream *UpstreamConfig, apiKey string) VolcenginePlanScope {
	if upstream == nil || cm == nil {
		return VolcenginePlanScope{AFPComparableReason: "nil upstream or ConfigManager"}
	}
	if upstream.ProviderID != "volcengine" && upstream.ProviderID != "volc-ark" {
		return VolcenginePlanScope{AFPComparableReason: "not a volcengine provider"}
	}
	credUID := upstream.CredentialUIDForKey(apiKey)
	if credUID == "" && upstream.AccountUID != "" && apiKey != "" {
		credUID = GenerateCredentialUID(upstream.AccountUID, apiKey)
	}
	return ResolveVolcenginePlanScope(cm, upstream.AccountUID, credUID)
}

// generateVolcengineScopeID 生成匿名稳定的配额作用域标识。
// 使用 sha256(accountUID + credentialUID + plan) 的前 12 字节 hex。
// 不含任何密钥信息，仅用于同 scope 候选的分组和比较。
func generateVolcengineScopeID(accountUID, credentialUID, plan string) string {
	h := sha256.Sum256([]byte(accountUID + "|" + credentialUID + "|" + plan))
	return fmt.Sprintf("vp_%x", h[:6])
}

// checkUsageExhausted 判断当前配额窗口是否已耗尽。
// Agent Plan 的 FiveHour 窗口是主要判断依据（5 小时滑动窗口）。
// 有 Quota 时用 Quota-Used 判断；仅有 UsedPercent 时保守返回 false。
func checkUsageExhausted(usage *VolcenginePlanUsage) bool {
	if usage == nil {
		return false
	}
	// FiveHour 窗口是 Agent Plan 的核心配额
	if usage.FiveHour != nil {
		if usage.FiveHour.Quota > 0 && usage.FiveHour.Used >= usage.FiveHour.Quota {
			return true
		}
	}
	// Daily 窗口
	if usage.Daily != nil {
		if usage.Daily.Quota > 0 && usage.Daily.Used >= usage.Daily.Quota {
			return true
		}
	}
	return false
}

// IsVolcengineProvider 检查上游是否为火山 provider。
func IsVolcengineProvider(upstream *UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	return upstream.ProviderID == "volcengine" || upstream.ProviderID == "volc-ark"
}
