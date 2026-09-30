package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

// streamLineReader 从 chunk channel 逐行读取，支持超时，用于替换阻塞的 bufio.Scanner。
// 转换函数通过 NextLine 获取 SSE 行，调用方每次成功读到行后重置 deadline。
type streamLineReader struct {
	chunkChan <-chan []byte
	errChan   <-chan error
	remainder string
	err       error
	eof       bool
	observer  *common.StreamTimeoutObserver
}

func newStreamLineReader(chunkChan <-chan []byte, errChan <-chan error, observers ...*common.StreamTimeoutObserver) *streamLineReader {
	reader := &streamLineReader{chunkChan: chunkChan, errChan: errChan}
	if len(observers) > 0 {
		reader.observer = observers[0]
	}
	return reader
}

// NextLine 返回下一行 SSE 数据（不含末尾 \n）。
// 返回 timedOut=true 时调用方应立即返回 ErrStreamPostCommitStalled。
// 返回 eof=true 时流正常结束。
func (r *streamLineReader) NextLine(deadline time.Duration) (line string, eof bool, err error, timedOut bool) {
	for {
		// 优先返回已缓冲的完整行（包括 preflight 阶段缓存的行）
		if idx := strings.IndexByte(r.remainder, '\n'); idx >= 0 {
			line = r.remainder[:idx]
			r.remainder = r.remainder[idx+1:]
			r.markActivity(line)
			return line, false, nil, false
		}
		if r.err != nil {
			if r.remainder != "" {
				line = r.remainder
				r.remainder = ""
				r.markActivity(line)
				return line, false, r.err, false
			}
			return "", true, r.err, false
		}
		if r.eof {
			// 补发剩余数据
			if r.remainder != "" {
				line = r.remainder
				r.remainder = ""
				r.markActivity(line)
				return line, false, nil, false
			}
			return "", true, nil, false
		}

		timer := time.NewTimer(deadline)
		select {
		case chunk, ok := <-r.chunkChan:
			timer.Stop()
			if !ok {
				r.eof = true
				// 查看 errChan 是否有错误
				select {
				case e := <-r.errChan:
					r.err = e
				default:
				}
				if r.remainder != "" {
					line = r.remainder
					r.remainder = ""
					r.markActivity(line)
					return line, false, r.err, false
				}
				return "", true, r.err, false
			}
			r.remainder += string(chunk)
			// 提取整行
			if idx := strings.IndexByte(r.remainder, '\n'); idx >= 0 {
				line = r.remainder[:idx]
				r.remainder = r.remainder[idx+1:]
				r.markActivity(line)
				return line, false, nil, false
			}
			// 还没凑够一行，继续等下一块
		case e, ok := <-r.errChan:
			timer.Stop()
			if ok {
				r.err = e
			} else {
				r.err = errors.New("errChan closed unexpectedly")
			}
			// 返回已有数据
			if r.remainder != "" {
				line = r.remainder
				r.remainder = ""
				r.markActivity(line)
				return line, false, r.err, false
			}
			return "", true, r.err, false
		case <-timer.C:
			return "", false, nil, true
		}
	}
}

func (r *streamLineReader) markActivity(line string) {
	if r.observer == nil || strings.TrimSpace(line) == "" {
		return
	}
	r.observer.MarkStreamActivity(time.Now())
}

