// Package common 提供 handlers 模块的公共功能
package common

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ErrEmptyStreamResponse 上游返回 HTTP 200 但流式响应内容为空或几乎为空
// 空响应定义：OutputTokens == 0 或 OutputTokens == 1 且内容仅为 "{"
var ErrEmptyStreamResponse = errors.New("upstream returned empty stream response")

// ErrInvalidResponseBody 上游返回 HTTP 200 但响应体不是合法 JSON（如返回 HTML 错误页面）
// Header 未发送，可安全 failover 到下一个 Key/BaseURL/渠道
var ErrInvalidResponseBody = errors.New("upstream returned invalid response body")

// ErrBlacklistKey 上游在 SSE 流中返回了应拉黑 Key 的错误（认证/余额）
// Header 未发送，可安全 failover 到下一个 Key/BaseURL/渠道
type ErrBlacklistKey struct {
	Reason  string // "authentication_error" / "permission_error" / "insufficient_balance"
	Message string
}

func (e *ErrBlacklistKey) Error() string {
	return fmt.Sprintf("upstream stream error requires key blacklist: %s", e.Reason)
}

// ErrCooldownKey 上游在 SSE 流中返回了应冷却 Key 的错误（限流/临时故障）
// Header 未发送，可安全 failover 到下一个 Key/BaseURL/渠道
type ErrCooldownKey struct {
	Reason   string
	Message  string
	Duration time.Duration
}

func (e *ErrCooldownKey) Error() string {
	return fmt.Sprintf("upstream stream error requires key cooldown: %s", e.Reason)
}

// StreamPreflightResult 流式预检结果
type StreamPreflightResult struct {
	BufferedEvents    []string
	IsEmpty           bool
	HasError          bool
	Error             error
	InterceptAction   string
	InterceptReason   string
	InterceptMessage  string
	InterceptDuration time.Duration
	Diagnostic        string
	UnknownEventType  string
}

const (
	rawStreamEventChannelSize        = 100
	maxRawStreamEventBytes           = 1024 * 1024
	maxRawStreamPreflightBufferBytes = 1024 * 1024
	rawStreamReaderBufferSize        = 32 * 1024
)

// lbMetricsManagerContextKey 是 LB 指标管理器在 gin.Context 中的存放键。
// PR3 T2 引入：调用方（如 upstream_failover）在进入 stream 处理前调用
// SetLBMetricsManager 注入；stream.go 内部按需读取并触发首 token 延迟记录。
const lbMetricsManagerContextKey = "lbMetricsManager"

// lbAPITypeContextKey 是 LB 指标 channelKey 所需 kind 在 gin.Context 中的存放键。
// PR3 T2/T3 对齐：handler 在调用 stream 处理前通过 SetLBAPIType 注入
// "messages" / "chat" / "responses" / "gemini" / "images" 之一，供 ChannelKeyForLB
// 与 scheduler.LBMetricsProvider 共用同一个 channelKey 命名空间。未注入时
// 退化为空串，等价于不带 kind 前缀（T8 wiring 时再补齐）。
const lbAPITypeContextKey = "lbApiType"

// SetLBMetricsManager 注入 LB 指标管理器到 gin.Context。
// 不传或传 nil 时，stream 内部首 token 延迟跟踪退化为 no-op，不影响其他流程。
func SetLBMetricsManager(c *gin.Context, manager *metrics.MetricsManager) {
	if c == nil || manager == nil {
		return
	}
	c.Set(lbMetricsManagerContextKey, manager)
}

// lbMetricsManagerFromContext 读取已注入的 LB 指标管理器，未注入时返回 nil。
func lbMetricsManagerFromContext(c *gin.Context) *metrics.MetricsManager {
	if c == nil {
		return nil
	}
	v, ok := c.Get(lbMetricsManagerContextKey)
	if !ok {
		return nil
	}
	m, _ := v.(*metrics.MetricsManager)
	return m
}

// SetLBAPIType 在 gin.Context 上注入当前请求所属的 LB kind（"messages" / "chat" /
// "responses" / "gemini" / "images"），供 stream 入口构造 channelKey 时取用。
// 空串等价于不注入。
func SetLBAPIType(c *gin.Context, apiType string) {
	if c == nil || apiType == "" {
		return
	}
	c.Set(lbAPITypeContextKey, apiType)
}

// lbAPITypeFromContext 读取已注入的 LB kind，未注入时返回空串。
func lbAPITypeFromContext(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, ok := c.Get(lbAPITypeContextKey)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// ChannelKeyForLB 返回 upstream 在 LB 指标维度的统一 channelKey。
// PR3 T2 / T3 共用此函数，保证 RecordFirstToken 写入与 scheduler.LBMetricsProvider
// 读取使用同一键；内部代理到 metrics.BuildLBChannelKey（single source of truth）。
//
// 行为：
//   - upstream == nil → ""（保留 T2 nil-safe 语义）；
//   - upstream.Name 非空 → metrics.BuildLBChannelKey(kind, upstream.Name)；
//   - upstream.Name 为空但 BaseURL 非空 → fallback 到
//     metrics.BuildLBChannelKey(kind, upstream.BaseURL)；
//   - 全空 → "" （key 无意义，调用方应跳过记录）。
func ChannelKeyForLB(kind string, upstream *config.UpstreamConfig) string {
	if upstream == nil {
		return ""
	}
	if upstream.Name != "" {
		return metrics.BuildLBChannelKey(kind, upstream.Name)
	}
	if upstream.BaseURL != "" {
		return metrics.BuildLBChannelKey(kind, upstream.BaseURL)
	}
	return ""
}

// firstTokenTracker 记录单个 SSE 流的首 token 延迟，确保只触发一次。
// 字段访问：fired 通过 atomic.Bool 保证多 goroutine 竞争下只会调用一次
// MetricsManager.RecordFirstToken。
type firstTokenTracker struct {
	manager      *metrics.MetricsManager
	channelKey   string
	requestStart time.Time
	fired        atomic.Bool
}

// newFirstTokenTracker 在 stream 入口构造跟踪器。
// 缺少 MetricsManager 或 channelKey 时返回 nil；MarkFirstEvent 对 nil 接收者安全。
// kind 通过 gin.Context（SetLBAPIType 注入）获取；未注入时退化为不带前缀的 channelKey。
func newFirstTokenTracker(c *gin.Context, upstream *config.UpstreamConfig, requestStart time.Time) *firstTokenTracker {
	manager := lbMetricsManagerFromContext(c)
	if manager == nil {
		return nil
	}
	channelKey := ChannelKeyForLB(lbAPITypeFromContext(c), upstream)
	if channelKey == "" {
		return nil
	}
	if requestStart.IsZero() {
		requestStart = time.Now()
	}
	return &firstTokenTracker{
		manager:      manager,
		channelKey:   channelKey,
		requestStart: requestStart,
	}
}

// MarkFirstEvent 在收到 / 写出首个 SSE event 时调用。
// 通过 atomic CompareAndSwap 保证生命周期内仅触发一次 RecordFirstToken；
// 后续调用为 no-op。MetricsManager.RecordFirstToken 自身忽略 latencyMs<=0，
// 但此处也提前剔除以减少无效调用。
func (t *firstTokenTracker) MarkFirstEvent() {
	if t == nil || t.manager == nil || t.channelKey == "" {
		return
	}
	if !t.fired.CompareAndSwap(false, true) {
		return
	}
	latencyMs := time.Since(t.requestStart).Milliseconds()
	if latencyMs <= 0 {
		return
	}
	t.manager.RecordFirstToken(t.channelKey, latencyMs)
}

type rawStreamEvent struct {
	Bytes []byte
	Text  string
}

type rawStreamPreflightResult struct {
	StreamPreflightResult
	BufferedRawEvents [][]byte
}

// PreflightStreamEvents 在发送 HTTP Header 之前预检流响应是否可继续
func PreflightStreamEvents(eventChan <-chan string, errChan <-chan error, upstream *config.UpstreamConfig) *StreamPreflightResult {
	result := &StreamPreflightResult{}
	var textBuf bytes.Buffer
	hasNonTextContent := false // tool_use / thinking 等非文本 content block
	seenEvent := false
	seenMessageStop := false
	seenUsageOnlyEvent := false
	seenUnknownDataType := false
	unknownEventType := ""
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				// eventChan 关闭：流结束
				if hasNonTextContent {
					return result // 有非文本内容，视为非空
				}
				result.IsEmpty = isEmptyContent(textBuf.String())
				result.UnknownEventType = unknownEventType
				result.Diagnostic = buildClaudePreflightDiagnostic(seenEvent, seenMessageStop, seenUsageOnlyEvent, seenUnknownDataType, unknownEventType, textBuf.String(), result.BufferedEvents)
				return result
			}
			seenEvent = true
			result.BufferedEvents = append(result.BufferedEvents, event)

			// 检测 SSE error 事件中的拉黑条件（认证/余额错误）
			if result.InterceptAction == "" {
				if action, reason, duration, msg := DetectStreamFailoverAction(event, upstream); action != "" {
					result.InterceptAction = action
					result.InterceptReason = reason
					result.InterceptDuration = duration
					result.InterceptMessage = msg
				}
			}

			// 检测非文本 content block（tool_use / thinking）
			if !hasNonTextContent && hasNonTextContentBlock(event) {
				return result // 有效内容，立即放行
			}

			seenMessageStop = seenMessageStop || IsMessageStopEvent(event)
			if isUsageOnlySSEEvent(event) {
				seenUsageOnlyEvent = true
			}
			if t, ok := firstUnknownSSEDataType(event); ok {
				seenUnknownDataType = true
				if unknownEventType == "" {
					unknownEventType = t
				}
			}

			// 提取文本内容
			ExtractTextFromEvent(event, &textBuf)

			// 检查是否有有效内容（非空且不是仅 "{"）
			if !isEmptyContent(textBuf.String()) {
				// 非空响应，放行
				return result
			}

			// 检查是否为 message_stop 事件（流正常结束）
			if IsMessageStopEvent(event) {
				if hasNonTextContent {
					return result
				}
				result.IsEmpty = isEmptyContent(textBuf.String())
				result.UnknownEventType = unknownEventType
				result.Diagnostic = buildClaudePreflightDiagnostic(seenEvent, true, seenUsageOnlyEvent, seenUnknownDataType, unknownEventType, textBuf.String(), result.BufferedEvents)
				return result
			}

		case err, ok := <-errChan:
			if !ok {
				// errChan 关闭：置为 nil 防止 select 忙等自旋
				errChan = nil
				continue
			}
			if err != nil {
				result.HasError = true
				result.Error = err
				return result
			}

		case <-timeout.C:
			// 超时：保守放行
			return result
		}
	}
}

func preflightRawStreamEvents(eventChan <-chan rawStreamEvent, errChan <-chan error, upstream *config.UpstreamConfig) *rawStreamPreflightResult {
	result := &rawStreamPreflightResult{}
	var textBuf bytes.Buffer
	hasNonTextContent := false
	seenEvent := false
	seenMessageStop := false
	seenUsageOnlyEvent := false
	seenUnknownDataType := false
	unknownEventType := ""
	bufferedBytes := 0
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				if hasNonTextContent {
					return result
				}
				result.IsEmpty = isEmptyContent(textBuf.String())
				result.UnknownEventType = unknownEventType
				result.Diagnostic = buildClaudePreflightDiagnostic(seenEvent, seenMessageStop, seenUsageOnlyEvent, seenUnknownDataType, unknownEventType, textBuf.String(), result.BufferedEvents)
				return result
			}

			seenEvent = true
			result.BufferedEvents = append(result.BufferedEvents, event.Text)
			result.BufferedRawEvents = append(result.BufferedRawEvents, append([]byte(nil), event.Bytes...))
			bufferedBytes += len(event.Bytes)
			if bufferedBytes > maxRawStreamPreflightBufferBytes {
				result.HasError = true
				result.Error = fmt.Errorf("%w: raw stream preflight exceeded %d bytes", ErrInvalidResponseBody, maxRawStreamPreflightBufferBytes)
				return result
			}

			if result.InterceptAction == "" {
				if action, reason, duration, msg := DetectStreamFailoverAction(event.Text, upstream); action != "" {
					result.InterceptAction = action
					result.InterceptReason = reason
					result.InterceptDuration = duration
					result.InterceptMessage = msg
					return result
				}
			}

			if !hasNonTextContent && hasNonTextContentBlock(event.Text) {
				return result
			}

			seenMessageStop = seenMessageStop || IsMessageStopEvent(event.Text)
			if isUsageOnlySSEEvent(event.Text) {
				seenUsageOnlyEvent = true
			}
			if t, ok := firstUnknownSSEDataType(event.Text); ok {
				seenUnknownDataType = true
				if unknownEventType == "" {
					unknownEventType = t
				}
			}

			ExtractTextFromEvent(event.Text, &textBuf)
			if !isEmptyContent(textBuf.String()) {
				return result
			}

			if IsMessageStopEvent(event.Text) {
				if hasNonTextContent {
					return result
				}
				result.IsEmpty = isEmptyContent(textBuf.String())
				result.UnknownEventType = unknownEventType
				result.Diagnostic = buildClaudePreflightDiagnostic(seenEvent, true, seenUsageOnlyEvent, seenUnknownDataType, unknownEventType, textBuf.String(), result.BufferedEvents)
				return result
			}

		case err, ok := <-errChan:
			if !ok {
				errChan = nil
				continue
			}
			if err != nil {
				result.HasError = true
				result.Error = err
				return result
			}

		case <-timeout.C:
			return result
		}
	}
}

