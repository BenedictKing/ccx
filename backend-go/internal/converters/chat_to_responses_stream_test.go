package converters

import (
	"context"
	"strings"
	"testing"
)

func TestConvertOpenAIChatToResponses_Stream(t *testing.T) {
	ctx := context.Background()

	// 模拟 OpenAI Chat Completions SSE 流
	sseLines := []string{
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world!"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-4o","input":"Hi"}`)

	var state any
	var allEvents []string

	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	// 验证事件序列
	if len(allEvents) == 0 {
		t.Fatal("should produce events")
	}

	// 检查是否有 response.created 事件
	hasCreated := false
	hasInProgress := false
	hasCompleted := false
	hasTextDelta := false

	for _, ev := range allEvents {
		if strings.Contains(ev, "response.created") {
			hasCreated = true
		}
		if strings.Contains(ev, "response.in_progress") {
			hasInProgress = true
		}
		if strings.Contains(ev, "response.completed") {
			hasCompleted = true
		}
		if strings.Contains(ev, "response.output_text.delta") {
			hasTextDelta = true
		}
	}

	if !hasCreated {
		t.Error("should have response.created event")
	}
	if !hasInProgress {
		t.Error("should have response.in_progress event")
	}
	if !hasCompleted {
		t.Error("should have response.completed event")
	}
	if !hasTextDelta {
		t.Error("should have response.output_text.delta event")
	}
}

func TestConvertOpenAIChatToResponses_StreamReasoningContent(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-ds","object":"chat.completion.chunk","created":1234567890,"model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"reasoning_content":"chat reasoning"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-ds","object":"chat.completion.chunk","created":1234567890,"model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"content":"chat text"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-ds","object":"chat.completion.chunk","created":1234567890,"model":"deepseek-v4-pro","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}

	var state any
	var allEvents []string
	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "deepseek-v4-pro", []byte(`{"model":"deepseek-v4-pro","input":"hello"}`), nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	joined := strings.Join(allEvents, "\n")
	if !strings.Contains(joined, `"type":"reasoning"`) {
		t.Fatalf("expected reasoning item, got %v", allEvents)
	}
	if !strings.Contains(joined, `"text":"chat reasoning"`) {
		t.Fatalf("expected reasoning summary text, got %v", allEvents)
	}
	if !strings.Contains(joined, `"delta":"chat text"`) {
		t.Fatalf("expected text delta after reasoning, got %v", allEvents)
	}
}

func TestConvertOpenAIChatToResponses_StreamVLLMReasoning(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-vllm","object":"chat.completion.chunk","created":1234567890,"model":"glm-5.2","choices":[{"index":0,"delta":{"reasoning":"vllm reasoning"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-vllm","object":"chat.completion.chunk","created":1234567890,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"chat text"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-vllm","object":"chat.completion.chunk","created":1234567890,"model":"glm-5.2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}

	var state any
	var allEvents []string
	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "glm-5.2", []byte(`{"model":"glm-5.2","input":"hello"}`), nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	joined := strings.Join(allEvents, "\n")
	if !strings.Contains(joined, `"type":"reasoning"`) {
		t.Fatalf("expected reasoning item, got %v", allEvents)
	}
	if !strings.Contains(joined, `"text":"vllm reasoning"`) {
		t.Fatalf("expected reasoning summary text from vLLM reasoning, got %v", allEvents)
	}
	if !strings.Contains(joined, `"delta":"chat text"`) {
		t.Fatalf("expected text delta after reasoning, got %v", allEvents)
	}
}

func TestConvertOpenAIChatToResponses_ToolCall(t *testing.T) {
	ctx := context.Background()

	// 模拟带 tool_call 的 SSE 流
	sseLines := []string{
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\": \"NYC\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-4o","input":"What's the weather?","tools":[{"name":"get_weather"}]}`)

	var state any
	var allEvents []string

	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	// 验证是否有 function_call 相关事件
	hasFuncAdded := false
	hasFuncDelta := false
	hasFuncDone := false

	for _, ev := range allEvents {
		if strings.Contains(ev, "response.output_item.added") && strings.Contains(ev, "function_call") {
			hasFuncAdded = true
		}
		if strings.Contains(ev, "response.function_call_arguments.delta") {
			hasFuncDelta = true
		}
		if strings.Contains(ev, "response.function_call_arguments.done") {
			hasFuncDone = true
		}
	}

	if !hasFuncAdded {
		t.Error("should have function_call output_item.added event")
	}
	if !hasFuncDelta {
		t.Error("should have function_call_arguments.delta event")
	}
	if !hasFuncDone {
		t.Error("should have function_call_arguments.done event")
	}
}

