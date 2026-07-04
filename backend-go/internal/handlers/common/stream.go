// Package common 提供 handlers 模块的公共功能
package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
)

// ErrEmptyStreamResponse 上游返回 HTTP 200 但流式响应内容为空或几乎为空
// 空响应定义：OutputTokens == 0 或 OutputTokens == 1 且内容仅为 "{"
var ErrEmptyStreamResponse = errors.New("upstream returned empty stream response")

// ErrStreamFirstContentTimeout 上游返回 HTTP 200 后，在指定时间内没有首个有效内容
// Header 未发送，可安全 failover 到下一个 Key/BaseURL/渠道
var ErrStreamFirstContentTimeout = errors.New("stream first content timeout")

// ErrStreamStalled 上游返回首个有效内容后，在指定时间内没有后续活动（断流）
// Header 未发送，可安全 failover 到下一个 Key/BaseURL/渠道
var ErrStreamStalled = errors.New("stream stalled after first content")

// ErrStreamPostCommitStalled Header 已发送后，上游长时间没有有效输出
// Header 已发送，不能安全拼接 failover；用于中止当前流并记录渠道故障
var ErrStreamPostCommitStalled = errors.New("stream stalled after response committed")

func streamPreflightEmptyError(preflight *StreamPreflightResult) error {
	if preflight == nil || strings.TrimSpace(preflight.Diagnostic) == "" {
		return ErrEmptyStreamResponse
	}
	return fmt.Errorf("%w: %s", ErrEmptyStreamResponse, preflight.Diagnostic)
}

// StreamPreflightTimeouts 流式预检测超时参数
type StreamPreflightTimeouts struct {
	FirstContentTimeoutMs int // 阶段A：首个有效内容等待超时（ms，范围 5000-300000）
	InactivityTimeoutMs   int // 阶段B：首字后连续性确认窗口（ms，范围 1000-180000）
	ToolCallIdleTimeoutMs int // 工具调用空闲超时（ms，范围 30000-300000）
}

const shortStreamEOFRetryTokenThreshold = 20
const streamPreflightDetailMaxEvents = 8
const streamPreflightDetailPreviewLen = 800

var (
	streamPreflightSensitiveAssignmentPattern = regexp.MustCompile(`(?i)((?:"(?:authorization|x-api-key|x-goog-api-key|api[_-]?key|access[_-]?token|token)"|(?:authorization|x-api-key|x-goog-api-key|api[_-]?key|access[_-]?token|token))\s*[:=]\s*"?)([^",\s}\\]+)`)
	streamPreflightBearerPattern              = regexp.MustCompile(`(?i)(bearer\s+)([A-Za-z0-9._~+/=-]{8,})`)
)

// ResolveStreamFirstContentTimeout 解析首字等待超时：渠道 >0 覆盖全局，否则继承全局
func ResolveStreamFirstContentTimeout(channelValue int, globalValue int) int {
	val := globalValue
	if channelValue > 0 {
		val = channelValue
	}
	if val < 5000 {
		val = 5000
	} else if val > 300000 {
		val = 300000
	}
	return val
}

// ResolveStreamInactivityTimeout 解析断流超时：渠道 >0 覆盖全局，否则继承全局
func ResolveStreamInactivityTimeout(channelValue int, globalValue int) int {
	val := globalValue
	if channelValue > 0 {
		val = channelValue
	}
	if val < 1000 {
		val = 1000
	} else if val > 180000 {
		val = 180000
	}
	return val
}

// ResolveStreamToolCallIdleTimeout 解析工具调用空闲超时：渠道 >0 覆盖全局，否则继承全局
func ResolveStreamToolCallIdleTimeout(channelValue int, globalValue int) int {
	val := globalValue
	if channelValue > 0 {
		val = channelValue
	}
	if val < 30000 {
		val = 30000
	} else if val > 300000 {
		val = 300000
	}
	return val
}