func buildClaudePreflightDiagnostic(seenEvent, seenMessageStop, seenUsageOnlyEvent, seenUnknownDataType bool, unknownEventType string, text string, events []string) string {
	switch {
	case !seenEvent:
		return "未收到任何 SSE 事件"
	case seenUsageOnlyEvent && IsEffectivelyEmptyStreamText(text):
		return "仅收到 usage/计数类事件，没有文本或语义内容"
	case seenUnknownDataType && IsEffectivelyEmptyStreamText(text):
		if unknownEventType != "" {
			return "收到了未识别的 SSE data.type=" + unknownEventType + "，但没有文本或语义内容"
		}
		return "收到了未识别的 SSE data.type，但没有文本或语义内容"
	case seenMessageStop && IsEffectivelyEmptyStreamText(text):
		return "流正常结束(message_stop)，但未检测到文本或语义内容"
	default:
		return "检测到空流，但未匹配到明确类别"
	}
}

func isUsageOnlySSEEvent(event string) bool {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		if usage, ok := data["usage"].(map[string]interface{}); ok && len(usage) > 0 {
			if _, hasDelta := data["delta"]; !hasDelta && data["type"] != "message_start" {
				return true
			}
		}
	}
	return false
}

func firstUnknownSSEDataType(event string) (string, bool) {
	knownTypes := map[string]struct{}{
		"message_start": {}, "message_delta": {}, "message_stop": {}, "content_block_start": {}, "content_block_delta": {}, "content_block_stop": {}, "ping": {}, "error": {},
	}
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		if t, _ := data["type"].(string); t != "" {
			if _, ok := knownTypes[t]; !ok {
				return t, true
			}
		}
	}
	return "", false
}

// isEmptyContent 判断流式响应的累积文本是否为空内容
func isEmptyContent(text string) bool {
	return IsEffectivelyEmptyStreamText(text)
}

// IsEffectivelyEmptyStreamText 判断流式响应文本是否仍可视为“空”
func IsEffectivelyEmptyStreamText(text string) bool {
	return text == "" || strings.TrimSpace(text) == "{"
}

func extractSSEJSONLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	jsonStr := strings.TrimPrefix(line, "data:")
	return strings.TrimPrefix(jsonStr, " "), true
}

// hasNonTextContentBlock 检测 SSE 事件是否包含非文本 content block（tool_use / thinking）
// 这些 content block 不产生 delta.text，但属于有效响应内容
func hasNonTextContentBlock(event string) bool {
	return HasClaudeSemanticContent(event)
}

// HasClaudeSemanticContent 判断 Claude/Messages 风格 SSE 是否包含有效语义内容
func HasClaudeSemanticContent(event string) bool {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// content_block_start 事件中检查 content_block.type
		if cb, ok := data["content_block"].(map[string]interface{}); ok {
			if cbType, ok := cb["type"].(string); ok {
				switch cbType {
				case "text", "":
				default:
					return true
				}
			}
		}

		if delta, ok := data["delta"].(map[string]interface{}); ok {
			if deltaType, _ := delta["type"].(string); deltaType == "input_json_delta" {
				return true
			}
			if stopReason, _ := delta["stop_reason"].(string); stopReason == "tool_use" || stopReason == "server_tool_use" {
				return true
			}
		}
	}
	return false
}

func responseItemCarriesSemanticContent(item map[string]interface{}) bool {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "function_call", "reasoning":
		return true
	}
	return strings.HasSuffix(itemType, "_call")
}

// HasResponsesSemanticContent 判断 Responses 风格 SSE 是否包含有效语义内容
func HasResponsesSemanticContent(event string) bool {
	lines := strings.Split(event, "\n")
	for _, line := range lines {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		switch data["type"] {
		case "response.function_call_arguments.delta", "response.function_call_arguments.done",
			"response.reasoning_summary_part.added", "response.reasoning_summary_part.done",
			"response.reasoning_summary_text.done":
			return true
		case "response.output_item.added", "response.output_item.done":
			item, _ := data["item"].(map[string]interface{})
			if responseItemCarriesSemanticContent(item) {
				return true
			}
		case "response.completed":
			if response, ok := data["response"].(map[string]interface{}); ok {
				if output, ok := response["output"].([]interface{}); ok {
					for _, item := range output {
						if itemMap, ok := item.(map[string]interface{}); ok && responseItemCarriesSemanticContent(itemMap) {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// drainChannels 排空 eventChan 和 errChan，防止 provider goroutine 泄漏
// 使用超时保护，避免在 channel 未关闭时永久阻塞
func drainChannels(eventChan <-chan string, errChan <-chan error) {
	go func() {
		timeout := time.After(60 * time.Second)
		for {
			select {
			case _, ok := <-eventChan:
				if !ok {
					return
				}
			case <-timeout:
				return
			}
		}
	}()
	go func() {
		timeout := time.After(60 * time.Second)
		for {
			select {
			case _, ok := <-errChan:
				if !ok {
					return
				}
			case <-timeout:
				return
			}
		}
	}()
}

func drainRawStreamChannels(eventChan <-chan rawStreamEvent, errChan <-chan error) {
	go func() {
		timeout := time.After(60 * time.Second)
		for {
			select {
			case _, ok := <-eventChan:
				if !ok {
					return
				}
			case <-timeout:
				return
			}
		}
	}()
	go func() {
		timeout := time.After(60 * time.Second)
		for {
			select {
			case _, ok := <-errChan:
				if !ok {
					return
				}
			case <-timeout:
				return
			}
		}
	}()
}

func closeReadCloserOnCancel(ctx context.Context, body io.Closer) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = body.Close()
		case <-done:
		}
	}()
	return func() {
		close(done)
	}
}

func startRawStreamFanout(ctx context.Context, body io.ReadCloser) (<-chan rawStreamEvent, <-chan error, <-chan struct{}) {
	eventChan := make(chan rawStreamEvent, rawStreamEventChannelSize)
	errChan := make(chan error, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(eventChan)
		defer close(errChan)
		defer closeReadCloserOnCancel(ctx, body)()
		defer func() { _ = body.Close() }()

		reader := bufio.NewReaderSize(body, rawStreamReaderBufferSize)
		var eventBytes []byte

		sendEvent := func() bool {
			if len(eventBytes) == 0 {
				return true
			}
			event := rawStreamEvent{
				Bytes: append([]byte(nil), eventBytes...),
				Text:  string(eventBytes),
			}
			eventBytes = eventBytes[:0]
			select {
			case <-ctx.Done():
				return false
			case eventChan <- event:
				return true
			}
		}

		sendError := func(err error) {
			select {
			case <-ctx.Done():
			case errChan <- err:
			}
		}

		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				eventBytes = append(eventBytes, line...)
				if len(eventBytes) > maxRawStreamEventBytes {
					sendError(fmt.Errorf("%w: raw stream event exceeded %d bytes", ErrInvalidResponseBody, maxRawStreamEventBytes))
					return
				}
				if isRawSSEBlankLine(line) && !sendEvent() {
					return
				}
			}

			if err != nil {
				if errors.Is(err, io.EOF) {
					_ = sendEvent()
					return
				}
				if ctx.Err() != nil {
					return
				}
				sendError(err)
				return
			}

			if ctx.Err() != nil {
				return
			}
		}
	}()

	return eventChan, errChan, done
}

func isRawSSEBlankLine(line []byte) bool {
	return bytes.Equal(line, []byte("\n")) || bytes.Equal(line, []byte("\r\n"))
}

func cleanupRawStreamFanout(cancel context.CancelFunc, eventChan <-chan rawStreamEvent, errChan <-chan error, done <-chan struct{}) {
	cancel()
	drainRawStreamChannels(eventChan, errChan)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("[Messages-Stream] raw passthrough fan-out cleanup timed out")
	}
}

// StreamContext 流处理上下文
type StreamContext struct {
	LogBuffer            bytes.Buffer
	OutputTextBuffer     bytes.Buffer
	Synthesizer          *utils.StreamSynthesizer
	LoggingEnabled       bool
	ClientGone           bool
	HasUsage             bool
	HasMessageDeltaUsage bool
	NeedTokenPatch       bool
	// 累积的 token 统计
	CollectedUsage CollectedUsageData
	// 用于日志的"续写前缀"（不参与真实转发，只影响 Stream-Synth 输出可读性）
	LogPrefillText string
	// SSE 事件调试追踪
	EventCount        int            // 事件总数
	ContentBlockCount int            // content block 计数
	ContentBlockTypes map[int]string // 每个 block 的类型
	// 低质量渠道处理
	RequestModel string // 请求中的 model（用于一致性检查）
	LowQuality   bool   // 是否为低质量渠道
	// 隐式缓存推断
	MessageStartInputTokens int // message_start 事件中的 input_tokens（用于推断隐式缓存）
}

// CollectedUsageData 从流事件中收集的 usage 数据
type CollectedUsageData struct {
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	// 缓存 TTL 细分
	CacheCreation5mInputTokens int
	CacheCreation1hInputTokens int
	CacheTTL                   string // "5m" | "1h" | "mixed"
}

// NewStreamContext 创建流处理上下文
func NewStreamContext(envCfg *config.EnvConfig) *StreamContext {
	ctx := &StreamContext{
		LoggingEnabled:    envCfg.IsDevelopment() && envCfg.EnableResponseLogs,
		ContentBlockTypes: make(map[int]string),
	}
	if ctx.LoggingEnabled {
		ctx.Synthesizer = utils.NewStreamSynthesizer("claude")
	}
	return ctx
}

// SetupStreamHeaders 设置流式响应头
func SetupStreamHeaders(c *gin.Context, resp *http.Response) {
	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(200)
}

// ProcessStreamEvents 处理流事件循环
// 返回值: error 表示流处理过程中是否发生错误（用于调用方决定是否记录失败指标）
func ProcessStreamEvents(
	c *gin.Context,
	w gin.ResponseWriter,
	flusher http.Flusher,
	eventChan <-chan string,
	errChan <-chan error,
	ctx *StreamContext,
	envCfg *config.EnvConfig,
	startTime time.Time,
	requestBody []byte,
) (*types.Usage, error) {
	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				usage := logStreamCompletion(ctx, envCfg, startTime)
				return usage, nil
			}
			ProcessStreamEvent(c, w, flusher, event, ctx, envCfg, requestBody)

		case err, ok := <-errChan:
			if !ok {
				continue
			}
			if err != nil {
				log.Printf("[Messages-Stream] 错误: 流式传输错误: %v", err)
				logPartialResponse(ctx, envCfg)

				// 向客户端发送错误事件（如果连接仍然有效）
				if !ctx.ClientGone {
					errorEvent := BuildStreamErrorEvent(err)
					if _, writeErr := w.Write([]byte(errorEvent)); writeErr != nil {
						log.Printf("[Messages-Stream] 写入错误事件失败: %v", writeErr)
					}
					flusher.Flush()
				}

				return nil, err
			}

		case <-c.Request.Context().Done():
			log.Printf("[Messages-Stream] 客户端已断开，停止流处理")
			ctx.ClientGone = true
			go drainChannels(eventChan, errChan)
			return nil, context.Canceled
		}
	}
}

