package common

import (
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

const streamTimeoutObservationKey = "streamTimeoutObservation"

// StreamTimeoutObserver 记录单次流式请求的真实空闲间隔，用于后续数据化校准超时参数。
type StreamTimeoutObserver struct {
	mu sync.Mutex

	store      *metrics.ChannelLogStore
	metricsKey string
	requestID  string
	startedAt  time.Time

	firstContentAt         time.Time
	lastStreamActivityAt   time.Time
	lastToolCallActivityAt time.Time
	toolCallPending        bool
	sawToolCall            bool
	maxStreamIdleMs        int64
	maxToolCallIdleMs      int64
}

func StartStreamTimeoutObservation(c *gin.Context, store *metrics.ChannelLogStore, metricsKey, requestID string, startedAt time.Time) {
	if c == nil || store == nil || metricsKey == "" || requestID == "" {
		return
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	c.Set(streamTimeoutObservationKey, &StreamTimeoutObserver{
		store:      store,
		metricsKey: metricsKey,
		requestID:  requestID,
		startedAt:  startedAt,
	})
}

func GetStreamTimeoutObserver(c *gin.Context) *StreamTimeoutObserver {
	if c == nil {
		return nil
	}
	value, ok := c.Get(streamTimeoutObservationKey)
	if !ok {
		return nil
	}
	observer, ok := value.(*StreamTimeoutObserver)
	if !ok {
		return nil
	}
	return observer
}

func MarkStreamFirstContent(c *gin.Context) {
	if observer := GetStreamTimeoutObserver(c); observer != nil {
		observer.MarkFirstContent(time.Now())
	}
}

func MarkStreamActivity(c *gin.Context) {
	if observer := GetStreamTimeoutObserver(c); observer != nil {
		observer.MarkStreamActivity(time.Now())
	}
}

func MarkStreamToolCallActivity(c *gin.Context) {
	if observer := GetStreamTimeoutObserver(c); observer != nil {
		observer.MarkToolCallActivity(time.Now())
	}
}

func MarkStreamToolCallComplete(c *gin.Context) {
	if observer := GetStreamTimeoutObserver(c); observer != nil {
		observer.MarkToolCallComplete(time.Now())
	}
}

func FinishStreamTimeoutObservation(c *gin.Context) {
	if observer := GetStreamTimeoutObserver(c); observer != nil {
		observer.Finish(time.Now())
	}
}

func (o *StreamTimeoutObserver) MarkFirstContent(now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.markFirstContentLocked(now)
	o.flushLocked()
}

func (o *StreamTimeoutObserver) MarkStreamActivity(now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.markStreamActivityLocked(now)
	o.flushLocked()
}

func (o *StreamTimeoutObserver) MarkToolCallActivity(now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.markToolCallActivityLocked(now)
	o.flushLocked()
}

func (o *StreamTimeoutObserver) MarkToolCallComplete(now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.markToolCallCompleteLocked(now)
	o.flushLocked()
}

func (o *StreamTimeoutObserver) Finish(now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.lastStreamActivityAt.IsZero() {
		o.maxStreamIdleMs = maxInt64(o.maxStreamIdleMs, now.Sub(o.lastStreamActivityAt).Milliseconds())
	}
	if o.toolCallPending && !o.lastToolCallActivityAt.IsZero() {
		o.maxToolCallIdleMs = maxInt64(o.maxToolCallIdleMs, now.Sub(o.lastToolCallActivityAt).Milliseconds())
	}
	o.flushLocked()
}

func (o *StreamTimeoutObserver) markFirstContentLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	if o.firstContentAt.IsZero() {
		o.firstContentAt = now
		o.lastStreamActivityAt = now
		return
	}
	o.markStreamActivityLocked(now)
}

func (o *StreamTimeoutObserver) markStreamActivityLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	if o.firstContentAt.IsZero() {
		o.markFirstContentLocked(now)
		return
	}
	if !o.lastStreamActivityAt.IsZero() {
		o.maxStreamIdleMs = maxInt64(o.maxStreamIdleMs, now.Sub(o.lastStreamActivityAt).Milliseconds())
	}
	o.lastStreamActivityAt = now
}

func (o *StreamTimeoutObserver) markToolCallActivityLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	o.markStreamActivityLocked(now)
	if !o.lastToolCallActivityAt.IsZero() {
		o.maxToolCallIdleMs = maxInt64(o.maxToolCallIdleMs, now.Sub(o.lastToolCallActivityAt).Milliseconds())
	}
	o.lastToolCallActivityAt = now
	o.toolCallPending = true
	// 流中出现过工具调用块的事实标记，供强制 tool_choice 负信号学习读取
	// （MaybeLearnForcedToolChoiceMiss）；仅 messages/responses 流式路径会触发。
	o.sawToolCall = true
}

// SawToolCall 返回本次流式中是否出现过任何工具调用块。
func (o *StreamTimeoutObserver) SawToolCall() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.sawToolCall
}

func (o *StreamTimeoutObserver) markToolCallCompleteLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	o.markStreamActivityLocked(now)
	if !o.lastToolCallActivityAt.IsZero() {
		o.maxToolCallIdleMs = maxInt64(o.maxToolCallIdleMs, now.Sub(o.lastToolCallActivityAt).Milliseconds())
	}
	o.lastToolCallActivityAt = time.Time{}
	o.toolCallPending = false
}

func (o *StreamTimeoutObserver) flushLocked() {
	if o.store == nil || o.metricsKey == "" || o.requestID == "" {
		return
	}
	firstContentLatencyMs := int64(0)
	if !o.firstContentAt.IsZero() {
		firstContentLatencyMs = o.firstContentAt.Sub(o.startedAt).Milliseconds()
	}
	o.store.Update(o.metricsKey, o.requestID, func(log *metrics.ChannelLog) {
		if firstContentLatencyMs > 0 {
			log.FirstContentLatencyMs = firstContentLatencyMs
		}
		if o.maxStreamIdleMs > 0 {
			log.MaxStreamIdleMs = o.maxStreamIdleMs
		}
		if o.maxToolCallIdleMs > 0 {
			log.MaxToolCallIdleMs = o.maxToolCallIdleMs
		}
	})
}

func maxInt64(a, b int64) int64 {
	if b > a {
		return b
	}
	return a
}
