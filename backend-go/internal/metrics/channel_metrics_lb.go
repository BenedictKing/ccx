package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// PR3 T1: Load-balancing 指标扩展。
// 该文件提供首 token 延迟（FTTL）、吞吐 TPS、活跃连接数三类数据，
// 以 channelKey（调用方自定义的渠道唯一标识，例如渠道 ID 或名称）为分桶维度，
// 通过 5 min 时间滑动窗口对 FTTL/TPS 做平均计算。
// PR3 T3 lb_metrics_provider.go 通过 GetFirstTokenLatencyAvg / GetTPSAvg /
// GetActiveConnections 三个 getter 读取，用于 LatencyAware / RateLimitAware
// 调度策略。
//
// 现有 RequestCount / ConsecutiveFailures / 历史记录等字段不受影响。

// lbWindowDuration 是 FTTL 与 TPS 滑动窗口的时长。
// 与 ConsecutiveFailures 衰减节奏（默认 30s 探测，10min 上限）保持同量级，
// 同时与 PRD 中“近 5 分钟均值”需求对齐。
const lbWindowDuration = 5 * time.Minute

// ftLatencyRecord 单次首 token 延迟样本。
type ftLatencyRecord struct {
	ts        time.Time
	latencyMs int64
}

// tpsRecord 单次请求的 TPS 样本（保存原始 token / duration，
// 计算时按时间窗口加权平均，避免短请求 TPS 抖动放大）。
type tpsRecord struct {
	ts          time.Time
	totalTokens int64
	durationMs  int64
}

// lbMetricsEntry 单个 channelKey 的负载均衡聚合指标。
// 字段访问规则：
//   - activeConn 通过 sync/atomic 直接读写，无需持锁；
//   - ftRecords / tpsRecords 通过 mu 保护（追加 + 头部裁剪窗口外样本）。
type lbMetricsEntry struct {
	mu         sync.Mutex
	ftRecords  []ftLatencyRecord
	tpsRecords []tpsRecord
	activeConn int64 // atomic
}

// pruneFTLocked 裁剪窗口外的 FTTL 样本。调用方需持有 e.mu。
func (e *lbMetricsEntry) pruneFTLocked(now time.Time) {
	cutoff := now.Add(-lbWindowDuration)
	idx := 0
	for idx < len(e.ftRecords) && e.ftRecords[idx].ts.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		e.ftRecords = append(e.ftRecords[:0], e.ftRecords[idx:]...)
	}
}

// pruneTPSLocked 裁剪窗口外的 TPS 样本。调用方需持有 e.mu。
func (e *lbMetricsEntry) pruneTPSLocked(now time.Time) {
	cutoff := now.Add(-lbWindowDuration)
	idx := 0
	for idx < len(e.tpsRecords) && e.tpsRecords[idx].ts.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		e.tpsRecords = append(e.tpsRecords[:0], e.tpsRecords[idx:]...)
	}
}

// getOrCreateLBEntry 获取（或创建）指定 channelKey 的 LB 指标桶。
// 使用 LoadOrStore 保证并发场景下只有一个 entry 实例。
func (m *MetricsManager) getOrCreateLBEntry(channelKey string) *lbMetricsEntry {
	if v, ok := m.lbMetrics.Load(channelKey); ok {
		return v.(*lbMetricsEntry)
	}
	entry := &lbMetricsEntry{}
	actual, _ := m.lbMetrics.LoadOrStore(channelKey, entry)
	return actual.(*lbMetricsEntry)
}

// RecordFirstToken 记录一次首 token 延迟（毫秒）。
// 调用时机：流式响应收到第一个 SSE event 时（或非流式响应整体完成时同义代用）。
// 参数：
//   - channelKey: 渠道唯一标识，调用方自行生成并保持稳定。
//   - latencyMs:  从请求发出到首 token 收到的耗时（毫秒），<=0 直接忽略。
func (m *MetricsManager) RecordFirstToken(channelKey string, latencyMs int64) {
	if channelKey == "" || latencyMs <= 0 {
		return
	}
	entry := m.getOrCreateLBEntry(channelKey)
	now := time.Now()
	entry.mu.Lock()
	entry.pruneFTLocked(now)
	entry.ftRecords = append(entry.ftRecords, ftLatencyRecord{ts: now, latencyMs: latencyMs})
	entry.mu.Unlock()
}

