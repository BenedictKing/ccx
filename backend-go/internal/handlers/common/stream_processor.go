// Package common 提供 handlers 模块的公共功能
package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/thinkingcache"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// isEmptyContent 判断流式响应的累积文本是否为空内容
func isEmptyContent(text string) bool {
	return IsEffectivelyEmptyStreamText(text)
}

func isEmptyStreamContent(text string, thinking string) bool {
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

// hasNonTextContentBlock 检测 SSE 事件是否包含可立即判定为有效的非文本语义内容（如 tool_use）
// 这些 content block 不产生 delta.text，但属于有效响应内容
func hasNonTextContentBlock(event string) bool {
	return HasClaudeSemanticContent(event)
}

func toolUseBlockStartIndex(event string) (int, bool) {
	if !strings.Contains(event, "content_block_start") {
		return 0, false
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
		if data["type"] != "content_block_start" {
			continue
		}
		index := 0
		if idx, ok := data["index"].(float64); ok {
			index = int(idx)
		}
		cb, ok := data["content_block"].(map[string]interface{})
		if !ok {
			continue
		}
		cbType, _ := cb["type"].(string)
		if cbType == "tool_use" || cbType == "server_tool_use" {
			return index, true
		}
	}
	return 0, false
}

func contentBlockEventIndex(event string) (string, int, bool) {
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
		if eventType != "content_block_delta" && eventType != "content_block_stop" {
			continue
		}
		index := 0
		if idx, ok := data["index"].(float64); ok {
			index = int(idx)
		}
		return eventType, index, true
	}
	return "", 0, false
}

// buildTruncationEndEvents 构建截断时注入的结束事件序列
// 生成 message_delta(stop_reason=end_turn) + message_stop，让客户端认为模型正常结束
func buildTruncationEndEvents(ctx *StreamContext) []string {
	outputTokens := ctx.CollectedUsage.OutputTokens
	if outputTokens <= 0 {
		outputTokens = utils.EstimateTokens(ctx.OutputTextBuffer.String())
	}

	messageDelta := fmt.Sprintf("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null,\"stop_details\":null},\"usage\":{\"output_tokens\":%d}}\n\n", outputTokens)
	messageStop := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	return []string{messageDelta, messageStop}
}

func handleToolUseBufferEvent(
	c *gin.Context,
	w gin.ResponseWriter,
	flusher http.Flusher,
	event string,
	ctx *StreamContext,
	malformedDetected bool,
	malformedToolName string,
) bool {
	if ctx == nil || ctx.ToolUseTruncated {
		return false
	}

	if index, ok := toolUseBlockStartIndex(event); ok {
		if ctx.ToolUseBuffers == nil {
			ctx.ToolUseBuffers = make(map[int]*ToolUseBlockBuffer)
		}
		buffer := ctx.ToolUseBuffers[index]
		if buffer == nil {
			buffer = &ToolUseBlockBuffer{Index: index}
			ctx.ToolUseBuffers[index] = buffer
			ctx.ToolUseBufferOrder = append(ctx.ToolUseBufferOrder, index)
		}
		buffer.Events = append(buffer.Events, event)
		return true
	}

	eventType, index, ok := contentBlockEventIndex(event)
	if !ok || ctx.ToolUseBuffers == nil {
		return false
	}
	buffer := ctx.ToolUseBuffers[index]
	if buffer == nil {
		return false
	}

	buffer.Events = append(buffer.Events, event)
	if eventType == "content_block_stop" {
		buffer.Closed = true
		buffer.Malformed = malformedDetected
		buffer.MalformedToolName = malformedToolName
		flushClosedToolUseBuffers(c, w, flusher, ctx)
	}
	return true
}