// ResolveStreamPreflightTimeouts 根据渠道覆盖和全局配置解析有效超时参数
func ResolveStreamPreflightTimeouts(upstream *config.UpstreamConfig, global metrics.CircuitBreakerParams) StreamPreflightTimeouts {
	inactivityTimeoutMs := ResolveStreamInactivityTimeout(upstream.StreamInactivityTimeoutMs, global.StreamInactivityTimeoutMs)
	return StreamPreflightTimeouts{
		FirstContentTimeoutMs: ResolveStreamFirstContentTimeout(upstream.StreamFirstContentTimeoutMs, global.StreamFirstContentTimeoutMs),
		InactivityTimeoutMs:   inactivityTimeoutMs,
		ToolCallIdleTimeoutMs: ResolveStreamToolCallIdleTimeout(upstream.StreamToolCallIdleTimeoutMs, global.StreamToolCallIdleTimeoutMs),
	}
}

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

// StreamPreflightResult 流式预检测结果
type StreamPreflightResult struct {
	BufferedEvents        []string // 缓冲的事件（需要回放）
	IsEmpty               bool     // 是否为空响应
	HasError              bool     // 是否有流错误
	Error                 error    // 流错误
	BlacklistReason       string   // 拉黑原因（非空时应拉黑 Key）
	BlacklistMessage      string   // 拉黑错误信息
	Diagnostic            string   // 空响应诊断摘要
	UnknownEventType      string   // 首个未知 SSE data.type
	MalformedToolCall     bool     // 是否检测到空或畸形工具调用
	MalformedToolCallName string   // 畸形工具调用名称
}

type StreamPreflightOptions struct {
	TreatThinkingAsContent bool
}

type StreamToolCallTracker struct {
	active map[int]*StreamToolCallState
}

type StreamToolCallState struct {
	Name      string
	Arguments strings.Builder
}

// PreflightStreamEvents 在发送 HTTP Header 之前预检测流式响应是否为空
// 缓冲事件并检查实际输出内容，避免发送 200 后无法撤销
//
// 两阶段检测：
//   - 阶段A：等待首个有效内容，超时返回 ErrStreamFirstContentTimeout
//   - 阶段B：首个有效内容后等待后续活动，超时返回 ErrStreamStalled
//
// timeouts 参数控制超时行为，0 表示禁用对应阶段。
func PreflightStreamEvents(eventChan <-chan string, errChan <-chan error, timeouts StreamPreflightTimeouts, observers ...*StreamTimeoutObserver) *StreamPreflightResult {
	return PreflightStreamEventsWithOptions(eventChan, errChan, timeouts, StreamPreflightOptions{}, observers...)
}

