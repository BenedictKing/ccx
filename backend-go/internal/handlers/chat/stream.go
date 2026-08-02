package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func handleStreamSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	model string,
	timeouts common.StreamPreflightTimeouts,
) (*types.Usage, error) {
	var totalUsage *types.Usage
	logBuffer := common.NewLimitedLogBuffer(common.MaxUpstreamResponseLogBytes)
	streamLoggingEnabled := envCfg.EnableResponseLogs && envCfg.IsDevelopment()

	common.LogUpstreamResponseHeaders(c, resp, envCfg, "Chat")

	preflight, chunkChan, bodyErrChan, err := preflightChatStream(resp, upstreamType, timeouts, common.GetStreamTimeoutObserver(c))
	if err != nil {
		if errors.Is(err, common.ErrStreamFirstContentTimeout) {
			common.RequestLogf(c, "[Chat-FirstContentTimeout] 流式首字超时: %dms，触发重试", timeouts.FirstContentTimeoutMs)
		} else if errors.Is(err, common.ErrStreamStalled) {
			common.RequestLogf(c, "[Chat-StreamStalled] 流式断流: 首字后 %dms 无活动，触发重试", timeouts.InactivityTimeoutMs)
		}
		return nil, err
	}
	resp.Body = common.NewChunkChannelReadCloser(chunkChan, bodyErrChan, resp.Body)

	if preflight.malformedToolName != "" {
		common.RequestLogf(c, "[Chat-EmptyResponse] 上游返回空或畸形 tool_call（流式，upstreamType=%s, tool=%s），触发 failover", upstreamType, preflight.malformedToolName)
		return nil, common.ErrEmptyStreamResponse
	}

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		common.RequestLogf(c, "[Chat-Stream] 警告: ResponseWriter 不支持 Flusher")
	}
	progress := common.NewStreamProgressLogger("Chat", startTime, envCfg.ShouldLog("info"), common.RequestLogTag(c))

	switch upstreamType {
	case "claude":
		var streamErr error
		totalUsage, streamErr = streamClaudeToChat(c, resp, flusher, model, logBuffer, streamLoggingEnabled, preflight.buffered, timeouts, progress)
		if streamErr != nil {
			return nil, streamErr
		}
	case "responses":
		var streamErr error
		totalUsage, streamErr = streamResponsesToChat(c, resp, flusher, model, logBuffer, streamLoggingEnabled, preflight.buffered, timeouts, progress)
		if streamErr != nil {
			return nil, streamErr
		}
	default:
		// OpenAI / Gemini / Responses 等：直接透传 SSE 流
		var streamErr error
		totalUsage, streamErr = streamPassthrough(c, resp, flusher, logBuffer, streamLoggingEnabled, preflight.buffered, timeouts, progress)
		if streamErr != nil {
			return nil, streamErr
		}
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		common.RequestLogf(c, "[Chat-Stream-Timing] 流式响应完成: %dms", responseTime)
		if logBuffer.Len() > 0 {
			common.RequestLogf(c, "[Chat-Stream] 上游流式响应原始内容:\n%s", logBuffer.String())
		}
	}

	return totalUsage, nil
}

type chatStreamPreflight struct {
	buffered          []byte
	malformedToolName string
}

type chatToolTracker interface {
	HasPendingToolCall() bool
	ProcessClaudeEvent(string) (bool, string)
	ProcessResponsesEvent(string) (bool, string)
}

