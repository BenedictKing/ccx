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
	sawSeverityTag         bool
	// sawPseudoToolCallMarker 流式输出文本中命中过伪工具调用标记
	// （MaybeCountPseudoToolCallMiss 的负反馈信号源）。
	sawPseudoToolCallMarker bool
	// sawRepetitionLoop 流式输出命中过无限重复检测（中止并强制熔断的事实标记）。
	sawRepetitionLoop bool
	maxStreamIdleMs   int64
	maxToolCallIdleMs int64
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

// HasFirstContent 是否已观测到首个有效内容（竞速触发复核用：主分支已出首字则不再派影子）。
func (o *StreamTimeoutObserver) HasFirstContent() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return !o.firstContentAt.IsZero()
}

// FirstContentMs 首个有效内容耗时（毫秒；未出现返回 0）。
func (o *StreamTimeoutObserver) FirstContentMs() int64 {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.firstContentAt.IsZero() {
		return 0
	}
	return o.firstContentAt.Sub(o.startedAt).Milliseconds()
}

// MarkStreamSeverityTag 标记本次流式输出中出现了安全分类格式标记（<severity）。
// 由各协议流处理器的文本增量扫描调用，供 MaybeLearnSeverityClassOutcome 读取。
func MarkStreamSeverityTag(c *gin.Context) {
	if observer := GetStreamTimeoutObserver(c); observer != nil {
		observer.MarkSeverityTag()
	}
}

// MarkSeverityTag 线程安全地记录"已见到 <severity 标记"事实。
func (o *StreamTimeoutObserver) MarkSeverityTag() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sawSeverityTag = true
}

// SawSeverityTag 返回本次流式输出中是否出现过 <severity 标记。
func (o *StreamTimeoutObserver) SawSeverityTag() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.sawSeverityTag
}

// MarkPseudoToolCallMarker 标记本次流式输出文本中命中了伪工具调用标记。
// 由各协议流处理器的文本扫描点调用，供 MaybeCountPseudoToolCallMiss 读取。
func MarkPseudoToolCallMarker(c *gin.Context) {
	if observer := GetStreamTimeoutObserver(c); observer != nil {
		observer.markPseudoToolCallMarker()
	}
}

// markPseudoToolCallMarker 线程安全地记录"已见到伪工具调用标记"事实。
func (o *StreamTimeoutObserver) markPseudoToolCallMarker() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sawPseudoToolCallMarker = true
}

// SawPseudoToolCallMarker 返回本次流式输出中是否命中过伪工具调用标记。
func (o *StreamTimeoutObserver) SawPseudoToolCallMarker() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.sawPseudoToolCallMarker
}

// MarkStreamRepetitionLoop 标记本次流式输出命中了无限重复检测。
// 由各协议流处理器的重复扫描点调用，供日志与后续学习扩展读取。
func MarkStreamRepetitionLoop(c *gin.Context) {
	if observer := GetStreamTimeoutObserver(c); observer != nil {
		observer.MarkRepetitionLoop()
	}
}

// MarkRepetitionLoop 线程安全地记录"已命中无限重复检测"事实。
func (o *StreamTimeoutObserver) MarkRepetitionLoop() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sawRepetitionLoop = true
}

// SawRepetitionLoop 返回本次流式输出是否命中过无限重复检测。
func (o *StreamTimeoutObserver) SawRepetitionLoop() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.sawRepetitionLoop
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