func PreflightStreamEventsWithOptions(eventChan <-chan string, errChan <-chan error, timeouts StreamPreflightTimeouts, options StreamPreflightOptions, observers ...*StreamTimeoutObserver) *StreamPreflightResult {
	result := &StreamPreflightResult{}
	var observer *StreamTimeoutObserver
	if len(observers) > 0 {
		observer = observers[0]
	}
	var textBuf bytes.Buffer
	var thinkingBuf bytes.Buffer
	toolTracker := NewStreamToolCallTracker()
	hasNonTextContent := false // tool_use / server_tool_use 等非文本语义内容
	hasFirstContent := false   // 已收到首个有效内容（进入阶段B）
	seenEvent := false
	seenMessageStop := false
	seenUsageOnlyEvent := false
	seenUnknownDataType := false
	unknownEventType := ""

	// 阶段A：首个有效内容等待超时（0=禁用）
	var firstContentTimer *time.Timer
	firstContentChan := (<-chan time.Time)(nil)
	if timeouts.FirstContentTimeoutMs > 0 {
		firstContentTimer = time.NewTimer(time.Duration(timeouts.FirstContentTimeoutMs) * time.Millisecond)
		firstContentChan = firstContentTimer.C
		defer firstContentTimer.Stop()
	}

	// 阶段B：首字后不活动超时（初始为 nil，阶段B 时激活）
	var inactivityTimer *time.Timer
	inactivityChan := (<-chan time.Time)(nil)

	for {
		select {
		case event, ok := <-eventChan:
			now := time.Now()
			if !ok {
				// eventChan 关闭：流结束
				if hasNonTextContent {
					return result // 有非文本内容，视为非空
				}
				if hasFirstContent && !seenMessageStop && isShortPreflightText(textBuf.String()) {
					result.HasError = true
					result.Error = fmt.Errorf("%w: short stream closed before message_stop (%d estimated tokens)",
						ErrStreamStalled, estimatePreflightTextTokens(textBuf.String()))
					return result
				}
				result.IsEmpty = isEmptyPreflightContent(textBuf.String(), thinkingBuf.String(), options)
				result.UnknownEventType = unknownEventType
				result.Diagnostic = buildClaudePreflightDiagnostic(seenEvent, seenMessageStop, seenUsageOnlyEvent, seenUnknownDataType, unknownEventType, textBuf.String(), thinkingBuf.String(), result.BufferedEvents)
				return result
			}
			seenEvent = true
			result.BufferedEvents = append(result.BufferedEvents, event)

			// 检测 SSE error 事件中的拉黑条件（认证/余额错误）
			if result.BlacklistReason == "" {
				if reason, msg := DetectStreamBlacklistError(event); reason != "" {
					result.BlacklistReason = reason
					result.BlacklistMessage = msg
				}
			}

			hadPending := toolTracker.HasPendingToolCall()
			if malformed, name := toolTracker.ProcessClaudeEvent(event); malformed {
				result.IsEmpty = true
				result.MalformedToolCall = true
				result.MalformedToolCallName = name
				result.Diagnostic = fmt.Sprintf("malformed tool call: %s", name)
				return result
			}
			// 检测工具调用是否刚闭合：标记为 non-text 内容
			if hadPending && !toolTracker.HasPendingToolCall() && !hasNonTextContent {
				if observer != nil {
					observer.MarkToolCallComplete(now)
				}
				hasNonTextContent = true
				if !hasFirstContent {
					if observer != nil {
						observer.MarkFirstContent(now)
					}
					hasFirstContent = true
					if firstContentTimer != nil {
						firstContentTimer.Stop()
					}
					if timeouts.InactivityTimeoutMs > 0 {
						inactivityTimer = time.NewTimer(time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond)
						inactivityChan = inactivityTimer.C
						defer inactivityTimer.Stop()
					}
					if timeouts.InactivityTimeoutMs <= 0 {
						return result
					}
				}
			}

			// 检测非文本 content block（tool_use / thinking）。tool_use 需等参数完整后再放行。
			if !hasNonTextContent && hasNonTextContentBlock(event) && !toolTracker.HasPendingToolCall() {
				hasNonTextContent = true
				// 非文本内容：视为首个有效内容，进入阶段B
				if !hasFirstContent {
					if observer != nil {
						observer.MarkFirstContent(now)
					}
					hasFirstContent = true
					if firstContentTimer != nil {
						firstContentTimer.Stop()
					}
					if timeouts.InactivityTimeoutMs > 0 {
						inactivityTimer = time.NewTimer(time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond)
						inactivityChan = inactivityTimer.C
						defer inactivityTimer.Stop()
					}
					// 如果禁用阶段B，直接放行
					if timeouts.InactivityTimeoutMs <= 0 {
						return result
					}
				}
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
			ExtractThinkingFromEvent(event, &thinkingBuf)

			outputText := textBuf.String()
			// 检查是否有有效内容（非空且不是仅 "{"）
			if !isEmptyPreflightContent(outputText, thinkingBuf.String(), options) {
				if !hasFirstContent {
					// 阶段A→阶段B：首次检测到有效文本内容
					if observer != nil {
						observer.MarkFirstContent(now)
					}
					hasFirstContent = true
					if firstContentTimer != nil {
						firstContentTimer.Stop()
					}
					if timeouts.InactivityTimeoutMs > 0 {
						inactivityTimer = time.NewTimer(time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond)
						inactivityChan = inactivityTimer.C
						defer inactivityTimer.Stop()
					}
					// 如果禁用阶段B，直接放行（兼容旧行为）
					if timeouts.InactivityTimeoutMs <= 0 {
						return result
					}
				} else if toolTracker.HasPendingToolCall() || !isShortPreflightText(outputText) {
					// 阶段B中累计文本达到阈值：健康流，放行
					if observer != nil {
						observer.MarkStreamActivity(now)
					}
					return result
				}
			}

			// 阶段B中重置不活动定时器（收到任何事件都重置）
			if hasFirstContent && inactivityTimer != nil {
				if observer != nil {
					nowPending := toolTracker.HasPendingToolCall()
					if nowPending {
						observer.MarkToolCallActivity(now)
					} else if hadPending {
						observer.MarkToolCallComplete(now)
					} else {
						observer.MarkStreamActivity(now)
					}
				}
				if !inactivityTimer.Stop() {
					select {
					case <-inactivityTimer.C:
					default:
					}
				}
				inactivityTimer.Reset(time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond)
			}

			// 检查是否为 message_stop 事件（流正常结束）
			if IsMessageStopEvent(event) {
				if hasNonTextContent {
					return result
				}
				result.IsEmpty = isEmptyPreflightContent(textBuf.String(), thinkingBuf.String(), options)
				result.UnknownEventType = unknownEventType
				result.Diagnostic = buildClaudePreflightDiagnostic(seenEvent, true, seenUsageOnlyEvent, seenUnknownDataType, unknownEventType, textBuf.String(), thinkingBuf.String(), result.BufferedEvents)
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
				if hasFirstContent && isShortPreflightText(textBuf.String()) && isStreamEOFError(err) {
					result.Error = fmt.Errorf("%w: short stream ended before message_stop (%d estimated tokens): %v",
						ErrStreamStalled, estimatePreflightTextTokens(textBuf.String()), err)
				} else {
					result.Error = err
				}
				return result
			}

		case <-firstContentChan:
			// 阶段A超时：首个有效内容等待超时
			if timeouts.FirstContentTimeoutMs > 0 {
				result.HasError = true
				result.Error = ErrStreamFirstContentTimeout
				result.Diagnostic = fmt.Sprintf("stream first content timeout after %dms", timeouts.FirstContentTimeoutMs)
				return result
			}
			// 超时被禁用（0），保守放行
			return result

		case <-inactivityChan:
			// 阶段B超时：首字后断流
			result.HasError = true
			result.Error = ErrStreamStalled
			result.Diagnostic = fmt.Sprintf("stream stalled: no activity for %dms after first content", timeouts.InactivityTimeoutMs)
			return result
		}
	}
}

func isEmptyPreflightContent(text string, thinking string, options StreamPreflightOptions) bool {
	if !IsEffectivelyEmptyStreamText(text) {
		return false
	}
	return !options.TreatThinkingAsContent || IsEffectivelyEmptyStreamText(thinking)
}

func buildClaudePreflightDiagnostic(seenEvent, seenMessageStop, seenUsageOnlyEvent, seenUnknownDataType bool, unknownEventType string, text string, thinking string, events []string) string {
	switch {
	case !seenEvent:
		return "未收到任何 SSE 事件"
	case seenUsageOnlyEvent && isEmptyStreamContent(text, thinking):
		return "仅收到 usage/计数类事件，没有文本或语义内容"
	case seenUnknownDataType && isEmptyStreamContent(text, thinking):
		if unknownEventType != "" {
			return "收到了未识别的 SSE data.type=" + unknownEventType + "，但没有文本或语义内容"
		}
		return "收到了未识别的 SSE data.type，但没有文本或语义内容"
	case seenMessageStop && isEmptyStreamContent(text, thinking):
		return "流正常结束(message_stop)，但未检测到文本或语义内容"
	default:
		return "检测到空流，但未匹配到明确类别"
	}
}

type streamPreflightEventInspection struct {
	sseEvents        []string
	dataTypes        []string
	topKeys          []string
	contentBlockType []string
	deltaTypes       []string
	deltaKeys        []string
	stopReasons      []string
	usageKeys        []string
	jsonLines        int
	jsonParseErrors  int
	nonJSONDataLines int
	textBytes        int
	thinkingBytes    int
	semanticContent  bool
	usageOnly        bool
	messageStop      bool
	unknownType      string
	rawBytes         int
	preview          string
}

func buildStreamPreflightDetail(preflight *StreamPreflightResult) string {
	if preflight == nil {
		return "summary: preflight=nil"
	}

	var textBuf bytes.Buffer
	var thinkingBuf bytes.Buffer
	inspections := make([]streamPreflightEventInspection, 0, len(preflight.BufferedEvents))
	for _, event := range preflight.BufferedEvents {
		ExtractTextFromEvent(event, &textBuf)
		ExtractThinkingFromEvent(event, &thinkingBuf)
		inspections = append(inspections, inspectStreamPreflightEvent(event))
	}

	semanticEvents := 0
	usageOnlyEvents := 0
	messageStopEvents := 0
	jsonParseErrors := 0
	nonJSONDataLines := 0
	for _, inspection := range inspections {
		if inspection.semanticContent {
			semanticEvents++
		}
		if inspection.usageOnly {
			usageOnlyEvents++
		}
		if inspection.messageStop {
			messageStopEvents++
		}
		jsonParseErrors += inspection.jsonParseErrors
		nonJSONDataLines += inspection.nonJSONDataLines
	}

	var b strings.Builder
	fmt.Fprintf(&b,
		"summary: events=%d shown=%d isEmpty=%t diagnostic=%q unknownType=%q textBytes=%d thinkingBytes=%d semanticEvents=%d usageOnlyEvents=%d messageStopEvents=%d jsonParseErrors=%d nonJSONDataLines=%d malformedToolCall=%t malformedToolCallName=%q blacklistReason=%q",
		len(preflight.BufferedEvents),
		min(len(preflight.BufferedEvents), streamPreflightDetailMaxEvents),
		preflight.IsEmpty,
		preflight.Diagnostic,
		preflight.UnknownEventType,
		textBuf.Len(),
		thinkingBuf.Len(),
		semanticEvents,
		usageOnlyEvents,
		messageStopEvents,
		jsonParseErrors,
		nonJSONDataLines,
		preflight.MalformedToolCall,
		preflight.MalformedToolCallName,
		preflight.BlacklistReason,
	)

	for i, inspection := range inspections {
		if i >= streamPreflightDetailMaxEvents {
			fmt.Fprintf(&b, "\nevent[%d+]: omitted=%d", i, len(inspections)-i)
			break
		}
		fmt.Fprintf(&b,
			"\nevent[%d]: sse=%s dataTypes=%s topKeys=%s contentBlockTypes=%s deltaTypes=%s deltaKeys=%s stopReasons=%s usageKeys=%s jsonLines=%d jsonParseErrors=%d nonJSONDataLines=%d textBytes=%d thinkingBytes=%d semantic=%t usageOnly=%t messageStop=%t unknownType=%q rawBytes=%d preview=%q",
			i,
			formatStreamLogValues(inspection.sseEvents),
			formatStreamLogValues(inspection.dataTypes),
			formatStreamLogValues(inspection.topKeys),
			formatStreamLogValues(inspection.contentBlockType),
			formatStreamLogValues(inspection.deltaTypes),
			formatStreamLogValues(inspection.deltaKeys),
			formatStreamLogValues(inspection.stopReasons),
			formatStreamLogValues(inspection.usageKeys),
			inspection.jsonLines,
			inspection.jsonParseErrors,
			inspection.nonJSONDataLines,
			inspection.textBytes,
			inspection.thinkingBytes,
			inspection.semanticContent,
			inspection.usageOnly,
			inspection.messageStop,
			inspection.unknownType,
			inspection.rawBytes,
			inspection.preview,
		)
	}

	return b.String()
}

func inspectStreamPreflightEvent(event string) streamPreflightEventInspection {
	var textBuf bytes.Buffer
	var thinkingBuf bytes.Buffer
	ExtractTextFromEvent(event, &textBuf)
	ExtractThinkingFromEvent(event, &thinkingBuf)

	inspection := streamPreflightEventInspection{
		textBytes:       textBuf.Len(),
		thinkingBytes:   thinkingBuf.Len(),
		semanticContent: HasClaudeSemanticContent(event),
		usageOnly:       isUsageOnlySSEEvent(event),
		messageStop:     IsMessageStopEvent(event),
		rawBytes:        len(event),
		preview:         truncateForLog(redactStreamPreflightLogText(event), streamPreflightDetailPreviewLen),
	}
	if unknownType, ok := firstUnknownSSEDataType(event); ok {
		inspection.unknownType = unknownType
	}

	for _, line := range strings.Split(event, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "event:") {
			inspection.sseEvents = appendUniqueString(inspection.sseEvents, strings.TrimSpace(strings.TrimPrefix(trimmedLine, "event:")))
		}

		jsonStr, ok := extractSSEJSONLine(trimmedLine)
		if !ok {
			continue
		}
		if strings.TrimSpace(jsonStr) == "" {
			inspection.nonJSONDataLines++
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			inspection.jsonParseErrors++
			continue
		}
		inspection.jsonLines++
		inspection.topKeys = mergeUniqueStrings(inspection.topKeys, sortedMapKeys(data))

		if dataType, _ := data["type"].(string); dataType != "" {
			inspection.dataTypes = appendUniqueString(inspection.dataTypes, dataType)
		}

		if contentBlock, ok := data["content_block"].(map[string]interface{}); ok {
			if blockType, _ := contentBlock["type"].(string); blockType != "" {
				inspection.contentBlockType = appendUniqueString(inspection.contentBlockType, blockType)
			}
		}

		if delta, ok := data["delta"].(map[string]interface{}); ok {
			inspection.deltaKeys = mergeUniqueStrings(inspection.deltaKeys, sortedMapKeys(delta))
			if deltaType, _ := delta["type"].(string); deltaType != "" {
				inspection.deltaTypes = appendUniqueString(inspection.deltaTypes, deltaType)
			}
			if stopReason, _ := delta["stop_reason"].(string); stopReason != "" {
				inspection.stopReasons = appendUniqueString(inspection.stopReasons, stopReason)
			}
		}

		if usage, ok := data["usage"].(map[string]interface{}); ok {
			inspection.usageKeys = mergeUniqueStrings(inspection.usageKeys, prefixedSortedMapKeys("usage.", usage))
		}
		if message, ok := data["message"].(map[string]interface{}); ok {
			if usage, ok := message["usage"].(map[string]interface{}); ok {
				inspection.usageKeys = mergeUniqueStrings(inspection.usageKeys, prefixedSortedMapKeys("message.usage.", usage))
			}
		}
	}

	return inspection
}

