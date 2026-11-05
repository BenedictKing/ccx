package responses

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestRunResponsesFoldDetectsContinuesAndFoldsRounds(t *testing.T) {
	baseBody := map[string]interface{}{
		"model":                "gpt-5.5",
		"stream":               true,
		"transformer_metadata": map[string]interface{}{"codex_tool_compat_enabled": false},
		"input": []interface{}{
			map[string]interface{}{"type": "message", "role": "user", "content": []interface{}{}},
		},
	}
	rounds := []string{
		responsesFoldReasoningRound("rs_1", responsesFoldStep-2, "TRUNCATED ANSWER", true),
		responsesFoldReasoningRound("rs_2", 2*responsesFoldStep-2, "STILL TRUNCATED", true),
		responsesFoldReasoningRound("rs_3", 404, "REAL ANSWER", true),
	}

	var openedBodies []map[string]interface{}
	openRound := func(body map[string]interface{}) (*http.Response, []byte, error) {
		openedBodies = append(openedBodies, cloneFoldTestMap(body))
		idx := len(openedBodies) - 1
		return responsesFoldTestResp(rounds[idx+1]), nil, nil
	}

	var emitted []map[string]interface{}
	_, err := runResponsesFold(baseBody, responsesFoldTestResp(rounds[0]), openRound, func(event map[string]interface{}) error {
		emitted = append(emitted, cloneFoldTestMap(event))
		return nil
	})
	if err != nil {
		t.Fatalf("runResponsesFold() err = %v", err)
	}

	if len(openedBodies) != 2 {
		t.Fatalf("continuation opens = %d, want 2", len(openedBodies))
	}
	for i, body := range openedBodies {
		if _, ok := body["transformer_metadata"]; ok {
			t.Fatalf("continuation %d leaked transformer_metadata: %#v", i+1, body)
		}
	}
	if got := foldTestInputTypes(openedBodies[0]["input"]); !reflect.DeepEqual(got, []string{"message", "reasoning", "message"}) {
		t.Fatalf("round 2 input types = %v", got)
	}
	if got := foldTestInputTypes(openedBodies[1]["input"]); !reflect.DeepEqual(got, []string{"message", "reasoning", "message", "reasoning", "message"}) {
		t.Fatalf("round 3 input types = %v", got)
	}
	if !foldTestIncludeContains(openedBodies[0]["include"], responsesFoldEncryptedInclude) {
		t.Fatalf("continuation include missing %q: %#v", responsesFoldEncryptedInclude, openedBodies[0]["include"])
	}
	lastInput := openedBodies[0]["input"].([]interface{})[2].(map[string]interface{})
	if lastInput["phase"] != "commentary" {
		t.Fatalf("continuation nudge phase = %v, want commentary", lastInput["phase"])
	}

	var deltas []string
	var terminals []map[string]interface{}
	seqs := make([]int, 0, len(emitted))
	outputIndexSet := make(map[int]struct{})
	for _, event := range emitted {
		seqs = append(seqs, intFromInterfaceDefault(event["sequence_number"]))
		if event["type"] == "response.output_text.delta" {
			deltas = append(deltas, stringFromInterface(event["delta"]))
		}
		if isResponsesFoldTerminalType(stringFromInterface(event["type"])) {
			terminals = append(terminals, event)
		}
		if idx, ok := intFromInterface(event["output_index"]); ok {
			outputIndexSet[idx] = struct{}{}
		}
	}
	if !reflect.DeepEqual(deltas, []string{"REAL ANSWER"}) {
		t.Fatalf("forwarded deltas = %v, want only final answer", deltas)
	}
	if len(terminals) != 1 || emitted[len(emitted)-1]["type"] != terminals[0]["type"] {
		t.Fatalf("expected exactly one final terminal event, got %d", len(terminals))
	}
	for i, seq := range seqs {
		if seq != i {
			t.Fatalf("sequence %d = %d, want %d", i, seq, i)
		}
	}
	var outputIndexes []int
	for idx := range outputIndexSet {
		outputIndexes = append(outputIndexes, idx)
	}
	sort.Ints(outputIndexes)
	if !reflect.DeepEqual(outputIndexes, []int{0, 1, 2, 3}) {
		t.Fatalf("downstream output indexes = %v, want [0 1 2 3]", outputIndexes)
	}

	terminalResponse := terminals[0]["response"].(map[string]interface{})
	usage := terminalResponse["usage"].(map[string]interface{})
	reasoning := intFromInterfaceDefault(mapFromInterface(usage["output_tokens_details"])["reasoning_tokens"])
	wantReasoning := (responsesFoldStep - 2) + (2*responsesFoldStep - 2) + 404
	if reasoning != wantReasoning {
		t.Fatalf("agent reasoning usage = %d, want %d", reasoning, wantReasoning)
	}

	metadata := terminalResponse["metadata"].(map[string]interface{})
	billed := metadata["proxy_billed_usage"].(map[string]interface{})
	if got := intFromInterfaceDefault(billed["input_tokens"]); got != 300 {
		t.Fatalf("proxy_billed_usage.input_tokens = %d, want 300", got)
	}
	roundInfo := metadata["proxy_rounds"].([]interface{})
	if len(roundInfo) != 3 {
		t.Fatalf("proxy_rounds len = %d, want 3", len(roundInfo))
	}

	output := terminalResponse["output"].([]interface{})
	gotOutput := foldTestOutputTypesAndIDs(output)
	wantOutput := []string{"reasoning:rs_1", "reasoning:rs_2", "reasoning:rs_3", "message:msg_rs_3"}
	if !reflect.DeepEqual(gotOutput, wantOutput) {
		t.Fatalf("terminal output = %v, want %v", gotOutput, wantOutput)
	}
}