// preflightGeminiStream Gemini 流式预检测（两阶段：首字等待 + 首字后断流检测）
func preflightGeminiStream(resp *http.Response, upstreamType string, timeouts common.StreamPreflightTimeouts, observers ...*common.StreamTimeoutObserver) (bufferedLines []string, chunkChan <-chan []byte, bodyErrChan <-chan error, err error) {
	hasFirstContent := false
	var observer *common.StreamTimeoutObserver
	if len(observers) > 0 {
		observer = observers[0]
	}

	// 启动 goroutine 读取 body chunk。preflight 放行后继续由同一个 channel 驱动正常流式转发，避免丢 chunk。
	chunkChan, bodyErrChan = common.StartBodyChunkReader(resp.Body, 32*1024, 16, common.RequestDone(resp))

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

	// 检测内容的辅助函数
	hasSemanticContent := func(lines []string) bool {
		for _, line := range lines {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonData := strings.TrimPrefix(line, "data: ")
			if jsonData == "[DONE]" {
				continue
			}
			// Gemini 原生格式检测
			if hasGeminiSemanticContent(jsonData) {
				return true
			}
			// 思考类增量对 Gemini 客户端是可交付内容（转换后为 thought part），
			// 计入语义内容，避免纯思考流在预检中被判空。
			if hasGeminiDeliverableThinkingContent(jsonData, upstreamType) {
				return true
			}
			// 转换后格式检测（Claude/OpenAI/Responses → Gemini）
			switch upstreamType {
			case "claude":
				// Claude SSE 事件检测
				if hasClaudeSemanticContent(line) {
					return true
				}
			case "openai":
				// OpenAI Chat chunk 检测
				if hasOpenAISemanticContent(jsonData) {
					return true
				}
			case "responses":
				// Responses event 检测
				if hasResponsesSemanticContent(line) {
					return true
				}
			}
		}
		return false
	}

	var remainder string
	var allLines []string

	for {
		var chunk []byte
		var chunkOk bool

		select {
		case chunk, chunkOk = <-chunkChan:
			if !chunkOk {
				// chunkChan 关闭：body 读取完成。
				// 整个预检期间从未检测到语义内容（usage-only / 空流）：Header 未写，
				// 按空响应进入 failover（对齐 messages/responses 语义）。
				if remainder != "" {
					allLines = append(allLines, remainder)
				}
				if !hasFirstContent {
					return allLines, chunkChan, bodyErrChan, common.ErrEmptyStreamResponse
				}
				return allLines, chunkChan, bodyErrChan, nil
			}
		case err := <-bodyErrChan:
			return nil, chunkChan, bodyErrChan, err
		case <-firstContentChan:
			// 阶段A超时
			if timeouts.FirstContentTimeoutMs > 0 {
				return nil, chunkChan, bodyErrChan, common.ErrStreamFirstContentTimeout
			}
			// 超时被禁用（0），保守放行
			return nil, chunkChan, bodyErrChan, nil
		case <-inactivityChan:
			// 阶段B超时：首字后断流
			return nil, nil, nil, common.ErrStreamStalled
		}

		if !chunkOk {
			continue
		}

		data := remainder + string(chunk)
		lines := strings.Split(data, "\n")
		remainder = lines[len(lines)-1]
		completeLines := lines[:len(lines)-1]
		allLines = append(allLines, completeLines...)

		// 流内错误事件检测：HTTP 200 但 data 里携带错误对象（中转站/原生错误形态）。
		// 拉黑类错误返回 ErrBlacklistKey；请求侧确定性错误（invalid_request /
		// 上下文超长等）返回终态错误，不做逐 Key 重放；其余错误按空响应语义
		// 进入 failover，对齐 messages/responses 的预检行为，不再原样透传给客户端。
		for _, line := range completeLines {
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if reason, msg, recoverAt, isErr := common.DetectStreamDataError(jsonData); isErr {
				if reason != "" {
					return nil, chunkChan, bodyErrChan, &common.ErrBlacklistKey{Reason: reason, Message: msg, RecoverAt: recoverAt}
				}
				if rejected := common.RequestSideStreamError(jsonData); rejected != nil {
					return nil, chunkChan, bodyErrChan, rejected
				}
				return nil, chunkChan, bodyErrChan, fmt.Errorf("%w: %s", common.ErrEmptyStreamResponse, msg)
			}
		}

		if hasSemanticContent(completeLines) {
			if !hasFirstContent {
				// 阶段A→阶段B
				if observer != nil {
					observer.MarkFirstContent(time.Now())
				}
				hasFirstContent = true
				if firstContentTimer != nil {
					firstContentTimer.Stop()
				}
				if timeouts.InactivityTimeoutMs > 0 {
					inactivityTimer = time.NewTimer(time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond)
					inactivityChan = inactivityTimer.C
					defer inactivityTimer.Stop()
				} else {
					// 禁用阶段B，直接放行
					if remainder != "" {
						allLines = append(allLines, remainder)
					}
					return allLines, chunkChan, bodyErrChan, nil
				}
			} else {
				// 阶段B中收到第二个有效内容：健康流，放行
				if observer != nil {
					observer.MarkStreamActivity(time.Now())
				}
				if remainder != "" {
					allLines = append(allLines, remainder)
				}
				return allLines, chunkChan, bodyErrChan, nil
			}
		}

		// 阶段B中重置不活动定时器
		if hasFirstContent && inactivityTimer != nil {
			if !inactivityTimer.Stop() {
				select {
				case <-inactivityTimer.C:
				default:
				}
			}
			inactivityTimer.Reset(time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond)
		}
	}
}

