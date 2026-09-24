package providers

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func extractInputJSONDelta(t *testing.T, events []string) string {
	t.Helper()
	for _, event := range events {
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonStr := strings.TrimPrefix(line, "data: ")

			var data map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
				continue
			}
			if data["type"] != "content_block_delta" {
				continue
			}
			delta, ok := data["delta"].(map[string]interface{})
			if !ok {
				continue
			}
			if delta["type"] != "input_json_delta" {
				continue
			}
			if partial, ok := delta["partial_json"].(string); ok && partial != "" {
				return partial
			}
		}
	}

	t.Fatalf("input_json_delta not found, events=%v", events)
	return ""
}

func extractMessageDeltaUsage(t *testing.T, events []string) map[string]interface{} {
	t.Helper()
	for _, event := range events {
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonStr := strings.TrimPrefix(line, "data: ")

			var data map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
				continue
			}
			if data["type"] != "message_delta" {
				continue
			}
			if usage, ok := data["usage"].(map[string]interface{}); ok {
				return usage
			}
		}
	}
	t.Fatalf("message_delta usage not found, events=%v", events)
	return nil
}

func extractMessageStartUsage(t *testing.T, events []string) map[string]interface{} {
	t.Helper()
	for _, event := range events {
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonStr := strings.TrimPrefix(line, "data: ")

			var data map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
				continue
			}
			if data["type"] != "message_start" {
				continue
			}
			message, ok := data["message"].(map[string]interface{})
			if !ok {
				continue
			}
			if usage, ok := message["usage"].(map[string]interface{}); ok {
				return usage
			}
		}
	}
	t.Fatalf("message_start usage not found, events=%v", events)
	return nil
}

type responsesToolBlockStart struct {
	index int
	id    string
	name  string
}

type responsesToolBlockDelta struct {
	index int
	json  string
}

func extractResponsesToolBlocks(t *testing.T, events []string) ([]responsesToolBlockStart, []responsesToolBlockDelta, []int) {
	t.Helper()
	var starts []responsesToolBlockStart
	var deltas []responsesToolBlockDelta
	var stops []int
	for _, event := range events {
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				continue
			}
			index, _ := data["index"].(float64)
			switch data["type"] {
			case "content_block_start":
				block, _ := data["content_block"].(map[string]interface{})
				if block["type"] == "tool_use" {
					starts = append(starts, responsesToolBlockStart{
						index: int(index),
						id:    toString(block["id"]),
						name:  toString(block["name"]),
					})
				}
			case "content_block_delta":
				delta, _ := data["delta"].(map[string]interface{})
				if delta["type"] == "input_json_delta" {
					deltas = append(deltas, responsesToolBlockDelta{
						index: int(index),
						json:  toString(delta["partial_json"]),
					})
				}
			case "content_block_stop":
				stops = append(stops, int(index))
			}
		}
	}
	return starts, deltas, stops
}

func TestResponsesProvider_HandleStreamResponse_StripsEmptyReadPages(t *testing.T) {
	body := `event: response.output_item.added
data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"Read"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","delta":"{\"file_path\":\"/tmp/x\",\"pages\":\"\"}"}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"/tmp/x\",\"pages\":\"\"}"}}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}

`

	provider := &ResponsesProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	partialJSON := extractInputJSONDelta(t, events)
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(partialJSON), &input); err != nil {
		t.Fatalf("partial_json is not valid JSON: %v, partial_json=%q", err, partialJSON)
	}
	if _, exists := input["pages"]; exists {
		t.Fatalf("pages exists = true, want false; input=%v", input)
	}
	if input["file_path"] != "/tmp/x" {
		t.Fatalf("file_path = %v, want /tmp/x", input["file_path"])
	}
}

func TestResponsesProvider_HandleStreamResponse_ParallelToolsCompleteOutOfOrder(t *testing.T) {
	body := `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item_0","call_id":"call_0","name":"Bash"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"Bash"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"item_1","delta":"{\"command\":\"second\"}"}

event: response.function_call_arguments.done
data: {"type":"response.function_call_arguments.done","output_index":1,"item_id":"item_1","arguments":"{\"command\":\"second\"}"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"Bash","arguments":"{\"command\":\"second\"}"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"item_0","delta":"{\"command\":\"first\"}"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"item_0","call_id":"call_0","name":"Bash","arguments":"{\"command\":\"first\"}"}}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","id":"item_0","call_id":"call_0","name":"Bash","arguments":"{\"command\":\"first\"}"},{"type":"function_call","id":"item_1","call_id":"call_1","name":"Bash","arguments":"{\"command\":\"second\"}"}],"usage":{"input_tokens":1,"output_tokens":1}}}

`

	provider := &ResponsesProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}
	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	starts := make(map[int]string)
	stops := make(map[int]bool)
	args := make(map[int]string)
	for _, event := range events {
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				continue
			}
			index, _ := data["index"].(float64)
			switch data["type"] {
			case "content_block_start":
				block, _ := data["content_block"].(map[string]interface{})
				if block["type"] == "tool_use" {
					starts[int(index)] = toString(block["id"])
				}
			case "content_block_delta":
				delta, _ := data["delta"].(map[string]interface{})
				if delta["type"] == "input_json_delta" {
					args[int(index)] = toString(delta["partial_json"])
				}
			case "content_block_stop":
				stops[int(index)] = true
			}
		}
	}
	if len(starts) != 2 || len(stops) != 2 {
		t.Fatalf("parallel tool blocks were not both closed: starts=%v stops=%v events=%v", starts, stops, events)
	}
	if args[0] != `{"command":"first"}` || args[1] != `{"command":"second"}` {
		t.Fatalf("parallel tool arguments crossed or were lost: %v", args)
	}
	if starts[0] != "call_0" || starts[1] != "call_1" {
		t.Fatalf("tool call IDs mismatch: %v", starts)
	}
}