func appendUniqueString(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func mergeUniqueStrings(values []string, more []string) []string {
	for _, value := range more {
		values = appendUniqueString(values, value)
	}
	return values
}

func sortedMapKeys(data map[string]interface{}) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func prefixedSortedMapKeys(prefix string, data map[string]interface{}) []string {
	keys := sortedMapKeys(data)
	for i := range keys {
		keys[i] = prefix + keys[i]
	}
	return keys
}

func formatStreamLogValues(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ",")
}

func redactStreamPreflightLogText(text string) string {
	text = streamPreflightBearerPattern.ReplaceAllString(text, "${1}***")
	return streamPreflightSensitiveAssignmentPattern.ReplaceAllString(text, "${1}***")
}

func estimatePreflightTextTokens(text string) int {
	return utils.EstimateTokens(text)
}

func isShortPreflightText(text string) bool {
	return estimatePreflightTextTokens(text) < shortStreamEOFRetryTokenThreshold
}

func isStreamEOFError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(strings.ToLower(err.Error()), "unexpected eof")
}

func NewStreamToolCallTracker() *StreamToolCallTracker {
	return &StreamToolCallTracker{active: make(map[int]*StreamToolCallState)}
}

func (t *StreamToolCallTracker) HasPendingToolCall() bool {
	return len(t.active) > 0
}