// ProcessStreamEvent 处理单个流事件
func ProcessStreamEvent(
	c *gin.Context,
	w gin.ResponseWriter,
	flusher http.Flusher,
	event string,
	ctx *StreamContext,
	envCfg *config.EnvConfig,
	requestBody []byte,
) {
	// SSE 事件调试日志
	ctx.EventCount++
	if envCfg.SSEDebugLevel == "full" || envCfg.SSEDebugLevel == "summary" {
		eventType, blockIndex, blockType := extractSSEEventInfo(event)
		if eventType == "content_block_start" {
			ctx.ContentBlockCount++
			if blockType != "" {
				ctx.ContentBlockTypes[blockIndex] = blockType
			}
		}
		if envCfg.SSEDebugLevel == "full" {
			log.Printf("[Messages-Stream-Event] #%d 类型=%s 长度=%d block_index=%d block_type=%s",
				ctx.EventCount, eventType, len(event), blockIndex, blockType)
			// 对于 content_block 相关事件，记录详细内容
			if strings.Contains(event, "content_block") {
				log.Printf("[Messages-Stream-Event] 详情: %s", truncateForLog(event, 500))
			}
		}
	}

	// 提取文本用于估算 token
	ExtractTextFromEvent(event, &ctx.OutputTextBuffer)

	// 检测并收集 usage
	hasUsage, needInputPatch, needOutputPatch, usageData := CheckEventUsageStatus(event, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"))
	needPatch := needInputPatch || needOutputPatch
	// 保存原始 usageData 用于后续 PatchMessageStartInputTokensIfNeeded
	originalUsageData := usageData
	if hasUsage {
		if !ctx.HasUsage {
			ctx.HasUsage = true
			ctx.NeedTokenPatch = needPatch || ctx.LowQuality
			if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") && needPatch && !IsMessageDeltaEvent(event) {
				log.Printf("[Messages-Stream-Token] 检测到虚假值, 延迟到流结束修补")
			}
		}
		// 对于 message_start 事件，不累积 input_tokens 到 CollectedUsage
		// 因为 message_start 的 input_tokens 是请求总 token，而非最终计费值
		// CollectedUsage.InputTokens 应该只记录 message_delta 的最终计费值
		if IsMessageStartEvent(event) && usageData.InputTokens > 0 {
			usageData.InputTokens = 0
		}
		// 累积收集 usage 数据
		updateCollectedUsage(&ctx.CollectedUsage, usageData)

		if IsMessageDeltaEvent(event) {
			ctx.HasMessageDeltaUsage = true
		}
	}

	// 日志缓存
	if ctx.LoggingEnabled {
		ctx.LogBuffer.WriteString(event)
		if ctx.Synthesizer != nil {
			for _, line := range strings.Split(event, "\n") {
				ctx.Synthesizer.ProcessLine(line)
			}
		}
	}

	// 在 message_stop 前注入 usage（message_delta 未携带 usage 的兜底场景）
	if !ctx.HasMessageDeltaUsage && !ctx.ClientGone && IsMessageStopEvent(event) {
		usageEvent := BuildUsageEvent(requestBody, ctx.OutputTextBuffer.String())
		if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
			log.Printf("[Messages-Stream-Token] message_delta 缺少 usage, 在 message_stop 前注入兜底 usage 事件")
		}
		if _, writeErr := w.Write([]byte(usageEvent)); writeErr != nil {
			log.Printf("[Messages-Stream] 写入 usage 事件失败: %v", writeErr)
		}
		flusher.Flush()
		ctx.HasUsage = true
		ctx.HasMessageDeltaUsage = true
	}

	// 修补 token
	eventToSend := event

	// 处理 message_start 事件：补全空 id 和检查 model 一致性（可选）
	if IsMessageStartEvent(event) && ctx.RequestModel != "" {
		eventToSend = PatchMessageStartEvent(eventToSend, ctx.RequestModel, envCfg.RewriteResponseModel, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"))
	}

	// 处理 message_start 事件：尽早补全 input_tokens（部分客户端只读取首个 usage 来累计）
	// 注意：使用 originalUsageData 而非被清零后的 usageData，避免误判
	if hasUsage {
		eventToSend = PatchMessageStartInputTokensIfNeeded(eventToSend, requestBody, needInputPatch, originalUsageData, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"), ctx.LowQuality)
	}

	// 对严格客户端做协议兜底：任何 message_delta 都应带顶层 usage。
	if IsMessageDeltaEvent(eventToSend) && !HasEventWithUsage(eventToSend) {
		inputTokens := ctx.CollectedUsage.InputTokens
		outputTokens := ctx.CollectedUsage.OutputTokens

		estimatedInputTokens := utils.EstimateRequestTokens(requestBody)
		estimatedOutputTokens := utils.EstimateTokens(ctx.OutputTextBuffer.String())

		if inputTokens <= 0 && estimatedInputTokens > 0 {
			inputTokens = estimatedInputTokens
		}
		if outputTokens <= 0 && estimatedOutputTokens > 0 {
			outputTokens = estimatedOutputTokens
		}

		eventToSend = EnsureMessageDeltaUsage(eventToSend, inputTokens, outputTokens)

		if inputTokens > ctx.CollectedUsage.InputTokens {
			ctx.CollectedUsage.InputTokens = inputTokens
		}
		if outputTokens > ctx.CollectedUsage.OutputTokens {
			ctx.CollectedUsage.OutputTokens = outputTokens
		}

		ctx.HasUsage = true
		ctx.HasMessageDeltaUsage = true
		if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
			log.Printf("[Messages-Stream-Token] message_delta 缺少 usage, 已就地补齐 input=%d output=%d", inputTokens, outputTokens)
		}
	}

	// 记录 message_start 中的 input_tokens（用于后续推断隐式缓存）
	// 注意：必须在 PatchMessageStartInputTokensIfNeeded 之后执行，因为原始值可能是 0 被修补成估算值
	if IsMessageStartEvent(event) && ctx.MessageStartInputTokens == 0 {
		if patchedInputTokens := ExtractInputTokensFromEvent(eventToSend); patchedInputTokens > 0 {
			ctx.MessageStartInputTokens = patchedInputTokens
		}
	}

	if ctx.NeedTokenPatch && HasEventWithUsage(eventToSend) {
		if IsMessageDeltaEvent(eventToSend) || IsMessageStopEvent(eventToSend) {
			hasCacheTokens := ctx.CollectedUsage.CacheCreationInputTokens > 0 ||
				ctx.CollectedUsage.CacheReadInputTokens > 0 ||
				ctx.CollectedUsage.CacheCreation5mInputTokens > 0 ||
				ctx.CollectedUsage.CacheCreation1hInputTokens > 0

			// 在转发前执行隐式缓存推断，确保下游能收到推断的 cache_read_input_tokens
			if !hasCacheTokens {
				inferImplicitCacheRead(ctx, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"))
				// 重新检查是否有缓存 token（可能刚被推断出来）
				hasCacheTokens = ctx.CollectedUsage.CacheReadInputTokens > 0
			}

			// 检测隐式缓存信号：message_start 的 input_tokens 远大于最终值
			// 这种情况下不应该用本地估算值覆盖，因为低 input_tokens 是缓存命中的正常结果
			hasImplicitCacheSignal := ctx.MessageStartInputTokens > 0 &&
				ctx.CollectedUsage.InputTokens > 0 &&
				ctx.MessageStartInputTokens > ctx.CollectedUsage.InputTokens

			inputTokens := ctx.CollectedUsage.InputTokens
			estimatedInputTokens := utils.EstimateRequestTokens(requestBody)
			// 仅在无缓存信号（显式或隐式）且 input_tokens 异常小时才用估算值修补
			if !hasCacheTokens && !hasImplicitCacheSignal && inputTokens < 10 && estimatedInputTokens > inputTokens {
				inputTokens = estimatedInputTokens
			}

			outputTokens := ctx.CollectedUsage.OutputTokens
			estimatedOutputTokens := utils.EstimateTokens(ctx.OutputTextBuffer.String())
			if outputTokens <= 1 && estimatedOutputTokens > outputTokens {
				outputTokens = estimatedOutputTokens
			}

			if inputTokens > ctx.CollectedUsage.InputTokens {
				ctx.CollectedUsage.InputTokens = inputTokens
			}
			if outputTokens > ctx.CollectedUsage.OutputTokens {
				ctx.CollectedUsage.OutputTokens = outputTokens
			}

			// 修补事件，包括推断的 cache_read_input_tokens
			eventToSend = PatchTokensInEventWithCache(eventToSend, inputTokens, outputTokens, ctx.CollectedUsage.CacheReadInputTokens, hasCacheTokens, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"), ctx.LowQuality)
			ctx.NeedTokenPatch = false
		}
	}

	if IsMessageDeltaEvent(eventToSend) && HasEventWithUsage(eventToSend) {
		ctx.HasUsage = true
		ctx.HasMessageDeltaUsage = true
	}

	// 转发给客户端
	if !ctx.ClientGone {
		if _, err := w.Write([]byte(eventToSend)); err != nil {
			ctx.ClientGone = true
			if !IsClientDisconnectError(err) {
				log.Printf("[Messages-Stream] 警告: 写入错误: %v", err)
			} else if envCfg.ShouldLog("info") {
				log.Printf("[Messages-Stream] 客户端中断连接 (正常行为)，继续接收上游数据...")
			}
		} else {
			flusher.Flush()
		}
	}
}

// EnsureMessageDeltaUsage 确保 message_delta 事件包含顶层 usage 字段。
func EnsureMessageDeltaUsage(event string, inputTokens, outputTokens int) string {
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}

	var result strings.Builder
	lines := strings.Split(event, "\n")

	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data: ")
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		if data["type"] == "message_delta" {
			if _, exists := data["usage"].(map[string]interface{}); !exists {
				data["usage"] = map[string]int{
					"input_tokens":  inputTokens,
					"output_tokens": outputTokens,
				}
			}
		}

		patchedJSON, err := json.Marshal(data)
		if err != nil {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		result.WriteString("data: ")
		result.Write(patchedJSON)
		result.WriteString("\n")
	}

	return result.String()
}

// updateCollectedUsage 更新收集的 usage 数据
func updateCollectedUsage(collected *CollectedUsageData, usageData CollectedUsageData) {
	if usageData.InputTokens > collected.InputTokens {
		collected.InputTokens = usageData.InputTokens
	}
	if usageData.OutputTokens > collected.OutputTokens {
		collected.OutputTokens = usageData.OutputTokens
	}
	if usageData.CacheCreationInputTokens > 0 {
		collected.CacheCreationInputTokens = usageData.CacheCreationInputTokens
	}
	if usageData.CacheReadInputTokens > 0 {
		collected.CacheReadInputTokens = usageData.CacheReadInputTokens
	}
	if usageData.CacheCreation5mInputTokens > 0 {
		collected.CacheCreation5mInputTokens = usageData.CacheCreation5mInputTokens
	}
	if usageData.CacheCreation1hInputTokens > 0 {
		collected.CacheCreation1hInputTokens = usageData.CacheCreation1hInputTokens
	}
	if usageData.CacheTTL != "" {
		collected.CacheTTL = usageData.CacheTTL
	}
}

// inferImplicitCacheRead 推断隐式缓存读取
//
// 当 message_start 中的 input_tokens 与 message_delta 中的最终 input_tokens 存在显著差异时，
// 差额可能是上游 prompt caching 命中但未明确返回 cache_read_input_tokens 的情况。
// 触发条件：差额 > 10% 或差额 > 10000 tokens，且上游未返回 cache_read_input_tokens。
func inferImplicitCacheRead(ctx *StreamContext, enableLog bool) {
	// 前置条件检查
	if ctx.MessageStartInputTokens == 0 || ctx.CollectedUsage.InputTokens == 0 {
		return
	}

	// 上游已明确返回 cache_read，无需推断
	if ctx.CollectedUsage.CacheReadInputTokens > 0 {
		return
	}

	// 计算差额
	diff := ctx.MessageStartInputTokens - ctx.CollectedUsage.InputTokens
	if diff <= 0 {
		return
	}

	// 计算差额比例
	ratio := float64(diff) / float64(ctx.MessageStartInputTokens)

	// 触发条件：差额 > 10% 或差额 > 10000 tokens
	if ratio > 0.10 || diff > 10000 {
		ctx.CollectedUsage.CacheReadInputTokens = diff
		if enableLog {
			log.Printf("[Messages-Stream-Token] 推断隐式缓存: message_start=%d, final=%d, cache_read=%d (%.1f%%)",
				ctx.MessageStartInputTokens, ctx.CollectedUsage.InputTokens, diff, ratio*100)
		}
	}
}

// logStreamCompletion 记录流完成日志
func logStreamCompletion(ctx *StreamContext, envCfg *config.EnvConfig, startTime time.Time) *types.Usage {
	if envCfg.EnableResponseLogs {
		log.Printf("[Messages-Stream] 流式响应完成: %dms", time.Since(startTime).Milliseconds())
	}
	if ctx.ClientGone && envCfg.ShouldLog("info") {
		log.Printf("[Messages-Stream] 客户端已提前断开；上游流仍已完整接收（仅服务端日志可见）")
	}

	// SSE 事件统计日志
	if envCfg.SSEDebugLevel == "full" || envCfg.SSEDebugLevel == "summary" {
		blockTypeSummary := make(map[string]int)
		for _, bt := range ctx.ContentBlockTypes {
			blockTypeSummary[bt]++
		}
		log.Printf("[Messages-Stream-Summary] 总事件数=%d, content_blocks=%d, 类型分布=%v",
			ctx.EventCount, ctx.ContentBlockCount, blockTypeSummary)
	}

	if envCfg.IsDevelopment() {
		logSynthesizedContent(ctx)
	}

	// 推断隐式缓存读取
	inferImplicitCacheRead(ctx, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"))

	// 将累积的 usage 数据转换为 *types.Usage
	return usageFromCollectedUsage(ctx.CollectedUsage)
}

// logPartialResponse 记录部分响应日志
func logPartialResponse(ctx *StreamContext, envCfg *config.EnvConfig) {
	if envCfg.EnableResponseLogs && envCfg.IsDevelopment() {
		logSynthesizedContent(ctx)
	}
}

// logSynthesizedContent 记录合成内容
func logSynthesizedContent(ctx *StreamContext) {
	if ctx.Synthesizer != nil {
		content := ctx.Synthesizer.GetSynthesizedContent()
		if content != "" && !ctx.Synthesizer.IsParseFailed() {
			trimmed := strings.TrimSpace(content)

			// 仅在“明显是 JSON 续写”的情况下拼接预置前缀，避免出现 "{OK" 这类误导日志
			if ctx.LogPrefillText == "{" && !strings.HasPrefix(strings.TrimLeft(trimmed, " \t\r\n"), "{") {
				left := strings.TrimLeft(trimmed, " \t\r\n")
				if strings.HasPrefix(left, "\"") {
					trimmed = ctx.LogPrefillText + trimmed
				}
			}

			log.Printf("[Messages-Stream] 上游流式响应合成内容:\n%s", strings.TrimSpace(trimmed))
			return
		}
	}
	if ctx.LogBuffer.Len() > 0 {
		log.Printf("[Messages-Stream] 上游流式响应原始内容:\n%s", ctx.LogBuffer.String())
	}
}

// IsClientDisconnectError 判断是否为客户端断开连接错误
func IsClientDisconnectError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "context canceled")
}