func TestResponsesProvider_HandleStreamResponse_MissingItemIDDoesNotDuplicateTool(t *testing.T) {
	body := `event: response.output_item.added
data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"Read"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","delta":"{\"file_path\":\"/tmp/x\",\"pages\":\"\"}"}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"/tmp/x\",\"pages\":\"\"}"}}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","id":"item_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"/tmp/x\",\"pages\":\"\"}"}],"usage":{"input_tokens":1,"output_tokens":1}}}

`

	provider := &ResponsesProvider{}
	eventChan, _, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}
	starts, deltas, stops := extractResponsesToolBlocks(t, collectStreamEvents(eventChan))
	if len(starts) != 1 || len(deltas) != 1 || len(stops) != 1 {
		t.Fatalf("expected one complete tool block, starts=%v deltas=%v stops=%v", starts, deltas, stops)
	}
	if starts[0] != (responsesToolBlockStart{index: 0, id: "call_1", name: "Read"}) {
		t.Fatalf("tool metadata = %+v", starts[0])
	}
	if deltas[0].index != 0 || deltas[0].json != `{"file_path":"/tmp/x"}` {
		t.Fatalf("tool arguments = %+v", deltas[0])
	}
	if stops[0] != 0 {
		t.Fatalf("tool stop indexes = %v", stops)
	}
}

func TestResponsesProvider_HandleStreamResponse_DeltaBeforeAddedBindsPendingTool(t *testing.T) {
	body := `event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","delta":"{\"file_path\":\"/tmp/x\",\"pages\":\"\"}"}

event: response.output_item.added
data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"Read"}}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"/tmp/x\",\"pages\":\"\"}"}}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}

`

	provider := &ResponsesProvider{}
	eventChan, _, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}
	starts, deltas, stops := extractResponsesToolBlocks(t, collectStreamEvents(eventChan))
	if len(starts) != 1 || len(deltas) != 1 || len(stops) != 1 {
		t.Fatalf("expected one complete tool block, starts=%v deltas=%v stops=%v", starts, deltas, stops)
	}
	if starts[0] != (responsesToolBlockStart{index: 0, id: "call_1", name: "Read"}) {
		t.Fatalf("tool metadata = %+v", starts[0])
	}
	if deltas[0].json != `{"file_path":"/tmp/x"}` || stops[0] != 0 {
		t.Fatalf("tool closure = deltas=%v stops=%v", deltas, stops)
	}
}

func TestResponsesProvider_HandleStreamResponse_ArgumentsDoneClosesToolOnce(t *testing.T) {
	body := `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item_0","call_id":"call_0","name":"Bash"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"item_0","delta":"{\"command\":\"run\"}"}

event: response.function_call_arguments.done
data: {"type":"response.function_call_arguments.done","output_index":0,"item_id":"item_0","arguments":"{\"command\":\"run\"}"}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}

`

	provider := &ResponsesProvider{}
	eventChan, _, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}
	starts, deltas, stops := extractResponsesToolBlocks(t, collectStreamEvents(eventChan))
	if len(starts) != 1 || len(deltas) != 1 || len(stops) != 1 {
		t.Fatalf("arguments.done emitted duplicate closure, starts=%v deltas=%v stops=%v", starts, deltas, stops)
	}
	if deltas[0].json != `{"command":"run"}` || stops[0] != 0 {
		t.Fatalf("arguments.done closure = deltas=%v stops=%v", deltas, stops)
	}
}

func TestResponsesProvider_HandleStreamResponse_CompletedClosesPendingTool(t *testing.T) {
	body := `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item_0","call_id":"call_0","name":"Bash"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"item_0","delta":"{\"command\":\"from completed\"}"}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}

`

	provider := &ResponsesProvider{}
	eventChan, _, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}
	events := collectStreamEvents(eventChan)
	partialJSON := extractInputJSONDelta(t, events)
	if partialJSON != `{"command":"from completed"}` {
		t.Fatalf("completed fallback arguments = %q", partialJSON)
	}
	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, `"type":"content_block_stop"`) {
		t.Fatalf("completed fallback did not close pending tool: %s", joined)
	}
}

