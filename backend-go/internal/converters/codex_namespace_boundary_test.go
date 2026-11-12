package converters

import (
	"encoding/json"
	"github.com/BenedictKing/ccx/internal/types"
	"strings"
	"testing"
)

func TestCustomNamespaceIdentityRoundTrip(t *testing.T) {
	var request map[string]interface{}
	_ = json.Unmarshal([]byte(`{"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"left","tools":[{"type":"custom","name":"exec"}]},{"type":"namespace","name":"right","tools":[{"type":"custom","name":"exec"}]}]},{"type":"custom_tool_call","namespace":"right","name":"exec","call_id":"call1","input":""}]}`), &request)
	_, ctx, ok := HoistCodexAdditionalTools(request)
	if !ok {
		t.Fatal("not hoisted")
	}
	item := request["input"].([]interface{})[0].(map[string]interface{})
	if item["name"] != "right__exec" {
		t.Errorf("wrong namespace selected: %#v", item)
	}
	if _, exists := item["namespace"]; exists {
		t.Error("flattened history retained namespace")
	}
	for _, input := range []string{"", "print(1)"} {
		args, _ := json.Marshal(map[string]string{"input": input})
		response := &types.ResponsesResponse{Output: []types.ResponsesItem{{ID: "fc1", Type: "function_call", Name: "right__exec", CallID: "call1", Arguments: string(args), Status: "completed"}}}
		ctx.RemapCustomToolCallsInResponse(response)
		got := response.Output[0]
		if got.Type != "custom_tool_call" || got.Namespace != "right" || got.Name != "exec" || got.ID != "fc1" || got.Input != input {
			t.Errorf("identity/content lost: %+v", got)
		}
		data, _ := json.Marshal(got)
		if !strings.Contains(string(data), `"input":`) {
			t.Errorf("empty input omitted: %s", data)
		}
	}
}

func TestNamespaceFunctionKeepsExplicitStrict(t *testing.T) {
	var request map[string]interface{}
	_ = json.Unmarshal([]byte(`{"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"ns","tools":[{"type":"function","name":"run","strict":false,"defer_loading":true,"parameters":{"type":"object","additionalProperties":true}}]}]}]}`), &request)
	_, _, _ = HoistCodexAdditionalTools(request)
	tool := request["tools"].([]interface{})[0].(map[string]interface{})
	if strict, ok := tool["strict"].(bool); !ok || strict {
		t.Error("explicit strict:false was lost")
	}
	if tool["defer_loading"] != true {
		t.Error("defer_loading was lost")
	}
	tools := responsesRawToolsToOpenAIWithContext(request["tools"].([]interface{}), BuildCodexToolContextFromRaw(request["tools"].([]interface{})))
	if tools[0]["function"].(map[string]interface{})["strict"] != false {
		t.Error("Chat conversion lost strict:false")
	}
}

func TestNamespacedCustomToolChoice(t *testing.T) {
	ctx := BuildCodexToolContextFromRaw([]interface{}{map[string]interface{}{"type": "namespace", "name": "right", "tools": []interface{}{map[string]interface{}{"type": "custom", "name": "exec"}}}})
	choice := ConvertToolChoiceForCodex(map[string]interface{}{"type": "custom", "namespace": "right", "name": "exec"}, ctx)
	if choice.(map[string]interface{})["function"].(map[string]interface{})["name"] != "right__exec" {
		t.Fatalf("tool_choice lost namespace: %#v", choice)
	}
}