// HandleStreamResponse 处理流式响应（Messages API）
//
// 流程：provider.HandleStreamResponse → PreflightStreamEvents（预检测）→ 直接透传
//   - 空响应 → return nil, ErrEmptyStreamResponse（Header 未发送，可安全重试）
//   - 拉黑错误 → return nil, ErrBlacklistKey（触发 failover + 拉黑）
//   - 正常 → SetupStreamHeaders → 回放缓冲事件 → 透传后续事件
func HandleStreamResponse(
	c *gin.Context,
	resp *http.Response,
	provider providers.Provider,
	envCfg *config.EnvConfig,
	requestBody []byte,
	upstream *config.UpstreamConfig,
) (*types.Usage, error) {
	defer func() { _ = resp.Body.Close() }()

	// PR3 T2: 在 stream 处理入口锁定 requestStart 并创建首 token 跟踪器。
	// 跟踪器在 newFirstTokenTracker 内部按需返回 nil，MarkFirstEvent 对 nil 安全。
	requestStart := time.Now()
	firstTokenT := newFirstTokenTracker(c, upstream, requestStart)

	if shouldUseRawMessagesStreamPassthrough(c, upstream) {
		return handleRawMessagesStreamPassthrough(c, resp, envCfg, requestBody, upstream)
	}

	// 所有 serviceType 先做 preflight 检测，再按渠道配置决定是否直接透传
	attemptCtx, cancelAttempt := context.WithCancel(c.Request.Context())
	defer cancelAttempt()
	eventChan, errChan, err := provider.HandleStreamResponse(attemptCtx, resp.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to handle stream response"})
		return nil, err
	}
	preflight := PreflightStreamEvents(eventChan, errChan, upstream)
	if preflight.HasError {
		cancelAttempt()
		drainChannels(eventChan, errChan)
		return nil, preflight.Error
	}

	if preflight.InterceptAction != "" {
		cancelAttempt()
		drainChannels(eventChan, errChan)
		switch preflight.InterceptAction {
		case failoverActionCooldown:
			duration := preflight.InterceptDuration
			if duration <= 0 {
				duration = 60 * time.Minute
			}
			return nil, &ErrCooldownKey{
				Reason:   preflight.InterceptReason,
				Message:  preflight.InterceptMessage,
				Duration: duration,
			}
		case failoverActionBlacklist:
			return nil, &ErrBlacklistKey{Reason: preflight.InterceptReason, Message: preflight.InterceptMessage}
		}
	}

	if preflight.IsEmpty {
		log.Printf("[Messages-EmptyResponse] upstream returned empty stream response (buffered events: %d, diagnostic: %s)", len(preflight.BufferedEvents), preflight.Diagnostic)
		cancelAttempt()
		drainChannels(eventChan, errChan)
		return nil, ErrEmptyStreamResponse
	}

	// PR3 T2: 预检通过即视为收到首个有效 SSE event；记录首 token 延迟。
	firstTokenT.MarkFirstEvent()

	SetupStreamHeaders(c, resp)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		cancelAttempt()
		drainChannels(eventChan, errChan)
		return nil, fmt.Errorf("response writer does not support flush")
	}
	flusher.Flush()

	// 渠道级开关：true=直接透传；false=走本地流事件处理链
	if upstream == nil || ShouldDirectPassthroughForRequest(c.Request.URL.Path, upstream, SelectedAPIKeyFromContext(c)) {
		var collectedUsage CollectedUsageData
		var outputText bytes.Buffer
		messageStartInputTokens := 0
		for _, buffered := range preflight.BufferedEvents {
			collectPassthroughStreamUsage(buffered, &collectedUsage, &messageStartInputTokens)
			ExtractTextFromEvent(buffered, &outputText)
			fmt.Fprint(c.Writer, buffered) //nolint:errcheck
			flusher.Flush()
		}
		for {
			select {
			case event, ok := <-eventChan:
				if !ok {
					return finalizePassthroughStreamUsage(collectedUsage, messageStartInputTokens, requestBody, outputText.String(), upstream, envCfg), nil
				}
				collectPassthroughStreamUsage(event, &collectedUsage, &messageStartInputTokens)
				ExtractTextFromEvent(event, &outputText)
				fmt.Fprint(c.Writer, event) //nolint:errcheck
				flusher.Flush()
			case err, ok := <-errChan:
				if !ok {
					errChan = nil
					continue
				}
				if err != nil {
					log.Printf("[Messages-Stream] passthrough stream error: %v", err)
					if !errors.Is(err, context.Canceled) {
						fmt.Fprint(c.Writer, BuildStreamErrorEvent(err)) //nolint:errcheck
						flusher.Flush()
					}
					return nil, err
				}
			case <-c.Request.Context().Done():
				log.Printf("[Messages-Stream] client disconnected during passthrough stream")
				cancelAttempt()
				drainChannels(eventChan, errChan)
				return nil, context.Canceled
			}
		}
	}

	if envCfg == nil {
		envCfg = &config.EnvConfig{}
	}

	streamCtx := NewStreamContext(envCfg)
	streamCtx.RequestModel = extractRequestModelFromRequestBody(requestBody)
	streamCtx.LowQuality = upstream.LowQuality
	startTime := time.Now()

	for _, buffered := range preflight.BufferedEvents {
		ProcessStreamEvent(c, c.Writer, flusher, buffered, streamCtx, envCfg, requestBody)
		if streamCtx.ClientGone {
			cancelAttempt()
			drainChannels(eventChan, errChan)
			return nil, context.Canceled
		}
	}

	return ProcessStreamEvents(c, c.Writer, flusher, eventChan, errChan, streamCtx, envCfg, startTime, requestBody)
}

func shouldUseRawMessagesStreamPassthrough(c *gin.Context, upstream *config.UpstreamConfig) bool {
	if c == nil || c.Request == nil || upstream == nil {
		return false
	}
	return ShouldDirectPassthroughForRequest(c.Request.URL.Path, upstream, SelectedAPIKeyFromContext(c))
}

func handleRawMessagesStreamPassthrough(
	c *gin.Context,
	resp *http.Response,
	envCfg *config.EnvConfig,
	requestBody []byte,
	upstream *config.UpstreamConfig,
) (*types.Usage, error) {
	// PR3 T2: 锁定 requestStart 并初始化首 token 跟踪器。
	requestStart := time.Now()
	firstTokenT := newFirstTokenTracker(c, upstream, requestStart)

	attemptCtx, cancelAttempt := context.WithCancel(c.Request.Context())
	defer cancelAttempt()

	eventChan, errChan, fanoutDone := startRawStreamFanout(attemptCtx, resp.Body)
	preflight := preflightRawStreamEvents(eventChan, errChan, upstream)
	if preflight.HasError {
		cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
		return nil, preflight.Error
	}

	if preflight.InterceptAction != "" {
		cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
		switch preflight.InterceptAction {
		case failoverActionCooldown:
			duration := preflight.InterceptDuration
			if duration <= 0 {
				duration = 60 * time.Minute
			}
			return nil, &ErrCooldownKey{
				Reason:   preflight.InterceptReason,
				Message:  preflight.InterceptMessage,
				Duration: duration,
			}
		case failoverActionBlacklist:
			return nil, &ErrBlacklistKey{Reason: preflight.InterceptReason, Message: preflight.InterceptMessage}
		}
	}

	if preflight.IsEmpty {
		log.Printf("[Messages-EmptyResponse] upstream returned empty raw stream response (buffered events: %d, diagnostic: %s)", len(preflight.BufferedEvents), preflight.Diagnostic)
		cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
		return nil, ErrEmptyStreamResponse
	}

	// PR3 T2: 预检通过即视为收到首个有效 SSE event；记录首 token 延迟。
	firstTokenT.MarkFirstEvent()

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
		return nil, fmt.Errorf("response writer does not support flush")
	}

	SetupStreamHeaders(c, resp)
	flusher.Flush()

	var collectedUsage CollectedUsageData
	var outputText bytes.Buffer
	messageStartInputTokens := 0
	for i, buffered := range preflight.BufferedEvents {
		collectPassthroughStreamUsage(buffered, &collectedUsage, &messageStartInputTokens)
		ExtractTextFromEvent(buffered, &outputText)
		if _, err := c.Writer.Write(preflight.BufferedRawEvents[i]); err != nil {
			cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
			return nil, err
		}
		flusher.Flush()
	}

	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				return finalizePassthroughStreamUsage(collectedUsage, messageStartInputTokens, requestBody, outputText.String(), upstream, envCfg), nil
			}
			collectPassthroughStreamUsage(event.Text, &collectedUsage, &messageStartInputTokens)
			ExtractTextFromEvent(event.Text, &outputText)
			if _, err := c.Writer.Write(event.Bytes); err != nil {
				cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
				return nil, err
			}
			flusher.Flush()
		case err, ok := <-errChan:
			if !ok {
				errChan = nil
				continue
			}
			if err != nil {
				log.Printf("[Messages-Stream] raw passthrough stream error: %v", err)
				if !errors.Is(err, context.Canceled) {
					fmt.Fprint(c.Writer, BuildStreamErrorEvent(err)) //nolint:errcheck
					flusher.Flush()
				}
				return nil, err
			}
		case <-attemptCtx.Done():
			log.Printf("[Messages-Stream] client disconnected during raw passthrough stream")
			cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
			return nil, context.Canceled
		}
	}
}

type rawStreamPassthroughCallbacks struct {
	logPrefix    string
	preflight    func(<-chan rawStreamEvent, <-chan error, *config.UpstreamConfig) *rawStreamPreflightResult
	collect      func(string)
	finalize     func() *types.Usage
	emptyMessage string
}

