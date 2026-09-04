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

func TestNormalizeOpenRouterReasoningEventKeepsPlainContentPart(t *testing.T) {
	// 普通 message 的 content_part 事件在 fold 主路上高频出现，误改会破坏正文流
	tests := []struct {
		name string
		in   string
	}{
		{
			name: "text part 不动",
			in:   `{"type":"response.content_part.added","item_id":"msg_1","output_index":1,"content_index":0,"part":{"type":"text","text":""}}`,
		},
		{
			name: "output_text part 不动",
			in:   `{"type":"response.content_part.done","item_id":"msg_1","output_index":1,"content_index":0,"part":{"type":"output_text","text":"391"}}`,
		},
		{
			name: "part 缺失不动",
			in:   `{"type":"response.content_part.added","item_id":"msg_1","content_index":0}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := mustEvent(t, tt.in)
			before, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if NormalizeOpenRouterReasoningEvent(event) {
				t.Fatalf("plain content_part event must not be touched: %s", tt.in)
			}
			after, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(before) != string(after) {
				t.Fatalf("event mutated: before=%s after=%s", before, after)
			}
		})
	}
}

func TestNormalizeOpenRouterReasoningEventPreservesExistingSummaryIndex(t *testing.T) {
	event := mustEvent(t, `{"type":"response.reasoning_text.delta","item_id":"rs_1","content_index":3,"summary_index":0,"delta":"x"}`)
	if !NormalizeOpenRouterReasoningEvent(event) {
		t.Fatal("event type rename should still happen")
	}
	if got, _ := event["summary_index"].(float64); got != 0 {
		t.Fatalf("existing summary_index must be preserved, got %v", event["summary_index"])
	}
	if _, has := event["content_index"]; !has {
		t.Fatal("content_index should be kept when summary_index already exists")
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

func TestNormalizeOpenRouterReasoningResponseBodyIdempotent(t *testing.T) {
	body := mustEvent(t, `{
		"output": [
			{"id": "rs_1", "type": "reasoning", "summary": [], "content": [{"type": "reasoning_text", "text": "思考"}]}
		]
	}`)
	if !NormalizeOpenRouterReasoningResponseBody(body) {
		t.Fatal("first call should normalize")
	}
	if NormalizeOpenRouterReasoningResponseBody(body) {
		t.Fatal("second call should be a no-op")
	}
}

func TestNormalizeOpenRouterReasoningResponseBodySkipsMixedParts(t *testing.T) {
	// reasoning 条目 content 混入未知 part 类型时保守跳过，Event 层与 Body 层行为必须一致
	body := mustEvent(t, `{
		"output": [
			{"id": "rs_1", "type": "reasoning", "summary": [], "content": [{"type": "reasoning_text", "text": "a"}, {"type": "custom", "text": "b"}]}
		]
	}`)
	if NormalizeOpenRouterReasoningResponseBody(body) {
		t.Fatal("mixed content parts should be left untouched")
	}
}

func TestNormalizeOpenRouterReasoningResponseBodyMissingSummaryField(t *testing.T) {
	// summary 字段缺失（而非空数组）时仍应迁移，防御上游形态漂移
	body := mustEvent(t, `{
		"output": [
			{"id": "rs_1", "type": "reasoning", "content": [{"type": "reasoning_text", "text": "思考"}]}
		]
	}`)
	if !NormalizeOpenRouterReasoningResponseBody(body) {
		t.Fatal("missing summary field should still normalize")
	}
	summary, ok := body["output"].([]interface{})[0].(map[string]interface{})["summary"].([]interface{})
	if !ok || len(summary) != 1 {
		t.Fatalf("summary = %#v, want one part", body["output"])
	}
	if part, _ := summary[0].(map[string]interface{}); part["text"] != "思考" {
		t.Fatalf("summary part = %#v", part)
	}
}

func TestNormalizeOpenRouterReasoningEventOutputItemAdded(t *testing.T) {
	event := mustEvent(t, `{
		"type": "response.output_item.added",
		"output_index": 0,
		"item": {"id": "rs_1", "type": "reasoning", "summary": [], "content": [{"type": "reasoning_text", "text": ""}]}
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
}

func TestNormalizeOpenRouterReasoningEventMigratesAllPartsInOrder(t *testing.T) {
	event := mustEvent(t, `{
		"type": "response.output_item.done",
		"item": {
			"id": "rs_1",
			"type": "reasoning",
			"summary": [],
			"content": [
				{"type": "reasoning_text", "text": "第一段"},
				{"type": "reasoning_text", "text": "第二段"},
				{"type": "reasoning_text", "text": "第三段"}
			]
		}
	}`)
	if !NormalizeOpenRouterReasoningEvent(event) {
		t.Fatal("expected change")
	}
	summary := event["item"].(map[string]interface{})["summary"].([]interface{})
	if len(summary) != 3 {
		t.Fatalf("summary len = %d, want 3", len(summary))
	}
	for i, want := range []string{"第一段", "第二段", "第三段"} {
		part, _ := summary[i].(map[string]interface{})
		if part["type"] != "summary_text" || part["text"] != want {
			t.Fatalf("summary[%d] = %#v, want type=summary_text text=%s", i, part, want)
		}
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
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(got, "data: ")), &parsed); err != nil {
		t.Fatalf("normalized line must stay valid JSON: %q: %v", got, err)
	}
	if parsed["type"] != "response.reasoning_summary_text.delta" || parsed["delta"] != "想" {
		t.Fatalf("normalized payload = %#v", parsed)
	}
	if idx, _ := parsed["summary_index"].(float64); idx != 0 {
		t.Fatalf("summary_index = %v, want 0 (migrated from content_index)", parsed["summary_index"])
	}

	// 幂等：对已归一化的行再次调用返回不变
	if again := NormalizeOpenRouterReasoningSSELine(got); again != got {
		t.Fatalf("idempotency violated: %q -> %q", got, again)
	}

	// output_item 路径整行归一化同样生效
	itemLine := `data: {"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"全文"}]}}`
	gotItem := NormalizeOpenRouterReasoningSSELine(itemLine)
	if !strings.Contains(gotItem, `"summary_text"`) || strings.Contains(gotItem, `"reasoning_text"`) {
		t.Fatalf("output_item line should be normalized: %q", gotItem)
	}
}
