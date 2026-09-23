package common

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 流式桥接兼容改写（TraitRequiresStream 的正向路径）。
//
// 仅接受流式的渠道（上游 400 "streaming is required" 学得）不再让非流式请求绕行：
// 请求侧把 stream 强制为 true（见 upstream_failover.go 的改写注入），上游按 SSE 返回；
// 响应侧由本文件把 SSE 合成为该上游协议的非流式响应体，随后走既有转换链路
// （provider.ConvertToClaudeResponse / ConvertToResponsesResponse / 透传），
// 使用量提取、空响应拦截、竞速提交等行为与原生非流式路径完全一致。
//
// 合成粒度是「上游协议」而非「客户端协议」：合成结果喂给既有协议转换器，
// 避免为每个 入口×执行 组合单独实现合成器。

// UpstreamStreamBridgeContextKey gin context 键：本次 attempt 已按流式桥接改写。
// 供 chat 入口的 buildProviderRequest 覆盖 isStream 参数（该路径从参数而非 body
// 派生上游 stream 字段）。每次 attempt 开始时复位，避免污染无改写的后续 attempt。
const UpstreamStreamBridgeContextKey = "upstreamStreamBridge"

// SetUpstreamStreamBridge 标记本次 attempt 已做流式桥接请求改写。
func SetUpstreamStreamBridge(c *gin.Context, enabled bool) {
	c.Set(UpstreamStreamBridgeContextKey, enabled)
}

// UpstreamStreamBridgeRequested 读取流式桥接改写标记。
func UpstreamStreamBridgeRequested(c *gin.Context) bool {
	if c == nil {
		return false
	}
	enabled, ok := c.Get(UpstreamStreamBridgeContextKey)
	return ok && enabled == true
}

// ErrStreamBridgeUnsupported 上游协议没有对应的 SSE 合成器。
// 返回给调用方按可 failover 错误处理（换渠道重试）。
var ErrStreamBridgeUnsupported = errors.New("stream bridge: upstream protocol not supported")

// StreamBridgeSupportedKind 判断入口协议请求体是否支持流式桥接改写。
// 依据是请求体是否有顶层 stream 字段语义：messages/chat/responses 有，gemini 没有
// （Gemini 协议靠 URL 路径区分流式，改写 body 无效）。
func StreamBridgeSupportedKind(kind string) bool {
	switch kind {
	case "messages", "chat", "responses":
		return true
	default:
		return false
	}
}

// ForceStreamRequestBody 把请求体改为流式：顶层 stream 置 true。
// body 协议与执行协议均为 chat 时附加 stream_options.include_usage，
// 让上游在 SSE 末尾回传 usage（否则合成的非流式响应缺 usage，指标退化为估算）。
// 已是流式的体原样返回（幂等）；非法 JSON 返回 changed=false，由调用方退回跳过逻辑。
func ForceStreamRequestBody(body []byte, bodyKind, executionKind string) ([]byte, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, false
	}
	if gjson.GetBytes(body, "stream").Bool() {
		return body, false
	}
	rewritten, err := sjson.SetBytes(body, "stream", true)
	if err != nil {
		return body, false
	}
	if bodyKind == "chat" && executionKind == "chat" {
		if rewritten, err = sjson.SetBytes(rewritten, "stream_options.include_usage", true); err != nil {
			return body, false
		}
	}
	return rewritten, true
}

// IsEventStreamResponse 判断上游响应是否为 SSE 流。
// Content-Type 为主；部分中转站不回正确 Content-Type，补 body 前缀兜底
// （SSE 事件行以 data:/event: 开头，JSON 体不可能以它们开头）。
func IsEventStreamResponse(resp *http.Response, body []byte) bool {
	if resp != nil && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return true
	}
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(trimmed, "data:") || strings.HasPrefix(trimmed, "event:")
}

