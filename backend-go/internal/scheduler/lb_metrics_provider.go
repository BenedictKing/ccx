package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/loadbalance"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/session"
)

// LBMetricsProvider 把 ccx 现有的 (baseURL, apiKey, serviceType) tuple 模型
// 桥接到 loadbalance.ChannelMetricsProvider 接口（PR2 锁定）。
//
// 设计要点：
//   - axonhub LB 用 int channelID 表示渠道；ccx 没有原生 int channelID，
//     这里以 cfgManager.GetConfig() 中 kind 对应 upstreams slice 的索引作为 channelID。
//     调用方（scheduler）在构建 loadbalance.Channel 时把同一索引塞入 Channel.ID 字段，
//     这样 LB 排序回调本 provider 时可通过该索引反查 *config.UpstreamConfig。
//   - T1 的 metrics.MetricsManager 使用不透明字符串 channelKey；本 provider 与
//     T2 (RecordFirstToken/RecordTPS/RecordActiveConnDelta 调用点) 一致采用
//     `metrics.BuildLBChannelKey(string(kind), upstream.Name)`，确保写入端与
//     读取端 key 同一来源（详见 metrics/channel_key.go）。
//   - 当前 ccx 没有 P95、E2E latency、RateLimit、MaxConcurrent、OrderingWeight
//     这些维度，遵循 noop fallback 原则返回零值（详见各方法注释），等待后续 PR
//     补齐数据源。
//
// 每个 ChannelKind 需独立构造一个 LBMetricsProvider，因为 channelID 命名空间是
// 按 kind 隔离的（messages/responses/gemini/chat/images 各自一套 upstreams 列表）。
type LBMetricsProvider struct {
	mm   *metrics.MetricsManager
	cm   *config.ConfigManager
	aff  *session.TraceAffinityManager
	kind ChannelKind
}

// NewLBMetricsProvider 构造一个针对指定 kind 的 metrics provider。
// mm/cm 必须非空；aff 可选（为 nil 时 LastSuccessfulChannelByTrace 总返回 false）。
func NewLBMetricsProvider(
	mm *metrics.MetricsManager,
	cm *config.ConfigManager,
	aff *session.TraceAffinityManager,
	kind ChannelKind,
) *LBMetricsProvider {
	return &LBMetricsProvider{mm: mm, cm: cm, aff: aff, kind: kind}
}

// 编译期断言：保证 13 个方法的 ChannelMetricsProvider 接口契约不破。
var _ loadbalance.ChannelMetricsProvider = (*LBMetricsProvider)(nil)

// upstreamsForKind 提供 kind → upstreams slice 的统一映射。
func upstreamsForKind(cfg config.Config, kind ChannelKind) []config.UpstreamConfig {
	switch kind {
	case ChannelKindResponses:
		return cfg.ResponsesUpstream
	case ChannelKindGemini:
		return cfg.GeminiUpstream
	case ChannelKindChat:
		return cfg.ChatUpstream
	case ChannelKindImages:
		return cfg.ImagesUpstream
	default:
		return cfg.Upstream
	}
}

// resolveChannel 从 channelID（slice 索引）解析出 *UpstreamConfig + channelKey。
// 解析失败时返回 (nil, "")，调用方应回退为零值结果。
func (p *LBMetricsProvider) resolveChannel(channelID int) (*config.UpstreamConfig, string) {
	if p == nil || p.cm == nil {
		return nil, ""
	}
	cfg := p.cm.GetConfig()
	upstreams := upstreamsForKind(cfg, p.kind)
	if channelID < 0 || channelID >= len(upstreams) {
		return nil, ""
	}
	u := upstreams[channelID]
	return &u, metrics.BuildLBChannelKey(string(p.kind), u.Name)
}

// LastSuccessfulChannelByTrace 代理 TraceAffinityManager。
// 注意：scheduler 现有亲和性使用复合 key `string(kind) + ":" + userID`（见
// channel_scheduler.go SetTraceAffinity），这里采用相同 key 规则保持一致。
func (p *LBMetricsProvider) LastSuccessfulChannelByTrace(_ context.Context, traceID string) (int, bool) {
	if p == nil || p.aff == nil || traceID == "" {
		return 0, false
	}
	idx, ok := p.aff.GetPreferredChannel(string(p.kind) + ":" + traceID)
	if !ok || idx < 0 {
		return 0, false
	}
	return idx, true
}

// ConsecutiveFailures 返回某 channel 当前最大连续失败次数（多 key 取 max）。
// 数据源：metrics.MetricsManager.GetChannelAggregatedMetrics（已有）。
func (p *LBMetricsProvider) ConsecutiveFailures(channelID int) int {
	upstream, _ := p.resolveChannel(channelID)
	if upstream == nil || p.mm == nil {
		return 0
	}
	serviceType := NormalizedMetricsServiceType(p.kind, upstream.ServiceType)
	var maxConsec int64
	for _, baseURL := range upstream.GetAllBaseURLs() {
		ch := p.mm.GetChannelAggregatedMetrics(channelID, baseURL, upstream.APIKeys, serviceType)
		if ch != nil && ch.ConsecutiveFailures > maxConsec {
			maxConsec = ch.ConsecutiveFailures
		}
	}
	return int(maxConsec)
}

