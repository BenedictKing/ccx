package config

import "time"

// MinHealthCheckInterval 保活验证间隔硬下限，任何更小的配置值一律 clamp 到该值。
const MinHealthCheckInterval = 30 * time.Minute

// GlobalHealthCheckConfig 渠道保活验证全局配置（所有字段可选，零值/nil 使用默认值）。
// 注意区别于 autopilot_config.go 中 autopilot 专用的 HealthCheckConfig。
type GlobalHealthCheckConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // 全局开关（nil=启用）
	// DefaultIntervalMinutes 全局默认验证间隔（分钟，0=按渠道 OriginTier 分档）
	DefaultIntervalMinutes int `json:"defaultIntervalMinutes,omitempty"`
	// VerifyRealCall L2 真实调用验证的全局默认（nil=按 OriginTier 分档）
	VerifyRealCall *bool `json:"verifyRealCall,omitempty"`
	// MaxConcurrency 全局最大并发验证数（0=4）
	MaxConcurrency int `json:"maxConcurrency,omitempty"`
	// TimeoutMs 单次验证超时（毫秒，0=10000）
	TimeoutMs int64 `json:"timeoutMs,omitempty"`
	// SparseL2MaxModels 每 key 每周期最多稀疏探测的模型数（0=关闭稀疏 L2）
	SparseL2MaxModels int `json:"sparseL2MaxModels,omitempty"`
	// SparseL2MaxCostAFP 每 key 每周期稀疏 L2 的 AFP 成本预算上限（0=不限制）
	SparseL2MaxCostAFP float64 `json:"sparseL2MaxCostAFP,omitempty"`
	// L2ModelQuietPeriodMinutes 模型近期成功后跳过探测的静默期（分钟，0=继承 Interval）
	L2ModelQuietPeriodMinutes int `json:"l2ModelQuietPeriodMinutes,omitempty"`
}

// ChannelHealthCheckConfig 渠道级保活验证配置（所有字段可选，优先于全局配置与分档默认）
type ChannelHealthCheckConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // 渠道级开关（nil=继承全局，可单独覆盖开/关）
	// IntervalMinutes 渠道级验证间隔（分钟，0=继承全局/分档默认）
	IntervalMinutes int `json:"intervalMinutes,omitempty"`
	// VerifyRealCall 渠道级 L2 真实调用验证开关（nil=继承全局/分档默认）
	VerifyRealCall *bool `json:"verifyRealCall,omitempty"`
	// VerifyModel 指定验活模型（空=自动选最便宜）
	VerifyModel string `json:"verifyModel,omitempty"`
	// SparseL2MaxModels 渠道级覆盖：每 key 每周期最多稀疏探测的模型数（0=关闭）
	SparseL2MaxModels int `json:"sparseL2MaxModels,omitempty"`
	// SparseL2MaxCostAFP 渠道级覆盖：每 key 每周期稀疏 L2 的 AFP 成本预算上限（0=不限制）
	SparseL2MaxCostAFP float64 `json:"sparseL2MaxCostAFP,omitempty"`
	// L2ModelQuietPeriodMinutes 渠道级覆盖：模型近期成功后跳过探测的静默期（分钟，0=继承 Interval）
	L2ModelQuietPeriodMinutes int `json:"l2ModelQuietPeriodMinutes,omitempty"`
}

// ResolvedHealthCheckPolicy 合并后的最终保活验证策略，供调度器直接使用。
type ResolvedHealthCheckPolicy struct {
	Enabled        bool          // 是否启用保活验证
	Interval       time.Duration // 验证间隔（已 clamp 到 >= MinHealthCheckInterval）
	VerifyRealCall bool          // 是否执行 L2 真实调用验证
	VerifyModel    string        // 验活模型（空=自动选最便宜）
	MaxConcurrency int           // 最大并发验证数
	Timeout        time.Duration // 单次验证超时

	// SparseL2MaxModels 每 key 每周期最多稀疏探测的模型数（0=关闭）
	SparseL2MaxModels int
	// SparseL2MaxCostAFP 每 key 每周期稀疏 L2 的 AFP 成本预算上限（0=不限制）
	SparseL2MaxCostAFP float64
	// L2ModelQuietPeriod 模型近期成功后跳过探测的静默期
	L2ModelQuietPeriod time.Duration
}

