package responses

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"
)

// 未开启推理续接时，Codex 工具兼容路径不得把正文缓冲到整轮结束（PR #304）：
// 正文实时转发；需还原的工具调用仅缓冲到自身 output_item.done，还原后立即下发。
func TestRunResponsesFoldWithoutContinueStreamsBeforeRoundEnds(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: pr}
	writeEvent := func(event map[string]interface{}) {
		if _, err := io.WriteString(pw, formatResponsesFoldSSE(event)); err != nil {
			t.Errorf("写入上游事件失败: %v", err)
		}
	}

	textSeen, toolSeen := make(chan struct{}), make(chan struct{})
	var textOnce, toolOnce sync.Once
	var emitted []map[string]interface{}
	done := make(chan error, 1)
	go func() {
		openRound := func(map[string]interface{}) (*http.Response, []byte, error) {
			return nil, nil, errors.New("未开启续接时不应发起额外上游请求")
		}
		_, err := runResponsesFoldWithOptions(map[string]interface{}{"model": "gpt-5.5", "input": []interface{}{}}, resp, openRound, func(event map[string]interface{}) error {
			emitted = append(emitted, cloneFoldTestMap(event))
			switch event["type"] {
			case "response.output_text.delta":
				textOnce.Do(func() { close(textSeen) })
			case "response.output_item.done":
				if mapFromInterface(event["item"])["type"] == "custom_tool_call" {
					toolOnce.Do(func() { close(toolSeen) })
				}
			}
			return nil
		}, foldCodexTestContext(), false)
		done <- err
	}()
	waitFor := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("上游未结束前未收到%s", what)
		}
	}

	message := map[string]interface{}{"id": "msg_1", "type": "message", "role": "assistant"}
	writeEvent(map[string]interface{}{"type": "response.created", "response": map[string]interface{}{"id": "resp_1", "status": "in_progress", "output": []interface{}{}}})
	writeEvent(map[string]interface{}{"type": "response.output_item.added", "output_index": 0, "item": message})
	writeEvent(map[string]interface{}{"type": "response.output_text.delta", "output_index": 0, "item_id": "msg_1", "content_index": 0, "delta": "hello"})
	waitFor(textSeen, "正文 delta")

	writeEvent(map[string]interface{}{"type": "response.output_item.done", "output_index": 0, "item": map[string]interface{}{
		"id": "msg_1", "type": "message", "role": "assistant",
		"content": []interface{}{map[string]interface{}{"type": "output_text", "text": "hello"}},
	}})
	call := map[string]interface{}{"id": "fc_1", "type": "function_call", "name": "functions__exec", "call_id": "call_1", "arguments": ""}
	writeEvent(map[string]interface{}{"type": "response.output_item.added", "output_index": 1, "item": call})
	writeEvent(map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": 1, "delta": `{"input":"ls"}`})
	writeEvent(map[string]interface{}{"type": "response.function_call_arguments.done", "output_index": 1, "arguments": `{"input":"ls"}`})
	writeEvent(map[string]interface{}{"type": "response.output_item.done", "output_index": 1, "item": map[string]interface{}{
		"id": "fc_1", "type": "function_call", "name": "functions__exec", "call_id": "call_1", "arguments": `{"input":"ls"}`, "status": "completed",
	}})
	waitFor(toolSeen, "还原后的工具调用")

	writeEvent(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{
		"id": "resp_1", "status": "completed", "output": []interface{}{},
		"usage": map[string]interface{}{"input_tokens": 1, "output_tokens": 2, "total_tokens": 3},
	}})
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatalf("runResponsesFoldWithOptions() err = %v", err)
	}

	var gotTypes []string
	for i, event := range emitted {
		if seq, _ := event["sequence_number"].(float64); int(seq) != i {
			t.Fatalf("事件 %d sequence_number = %v，应单调连续", i, event["sequence_number"])
		}
		gotTypes = append(gotTypes, stringFromInterface(event["type"]))
	}
	wantTypes := []string{
		"response.created",
		"response.output_item.added", "response.output_text.delta", "response.output_item.done",
		"response.output_item.added", "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done", "response.output_item.done",
		"response.completed",
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("事件序列 = %v\nwant %v", gotTypes, wantTypes)
	}
	if idx := emitted[5]["output_index"]; idx != float64(1) {
		t.Fatalf("工具调用 output_index = %v, want 1", idx)
	}
	output, _ := mapFromInterface(emitted[len(emitted)-1]["response"])["output"].([]interface{})
	var outputTypes []string
	for _, item := range output {
		outputTypes = append(outputTypes, stringFromInterface(mapFromInterface(item)["type"]))
	}
	if !reflect.DeepEqual(outputTypes, []string{"message", "custom_tool_call"}) {
		t.Fatalf("终态 output 类型 = %v", outputTypes)
	}
}
