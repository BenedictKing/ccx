package metrics

import (
	"math"
	"sync"
	"time"
)

// ── per-key 滑窗自动权重 ──
//
// 定位：手控 weight 之外的软降权系数。二元隔离（持久限制 / 模型熔断）之前的
// 灰度层——健康度差的 Key 先被降权少调度，持续恶化仍由熔断接管硬隔离。
//
// 公式（蓝本 GPT-Load v2 calculateAutoWeight）：
//
//	factor = Laplace平滑成功率 × 0.5^连续失败次数
//	       = (success+1)/(total+2) × 0.5^consecutiveFailure
//
// 样本 < minWeightSamples 时返回 1.0（不干预）；factor 下限 minWeightFactor，
// 保证软降权永不替代熔断的"完全隔离"语义。
//
// 与 ModelCircuitTracker 的分工：模型级失败只进熔断（组合隔离），整把 Key 不可用
// 的失败（global 桶）与成功才进自动权重，避免"某模型挂了拖累同 Key 其他模型"。

const (
	// autoWeightBucketCount 滑窗桶数，每桶 1 分钟，窗口共 5 分钟。
	autoWeightBucketCount = 5
	// autoWeightBucketWidth 单桶时长。
	autoWeightBucketWidth = time.Minute
	// AutoWeightWindow 滑窗总时长。
	AutoWeightWindow = autoWeightBucketCount * autoWeightBucketWidth
	// minWeightSamples 低于该样本数不干预（返回 1.0）。
	minWeightSamples = 10
	// minWeightFactor 软降权下限，剩余调度概率仍有健康 Key 的 5%。
	minWeightFactor = 0.05
	// autoWeightIdleTTL 无事件条目的清理阈值（窗口结束后再留一个窗口宽度）。
	autoWeightIdleTTL = 2 * AutoWeightWindow
)

// KeyAutoWeightTracker 进程内 per-(channelUID, keyHash) 滑窗统计与权重计算。
// 零值不可用，请通过 NewKeyAutoWeightTracker 构造。
type KeyAutoWeightTracker struct {
	mu      sync.Mutex
	windows map[string]*keyAutoWeightWindow
}

type keyAutoWeightWindow struct {
	buckets            [autoWeightBucketCount]keyAutoWeightBucket
	consecutiveFailure uint64
	lastEventAt        time.Time
}

type keyAutoWeightBucket struct {
	minute  int64
	valid   bool
	success uint64
	failure uint64
}

// NewKeyAutoWeightTracker 创建自动权重跟踪器。
func NewKeyAutoWeightTracker() *KeyAutoWeightTracker {
	return &KeyAutoWeightTracker{windows: make(map[string]*keyAutoWeightWindow)}
}

func autoWeightEntryKey(channelUID, keyHash string) string {
	return channelUID + "|" + keyHash
}

// RecordSuccess 记录一次成功：清零连续失败并计入当前分钟桶。
func (t *KeyAutoWeightTracker) RecordSuccess(channelUID, keyHash string, now time.Time) {
	t.record(channelUID, keyHash, now, true)
}

// RecordFailure 记录一次整把 Key 级失败：累加连续失败并计入当前分钟桶。
func (t *KeyAutoWeightTracker) RecordFailure(channelUID, keyHash string, now time.Time) {
	t.record(channelUID, keyHash, now, false)
}

func (t *KeyAutoWeightTracker) record(channelUID, keyHash string, now time.Time, success bool) {
	if t == nil || channelUID == "" || keyHash == "" {
		return
	}
	minute := now.UnixNano() / int64(autoWeightBucketWidth)
	slot := autoWeightBucketSlot(minute)

	t.mu.Lock()
	defer t.mu.Unlock()

	window := t.windows[autoWeightEntryKey(channelUID, keyHash)]
	if window == nil {
		window = &keyAutoWeightWindow{}
		t.windows[autoWeightEntryKey(channelUID, keyHash)] = window
	}

	bucket := &window.buckets[slot]
	if !bucket.valid || minute > bucket.minute {
		*bucket = keyAutoWeightBucket{minute: minute, valid: true}
	} else if minute < bucket.minute {
		// 乱序事件（时钟回拨/并发竞态）丢弃，不回写旧桶
		return
	}

	if success {
		bucket.success++
		window.consecutiveFailure = 0
	} else {
		bucket.failure++
		window.consecutiveFailure++
	}
	window.lastEventAt = now
}

// WeightFactor 返回该 Key 当前软降权系数（0.05-1.0）。
// 样本不足时返回 1.0（不改变现有手控 weight 语义）。
func (t *KeyAutoWeightTracker) WeightFactor(channelUID, keyHash string, now time.Time) float64 {
	if t == nil {
		return 1.0
	}
	minute := now.UnixNano() / int64(autoWeightBucketWidth)

	t.mu.Lock()
	defer t.mu.Unlock()

	window := t.windows[autoWeightEntryKey(channelUID, keyHash)]
	if window == nil {
		return 1.0
	}

	var success, failure uint64
	firstMinute := minute - (autoWeightBucketCount - 1)
	for i := range window.buckets {
		bucket := &window.buckets[i]
		if !bucket.valid || bucket.minute < firstMinute || bucket.minute > minute {
			continue
		}
		success += bucket.success
		failure += bucket.failure
	}
	total := success + failure
	if total < minWeightSamples {
		return 1.0
	}

	rate := float64(success+1) / float64(total+2)
	factor := rate * math.Pow(0.5, float64(window.consecutiveFailure))
	if factor < minWeightFactor {
		return minWeightFactor
	}
	if factor > 1.0 {
		return 1.0
	}
	return factor
}

// Cleanup 清理长期无事件的条目，防止已删除渠道/Key 的窗口常驻内存。
func (t *KeyAutoWeightTracker) Cleanup(now time.Time) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	removed := 0
	for key, window := range t.windows {
		if now.Sub(window.lastEventAt) > autoWeightIdleTTL {
			delete(t.windows, key)
			removed++
		}
	}
	return removed
}

// TrackedCount 返回当前跟踪条目数（观测用）。
func (t *KeyAutoWeightTracker) TrackedCount() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.windows)
}

func autoWeightBucketSlot(minute int64) int {
	slot := minute % autoWeightBucketCount
	if slot < 0 {
		slot += autoWeightBucketCount
	}
	return int(slot)
}
