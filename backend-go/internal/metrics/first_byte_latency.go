package metrics

import "math"

const latencyBucketCount = 2048

type latencyBucket struct {
	count    int64
	min, max float64
}

// 固定空间直方图：每个倍增区间分为 32 桶，覆盖 time.Duration 的纳秒范围。
// 桶内按观察到的最小/最大值插值，正常范围内分位数误差不超过约 2.2%。
type latencyHistogram struct {
	buckets [latencyBucketCount]latencyBucket
	count   int64
}

func (h *latencyHistogram) add(latencyMs float64) {
	if latencyMs < 0 || math.IsNaN(latencyMs) || math.IsInf(latencyMs, 0) {
		return
	}
	index := 0
	if latencyMs > 0 {
		index = int(math.Log2(latencyMs*1e6) * 32)
	}
	if index < 0 {
		index = 0
	} else if index >= len(h.buckets) {
		index = len(h.buckets) - 1
	}
	bucket := &h.buckets[index]
	if bucket.count == 0 {
		bucket.min, bucket.max = latencyMs, latencyMs
	} else {
		bucket.min = math.Min(bucket.min, latencyMs)
		bucket.max = math.Max(bucket.max, latencyMs)
	}
	bucket.count++
	h.count++
}

func (h *latencyHistogram) valueAtRank(rank int64) float64 {
	for _, bucket := range h.buckets {
		if rank >= bucket.count {
			rank -= bucket.count
			continue
		}
		if bucket.count == 1 {
			return bucket.min
		}
		return bucket.min + (bucket.max-bucket.min)*float64(rank)/float64(bucket.count-1)
	}
	return 0
}

func (h *latencyHistogram) percentile(percentile float64) float64 {
	position := percentile * float64(h.count-1)
	lower := int64(position)
	value := h.valueAtRank(lower)
	if fraction := position - float64(lower); fraction > 0 {
		value += (h.valueAtRank(lower+1) - value) * fraction
	}
	return value
}

func (h *latencyHistogram) summary() (int64, float64, float64) {
	if h.count == 0 {
		return 0, 0, 0
	}
	return h.count, h.percentile(0.50), h.percentile(0.95)
}

// 渠道与 Key 查询共用样本口径：仅纳入已结束且确实观测到的耗时。
type requestLatencyHistograms struct {
	firstByte, firstContent latencyHistogram
}

func (h *requestLatencyHistograms) add(record RequestRecord) {
	if record.pending {
		return
	}
	if record.FirstByteLatencyMs > 0 {
		h.firstByte.add(record.FirstByteLatencyMs)
	}
	if record.FirstContentLatencyMs != nil {
		h.firstContent.add(*record.FirstContentLatencyMs)
	}
}

func (h *requestLatencyHistograms) populate(stats *TimeWindowStats) {
	stats.FirstByteLatencySamples, stats.FirstByteLatencyP50Ms, stats.FirstByteLatencyP95Ms = h.firstByte.summary()
	stats.FirstContentLatencySamples, stats.FirstContentLatencyP50Ms, stats.FirstContentLatencyP95Ms = h.firstContent.summary()
}