func (t *StreamToolCallTracker) ProcessClaudeEvent(event string) (bool, string) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)
		index := 0
		if idx, ok := data["index"].(float64); ok {
			index = int(idx)
		}

		switch eventType {
		case "content_block_start":
			contentBlock, _ := data["content_block"].(map[string]interface{})
			blockType, _ := contentBlock["type"].(string)
			if blockType != "tool_use" && blockType != "server_tool_use" {
				continue
			}
			state := &StreamToolCallState{}
			if name, ok := contentBlock["name"].(string); ok {
				state.Name = name
			}
			// 注意：在 Claude 流式协议中，content_block_start 的 input 字段始终为 {}（占位符），
			// 实际参数通过后续 input_json_delta 事件流入。
			// 仅当 input 非空对象时才写入 Arguments（兼容非流式或已内联参数的场景）。
			if input, exists := contentBlock["input"]; exists && !IsMalformedToolArguments(input) {
				if inputMap, ok := input.(map[string]interface{}); ok && len(inputMap) > 0 {
					if b, err := json.Marshal(input); err == nil {
						state.Arguments.Write(b)
					}
				}
			}
			t.active[index] = state
		case "content_block_delta":
			delta, _ := data["delta"].(map[string]interface{})
			if partial, ok := delta["partial_json"].(string); ok {
				state := t.active[index]
				if state == nil {
					state = &StreamToolCallState{}
					t.active[index] = state
				}
				state.Arguments.WriteString(partial)
			}
		case "content_block_stop":
			state := t.active[index]
			if state == nil {
				continue
			}
			delete(t.active, index)
			if isMalformedNamedToolArguments(state.Name, state.Arguments.String()) {
				name := state.Name
				if name == "" {
					name = "unknown_tool"
				}
				return true, name
			}
		}
	}
	return false, ""
}

