package types

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestClaudeResponsePreservesUnknownFields(t *testing.T) {
	const body = `{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"container":{"id":"c_test"},"safeguard_results":{"decision":"allow","count":9007199254740993},"future_fields":[true,{"amount":1.234567890123456789}]}`
	var response ClaudeResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	for key, value := range before {
		if !bytes.Equal(value, after[key]) {
			t.Errorf("field %s = %s, want %s", key, after[key], value)
		}
	}
	// 复用目标结构体时，新响应不能携带上一次的扩展字段。
	if err := json.Unmarshal([]byte(`{"id":"msg_next","type":"message","role":"assistant","content":[]}`), &response); err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("safeguard_results")) || bytes.Contains(encoded, []byte("claude-sonnet-5")) {
		t.Fatalf("stale fields leaked: %s", encoded)
	}
}

func TestClaudeResponseConstructedEncodingUnchanged(t *testing.T) {
	response := ClaudeResponse{ID: "msg_test", Type: "message", Role: "assistant", Content: []ClaudeContent{{Type: "text", Text: "ok"}}, StopReason: "end_turn", Usage: &Usage{InputTokens: 1, OutputTokens: 2}}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`
	if string(encoded) != want {
		t.Fatalf("encoded = %s, want %s", encoded, want)
	}
}

func TestClaudeResponseExtrasCannotOverrideDeclaredFields(t *testing.T) {
	response := ClaudeResponse{
		ID: "msg_test", Type: "message", Role: "assistant", Content: []ClaudeContent{},
		ExtraFields: map[string]json.RawMessage{
			"id": json.RawMessage(`"shadow"`), "model": json.RawMessage(`"shadow-model"`),
			"usage": json.RawMessage(`{"input_tokens":999}`), "stop_reason": json.RawMessage(`"shadow-reason"`),
			"future": json.RawMessage(`{"ok":true}`),
		},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["id"]) != `"msg_test"` || string(fields["future"]) != `{"ok":true}` {
		t.Fatalf("declared or extra fields changed: %s", encoded)
	}
	for _, key := range []string{"model", "usage", "stop_reason"} {
		if _, ok := fields[key]; ok {
			t.Errorf("omitted declared field %q was reintroduced by extras", key)
		}
	}
}