func handleRawStreamPassthrough(
	c *gin.Context,
	resp *http.Response,
	upstream *config.UpstreamConfig,
	callbacks rawStreamPassthroughCallbacks,
) (*types.Usage, error) {
	// PR3 T2: 锁定 requestStart 并初始化首 token 跟踪器。
	requestStart := time.Now()
	firstTokenT := newFirstTokenTracker(c, upstream, requestStart)

	attemptCtx, cancelAttempt := context.WithCancel(c.Request.Context())
	defer cancelAttempt()

	eventChan, errChan, fanoutDone := startRawStreamFanout(attemptCtx, resp.Body)
	preflight := callbacks.preflight(eventChan, errChan, upstream)
	if preflight.HasError {
		cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
		return nil, preflight.Error
	}

	if preflight.InterceptAction != "" {
		cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
		switch preflight.InterceptAction {
		case failoverActionCooldown:
			duration := preflight.InterceptDuration
			if duration <= 0 {
				duration = 60 * time.Minute
			}
			return nil, &ErrCooldownKey{
				Reason:   preflight.InterceptReason,
				Message:  preflight.InterceptMessage,
				Duration: duration,
			}
		case failoverActionBlacklist:
			return nil, &ErrBlacklistKey{Reason: preflight.InterceptReason, Message: preflight.InterceptMessage}
		}
	}

	if preflight.IsEmpty {
		log.Printf("[%s-EmptyResponse] %s (buffered events: %d, diagnostic: %s)", callbacks.logPrefix, callbacks.emptyMessage, len(preflight.BufferedEvents), preflight.Diagnostic)
		cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
		return nil, ErrEmptyStreamResponse
	}

	// PR3 T2: 预检通过即视为收到首个有效 SSE event；记录首 token 延迟。
	firstTokenT.MarkFirstEvent()

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
		return nil, fmt.Errorf("response writer does not support flush")
	}

	SetupStreamHeaders(c, resp)
	c.Header("X-Accel-Buffering", "no")
	flusher.Flush()

	for i, buffered := range preflight.BufferedEvents {
		callbacks.collect(buffered)
		if _, err := c.Writer.Write(preflight.BufferedRawEvents[i]); err != nil {
			cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
			return nil, err
		}
		flusher.Flush()
	}

	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				return callbacks.finalize(), nil
			}
			callbacks.collect(event.Text)
			if _, err := c.Writer.Write(event.Bytes); err != nil {
				cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
				return nil, err
			}
			flusher.Flush()
		case err, ok := <-errChan:
			if !ok {
				errChan = nil
				continue
			}
			if err != nil {
				log.Printf("[%s-Stream] raw passthrough stream error: %v", callbacks.logPrefix, err)
				if !errors.Is(err, context.Canceled) {
					fmt.Fprint(c.Writer, BuildStreamErrorEvent(err)) //nolint:errcheck
					flusher.Flush()
				}
				return nil, err
			}
		case <-attemptCtx.Done():
			log.Printf("[%s-Stream] client disconnected during raw passthrough stream", callbacks.logPrefix)
			cleanupRawStreamFanout(cancelAttempt, eventChan, errChan, fanoutDone)
			return nil, context.Canceled
		}
	}
}

// HandleRawOpenAIChatStreamPassthrough forwards same-format OpenAI Chat SSE
// bytes unchanged while collecting usage from the side channel.
func HandleRawOpenAIChatStreamPassthrough(c *gin.Context, resp *http.Response, upstream *config.UpstreamConfig) (*types.Usage, error) {
	var usageCollector openAIChatStreamUsageCollector
	return handleRawStreamPassthrough(c, resp, upstream, rawStreamPassthroughCallbacks{
		logPrefix:    "Chat",
		preflight:    preflightRawOpenAIChatStreamEvents,
		emptyMessage: "upstream returned empty raw OpenAI Chat stream response",
		collect: func(event string) {
			usageCollector.Collect(event)
		},
		finalize: func() *types.Usage {
			return usageCollector.Finish()
		},
	})
}

// HandleRawGeminiStreamPassthrough forwards same-format Gemini native SSE
// bytes unchanged while collecting usageMetadata for metrics.
func HandleRawGeminiStreamPassthrough(c *gin.Context, resp *http.Response, upstream *config.UpstreamConfig) (*types.Usage, error) {
	var usageCollector geminiNativeStreamUsageCollector
	return handleRawStreamPassthrough(c, resp, upstream, rawStreamPassthroughCallbacks{
		logPrefix:    "Gemini",
		preflight:    preflightRawGeminiStreamEvents,
		emptyMessage: "upstream returned empty raw Gemini stream response",
		collect: func(event string) {
			usageCollector.Collect(event)
		},
		finalize: func() *types.Usage {
			return usageCollector.Finish()
		},
	})
}

// HandleRawResponsesStreamPassthrough forwards same-format Responses SSE bytes
// unchanged while protocol-specific usage collection runs as a side channel.
func HandleRawResponsesStreamPassthrough(
	c *gin.Context,
	resp *http.Response,
	upstream *config.UpstreamConfig,
	collect func(string),
	finalize func() *types.Usage,
) (*types.Usage, error) {
	return handleRawStreamPassthrough(c, resp, upstream, rawStreamPassthroughCallbacks{
		logPrefix:    "Responses",
		preflight:    preflightRawResponsesStreamEvents,
		emptyMessage: "upstream returned empty raw Responses stream response",
		collect: func(event string) {
			if collect != nil {
				collect(event)
			}
		},
		finalize: func() *types.Usage {
			if finalize == nil {
				return nil
			}
			return finalize()
		},
	})
}

func preflightRawOpenAIChatStreamEvents(eventChan <-chan rawStreamEvent, errChan <-chan error, upstream *config.UpstreamConfig) *rawStreamPreflightResult {
	return preflightRawStreamEventsWithSemanticCheck(eventChan, errChan, upstream, hasOpenAIChatSemanticContent, "OpenAI Chat")
}

func preflightRawGeminiStreamEvents(eventChan <-chan rawStreamEvent, errChan <-chan error, upstream *config.UpstreamConfig) *rawStreamPreflightResult {
	return preflightRawStreamEventsWithSemanticCheck(eventChan, errChan, upstream, hasGeminiNativeSemanticContent, "Gemini")
}

func preflightRawResponsesStreamEvents(eventChan <-chan rawStreamEvent, errChan <-chan error, upstream *config.UpstreamConfig) *rawStreamPreflightResult {
	result := &rawStreamPreflightResult{}
	seenEvent := false
	bufferedBytes := 0
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				result.IsEmpty = true
				if !seenEvent {
					result.Diagnostic = "no SSE events received"
				} else {
					result.Diagnostic = "Responses stream ended without semantic content"
				}
				return result
			}

			seenEvent = true
			result.BufferedEvents = append(result.BufferedEvents, event.Text)
			result.BufferedRawEvents = append(result.BufferedRawEvents, append([]byte(nil), event.Bytes...))
			bufferedBytes += len(event.Bytes)
			if bufferedBytes > maxRawStreamPreflightBufferBytes {
				result.HasError = true
				result.Error = fmt.Errorf("%w: raw stream preflight exceeded %d bytes", ErrInvalidResponseBody, maxRawStreamPreflightBufferBytes)
				return result
			}

			if action, reason, duration, msg := DetectStreamFailoverAction(event.Text, upstream); action != "" {
				result.InterceptAction = action
				result.InterceptReason = reason
				result.InterceptDuration = duration
				result.InterceptMessage = msg
				return result
			}

			if !isValidRawSSEEvent(event.Text) {
				result.HasError = true
				result.Error = fmt.Errorf("%w: invalid raw Responses SSE event", ErrInvalidResponseBody)
				return result
			}

			if hasResponsesRawSemanticContent(event.Text) {
				return result
			}

		case err, ok := <-errChan:
			if !ok {
				errChan = nil
				continue
			}
			if err != nil {
				result.HasError = true
				result.Error = err
				return result
			}

		case <-timeout.C:
			return result
		}
	}
}

func preflightRawStreamEventsWithSemanticCheck(
	eventChan <-chan rawStreamEvent,
	errChan <-chan error,
	upstream *config.UpstreamConfig,
	hasSemanticContent func(string) bool,
	protocol string,
) *rawStreamPreflightResult {
	result := &rawStreamPreflightResult{}
	seenEvent := false
	bufferedBytes := 0
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				result.IsEmpty = true
				if !seenEvent {
					result.Diagnostic = "no SSE events received"
				} else {
					result.Diagnostic = protocol + " stream ended without semantic content"
				}
				return result
			}

			seenEvent = true
			result.BufferedEvents = append(result.BufferedEvents, event.Text)
			result.BufferedRawEvents = append(result.BufferedRawEvents, append([]byte(nil), event.Bytes...))
			bufferedBytes += len(event.Bytes)
			if bufferedBytes > maxRawStreamPreflightBufferBytes {
				result.HasError = true
				result.Error = fmt.Errorf("%w: raw stream preflight exceeded %d bytes", ErrInvalidResponseBody, maxRawStreamPreflightBufferBytes)
				return result
			}

			if action, reason, duration, msg := DetectStreamFailoverAction(event.Text, upstream); action != "" {
				result.InterceptAction = action
				result.InterceptReason = reason
				result.InterceptDuration = duration
				result.InterceptMessage = msg
				return result
			}

			if hasSemanticContent(event.Text) {
				return result
			}

		case err, ok := <-errChan:
			if !ok {
				errChan = nil
				continue
			}
			if err != nil {
				result.HasError = true
				result.Error = err
				return result
			}

		case <-timeout.C:
			return result
		}
	}
}

func isValidRawSSEEvent(event string) bool {
	seenField := false
	for _, line := range strings.Split(event, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, ":") ||
			strings.HasPrefix(line, "data:") ||
			strings.HasPrefix(line, "event:") ||
			strings.HasPrefix(line, "id:") ||
			strings.HasPrefix(line, "retry:") {
			seenField = true
			continue
		}
		return false
	}
	return seenField
}

func hasOpenAIChatSemanticContent(event string) bool {
	found := false
	forEachSSEJSONPayload(event, func(payload map[string]interface{}) {
		if _, ok := payload["usage"].(map[string]interface{}); ok {
			found = true
			return
		}
		choices, _ := payload["choices"].([]interface{})
		for _, item := range choices {
			choice, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
				found = true
				return
			}
			delta, _ := choice["delta"].(map[string]interface{})
			if content, ok := delta["content"].(string); ok && content != "" {
				found = true
				return
			}
			if toolCalls, ok := delta["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
				found = true
				return
			}
			if functionCall, ok := delta["function_call"].(map[string]interface{}); ok && len(functionCall) > 0 {
				found = true
				return
			}
		}
	})
	return found
}