// ReadUpstreamNonStreamBody 读取上游非流式响应体。
// 上游实际返回 SSE 时（仅接受流式渠道的流式桥接改写），把 SSE 合成为该上游协议的
// 非流式响应体后返回；合成失败返回错误，调用方按可 failover 错误处理。
// upstreamType 为上游 ServiceType（openai/claude/responses/gemini/copilot）。
func ReadUpstreamNonStreamBody(c *gin.Context, resp *http.Response, upstreamType string) ([]byte, error) {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if !IsEventStreamResponse(resp, bodyBytes) {
		return bodyBytes, nil
	}

	var synthesized []byte
	var synErr error
	switch normalizeBridgeUpstreamType(upstreamType) {
	case "openai":
		synthesized, synErr = synthesizeChatCompletionFromSSE(bodyBytes)
	case "claude":
		synthesized, synErr = synthesizeClaudeMessageFromSSE(bodyBytes)
	case "responses":
		synthesized, synErr = synthesizeResponsesObjectFromSSE(bodyBytes)
	default:
		synErr = fmt.Errorf("%w: %s", ErrStreamBridgeUnsupported, upstreamType)
	}
	if synErr != nil {
		RequestLogf(c, "[StreamBridge] 上游 %s 流式响应合成失败: %v, body前100字节=%q", upstreamType, synErr, previewBridgePrefix(bodyBytes, 100))
		return nil, synErr
	}
	RequestLogf(c, "[StreamBridge] 上游 %s 流式响应已合成回非流式响应体（%d -> %d bytes）", upstreamType, len(bodyBytes), len(synthesized))
	return synthesized, nil
}

// normalizeBridgeUpstreamType 归一化上游协议类型（copilot 即 Responses 协议）。
func normalizeBridgeUpstreamType(t string) string {
	if t == "copilot" {
		return "responses"
	}
	return t
}

// previewBridgePrefix 按字节截断预览（日志诊断用）。
func previewBridgePrefix(body []byte, limit int) string {
	if limit <= 0 || len(body) <= limit {
		return string(body)
	}
	return string(body[:limit])
}

