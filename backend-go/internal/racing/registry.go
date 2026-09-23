// Package racing 实现跨渠道影子竞速（hedging）的阈值观测与提交裁决基础设施。
//
// 机制参照 kiro.rs 的对冲竞速：主请求等待超过自适应阈值时向候选渠道并行派影子请求，
// 谁先交付有效响应用谁。阈值 = 该模型家族近 15 分钟成功样本的分位数（p90），
// 样本不足时回退策略表 floor，再 clamp 到 [floor, ceiling]。
//
// 行为参数（影子数、floor、候选成本过滤）不暴露为配置项，由请求的
// CostPreference 经 BehaviorForCostPreference 策略表自动推导；用户只控制开关。
package racing

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── 维度 ──

// Stage 延迟观测阶段。
type Stage int

const (
	// StageStreamFirstContent 流式：HTTP 200 后首个有效语义内容耗时。
	StageStreamFirstContent Stage = iota
	// StageNonStreamComplete 非流式：完整有效响应交付耗时。
	StageNonStreamComplete
)

// Family 模型家族。同家族首字延迟特征接近，按家族分窗加速样本积累。
type Family int

const (
	FamilyClaude Family = iota
	FamilyGPT
	FamilyGemini
	FamilyOther
)

// FamilyForModel 按模型名归类家族。
func FamilyForModel(model string) Family {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "":
		return FamilyOther
	case strings.HasPrefix(m, "claude") ||
		strings.Contains(m, "sonnet") || strings.Contains(m, "opus") ||
		strings.Contains(m, "haiku") || strings.Contains(m, "fable"):
		return FamilyClaude
	case strings.HasPrefix(m, "gpt") || strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") ||
		strings.Contains(m, "codex"):
		return FamilyGPT
	case strings.HasPrefix(m, "gemini"):
		return FamilyGemini
	default:
		return FamilyOther
	}
}

// ── 内部策略常量（不暴露为配置项）──

const (
	// 观测窗口与样本容量
	windowRetention     = 15 * time.Minute
	maxSamplesPerWindow = 512
	// 分位数目标与启用分位数所需最小样本数
	percentileTarget = 90
	sampleFloor      = 20
	// 全局并发影子上限（防主渠道系统性事故时影子堆积放大）
	maxConcurrentShadows = 12
)

// MaxConcurrentShadows 全局并发影子信号量容量。
func MaxConcurrentShadows() int { return maxConcurrentShadows }

// ── 策略表：CostPreference → 竞速行为 ──

// Behavior 一次请求的竞速行为（由策略表推导，非配置）。
type Behavior struct {
	// MaxShadows 本请求最多派出的影子数。
	MaxShadows int
	// StreamFloorMs 流式触发下限（毫秒）；样本不足时的静态回退值。
	StreamFloorMs int
	// CheapCandidateOnly true 时仅允许综合倍率 ≤ 主候选一半的候选作影子（cost_first）。
	CheapCandidateOnly bool
}

// BehaviorForCostPreference 按请求的 CostPreference 推导竞速行为。
// 流式首字 floor 两档（2026-09-12 拍板）：quality_first=4s（速度敏感，早对冲），
// balanced / cost_first=8s（信任主渠道/少烧影子钱）。
// 旧值 2s/3s/5s 对"慢而真"的渠道（如 ark kimi-k3 真实首字 2-4s）系统性误判慢，
// 且家族分位数窗口混入快而差的中转假模型首字后 p90 被拉低，2s 档形同虚设——
// 抢闸交付伪工具标记文本的根因之一。floor 上调后 clamp 兜底两类污染。
// quality_first：3 影子；balanced：1 影子；cost_first：1 影子且仅更便宜候选，
// 无合适候选即不竞速。空值/未知值按 balanced。
func BehaviorForCostPreference(costPreference string) Behavior {
	switch strings.TrimSpace(costPreference) {
	case "quality_first":
		return Behavior{MaxShadows: 3, StreamFloorMs: 4000}
	case "cost_first":
		return Behavior{MaxShadows: 1, StreamFloorMs: 8000, CheapCandidateOnly: true}
	default: // balanced / "" / 未知
		return Behavior{MaxShadows: 1, StreamFloorMs: 8000}
	}
}