// preflightChatStream Chat 流式预检测（两阶段：首字等待 + 首字后断流检测）
func preflightChatStream(resp *http.Response, upstreamType string, timeouts common.StreamPreflightTimeouts, observers ...*common.StreamTimeoutObserver) (*chatStreamPreflight, <-chan []byte, <-chan error, error) {
	result := &chatStreamPreflight{}
	var observer *common.StreamTimeoutObserver
	if len(observers) > 0 {
		observer = observers[0]
	}
	tracker := common.NewStreamToolCallTracker()
	chatTracker := newOpenAIChatToolCallTracker()
	var remainder string
	const maxPreflightBytes = 1024 * 1024
	hasFirstContent := false

	flushRemainder := func() {
		// FIX(fix/flush-remainder-dup): remainder 是缓冲区尾部的字节子串——chunk 已被整体
		// append 进 result.buffered（含末尾不完整行），再 append remainder 会重复写入
		// 同一段字节，导致下游 SSE 事件被截断拼接（如 data: {"id":"X","odata: ...）。
		// 因此这里只重置 remainder，不再追加；buffered 已包含其全部内容。
		remainder = ""
	}

	// 启动 goroutine 读取 body chunk。preflight 放行后继续由同一个 channel 驱动正常流式转发，避免丢 chunk。
	chunkChan, bodyErrChan := common.StartBodyChunkReader(resp.Body, 32*1024, 16)

	// 阶段A：首个有效内容等待超时
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
	defer func() {
		if inactivityTimer != nil {
			inactivityTimer.Stop()
		}
	}()

	stopFirstContentTimer := func() {
		if firstContentTimer == nil {
			return
		}
		if !firstContentTimer.Stop() {
			select {
			case <-firstContentTimer.C:
			default:
			}
		}
		firstContentChan = nil
	}

	enterPhaseB := func() {
		if hasFirstContent {
			return
		}
		hasFirstContent = true
		stopFirstContentTimer()
		if timeouts.InactivityTimeoutMs > 0 {
			inactivityTimer = time.NewTimer(time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond)
			inactivityChan = inactivityTimer.C
		}
	}

	resetInactivityTimer := func() {
		if !hasFirstContent || inactivityTimer == nil {
			return
		}
		if !inactivityTimer.Stop() {
			select {
			case <-inactivityTimer.C:
			default:
			}
		}
		inactivityTimer.Reset(time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond)
	}

	hasPendingToolCall := func() bool {
		return tracker.HasPendingToolCall() || chatTracker.HasPendingToolCall()
	}

	for result.malformedToolName == "" && len(result.buffered) < maxPreflightBytes {
		var chunk []byte
		var chunkOk bool

		select {
		case chunk, chunkOk = <-chunkChan:
			if !chunkOk {
				// chunkChan 关闭：body 读取完成
				flushRemainder()
				return result, chunkChan, bodyErrChan, nil
			}
		case err := <-bodyErrChan:
			flushRemainder()
			return result, chunkChan, bodyErrChan, err
		case <-firstContentChan:
			// 阶段A超时：首个有效内容等待超时
			if timeouts.FirstContentTimeoutMs > 0 {
				flushRemainder()
				return result, chunkChan, bodyErrChan, common.ErrStreamFirstContentTimeout
			}
			// 超时被禁用（0），保守放行
			flushRemainder()
			return result, chunkChan, bodyErrChan, nil
		case <-inactivityChan:
			// 阶段B超时：首字后断流
			flushRemainder()
			return result, chunkChan, bodyErrChan, common.ErrStreamStalled
		}

		if !chunkOk {
			continue
		}

		result.buffered = append(result.buffered, chunk...)
		data := remainder + string(chunk)
		lines := strings.Split(data, "\n")
		remainder = lines[len(lines)-1]
		completeLines := lines[:len(lines)-1]

		for _, line := range completeLines {
			wasInPhaseB := hasFirstContent
			wasPendingToolCall := hasPendingToolCall()
			lineSet := []string{line}
			if malformed, name := detectMalformedChatStreamLines(lineSet, upstreamType, tracker, chatTracker); malformed {
				result.malformedToolName = name
				flushRemainder()
				break
			}

			hasSemanticContent := chatStreamHasSemanticContent(lineSet, upstreamType)
			hasDataActivity := chatStreamHasDataActivity(lineSet)

			if !hasFirstContent && hasSemanticContent {
				// 阶段A→阶段B：首次检测到有效语义内容。工具调用参数未完成也算首内容，但不能提前放行。
				if observer != nil {
					observer.MarkFirstContent(time.Now())
				}
				enterPhaseB()
				if timeouts.InactivityTimeoutMs <= 0 && !hasPendingToolCall() {
					flushRemainder()
					return result, chunkChan, bodyErrChan, nil
				}
			}

			if wasInPhaseB && hasDataActivity && !hasPendingToolCall() {
				// 阶段B中收到后续 SSE 活动且没有未完成工具调用：健康流，放行
				if observer != nil {
					if wasPendingToolCall {
						observer.MarkToolCallComplete(time.Now())
					} else {
						observer.MarkStreamActivity(time.Now())
					}
				}
				flushRemainder()
				return result, chunkChan, bodyErrChan, nil
			}
			if hasFirstContent && hasDataActivity && observer != nil {
				if hasPendingToolCall() {
					observer.MarkToolCallActivity(time.Now())
				} else if wasPendingToolCall {
					observer.MarkToolCallComplete(time.Now())
				} else {
					observer.MarkStreamActivity(time.Now())
				}
			}
		}
		if result.malformedToolName != "" {
			break
		}

		// 阶段B中收到任何 chunk 都重置不活动定时器，避免持续分片输出被误判断流。
		resetInactivityTimer()
	}

	flushRemainder()
	return result, chunkChan, bodyErrChan, nil
}