// LastFailureTime 返回某 channel 最近一次失败时间，零值表示无失败记录。
// 数据源：ChannelMetrics.LastFailureAt（多 baseURL 取最近一次）。
func (p *LBMetricsProvider) LastFailureTime(channelID int) time.Time {
	upstream, _ := p.resolveChannel(channelID)
	if upstream == nil || p.mm == nil {
		return time.Time{}
	}
	serviceType := NormalizedMetricsServiceType(p.kind, upstream.ServiceType)
	var latest time.Time
	for _, baseURL := range upstream.GetAllBaseURLs() {
		ch := p.mm.GetChannelAggregatedMetrics(channelID, baseURL, upstream.APIKeys, serviceType)
		if ch == nil || ch.LastFailureAt == nil {
			continue
		}
		if latest.IsZero() || ch.LastFailureAt.After(latest) {
			latest = *ch.LastFailureAt
		}
	}
	return latest
}

// RequestCount 返回最近 since 时间窗口内的请求数（用于加权 RR）。
// 数据源：metrics.MetricsManager.GetTimeWindowStatsForKey（按时间窗口统计）。
// since <= 0 时返回 0。
func (p *LBMetricsProvider) RequestCount(channelID int, since time.Duration) int {
	if since <= 0 {
		return 0
	}
	upstream, _ := p.resolveChannel(channelID)
	if upstream == nil || p.mm == nil {
		return 0
	}
	serviceType := NormalizedMetricsServiceType(p.kind, upstream.ServiceType)
	var total int64
	for _, baseURL := range upstream.GetAllBaseURLs() {
		for _, apiKey := range upstream.APIKeys {
			stats := p.mm.GetTimeWindowStatsForKey(baseURL, apiKey, serviceType, since)
			total += stats.RequestCount
		}
	}
	return int(total)
}

// OrderingWeight 返回某 channel 的加权轮询权重。
// 当前 ccx UpstreamConfig 没有 weight 字段，返回 0（fallback 等价于"不设权重"，
// 由 strategy_weightrr 自行处理零值；后续 PR 补字段时再升级）。
func (p *LBMetricsProvider) OrderingWeight(channelID int) int {
	_, _ = p.resolveChannel(channelID)
	return 0
}

// FTTLP95 返回首 token 时间。当前 T1 只提供 5 分钟均值（GetFirstTokenLatencyAvg），
// 暂以 Avg 作为 P95 近似值（model 维度 ccx 暂未拆分，忽略 model 参数）。
// 后续 PR 补 P95 后只需替换数据源。
func (p *LBMetricsProvider) FTTLP95(channelID int, _ string) time.Duration {
	_, key := p.resolveChannel(channelID)
	if key == "" || p.mm == nil {
		return 0
	}
	avgMs := p.mm.GetFirstTokenLatencyAvg(key)
	if avgMs <= 0 {
		return 0
	}
	return time.Duration(avgMs) * time.Millisecond
}

// TPSP50 返回 tokens/sec。当前 T1 只提供窗口均值（GetTPSAvg），
// 暂以 Avg 作为 P50 近似值；model 维度 ccx 暂未拆分，忽略 model 参数。
func (p *LBMetricsProvider) TPSP50(channelID int, _ string) float64 {
	_, key := p.resolveChannel(channelID)
	if key == "" || p.mm == nil {
		return 0
	}
	return p.mm.GetTPSAvg(key)
}

// E2ELatencyP95 返回端到端时延 P95。
// 当前 ccx 未单独采集 e2e latency（只有 FTTL 与 TPS），返回 0（fallback）。
// 后续 PR 接入 e2e latency 采样后只需替换数据源，调用方与本签名无需变化。
func (p *LBMetricsProvider) E2ELatencyP95(channelID int, _ string) time.Duration {
	_, _ = p.resolveChannel(channelID)
	return 0
}

// RateLimitState 返回限流状态。当前 ccx 未解析上游 rate-limit header，
// 一律返回零值结构体（Limited=false / QuotaRemaining=-1 表示未知）。
// strategy_ratelimit 在零值下应当退化为不打分（详见 PR2 注释）。
func (p *LBMetricsProvider) RateLimitState(channelID int) loadbalance.RateLimitState {
	_, _ = p.resolveChannel(channelID)
	return loadbalance.RateLimitState{
		Limited:        false,
		CooldownUntil:  time.Time{},
		QuotaRemaining: -1,
		QuotaTotal:     -1,
	}
}

// ActiveConnections 返回某 channel 当前并发连接数。
// 数据源：T1 的 metrics.MetricsManager.GetActiveConnections（atomic 计数）。
func (p *LBMetricsProvider) ActiveConnections(channelID int) int {
	_, key := p.resolveChannel(channelID)
	if key == "" || p.mm == nil {
		return 0
	}
	return int(p.mm.GetActiveConnections(key))
}

// MaxConcurrent 返回某 channel 的并发上限。
// 当前 UpstreamConfig 没有 maxConcurrent 字段，返回 0（接口契约：0=不限）。
func (p *LBMetricsProvider) MaxConcurrent(channelID int) int {
	_, _ = p.resolveChannel(channelID)
	return 0
}

// IsPromotionActive 返回某 channel 是否处于促销期。
// 数据源：config.IsChannelInPromotion（已有）。
func (p *LBMetricsProvider) IsPromotionActive(channelID int) bool {
	upstream, _ := p.resolveChannel(channelID)
	if upstream == nil {
		return false
	}
	active := config.IsChannelInPromotion(upstream)
	if active {
		log.Printf("[Scheduler-LB] 渠道 [%d] %s 处于促销期", channelID, upstream.Name)
	}
	return active
}
