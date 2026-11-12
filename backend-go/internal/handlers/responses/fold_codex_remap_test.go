package responses

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/converters"
)

func foldCodexTestContext() *converters.CodexToolContext {
	return &converters.CodexToolContext{
		CustomTools: map[string]converters.CodexCustomToolSpec{
			"functions__exec": {OpenAIName: "exec", Kind: converters.CodexCustomToolExec},
		},
		FunctionTools: map[string]converters.CodexFunctionToolSpec{
			"functions__wait": {Namespace: "functions", Name: "wait"},
		},
		HasCustomTools:    true,
		HasNamespaceTools: true,
	}
}

func TestRemapFoldCodexEntry_CustomProxy(t *testing.T) {
	entry := &responsesFoldBufferedOutput{
		item: map[string]interface{}{
			"id": "fc_1", "type": "function_call", "name": "functions__exec",
			"call_id": "call_1", "arguments": `{"input":"console.log(1)"}`, "status": "completed",
		},
		events: []map[string]interface{}{
			{"type": "response.output_item.added", "output_index": 1, "item": map[string]interface{}{
				"id": "fc_1", "type": "function_call", "name": "functions__exec", "call_id": "call_1", "arguments": "",
			}},
			{"type": "response.function_call_arguments.delta", "output_index": 1, "delta": `{"input":`},
			{"type": "response.function_call_arguments.delta", "output_index": 1, "delta": `"console.log(1)"}`},
			{"type": "response.function_call_arguments.done", "output_index": 1, "arguments": `{"input":"console.log(1)"}`},
			{"type": "response.output_item.done", "output_index": 1, "item": map[string]interface{}{
				"id": "fc_1", "type": "function_call", "name": "functions__exec", "call_id": "call_1",
				"arguments": `{"input":"console.log(1)"}`, "status": "completed",
			}},
		},
	}

	remapFoldCodexEntry(foldCodexTestContext(), entry)

	types := make([]string, 0, len(entry.events))
	for _, event := range entry.events {
		types = append(types, event["type"].(string))
	}
	want := []string{
		"response.output_item.added",
		"response.custom_tool_call_input.delta",
		"response.custom_tool_call_input.done",
		"response.output_item.done",
	}
	if len(types) != len(want) {
		t.Fatalf("事件序列 = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("事件序列 = %v, want %v", types, want)
		}
	}

	added := entry.events[0]["item"].(map[string]interface{})
	if added["type"] != "custom_tool_call" || added["name"] != "exec" || added["input"] != "" {
		t.Fatalf("added item = %#v", added)
	}
	if _, leaked := added["arguments"]; leaked {
		t.Fatalf("added item 不应残留 arguments: %#v", added)
	}

	delta := entry.events[1]
	if delta["delta"] != "console.log(1)" || delta["call_id"] != "call_1" || delta["item_id"] != "fc_1" {
		t.Fatalf("custom_tool_call_input.delta = %#v", delta)
	}

	doneItem := entry.events[3]["item"].(map[string]interface{})
	if doneItem["type"] != "custom_tool_call" || doneItem["name"] != "exec" || doneItem["input"] != "console.log(1)" {
		t.Fatalf("done item = %#v", doneItem)
	}

	if entry.item["type"] != "custom_tool_call" || entry.item["name"] != "exec" || entry.item["input"] != "console.log(1)" {
		t.Fatalf("entry.item = %#v", entry.item)
	}
}

func TestRemapFoldCodexEntry_CustomProxyWithoutArgumentsDone(t *testing.T) {
	// 上游缺 arguments.done 时，在 output_item.done 前补 input 事件
	entry := &responsesFoldBufferedOutput{
		item: map[string]interface{}{
			"id": "fc_1", "type": "function_call", "name": "functions__exec",
			"call_id": "call_1", "arguments": `{"input":"ls"}`, "status": "completed",
		},
		events: []map[string]interface{}{
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]interface{}{
				"id": "fc_1", "type": "function_call", "name": "functions__exec", "call_id": "call_1", "arguments": "",
			}},
			{"type": "response.output_item.done", "output_index": 0, "item": map[string]interface{}{
				"id": "fc_1", "type": "function_call", "name": "functions__exec", "call_id": "call_1",
				"arguments": `{"input":"ls"}`, "status": "completed",
			}},
		},
	}

	remapFoldCodexEntry(foldCodexTestContext(), entry)

	if len(entry.events) != 4 {
		types := []string{}
		for _, e := range entry.events {
			types = append(types, e["type"].(string))
		}
		t.Fatalf("事件序列 = %v, want added+delta+done+item.done", types)
	}
	if entry.events[1]["type"] != "response.custom_tool_call_input.delta" || entry.events[1]["delta"] != "ls" {
		t.Fatalf("补发的 input delta 异常: %#v", entry.events[1])
	}
	if entry.events[2]["type"] != "response.custom_tool_call_input.done" {
		t.Fatalf("补发的 input done 异常: %#v", entry.events[2])
	}
}