// iterateSSEDataLines 逐行产出 SSE data 载荷（剥离 "data: " 前缀，跳过 [DONE]）。
// 兼容 \r\n 行尾与无空格的 "data:" 形态。
func iterateSSEDataLines(body []byte, fn func(dataLine string) error) error {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	// SSE 单事件可很长（base64 图像、大参数块），放大缓冲上限
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if err := fn(payload); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// ============== OpenAI Chat：chat.completion.chunk → chat.completion ==============

type bridgeChatMessage struct {
	Role             string                   `json:"role"`
	Content          *string                  `json:"content"`
	ReasoningContent *string                  `json:"reasoning_content,omitempty"`
	ToolCalls        []map[string]interface{} `json:"tool_calls,omitempty"`
}

type bridgeChatChoice struct {
	Index        int               `json:"index"`
	Message      bridgeChatMessage `json:"message"`
	FinishReason *string           `json:"finish_reason"`
}

type bridgeChatCompletion struct {
	ID                string             `json:"id"`
	Object            string             `json:"object"`
	Created           int64              `json:"created"`
	Model             string             `json:"model"`
	SystemFingerprint string             `json:"system_fingerprint,omitempty"`
	Choices           []bridgeChatChoice `json:"choices"`
	Usage             json.RawMessage    `json:"usage,omitempty"`
}

type bridgeChatChoiceState struct {
	message      bridgeChatMessage
	content      strings.Builder
	reasoning    strings.Builder
	toolCalls    map[int]*bridgeChatToolCallState
	finishReason *string
}

type bridgeChatToolCallState struct {
	id        string
	name      string
	arguments strings.Builder
}

// synthesizeChatCompletionFromSSE 把 chat.completion.chunk SSE 聚合为 chat.completion JSON。
func synthesizeChatCompletionFromSSE(body []byte) ([]byte, error) {
	result := &bridgeChatCompletion{Object: "chat.completion", Created: time.Now().Unix()}
	choices := make(map[int]*bridgeChatChoiceState)
	sawChunk := false

	err := iterateSSEDataLines(body, func(dataLine string) error {
		chunk := gjson.Parse(dataLine)
		if !chunk.IsObject() {
			return nil
		}
		sawChunk = true
		if v := chunk.Get("id"); v.Exists() && result.ID == "" {
			result.ID = v.String()
		}
		if v := chunk.Get("model"); v.Exists() && result.Model == "" {
			result.Model = v.String()
		}
		if v := chunk.Get("created"); v.Exists() && v.Int() > 0 {
			result.Created = v.Int()
		}
		if v := chunk.Get("system_fingerprint"); v.Exists() && result.SystemFingerprint == "" {
			result.SystemFingerprint = v.String()
		}
		if v := chunk.Get("usage"); v.Exists() && v.IsObject() {
			result.Usage = json.RawMessage(v.Raw)
		}

		chunk.Get("choices").ForEach(func(_, choice gjson.Result) bool {
			index := int(choice.Get("index").Int())
			state := choices[index]
			if state == nil {
				state = &bridgeChatChoiceState{
					message:   bridgeChatMessage{Role: "assistant"},
					toolCalls: make(map[int]*bridgeChatToolCallState),
				}
				choices[index] = state
			}
			if role := choice.Get("delta.role"); role.Exists() && role.String() != "" {
				state.message.Role = role.String()
			}
			if delta := choice.Get("delta.content"); delta.Exists() && delta.Type == gjson.String {
				state.content.WriteString(delta.String())
			}
			if delta := choice.Get("delta.reasoning_content"); delta.Exists() && delta.Type == gjson.String {
				state.reasoning.WriteString(delta.String())
			}
			choice.Get("delta.tool_calls").ForEach(func(_, tc gjson.Result) bool {
				tcIndex := int(tc.Get("index").Int())
				call := state.toolCalls[tcIndex]
				if call == nil {
					call = &bridgeChatToolCallState{}
					state.toolCalls[tcIndex] = call
				}
				if id := tc.Get("id"); id.Exists() && id.String() != "" {
					call.id = id.String()
				}
				if name := tc.Get("function.name"); name.Exists() && name.String() != "" {
					call.name = name.String()
				}
				if args := tc.Get("function.arguments"); args.Exists() && args.Type == gjson.String {
					call.arguments.WriteString(args.String())
				}
				return true
			})
			if fr := choice.Get("finish_reason"); fr.Exists() && fr.Type != gjson.Null {
				reason := fr.String()
				state.finishReason = &reason
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("解析 chat SSE 失败: %w", err)
	}
	if !sawChunk || len(choices) == 0 {
		return nil, errors.New("chat SSE 中未发现任何选择块")
	}

	for index, state := range choices {
		content := state.content.String()
		state.message.Content = &content
		if state.reasoning.Len() > 0 {
			reasoning := state.reasoning.String()
			state.message.ReasoningContent = &reasoning
		}
		if len(state.toolCalls) > 0 {
			indexes := make([]int, 0, len(state.toolCalls))
			for tcIndex := range state.toolCalls {
				indexes = append(indexes, tcIndex)
			}
			sort.Ints(indexes)
			for _, tcIndex := range indexes {
				call := state.toolCalls[tcIndex]
				state.message.ToolCalls = append(state.message.ToolCalls, map[string]interface{}{
					"id":   call.id,
					"type": "function",
					"function": map[string]interface{}{
						"name":      call.name,
						"arguments": call.arguments.String(),
					},
				})
			}
		}
		result.Choices = append(result.Choices, bridgeChatChoice{
			Index:        index,
			Message:      state.message,
			FinishReason: state.finishReason,
		})
	}
	sort.Slice(result.Choices, func(i, j int) bool { return result.Choices[i].Index < result.Choices[j].Index })

	return json.Marshal(result)
}

// ============== Claude：SSE 事件流 → messages JSON ==============

type bridgeClaudeBlock struct {
	index     int
	blockType string
	id        string
	name      string
	data      string
	text      strings.Builder
	thinking  strings.Builder
	signature string
	inputJSON strings.Builder
}

// synthesizeClaudeMessageFromSSE 把 Claude messages SSE 聚合为非流式 messages JSON。
func synthesizeClaudeMessageFromSSE(body []byte) ([]byte, error) {
	var (
		messageID    string
		model        string
		stopReason   interface{}
		stopSequence interface{}
		usage        = map[string]interface{}{}
		blocks       = map[int]*bridgeClaudeBlock{}
		sawStart     bool
	)

	err := iterateSSEDataLines(body, func(dataLine string) error {
		event := gjson.Parse(dataLine)
		if !event.IsObject() {
			return nil
		}
		switch event.Get("type").String() {
		case "message_start":
			sawStart = true
			msg := event.Get("message")
			if msg.IsObject() {
				messageID = msg.Get("id").String()
				model = msg.Get("model").String()
				if u := msg.Get("usage"); u.IsObject() {
					u.ForEach(func(key, value gjson.Result) bool {
						usage[key.String()] = value.Value()
						return true
					})
				}
			}
		case "content_block_start":
			index := int(event.Get("index").Int())
			block := event.Get("content_block")
			state := &bridgeClaudeBlock{
				index:     index,
				blockType: block.Get("type").String(),
				id:        block.Get("id").String(),
				name:      block.Get("name").String(),
				data:      block.Get("data").String(),
			}
			if text := block.Get("text"); text.Exists() && text.Type == gjson.String {
				state.text.WriteString(text.String())
			}
			blocks[index] = state
		case "content_block_delta":
			index := int(event.Get("index").Int())
			state := blocks[index]
			if state == nil {
				return nil
			}
			delta := event.Get("delta")
			switch delta.Get("type").String() {
			case "text_delta":
				state.text.WriteString(delta.Get("text").String())
			case "thinking_delta":
				state.thinking.WriteString(delta.Get("thinking").String())
			case "signature_delta":
				state.signature += delta.Get("signature").String()
			case "input_json_delta":
				state.inputJSON.WriteString(delta.Get("partial_json").String())
			}
		case "message_delta":
			if sr := event.Get("delta.stop_reason"); sr.Exists() {
				stopReason = sr.Value()
			}
			if ss := event.Get("delta.stop_sequence"); ss.Exists() {
				stopSequence = ss.Value()
			}
			if u := event.Get("usage"); u.IsObject() {
				u.ForEach(func(key, value gjson.Result) bool {
					usage[key.String()] = value.Value()
					return true
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("解析 Claude SSE 失败: %w", err)
	}
	if !sawStart {
		return nil, errors.New("Claude SSE 中未发现 message_start 事件")
	}

	indexes := make([]int, 0, len(blocks))
	for index := range blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	content := make([]interface{}, 0, len(blocks))
	for _, index := range indexes {
		state := blocks[index]
		switch state.blockType {
		case "text":
			content = append(content, map[string]interface{}{
				"type": "text",
				"text": state.text.String(),
			})
		case "thinking":
			block := map[string]interface{}{
				"type":     "thinking",
				"thinking": state.thinking.String(),
			}
			if state.signature != "" {
				block["signature"] = state.signature
			}
			content = append(content, block)
		case "redacted_thinking":
			content = append(content, map[string]interface{}{
				"type": "redacted_thinking",
				"data": state.data,
			})
		case "tool_use":
			var input interface{}
			raw := strings.TrimSpace(state.inputJSON.String())
			if raw != "" && json.Unmarshal([]byte(raw), &input) != nil {
				input = map[string]interface{}{}
			}
			if input == nil {
				input = map[string]interface{}{}
			}
			content = append(content, map[string]interface{}{
				"type":  "tool_use",
				"id":    state.id,
				"name":  state.name,
				"input": input,
			})
		}
	}

	if messageID == "" {
		messageID = fmt.Sprintf("msg_bridge_%d", time.Now().UnixNano())
	}
	message := map[string]interface{}{
		"id":            messageID,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": stopSequence,
		"usage":         usage,
	}
	return json.Marshal(message)
}

// ============== Responses：捕获终态事件中的完整 response 对象 ==============

// synthesizeResponsesObjectFromSSE 从 Responses SSE 中捕获终态事件
// （response.completed / response.incomplete）携带的完整 response 对象。
// response.failed 视为生成失败返回错误，交由 failover 换渠道重试。
func synthesizeResponsesObjectFromSSE(body []byte) ([]byte, error) {
	var terminal json.RawMessage
	sawEvent := false

	err := iterateSSEDataLines(body, func(dataLine string) error {
		event := gjson.Parse(dataLine)
		if !event.IsObject() {
			return nil
		}
		sawEvent = true
		switch event.Get("type").String() {
		case "response.completed", "response.incomplete":
			if resp := event.Get("response"); resp.IsObject() {
				terminal = json.RawMessage(resp.Raw)
			}
		case "response.failed":
			return errors.New("response.failed: 上游流式生成失败")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !sawEvent {
		return nil, errors.New("Responses SSE 中未发现任何事件")
	}
	if terminal == nil {
		return nil, errors.New("Responses SSE 中未发现 response.completed/incomplete 终态事件")
	}
	return terminal, nil
}
