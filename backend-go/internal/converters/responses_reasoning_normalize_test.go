package converters

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustEvent(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	return event
}

func TestNormalizeOpenRouterReasoningEvent(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantChanged bool
		wantType    string
		wantSummary interface{}
	}{
		{
			name:        "reasoning_text_delta 改名为 summary_text 并迁移索引",
			in:          `{"type":"response.reasoning_text.delta","item_id":"rs_1","output_index":0,"content_index":0,"delta":"思考"}`,
			wantChanged: true,
			wantType:    "response.reasoning_summary_text.delta",
			wantSummary: nil,
		},
		{
			name:        "reasoning_text_done 改名",
			in:          `{"type":"response.reasoning_text.done","item_id":"rs_1","text":"思考完成"}`,
			wantChanged: true,
			wantType:    "response.reasoning_summary_text.done",
		},
		{
			name:        "content_part_added 的 reasoning_text part 转为 summary part",
			in:          `{"type":"response.content_part.added","item_id":"rs_1","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":""}}`,
			wantChanged: true,
			wantType:    "response.reasoning_summary_part.added",
		},
		{
			name:        "content_part_done 携带完整文本",
			in:          `{"type":"response.content_part.done","part":{"type":"reasoning_text","text":"完整思考"}}`,
			wantChanged: true,
			wantType:    "response.reasoning_summary_part.done",
		},
		{
			name:        "标准 summary 事件不动",
			in:          `{"type":"response.reasoning_summary_text.delta","summary_index":0,"delta":"x"}`,
			wantChanged: false,
			wantType:    "response.reasoning_summary_text.delta",
		},
		{
			name:        "output_text 事件不动",
			in:          `{"type":"response.output_text.delta","delta":"hi"}`,
			wantChanged: false,
			wantType:    "response.output_text.delta",
		},
		{
			name:        "无 type 的事件不动",
			in:          `{"foo":"bar"}`,
			wantChanged: false,
			wantType:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := mustEvent(t, tt.in)
			changed := NormalizeOpenRouterReasoningEvent(event)
			if changed != tt.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if got, _ := event["type"].(string); got != tt.wantType {
				t.Fatalf("type = %v, want %v", got, tt.wantType)
			}
			if _, has := event["content_index"]; has && changed {
				t.Fatalf("content_index should be renamed to summary_index: %#v", event)
			}
		})
	}
}

func TestNormalizeOpenRouterReasoningEventOutputItem(t *testing.T) {
	event := mustEvent(t, `{
		"type": "response.output_item.done",
		"item": {
			"id": "rs_1",
			"type": "reasoning",
			"status": "completed",
			"summary": [],
			"content": [{"type": "reasoning_text", "text": "17*20=340"}]
		}
	}`)
	if !NormalizeOpenRouterReasoningEvent(event) {
		t.Fatal("expected change")
	}
	item := event["item"].(map[string]interface{})
	if _, has := item["content"]; has {
		t.Fatalf("content field should be removed: %#v", item)
	}
	summary, ok := item["summary"].([]interface{})
	if !ok || len(summary) != 1 {
		t.Fatalf("summary = %#v, want one part", item["summary"])
	}
	part := summary[0].(map[string]interface{})
	if part["type"] != "summary_text" || part["text"] != "17*20=340" {
		t.Fatalf("summary part = %#v", part)
	}

	// 幂等：再次调用不再改动
	if NormalizeOpenRouterReasoningEvent(event) {
		t.Fatal("second call should be a no-op")
	}
}

func TestNormalizeOpenRouterReasoningEventKeepsStandardItem(t *testing.T) {
	event := mustEvent(t, `{
		"type": "response.output_item.done",
		"item": {
			"id": "rs_2",
			"type": "reasoning",
			"summary": [{"type": "summary_text", "text": "官方形态"}]
		}
	}`)
	if NormalizeOpenRouterReasoningEvent(event) {
		t.Fatal("standard item should not be touched")
	}
}

func TestNormalizeOpenRouterReasoningEventSkipsUnknownContentParts(t *testing.T) {
	event := mustEvent(t, `{
		"type": "response.output_item.done",
		"item": {
			"id": "rs_3",
			"type": "reasoning",
			"summary": [],
			"content": [{"type": "reasoning_text", "text": "a"}, {"type": "custom", "text": "b"}]
		}
	}`)
	if NormalizeOpenRouterReasoningEvent(event) {
		t.Fatal("unknown content part types should be left untouched")
	}
}

func TestNormalizeOpenRouterReasoningResponseBody(t *testing.T) {
	body := map[string]interface{}{
		"id": "resp_1",
		"output": []interface{}{
			map[string]interface{}{
				"id":      "rs_1",
				"type":    "reasoning",
				"summary": []interface{}{},
				"content": []interface{}{map[string]interface{}{"type": "reasoning_text", "text": "思考"}},
			},
			map[string]interface{}{
				"id":      "msg_1",
				"type":    "message",
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "output_text", "text": "391"}},
			},
		},
	}
	if !NormalizeOpenRouterReasoningResponseBody(body) {
		t.Fatal("expected change")
	}
	reasoning := body["output"].([]interface{})[0].(map[string]interface{})
	if _, has := reasoning["content"]; has {
		t.Fatalf("reasoning content should be removed: %#v", reasoning)
	}
	message := body["output"].([]interface{})[1].(map[string]interface{})
	if _, has := message["content"]; !has {
		t.Fatal("message content must be preserved")
	}

	// 标准响应体零改动
	standard := map[string]interface{}{
		"output": []interface{}{
			map[string]interface{}{
				"id":      "rs_2",
				"type":    "reasoning",
				"summary": []interface{}{map[string]interface{}{"type": "summary_text", "text": "官方"}},
			},
		},
	}
	if NormalizeOpenRouterReasoningResponseBody(standard) {
		t.Fatal("standard body should not be touched")
	}
}

func TestNormalizeOpenRouterReasoningSSELine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "非 data 行原样返回",
			in:   "event: response.created",
			want: "event: response.created",
		},
		{
			name: "DONE 行原样返回",
			in:   "data: [DONE]",
			want: "data: [DONE]",
		},
		{
			name: "不含 reasoning 的行不解析",
			in:   `data: {"type":"response.output_text.delta","delta":"hi"}`,
			want: `data: {"type":"response.output_text.delta","delta":"hi"}`,
		},
		{
			name: "畸形 JSON 原样返回",
			in:   `data: {"type":"response.reasoning_text.delta","delta":`,
			want: `data: {"type":"response.reasoning_text.delta","delta":`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeOpenRouterReasoningSSELine(tt.in); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}

	line := `data:{"type":"response.reasoning_text.delta","content_index":0,"delta":"想"}`
	got := NormalizeOpenRouterReasoningSSELine(line)
	if !strings.HasPrefix(got, "data: ") || !strings.Contains(got, "reasoning_summary_text.delta") {
		t.Fatalf("unexpected normalization: %q", got)
	}
	if strings.Contains(got, "content_index") {
		t.Fatalf("content_index should be renamed: %q", got)
	}
}
