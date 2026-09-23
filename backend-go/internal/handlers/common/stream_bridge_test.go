package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

const chatStreamSSE = "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"gpt-5\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你好\"},\"finish_reason\":null}]}\n" +
	"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"！\"},\"finish_reason\":null}]}\n" +
	"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"思考\"},\"finish_reason\":null}]}\n" +
	"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\"\"}}]}},\"finish_reason\":null}]}\n" +
	"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\\\",\\\"q\\\":\\\"北京\\\"}\"}}]},\"finish_reason\":null}]}\n" +
	"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n" +
	"data: {\"id\":\"chatcmpl-1\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n" +
	"data: [DONE]\n"

func TestSynthesizeChatCompletionFromSSE(t *testing.T) {
	body, err := synthesizeChatCompletionFromSSE([]byte(chatStreamSSE))
	if err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("合成结果非合法 JSON: %v", err)
	}
	if resp["object"] != "chat.completion" {
		t.Fatalf("object = %v, want chat.completion", resp["object"])
	}
	if resp["id"] != "chatcmpl-1" || resp["model"] != "gpt-5" {
		t.Fatalf("id/model 透传失败: %v / %v", resp["id"], resp["model"])
	}
	choices, ok := resp["choices"].([]interface{})
	if !ok || len(choices) != 1 {
		t.Fatalf("choices = %#v, want 1 个", resp["choices"])
	}
	choice := choices[0].(map[string]interface{})
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %v, want tool_calls", choice["finish_reason"])
	}
	message := choice["message"].(map[string]interface{})
	if message["role"] != "assistant" {
		t.Fatalf("role = %v, want assistant", message["role"])
	}
	if message["content"] != "你好！" {
		t.Fatalf("content = %v, want 你好！", message["content"])
	}
	if message["reasoning_content"] != "思考" {
		t.Fatalf("reasoning_content = %v, want 思考", message["reasoning_content"])
	}
	toolCalls, ok := message["tool_calls"].([]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("tool_calls = %#v, want 1 个", message["tool_calls"])
	}
	call := toolCalls[0].(map[string]interface{})
	if call["id"] != "call_1" || call["type"] != "function" {
		t.Fatalf("tool_call id/type = %#v", call)
	}
	fn := call["function"].(map[string]interface{})
	if fn["name"] != "get_weather" {
		t.Fatalf("function.name = %v, want get_weather", fn["name"])
	}
	if fn["arguments"] != `{"city","q":"北京"}` {
		t.Fatalf("function.arguments = %q", fn["arguments"])
	}
	usage, ok := resp["usage"].(map[string]interface{})
	if !ok || usage["prompt_tokens"].(float64) != 10 || usage["completion_tokens"].(float64) != 5 {
		t.Fatalf("usage = %#v", resp["usage"])
	}
}

func TestSynthesizeChatCompletionEmptyStream(t *testing.T) {
	if _, err := synthesizeChatCompletionFromSSE([]byte("data: [DONE]\n")); err == nil {
		t.Fatal("空 SSE 应返回错误而非空 choices 的伪成功")
	}
}

const claudeStreamSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-opus-4-8\",\"content\":[],\"usage\":{\"input_tokens\":25,\"cache_read_input_tokens\":5}}}\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"想一下\"}}\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig==\"}}\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_weather\",\"input\":{}}}\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\\\"北京\\\"}\"}}\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"text_delta\",\"text\":\"今天晴\"}}\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":42}}\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n"

