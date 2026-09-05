package quota

import "strings"

// ── 配额真相五级枚举 ──

// TruthLevel 表示配额余量的可信状态分级。
// 硬原则：unknown ≠ exhausted，unknown 不禁用渠道（fail-open）。
type TruthLevel string

const (
	// TruthHealthy 表示配额充足（> 阈值，默认 20% 余量以上）。
	TruthHealthy TruthLevel = "healthy"
	// TruthApproachingLimit 表示配额接近上限（≤ 阈值，默认 20% 余量以内）。
	TruthApproachingLimit TruthLevel = "approaching_limit"
	// TruthExhausted 表示配额已耗尽（remaining ≤ 0 或 used ≥ limit）。
	TruthExhausted TruthLevel = "exhausted"
	// TruthUnavailable 表示 provider 支持配额查询但本次获取失败/无数据。
	TruthUnavailable TruthLevel = "unavailable"
	// TruthUnknown 表示无任何配额数据（冷启动或不支持的 provider）。
	TruthUnknown TruthLevel = "unknown"
)

// ── 来源优先级 ──

// Source 表示配额数据的来源，按可信度从高到低排列。
// 优先级：provider_api > response_headers > configured > estimated > unknown。
type Source string

const (
	SourceProviderAPI     Source = "provider_api"     // provider 官方账单/用量 API（最高可信度）
	SourceResponseHeaders Source = "response_headers" // 响应头中解析到的速率/配额信息
	SourceConfigured      Source = "configured"       // 配置中静态声明的配额（如 newapi multiplier）
	SourceEstimated       Source = "estimated"        // 基于历史用量的估算
	SourceUnknown         Source = "unknown"          // 无数据
)

// sourceRank 返回来源的优先级排名，数值越小优先级越高。
func sourceRank(s Source) int {
	switch s {
	case SourceProviderAPI:
		return 0
	case SourceResponseHeaders:
		return 1
	case SourceConfigured:
		return 2
	case SourceEstimated:
		return 3
	default:
		return 4
	}
}

// SourceHigherThan 判断 a 是否比 b 可信度更高。
func SourceHigherThan(a, b Source) bool {
	return sourceRank(a) < sourceRank(b)
}

// ── 维度定义 ──

// Dimension 是配额维度名。
type Dimension string

const (
	DimTokens       Dimension = "tokens"        // 总 token 配额
	DimInputTokens  Dimension = "input_tokens"  // 输入 token 配额
	DimOutputTokens Dimension = "output_tokens" // 输出 token 配额
	DimRequests     Dimension = "requests"      // 请求次数配额
	DimCredits      Dimension = "credits"       // 额度/点数
	DimCurrency     Dimension = "currency"      // 货币余额（美元等）
	DimRateLimit    Dimension = "rate_limit"    // 速率限制（RPM/并发等）
	DimUnknown      Dimension = "unknown"
)

// ── 配额值 ──

// Value 表示单个维度的配额数据。
type Value struct {
	Dimension Dimension `json:"dimension"`
	Limit     *float64  `json:"limit,omitempty"`     // 总量上限
	Used      *float64  `json:"used,omitempty"`      // 已用量
	Remaining *float64  `json:"remaining,omitempty"` // 剩余量
	ResetAtMs int64     `json:"resetAtMs,omitempty"` // 窗口重置时间（毫秒时间戳），0 表示未知
	Unit      string    `json:"unit,omitempty"`      // 单位（如 "tokens", "USD"）
	Source    Source    `json:"source"`              // 数据来源
}

// Headroom 返回归一化剩余额度（0.0-1.0）。
// 无法计算时返回 0.5（中性分，对齐冷候选中性分纪律）。
func (v Value) Headroom() float64 {
	// 优先用 remaining/limit
	if v.Remaining != nil && v.Limit != nil && *v.Limit > 0 {
		if *v.Remaining <= 0 {
			return 0.0
		}
		r := *v.Remaining / *v.Limit
		if r > 1.0 {
			r = 1.0
		}
		return r
	}
	// 退化到 used/limit
	if v.Used != nil && v.Limit != nil && *v.Limit > 0 {
		if *v.Used >= *v.Limit {
			return 0.0
		}
		r := 1.0 - (*v.Used / *v.Limit)
		if r < 0 {
			r = 0.0
		}
		return r
	}
	return 0.5 // 中性分
}

// IsExhausted 判断是否已耗尽。
func (v Value) IsExhausted() bool {
	if v.Remaining != nil && *v.Remaining <= 0 {
		return true
	}
	if v.Used != nil && v.Limit != nil && *v.Limit > 0 && *v.Used >= *v.Limit {
		return true
	}
	return false
}

// IsApproaching 判断是否接近上限（approachingThreshold 为余量比例阈值，如 0.2 表示剩余 ≤ 20%）。
func (v Value) IsApproaching(approachingThreshold float64) bool {
	if approachingThreshold <= 0 || approachingThreshold >= 1 {
		approachingThreshold = 0.2
	}
	headroom := v.Headroom()
	if headroom == 0.5 { // 中性分 = 无足够数据
		return false
	}
	return headroom > 0 && headroom <= approachingThreshold
}

