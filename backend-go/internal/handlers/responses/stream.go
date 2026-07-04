package responses

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/thinkingcache"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

func handleStreamSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	sessionManager *session.SessionManager,
	startTime time.Time,
	originalReq *types.ResponsesRequest,
	originalRequestJSON []byte,
	timeouts common.StreamPreflightTimeouts,
) (*types.Usage, error) {
	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		common.RequestLogf(c, "[Responses-Stream] Responses 流式响应开始: %dms, 状态: %d", responseTime, resp.StatusCode)
		common.LogUpstreamResponseHeaders(c, resp, envCfg, "Responses")
	}

	var synthesizer *utils.StreamSynthesizer
	logBuffer := common.NewLimitedLogBuffer(common.MaxUpstreamResponseLogBytes)
	streamLoggingEnabled := envCfg.IsDevelopment() && envCfg.EnableResponseLogs

	if streamLoggingEnabled {
		synthesizer = utils.NewStreamSynthesizer(upstreamType)
	}

	needConvert := upstreamType != "responses"
	var converterState any
	isCompactionV2Stream := originalReq != nil && hasCompactionTrigger(originalReq.Input)

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, utils.ResponsesSSEScannerMaxBufferSize)

	// 预检测：在发送 HTTP Header 之前缓冲行并检查是否为空响应
	// 使用 goroutine + channel 实现真正的超时控制（scanner.Scan 是阻塞调用）
	type scanLine struct {
		text string
		ok   bool
	}
	lineChan := make(chan scanLine, 1)
	scanDone := make(chan struct{})
	go func() {
		defer close(lineChan)
		for scanner.Scan() {
			select {
			case lineChan <- scanLine{text: normalizeResponsesSSEFieldLine(scanner.Text()), ok: true}:
			case <-scanDone:
				return
			}
		}
		select {
		case lineChan <- scanLine{ok: false}: // scanner 结束
		case <-scanDone:
		}
	}()

	var bufferedLines []string
	var preflightTextBuf bytes.Buffer
	preflightToolTracker := common.NewStreamToolCallTracker()
	streamObserver := common.GetStreamTimeoutObserver(c)
	preflightHasNonTextContent := false
	preflightEmpty := false
	preflightDiagnostic := ""
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
	hasFirstContent := false
	preflightDone := false
	var blacklistReason, blacklistMessage string
	seenConvertedEvent := false
	seenCompletedEvent := false
	seenUsageOnlyEvent := false
	seenUnknownEvent := false
	unknownEventType := ""
	currentSSEEventName := ""

	// enterPhaseB 进入阶段B（首字后连续性确认）
	enterPhaseB := func() {
		if !hasFirstContent {
			hasFirstContent = true
			if firstContentTimer != nil {
				firstContentTimer.Stop()
			}
			if timeouts.InactivityTimeoutMs > 0 {
				inactivityTimer = time.NewTimer(time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond)
				inactivityChan = inactivityTimer.C
			}
		}
	}

	// resetInactivityTimer 重置阶段B不活动定时器
	resetInactivityTimer := func() {
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

	for !preflightDone {
		select {
		case sl := <-lineChan:
			if !sl.ok {
				// scanner 结束
				if preflightHasNonTextContent {
					preflightEmpty = false
				} else {
					preflightEmpty = common.IsEffectivelyEmptyStreamText(preflightTextBuf.String())
					if preflightEmpty && isCompactionV2UsageOnlyStream(isCompactionV2Stream, seenCompletedEvent, seenUsageOnlyEvent) {
						preflightEmpty = false
					}
				}
				preflightDiagnostic = buildResponsesPreflightDiagnostic(seenConvertedEvent, seenCompletedEvent, seenUsageOnlyEvent, seenUnknownEvent, unknownEventType, preflightTextBuf.String())
				preflightDone = true
				break
			}
			line := sl.text
			bufferedLines = append(bufferedLines, line)
			if strings.TrimSpace(line) == "" {
				currentSSEEventName = ""
			} else if strings.HasPrefix(line, "event:") {
				currentSSEEventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}

			// 检测 SSE error 事件中的拉黑条件
			if blacklistReason == "" {
				if r, m := common.DetectStreamBlacklistError(line + "\n"); r != "" {
					blacklistReason = r
					blacklistMessage = m
				}
			}

			// 处理转换后的事件用于文本提取
			var eventsToCheck []string
			if needConvert {
				switch upstreamType {
				case "claude":
					eventsToCheck = converters.ConvertClaudeMessagesToResponses(
						c.Request.Context(),
						originalReq.Model,
						originalRequestJSON,
						nil,
						[]byte(line),
						&converterState,
					)
				case "gemini":
					eventsToCheck = converters.ConvertGeminiStreamToResponses(
						c.Request.Context(),
						originalReq.Model,
						originalRequestJSON,
						nil,
						[]byte(line),
						&converterState,
					)
				default:
					eventsToCheck = converters.ConvertOpenAIChatToResponses(
						c.Request.Context(),
						originalReq.Model,
						originalRequestJSON,
						nil,
						[]byte(line),
						&converterState,
					)
				}
			} else {
				eventsToCheck = []string{line + "\n"}
			}

			for _, event := range eventsToCheck {
				seenConvertedEvent = true
				if upstreamErr, ok := detectResponsesStreamError(event, currentSSEEventName); ok {
					preflightDiagnostic = formatResponsesErrorDiagnostic(upstreamErr)
					close(scanDone)
					if r, m := detectResponsesErrorBlacklist(upstreamErr); r != "" {
						return nil, &common.ErrBlacklistKey{Reason: r, Message: m}
					}
					if isRetryableResponsesError(upstreamErr) {
						common.RequestLogf(c, "[Responses-UpstreamError] %s，触发重试", preflightDiagnostic)
						return nil, fmt.Errorf("%w: %s", common.ErrEmptyStreamResponse, preflightDiagnostic)
					}
					return nil, fmt.Errorf("upstream Responses error: %s", preflightDiagnostic)
				}
				hadPendingToolCall := preflightToolTracker.HasPendingToolCall()
				if malformed, name := preflightToolTracker.ProcessResponsesEvent(event); malformed {
					preflightEmpty = true
					preflightDiagnostic = fmt.Sprintf("malformed tool call: %s", name)
					preflightDone = true
					break
				}
				seenCompletedEvent = seenCompletedEvent || isResponsesCompletedEvent(event)
				seenUsageOnlyEvent = seenUsageOnlyEvent || isResponsesUsageOnlyEvent(event)
				if t, ok := firstUnknownResponsesEventType(event); ok {
					seenUnknownEvent = true
					if unknownEventType == "" {
						unknownEventType = t
					}
				}

				if !preflightHasNonTextContent && common.HasResponsesSemanticContent(event) && !preflightToolTracker.HasPendingToolCall() {
					preflightHasNonTextContent = true
					preflightEmpty = false
					// 进入阶段B，不立即放行
					if streamObserver != nil {
						streamObserver.MarkFirstContent(time.Now())
					}
					enterPhaseB()
					if timeouts.InactivityTimeoutMs <= 0 {
						preflightDone = true
						break
					}
					resetInactivityTimer()
					continue
				}

				extractResponsesTextFromEvent(event, &preflightTextBuf)

				// 检查是否有有效内容 delta 事件
				if !common.IsEffectivelyEmptyStreamText(preflightTextBuf.String()) {
					if !hasFirstContent {
						// 阶段A→阶段B：首次检测到有效文本内容
						if streamObserver != nil {
							streamObserver.MarkFirstContent(time.Now())
						}
						enterPhaseB()
						if timeouts.InactivityTimeoutMs <= 0 {
							preflightDone = true
							break
						}
						resetInactivityTimer()
					} else {
						// 阶段B中收到第二个有效内容：健康流，放行
						if streamObserver != nil {
							streamObserver.MarkStreamActivity(time.Now())
						}
						preflightDone = true
						break
					}
					continue
				}

				// 检查是否为 response.completed 事件（流正常结束）
				if isResponsesCompletedEvent(event) {
					preflightDone = true
					// 检查是否有实际内容（文本或工具调用）
					preflightEmpty = !preflightHasNonTextContent && common.IsEffectivelyEmptyStreamText(preflightTextBuf.String())
					// 如果有工具调用，不算空响应
					if preflightEmpty && hasResponsesFunctionCall(event) {
						preflightEmpty = false
					}
					if preflightEmpty && isCompactionV2UsageOnlyStream(isCompactionV2Stream, true, seenUsageOnlyEvent) {
						preflightEmpty = false
					}
					preflightDiagnostic = buildResponsesPreflightDiagnostic(seenConvertedEvent, true, seenUsageOnlyEvent, seenUnknownEvent, unknownEventType, preflightTextBuf.String())
					break
				}
				if hasFirstContent && streamObserver != nil {
					if preflightToolTracker.HasPendingToolCall() {
						streamObserver.MarkToolCallActivity(time.Now())
					} else if hadPendingToolCall {
						streamObserver.MarkToolCallComplete(time.Now())
					} else if common.HasStreamEventActivity(event) {
						streamObserver.MarkStreamActivity(time.Now())
					}
				}
			}

			// 阶段B中重置不活动定时器
			resetInactivityTimer()

		case <-firstContentChan:
			// 阶段A超时：首个有效内容等待超时
			if timeouts.FirstContentTimeoutMs > 0 {
				common.RequestLogf(c, "[Responses-FirstContentTimeout] 流式首字超时: %dms，触发重试", timeouts.FirstContentTimeoutMs)
				close(scanDone)
				return nil, common.ErrStreamFirstContentTimeout
			}
			// 超时被禁用（0），保守放行
			preflightDone = true

		case <-inactivityChan:
			// 阶段B超时：首字后断流
			common.RequestLogf(c, "[Responses-StreamStalled] 流式断流: 首字后 %dms 无活动，触发重试", timeouts.InactivityTimeoutMs)
			close(scanDone)
			return nil, common.ErrStreamStalled
		}
	}
	if inactivityTimer != nil {
		inactivityTimer.Stop()
	}

	// 空响应：Header 未发送，可安全重试
	if preflightEmpty {
		common.RequestLogf(c, "[Responses-EmptyResponse] 上游返回空响应 (缓冲行数: %d, 诊断: %s)，触发重试", len(bufferedLines), preflightDiagnostic)
		close(scanDone) // 通知 scanner goroutine 退出
		if blacklistReason != "" {
			return nil, &common.ErrBlacklistKey{Reason: blacklistReason, Message: blacklistMessage}
		}
		return nil, common.ErrEmptyStreamResponse
	}

	// 流中有拉黑错误但内容非空：仍返回拉黑错误以触发 Key 拉黑
	if blacklistReason != "" {
		close(scanDone)
		return nil, &common.ErrBlacklistKey{Reason: blacklistReason, Message: blacklistMessage}
	}

	// 非空响应：发送 Header 并回放缓冲行
	// 重置 converterState 以便回放时重新转换
	converterState = nil

	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	c.Status(resp.StatusCode)
	flusher, _ := c.Writer.(http.Flusher)

	// Token 统计状态
	var outputTextBuffer bytes.Buffer
	const maxOutputBufferSize = 1024 * 1024 // 1MB 上限，防止内存溢出
	var collectedUsage responsesStreamUsage
	hasUsage := false
	needTokenPatch := false
	clientGone := false
	promptTokensTotal := 0
	completedEventSent := false
	eventsSentCount := 0
	progress := common.NewStreamProgressLogger("Responses", startTime, envCfg.ShouldLog("info"), common.RequestLogTag(c))

	// 收集流式 output items（含 reasoning 的 encrypted_content）用于 session 回写
	outputCollector := newStreamOutputCollector()
	// 收集 reasoning encrypted_content 到 thinkingcache，供未来续接重放使用
	reasoningCollector := thinkingcache.NewResponsesStreamCollector()

	// processLine 处理单行数据（复用于缓冲行回放和后续读取），并返回转换后的 Responses 事件用于 watchdog 状态判断。
	processLine := func(line string) []string {

		if streamLoggingEnabled {
			logBuffer.WriteString(line + "\n")
			if synthesizer != nil {
				synthesizer.ProcessLine(line)
			}
		}

		// 处理转换后的事件
		var eventsToProcess []string

		if needConvert {
			var events []string
			switch upstreamType {
			case "claude":
				events = converters.ConvertClaudeMessagesToResponses(
					c.Request.Context(),
					originalReq.Model,
					originalRequestJSON,
					nil,
					[]byte(line),
					&converterState,
				)
			case "gemini":
				events = converters.ConvertGeminiStreamToResponses(
					c.Request.Context(),
					originalReq.Model,
					originalRequestJSON,
					nil,
					[]byte(line),
					&converterState,
				)
			default:
				events = converters.ConvertOpenAIChatToResponses(
					c.Request.Context(),
					originalReq.Model,
					originalRequestJSON,
					nil,
					[]byte(line),
					&converterState,
				)
			}
			eventsToProcess = events
		} else {
			eventsToProcess = []string{line + "\n"}
		}

		for _, event := range eventsToProcess {
			// 收集 output items（含 reasoning encrypted_content）用于 session 回写
			outputCollector.processEvent(event)
			// 收集 reasoning encrypted_content 到 thinkingcache
			reasoningCollector.ProcessEvent(event)
			prevTextLen := outputTextBuffer.Len()
			// 提取文本内容用于估算（限制缓冲区大小）
			if outputTextBuffer.Len() < maxOutputBufferSize {
				extractResponsesTextFromEvent(event, &outputTextBuffer)
			}
			if outputTextBuffer.Len() > prevTextLen {
				progress.AddText(outputTextBuffer.String()[prevTextLen:])
				progress.Tick()
			}

			// 检测并收集 usage
			detected, needPatch, usageData := checkResponsesEventUsageWithLogTag(event, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"), common.RequestLogTag(c))
			if detected {
				if !hasUsage {
					hasUsage = true
					needTokenPatch = needPatch
					if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") && needPatch {
						common.RequestLogf(c, "[Responses-Stream-Token] 检测到虚假值, 延迟到流结束修补")
					}
				}
				updateResponsesStreamUsage(&collectedUsage, usageData)
				if !needConvert {
					candidatePromptTokensTotal := promptTokensTotalFromResponsesInput(
						usageData.InputTokens,
						upstreamType,
						usageData.HasClaudeCache,
					)
					if candidatePromptTokensTotal > promptTokensTotal {
						promptTokensTotal = candidatePromptTokensTotal
					}
				}
			}

			// 在 response.completed 事件前注入/修补 usage
			eventToSend := event
			if isResponsesCompletedEvent(event) {
				completedEventSent = true
				if !hasUsage {
					// 上游完全没有 usage，注入本地估算
					var injectedInput, injectedOutput int
					eventToSend, injectedInput, injectedOutput = injectResponsesUsageToCompletedEventWithLogTag(event, originalRequestJSON, outputTextBuffer.String(), envCfg, common.RequestLogTag(c))
					// 更新 collectedUsage 以便最终日志输出
					collectedUsage.InputTokens = injectedInput
					collectedUsage.OutputTokens = injectedOutput
					collectedUsage.TotalTokens = calculateTotalTokensWithCache(
						injectedInput,
						injectedOutput,
						collectedUsage.CacheReadInputTokens,
						collectedUsage.CacheCreationInputTokens,
						collectedUsage.CacheCreation5mInputTokens,
						collectedUsage.CacheCreation1hInputTokens,
					)
					if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
						common.RequestLogf(c, "[Responses-Stream-Token] 上游无usage, 注入本地估算: input=%d, output=%d", injectedInput, injectedOutput)
					}
				} else if needTokenPatch {
					// 需要修补虚假值
					eventToSend = patchResponsesCompletedEventUsageWithLogTag(event, originalRequestJSON, outputTextBuffer.String(), &collectedUsage, envCfg, common.RequestLogTag(c))
				}
				// 改写 model 字段（仅 passthrough 场景，转换器已处理好转换场景）
				if envCfg.RewriteResponseModel && !needConvert && originalReq != nil && originalReq.Model != "" {
					eventToSend = patchResponsesCompletedEventModel(eventToSend, originalReq.Model, common.RequestLogTag(c))
				}
			}

			// 转发给客户端
			if !clientGone {
				_, err := c.Writer.Write([]byte(eventToSend))
				if err != nil {
					clientGone = true
					if !isClientDisconnectError(err) {
						common.RequestLogf(c, "[Responses-Stream] 警告: 流式响应传输错误: %v", err)
					} else if envCfg.ShouldLog("info") {
						common.RequestLogf(c, "[Responses-Stream] 客户端中断连接 (正常行为)，继续接收上游数据...")
					}
				} else {
					eventsSentCount++
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
		}
		return eventsToProcess
	}

	postCommitToolTracker := common.NewStreamToolCallTracker()
	observePostCommitEvents := func(events []string) bool {
		hadChange := false
		wasPending := postCommitToolTracker.HasPendingToolCall()
		for _, event := range events {
			if malformed, name := postCommitToolTracker.ProcessResponsesEvent(event); malformed && envCfg.ShouldLog("info") {
				common.RequestLogf(c, "[Responses-Stream-ToolCall] 检测到畸形工具调用: %s", name)
			}
		}
		if postCommitToolTracker.HasPendingToolCall() != wasPending {
			hadChange = true
		}
		return hadChange
	}

	// 回放预检测期间缓冲的行
	for _, bufferedLine := range bufferedLines {
		observePostCommitEvents(processLine(bufferedLine))
	}

	// 继续从 lineChan 读取剩余的流数据（带 SSE keep-alive 防止下游 idle timeout）
	keepaliveTicker := time.NewTicker(15 * time.Second)
	defer keepaliveTicker.Stop()

	// post-commit：Header 已发送后的 idle watchdog，由任意上游 SSE 活动重置。
	var postCommitTimer *time.Timer
	var postCommitChan <-chan time.Time
	activePostCommitTimeoutMs := timeouts.InactivityTimeoutMs
	if postCommitToolTracker.HasPendingToolCall() && timeouts.ToolCallIdleTimeoutMs > 0 {
		activePostCommitTimeoutMs = timeouts.ToolCallIdleTimeoutMs
	}
	if activePostCommitTimeoutMs > 0 {
		postCommitTimer = time.NewTimer(time.Duration(activePostCommitTimeoutMs) * time.Millisecond)
		postCommitChan = postCommitTimer.C
	}
	defer func() {
		if postCommitTimer != nil {
			postCommitTimer.Stop()
		}
	}()
	resetPostCommitTimer := func(timeoutMs int) {
		activePostCommitTimeoutMs = timeoutMs
		if timeoutMs <= 0 {
			if postCommitTimer != nil {
				postCommitTimer.Stop()
				postCommitTimer = nil
				postCommitChan = nil
			}
			return
		}
		if postCommitTimer == nil {
			postCommitTimer = time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
			postCommitChan = postCommitTimer.C
			return
		}
		if !postCommitTimer.Stop() {
			select {
			case <-postCommitTimer.C:
			default:
			}
		}
		postCommitTimer.Reset(time.Duration(timeoutMs) * time.Millisecond)
	}
	resolvePostCommitTimeoutMs := func() int {
		if postCommitToolTracker.HasPendingToolCall() && timeouts.ToolCallIdleTimeoutMs > 0 {
			return timeouts.ToolCallIdleTimeoutMs
		}
		return timeouts.InactivityTimeoutMs
	}

	for {
		select {
		case sl, ok := <-lineChan:
			if !ok || !sl.ok {
				goto streamEnd
			}
			events := processLine(sl.text)
			keepaliveTicker.Reset(15 * time.Second)
			wasToolCallPending := postCommitToolTracker.HasPendingToolCall()
			toolCallStateChanged := observePostCommitEvents(events)
			nowToolCallPending := postCommitToolTracker.HasPendingToolCall()
			nextTimeoutMs := resolvePostCommitTimeoutMs()
			hasActivity := common.HasStreamEventActivity(sl.text + "\n")
			if nowToolCallPending && common.HasSSEFrame(sl.text) {
				hasActivity = true
			}
			if hasActivity || toolCallStateChanged {
				if nowToolCallPending {
					common.MarkStreamToolCallActivity(c)
				} else if wasToolCallPending {
					common.MarkStreamToolCallComplete(c)
				} else {
					common.MarkStreamActivity(c)
				}
			}
			if hasActivity || toolCallStateChanged || nextTimeoutMs != activePostCommitTimeoutMs {
				resetPostCommitTimer(nextTimeoutMs)
			}
		case <-postCommitChan:
			progress.Finish("stalled")
			if postCommitToolTracker.HasPendingToolCall() {
				common.RequestLogf(c, "[Responses-StreamStalled] 流式断流: 工具调用阶段空闲 %dms 无上游输出（Header 已发送）", activePostCommitTimeoutMs)
			} else {
				common.RequestLogf(c, "[Responses-StreamStalled] 流式断流: Header 已发送后 %dms 无上游输出", activePostCommitTimeoutMs)
			}
			close(scanDone)
			// 补写 response.incomplete 终端事件，确保下游客户端收到带 incomplete_details.reason 的
			// 终止信号，而非裸断连。对齐 Messages 协议在 post-commit stall 时的 BuildStreamErrorEvent 行为。
			if !clientGone {
				incompleteEvent := converters.SynthesizeResponsesIncomplete(eventsSentCount+1, outputCollector.responseID, "stream_stalled")
				if _, err := c.Writer.Write([]byte(incompleteEvent)); err == nil && flusher != nil {
					flusher.Flush()
				}
			}
			// 流被中断时仍回写已收集的 reasoning 状态，避免推理状态因断流而丢失
			stallSessionID := writeStreamSession(sessionManager, originalReq, outputCollector)
			if stallSessionID != "" {
				reasoningCollector.Store(stallSessionID)
			}
			return nil, common.ErrStreamPostCommitStalled
		case <-keepaliveTicker.C:
			if !clientGone {
				_, err := c.Writer.Write([]byte(": keepalive\n\n"))
				if err != nil {
					clientGone = true
				} else if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}
streamEnd:
	progress.Finish("completed")

	// 兜底：如果上游未发送终止符（如 MiniMax 不发 [DONE]），补发 response.completed
	if !completedEventSent && !clientGone {
		common.RequestLogf(c, "[Responses-Stream] 上游未发送终止符，补发 response.completed (upstreamType=%s)", upstreamType)

		var fallbackEvents []string
		if needConvert {
			switch upstreamType {
			case "claude", "gemini":
				fallbackEvents = converters.SynthesizeResponsesCompleted(originalRequestJSON, &converterState, upstreamType, eventsSentCount)
			default:
				// OpenAI 格式（包括 MiniMax）：发送合成 [DONE] 触发 converter 正常完成流程
				fallbackEvents = converters.ConvertOpenAIChatToResponses(
					c.Request.Context(),
					originalReq.Model,
					originalRequestJSON,
					nil,
					[]byte("data: [DONE]"),
					&converterState,
				)
			}
		} else {
			fallbackEvents = converters.SynthesizeResponsesCompleted(originalRequestJSON, &converterState, "responses", eventsSentCount)
		}

		for _, event := range fallbackEvents {
			// 兜底事件也纳入收集，确保合成 completed 事件的 responseID 被捕获
			outputCollector.processEvent(event)
			eventToSend := event
			if isResponsesCompletedEvent(event) {
				completedEventSent = true
				if !hasUsage {
					var injectedInput, injectedOutput int
					eventToSend, injectedInput, injectedOutput = injectResponsesUsageToCompletedEventWithLogTag(event, originalRequestJSON, outputTextBuffer.String(), envCfg, common.RequestLogTag(c))
					collectedUsage.InputTokens = injectedInput
					collectedUsage.OutputTokens = injectedOutput
					collectedUsage.TotalTokens = calculateTotalTokensWithCache(
						injectedInput,
						injectedOutput,
						collectedUsage.CacheReadInputTokens,
						collectedUsage.CacheCreationInputTokens,
						collectedUsage.CacheCreation5mInputTokens,
						collectedUsage.CacheCreation1hInputTokens,
					)
				} else if needTokenPatch {
					eventToSend = patchResponsesCompletedEventUsageWithLogTag(event, originalRequestJSON, outputTextBuffer.String(), &collectedUsage, envCfg, common.RequestLogTag(c))
				}
				// 改写 model 字段（仅 passthrough 场景，转换器已处理好转换场景）
				if envCfg.RewriteResponseModel && !needConvert && originalReq != nil && originalReq.Model != "" {
					eventToSend = patchResponsesCompletedEventModel(eventToSend, originalReq.Model, common.RequestLogTag(c))
				}
			}
			if _, err := c.Writer.Write([]byte(eventToSend)); err == nil && flusher != nil {
				flusher.Flush()
			}
		}
	}

	// 会话回写：将流式收集的 input/output items（含 reasoning encrypted_content）写入 session。
	// 即使客户端中途断连也执行，以保证会话历史完整。仅修改服务端 session，不改变客户端输出。
	streamSessionID := writeStreamSession(sessionManager, originalReq, outputCollector)
	// 将 reasoning encrypted_content 写入 thinkingcache，按 sessionID + itemID 索引，
	// 供未来续接重放使用（与 session 存储互补：session 用于多轮上下文，thinkingcache 用于按 id 快速取回）
	if streamSessionID != "" {
		reasoningCollector.Store(streamSessionID)
	}

	if err := scanner.Err(); err != nil {
		if !isClientDisconnectError(err) {
			common.RequestLogf(c, "[Responses-Stream] 警告: 流式响应读取错误: %v", err)
		} else if envCfg.ShouldLog("info") {
			common.RequestLogf(c, "[Responses-Stream] 上游读取因客户端取消而结束")
		}
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		common.RequestLogf(c, "[Responses-Stream] Responses 流式响应完成: %dms", responseTime)

		// 输出 Token 统计
		if hasUsage || collectedUsage.InputTokens > 0 || collectedUsage.OutputTokens > 0 {
			common.RequestLogf(c, "[Responses-Stream-Token] InputTokens=%d, OutputTokens=%d, CacheCreation=%d, CacheRead=%d, CacheCreation5m=%d, CacheCreation1h=%d, CacheTTL=%s",
				collectedUsage.InputTokens, collectedUsage.OutputTokens,
				collectedUsage.CacheCreationInputTokens, collectedUsage.CacheReadInputTokens,
				collectedUsage.CacheCreation5mInputTokens, collectedUsage.CacheCreation1hInputTokens,
				collectedUsage.CacheTTL)
		}

		if envCfg.IsDevelopment() {
			if synthesizer != nil {
				synthesizedContent := synthesizer.GetSynthesizedContent()
				parseFailed := synthesizer.IsParseFailed()
				if synthesizedContent != "" && !parseFailed {
					common.RequestLogf(c, "[Responses-Stream] 上游流式响应合成内容:\n%s", strings.TrimSpace(synthesizedContent))
				} else if logBuffer.Len() > 0 {
					common.RequestLogf(c, "[Responses-Stream] 上游流式响应原始内容:\n%s", logBuffer.String())
				}
			} else if logBuffer.Len() > 0 {
				common.RequestLogf(c, "[Responses-Stream] 上游流式响应原始内容:\n%s", logBuffer.String())
			}
		}
	}

	// 返回收集到的 usage 数据
	return metricsUsageFromResponsesUsage(types.ResponsesUsage{
		InputTokens:                collectedUsage.InputTokens,
		OutputTokens:               collectedUsage.OutputTokens,
		CacheCreationInputTokens:   collectedUsage.CacheCreationInputTokens,
		CacheReadInputTokens:       collectedUsage.CacheReadInputTokens,
		CacheCreation5mInputTokens: collectedUsage.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: collectedUsage.CacheCreation1hInputTokens,
		CacheTTL:                   collectedUsage.CacheTTL,
	}, promptTokensTotal), nil
}

// responsesStreamUsage 流式响应 usage 收集结构
type responsesStreamUsage struct {
	InputTokens                int
	OutputTokens               int
	TotalTokens                int // 用于检测 total_tokens 是否需要补全
	CacheCreationInputTokens   int
	CacheReadInputTokens       int
	CacheCreation5mInputTokens int
	CacheCreation1hInputTokens int
	CacheTTL                   string
	HasClaudeCache             bool // 是否检测到 Claude 原生缓存字段（区别于 OpenAI cached_tokens）
}

func normalizeResponsesSSEFieldLine(line string) string {
	for _, prefix := range []string{"data:", "event:", "id:", "retry:"} {
		if strings.HasPrefix(line, prefix) && !strings.HasPrefix(line, prefix+" ") {
			return prefix + " " + line[len(prefix):]
		}
	}
	return line
}

// extractResponsesTextFromEvent 从 Responses SSE 事件中提取文本内容
func extractResponsesTextFromEvent(event string, buf *bytes.Buffer) {
	for _, line := range strings.Split(event, "\n") {
		// 支持 "data:" 和 "data: " 两种格式（有些上游不带空格）
		var jsonStr string
		if strings.HasPrefix(line, "data:") {
			jsonStr = strings.TrimPrefix(line, "data:")
			jsonStr = strings.TrimPrefix(jsonStr, " ") // 移除可能的前导空格
		} else {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)

		// 处理各种 delta 类型
		switch eventType {
		case "response.output_text.delta":
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			}
		case "response.function_call_arguments.delta":
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			}
		case "response.reasoning_summary_text.delta":
			if text, ok := data["text"].(string); ok {
				buf.WriteString(text)
			}
		case "response.output_json.delta":
			// JSON 输出增量
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			}
		case "response.content_part.delta":
			// 内容块增量（通用）
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			} else if text, ok := data["text"].(string); ok {
				buf.WriteString(text)
			}
		case "response.audio.delta", "response.audio_transcript.delta":
			// 音频转录增量
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			}
		default:
			// 未知事件类型兜底：上游新增 response.*.delta / response.*.done 事件时，
			// 尝试提取通用 delta/text 字段，避免文本提取不到被 preflight 误判为空流
			if strings.HasPrefix(eventType, "response.") &&
				(strings.HasSuffix(eventType, ".delta") || strings.HasSuffix(eventType, ".done")) {
				if delta, ok := data["delta"].(string); ok {
					buf.WriteString(delta)
				} else if text, ok := data["text"].(string); ok {
					buf.WriteString(text)
				}
			}
		}
	}
}
