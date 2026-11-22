package metrics

import "math"

const firstByteLatencyBucketCount = 2048

type firstByteLatencyBucket struct {
	count    int64
	min, max float64
}

// 固定空间直方图：每个倍增区间分为 32 桶，覆盖 time.Duration 的纳秒范围。
// 桶内按观察到的最小/最大值插值，正常范围内分位数误差不超过约 2.2%。
type firstByteLatencyHistogram struct {
	buckets [firstByteLatencyBucketCount]firstByteLatencyBucket
	count   int64
}

func (h *firstByteLatencyHistogram) add(latencyMs float64) {
	if latencyMs <= 0 || math.IsNaN(latencyMs) || math.IsInf(latencyMs, 0) {
		return
	}
	index := int(math.Log2(latencyMs*1e6) * 32)
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

func (h *firstByteLatencyHistogram) valueAtRank(rank int64) float64 {
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

func (h *firstByteLatencyHistogram) percentile(percentile float64) float64 {
	position := percentile * float64(h.count-1)
	lower := int64(position)
	value := h.valueAtRank(lower)
	if fraction := position - float64(lower); fraction > 0 {
		value += (h.valueAtRank(lower+1) - value) * fraction
	}
	return value
}

func (h *firstByteLatencyHistogram) summary() (int64, float64, float64) {
	if h.count == 0 {
		return 0, 0, 0
	}
	return h.count, h.percentile(0.50), h.percentile(0.95)
}