// RecordTPS 记录一次请求的 token / 耗时样本，用于平均 TPS 计算。
// 调用时机：请求结束时（成功或失败均可，建议仅成功调用）。
// 参数：
//   - channelKey:   渠道唯一标识。
//   - totalTokens:  本次请求 input + output 的总 token 数，<=0 忽略。
//   - durationMs:   请求总耗时（毫秒），<=0 忽略。
func (m *MetricsManager) RecordTPS(channelKey string, totalTokens int64, durationMs int64) {
	if channelKey == "" || totalTokens <= 0 || durationMs <= 0 {
		return
	}
	entry := m.getOrCreateLBEntry(channelKey)
	now := time.Now()
	entry.mu.Lock()
	entry.pruneTPSLocked(now)
	entry.tpsRecords = append(entry.tpsRecords, tpsRecord{
		ts:          now,
		totalTokens: totalTokens,
		durationMs:  durationMs,
	})
	entry.mu.Unlock()
}

// RecordActiveConnDelta 调整指定 channelKey 的活跃连接计数。
// 请求开始时调用 +1，请求结束（任何结局）时调用 -1。
// 内部使用 atomic.AddInt64，多 goroutine 并发安全；不会下溢到负数以下，
// 但也不主动 clamp（异常 -1/+1 不平衡时会暴露给观测方做排错）。
func (m *MetricsManager) RecordActiveConnDelta(channelKey string, delta int64) {
	if channelKey == "" || delta == 0 {
		return
	}
	entry := m.getOrCreateLBEntry(channelKey)
	atomic.AddInt64(&entry.activeConn, delta)
}

// GetFirstTokenLatencyAvg 返回最近 lbWindowDuration 窗口内的平均首 token 延迟（毫秒）。
// 若窗口内无样本返回 0。读路径不修改样本但会触发一次窗口裁剪。
func (m *MetricsManager) GetFirstTokenLatencyAvg(channelKey string) int64 {
	v, ok := m.lbMetrics.Load(channelKey)
	if !ok {
		return 0
	}
	entry := v.(*lbMetricsEntry)
	now := time.Now()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.pruneFTLocked(now)
	if len(entry.ftRecords) == 0 {
		return 0
	}
	var sum int64
	for i := range entry.ftRecords {
		sum += entry.ftRecords[i].latencyMs
	}
	return sum / int64(len(entry.ftRecords))
}

// GetTPSAvg 返回最近 lbWindowDuration 窗口内的平均吞吐（tokens/sec）。
// 计算方式：sum(totalTokens) / sum(durationMs)*1000。该口径对短请求和长请求
// 进行时间加权，避免单条短请求的极高 TPS 拉偏均值。
// 若窗口内无样本或 duration 总和为 0 返回 0。
func (m *MetricsManager) GetTPSAvg(channelKey string) float64 {
	v, ok := m.lbMetrics.Load(channelKey)
	if !ok {
		return 0
	}
	entry := v.(*lbMetricsEntry)
	now := time.Now()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.pruneTPSLocked(now)
	if len(entry.tpsRecords) == 0 {
		return 0
	}
	var tokens int64
	var durMs int64
	for i := range entry.tpsRecords {
		tokens += entry.tpsRecords[i].totalTokens
		durMs += entry.tpsRecords[i].durationMs
	}
	if durMs <= 0 {
		return 0
	}
	return float64(tokens) * 1000.0 / float64(durMs)
}

// GetActiveConnections 返回当前活跃连接数。
// 若 channelKey 从未被记录过返回 0。读路径完全 lock-free（atomic 读）。
func (m *MetricsManager) GetActiveConnections(channelKey string) int64 {
	v, ok := m.lbMetrics.Load(channelKey)
	if !ok {
		return 0
	}
	entry := v.(*lbMetricsEntry)
	return atomic.LoadInt64(&entry.activeConn)
}