func TestRunResponsesFoldStopsWhenReasoningHasNoEncryptedContent(t *testing.T) {
	baseBody := map[string]interface{}{
		"model":  "gpt-5.5",
		"stream": true,
		"input":  []interface{}{map[string]interface{}{"type": "message", "role": "user"}},
	}

	var emitted []map[string]interface{}
	_, err := runResponsesFold(
		baseBody,
		responsesFoldTestResp(responsesFoldReasoningRound("rs_1", responsesFoldStep-2, "ANSWER", false)),
		func(body map[string]interface{}) (*http.Response, []byte, error) {
			t.Fatalf("openRound should not be called when encrypted_content is missing")
			return nil, nil, nil
		},
		func(event map[string]interface{}) error {
			emitted = append(emitted, cloneFoldTestMap(event))
			return nil
		},
	)
	if err != nil {
		t.Fatalf("runResponsesFold() err = %v", err)
	}

	terminal := emitted[len(emitted)-1]
	response := terminal["response"].(map[string]interface{})
	metadata := response["metadata"].(map[string]interface{})
	if metadata["proxy_stopped_reason"] != "no_encrypted_content" {
		t.Fatalf("proxy_stopped_reason = %v, want no_encrypted_content", metadata["proxy_stopped_reason"])
	}
	var deltas []string
	for _, event := range emitted {
		if event["type"] == "response.output_text.delta" {
			deltas = append(deltas, stringFromInterface(event["delta"]))
		}
	}
	if !reflect.DeepEqual(deltas, []string{"ANSWER"}) {
		t.Fatalf("final deltas = %v, want current round answer", deltas)
	}
}

func responsesFoldReasoningRound(id string, reasoningTokens int, text string, encrypted bool) string {
	item := map[string]interface{}{"id": id, "type": "reasoning", "summary": []interface{}{}}
	doneItem := cloneFoldTestMap(item)
	if encrypted {
		doneItem["encrypted_content"] = "ENC_" + id
	}
	message := map[string]interface{}{"id": "msg_" + id, "type": "message", "role": "assistant"}
	events := []map[string]interface{}{
		{"type": "response.created", "response": map[string]interface{}{"id": "resp_1", "status": "in_progress", "output": []interface{}{}}},
		{"type": "response.in_progress", "response": map[string]interface{}{"id": "resp_1", "status": "in_progress"}},
		{"type": "response.output_item.added", "output_index": 0, "item": item},
		{"type": "response.output_item.done", "output_index": 0, "item": doneItem},
		{"type": "response.output_item.added", "output_index": 1, "item": message},
		{"type": "response.output_text.delta", "output_index": 1, "item_id": message["id"], "content_index": 0, "delta": text},
		{"type": "response.output_item.done", "output_index": 1, "item": map[string]interface{}{
			"id":      message["id"],
			"type":    "message",
			"role":    "assistant",
			"content": []interface{}{map[string]interface{}{"type": "output_text", "text": text}},
		}},
		{"type": "response.completed", "response": map[string]interface{}{
			"id":     "resp_1",
			"status": "completed",
			"output": []interface{}{},
			"usage": map[string]interface{}{
				"input_tokens":  100,
				"output_tokens": reasoningTokens + 20,
				"total_tokens":  120 + reasoningTokens,
				"output_tokens_details": map[string]interface{}{
					"reasoning_tokens": reasoningTokens,
				},
			},
		}},
	}
	var out string
	for _, event := range events {
		out += formatResponsesFoldSSE(event)
	}
	return out
}

func responsesFoldTestResp(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func cloneFoldTestMap(src map[string]interface{}) map[string]interface{} {
	data, _ := json.Marshal(src)
	var dst map[string]interface{}
	_ = json.Unmarshal(data, &dst)
	return dst
}

func foldTestInputTypes(raw interface{}) []string {
	items := raw.([]interface{})
	types := make([]string, 0, len(items))
	for _, item := range items {
		types = append(types, stringFromInterface(item.(map[string]interface{})["type"]))
	}
	return types
}

func foldTestIncludeContains(raw interface{}, want string) bool {
	items := raw.([]interface{})
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func foldTestOutputTypesAndIDs(output []interface{}) []string {
	result := make([]string, 0, len(output))
	for _, raw := range output {
		item := raw.(map[string]interface{})
		result = append(result, stringFromInterface(item["type"])+":"+stringFromInterface(item["id"]))
	}
	return result
}