func TestResponsesProvider_HandleStreamResponse_PropagatesCacheUsageFromInputTokensDetails(t *testing.T) {
	body := `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"hello"}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5,"input_tokens_details":{"cached_tokens":7},"cache_creation_5m_input_tokens":2,"cache_ttl":"5m"}}}

`

	provider := &ResponsesProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	usage := extractMessageDeltaUsage(t, events)
	if int(usage["input_tokens"].(float64)) != 10 || int(usage["output_tokens"].(float64)) != 5 {
		t.Fatalf("basic usage mismatch: %#v", usage)
	}
	if int(usage["cache_read_input_tokens"].(float64)) != 7 {
		t.Fatalf("cache_read_input_tokens mismatch: %#v", usage)
	}
	if int(usage["cache_creation_5m_input_tokens"].(float64)) != 2 {
		t.Fatalf("cache_creation_5m_input_tokens mismatch: %#v", usage)
	}
	if usage["cache_ttl"] != "5m" {
		t.Fatalf("cache_ttl mismatch: %#v", usage)
	}
}

func TestResponsesProvider_HandleStreamResponse_LeavesResponsesTotalPromptTokensInUsage(t *testing.T) {
	body := `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"hello"}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":114931,"output_tokens":100,"cache_read_input_tokens":112256}}}

`

	provider := &ResponsesProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	messageStartUsage := extractMessageStartUsage(t, events)
	if int(messageStartUsage["input_tokens"].(float64)) != 0 {
		t.Fatalf("message_start input_tokens = %v, want 0 placeholder", messageStartUsage["input_tokens"])
	}

	usage := extractMessageDeltaUsage(t, events)
	if int(usage["input_tokens"].(float64)) != 114931 {
		t.Fatalf("input_tokens = %v, want 114931", usage["input_tokens"])
	}
	if int(usage["cache_read_input_tokens"].(float64)) != 112256 {
		t.Fatalf("cache_read_input_tokens = %v, want 112256", usage["cache_read_input_tokens"])
	}
}

func TestResponsesProvider_HandleStreamResponse_DoesNotInventMessageStartPromptTotals(t *testing.T) {
	body := `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"hello"}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":5,"input_tokens_details":{"cached_tokens":112256}}}}

`

	provider := &ResponsesProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	default:
	}

	messageStartUsage := extractMessageStartUsage(t, events)
	if int(messageStartUsage["input_tokens"].(float64)) != 0 {
		t.Fatalf("message_start input_tokens = %v, want 0 placeholder", messageStartUsage["input_tokens"])
	}

	usage := extractMessageDeltaUsage(t, events)
	if int(usage["input_tokens"].(float64)) != 1 {
		t.Fatalf("message_delta input_tokens = %v, want 1", usage["input_tokens"])
	}
	if int(usage["cache_read_input_tokens"].(float64)) != 112256 {
		t.Fatalf("cache_read_input_tokens = %v, want 112256", usage["cache_read_input_tokens"])
	}
}

// extractMessageStartModel 提取转换后 message_start 事件的模型名
func extractMessageStartModel(t *testing.T, events []string) string {
	t.Helper()
	for _, event := range events {
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				continue
			}
			if data["type"] != "message_start" {
				continue
			}
			if message, ok := data["message"].(map[string]interface{}); ok {
				if model, _ := message["model"].(string); model != "" {
					return model
				}
			}
		}
	}
	return ""
}

func TestResponsesProvider_HandleStreamResponse_MessageStartCarriesUpstreamModel(t *testing.T) {
	body := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1","model":"doubao-seed-code-latest","status":"in_progress","output":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"ok"}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","model":"doubao-seed-code-latest","usage":{"input_tokens":1,"output_tokens":1}}}

`
	p := &ResponsesProvider{}
	eventChan, _, err := p.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse failed: %v", err)
	}
	var events []string
	for ev := range eventChan {
		events = append(events, ev)
	}

	if model := extractMessageStartModel(t, events); model != "doubao-seed-code-latest" {
		t.Fatalf("message_start model = %q, want %q", model, "doubao-seed-code-latest")
	}
}

func TestResponsesProvider_HandleStreamResponse_MessageStartModelFallback(t *testing.T) {
	body := `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"ok"}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}

`
	p := &ResponsesProvider{}
	eventChan, _, err := p.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse failed: %v", err)
	}
	var events []string
	for ev := range eventChan {
		events = append(events, ev)
	}

	if model := extractMessageStartModel(t, events); model != "responses" {
		t.Fatalf("message_start model = %q, want placeholder %q", model, "responses")
	}
}
