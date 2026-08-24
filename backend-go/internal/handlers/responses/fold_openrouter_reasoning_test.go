package responses

import (
	"encoding/json"
	"strings"
	"testing"
)

// OpenRouter 流式 reasoning 非标准形态（reasoning_text.* 事件 + content[reasoning_text] 条目）
// 经 runResponsesFold 归一化后，应全部转为规范的 summary 系事件与 summary 条目。
func TestRunResponsesFoldNormalizesOpenRouterReasoningEvents(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"in_progress","summary":[]}}`,
		`data: {"type":"response.content_part.added","item_id":"rs_1","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":""}}`,
		`data: {"type":"response.reasoning_text.delta","item_id":"rs_1","output_index":0,"content_index":0,"delta":"17*"}`,
		`data: {"type":"response.reasoning_text.delta","item_id":"rs_1","output_index":0,"content_index":0,"delta":"23=391"}`,
		`data: {"type":"response.reasoning_text.done","item_id":"rs_1","text":"17*23=391"}`,
		`data: {"type":"response.content_part.done","item_id":"rs_1","part":{"type":"reasoning_text","text":"17*23=391"}}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"17*23=391"}],"encrypted_content":"ENC_1"}}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant"}}`,
		`data: {"type":"response.output_text.delta","output_index":1,"item_id":"msg_1","content_index":0,"delta":"**391**"}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"**391**"}]}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
		"",
	}, "\n\n")

	var emitted []map[string]interface{}
	_, err := runResponsesFold(
		map[string]interface{}{"model": "test", "stream": true, "input": []interface{}{}},
		responsesFoldTestResp(upstreamSSE),
		nil,
		func(event map[string]interface{}) error {
			emitted = append(emitted, event)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("runResponsesFold() err = %v", err)
	}

	types := make([]string, 0, len(emitted))
	byType := map[string][]map[string]interface{}{}
	for _, event := range emitted {
		eventType, _ := event["type"].(string)
		types = append(types, eventType)
		byType[eventType] = append(byType[eventType], event)
	}
	joined := strings.Join(types, ",")

	for _, want := range []string{
		"response.reasoning_summary_part.added",
		"response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done",
		"response.reasoning_summary_part.done",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing normalized event %q in: %s", want, joined)
		}
	}
	if strings.Contains(joined, "reasoning_text.") {
		t.Fatalf("non-standard reasoning_text events leaked: %s", joined)
	}

	// delta 的分片索引应为 summary_index
	delta := byType["response.reasoning_summary_text.delta"][0]
	if _, has := delta["summary_index"]; !has {
		t.Fatalf("delta missing summary_index: %#v", delta)
	}

	// output_item.done 的 reasoning 条目应已迁移为 summary 形态且保留 encrypted_content
	doneItems := byType["response.output_item.done"]
	var reasoningItem map[string]interface{}
	for _, event := range doneItems {
		item := event["item"].(map[string]interface{})
		if item["type"] == "reasoning" {
			reasoningItem = item
		}
	}
	if reasoningItem == nil {
		t.Fatalf("no reasoning item done event: %#v", emitted)
	}
	if _, has := reasoningItem["content"]; has {
		t.Fatalf("reasoning item still has content field: %#v", reasoningItem)
	}
	summary, ok := reasoningItem["summary"].([]interface{})
	if !ok || len(summary) == 0 || summary[0].(map[string]interface{})["text"] != "17*23=391" {
		t.Fatalf("reasoning summary not normalized: %#v", reasoningItem["summary"])
	}
	if reasoningItem["encrypted_content"] != "ENC_1" {
		t.Fatalf("encrypted_content must be preserved: %#v", reasoningItem)
	}

	// 终止事件 response.completed 里回放的 output 也应是规范形态
	terminal := byType["response.completed"][0]
	response := terminal["response"].(map[string]interface{})
	output := response["output"].([]interface{})
	first := output[0].(map[string]interface{})
	if first["type"] != "reasoning" {
		t.Fatalf("terminal output first item should be reasoning: %#v", first)
	}
	if _, has := first["content"]; has {
		t.Fatalf("terminal output reasoning item not normalized: %#v", first)
	}
}

func TestRunResponsesFoldKeepsStandardSummaryEventsUntouched(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_2","status":"in_progress","output":[]}}`,
		`data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_9","output_index":0,"summary_index":0,"delta":"官方形态"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"rs_9","type":"reasoning","summary":[{"type":"summary_text","text":"官方形态"}]}}`,
		`data: {"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		"",
	}, "\n\n")

	var emitted []map[string]interface{}
	_, err := runResponsesFold(
		map[string]interface{}{"model": "test", "stream": true, "input": []interface{}{}},
		responsesFoldTestResp(upstreamSSE),
		nil,
		func(event map[string]interface{}) error {
			emitted = append(emitted, event)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("runResponsesFold() err = %v", err)
	}
	raw, _ := json.Marshal(emitted)
	if !strings.Contains(string(raw), `"summary_index"`) {
		t.Fatalf("standard summary_index should be preserved: %s", raw)
	}
	item := emitted[2]["item"].(map[string]interface{})
	summary := item["summary"].([]interface{})[0].(map[string]interface{})
	if summary["text"] != "官方形态" || summary["type"] != "summary_text" {
		t.Fatalf("standard item was mutated: %#v", item)
	}
}
