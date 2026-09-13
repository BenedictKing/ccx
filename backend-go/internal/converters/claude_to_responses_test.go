package converters

import (
	"context"
	"strings"
	"testing"
)

func TestConvertClaudeMessagesToResponses_StreamThinkingToReasoning(t *testing.T) {
	lines := []string{
		`data: {"type":"message_start","message":{"id":"msg_ds","type":"message","role":"assistant","model":"deepseek-v4-pro","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"messages thinking"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"messages text"}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":1,"output_tokens":2}}`,
		`data: {"type":"message_stop"}`,
	}

	var state any
	var events []string
	for _, line := range lines {
		events = append(events, ConvertClaudeMessagesToResponses(context.Background(), "deepseek-v4-pro", []byte(`{"model":"deepseek-v4-pro","input":"hello"}`), nil, []byte(line), &state)...)
	}

	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, `"type":"reasoning"`) {
		t.Fatalf("expected reasoning item events, got %v", events)
	}
	if !strings.Contains(joined, `"text":"messages thinking"`) {
		t.Fatalf("expected reasoning text, got %v", events)
	}
	if !strings.Contains(joined, `"delta":"messages text"`) {
		t.Fatalf("expected output text delta, got %v", events)
	}
	if !strings.Contains(joined, `"type":"response.completed"`) {
		t.Fatalf("expected response.completed, got %v", events)
	}
}

func TestConvertClaudeMessagesToResponses_StreamToolUseToFunctionCall(t *testing.T) {
	lines := []string{
		`data: {"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","model":"deepseek-v4-pro","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_read","name":"Read","input":{}}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":":\"/tmp/a\"}"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":1,"output_tokens":2}}`,
		`data: {"type":"message_stop"}`,
	}

	var state any
	var events []string
	for _, line := range lines {
		events = append(events, ConvertClaudeMessagesToResponses(context.Background(), "deepseek-v4-pro", []byte(`{"model":"deepseek-v4-pro","input":"hello"}`), nil, []byte(line), &state)...)
	}

	joined := strings.Join(events, "\n")
	for _, want := range []string{
		`"type":"function_call"`,
		`"call_id":"call_read"`,
		`"name":"Read"`,
		`"arguments":"{\"file_path\":\"/tmp/a\"}"`,
		`response.function_call_arguments.delta`,
		`response.function_call_arguments.done`,
		`"type":"response.completed"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in converted events, got %v", want, events)
		}
	}
}

// additional_tools 提升场景的 claude 承接：上游 tool_use 返回扁平名 functions__exec，
// 必须还原为 custom_tool_call 序列（name=exec、input=原始 JS）；namespace function
// 还原 name+namespace。originalRequestJSON 为 handlers 注入后形态（提升前原始工具
// + transformer_metadata 开关置位）。
func TestConvertClaudeMessagesToResponses_HoistedAdditionalToolsRemap(t *testing.T) {
	originalReq := []byte(`{"model":"gpt-6-astra","input":"run ls","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","format":{"type":"grammar","syntax":"lark","definition":"start: .*"}},{"type":"function","name":"wait","parameters":{"type":"object"}}]}],"transformer_metadata":{"codex_tool_compat_enabled":true}}`)
	lines := []string{
		`data: {"type":"message_start","message":{"id":"msg_hoist","type":"message","role":"assistant","model":"claude-x","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_exec","name":"functions__exec","input":{}}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"input\":\"await tools.exec_command({ cmd: \\\"cat probe.txt\\\" })\"}"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_wait","name":"functions__wait","input":{"cell_id":"c1"}}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":10,"output_tokens":5}}`,
		`data: {"type":"message_stop"}`,
	}

	var state any
	var events []string
	for _, line := range lines {
		events = append(events, ConvertClaudeMessagesToResponses(context.Background(), "claude-x", originalReq, nil, []byte(line), &state)...)
	}
	joined := strings.Join(events, "\n")

	// custom 代理 → custom_tool_call 序列
	for _, want := range []string{
		`"type":"custom_tool_call"`,
		`"name":"exec"`,
		`response.custom_tool_call_input.delta`,
		`response.custom_tool_call_input.done`,
		`await tools.exec_command`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in converted events: %v", want, events)
		}
	}
	if strings.Contains(joined, `"name":"functions__exec"`) {
		t.Fatalf("扁平名 functions__exec 不应泄漏: %v", events)
	}

	// namespace function → function_call wait + namespace=functions
	if !strings.Contains(joined, `"name":"wait"`) || !strings.Contains(joined, `"namespace":"functions"`) {
		t.Fatalf("namespace function 应还原 name+namespace: %v", events)
	}
	if strings.Contains(joined, `"name":"functions__wait"`) {
		t.Fatalf("扁平名 functions__wait 不应泄漏: %v", events)
	}
}

// 无 codex 上下文时（普通请求）工具调用保持 function_call 原样——回归保护。
func TestConvertClaudeMessagesToResponses_NoCodexCtxKeepsFunctionCall(t *testing.T) {
	lines := []string{
		`data: {"type":"message_start","message":{"id":"msg_plain","type":"message","role":"assistant","model":"claude-x","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"functions__exec","input":{}}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"input\":\"ls\"}"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_stop"}`,
	}

	var state any
	var events []string
	for _, line := range lines {
		events = append(events, ConvertClaudeMessagesToResponses(context.Background(), "claude-x", []byte(`{"model":"m","input":"hi"}`), nil, []byte(line), &state)...)
	}
	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, `"type":"function_call"`) || !strings.Contains(joined, `"name":"functions__exec"`) {
		t.Fatalf("无 codex ctx 时应保持 function_call 原样: %v", events)
	}
	if strings.Contains(joined, "custom_tool_call") {
		t.Fatalf("无 codex ctx 时不应 remap: %v", events)
	}
}