// ── 渠道配额状态 ──

// ChannelState 是一个渠道的完整配额状态。
type ChannelState struct {
	ChannelUID  string              `json:"channelUid"`
	AccountUID  string              `json:"accountUid,omitempty"`
	Supported   bool                `json:"supported"`   // 是否有任何来源支持配额查询
	FetchedAtMs int64               `json:"fetchedAtMs"` // 数据获取时间
	Values      map[Dimension]Value `json:"values"`      // 按维度存储，每个维度只保留最高来源优先级的数据
	Status      TruthLevel          `json:"status"`      // 综合状态（取所有维度的最差情况）
	Error       string              `json:"error,omitempty"`
}

// NewChannelState 创建空的渠道配额状态（unknown）。
func NewChannelState(channelUID string) *ChannelState {
	return &ChannelState{
		ChannelUID: channelUID,
		Values:     make(map[Dimension]Value),
		Status:     TruthUnknown,
	}
}

// DeepCopy 返回状态的深拷贝（至少复制 Values map）。
// Manager 拥有状态，对外只交出快照：调用方修改返回值不影响内部数据。
func (cs *ChannelState) DeepCopy() *ChannelState {
	if cs == nil {
		return nil
	}
	cp := *cs
	cp.Values = make(map[Dimension]Value, len(cs.Values))
	for k, v := range cs.Values {
		cp.Values[k] = v
	}
	return &cp
}

// MergeValues 合并一组配额值，按维度逐项判定归属：
//   - 新维度直接写入；
//   - 更高优先级来源覆盖；
//   - 相同来源由本次调用中的最新观测覆盖（同来源快照代表同一数据面的
//     新读数，不覆盖会让调度长期使用陈旧余量，如 provider API 80%→5%）；
//   - 更低优先级来源保持忽略。
//
// Manager 的一次 update 即代表同来源的新快照，无需为 Value 增加时间戳。
func (cs *ChannelState) MergeValues(values []Value) {
	if cs.Values == nil {
		cs.Values = make(map[Dimension]Value)
	}
	for _, v := range values {
		current, exists := cs.Values[v.Dimension]
		if !exists || sourceRank(v.Source) <= sourceRank(current.Source) {
			cs.Values[v.Dimension] = v
		}
	}
	cs.recomputeStatus()
}

// recomputeStatus 从所有维度值重新计算综合状态。
// 策略：取最差状态（exhausted > approaching_limit > healthy > unavailable > unknown）。
// 只要有一个维度 exhausted 就整体 exhausted；没有数据但 supported=true 时为 unavailable。
func (cs *ChannelState) recomputeStatus() {
	if len(cs.Values) == 0 {
		if cs.Supported {
			cs.Status = TruthUnavailable
		} else {
			cs.Status = TruthUnknown
		}
		return
	}

	hasHealthy := false
	hasApproaching := false
	hasExhausted := false

	for _, v := range cs.Values {
		if v.IsExhausted() {
			hasExhausted = true
			break
		}
		if v.IsApproaching(0.2) {
			hasApproaching = true
			continue
		}
		// 有真实 headroom 数据且不为 0.5 中性分
		if v.Headroom() != 0.5 {
			hasHealthy = true
		}
	}

	switch {
	case hasExhausted:
		cs.Status = TruthExhausted
	case hasApproaching:
		cs.Status = TruthApproachingLimit
	case hasHealthy:
		cs.Status = TruthHealthy
	default:
		// 所有值都是中性分（无有效数据）
		if cs.Supported {
			cs.Status = TruthUnavailable
		} else {
			cs.Status = TruthUnknown
		}
	}
}

// OverallHeadroom 返回所有维度的综合 headroom（取最小值）。
// 无数据时返回 0.5（中性分）。
func (cs *ChannelState) OverallHeadroom() float64 {
	if len(cs.Values) == 0 {
		return 0.5
	}

	minHeadroom := 1.0
	hasRealData := false
	for _, v := range cs.Values {
		h := v.Headroom()
		if h != 0.5 {
			hasRealData = true
			if h < minHeadroom {
				minHeadroom = h
			}
		}
	}

	if !hasRealData {
		return 0.5
	}
	return minHeadroom
}

// ── 辅助函数 ──

// ParseSource 解析来源字符串，不识别时回退为 unknown。
func ParseSource(s string) Source {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "provider_api", "provider-api", "providerapi":
		return SourceProviderAPI
	case "response_headers", "response-headers", "headers":
		return SourceResponseHeaders
	case "configured", "config":
		return SourceConfigured
	case "estimated", "estimate":
		return SourceEstimated
	default:
		return SourceUnknown
	}
}

// ParseTruthLevel 解析真相等级字符串，不识别时回退为 unknown。
func ParseTruthLevel(s string) TruthLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "healthy":
		return TruthHealthy
	case "approaching_limit", "approaching-limit", "approaching":
		return TruthApproachingLimit
	case "exhausted":
		return TruthExhausted
	case "unavailable":
		return TruthUnavailable
	default:
		return TruthUnknown
	}
}