func (t *StreamToolCallTracker) ProcessResponsesEvent(event string) (bool, string) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)
		index := 0
		if idx, ok := data["output_index"].(float64); ok {
			index = int(idx)
		}

		switch eventType {
		case "response.output_item.added":
			item, _ := data["item"].(map[string]interface{})
			if !isToolCallItem(item) {
				continue
			}
			state := &StreamToolCallState{}
			if name, ok := item["name"].(string); ok {
				state.Name = name
			}
			if args, ok := firstPresentToolArgument(item); ok && !IsMalformedToolArguments(args) {
				if b, err := json.Marshal(args); err == nil {
					state.Arguments.Write(b)
				}
			}
			t.active[index] = state
		case "response.function_call_arguments.delta":
			state := t.active[index]
			if state == nil {
				state = &StreamToolCallState{}
				t.active[index] = state
			}
			if delta, ok := data["delta"].(string); ok {
				state.Arguments.WriteString(delta)
			}
		case "response.function_call_arguments.done":
			state := t.active[index]
			if state == nil {
				state = &StreamToolCallState{}
				t.active[index] = state
			}
			if args, ok := data["arguments"]; ok {
				state.Arguments.Reset()
				writeToolArgument(&state.Arguments, args)
			}
			if item, ok := data["item"].(map[string]interface{}); ok {
				if name, ok := item["name"].(string); ok && name != "" {
					state.Name = name
				}
			}
		case "response.output_item.done":
			item, _ := data["item"].(map[string]interface{})
			if !isToolCallItem(item) {
				continue
			}
			state := t.active[index]
			if state == nil {
				state = &StreamToolCallState{}
			}
			if name, ok := item["name"].(string); ok && name != "" {
				state.Name = name
			}
			if args, ok := firstPresentToolArgument(item); ok {
				state.Arguments.Reset()
				writeToolArgument(&state.Arguments, args)
			}
			delete(t.active, index)
			if isMalformedResponsesToolCall(item, state.Arguments.String()) {
				return true, fallbackToolName(state.Name, index)
			}
		case "response.completed":
			if response, ok := data["response"].(map[string]interface{}); ok {
				if output, ok := response["output"].([]interface{}); ok {
					for i, raw := range output {
						item, ok := raw.(map[string]interface{})
						if !ok || !isToolCallItem(item) {
							continue
						}
						args, _ := firstPresentToolArgument(item)
						if isMalformedResponsesToolCall(item, args) {
							name, _ := item["name"].(string)
							return true, fallbackToolName(name, i)
						}
					}
				}
			}
		}
	}
	return false, ""
}