func TestConvertOpenAIChatToResponses_CustomToolCall(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-custom","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_patch","type":"function","function":{"name":"apply_patch_add_file","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-custom","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"docs/test.md\",\"content\":\"# Test\\n\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-custom","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	originalReq := []byte(`{"model":"gpt-4o","input":"edit","tools":[{"type":"custom","name":"apply_patch"}],"transformer_metadata":{"codex_tool_compat_enabled":true}}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)...)
	}
	joined := strings.Join(allEvents, "\n")
	if strings.Contains(joined, `"type":"function_call"`) {
		t.Fatalf("custom tool stream leaked function_call events: %s", joined)
	}
	if strings.Contains(joined, "response.function_call_arguments.") {
		t.Fatalf("custom tool stream leaked function argument events: %s", joined)
	}
	if !strings.Contains(joined, `"type":"custom_tool_call"`) {
		t.Fatalf("missing custom_tool_call item: %s", joined)
	}
	if !strings.Contains(joined, "response.custom_tool_call_input.delta") {
		t.Fatalf("missing custom tool input delta: %s", joined)
	}
	if !strings.Contains(joined, "response.custom_tool_call_input.done") {
		t.Fatalf("missing custom tool input done: %s", joined)
	}
	if !strings.Contains(joined, `*** Add File: docs/test.md`) || strings.Contains(joined, `\n+\n*** End Patch`) {
		t.Fatalf("unexpected patch input: %s", joined)
	}
}

func TestConvertOpenAIChatToResponses_ToolSearchRestoresNativeCall(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-tool-search","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_search","type":"function","function":{"name":"tool_search","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-tool-search","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":\"sub-agent\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-tool-search","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	originalReq := []byte(`{"model":"gpt-4o","input":"find tools","tools":[{"type":"tool_search","execution":"client","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}],"transformer_metadata":{"codex_tool_compat_enabled":true}}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)...)
	}
	joined := strings.Join(allEvents, "\n")
	if !strings.Contains(joined, `"type":"tool_search_call"`) {
		t.Fatalf("missing native tool_search_call item: %s", joined)
	}
	if strings.Contains(joined, `"type":"custom_tool_call"`) || strings.Contains(joined, `"name":"tool_search"`) {
		t.Fatalf("tool_search should not be returned as custom_tool_call: %s", joined)
	}
	if strings.Contains(joined, "response.function_call_arguments.done") {
		t.Fatalf("tool_search should not be returned as a function_call: %s", joined)
	}
	if strings.Contains(joined, "response.custom_tool_call_input.") {
		t.Fatalf("tool_search should not emit custom tool input events: %s", joined)
	}
	if !strings.Contains(joined, `"execution":"client"`) {
		t.Fatalf("tool_search_call should carry client execution: %s", joined)
	}
	if !strings.Contains(joined, `"query":"sub-agent"`) {
		t.Fatalf("tool_search input should preserve query arguments: %s", joined)
	}
}

func TestConvertOpenAIChatToResponses_SubAgentFunctionsStayFunctionCalls(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-subagent","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_spawn","type":"function","function":{"name":"spawn_agent","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-subagent","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"agent_type\":\"explorer\",\"message\":\"只读检查仓库\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-subagent","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_wait","type":"function","function":{"name":"wait_agent","arguments":"{\"targets\":[\"agent-1\"],\"timeout_ms\":60000}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-subagent","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	originalReq := []byte(`{"model":"gpt-4o","input":"try sub-agent","tools":[{"type":"tool_search","execution":"client","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}},{"type":"function","name":"spawn_agent","parameters":{"type":"object","properties":{"agent_type":{"type":"string"},"message":{"type":"string"}},"required":["agent_type","message"]}},{"type":"function","name":"wait_agent","parameters":{"type":"object","properties":{"targets":{"type":"array","items":{"type":"string"}},"timeout_ms":{"type":"integer"}},"required":["targets"]}}],"transformer_metadata":{"codex_tool_compat_enabled":true}}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)...)
	}
	joined := strings.Join(allEvents, "\n")
	if strings.Contains(joined, `"type":"custom_tool_call"`) {
		t.Fatalf("sub-agent function tools should not be remapped to custom_tool_call: %s", joined)
	}
	for _, name := range []string{"spawn_agent", "wait_agent"} {
		if !strings.Contains(joined, `"type":"function_call"`) || !strings.Contains(joined, `"name":"`+name+`"`) {
			t.Fatalf("missing function_call for %s: %s", name, joined)
		}
	}
	if !strings.Contains(joined, `\"agent_type\":\"explorer\"`) {
		t.Fatalf("spawn_agent arguments should be preserved: %s", joined)
	}
	if !strings.Contains(joined, `\"targets\":[\"agent-1\"]`) {
		t.Fatalf("wait_agent arguments should be preserved: %s", joined)
	}
}