func hasGeminiNativeSemanticContent(event string) bool {
	found := false
	forEachSSEJSONPayload(event, func(payload map[string]interface{}) {
		if _, ok := payload["usageMetadata"].(map[string]interface{}); ok {
			found = true
			return
		}
		candidates, _ := payload["candidates"].([]interface{})
		for _, item := range candidates {
			candidate, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if finishReason, ok := candidate["finishReason"].(string); ok && finishReason != "" {
				found = true
				return
			}
			content, _ := candidate["content"].(map[string]interface{})
			parts, _ := content["parts"].([]interface{})
			for _, partItem := range parts {
				part, ok := partItem.(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := part["text"].(string); ok && text != "" {
					found = true
					return
				}
				if functionCall, ok := part["functionCall"].(map[string]interface{}); ok && len(functionCall) > 0 {
					found = true
					return
				}
				if inlineData, ok := part["inlineData"].(map[string]interface{}); ok && len(inlineData) > 0 {
					found = true
					return
				}
			}
		}
	})
	return found
}

func hasResponsesRawSemanticContent(event string) bool {
	found := false
	forEachSSEJSONPayload(event, func(payload map[string]interface{}) {
		if responsesPayloadHasSemanticContent(payload) {
			found = true
		}
	})
	return found
}

func responsesPayloadHasSemanticContent(payload map[string]interface{}) bool {
	switch payload["type"] {
	case "response.output_text.delta", "response.function_call_arguments.delta",
		"response.reasoning_summary_text.delta", "response.output_json.delta",
		"response.content_part.delta", "response.audio.delta", "response.audio_transcript.delta":
		for _, key := range []string{"delta", "text"} {
			if value, ok := payload[key].(string); ok && value != "" {
				return true
			}
		}
	case "response.function_call_arguments.done", "response.reasoning_summary_text.done",
		"response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		return true
	case "response.output_item.added", "response.output_item.done":
		item, _ := payload["item"].(map[string]interface{})
		return responseItemHasSemanticContent(item)
	case "response.completed":
		response, _ := payload["response"].(map[string]interface{})
		output, _ := response["output"].([]interface{})
		for _, item := range output {
			itemMap, _ := item.(map[string]interface{})
			if responseItemHasSemanticContent(itemMap) {
				return true
			}
		}
	}
	return false
}

func responseItemHasSemanticContent(item map[string]interface{}) bool {
	if len(item) == 0 {
		return false
	}
	itemType, _ := item["type"].(string)
	switch itemType {
	case "function_call", "reasoning":
		return true
	}
	if strings.HasSuffix(itemType, "_call") {
		return true
	}
	if text, ok := item["text"].(string); ok && text != "" {
		return true
	}
	switch content := item["content"].(type) {
	case string:
		return content != ""
	case []interface{}:
		for _, contentItem := range content {
			contentMap, _ := contentItem.(map[string]interface{})
			if text, ok := contentMap["text"].(string); ok && text != "" {
				return true
			}
			if contentType, _ := contentMap["type"].(string); contentType != "" && contentType != "output_text" && contentType != "text" {
				return true
			}
		}
	}
	return false
}

type openAIChatStreamUsageCollector struct {
	promptTokens     int
	completionTokens int
	cacheReadTokens  int
	hasUsage         bool
}

func (c *openAIChatStreamUsageCollector) Collect(event string) {
	forEachSSEJSONPayload(event, func(payload map[string]interface{}) {
		usageMap, ok := payload["usage"].(map[string]interface{})
		if !ok {
			return
		}
		c.hasUsage = true
		if value, ok := intFromJSONNumberOK(usageMap["prompt_tokens"]); ok {
			c.promptTokens = value
		}
		if value, ok := intFromJSONNumberOK(usageMap["completion_tokens"]); ok {
			c.completionTokens = value
		}
		if details, ok := usageMap["prompt_tokens_details"].(map[string]interface{}); ok {
			if value, ok := intFromJSONNumberOK(details["cached_tokens"]); ok {
				c.cacheReadTokens = value
			}
		}
	})
}

func (c *openAIChatStreamUsageCollector) Finish() *types.Usage {
	if !c.hasUsage {
		return nil
	}
	return OpenAIChatUsageFromCounts(c.promptTokens, c.completionTokens, c.cacheReadTokens)
}

type geminiNativeStreamUsageCollector struct {
	promptTokens    int
	cacheReadTokens int
	outputTokens    int
	hasUsage        bool
}

func (c *geminiNativeStreamUsageCollector) Collect(event string) {
	forEachSSEJSONPayload(event, func(payload map[string]interface{}) {
		usageMetadata, ok := payload["usageMetadata"].(map[string]interface{})
		if !ok {
			return
		}
		c.hasUsage = true
		if value, ok := intFromJSONNumberOK(usageMetadata["promptTokenCount"]); ok {
			c.promptTokens = value
		}
		if value, ok := intFromJSONNumberOK(usageMetadata["cachedContentTokenCount"]); ok {
			c.cacheReadTokens = value
		}
		if value, ok := intFromJSONNumberOK(usageMetadata["candidatesTokenCount"]); ok && value > c.outputTokens {
			c.outputTokens = value
		}
	})
}

func (c *geminiNativeStreamUsageCollector) Finish() *types.Usage {
	if !c.hasUsage {
		return nil
	}
	return GeminiUsageFromCounts(c.promptTokens, c.cacheReadTokens, c.outputTokens)
}

func OpenAIChatUsageFromMap(usageMap map[string]interface{}) *types.Usage {
	promptTokens := intFromJSONNumber(usageMap["prompt_tokens"])
	completionTokens := intFromJSONNumber(usageMap["completion_tokens"])
	cacheReadTokens := 0
	if details, ok := usageMap["prompt_tokens_details"].(map[string]interface{}); ok {
		cacheReadTokens = intFromJSONNumber(details["cached_tokens"])
	}
	return OpenAIChatUsageFromCounts(promptTokens, completionTokens, cacheReadTokens)
}

func OpenAIChatUsageFromCounts(promptTokens, completionTokens, cacheReadTokens int) *types.Usage {
	return &types.Usage{
		InputTokens:          promptTokens,
		OutputTokens:         completionTokens,
		CacheReadInputTokens: cacheReadTokens,
		PromptTokensTotal:    promptTokens,
	}
}

func GeminiUsageFromMetadata(metadata *types.GeminiUsageMetadata) *types.Usage {
	if metadata == nil {
		return nil
	}
	return GeminiUsageFromCounts(
		metadata.PromptTokenCount,
		metadata.CachedContentTokenCount,
		metadata.CandidatesTokenCount,
	)
}

func GeminiUsageFromCounts(promptTokens, cachedTokens, outputTokens int) *types.Usage {
	inputTokens := promptTokens - cachedTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	return &types.Usage{
		InputTokens:          inputTokens,
		OutputTokens:         outputTokens,
		CacheReadInputTokens: cachedTokens,
		PromptTokensTotal:    promptTokens,
	}
}

func forEachSSEJSONPayload(event string, visit func(map[string]interface{})) {
	for _, line := range strings.Split(event, "\n") {
		line = strings.TrimSpace(line)
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}
		jsonStr = strings.TrimSpace(jsonStr)
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			continue
		}
		visit(payload)
	}
}

func intFromJSONNumber(value interface{}) int {
	v, _ := intFromJSONNumberOK(value)
	return v
}

func intFromJSONNumberOK(value interface{}) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

func extractRequestModelFromRequestBody(requestBody []byte) string {
	if len(requestBody) == 0 {
		return ""
	}

	var req map[string]interface{}
	if err := json.Unmarshal(requestBody, &req); err != nil {
		return ""
	}

	model, _ := req["model"].(string)
	return strings.TrimSpace(model)
}

// ========== Token 检测和修补相关函数 ==========

// finalizePassthroughStreamUsage converts directly forwarded SSE usage into
// the metrics shape without changing events sent to the client.
func finalizePassthroughStreamUsage(
	collectedUsage CollectedUsageData,
	messageStartInputTokens int,
	requestBody []byte,
	outputText string,
	upstream *config.UpstreamConfig,
	envCfg *config.EnvConfig,
) *types.Usage {
	if collectedUsage.InputTokens == 0 && messageStartInputTokens > 0 {
		collectedUsage.InputTokens = messageStartInputTokens
	}

	streamCtx := &StreamContext{
		CollectedUsage:          collectedUsage,
		MessageStartInputTokens: messageStartInputTokens,
	}
	enableLog := envCfg != nil && envCfg.EnableResponseLogs && envCfg.ShouldLog("debug")
	inferImplicitCacheRead(streamCtx, enableLog)

	lowQuality := upstream != nil && upstream.LowQuality
	return normalizeUsageForMetrics(usageFromCollectedUsage(streamCtx.CollectedUsage), requestBody, outputText, lowQuality, enableLog)
}

func normalizeUsageForMetrics(usage *types.Usage, requestBody []byte, outputText string, lowQuality bool, enableLog bool) *types.Usage {
	if !lowQuality {
		return usage
	}

	estimatedInput := utils.EstimateRequestTokens(requestBody)
	estimatedOutput := utils.EstimateTokens(outputText)
	if usage == nil {
		if estimatedInput <= 0 && estimatedOutput <= 0 {
			return nil
		}
		return &types.Usage{
			InputTokens:  max(estimatedInput, 0),
			OutputTokens: max(estimatedOutput, 0),
		}
	}

	normalized := *usage
	hasCacheTokens := normalized.CacheCreationInputTokens > 0 ||
		normalized.CacheReadInputTokens > 0 ||
		normalized.CacheCreation5mInputTokens > 0 ||
		normalized.CacheCreation1hInputTokens > 0

	if estimatedInput > 0 {
		switch {
		case normalized.InputTokens > 0 && !hasCacheTokens:
			deviation := float64(abs(normalized.InputTokens-estimatedInput)) / float64(estimatedInput)
			if deviation > 0.05 {
				if enableLog {
					log.Printf("[Messages-Usage-LowQuality] metrics input_tokens %d -> %d (deviation %.1f%% > 5%%)",
						normalized.InputTokens, estimatedInput, deviation*100)
				}
				normalized.InputTokens = estimatedInput
			}
		case normalized.InputTokens <= 1 && !hasCacheTokens:
			normalized.InputTokens = estimatedInput
		}
	}

	if estimatedOutput > 0 {
		switch {
		case normalized.OutputTokens > 0:
			deviation := float64(abs(normalized.OutputTokens-estimatedOutput)) / float64(estimatedOutput)
			if deviation > 0.05 {
				if enableLog {
					log.Printf("[Messages-Usage-LowQuality] metrics output_tokens %d -> %d (deviation %.1f%% > 5%%)",
						normalized.OutputTokens, estimatedOutput, deviation*100)
				}
				normalized.OutputTokens = estimatedOutput
			}
		case normalized.OutputTokens <= 1:
			normalized.OutputTokens = estimatedOutput
		}
	}

	return &normalized
}

// CheckEventUsageStatus 检测事件是否包含 usage 字段
func CheckEventUsageStatus(event string, enableLog bool) (bool, bool, bool, CollectedUsageData) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// 检查顶层 usage 字段
		if hasUsage, needInputPatch, needOutputPatch := checkUsageFieldsWithPatch(data["usage"]); hasUsage {
			var usageData CollectedUsageData
			if usage, ok := data["usage"].(map[string]interface{}); ok {
				if enableLog {
					logUsageDetection("顶层usage", usage, needInputPatch || needOutputPatch)
				}
				usageData = extractUsageFromMap(usage)
			}
			return true, needInputPatch, needOutputPatch, usageData
		}

		// 检查 message.usage
		if msg, ok := data["message"].(map[string]interface{}); ok {
			if hasUsage, needInputPatch, needOutputPatch := checkUsageFieldsWithPatch(msg["usage"]); hasUsage {
				var usageData CollectedUsageData
				if usage, ok := msg["usage"].(map[string]interface{}); ok {
					if enableLog {
						logUsageDetection("message.usage", usage, needInputPatch || needOutputPatch)
					}
					usageData = extractUsageFromMap(usage)
				}
				return true, needInputPatch, needOutputPatch, usageData
			}
		}
	}
	return false, false, false, CollectedUsageData{}
}

// checkUsageFieldsWithPatch 检查 usage 对象是否包含 token 字段
func checkUsageFieldsWithPatch(usage interface{}) (bool, bool, bool) {
	if u, ok := usage.(map[string]interface{}); ok {
		inputTokens, hasInput := u["input_tokens"]
		outputTokens, hasOutput := u["output_tokens"]
		if hasInput || hasOutput {
			needInputPatch := false
			needOutputPatch := false

			cacheCreation, _ := u["cache_creation_input_tokens"].(float64)
			cacheRead, _ := u["cache_read_input_tokens"].(float64)
			hasCacheTokens := cacheCreation > 0 || cacheRead > 0

			if hasInput {
				if inputTokens == nil {
					// input_tokens 为 nil 时需要修补
					needInputPatch = true
				} else if v, ok := inputTokens.(float64); ok && v <= 1 && !hasCacheTokens {
					needInputPatch = true
				}
			}
			if hasOutput {
				if v, ok := outputTokens.(float64); ok && v <= 1 {
					needOutputPatch = true
				}
			}
			return true, needInputPatch, needOutputPatch
		}
	}
	return false, false, false
}

// extractUsageFromMap 从 usage map 中提取 token 数据
func extractUsageFromMap(usage map[string]interface{}) CollectedUsageData {
	var data CollectedUsageData

	if v, ok := usage["input_tokens"].(float64); ok {
		data.InputTokens = int(v)
	} else if v, ok := usage["prompt_tokens"].(float64); ok {
		data.InputTokens = int(v)
	}
	if v, ok := usage["output_tokens"].(float64); ok {
		data.OutputTokens = int(v)
	} else if v, ok := usage["completion_tokens"].(float64); ok {
		data.OutputTokens = int(v)
	}
	if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
		data.CacheCreationInputTokens = int(v)
	}
	if v, ok := usage["cache_read_input_tokens"].(float64); ok {
		data.CacheReadInputTokens = int(v)
	}

	var has5m, has1h bool
	if v, ok := usage["cache_creation_5m_input_tokens"].(float64); ok {
		data.CacheCreation5mInputTokens = int(v)
		has5m = data.CacheCreation5mInputTokens > 0
	}
	if v, ok := usage["cache_creation_1h_input_tokens"].(float64); ok {
		data.CacheCreation1hInputTokens = int(v)
		has1h = data.CacheCreation1hInputTokens > 0
	}

	if has5m && has1h {
		data.CacheTTL = "mixed"
	} else if has1h {
		data.CacheTTL = "1h"
	} else if has5m {
		data.CacheTTL = "5m"
	}

	return data
}