func flushClosedToolUseBuffers(c *gin.Context, w gin.ResponseWriter, flusher http.Flusher, ctx *StreamContext) {
	if ctx == nil || ctx.ToolUseTruncated {
		return
	}

	wrote := false
	for len(ctx.ToolUseBufferOrder) > 0 {
		index := ctx.ToolUseBufferOrder[0]
		buffer := ctx.ToolUseBuffers[index]
		if buffer == nil {
			ctx.ToolUseBufferOrder = ctx.ToolUseBufferOrder[1:]
			continue
		}
		if !buffer.Closed {
			break
		}

		if buffer.Malformed && ctx.CommittedToolUseCount == 0 {
			// 畸形 + 尚无已透传的 tool_use → 截断：注入 end_turn 结束响应
			ctx.ToolUseTruncated = true
			toolName := buffer.MalformedToolName
			if toolName == "" {
				toolName = "unknown_tool"
			}
			RequestLogf(c, "[Messages-Stream-ToolCall] 截断畸形工具调用 %s，注入 end_turn 终止响应", toolName)
			if !ctx.ClientGone {
				endEvents := buildTruncationEndEvents(ctx)
				for _, ev := range endEvents {
					if _, err := w.Write([]byte(ev)); err != nil {
						ctx.ClientGone = true
						break
					}
				}
				if !ctx.ClientGone {
					flusher.Flush()
				}
			}
			ctx.HasMessageDeltaUsage = true // 防止后续注入 usage
			ctx.ToolUseBuffers = nil
			ctx.ToolUseBufferOrder = nil
			return
		}

		ctx.CommittedToolUseCount++
		for _, buffered := range buffer.Events {
			if ctx.ClientGone {
				break
			}
			if _, err := w.Write([]byte(buffered)); err != nil {
				ctx.ClientGone = true
				break
			}
		}
		wrote = true
		delete(ctx.ToolUseBuffers, index)
		ctx.ToolUseBufferOrder = ctx.ToolUseBufferOrder[1:]
	}

	if wrote && !ctx.ClientGone {
		flusher.Flush()
	}
}

// HasStreamEventActivity 判断 SSE 事件是否包含可视为上游仍在输出的内容。
// post-commit watchdog 使用它作为 idle reset 信号，避免 reasoning/progress/tool 状态事件被误判为断流。
func HasStreamEventActivity(event string) bool {
	for _, line := range strings.Split(event, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
		}
		return true
	}
	return false
}

// HasSSEFrame 判断是否确实收到了一个上游 SSE 帧。
// 工具调用参数可能会长时间批量输出；pending 阶段收到心跳/空帧也说明连接仍活着。
func HasSSEFrame(event string) bool {
	return event != ""
}

func summarizeStreamEventForIdleLog(event string) string {
	eventType, blockIndex, blockType := extractSSEEventInfo(event)
	lineCount, firstField := summarizeSSELineFields(event)
	toolName := extractSSEToolName(event)

	parts := []string{
		fmt.Sprintf("event_len=%d", len(event)),
		fmt.Sprintf("non_empty_lines=%d", lineCount),
	}
	if eventType != "" {
		parts = append(parts, "data_type="+eventType)
	} else if firstField != "" {
		parts = append(parts, "first_field="+firstField)
	}
	if blockIndex > 0 || blockType != "" {
		parts = append(parts, fmt.Sprintf("block_index=%d", blockIndex))
	}
	if blockType != "" {
		parts = append(parts, "block_type="+blockType)
	}
	if toolName != "" {
		parts = append(parts, "tool="+toolName)
	}
	return strings.Join(parts, " ")
}

func summarizeSSELineFields(event string) (int, string) {
	lineCount := 0
	firstField := ""
	for _, line := range strings.Split(event, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lineCount++
		if firstField != "" {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, ":"):
			firstField = "comment"
		case strings.HasPrefix(trimmed, "data:"):
			firstField = "data"
		case strings.HasPrefix(trimmed, "event:"):
			firstField = "event"
		case strings.HasPrefix(trimmed, "id:"):
			firstField = "id"
		case strings.HasPrefix(trimmed, "retry:"):
			firstField = "retry"
		default:
			firstField = "unknown"
		}
	}
	return lineCount, firstField
}

func extractSSEToolName(event string) string {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		if cb, ok := data["content_block"].(map[string]interface{}); ok {
			if name, ok := cb["name"].(string); ok && name != "" {
				return name
			}
		}
	}
	return ""
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
				case "text", "", "thinking", "redacted_thinking":
				default:
					return true
				}
			}
		}

		if delta, ok := data["delta"].(map[string]interface{}); ok {
			if deltaType, _ := delta["type"].(string); deltaType == "input_json_delta" {
				return true
			}
		}
	}
	return false
}

