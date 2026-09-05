package common

import (
	"encoding/json"
	"testing"
)

func TestStripResponsesEncryptedContent(t *testing.T) {
	body := []byte(`{
	  "model": "gpt-5.6-sol",
	  "input": [
	    {"type": "message", "role": "user", "content": "hi"},
	    {"type": "reasoning", "id": "rs_1", "summary": [{"type":"summary_text","text":"thought"}], "encrypted_content": "gAAAA-secret-1"},
	    {"type": "reasoning", "id": "rs_2", "summary": [], "encrypted_content": "gAAAA-secret-2"},
	    {"type": "function_call", "name": "exec", "call_id": "c1", "arguments": "{}"}
	  ]
	}`)

	out := stripResponsesEncryptedContent(body)

	var req struct {
		Input []map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("输出不是合法 JSON: %v", err)
	}
	if len(req.Input) != 4 {
		t.Fatalf("input 条目数 = %d, want 4", len(req.Input))
	}
	for i, item := range req.Input {
		if string(item["type"]) == `"reasoning"` {
			if _, has := item["encrypted_content"]; has {
				t.Fatalf("input[%d] reasoning 仍含 encrypted_content", i)
			}
			if _, has := item["summary"]; !has {
				t.Fatalf("input[%d] reasoning 应保留 summary", i)
			}
		}
	}
	if string(req.Input[3]["call_id"]) != `"c1"` {
		t.Fatal("非 reasoning 条目不应被改动")
	}

	// 非 reasoning / 无密文 / input 为字符串 / 非法 JSON：原样返回
	unchanged := []byte(`{"input": "just a string"}`)
	if got := stripResponsesEncryptedContent(unchanged); string(got) != string(unchanged) {
		t.Fatalf("字符串 input 应原样返回, got %s", got)
	}
	if got := stripResponsesEncryptedContent([]byte(`not json`)); string(got) != "not json" {
		t.Fatal("非法 JSON 应原样返回")
	}
}