func TestRemapFoldCodexEntry_NamespaceFunction(t *testing.T) {
	entry := &responsesFoldBufferedOutput{
		item: map[string]interface{}{
			"id": "fc_2", "type": "function_call", "name": "functions__wait",
			"call_id": "call_2", "arguments": `{"cell_id":"c1"}`, "status": "completed",
		},
		events: []map[string]interface{}{
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]interface{}{
				"id": "fc_2", "type": "function_call", "name": "functions__wait", "call_id": "call_2", "arguments": "",
			}},
			{"type": "response.function_call_arguments.delta", "output_index": 0, "delta": `{"cell_id":"c1"}`},
			{"type": "response.function_call_arguments.done", "output_index": 0, "arguments": `{"cell_id":"c1"}`},
			{"type": "response.output_item.done", "output_index": 0, "item": map[string]interface{}{
				"id": "fc_2", "type": "function_call", "name": "functions__wait", "call_id": "call_2",
				"arguments": `{"cell_id":"c1"}`, "status": "completed",
			}},
		},
	}

	remapFoldCodexEntry(foldCodexTestContext(), entry)

	// namespace function：事件序列保持不变，仅 added/done 的 item 还原 name+namespace
	if len(entry.events) != 4 {
		t.Fatalf("事件数 = %d, want 4（不重组）", len(entry.events))
	}
	for _, idx := range []int{0, 3} {
		item := entry.events[idx]["item"].(map[string]interface{})
		if item["name"] != "wait" || item["namespace"] != "functions" {
			t.Fatalf("event[%d].item = %#v, want name=wait namespace=functions", idx, item)
		}
		if item["type"] != "function_call" {
			t.Fatalf("event[%d].item.type = %v, want function_call 保留", idx, item["type"])
		}
	}
	if entry.events[1]["type"] != "response.function_call_arguments.delta" {
		t.Fatalf("arguments delta 应保留: %#v", entry.events[1])
	}
	if entry.item["name"] != "wait" || entry.item["namespace"] != "functions" {
		t.Fatalf("entry.item = %#v", entry.item)
	}
}

func TestRemapFoldCodexEntry_Untouched(t *testing.T) {
	// nil ctx / 非 function_call / 未知名都不动
	plain := &responsesFoldBufferedOutput{
		item:   map[string]interface{}{"id": "m1", "type": "message"},
		events: []map[string]interface{}{{"type": "response.output_item.added", "item": map[string]interface{}{"id": "m1", "type": "message"}}},
	}
	remapFoldCodexEntry(nil, plain)
	remapFoldCodexEntry(foldCodexTestContext(), plain)
	if plain.item["type"] != "message" || len(plain.events) != 1 {
		t.Fatalf("非 function_call 不应被改动: %#v", plain.item)
	}

	unknown := &responsesFoldBufferedOutput{
		item:   map[string]interface{}{"id": "fc_9", "type": "function_call", "name": "unknown_tool", "call_id": "c9"},
		events: []map[string]interface{}{{"type": "response.output_item.done", "item": map[string]interface{}{"id": "fc_9", "type": "function_call", "name": "unknown_tool"}}},
	}
	remapFoldCodexEntry(foldCodexTestContext(), unknown)
	if unknown.item["name"] != "unknown_tool" {
		t.Fatalf("未知工具名不应被改动: %#v", unknown.item)
	}
	if _, hasNs := unknown.item["namespace"]; hasNs {
		t.Fatalf("未知工具不应加 namespace: %#v", unknown.item)
	}
}