func TestSynthesizeClaudeMessageFromSSE(t *testing.T) {
	body, err := synthesizeClaudeMessageFromSSE([]byte(claudeStreamSSE))
	if err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("合成结果非合法 JSON: %v", err)
	}
	if resp["type"] != "message" || resp["role"] != "assistant" {
		t.Fatalf("type/role = %v / %v", resp["type"], resp["role"])
	}
	if resp["id"] != "msg_1" || resp["model"] != "claude-opus-4-8" {
		t.Fatalf("id/model = %v / %v", resp["id"], resp["model"])
	}
	if resp["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason = %v, want tool_use", resp["stop_reason"])
	}
	usage := resp["usage"].(map[string]interface{})
	if usage["input_tokens"].(float64) != 25 || usage["output_tokens"].(float64) != 42 || usage["cache_read_input_tokens"].(float64) != 5 {
		t.Fatalf("usage 合并失败: %#v", usage)
	}
	content := resp["content"].([]interface{})
	if len(content) != 3 {
		t.Fatalf("content 块数 = %d, want 3", len(content))
	}
	thinking := content[0].(map[string]interface{})
	if thinking["type"] != "thinking" || thinking["thinking"] != "想一下" || thinking["signature"] != "sig==" {
		t.Fatalf("thinking 块 = %#v", thinking)
	}
	toolUse := content[1].(map[string]interface{})
	if toolUse["type"] != "tool_use" || toolUse["id"] != "toolu_1" || toolUse["name"] != "get_weather" {
		t.Fatalf("tool_use 块 = %#v", toolUse)
	}
	input := toolUse["input"].(map[string]interface{})
	if input["city"] != "北京" {
		t.Fatalf("tool_use.input = %#v", input)
	}
	text := content[2].(map[string]interface{})
	if text["type"] != "text" || text["text"] != "今天晴" {
		t.Fatalf("text 块 = %#v", text)
	}
}

func TestSynthesizeClaudeMessageWithoutStart(t *testing.T) {
	if _, err := synthesizeClaudeMessageFromSSE([]byte("data: {\"type\":\"message_stop\"}\n")); err == nil {
		t.Fatal("缺少 message_start 应返回错误")
	}
}

const responsesStreamSSE = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"status\":\"in_progress\"}}\n" +
	"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"item_1\",\"delta\":\"he\"}\n" +
	"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"item_1\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}}\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"item_1\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n"

func TestSynthesizeResponsesObjectFromSSE(t *testing.T) {
	body, err := synthesizeResponsesObjectFromSSE([]byte(responsesStreamSSE))
	if err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("合成结果非合法 JSON: %v", err)
	}
	if resp["id"] != "resp_1" || resp["status"] != "completed" {
		t.Fatalf("response 对象捕获失败: id=%v status=%v", resp["id"], resp["status"])
	}
	if _, ok := resp["usage"].(map[string]interface{}); !ok {
		t.Fatalf("response.usage 丢失: %#v", resp)
	}
}