// hasGeminiDeliverableThinkingContent 检测转换上游**确实会被转换器交付**的思考类增量。
// Gemini 客户端可把思考交付为 thought part（含纯思考流），因此这类增量计入语义内容，
// 避免思考流在预检空判定中被误判。
//
// 判定必须与三个转换器的实现严格一致：只有真正写出 thought part 的事件才算可交付，
// 否则预检会放行并写出空 200，取代本应发生的 failover。
//   - claude:    streamClaudeToGemini 只处理 content_block_delta.thinking_delta 且 thinking 非空
//   - openai:    streamOpenAIToGemini 只处理 delta.reasoning_content / delta.reasoning 非空
//   - responses: ConvertResponsesToGeminiStream 只处理 response.reasoning_summary_text.delta 且 text 非空
//
// 反例（转换器零输出，不得计入）：redacted_thinking 的 content_block_start（无正文）、
// response.reasoning_summary_part.added/done、response.reasoning_summary_text.done、
// response.reasoning_text.delta/done 等完成态或结构态事件。
func hasGeminiDeliverableThinkingContent(jsonData string, upstreamType string) bool {
	switch upstreamType {
	case "claude":
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type     string `json:"type"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			return false
		}
		return event.Type == "content_block_delta" &&
			event.Delta.Type == "thinking_delta" &&
			event.Delta.Thinking != ""
	case "openai":
		var event struct {
			Choices []struct {
				Delta struct {
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			return false
		}
		if len(event.Choices) == 0 {
			return false
		}
		delta := event.Choices[0].Delta
		return delta.ReasoningContent != "" || delta.Reasoning != ""
	case "responses":
		var event struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			return false
		}
		return event.Type == "response.reasoning_summary_text.delta" && event.Text != ""
	}
	return false
}

// hasGeminiSemanticContent 检测 Gemini 原生格式的语义内容
func hasGeminiSemanticContent(jsonData string) bool {
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		return false
	}
	// 检测 text content
	if candidates, ok := event["candidates"].([]interface{}); ok && len(candidates) > 0 {
		if candidate, ok := candidates[0].(map[string]interface{}); ok {
			if content, ok := candidate["content"].(map[string]interface{}); ok {
				if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
					for _, part := range parts {
						if p, ok := part.(map[string]interface{}); ok {
							if text, ok := p["text"].(string); ok && text != "" {
								return true
							}
							if _, ok := p["functionCall"]; ok {
								return true
							}
							if _, ok := p["thought"]; ok {
								return true
							}
						}
					}
				}
			}
		}
	}
	return false
}

// hasClaudeSemanticContent 检测 Claude SSE 事件的语义内容
func hasClaudeSemanticContent(line string) bool {
	// Claude 格式: event: xxx\ndata: {...}
	// 简化检测：查找 text delta 或 tool_use
	if strings.Contains(line, "content_block_delta") && strings.Contains(line, "text") {
		return true
	}
	if strings.Contains(line, "tool_use") {
		return true
	}
	return false
}

// hasOpenAISemanticContent 检测 OpenAI Chat chunk 的语义内容
func hasOpenAISemanticContent(jsonData string) bool {
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		return false
	}
	if choices, ok := event["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if delta, ok := choice["delta"].(map[string]interface{}); ok {
				if content, ok := delta["content"].(string); ok && content != "" {
					return true
				}
				if _, ok := delta["tool_calls"]; ok {
					return true
				}
			}
		}
	}
	return false
}

// hasResponsesSemanticContent 检测 Responses event 的语义内容
func hasResponsesSemanticContent(line string) bool {
	// Responses 格式: data: {...}
	if strings.Contains(line, "response.output_text.delta") {
		return true
	}
	if strings.Contains(line, "response.function_call") {
		return true
	}
	return false
}

// handleStreamSuccess 处理流式响应
func handleStreamSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	model string,
	timeouts common.StreamPreflightTimeouts,
) (*types.Usage, error) {
	// Preflight：在发送 HTTP Header 之前检测流是否可用
	bufferedLines, chunkChan, bodyErrChan, err := preflightGeminiStream(resp, upstreamType, timeouts, common.GetStreamTimeoutObserver(c))
	if err != nil {
		if errors.Is(err, common.ErrStreamFirstContentTimeout) {
			common.RequestLogf(c, "[Gemini-FirstContentTimeout] 流式首字超时: %dms，触发重试", timeouts.FirstContentTimeoutMs)
		} else if errors.Is(err, common.ErrStreamStalled) {
			common.RequestLogf(c, "[Gemini-StreamStalled] 流式断流: 首字后 %dms 无活动，触发重试", timeouts.InactivityTimeoutMs)
		}
		// 预检阶段的任何非客户端错误 Header 均未写：统一归入 failover 家族，
		// 避免裸传输错误/上游错误终结整个 failover 链。
		return nil, common.ClassifyPreflightStreamError(err)
	}

	// 检查是否为空响应
	if len(bufferedLines) == 0 {
		return nil, common.ErrEmptyStreamResponse
	}

	// 竞速提交闸门：preflight 确认首字有效后才裁决——赢家 claim 并写出，
	// 败者在此返回 ErrRacingSuperseded（Header 未写，零字节污染）。
	// 流式路径漏掉此裁决时，分支 writer 的缓冲内容永远不会 Commit 到
	// 真实客户端 writer，客户端只会拿到空 200。ForStream 版带伪工具调用
	// 标记软校验（tool_choice=auto 文本化工具调用的分支让出提交权）。
	if !common.RacingClaimClientCommitForStream(c, strings.Join(bufferedLines, "\n")) {
		return nil, common.ErrRacingSuperseded
	}

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		common.RequestLogf(c, "[Gemini-Stream] 警告: ResponseWriter 不支持 Flusher")
	}

	var totalUsage *types.Usage
	var streamErr error
	logBuffer := common.NewLimitedLogBuffer(common.MaxUpstreamResponseLogBytes)
	streamLoggingEnabled := envCfg.EnableResponseLogs && envCfg.IsDevelopment()

	common.LogUpstreamResponseHeaders(c, resp, envCfg, "Gemini")

	// 回放缓冲的行，然后继续读取原始上游 body
	bufferedBody := strings.Join(bufferedLines, "\n")
	if bufferedBody != "" && !strings.HasSuffix(bufferedBody, "\n") {
		bufferedBody += "\n"
	}
	reader := newStreamLineReader(chunkChan, bodyErrChan, common.GetStreamTimeoutObserver(c))
	reader.remainder = bufferedBody
	progress := common.NewStreamProgressLogger("Gemini", startTime, envCfg.ShouldLog("info"), common.RequestLogTag(c))

	switch upstreamType {
	case "gemini":
		totalUsage, streamErr = streamGeminiToGemini(c, reader, flusher, logBuffer, streamLoggingEnabled, timeouts, progress)
	case "claude":
		totalUsage, streamErr = streamClaudeToGemini(c, reader, flusher, model, logBuffer, streamLoggingEnabled, timeouts, progress)
	case "openai":
		totalUsage, streamErr = streamOpenAIToGemini(c, reader, flusher, model, logBuffer, streamLoggingEnabled, timeouts, progress)
	case "responses":
		totalUsage, streamErr = streamResponsesToGemini(c, reader, flusher, model, logBuffer, streamLoggingEnabled, timeouts, progress)
	default:
		// 默认按 Gemini 直通处理
		totalUsage, streamErr = streamGeminiToGemini(c, reader, flusher, logBuffer, streamLoggingEnabled, timeouts, progress)
	}
	if streamErr != nil {
		return nil, streamErr
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		common.RequestLogf(c, "[Gemini-Stream-Timing] 流式响应完成: %dms", responseTime)
		if logBuffer.Len() > 0 {
			common.RequestLogf(c, "[Gemini-Stream] 上游流式响应原始内容:\n%s", logBuffer.String())
		}
	}

	return totalUsage, nil
}

// streamGeminiToGemini Gemini 上游直接透传
func streamGeminiToGemini(
	c *gin.Context,
	reader *streamLineReader,
	flusher http.Flusher,
	logBuffer *common.LimitedLogBuffer,
	loggingEnabled bool,
	timeouts common.StreamPreflightTimeouts,
	progress *common.StreamProgressLogger,
) (*types.Usage, error) {
	var totalUsage *types.Usage
	// lastWasDataLine 记录上一次写出的是否是 data 行：上游以单换行收尾时，
	// EOF 需要再补一个空行才算把最后一个事件派发给客户端。
	lastWasDataLine := false
	inactivityTimeout := time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond

	for {
		line, eof, err, timedOut := reader.NextLine(inactivityTimeout)
		if timedOut {
			progress.Finish("stalled")
			return nil, common.ErrStreamPostCommitStalled
		}
		if eof {
			if lastWasDataLine {
				// 上游最后一个 data 行只有单个 "\n"（没有空行）：补空行完成事件派发，
				// 否则严格 SSE 解析器会丢掉最后一帧（finish / usage / [DONE]）。
				_, _ = fmt.Fprint(c.Writer, "\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
			if err != nil {
				progress.Finish("error")
				return totalUsage, err
			}
			progress.Finish("completed")
			return totalUsage, nil
		}
		progress.AddBytes(len(line))
		progress.Tick()
		if loggingEnabled {
			_, _ = logBuffer.WriteString(line + "\n")
		}

		// 直接转发 SSE 数据
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")

			// 尝试解析 usage
			var chunk types.GeminiStreamChunk
			if err := json.Unmarshal([]byte(jsonData), &chunk); err == nil {
				if chunk.UsageMetadata != nil {
					totalUsage = &types.Usage{
						InputTokens:  chunk.UsageMetadata.PromptTokenCount - chunk.UsageMetadata.CachedContentTokenCount,
						OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
					}
				}
			}
			lastWasDataLine = true
			_, _ = fmt.Fprintf(c.Writer, "%s\n", line)
		} else if line != "" {
			lastWasDataLine = false
			_, _ = fmt.Fprintf(c.Writer, "%s\n", line)
		} else {
			lastWasDataLine = false
			_, _ = fmt.Fprintf(c.Writer, "\n")
		}

		if flusher != nil {
			flusher.Flush()
		}
	}
}

// streamClaudeToGemini Claude 流式响应转换为 Gemini 格式
func streamClaudeToGemini(
	c *gin.Context,
	reader *streamLineReader,
	flusher http.Flusher,
	model string,
	logBuffer *common.LimitedLogBuffer,
	loggingEnabled bool,
	timeouts common.StreamPreflightTimeouts,
	progress *common.StreamProgressLogger,
) (*types.Usage, error) {
	var totalUsage *types.Usage
	var currentText strings.Builder
	inactivityTimeout := time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond

	for {
		line, eof, err, timedOut := reader.NextLine(inactivityTimeout)
		if timedOut {
			progress.Finish("stalled")
			return nil, common.ErrStreamPostCommitStalled
		}
		if eof {
			if err != nil {
				progress.Finish("error")
				return totalUsage, err
			}
			progress.Finish("completed")
			return totalUsage, nil
		}
		progress.AddBytes(len(line))
		progress.Tick()
		if loggingEnabled {
			_, _ = logBuffer.WriteString(line + "\n")
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonData := strings.TrimPrefix(line, "data: ")
		if jsonData == "[DONE]" {
			break
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "content_block_delta":
			// 文本增量
			delta, ok := event["delta"].(map[string]interface{})
			if !ok {
				continue
			}
			deltaType, _ := delta["type"].(string)
			switch deltaType {
			case "thinking_delta":
				thinking, _ := delta["thinking"].(string)
				if thinking == "" {
					continue
				}
				geminiChunk := types.GeminiStreamChunk{
					Candidates: []types.GeminiCandidate{
						{
							Content: &types.GeminiContent{
								Parts: []types.GeminiPart{
									{Text: thinking, Thought: true},
								},
								Role: "model",
							},
						},
					},
				}
				chunkBytes, _ := json.Marshal(geminiChunk)
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
				if flusher != nil {
					flusher.Flush()
				}
			case "text_delta":
				text, _ := delta["text"].(string)
				currentText.WriteString(text)

				// 转换为 Gemini 格式
				geminiChunk := types.GeminiStreamChunk{
					Candidates: []types.GeminiCandidate{
						{
							Content: &types.GeminiContent{
								Parts: []types.GeminiPart{
									{Text: text},
								},
								Role: "model",
							},
						},
					},
				}

				chunkBytes, _ := json.Marshal(geminiChunk)
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
				if flusher != nil {
					flusher.Flush()
				}
			}

		case "message_delta":
			// 消息完成，包含 usage
			if usage, ok := event["usage"].(map[string]interface{}); ok {
				inputTokens := 0
				outputTokens := 0
				if v, ok := usage["input_tokens"].(float64); ok {
					inputTokens = int(v)
				}
				if v, ok := usage["output_tokens"].(float64); ok {
					outputTokens = int(v)
				}
				totalUsage = &types.Usage{
					InputTokens:  inputTokens,
					OutputTokens: outputTokens,
				}

				// 发送带 finishReason 和 usage 的最终块
				geminiChunk := types.GeminiStreamChunk{
					Candidates: []types.GeminiCandidate{
						{
							FinishReason: "STOP",
						},
					},
					UsageMetadata: &types.GeminiUsageMetadata{
						PromptTokenCount:     inputTokens,
						CandidatesTokenCount: outputTokens,
						TotalTokenCount:      inputTokens + outputTokens,
					},
				}
				chunkBytes, _ := json.Marshal(geminiChunk)
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}
	progress.Finish("completed")
	return totalUsage, nil
}

// streamOpenAIToGemini OpenAI 流式响应转换为 Gemini 格式
func streamOpenAIToGemini(
	c *gin.Context,
	reader *streamLineReader,
	flusher http.Flusher,
	model string,
	logBuffer *common.LimitedLogBuffer,
	loggingEnabled bool,
	timeouts common.StreamPreflightTimeouts,
	progress *common.StreamProgressLogger,
) (*types.Usage, error) {
	var totalUsage *types.Usage
	var currentText strings.Builder
	inactivityTimeout := time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond

	for {
		line, eof, err, timedOut := reader.NextLine(inactivityTimeout)
		if timedOut {
			progress.Finish("stalled")
			return nil, common.ErrStreamPostCommitStalled
		}
		if eof {
			if err != nil {
				progress.Finish("error")
				return totalUsage, err
			}
			progress.Finish("completed")
			return totalUsage, nil
		}
		progress.AddBytes(len(line))
		progress.Tick()
		if loggingEnabled {
			_, _ = logBuffer.WriteString(line + "\n")
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonData := strings.TrimPrefix(line, "data: ")
		if jsonData == "[DONE]" {
			break
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
			continue
		}

		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			// 检查是否有 usage（某些 OpenAI 兼容 API 在最后发送）
			if usage, ok := chunk["usage"].(map[string]interface{}); ok {
				promptTokens := 0
				completionTokens := 0
				if v, ok := usage["prompt_tokens"].(float64); ok {
					promptTokens = int(v)
				}
				if v, ok := usage["completion_tokens"].(float64); ok {
					completionTokens = int(v)
				}
				totalUsage = &types.Usage{
					InputTokens:  promptTokens,
					OutputTokens: completionTokens,
				}

				// 发送带 usage 的最终块
				geminiChunk := types.GeminiStreamChunk{
					UsageMetadata: &types.GeminiUsageMetadata{
						PromptTokenCount:     promptTokens,
						CandidatesTokenCount: completionTokens,
						TotalTokenCount:      promptTokens + completionTokens,
					},
				}
				chunkBytes, _ := json.Marshal(geminiChunk)
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
				if flusher != nil {
					flusher.Flush()
				}
			}
			continue
		}

		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}

		// 检查 finish_reason
		finishReason, hasFinish := choice["finish_reason"].(string)

		// 获取 delta
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			if hasFinish && finishReason != "" {
				// 发送 finishReason
				geminiFinishReason := openaiFinishReasonToGemini(finishReason)
				geminiChunk := types.GeminiStreamChunk{
					Candidates: []types.GeminiCandidate{
						{
							FinishReason: geminiFinishReason,
						},
					},
				}
				chunkBytes, _ := json.Marshal(geminiChunk)
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
				if flusher != nil {
					flusher.Flush()
				}
			}
			continue
		}

		// 提取文本内容
		reasoning, _ := delta["reasoning_content"].(string)
		if reasoning == "" {
			reasoning, _ = delta["reasoning"].(string)
		}
		if reasoning != "" {
			geminiChunk := types.GeminiStreamChunk{
				Candidates: []types.GeminiCandidate{
					{
						Content: &types.GeminiContent{
							Parts: []types.GeminiPart{
								{Text: reasoning, Thought: true},
							},
							Role: "model",
						},
					},
				},
			}

			chunkBytes, _ := json.Marshal(geminiChunk)
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
			if flusher != nil {
				flusher.Flush()
			}
		}

		content, _ := delta["content"].(string)
		if content != "" {
			currentText.WriteString(content)

			geminiChunk := types.GeminiStreamChunk{
				Candidates: []types.GeminiCandidate{
					{
						Content: &types.GeminiContent{
							Parts: []types.GeminiPart{
								{Text: content},
							},
							Role: "model",
						},
					},
				},
			}

			chunkBytes, _ := json.Marshal(geminiChunk)
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
			if flusher != nil {
				flusher.Flush()
			}
		}

		// 如果有 finish_reason，发送
		if hasFinish && finishReason != "" {
			geminiFinishReason := openaiFinishReasonToGemini(finishReason)
			geminiChunk := types.GeminiStreamChunk{
				Candidates: []types.GeminiCandidate{
					{
						FinishReason: geminiFinishReason,
					},
				},
			}
			chunkBytes, _ := json.Marshal(geminiChunk)
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	progress.Finish("completed")
	return totalUsage, nil
}

// openaiFinishReasonToGemini 将 OpenAI 停止原因转换为 Gemini 格式
func openaiFinishReasonToGemini(finishReason string) string {
	switch finishReason {
	case "stop":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "tool_calls":
		return "STOP"
	case "content_filter":
		return "SAFETY"
	default:
		return "STOP"
	}
}

// streamResponsesToGemini Responses 流式响应转换为 Gemini 格式
func streamResponsesToGemini(
	c *gin.Context,
	reader *streamLineReader,
	flusher http.Flusher,
	model string,
	logBuffer *common.LimitedLogBuffer,
	loggingEnabled bool,
	timeouts common.StreamPreflightTimeouts,
	progress *common.StreamProgressLogger,
) (*types.Usage, error) {
	var totalUsage *types.Usage
	var converterState any
	inactivityTimeout := time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond

	for {
		line, eof, err, timedOut := reader.NextLine(inactivityTimeout)
		if timedOut {
			progress.Finish("stalled")
			return nil, common.ErrStreamPostCommitStalled
		}
		if eof {
			if err != nil {
				progress.Finish("error")
				return totalUsage, err
			}
			progress.Finish("completed")
			return totalUsage, nil
		}
		progress.AddBytes(len(line))
		progress.Tick()
		if loggingEnabled {
			_, _ = logBuffer.WriteString(line + "\n")
		}
		if line == "" {
			continue
		}

		// 使用转换器将 Responses SSE 转换为 Gemini SSE
		events := converters.ConvertResponsesToGeminiStream(
			c.Request.Context(),
			model,
			[]byte(line),
			&converterState,
		)

		for _, event := range events {
			// 尝试从事件中提取 usage
			if strings.HasPrefix(event, "data: ") {
				jsonData := strings.TrimPrefix(event, "data: ")
				jsonData = strings.TrimSuffix(jsonData, "\n\n")
				var chunk types.GeminiStreamChunk
				if err := json.Unmarshal([]byte(jsonData), &chunk); err == nil {
					if chunk.UsageMetadata != nil {
						totalUsage = &types.Usage{
							InputTokens:  chunk.UsageMetadata.PromptTokenCount - chunk.UsageMetadata.CachedContentTokenCount,
							OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
						}
					}
				}
			}
			_, _ = fmt.Fprint(c.Writer, event)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}