func extractUsageFromJSONPayload(payload map[string]interface{}) *types.Usage {
	if usage, ok := payload["usage"].(map[string]interface{}); ok {
		return usageFromCollectedUsage(extractUsageFromMap(usage))
	}
	if message, ok := payload["message"].(map[string]interface{}); ok {
		if usage, ok := message["usage"].(map[string]interface{}); ok {
			return usageFromCollectedUsage(extractUsageFromMap(usage))
		}
	}
	return nil
}

func extractOutputTextFromJSONPayload(payload map[string]interface{}) string {
	var buf strings.Builder

	appendContentText := func(items interface{}) {
		content, ok := items.([]interface{})
		if !ok {
			return
		}
		for _, item := range content {
			switch v := item.(type) {
			case string:
				buf.WriteString(v)
			case map[string]interface{}:
				if text, ok := v["text"].(string); ok {
					buf.WriteString(text)
				}
			}
		}
	}

	appendContentText(payload["content"])
	if choices, ok := payload["choices"].([]interface{}); ok {
		for _, choice := range choices {
			choiceMap, ok := choice.(map[string]interface{})
			if !ok {
				continue
			}
			if message, ok := choiceMap["message"].(map[string]interface{}); ok {
				switch content := message["content"].(type) {
				case string:
					buf.WriteString(content)
				default:
					appendContentText(content)
				}
			}
		}
	}

	return buf.String()
}

func collectPassthroughStreamUsage(event string, collected *CollectedUsageData, messageStartInputTokens *int) {
	hasUsage, _, _, usageData := CheckEventUsageStatus(event, false)
	if !hasUsage {
		return
	}
	if IsMessageStartEvent(event) && usageData.InputTokens > 0 {
		if messageStartInputTokens != nil && *messageStartInputTokens == 0 {
			*messageStartInputTokens = usageData.InputTokens
		}
		usageData.InputTokens = 0
	}
	updateCollectedUsage(collected, usageData)
}

func usageFromCollectedUsage(data CollectedUsageData) *types.Usage {
	hasUsageData := data.InputTokens > 0 ||
		data.OutputTokens > 0 ||
		data.CacheCreationInputTokens > 0 ||
		data.CacheReadInputTokens > 0 ||
		data.CacheCreation5mInputTokens > 0 ||
		data.CacheCreation1hInputTokens > 0
	if !hasUsageData {
		return nil
	}

	return &types.Usage{
		InputTokens:                data.InputTokens,
		OutputTokens:               data.OutputTokens,
		CacheCreationInputTokens:   data.CacheCreationInputTokens,
		CacheReadInputTokens:       data.CacheReadInputTokens,
		CacheCreation5mInputTokens: data.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: data.CacheCreation1hInputTokens,
		CacheTTL:                   data.CacheTTL,
	}
}

// logUsageDetection 统一格式输出 usage 检测日志
func logUsageDetection(location string, usage map[string]interface{}, needPatch bool) {
	inputTokens := usage["input_tokens"]
	outputTokens := usage["output_tokens"]
	cacheCreation, _ := usage["cache_creation_input_tokens"].(float64)
	cacheRead, _ := usage["cache_read_input_tokens"].(float64)

	log.Printf("[Messages-Stream-Token] %s: InputTokens=%v, OutputTokens=%v, CacheCreation=%.0f, CacheRead=%.0f, 需补全=%v",
		location, inputTokens, outputTokens, cacheCreation, cacheRead, needPatch)
}

// HasEventWithUsage 检查事件是否包含 usage 字段
func HasEventWithUsage(event string) bool {
	for _, line := range strings.Split(event, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonStr := strings.TrimPrefix(line, "data: ")

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		if _, ok := data["usage"].(map[string]interface{}); ok {
			return true
		}

		if msg, ok := data["message"].(map[string]interface{}); ok {
			if _, ok := msg["usage"].(map[string]interface{}); ok {
				return true
			}
		}
	}
	return false
}

// PatchTokensInEvent 修补事件中的 token 字段
func PatchTokensInEvent(event string, estimatedInputTokens, estimatedOutputTokens int, hasCacheTokens bool, enableLog bool, lowQuality bool) string {
	var result strings.Builder
	lines := strings.Split(event, "\n")

	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data: ")
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		// 修补顶层 usage
		if usage, ok := data["usage"].(map[string]interface{}); ok {
			patchUsageFieldsWithLog(usage, estimatedInputTokens, estimatedOutputTokens, hasCacheTokens, enableLog, "顶层usage", lowQuality)
		}

		// 修补 message.usage
		if msg, ok := data["message"].(map[string]interface{}); ok {
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				patchUsageFieldsWithLog(usage, estimatedInputTokens, estimatedOutputTokens, hasCacheTokens, enableLog, "message.usage", lowQuality)
			}
		}

		patchedJSON, err := json.Marshal(data)
		if err != nil {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		result.WriteString("data: ")
		result.Write(patchedJSON)
		result.WriteString("\n")
	}

	return result.String()
}

// PatchTokensInEventWithCache 修补事件中的 token 字段，并写入推断的 cache_read_input_tokens
// 当 inferredCacheRead > 0 且事件中没有 cache_read_input_tokens 时，将推断值写入
func PatchTokensInEventWithCache(event string, estimatedInputTokens, estimatedOutputTokens, inferredCacheRead int, hasCacheTokens bool, enableLog bool, lowQuality bool) string {
	var result strings.Builder
	lines := strings.Split(event, "\n")

	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data: ")
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		// 修补顶层 usage
		if usage, ok := data["usage"].(map[string]interface{}); ok {
			patchUsageFieldsWithLog(usage, estimatedInputTokens, estimatedOutputTokens, hasCacheTokens, enableLog, "顶层usage", lowQuality)
			// 写入推断的 cache_read_input_tokens（仅当字段不存在时）
			if inferredCacheRead > 0 {
				if _, exists := usage["cache_read_input_tokens"]; !exists {
					usage["cache_read_input_tokens"] = inferredCacheRead
					if enableLog {
						log.Printf("[Messages-Stream-Token] 顶层usage: 写入推断的 cache_read_input_tokens=%d", inferredCacheRead)
					}
				}
			}
		}

		// 修补 message.usage
		if msg, ok := data["message"].(map[string]interface{}); ok {
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				patchUsageFieldsWithLog(usage, estimatedInputTokens, estimatedOutputTokens, hasCacheTokens, enableLog, "message.usage", lowQuality)
				// 写入推断的 cache_read_input_tokens（仅当字段不存在时）
				if inferredCacheRead > 0 {
					if _, exists := usage["cache_read_input_tokens"]; !exists {
						usage["cache_read_input_tokens"] = inferredCacheRead
						if enableLog {
							log.Printf("[Messages-Stream-Token] message.usage: 写入推断的 cache_read_input_tokens=%d", inferredCacheRead)
						}
					}
				}
			}
		}

		patchedJSON, err := json.Marshal(data)
		if err != nil {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		result.WriteString("data: ")
		result.Write(patchedJSON)
		result.WriteString("\n")
	}

	return result.String()
}

// PatchMessageStartInputTokensIfNeeded 在首个 message_start 事件中尽早补全 input_tokens。
//
// 部分客户端（例如终端工具）只读取首个 usage 来累计 prompt tokens；如果 message_start 的 input_tokens 为 0/极小值，
// 即便后续顶层 usage 给出正确值，也可能导致累计失败。
func PatchMessageStartInputTokensIfNeeded(event string, requestBody []byte, needInputPatch bool, usageData CollectedUsageData, enableLog bool, lowQuality bool) string {
	if !IsMessageStartEvent(event) {
		return event
	}
	if !HasEventWithUsage(event) {
		return event
	}

	hasCacheTokens := usageData.CacheCreationInputTokens > 0 ||
		usageData.CacheReadInputTokens > 0 ||
		usageData.CacheCreation5mInputTokens > 0 ||
		usageData.CacheCreation1hInputTokens > 0

	// 仅在 input_tokens 明显异常时提前补齐；缓存命中场景不应强行补 input_tokens（除非上游返回 nil）
	// 低质量渠道模式下，即使 input_tokens >= 10 也需要进行偏差检测
	if !lowQuality && !needInputPatch && (hasCacheTokens || usageData.InputTokens >= 10) {
		return event
	}

	estimatedInputTokens := utils.EstimateRequestTokens(requestBody)
	if estimatedInputTokens <= 0 {
		return event
	}

	return PatchTokensInEvent(event, estimatedInputTokens, 0, hasCacheTokens, enableLog, lowQuality)
}

// patchUsageFieldsWithLog 修补 usage 对象中的 token 字段
// lowQuality 模式：偏差 > 5% 时使用本地估算值
func patchUsageFieldsWithLog(usage map[string]interface{}, estimatedInput, estimatedOutput int, hasCacheTokens bool, enableLog bool, location string, lowQuality bool) {
	originalInput := usage["input_tokens"]
	originalOutput := usage["output_tokens"]
	inputPatched := false
	outputPatched := false

	cacheCreation, _ := usage["cache_creation_input_tokens"].(float64)
	cacheRead, _ := usage["cache_read_input_tokens"].(float64)
	cacheCreation5m, _ := usage["cache_creation_5m_input_tokens"].(float64)
	cacheCreation1h, _ := usage["cache_creation_1h_input_tokens"].(float64)
	cacheTTL, _ := usage["cache_ttl"].(string)

	// 低质量渠道模式：偏差 > 5% 时使用本地估算值
	if lowQuality {
		if hasCacheTokens {
			if enableLog {
				log.Printf("[Messages-Stream-Token-LowQuality] %s: cache tokens present, keep upstream input_tokens=%v",
					location, usage["input_tokens"])
			}
		} else if v, ok := usage["input_tokens"].(float64); ok && estimatedInput > 0 {
			currentInput := int(v)
			if currentInput > 0 {
				deviation := float64(abs(currentInput-estimatedInput)) / float64(estimatedInput)
				if deviation > 0.05 {
					usage["input_tokens"] = estimatedInput
					inputPatched = true
					if enableLog {
						log.Printf("[Messages-Stream-Token-LowQuality] %s: input_tokens %d -> %d (偏差 %.1f%% > 5%%)",
							location, currentInput, estimatedInput, deviation*100)
					}
				} else if enableLog {
					log.Printf("[Messages-Stream-Token-LowQuality] %s: input_tokens %d ≈ %d (偏差 %.1f%% ≤ 5%%, 保留上游值)",
						location, currentInput, estimatedInput, deviation*100)
				}
			}
		} else if enableLog && estimatedInput > 0 {
			log.Printf("[Messages-Stream-Token-LowQuality] %s: input_tokens=%v (上游无效值, 本地估算=%d)",
				location, usage["input_tokens"], estimatedInput)
		}
		if v, ok := usage["output_tokens"].(float64); ok && estimatedOutput > 0 {
			currentOutput := int(v)
			if currentOutput > 0 {
				deviation := float64(abs(currentOutput-estimatedOutput)) / float64(estimatedOutput)
				if deviation > 0.05 {
					usage["output_tokens"] = estimatedOutput
					outputPatched = true
					if enableLog {
						log.Printf("[Messages-Stream-Token-LowQuality] %s: output_tokens %d -> %d (偏差 %.1f%% > 5%%)",
							location, currentOutput, estimatedOutput, deviation*100)
					}
				} else if enableLog {
					log.Printf("[Messages-Stream-Token-LowQuality] %s: output_tokens %d ≈ %d (偏差 %.1f%% ≤ 5%%, 保留上游值)",
						location, currentOutput, estimatedOutput, deviation*100)
				}
			}
		} else if enableLog && estimatedOutput > 0 {
			log.Printf("[Messages-Stream-Token-LowQuality] %s: output_tokens=%v (上游无效值, 本地估算=%d)",
				location, usage["output_tokens"], estimatedOutput)
		}
	}

	// 常规修补逻辑（非 lowQuality 模式或 lowQuality 模式下未修补的情况）
	if !inputPatched {
		if v, ok := usage["input_tokens"].(float64); ok {
			currentInput := int(v)
			if !hasCacheTokens && ((currentInput <= 1) || (estimatedInput > currentInput && estimatedInput > 1)) {
				usage["input_tokens"] = estimatedInput
				inputPatched = true
			}
		} else if usage["input_tokens"] == nil && estimatedInput > 0 {
			// input_tokens 为 nil 时，用收集到的值修补
			usage["input_tokens"] = estimatedInput
			inputPatched = true
		}
	}

	if !outputPatched && estimatedOutput > 0 {
		if v, ok := usage["output_tokens"].(float64); ok {
			currentOutput := int(v)
			if currentOutput <= 1 || (estimatedOutput > currentOutput && estimatedOutput > 1) {
				usage["output_tokens"] = estimatedOutput
				outputPatched = true
			}
		}
	}

	if enableLog {
		if inputPatched || outputPatched {
			log.Printf("[Messages-Stream-Token-Patch] %s: InputTokens=%v -> %v, OutputTokens=%v -> %v",
				location, originalInput, usage["input_tokens"], originalOutput, usage["output_tokens"])
		}
		log.Printf("[Messages-Stream-Token] %s: InputTokens=%v, OutputTokens=%v, CacheCreationInputTokens=%.0f, CacheReadInputTokens=%.0f, CacheCreation5m=%.0f, CacheCreation1h=%.0f, CacheTTL=%s",
			location, usage["input_tokens"], usage["output_tokens"], cacheCreation, cacheRead, cacheCreation5m, cacheCreation1h, cacheTTL)
	}
}