func isToolCallItem(item map[string]interface{}) bool {
	if item == nil {
		return false
	}
	itemType, _ := item["type"].(string)
	return itemType == "function_call" || itemType == "custom_tool_call" || strings.HasSuffix(itemType, "_call")
}

func isMalformedResponsesToolCall(item map[string]interface{}, args interface{}) bool {
	itemType, _ := item["type"].(string)
	name, _ := item["name"].(string)
	if itemType == "custom_tool_call" {
		switch v := args.(type) {
		case nil:
			return true
		case string:
			return strings.TrimSpace(v) == ""
		case map[string]interface{}:
			return len(v) == 0
		case []interface{}:
			return len(v) == 0
		default:
			return false
		}
	}
	return isMalformedNamedToolArguments(name, args)
}

func firstPresentToolArgument(item map[string]interface{}) (interface{}, bool) {
	for _, key := range []string{"arguments", "input", "args"} {
		if v, ok := item[key]; ok {
			return v, true
		}
	}
	return nil, false
}

func writeToolArgument(builder *strings.Builder, args interface{}) {
	if s, ok := args.(string); ok {
		builder.WriteString(s)
		return
	}
	if b, err := json.Marshal(args); err == nil {
		builder.Write(b)
	}
}

func fallbackToolName(name string, index int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("tool_%d", index)
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
