package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// 未知顶层字段（safeguards 相关、container、stop_sequence 等）在
// 反序列化 → 序列化后应原样保留，满足网关兼容性要求：不丢响应键。
func TestClaudeResponse_ExtraFieldsRoundTrip(t *testing.T) {
	body := `{
		"id": "msg_01",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-5",
		"content": [{"type": "text", "text": "ok"}],
		"stop_reason": "end_turn",
		"stop_sequence": null,
		"usage": {"input_tokens": 1, "output_tokens": 2},
		"safeguard_results": {"decision": "allow"},
		"container": {"id": "c_1"}
	}`

	var resp ClaudeResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.ExtraFields) != 3 {
		t.Fatalf("ExtraFields = %v, want 3 keys", resp.ExtraFields)
	}
	if _, ok := resp.ExtraFields["safeguard_results"]; !ok {
		t.Fatalf("ExtraFields missing safeguard_results: %v", resp.ExtraFields)
	}

	out, err := json.Marshal(&resp)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, key := range []string{`"safeguard_results"`, `"container"`, `"stop_sequence":null`} {
		if !strings.Contains(string(out), key) {
			t.Fatalf("Marshal() output missing %s: %s", key, out)
		}
	}
}

// 已知字段不进入 ExtraFields，且 Marshal 时已知字段优先、extra 不覆盖。
func TestClaudeResponse_ExtraFieldsDoNotShadowKnownFields(t *testing.T) {
	body := `{
		"id": "msg_01",
		"type": "message",
		"role": "assistant",
		"content": [],
		"model": "claude-sonnet-5"
	}`

	var resp ClaudeResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.ExtraFields != nil {
		t.Fatalf("ExtraFields = %v, want nil for known-only body", resp.ExtraFields)
	}
}

// 无未知字段时输出与原窄结构体完全一致，保障 OpenAI/Gemini 等
// 手工构造 ClaudeResponse 的路径零回归。
func TestClaudeResponse_MarshalWithoutExtraFieldsUnchanged(t *testing.T) {
	resp := ClaudeResponse{
		ID:         "msg_1",
		Type:       "message",
		Role:       "assistant",
		Model:      "m",
		Content:    []ClaudeContent{{Type: "text", Text: "hi"}},
		StopReason: "end_turn",
		Usage:      &Usage{InputTokens: 1, OutputTokens: 2},
	}
	out, err := json.Marshal(&resp)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`
	if string(out) != want {
		t.Fatalf("Marshal() = %s, want %s", out, want)
	}
}