func TestConvertOpenAIChatToResponses_Stream_ClaudeCacheTotalTokens(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"msg-claude-cache","object":"chat.completion.chunk","created":1234567890,"model":"claude","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
		`data: {"id":"msg-claude-cache","object":"chat.completion.chunk","created":1234567890,"model":"claude","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":10,"cache_read_input_tokens":30}}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"claude","input":"hi"}`)
	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "claude", originalReq, nil, []byte(line), &state)...)
	}

	usage := extractResponseCompletedUsage(t, allEvents)
	if got := int(usage["total_tokens"].(float64)); got != 160 {
		t.Fatalf("total_tokens = %d, want 160", got)
	}
	if got := int(usage["cache_creation_input_tokens"].(float64)); got != 10 {
		t.Fatalf("cache_creation_input_tokens = %d, want 10", got)
	}
	if got := int(usage["cache_read_input_tokens"].(float64)); got != 30 {
		t.Fatalf("cache_read_input_tokens = %d, want 30", got)
	}
}

func TestConvertOpenAIChatToResponses_Stream_OpenAICacheDetailsNormalizesInput(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-openai-cache","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-openai-cache","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":38451,"completion_tokens":1275,"total_tokens":39726,"prompt_tokens_details":{"cached_tokens":36608}}}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-5.5","input":"hi"}`)
	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "gpt-5.5", originalReq, nil, []byte(line), &state)...)
	}

	usage := extractResponseCompletedUsage(t, allEvents)
	if got := int(usage["input_tokens"].(float64)); got != 1843 {
		t.Fatalf("input_tokens = %d, want 1843", got)
	}
	if got := int(usage["total_tokens"].(float64)); got != 39726 {
		t.Fatalf("total_tokens = %d, want 39726", got)
	}
	if _, exists := usage["cache_read_input_tokens"]; exists {
		t.Fatalf("cache_read_input_tokens should not be emitted for OpenAI cache details: %#v", usage)
	}
	details, ok := usage["input_tokens_details"].(map[string]interface{})
	if !ok {
		t.Fatalf("input_tokens_details missing: %#v", usage)
	}
	if got := int(details["cached_tokens"].(float64)); got != 36608 {
		t.Fatalf("cached_tokens = %d, want 36608", got)
	}
}

// additional_tools 提升场景的 chat 承接端到端：originalRequestJSON 经 handlers 层
// 注入后为「提升前 namespace/custom 形态 tools + transformer_metadata 开关置位」，
// 上游 chat 返回扁平名 tool_call（functions__exec），必须还原为 custom_tool_call
// 序列（name=exec、input=原始 JS），且 namespace function 还原 name+namespace。
func TestConvertOpenAIChatToResponses_HoistedAdditionalToolsRemap(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-hoist","object":"chat.completion.chunk","created":1234567890,"model":"gpt-6-astra","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_exec","type":"function","function":{"name":"functions__exec","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-hoist","object":"chat.completion.chunk","created":1234567890,"model":"gpt-6-astra","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"input\":\"await tools.exec_command({ cmd: \"cat probe.txt\" })\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-hoist","object":"chat.completion.chunk","created":1234567890,"model":"gpt-6-astra","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_wait","type":"function","function":{"name":"functions__wait","arguments":"{\"cell_id\":\"c1\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-hoist","object":"chat.completion.chunk","created":1234567890,"model":"gpt-6-astra","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	// 提升前原始形态（response.go 注入 codex_merged_raw_tools 后的 tools 字段）
	originalReq := []byte(`{"model":"gpt-6-astra","input":"run ls","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","format":{"type":"grammar","syntax":"lark","definition":"start: .*"}},{"type":"function","name":"wait","parameters":{"type":"object"}}]}],"transformer_metadata":{"codex_tool_compat_enabled":true}}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "gpt-6-astra", originalReq, nil, []byte(line), &state)...)
	}
	joined := strings.Join(allEvents, "\n")

	// functions__exec → custom_tool_call exec，input 还原为原始 JS
	if !strings.Contains(joined, `"type":"custom_tool_call"`) {
		t.Fatalf("missing custom_tool_call: %s", joined)
	}
	if !strings.Contains(joined, `"name":"exec"`) {
		t.Fatalf("custom_tool_call name 应为 exec: %s", joined)
	}
	if !strings.Contains(joined, "await tools.exec_command") {
		t.Fatalf("custom_tool_call input 应为原始 JS: %s", joined)
	}
	if strings.Contains(joined, `"name":"functions__exec"`) {
		t.Fatalf("扁平名 functions__exec 不应泄漏给客户端: %s", joined)
	}

	// functions__wait → function_call wait + namespace=functions
	if !strings.Contains(joined, `"name":"wait"`) || !strings.Contains(joined, `"namespace":"functions"`) {
		t.Fatalf("namespace function 应还原 name+namespace: %s", joined)
	}
	if strings.Contains(joined, `"name":"functions__wait"`) {
		t.Fatalf("扁平名 functions__wait 不应泄漏: %s", joined)
	}
}