// StreamFloorForReasoning 返回带隐藏推理阶段的流式竞速下限。
// 这类模型可能在产生首个可见语义内容前完成较长推理，沿用普通请求的
// 4s/8s floor 会在正常推理期间过早派出影子请求。显式 effort 越高，等待下限越高；
// 未声明 effort 时按中档保守处理。最终阈值仍由调用方按渠道首字超时上限裁剪。
func StreamFloorForReasoning(baseMs int, effort string) int {
	if baseMs < 0 {
		baseMs = 0
	}
	minimum := 24_000
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "minimal":
		minimum = 16_000
	case "medium":
		minimum = 24_000
	case "high", "xhigh", "max", "ultra":
		minimum = 30_000
	case "none", "off":
		return baseMs
	}
	if baseMs > minimum {
		return baseMs
	}
	return minimum
}

// NonStreamFloorMs 非流式触发下限（全策略统一）。
const NonStreamFloorMs = 10000

// ── 阈值注册表 ──

type sample struct {
	at time.Time
	ms int64
}

type windowKey struct {
	family Family
	stage  Stage
}

// Registry 按（模型家族 × 阶段）维护 15 分钟滚动成功样本窗口，
// 输出自适应竞速触发阈值。并发安全。
type Registry struct {
	mu      sync.Mutex
	windows map[windowKey]*sampleRing
	now     func() time.Time
}

// NewRegistry 创建注册表。
func NewRegistry() *Registry {
	return &Registry{
		windows: make(map[windowKey]*sampleRing),
		now:     time.Now,
	}
}

type sampleRing struct {
	samples []sample // 按 at 升序；容量超限时淘汰最旧
}

// Record 记录一次成功样本（流式出过首字即记，含竞速败者，消赢家偏差）。
func (r *Registry) Record(family Family, stage Stage, latencyMs int64) {
	if latencyMs <= 0 {
		return
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	ring := r.windows[windowKey{family, stage}]
	if ring == nil {
		ring = &sampleRing{}
		r.windows[windowKey{family, stage}] = ring
	}
	ring.prune(now)
	ring.samples = append(ring.samples, sample{at: now, ms: latencyMs})
	if len(ring.samples) > maxSamplesPerWindow {
		ring.samples = ring.samples[len(ring.samples)-maxSamplesPerWindow:]
	}
}

// ThresholdMs 返回该家族×阶段的竞速触发阈值（毫秒）：
// 样本 ≥ sampleFloor 时取 p90，否则回退 floorMs；结果 clamp 到 [floorMs, ceilingMs]。
func (r *Registry) ThresholdMs(family Family, stage Stage, floorMs, ceilingMs int) int {
	if ceilingMs < floorMs {
		ceilingMs = floorMs
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	ring := r.windows[windowKey{family, stage}]
	if ring == nil {
		return floorMs
	}
	ring.prune(now)
	if len(ring.samples) < sampleFloor {
		return floorMs
	}
	values := make([]int64, len(ring.samples))
	for i, s := range ring.samples {
		values[i] = s.ms
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	rank := int(math.Ceil(float64(percentileTarget) / 100 * float64(len(values))))
	if rank < 1 {
		rank = 1
	}
	p := int(values[rank-1])
	if p < floorMs {
		return floorMs
	}
	if p > ceilingMs {
		return ceilingMs
	}
	return p
}

// SampleCount 返回窗口内当前样本数（测试与诊断用）。
func (r *Registry) SampleCount(family Family, stage Stage) int {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	ring := r.windows[windowKey{family, stage}]
	if ring == nil {
		return 0
	}
	ring.prune(now)
	return len(ring.samples)
}

// prune 淘汰窗口外样本。调用方必须持锁。
func (ring *sampleRing) prune(now time.Time) {
	cutoff := now.Add(-windowRetention)
	idx := 0
	for idx < len(ring.samples) && ring.samples[idx].at.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		ring.samples = ring.samples[idx:]
	}
}