func responseItemCarriesSemanticContent(item map[string]interface{}) bool {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "compaction", "compaction_summary":
		encryptedContent, _ := item["encrypted_content"].(string)
		return strings.TrimSpace(encryptedContent) != ""
	case "function_call", "reasoning", "tool_search_call", "tool_search_output":
		return true
	}
	// 形态规则：覆盖上游新增的 xxx_call / xxx_output 工具类 item 类型
	return strings.HasSuffix(itemType, "_call") || strings.HasSuffix(itemType, "_output")
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
			"response.custom_tool_call_input.delta", "response.custom_tool_call_input.done",
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
		default:
			// 未知事件类型兜底：上游新增事件按形态识别语义内容，
			// 避免 preflight 把携带工具调用/输出的新事件误判为空流
			eventType, _ := data["type"].(string)
			if !strings.HasPrefix(eventType, "response.") {
				continue
			}
			if item, ok := data["item"].(map[string]interface{}); ok && responseItemCarriesSemanticContent(item) {
				return true
			}
			if callID, _ := data["call_id"].(string); callID != "" {
				return true
			}
		}
	}
	return false
}

// HasOpenAIChatSemanticContent 判断 OpenAI Chat 风格 SSE 是否包含有效语义内容

func HasOpenAIChatSemanticContent(event string) bool {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok || strings.TrimSpace(jsonStr) == "[DONE]" {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		choices, _ := data["choices"].([]interface{})
		for _, rawChoice := range choices {
			choice, _ := rawChoice.(map[string]interface{})
			delta, _ := choice["delta"].(map[string]interface{})
			if content, _ := delta["content"].(string); !IsEffectivelyEmptyStreamText(content) {
				return true
			}
			reasoning, _ := delta["reasoning_content"].(string)
			if reasoning == "" {
				reasoning, _ = delta["reasoning"].(string)
			}
			if !IsEffectivelyEmptyStreamText(reasoning) {
				return true
			}
			if functionCall, ok := delta["function_call"].(map[string]interface{}); ok && len(functionCall) > 0 {
				return true
			}
			if calls, ok := delta["tool_calls"].([]interface{}); ok && len(calls) > 0 {
				return true
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

type ToolUseBlockBuffer struct {
	Index             int
	Events            []string
	Closed            bool
	Malformed         bool
	MalformedToolName string
}

// StreamContext 流处理上下文
type StreamContext struct {
	LogBuffer            *LimitedLogBuffer
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
	ToolCallTracker   *StreamToolCallTracker
	LastEventAt       time.Time // 最近一次本地处理 SSE 事件的时间
	LastEventSummary  string    // 最近一次 SSE 事件摘要，不包含正文或参数
	// 低质量渠道处理
	RequestModel string // 请求中的 model（用于一致性检查）
	LowQuality   bool   // 是否为低质量渠道
	// 隐式缓存推断
	MessageStartInputTokens int // message_start 事件中的 input_tokens（用于推断隐式缓存）
	ResponseText            string
	LogTag                  string
	// 畸形工具调用缓冲（post-commit 阶段截断策略）
	ToolUseBuffers        map[int]*ToolUseBlockBuffer // 按 content_block index 缓冲 tool_use 事件
	ToolUseBufferOrder    []int                       // tool_use block 的开始顺序，用于正规化重叠块
	CommittedToolUseCount int                         // 已透传给客户端的 tool_use block 数量
	ToolUseTruncated      bool                        // 是否已执行截断（注入 end_turn）
	// DeepSeek Claude thinking 回填缓存
	ThinkingCollector      *thinkingcache.ClaudeStreamCollector
	ThinkingCacheSessionID string
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
		ToolCallTracker:   NewStreamToolCallTracker(),
		LogBuffer:         NewLimitedLogBuffer(MaxUpstreamResponseLogBytes),
	}
	if ctx.LoggingEnabled {
		ctx.Synthesizer = utils.NewStreamSynthesizer("claude")
	}
	return ctx
}

// seedSynthesizerFromRequest 将请求里预置的 assistant 文本拼接进合成器（仅用于日志可读性）
//
// Claude Code 的部分内部调用会在 messages 里预置一条 assistant 内容（例如 "{"），让模型只输出“续写”部分。
// 这会导致我们仅基于 SSE delta 合成的日志缺失开头。这里用请求体做一次轻量补齐。
func seedSynthesizerFromRequest(ctx *StreamContext, requestBody []byte) {
	if ctx == nil || ctx.Synthesizer == nil || len(requestBody) == 0 {
		return
	}

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(requestBody, &req); err != nil {
		return
	}

	// 只取最后一条 assistant，避免把历史上下文都拼进日志
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != "assistant" {
			continue
		}
		var b strings.Builder
		for _, c := range msg.Content {
			if c.Type == "text" && c.Text != "" {
				b.WriteString(c.Text)
			}
		}
		prefill := b.String()
		// 防止把很长的预置内容刷进日志
		if len(prefill) > 0 && len(prefill) <= 256 {
			ctx.LogPrefillText = prefill
		}
		return
	}
}

// SetupStreamHeaders 设置流式响应头
func SetupStreamHeaders(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, apiType string) {
	LogUpstreamResponseHeaders(c, resp, envCfg, apiType)
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
	timeouts StreamPreflightTimeouts,
) (*types.Usage, error) {
	// post-commit：Header 已发送后的 idle watchdog，由任意上游 SSE 活动重置。
	var postCommitTimer *time.Timer
	var postCommitChan <-chan time.Time
	activeTimeoutMs := timeouts.InactivityTimeoutMs
	if activeTimeoutMs > 0 {
		postCommitTimer = time.NewTimer(time.Duration(activeTimeoutMs) * time.Millisecond)
		postCommitChan = postCommitTimer.C
		defer postCommitTimer.Stop()
	}
	progress := NewStreamProgressLogger("Messages", startTime, envCfg.ShouldLog("info"), RequestLogTag(c))
	toolCallPending := ctx.ToolCallTracker != nil && ctx.ToolCallTracker.HasPendingToolCall()
	if toolCallPending && timeouts.ToolCallIdleTimeoutMs > 0 {
		activeTimeoutMs = timeouts.ToolCallIdleTimeoutMs
		if postCommitTimer != nil {
			if !postCommitTimer.Stop() {
				select {
				case <-postCommitTimer.C:
				default:
				}
			}
			postCommitTimer.Reset(time.Duration(activeTimeoutMs) * time.Millisecond)
		}
	}
	keepaliveTicker := time.NewTicker(15 * time.Second)
	defer keepaliveTicker.Stop()

	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				progress.Finish("completed")
				usage := logStreamCompletion(ctx, envCfg, startTime)
				storeStreamThinkingCache(ctx, envCfg)
				return usage, nil
			}
			keepaliveTicker.Reset(15 * time.Second)
			prevTextLen := ctx.OutputTextBuffer.Len()
			prevToolCallPending := toolCallPending
			ProcessStreamEvent(c, w, flusher, event, ctx, envCfg, requestBody)
			if ctx.OutputTextBuffer.Len() > prevTextLen {
				progress.AddText(ctx.OutputTextBuffer.String()[prevTextLen:])
				progress.Tick()
			}
			eventHasActivity := ctx.OutputTextBuffer.Len() > prevTextLen || HasClaudeSemanticContent(event) || HasStreamEventActivity(event)
			nowToolCallPending := ctx.ToolCallTracker != nil && ctx.ToolCallTracker.HasPendingToolCall()
			if nowToolCallPending != toolCallPending {
				toolCallPending = nowToolCallPending
				if toolCallPending && timeouts.ToolCallIdleTimeoutMs > 0 {
					activeTimeoutMs = timeouts.ToolCallIdleTimeoutMs
				} else {
					activeTimeoutMs = timeouts.InactivityTimeoutMs
				}
				eventHasActivity = eventHasActivity || activeTimeoutMs > 0
			}
			if nowToolCallPending && HasSSEFrame(event) {
				eventHasActivity = true
			}
			if eventHasActivity {
				if nowToolCallPending {
					MarkStreamToolCallActivity(c)
				} else if prevToolCallPending {
					MarkStreamToolCallComplete(c)
				} else {
					MarkStreamActivity(c)
				}
			}
			if postCommitTimer != nil && eventHasActivity && activeTimeoutMs > 0 {
				if !postCommitTimer.Stop() {
					select {
					case <-postCommitTimer.C:
					default:
					}
				}
				postCommitTimer.Reset(time.Duration(activeTimeoutMs) * time.Millisecond)
			}

		case err, ok := <-errChan:
			if !ok {
				continue
			}
			if err != nil {
				RequestLogf(c, "[Messages-Stream] 错误: 流式传输错误: %v", err)
				logPartialResponse(ctx, envCfg)

				// 向客户端发送错误事件（如果连接仍然有效）
				if !ctx.ClientGone {
					errorEvent := BuildStreamErrorEvent(err)
					w.Write([]byte(errorEvent))
					flusher.Flush()
				}

				progress.Finish("error")
				return nil, err
			}
		case <-postCommitChan:
			lastEventAgeMs := int64(-1)
			lastEventSummary := "none"
			if ctx.LastEventAt.IsZero() {
				lastEventAgeMs = time.Since(startTime).Milliseconds()
			} else {
				lastEventAgeMs = time.Since(ctx.LastEventAt).Milliseconds()
				if ctx.LastEventSummary != "" {
					lastEventSummary = ctx.LastEventSummary
				}
			}
			if toolCallPending {
				RequestLogf(c, "[Messages-StreamStalled] 流式断流: 工具调用阶段空闲 %dms 无上游输出（Header 已发送，最后本地事件距今 %dms，%s）", activeTimeoutMs, lastEventAgeMs, lastEventSummary)
			} else {
				RequestLogf(c, "[Messages-StreamStalled] 流式断流: Header 已发送后 %dms 无上游输出（最后本地事件距今 %dms，%s）", activeTimeoutMs, lastEventAgeMs, lastEventSummary)
			}
			logPartialResponse(ctx, envCfg)
			if !ctx.ClientGone {
				if _, err := w.Write([]byte(BuildStreamErrorEvent(ErrStreamPostCommitStalled))); err == nil {
					flusher.Flush()
				}
			}
			progress.Finish("stalled")
			return nil, ErrStreamPostCommitStalled
		case <-keepaliveTicker.C:
			if toolCallPending && !ctx.ClientGone {
				if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
					ctx.ClientGone = true
				} else {
					flusher.Flush()
				}
			}
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
	ctx.LastEventAt = time.Now()
	ctx.LastEventSummary = summarizeStreamEventForIdleLog(event)
	if envCfg.SSEDebugLevel == "full" || envCfg.SSEDebugLevel == "summary" {
		eventType, blockIndex, blockType := extractSSEEventInfo(event)
		if eventType == "content_block_start" {
			ctx.ContentBlockCount++
			if blockType != "" {
				ctx.ContentBlockTypes[blockIndex] = blockType
			}
		}
		if envCfg.SSEDebugLevel == "full" {
			RequestLogf(c, "[Messages-Stream-Event] #%d 类型=%s 长度=%d block_index=%d block_type=%s",
				ctx.EventCount, eventType, len(event), blockIndex, blockType)
			// 对于 content_block 相关事件，记录详细内容
			if strings.Contains(event, "content_block") {
				RequestLogf(c, "[Messages-Stream-Event] 详情: %s", truncateForLog(event, 500))
			}
		}
	}

	recordRawStreamEvent(ctx, event)
	if ctx.ThinkingCollector != nil {
		ctx.ThinkingCollector.ProcessEvent(event)
	}

	// 如果已截断（注入了 end_turn），后续事件只消费不转发
	if ctx.ToolUseTruncated {
		// 仍需收集 usage 数据（下方正常路径会处理），但不转发给客户端
		// 这里不 return 以便 usage 收集逻辑执行，但在转发处拦截
	}

	// 畸形工具调用检测
	var malformedDetected bool
	var malformedToolName string
	if ctx.ToolCallTracker != nil {
		malformedDetected, malformedToolName = ctx.ToolCallTracker.ProcessClaudeEvent(event)
		if malformedDetected && envCfg.ShouldLog("info") {
			RequestLogf(c, "[Messages-Stream-ToolCall] 检测到畸形工具调用: %s", malformedToolName)
		}
	}

	if handleToolUseBufferEvent(c, w, flusher, event, ctx, malformedDetected, malformedToolName) {
		ExtractTextFromEvent(event, &ctx.OutputTextBuffer)
		ctx.ResponseText = ctx.OutputTextBuffer.String()
		return
	}

	ExtractTextFromEvent(event, &ctx.OutputTextBuffer)
	ctx.ResponseText = ctx.OutputTextBuffer.String()

	// 检测并收集 usage
	hasUsage, needInputPatch, needOutputPatch, usageData := checkEventUsageStatusWithLogTag(event, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"), ctx.LogTag)
	needPatch := needInputPatch || needOutputPatch
	// 保存原始 usageData 用于后续 PatchMessageStartInputTokensIfNeeded
	originalUsageData := usageData
	if hasUsage {
		if !ctx.HasUsage {
			ctx.HasUsage = true
			ctx.NeedTokenPatch = needPatch || ctx.LowQuality
			if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") && needPatch && !IsMessageDeltaEvent(event) {
				RequestLogf(c, "[Messages-Stream-Token] 检测到虚假值, 延迟到流结束修补")
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

	// 在 message_stop 前注入 usage（message_delta 未携带 usage 的兜底场景）
	if !ctx.HasMessageDeltaUsage && !ctx.ClientGone && !ctx.ToolUseTruncated && IsMessageStopEvent(event) {
		usageEvent := BuildUsageEvent(requestBody, ctx.OutputTextBuffer.String())
		if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
			RequestLogf(c, "[Messages-Stream-Token] message_delta 缺少 usage, 在 message_stop 前注入兜底 usage 事件")
		}
		w.Write([]byte(usageEvent))
		flusher.Flush()
		ctx.HasUsage = true
		ctx.HasMessageDeltaUsage = true
	}

	// 修补 token
	eventToSend := event

	// 处理 message_start 事件：补全空 id 和检查 model 一致性（可选）
	if IsMessageStartEvent(event) && ctx.RequestModel != "" {
		eventToSend = patchMessageStartEventWithLogTag(eventToSend, ctx.RequestModel, envCfg.RewriteResponseModel, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"), ctx.LogTag)
	}

	// 处理 message_start 事件：尽早补全 input_tokens（部分客户端只读取首个 usage 来累计）
	// 注意：使用 originalUsageData 而非被清零后的 usageData，避免误判
	if hasUsage {
		eventToSend = patchMessageStartInputTokensIfNeededWithLogTag(eventToSend, requestBody, needInputPatch, originalUsageData, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"), ctx.LowQuality, ctx.LogTag)
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
			RequestLogf(c, "[Messages-Stream-Token] message_delta 缺少 usage, 已就地补齐 input=%d output=%d", inputTokens, outputTokens)
		}
	}

	// 记录上游原始 message_start 中的 input_tokens（用于后续推断隐式缓存）。
	// eventToSend 可能已经被本地估算值修补，不能作为缓存推断依据。
	if IsMessageStartEvent(event) && ctx.MessageStartInputTokens == 0 {
		if upstreamInputTokens := ExtractInputTokensFromEvent(event); upstreamInputTokens > 1 {
			ctx.MessageStartInputTokens = upstreamInputTokens
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
			eventToSend = patchTokensInEventWithCacheWithLogTag(eventToSend, inputTokens, outputTokens, ctx.CollectedUsage.CacheReadInputTokens, hasCacheTokens, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"), ctx.LowQuality, ctx.LogTag)
			ctx.NeedTokenPatch = false
		}
	}

	if IsMessageDeltaEvent(eventToSend) && HasEventWithUsage(eventToSend) {
		ctx.HasUsage = true
		ctx.HasMessageDeltaUsage = true
	}

	// 转发给客户端（已截断的流不再转发后续事件）
	if !ctx.ClientGone && !ctx.ToolUseTruncated {
		if _, err := w.Write([]byte(eventToSend)); err != nil {
			ctx.ClientGone = true
			if !IsClientDisconnectError(err) {
				RequestLogf(c, "[Messages-Stream] 警告: 写入错误: %v", err)
			} else if envCfg.ShouldLog("info") {
				RequestLogf(c, "[Messages-Stream] 客户端中断连接 (正常行为)，继续接收上游数据...")
			}
		} else {
			flusher.Flush()
		}
	}
}

func recordRawStreamEvent(ctx *StreamContext, event string) {
	if ctx == nil || !ctx.LoggingEnabled {
		return
	}
	ctx.LogBuffer.WriteString(event)
	if ctx.Synthesizer == nil {
		return
	}
	for _, line := range strings.Split(event, "\n") {
		ctx.Synthesizer.ProcessLine(line)
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
			streamLogf(ctx, "[Messages-Stream-Token] 推断隐式缓存: message_start=%d, final=%d, cache_read=%d (%.1f%%)",
				ctx.MessageStartInputTokens, ctx.CollectedUsage.InputTokens, diff, ratio*100)
		}
	}
}

// logStreamCompletion 记录流完成日志
func logStreamCompletion(ctx *StreamContext, envCfg *config.EnvConfig, startTime time.Time) *types.Usage {
	if envCfg.EnableResponseLogs {
		streamLogf(ctx, "[Messages-Stream] 流式响应完成: %dms", time.Since(startTime).Milliseconds())
	}
	if ctx.ClientGone && envCfg.ShouldLog("info") {
		streamLogf(ctx, "[Messages-Stream] 客户端已提前断开；上游流仍已完整接收（仅服务端日志可见）")
	}

	// SSE 事件统计日志
	if envCfg.SSEDebugLevel == "full" || envCfg.SSEDebugLevel == "summary" {
		blockTypeSummary := make(map[string]int)
		for _, bt := range ctx.ContentBlockTypes {
			blockTypeSummary[bt]++
		}
		streamLogf(ctx, "[Messages-Stream-Summary] 总事件数=%d, content_blocks=%d, 类型分布=%v",
			ctx.EventCount, ctx.ContentBlockCount, blockTypeSummary)
	}

	if envCfg.IsDevelopment() {
		logSynthesizedContent(ctx)
	}

	// 推断隐式缓存读取
	inferImplicitCacheRead(ctx, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"))

	// 将累积的 usage 数据转换为 *types.Usage
	var usage *types.Usage
	hasUsageData := ctx.CollectedUsage.InputTokens > 0 ||
		ctx.CollectedUsage.OutputTokens > 0 ||
		ctx.CollectedUsage.CacheCreationInputTokens > 0 ||
		ctx.CollectedUsage.CacheReadInputTokens > 0 ||
		ctx.CollectedUsage.CacheCreation5mInputTokens > 0 ||
		ctx.CollectedUsage.CacheCreation1hInputTokens > 0
	if hasUsageData {
		usage = &types.Usage{
			InputTokens:                ctx.CollectedUsage.InputTokens,
			OutputTokens:               ctx.CollectedUsage.OutputTokens,
			CacheCreationInputTokens:   ctx.CollectedUsage.CacheCreationInputTokens,
			CacheReadInputTokens:       ctx.CollectedUsage.CacheReadInputTokens,
			CacheCreation5mInputTokens: ctx.CollectedUsage.CacheCreation5mInputTokens,
			CacheCreation1hInputTokens: ctx.CollectedUsage.CacheCreation1hInputTokens,
			CacheTTL:                   ctx.CollectedUsage.CacheTTL,
		}
	}
	return usage
}

func storeStreamThinkingCache(ctx *StreamContext, envCfg *config.EnvConfig) {
	if ctx == nil || ctx.ThinkingCollector == nil || ctx.ThinkingCacheSessionID == "" || ctx.ToolUseTruncated {
		return
	}
	if ctx.ThinkingCollector.Store(ctx.ThinkingCacheSessionID) && envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
		streamLogf(ctx, "[Messages-ThinkingCache] 已缓存 Claude thinking 回填内容")
	}
}

// logPartialResponse 记录部分响应日志
func logPartialResponse(ctx *StreamContext, envCfg *config.EnvConfig) {
	if envCfg.EnableResponseLogs && envCfg.IsDevelopment() {
		logSynthesizedContent(ctx)
	}
}

func streamLogf(ctx *StreamContext, format string, args ...interface{}) {
	if ctx == nil {
		log.Printf(format, args...)
		return
	}
	logWithTag(ctx.LogTag, format, args...)
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

			streamLogf(ctx, "[Messages-Stream] 上游流式响应合成内容:\n%s", strings.TrimSpace(trimmed))
			return
		}
	}
	if ctx.LogBuffer.Len() > 0 {
		streamLogf(ctx, "[Messages-Stream] 上游流式响应原始内容:\n%s", ctx.LogBuffer.String())
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
// 流程：provider.HandleStreamResponse → PreflightStreamEvents（预检测）
//   - 空响应 → return nil, ErrEmptyStreamResponse（Header 未发送，可安全重试）
//   - 首字超时 → return nil, ErrStreamFirstContentTimeout（Header 未发送，可安全重试）
//   - 首字后断流 → return nil, ErrStreamStalled（Header 未发送，可安全重试）
//   - 非空   → SetupStreamHeaders → 回放缓冲事件 → ProcessStreamEvents
func HandleStreamResponse(
	c *gin.Context,
	resp *http.Response,
	provider providers.Provider,
	envCfg *config.EnvConfig,
	startTime time.Time,
	upstream *config.UpstreamConfig,
	requestBody []byte,
	requestModel string,
	timeouts StreamPreflightTimeouts,
) (*types.Usage, error) {
	defer resp.Body.Close()

	eventChan, errChan, err := provider.HandleStreamResponse(resp.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to handle stream response"})
		return nil, err
	}

	// 预检测：在发送 HTTP Header 之前缓冲事件并检查是否为空响应
	preflight := PreflightStreamEvents(eventChan, errChan, timeouts, GetStreamTimeoutObserver(c))

	// 流错误：排空 channel 后返回错误
	if preflight.HasError {
		drainChannels(eventChan, errChan)
		if errors.Is(preflight.Error, ErrStreamFirstContentTimeout) {
			RequestLogf(c, "[Messages-FirstContentTimeout] 流式首字超时: %dms，触发重试", timeouts.FirstContentTimeoutMs)
		} else if errors.Is(preflight.Error, ErrStreamStalled) {
			RequestLogf(c, "[Messages-StreamStalled] 流式断流: 首字后 %dms 无活动，触发重试", timeouts.InactivityTimeoutMs)
		}
		return nil, preflight.Error
	}

	// 空响应：Header 未发送，可安全重试
	if preflight.IsEmpty {
		RequestLogf(c, "[Messages-EmptyResponse] 上游返回空响应 (缓冲事件数: %d, 诊断: %s)，触发重试", len(preflight.BufferedEvents), preflight.Diagnostic)
		RequestLogf(c, "[Messages-EmptyResponse-Debug] %s", buildStreamPreflightDetail(preflight))
		drainChannels(eventChan, errChan)
		// 如果同时检测到拉黑条件，优先返回拉黑错误
		if preflight.BlacklistReason != "" {
			return nil, &ErrBlacklistKey{Reason: preflight.BlacklistReason, Message: preflight.BlacklistMessage}
		}
		return nil, streamPreflightEmptyError(preflight)
	}

	// 流中有拉黑错误但内容非空（如错误前有部分输出）：仍返回拉黑错误以触发 Key 拉黑
	if preflight.BlacklistReason != "" {
		drainChannels(eventChan, errChan)
		return nil, &ErrBlacklistKey{Reason: preflight.BlacklistReason, Message: preflight.BlacklistMessage}
	}

	// 非空响应：正常流程
	SetupStreamHeaders(c, resp, envCfg, "Messages")

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		RequestLogf(c, "[Messages-Stream] 警告: ResponseWriter不支持Flush接口")
		drainChannels(eventChan, errChan)
		return nil, fmt.Errorf("ResponseWriter不支持Flush接口")
	}
	flusher.Flush()

	ctx := NewStreamContext(envCfg)
	ctx.RequestModel = requestModel
	ctx.LowQuality = upstream.LowQuality
	ctx.LogTag = RequestLogTag(c)
	if thinkingcache.ShouldTrackClaudeThinking(upstream, requestBody) {
		if sessionID := utils.ExtractUnifiedSessionID(c, requestBody); sessionID != "" {
			ctx.ThinkingCollector = thinkingcache.NewClaudeStreamCollector()
			ctx.ThinkingCacheSessionID = sessionID
		}
	}
	seedSynthesizerFromRequest(ctx, requestBody)

	// 回放预检测期间缓冲的事件
	for _, bufferedEvent := range preflight.BufferedEvents {
		ProcessStreamEvent(c, w, flusher, bufferedEvent, ctx, envCfg, requestBody)
	}

	usage, err := ProcessStreamEvents(c, w, flusher, eventChan, errChan, ctx, envCfg, startTime, requestBody, timeouts)
	c.Set("responseText", ctx.ResponseText)
	if err != nil {
		return nil, err
	}
	return annotatePromptTokensTotalForProvider(provider, usage), nil
}