// healthCheckTierDefaults 按 OriginTier 返回分档默认（间隔、L2 默认开关）。
// OriginTier 取值：first（官方）/ second（中转站）/ third（公益站）/ local / unknown。
func healthCheckTierDefaults(originTier string) (interval time.Duration, verifyRealCall bool) {
	switch originTier {
	case "third": // 公益站：验活最频繁，默认开 L2
		return MinHealthCheckInterval, true
	case "second": // 中转站
		return 2 * time.Hour, false
	default: // first / local / unknown / 空 / 其他
		return 6 * time.Hour, false
	}
}

// ResolveHealthCheckPolicy 解析指定渠道的最终保活验证策略。
// 覆盖优先级：渠道级字段 > 全局字段 > OriginTier 分档默认。
func (c *Config) ResolveHealthCheckPolicy(u *UpstreamConfig) ResolvedHealthCheckPolicy {
	tierInterval, tierVerifyRealCall := healthCheckTierDefaults(u.OriginTier)

	policy := ResolvedHealthCheckPolicy{
		Enabled:            true,
		Interval:           tierInterval,
		VerifyRealCall:     tierVerifyRealCall,
		MaxConcurrency:     4,
		Timeout:            10 * time.Second,
		SparseL2MaxModels:  3,
		SparseL2MaxCostAFP: 6.0,
	}

	if g := c.HealthCheck; g != nil {
		if g.Enabled != nil {
			policy.Enabled = *g.Enabled
		}
		if g.DefaultIntervalMinutes > 0 {
			policy.Interval = time.Duration(g.DefaultIntervalMinutes) * time.Minute
		}
		if g.VerifyRealCall != nil {
			policy.VerifyRealCall = *g.VerifyRealCall
		}
		if g.MaxConcurrency > 0 {
			policy.MaxConcurrency = g.MaxConcurrency
		}
		if g.TimeoutMs > 0 {
			policy.Timeout = time.Duration(g.TimeoutMs) * time.Millisecond
		}
		if g.SparseL2MaxModels > 0 {
			policy.SparseL2MaxModels = g.SparseL2MaxModels
		}
		if g.SparseL2MaxCostAFP > 0 {
			policy.SparseL2MaxCostAFP = g.SparseL2MaxCostAFP
		}
		if g.L2ModelQuietPeriodMinutes > 0 {
			policy.L2ModelQuietPeriod = time.Duration(g.L2ModelQuietPeriodMinutes) * time.Minute
		}
	}

	if ch := u.HealthCheck; ch != nil {
		if ch.Enabled != nil {
			policy.Enabled = *ch.Enabled
		}
		if ch.IntervalMinutes > 0 {
			policy.Interval = time.Duration(ch.IntervalMinutes) * time.Minute
		}
		if ch.VerifyRealCall != nil {
			policy.VerifyRealCall = *ch.VerifyRealCall
		}
		policy.VerifyModel = ch.VerifyModel
		if ch.SparseL2MaxModels > 0 {
			policy.SparseL2MaxModels = ch.SparseL2MaxModels
		}
		if ch.SparseL2MaxCostAFP > 0 {
			policy.SparseL2MaxCostAFP = ch.SparseL2MaxCostAFP
		}
		if ch.L2ModelQuietPeriodMinutes > 0 {
			policy.L2ModelQuietPeriod = time.Duration(ch.L2ModelQuietPeriodMinutes) * time.Minute
		}
	}

	if policy.Interval < MinHealthCheckInterval {
		policy.Interval = MinHealthCheckInterval
	}
	if policy.L2ModelQuietPeriod <= 0 {
		policy.L2ModelQuietPeriod = policy.Interval
	}
	return policy
}