// abs 返回整数的绝对值
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// BuildStreamErrorEvent 构建流错误 SSE 事件
func BuildStreamErrorEvent(err error) string {
	errorEvent := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "stream_error",
			"message": fmt.Sprintf("Stream processing error: %v", err),
		},
	}
	eventJSON, _ := json.Marshal(errorEvent)
	return fmt.Sprintf("event: error\ndata: %s\n\n", eventJSON)
}

// BuildUsageEvent 构建带 usage 的 message_delta SSE 事件
func BuildUsageEvent(requestBody []byte, outputText string) string {
	inputTokens := utils.EstimateRequestTokens(requestBody)
	outputTokens := utils.EstimateTokens(outputText)

	event := map[string]interface{}{
		"type": "message_delta",
		"usage": map[string]int{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}
	eventJSON, _ := json.Marshal(event)
	return fmt.Sprintf("event: message_delta\ndata: %s\n\n", eventJSON)
}

// IsMessageStartEvent 检测是否为 message_start 事件
func IsMessageStartEvent(event string) bool {
	return strings.Contains(event, "\"type\":\"message_start\"") ||
		strings.Contains(event, "\"type\": \"message_start\"")
}

// PatchMessageStartEvent 修补 message_start 事件中的 id 和 model 字段
func PatchMessageStartEvent(event string, requestModel string, rewriteModel bool, enableLog bool) string {
	if !IsMessageStartEvent(event) {
		return event
	}

	var result strings.Builder
	lines := strings.Split(event, "\n")
	patched := false

	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data: ")
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		msg, ok := data["message"].(map[string]interface{})
		if !ok {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		// 补全空 id
		if id, _ := msg["id"].(string); id == "" {
			msg["id"] = fmt.Sprintf("msg_%s", uuid.New().String())
			patched = true
			if enableLog {
				log.Printf("[Messages-Stream-Patch] 补全空 message.id: %s", msg["id"])
			}
		}

		// 检查 model 一致性（仅在配置启用时改写）
		if rewriteModel {
			if responseModel, _ := msg["model"].(string); responseModel != "" && requestModel != "" && responseModel != requestModel {
				msg["model"] = requestModel
				patched = true
				if enableLog {
					log.Printf("[Messages-Stream-Patch] 改写 message.model: %s -> %s", responseModel, requestModel)
				}
			}
		}

		if patched {
			patchedJSON, err := json.Marshal(data)
			if err != nil {
				result.WriteString(line)
				result.WriteString("\n")
				continue
			}
			result.WriteString("data: ")
			result.Write(patchedJSON)
			result.WriteString("\n")
		} else {
			result.WriteString(line)
			result.WriteString("\n")
		}
	}

	return result.String()
}

// IsMessageStopEvent 检测是否为 message_stop 事件
func IsMessageStopEvent(event string) bool {
	if strings.Contains(event, "event: message_stop") {
		return true
	}

	for _, line := range strings.Split(event, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonStr := strings.TrimPrefix(line, "data: ")

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		if data["type"] == "message_stop" {
			return true
		}
	}
	return false
}

// IsMessageDeltaEvent 检测是否为 message_delta 事件
func IsMessageDeltaEvent(event string) bool {
	if strings.Contains(event, "event: message_delta") {
		return true
	}
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		if data["type"] == "message_delta" {
			return true
		}
	}
	return false
}

// ExtractInputTokensFromEvent 从 SSE 事件中提取 input_tokens
// 支持 message_start 事件的 message.usage.input_tokens 和顶层 usage.input_tokens
func ExtractInputTokensFromEvent(event string) int {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// 检查 message.usage.input_tokens (message_start 事件)
		if msg, ok := data["message"].(map[string]interface{}); ok {
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				if v, ok := usage["input_tokens"].(float64); ok && v > 0 {
					return int(v)
				}
			}
		}

		// 检查顶层 usage.input_tokens (message_delta 事件)
		if usage, ok := data["usage"].(map[string]interface{}); ok {
			if v, ok := usage["input_tokens"].(float64); ok && v > 0 {
				return int(v)
			}
		}
	}
	return 0
}

// ExtractTextFromEvent 从 SSE 事件中提取文本内容
func ExtractTextFromEvent(event string, buf *bytes.Buffer) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// Claude SSE: delta.text
		if delta, ok := data["delta"].(map[string]interface{}); ok {
			if text, ok := delta["text"].(string); ok {
				buf.WriteString(text)
			}
			if partialJSON, ok := delta["partial_json"].(string); ok {
				buf.WriteString(partialJSON)
			}
		}

		// content_block_start 中的初始文本
		if cb, ok := data["content_block"].(map[string]interface{}); ok {
			if text, ok := cb["text"].(string); ok {
				buf.WriteString(text)
			}
		}
	}
}

// DetectStreamBlacklistError 检测 SSE error 事件中是否包含应拉黑 Key 的错误
// 返回 (reason, message)，reason 非空表示应拉黑

// DetectStreamFailoverAction 检测 SSE 事件是否触发渠道级 failover 动作
// 返回 (action, reason, duration, message)
func DetectStreamFailoverAction(event string, upstream *config.UpstreamConfig) (string, string, time.Duration, string) {
	if upstream != nil {
		for _, line := range strings.Split(event, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			payload := ""
			if jsonStr, ok := extractSSEJSONLine(line); ok {
				payload = jsonStr
			} else if strings.HasPrefix(line, "{") {
				payload = line
			}
			if payload == "" {
				continue
			}

			if decision := matchChannelFailoverRule(upstream, http.StatusOK, []byte(payload), "", "", ""); decision.Matched {
				return decision.Action, decision.Reason, decision.Duration, decision.Message
			}
		}
	}

	// 兼容旧逻辑：从 SSE error 事件中提取标准错误并映射到动作
	reason, message := DetectStreamBlacklistError(event)
	switch reason {
	case "":
		return "", "", 0, ""
	case "rate_limit":
		return failoverActionCooldown, reason, 60 * time.Minute, message
	default:
		return failoverActionBlacklist, reason, 0, message
	}
}
func DetectStreamBlacklistError(event string) (reason string, message string) {
	// 检查是否为 error 事件
	isErrorEvent := false
	for _, line := range strings.Split(event, "\n") {
		if strings.HasPrefix(line, "event: ") {
			if strings.TrimPrefix(line, "event: ") == "error" {
				isErrorEvent = true
			}
			break
		}
	}

	// 即使不是显式的 event: error，也检查 data 中的 type == "error"
	for _, line := range strings.Split(event, "\n") {
		line = strings.TrimSpace(line)
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			// 兜底：裸 JSON（bigmodel 等不规范 SSE，error 对象直接作为行内容，无 data: 前缀）
			if strings.HasPrefix(line, "{") {
				jsonStr = line
			} else {
				continue
			}
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// Claude 格式: {"type":"error","error":{"type":"...","message":"..."}}
		// bigmodel 格式: {"error":{"code":"1113","message":"..."}} (data 和 event: error 分两包发送)
		errObj, hasErrObj := data["error"].(map[string]interface{})
		dataType, _ := data["type"].(string)
		if dataType == "error" || isErrorEvent || hasErrObj {
			if hasErrObj {
				errType, _ := errObj["type"].(string)
				errMsg, _ := errObj["message"].(string)
				// code 字段可能是字符串或数字，统一转为字符串
				var errCode string
				switch v := errObj["code"].(type) {
				case string:
					errCode = v
				case float64:
					errCode = fmt.Sprintf("%.0f", v)
				}

				typeLower := strings.ToLower(errType)
				msgLower := strings.ToLower(errMsg)

				// 认证错误
				if typeLower == "authentication_error" || typeLower == "invalid_api_key" || isAuthenticationMessage(errMsg) {
					return "authentication_error", truncateMsg(errMsg)
				}
				// 权限错误
				if typeLower == "permission_error" || typeLower == "permission_denied" || isPermissionMessage(errMsg) {
					return "permission_error", truncateMsg(errMsg)
				}
				// 余额不足（明确的错误类型或错误码）
				if typeLower == "insufficient_balance" || typeLower == "insufficient_quota" || typeLower == "billing_error" {
					return "insufficient_balance", truncateMsg(errMsg)
				}
				// 已知的余额不足错误码（如 bigmodel/Kimi 的 1113）
				if isInsufficientBalanceCode(errCode) || isInsufficientBalanceMessage(errMsg) {
					return "insufficient_balance", truncateMsg(errMsg)
				}
				// 速率限制（临时冷却，非永久拉黑）
				if isRateLimitCode(errCode) {
					return "rate_limit", truncateMsg(errMsg)
				}
				if typeLower == "rate_limit_error" || typeLower == "rate_limit" {
					return "rate_limit", truncateMsg(errMsg)
				}
				if strings.Contains(msgLower, "速率限制") || strings.Contains(msgLower, "请求频率") ||
					strings.Contains(msgLower, "访问量过大") || strings.Contains(msgLower, "稍后再试") && strings.Contains(msgLower, "模型") {
					return "rate_limit", truncateMsg(errMsg)
				}
			}
			if errStr, ok := data["error"].(string); ok {
				if isAuthenticationMessage(errStr) {
					return "authentication_error", truncateMsg(errStr)
				}
				if isPermissionMessage(errStr) {
					return "permission_error", truncateMsg(errStr)
				}
				if isInsufficientBalanceMessage(errStr) {
					return "insufficient_balance", truncateMsg(errStr)
				}
			}
			if msg, ok := data["message"].(string); ok {
				if isAuthenticationMessage(msg) {
					return "authentication_error", truncateMsg(msg)
				}
				if isPermissionMessage(msg) {
					return "permission_error", truncateMsg(msg)
				}
				if isInsufficientBalanceMessage(msg) {
					return "insufficient_balance", truncateMsg(msg)
				}
			}
		}
	}
	return "", ""
}

// isInsufficientBalanceCode 检查错误码是否为已知的余额/限额不足代码（触发永久拉黑）
func isInsufficientBalanceCode(code string) bool {
	knownCodes := []string{
		"1113", // bigmodel/Kimi: 余额不足或无可用资源包
	}
	for _, c := range knownCodes {
		if code == c {
			return true
		}
	}
	return false
}

// isRateLimitCode 检查错误码是否为已知的速率限制代码（触发冷却重试，非永久拉黑）
func isRateLimitCode(code string) bool {
	knownCodes := []string{
		"1302", // bigmodel: 账户已达到速率限制
		"1305", // bigmodel: 该模型当前访问量过大（临时限速，非余额问题）
	}
	for _, c := range knownCodes {
		if code == c {
			return true
		}
	}
	return false
}

// truncateMsg 截断消息（最多200字符）
func truncateMsg(msg string) string {
	if len(msg) > 200 {
		return msg[:200]
	}
	return msg
}

// extractSSEEventInfo 从 SSE 事件中提取事件类型、block 索引和 block 类型
func extractSSEEventInfo(event string) (eventType string, blockIndex int, blockType string) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ = data["type"].(string)
		if idx, ok := data["index"].(float64); ok {
			blockIndex = int(idx)
		}

		// 从 content_block 中提取类型
		if cb, ok := data["content_block"].(map[string]interface{}); ok {
			blockType, _ = cb["type"].(string)
		}

		return
	}
	return
}

// truncateForLog 截断字符串用于日志输出
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
