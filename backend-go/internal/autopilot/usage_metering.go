package autopilot

import (
	"log"
	"sync"
	"time"
)

// ── 计量桶聚合器接口（设计 §3.2.4 数据来源 3：本地计量兜底）──

// BucketReader 抽象 TimeBucketStore 的只读查询能力，便于注入和测试。
type BucketReader interface {
	// GetBuckets 返回 endpointUID 最近 n 个桶（时间正序）。
	GetBuckets(endpointUID string, n int) []*TimeBucketMetrics
}

// ── UsageMeter 配置 ──

// UsageMeterConfig 可调参数。
type UsageMeterConfig struct {
	// Windows 定义要计算的滚动窗口列表。
	// 默认 ["5h", "day", "week", "month"]。
	Windows []string `json:"windows,omitempty"`
	// QuietLogs 是否静默日志。
	QuietLogs bool `json:"quietLogs"`
}

// defaultUsageMeterConfig 返回默认配置。
func defaultUsageMeterConfig() UsageMeterConfig {
	return UsageMeterConfig{
		Windows:   []string{"5h", "day", "week", "month"},
		QuietLogs: false,
	}
}

// ── 内部累计器 ──

// usageCounter 按 endpoint 维护轻量级请求/token 累计器。
// 当 TimeBucketStore 可用时优先使用桶数据，此计数器作为补充（精确到请求级增量）。
type usageCounter struct {
	// 按窗口维护累计值（独立滚动）
	windowUsed map[string]float64 // key: window name, value: 累计量
	// 最近一次请求时间
	lastRequestAt time.Time
}

// ── RecordRequest 上下文（由信号调用方提供，供日志排障用）──

// RequestRecordMeta 承载一次上游请求的可读上下文，仅用于日志展示，不参与计量逻辑。
type RequestRecordMeta struct {
	ChannelName string // 渠道名（upstream.Name），可能为空（如渠道已被删除）
	ServiceType string // 接口类型：messages/chat/responses/gemini/images/vectors
	StatusCode  int    // 上游响应状态码
	IsStream    bool   // 是否为流式请求
	Reason      string // 429 细分原因等（可能为空）
}

// ── UsageMeter 主体 ──

// UsageMeter 本地计量兜底，为 endpoint 生成 UsageWindow 数据。
// Source = local_metering。并发安全。
//
// 数据优先级：
//  1. 如注入了 BucketReader，优先从 TimeBucketStore 聚合历史桶数据
//  2. 内部轻量计数器补充实时增量（桶尚未写入的当次请求）
//
// Phase 1 shadow：仅生成用量视图，不参与调度。
type UsageMeter struct {
	mu       sync.RWMutex
	counters map[string]*usageCounter // key: endpointUID
	windows  []string
	buckets  BucketReader // 可选注入；nil 时纯用内部计数器
	nowFunc  func() time.Time
	quiet    bool
}

// NewUsageMeter 创建 UsageMeter。
// buckets 可传 nil（纯用内部计数器）或传入 *TimeBucketStore 实例。
func NewUsageMeter(cfg UsageMeterConfig, buckets BucketReader) *UsageMeter {
	if len(cfg.Windows) == 0 {
		cfg = defaultUsageMeterConfig()
	}
	return &UsageMeter{
		counters: make(map[string]*usageCounter),
		windows:  cfg.Windows,
		buckets:  buckets,
		nowFunc:  time.Now,
		quiet:    cfg.QuietLogs,
	}
}

// RecordRequest 记录一次请求到 endpoint 的计量器。
// tokens 为该次请求的 token 消耗；代理层当前无法精确获取上游 token 用量，
// 调用方恒传 0，计量逻辑仅按请求次数累加（不使用 tokens 值）。
// meta 为可读上下文（渠道名/接口类型/状态码等），仅用于日志展示。
// 并发安全。
func (m *UsageMeter) RecordRequest(endpointUID string, tokens int64, meta RequestRecordMeta) {
	if endpointUID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.counters[endpointUID]
	if !ok {
		c = &usageCounter{
			windowUsed: make(map[string]float64),
		}
		m.counters[endpointUID] = c
	}

	now := m.nowFunc()
	c.lastRequestAt = now

	// 按请求次数 +1
	for _, w := range m.windows {
		c.windowUsed[w] += 1
	}

	if !m.quiet {
		channelName := meta.ChannelName
		if channelName == "" {
			channelName = "-"
		}
		serviceType := meta.ServiceType
		if serviceType == "" {
			serviceType = "-"
		}
		reason := meta.Reason
		if reason == "" {
			reason = "-"
		}
		// tokens 当前恒为 0（未接入真实 usage），此处仍打印以保留字段兼容性，
		// 但不作为用量依据；请以 status/reason 判断请求结果。
		log.Printf("[UsageMeter-Record] endpoint=%s channel=%s service=%s status=%d stream=%t reason=%s tokens=%d",
			endpointUID, channelName, serviceType, meta.StatusCode, meta.IsStream, reason, tokens)
	}
}