func detectMalformedChatStreamLines(lines []string, upstreamType string, tracker chatToolTracker, chatTracker *openAIChatToolCallTracker) (bool, string) {
	for _, line := range lines {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonData := strings.TrimPrefix(line, "data:")
		jsonData = strings.TrimPrefix(jsonData, " ")
		if strings.TrimSpace(jsonData) == "[DONE]" {
			continue
		}
		event := "data: " + jsonData + "\n\n"
		switch upstreamType {
		case "claude":
			if malformed, name := tracker.ProcessClaudeEvent(event); malformed {
				return true, name
			}
		case "responses":
			if malformed, name := tracker.ProcessResponsesEvent(event); malformed {
				return true, name
			}
		default:
			if malformed, name := chatTracker.ProcessLine(jsonData); malformed {
				return true, name
			}
		}
	}
	return false, ""
}

type openAIChatToolCallTracker struct {
	active map[int]*strings.Builder
	names  map[int]string
}

func newOpenAIChatToolCallTracker() *openAIChatToolCallTracker {
	return &openAIChatToolCallTracker{
		active: make(map[int]*strings.Builder),
		names:  make(map[int]string),
	}
}

func (t *openAIChatToolCallTracker) HasPendingToolCall() bool {
	return len(t.active) > 0
}

func (t *openAIChatToolCallTracker) ProcessLine(jsonData string) (bool, string) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return false, ""
	}

	choices, _ := data["choices"].([]interface{})
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]interface{})
		if finish, _ := choice["finish_reason"].(string); finish == "tool_calls" || finish == "function_call" {
			if finish == "function_call" {
				if builder := t.active[0]; builder != nil && common.IsMalformedToolArguments(builder.String()) && t.toolRequiresArguments(0) {
					return true, fallbackChatToolName(t.names[0], 0)
				}
			} else {
				for idx, builder := range t.active {
					if common.IsMalformedToolArguments(builder.String()) && t.toolRequiresArguments(idx) {
						return true, fallbackChatToolName(t.names[idx], idx)
					}
				}
			}
			t.active = make(map[int]*strings.Builder)
			t.names = make(map[int]string)
			continue
		}

		delta, _ := choice["delta"].(map[string]interface{})
		if functionCall, ok := delta["function_call"].(map[string]interface{}); ok {
			builder := t.ensure(0)
			if name, ok := functionCall["name"].(string); ok && name != "" {
				t.names[0] = name
			}
			if args, ok := functionCall["arguments"].(string); ok {
				builder.WriteString(args)
			}
		}
		if calls, ok := delta["tool_calls"].([]interface{}); ok {
			for _, rawCall := range calls {
				call, _ := rawCall.(map[string]interface{})
				idx := 0
				if fidx, ok := call["index"].(float64); ok {
					idx = int(fidx)
				}
				builder := t.ensure(idx)
				function, _ := call["function"].(map[string]interface{})
				if name, ok := function["name"].(string); ok && name != "" {
					t.names[idx] = name
				}
				if args, ok := function["arguments"].(string); ok {
					builder.WriteString(args)
				}
			}
		}
	}
	return false, ""
}

func (t *openAIChatToolCallTracker) ensure(index int) *strings.Builder {
	builder := t.active[index]
	if builder == nil {
		builder = &strings.Builder{}
		t.active[index] = builder
	}
	return builder
}

func (t *openAIChatToolCallTracker) toolRequiresArguments(index int) bool {
	name := strings.ToLower(strings.TrimSpace(t.names[index]))
	switch name {
	case "read", "edit", "write", "bash", "grep", "glob", "webfetch", "websearch":
		return true
	default:
		return false
	}
}

func fallbackChatToolName(name string, index int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("tool_%d", index)
}

func chatStreamHasSemanticContent(lines []string, upstreamType string) bool {
	for _, line := range lines {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonData := strings.TrimPrefix(line, "data:")
		jsonData = strings.TrimPrefix(jsonData, " ")
		if strings.TrimSpace(jsonData) == "[DONE]" {
			continue
		}
		event := "data: " + jsonData + "\n\n"
		if upstreamType == "claude" {
			if common.HasClaudeSemanticContent(event) {
				return true
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
				continue
			}
			if eventType, _ := data["type"].(string); eventType == "content_block_delta" {
				delta, _ := data["delta"].(map[string]interface{})
				if text, _ := delta["text"].(string); !common.IsEffectivelyEmptyStreamText(text) {
					return true
				}
			}
			continue
		}
		if upstreamType == "responses" {
			if common.HasResponsesSemanticContent(event) {
				return true
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
				continue
			}
			if eventType, _ := data["type"].(string); eventType == "response.output_text.delta" {
				if text, _ := data["delta"].(string); !common.IsEffectivelyEmptyStreamText(text) {
					return true
				}
			}
			continue
		}
		if common.HasOpenAIChatSemanticContent(event) {
			return true
		}
	}
	return false
}
