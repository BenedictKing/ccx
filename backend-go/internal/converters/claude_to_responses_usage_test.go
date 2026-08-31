package converters

import (
	"context"
	"strings"
	"testing"
)

// Claude Messages 上游的 usage 含缓存字段（cache_read/cache_creation），
// 转换为 Responses 事件时必须原样透出，否则出口归一为官方 OpenAI 口径时无从加回。
// 转换器自身仍按"不含缓存"口径输出 input_tokens（#274 的出口归一在 handlers 层完成）。
func TestConvertClaudeMessagesToResponses_CompletedCarriesCacheFields(t *testing.T) {
	lines := []string{
		`data: {"type":"message_start","message":{"id":"msg_ds","type":"message","role":"assistant","model":"deepseek-v4-pro","content":[],"usage":{"input_tokens":789,"cache_read_input_tokens":9984,"cache_creation_5m_input_tokens":7,"cache_creation_1h_input_tokens":3}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":154}}`,
		`data: {"type":"message_stop"}`,
	}

	var state any
	var events []string
	for _, line := range lines {
		events = append(events, ConvertClaudeMessagesToResponses(context.Background(), "deepseek-v4-pro", []byte(`{"model":"deepseek-v4-pro","input":"hello"}`), nil, []byte(line), &state)...)
	}

	var completed string
	for _, e := range events {
		if strings.Contains(e, `"type":"response.completed"`) {
			completed = e
		}
	}
	if completed == "" {
		t.Fatal("未生成 response.completed 事件")
	}

	for _, want := range []string{
		`"input_tokens":789`,
		`"output_tokens":154`,
		`"cache_read_input_tokens":9984`,
		`"cache_creation_5m_input_tokens":7`,
		`"cache_creation_1h_input_tokens":3`,
	} {
		if !strings.Contains(completed, want) {
			t.Fatalf("completed 缺少 %s:\n%s", want, completed)
		}
	}
}