// ComputeWindows 为指定 endpoint 生成当前的 UsageWindow 列表。
// 优先从 BucketReader 聚合，不足时回退到内部计数器。
// 并发安全。
func (m *UsageMeter) ComputeWindows(endpointUID string) []UsageWindow {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := m.nowFunc()
	result := make([]UsageWindow, 0, len(m.windows))

	for _, w := range m.windows {
		used := m.computeWindowUsed(endpointUID, w, now)
		resetAt := computeWindowResetAt(w, now)

		result = append(result, UsageWindow{
			Window:    w,
			Used:      used,
			Limit:     0, // 本地计量无法得知真实上限
			Unit:      "requests",
			ResetsAt:  resetAt,
			Source:    "local_metering",
			FetchedAt: now,
		})
	}

	return result
}

// computeWindowUsed 计算指定窗口内的已用量。
// 优先从 BucketReader 聚合；无 BucketReader 数据时回退到内部计数器。
func (m *UsageMeter) computeWindowUsed(endpointUID, window string, now time.Time) float64 {
	// 优先从桶聚合
	if m.buckets != nil {
		windowDuration := windowToDuration(window)
		if windowDuration > 0 {
			count := m.aggregateFromBuckets(endpointUID, windowDuration, now)
			if count >= 0 {
				return float64(count)
			}
		}
	}

	// 回退到内部计数器
	if c, ok := m.counters[endpointUID]; ok {
		return c.windowUsed[window]
	}
	return 0
}

// aggregateFromBuckets 从 BucketReader 聚合指定时间窗口内的请求总数。
// 返回 -1 表示无数据。
func (m *UsageMeter) aggregateFromBuckets(endpointUID string, windowDur time.Duration, now time.Time) int {
	// 最多取 7 天的桶（TimeBucketStore 上限）
	maxBuckets := int(windowDur / bucketSize)
	if maxBuckets <= 0 {
		return -1
	}
	// 多取一些以防时间对齐偏移
	buckets := m.buckets.GetBuckets(endpointUID, maxBuckets+4)
	if len(buckets) == 0 {
		return -1
	}

	cutoff := now.Add(-windowDur)
	total := 0
	for _, b := range buckets {
		if b.BucketStart.Before(cutoff) {
			continue
		}
		total += b.RequestCount
	}
	return total
}

// Clear 清除 endpoint 的内部计数器数据。
func (m *UsageMeter) Clear(endpointUID string) {
	m.mu.Lock()
	delete(m.counters, endpointUID)
	m.mu.Unlock()
}

// EndpointCount 返回当前跟踪的 endpoint 数量。
func (m *UsageMeter) EndpointCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.counters)
}

// ── 窗口时长与重置时间计算 ──

// windowToDuration 将窗口名称转换为 time.Duration。
func windowToDuration(window string) time.Duration {
	switch window {
	case "5h":
		return 5 * time.Hour
	case "day":
		return 24 * time.Hour
	case "week":
		return 7 * 24 * time.Hour
	case "month":
		return 30 * 24 * time.Hour
	default:
		return 0
	}
}

// computeWindowResetAt 计算窗口重置时间。
// 滚动窗口：从当前时刻起加窗口时长。
// 日/周/月窗口：对齐到自然周期结束（UTC 零点）。
func computeWindowResetAt(window string, now time.Time) time.Time {
	switch window {
	case "5h":
		// 滚动窗口：5 小时后重置
		return now.Add(5 * time.Hour)
	case "day":
		// 当前 UTC 日结束（明天 00:00 UTC）
		y, m, d := now.UTC().Date()
		return time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
	case "week":
		// 当前 UTC 周结束（下周一 00:00 UTC）
		wd := now.UTC().Weekday()
		daysToMonday := (8 - int(wd)) % 7
		if daysToMonday == 0 {
			daysToMonday = 7
		}
		y, m, d := now.UTC().Date()
		return time.Date(y, m, d+daysToMonday, 0, 0, 0, 0, time.UTC)
	case "month":
		// 当前 UTC 月结束（下月 1 号 00:00 UTC）
		y, m, _ := now.UTC().Date()
		return time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Time{}
	}
}