func TestSynthesizeResponsesObjectFailedAndMissingTerminal(t *testing.T) {
	if _, err := synthesizeResponsesObjectFromSSE([]byte("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n")); err == nil {
		t.Fatal("response.failed 应返回错误以触发 failover")
	}
	if _, err := synthesizeResponsesObjectFromSSE([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n")); err == nil {
		t.Fatal("缺少终态事件应返回错误")
	}
}

func TestIsEventStreamResponse(t *testing.T) {
	sseHeader := http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}}
	jsonHeader := http.Header{"Content-Type": []string{"application/json"}}
	tests := []struct {
		name   string
		header http.Header
		body   string
		want   bool
	}{
		{name: "Content-Type 判定", header: sseHeader, body: "", want: true},
		{name: "body 前缀兜底 data:", header: jsonHeader, body: "data: {\"x\":1}\n", want: true},
		{name: "body 前缀兜底 event:", header: jsonHeader, body: "event: message_start\ndata: {}\n", want: true},
		{name: "JSON 体不误判", header: jsonHeader, body: "{\"choices\":[]}", want: false},
		{name: "JSON data 字段不误判", header: jsonHeader, body: "{\"data\":{\"x\":1}}", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{Header: tt.header}
			if got := IsEventStreamResponse(resp, []byte(tt.body)); got != tt.want {
				t.Fatalf("IsEventStreamResponse = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestForceStreamRequestBody(t *testing.T) {
	t.Run("stream false 改写为 true", func(t *testing.T) {
		rewritten, changed := ForceStreamRequestBody([]byte(`{"model":"gpt-5","stream":false}`), "chat", "chat")
		if !changed {
			t.Fatal("应执行改写")
		}
		if !strings.Contains(string(rewritten), `"stream":true`) {
			t.Fatalf("stream 应为 true: %s", rewritten)
		}
		if !strings.Contains(string(rewritten), `"include_usage":true`) {
			t.Fatalf("chat→chat 应附 stream_options.include_usage: %s", rewritten)
		}
	})
	t.Run("stream 缺失改写为 true", func(t *testing.T) {
		rewritten, changed := ForceStreamRequestBody([]byte(`{"model":"gpt-5"}`), "chat", "chat")
		if !changed || !strings.Contains(string(rewritten), `"stream":true`) {
			t.Fatalf("changed=%v rewritten=%s", changed, rewritten)
		}
	})
	t.Run("messages 体不附 stream_options", func(t *testing.T) {
		rewritten, changed := ForceStreamRequestBody([]byte(`{"model":"gpt-5","stream":false}`), "messages", "chat")
		if !changed {
			t.Fatal("应执行改写")
		}
		if strings.Contains(string(rewritten), "include_usage") {
			t.Fatalf("messages 体不应附 stream_options: %s", rewritten)
		}
	})
	t.Run("已是流式幂等返回", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5","stream":true}`)
		rewritten, changed := ForceStreamRequestBody(body, "chat", "chat")
		if changed {
			t.Fatal("已是流式不应改写")
		}
		if !bytes.Equal(body, rewritten) {
			t.Fatal("幂等返回应保持原体")
		}
	})
	t.Run("非法 JSON 返回 changed false", func(t *testing.T) {
		if _, changed := ForceStreamRequestBody([]byte("not-json"), "chat", "chat"); changed {
			t.Fatal("非法 JSON 不应改写")
		}
	})
}

func TestStreamBridgeSupportedKind(t *testing.T) {
	for _, kind := range []string{"messages", "chat", "responses"} {
		if !StreamBridgeSupportedKind(kind) {
			t.Fatalf("%s 应支持流式桥接", kind)
		}
	}
	if StreamBridgeSupportedKind("gemini") {
		t.Fatal("gemini 不应支持流式桥接（无顶层 stream 字段语义）")
	}
}

func TestReadUpstreamNonStreamBodyUnsupportedType(t *testing.T) {
	resp := &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}}
	resp.Body = http.NoBody
	_, err := ReadUpstreamNonStreamBody(nil, resp, "gemini")
	if !errors.Is(err, ErrStreamBridgeUnsupported) {
		t.Fatalf("err = %v, want ErrStreamBridgeUnsupported", err)
	}
}

// 端到端合成：chat 上游返回 SSE，非流式路径应拿到合成后的 chat.completion 体。
func TestReadUpstreamNonStreamBodySynthesizesChatSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:   io.NopCloser(strings.NewReader(chatStreamSSE)),
	}
	bodyBytes, err := ReadUpstreamNonStreamBody(c, resp, "openai")
	if err != nil {
		t.Fatalf("读取合成失败: %v", err)
	}
	if !json.Valid(bodyBytes) {
		t.Fatal("合成结果应为合法 JSON")
	}
	var respMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &respMap); err != nil {
		t.Fatalf("解析合成结果失败: %v", err)
	}
	if respMap["object"] != "chat.completion" {
		t.Fatalf("object = %v, want chat.completion", respMap["object"])
	}

	// JSON 响应体应原样返回（不做合成）
	jsonResp := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"hi"}}]}`)),
	}
	passthrough, err := ReadUpstreamNonStreamBody(c, jsonResp, "openai")
	if err != nil {
		t.Fatalf("JSON 体读取失败: %v", err)
	}
	if !strings.Contains(string(passthrough), `"content":"hi"`) {
		t.Fatalf("JSON 体应原样返回: %s", passthrough)
	}
}

// 学习结论读取回归：trait 记录后 MarkApplied 不改变 Enabled 判断。
func TestTraitRequiresStreamMarkApplied(t *testing.T) {
	cache := config.NewChannelCompatCache()
	if !cache.Record("ch_bridge", "kh_1", "gpt-5", config.TraitRequiresStream, true,
		config.CompatSourceErrorSignal, "streaming is required") {
		t.Fatal("首次学习应返回新增")
	}
	cache.MarkApplied("ch_bridge", "kh_1", "gpt-5", config.TraitRequiresStream)
	state, ok := cache.Trait("ch_bridge", "kh_1", "gpt-5", config.TraitRequiresStream)
	if !ok || !state.Enabled || state.ApplyCount != 1 {
		t.Fatalf("MarkApplied 后 state = %+v, ok=%v", state, ok)
	}
}
