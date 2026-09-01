package quota

import (
	"sync"
	"time"
)

// ── 懒重置饱和桶 ──
//
// 设计：每 (accountUID, windowKey) 一个桶，读取时判断 now >= resetsAtMs 即清零。
// 与 scheduler/recovery.go 解耦，桶本身零调度成本，无后台 cron。
// Fail-open：缺失条目 → 未饱和。所有时间输入可注入（nowMs 参数），测试时驱动时钟。
//
// 蓝本参考：OmniRoute src/lib/quota/accountBuckets.ts

// SaturationThresholdPct 是饱和度阈值（百分比，0-100）。
// 使用率 >= 此阈值时桶被标记为饱和。100 = 完全耗尽。
const SaturationThresholdPct = 100.0

// bucketEntry 是单个饱和桶的内存态。
type bucketEntry struct {
	saturated  bool
	resetsAtMs int64 // 0 表示重置时间未知
}

// BucketManager 管理所有饱和桶，线程安全。
type BucketManager struct {
	mu      sync.RWMutex
	buckets map[string]bucketEntry // key: accountUID::windowKey
}

// NewBucketManager 创建新的饱和桶管理器。
func NewBucketManager() *BucketManager {
	return &BucketManager{
		buckets: make(map[string]bucketEntry),
	}
}

func bucketKey(accountUID, windowKey string) string {
	return accountUID + "::" + windowKey
}

// IsSaturated 检查 (accountUID, windowKey) 是否当前处于饱和状态。
//
// 懒重置：如果 nowMs >= entry.resetsAtMs（且 resetsAtMs > 0），
// 则条目被清除并返回 false —— 无需后台扫描即可恢复 eligibility。
//
// Fail-open：缺失条目返回 false（未饱和）。
func (bm *BucketManager) IsSaturated(accountUID, windowKey string, nowMs int64) bool {
	if bm == nil || accountUID == "" || windowKey == "" {
		return false // fail-open
	}

	bm.mu.RLock()
	entry, ok := bm.buckets[bucketKey(accountUID, windowKey)]
	bm.mu.RUnlock()

	if !ok {
		return false // fail-open
	}

	// 懒重置：窗口已翻转 → 饱和状态已过期。
	if entry.resetsAtMs > 0 && nowMs >= entry.resetsAtMs {
		bm.mu.Lock()
		delete(bm.buckets, bucketKey(accountUID, windowKey))
		bm.mu.Unlock()
		return false
	}

	return entry.saturated
}

// RecordUsage 记录一次用量观测。
//
// 当 usedPct >= SATURATION_THRESHOLD_PCT 且窗口尚未翻转时，标记桶为饱和。
// 当观测低于阈值（或已过重置时间）时，清除已有条目以恢复 eligibility。
//
// resetAtMs 为 0 表示重置时间未知（懒重置不会主动触发，但条目会被新的观测覆盖）。
func (bm *BucketManager) RecordUsage(accountUID, windowKey string, usedPct float64, resetAtMs int64, nowMs int64) {
	if bm == nil || accountUID == "" || windowKey == "" {
		return
	}

	key := bucketKey(accountUID, windowKey)

	// 过期信号：窗口已重置 → 丢弃任何状态并退出。
	if resetAtMs > 0 && nowMs >= resetAtMs {
		bm.mu.Lock()
		delete(bm.buckets, key)
		bm.mu.Unlock()
		return
	}

	saturated := usedPct >= SaturationThresholdPct
	if !saturated {
		// 低于阈值 → 清除任何过期饱和标记，恢复 eligibility。
		bm.mu.Lock()
		delete(bm.buckets, key)
		bm.mu.Unlock()
		return
	}

	bm.mu.Lock()
	bm.buckets[key] = bucketEntry{saturated: true, resetsAtMs: resetAtMs}
	bm.mu.Unlock()
}

// UpdateFromValues 从一组配额 Value 更新相关桶的饱和状态。
// 每个有 limit 的维度都映射到一个 window key（如 "tokens"、"input_tokens"）。
func (bm *BucketManager) UpdateFromValues(accountUID string, values []Value, nowMs int64) {
	if bm == nil || accountUID == "" || len(values) == 0 {
		return
	}
	for _, v := range values {
		if v.Limit == nil || *v.Limit <= 0 {
			continue
		}
		var usedPct float64
		if v.Used != nil {
			usedPct = (*v.Used / *v.Limit) * 100.0
		} else if v.Remaining != nil {
			usedPct = (1.0 - *v.Remaining/(*v.Limit)) * 100.0
		} else {
			continue
		}
		bm.RecordUsage(accountUID, string(v.Dimension), usedPct, v.ResetAtMs, nowMs)
	}
}

// SaturatedAccounts 返回所有在指定 windowKey 下处于饱和状态的 accountUID 列表。
// 注意：此方法会触发懒重置检查。
func (bm *BucketManager) SaturatedAccounts(windowKey string, nowMs int64) []string {
	if bm == nil {
		return nil
	}
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	var result []string
	// 由于懒重置需要写锁，这里只做只读遍历，
	// 饱和但过期的条目在下一次 IsSaturated 调用时会被清除。
	for k, entry := range bm.buckets {
		// 检查 windowKey 后缀
		suffix := "::" + windowKey
		if len(k) <= len(suffix) || k[len(k)-len(suffix):] != suffix {
			continue
		}
		if entry.resetsAtMs > 0 && nowMs >= entry.resetsAtMs {
			continue // 已过期，不计入
		}
		if entry.saturated {
			result = append(result, k[:len(k)-len(suffix)])
		}
	}
	return result
}

// ── 共享 TTFB/拥挤度采集预留口 ──
//
// ObservationCollector 是共享采集管道的预留接口。
// TTFB 拥挤度（未来功能）与配额观测共用同一采集管道，
// 避免双份观测开销。当前仅定义接口，不实现具体逻辑。
type ObservationCollector interface {
	// RecordLatencyObservation 记录一次延迟观测（预留，TTFB 拥挤度功能使用）。
	RecordLatencyObservation(accountUID, windowKey string, latencyMs float64, now time.Time)
}
