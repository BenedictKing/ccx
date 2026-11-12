package converters

import (
	"encoding/json"
	"github.com/BenedictKing/ccx/internal/types"
	"strings"
	"testing"
)

func TestAdditionalToolsMergeAndIdempotence(t *testing.T) {
	const raw = `{"tools":[{"type":"function","name":"existing","parameters":{"type":"object"}}],"input":[{"type":"message","role":"user","content":"hello"},{"type":"additional_tools","tools":[{"type":"function","name":"added","parameters":{"type":"object"}}]}]}`
	var request map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		t.Fatal(err)
	}
	original, _, changed := HoistCodexAdditionalTools(request)
	if !changed || len(original) != 2 || len(request["tools"].([]interface{})) != 2 {
		t.Fatalf("merged tools=%#v", request)
	}
	once, _ := json.Marshal(request)
	_, _, again := HoistCodexAdditionalTools(request)
	twice, _ := json.Marshal(request)
	if again || string(once) != string(twice) {
		t.Fatal("hoisting is not idempotent")
	}
	converted := ConvertResponsesToOpenAIChatRequest("test", []byte(raw), false)
	if !strings.Contains(string(converted), `"name":"existing"`) || !strings.Contains(string(converted), `"name":"added"`) {
		t.Fatalf("raw conversion lost tools: %s", converted)
	}
}

func TestEncryptedArgumentsConvertToValidEmptyJSON(t *testing.T) {
	item := map[string]interface{}{"type": "function_call", "name": "notes", "call_id": "call1", "encrypted_function_args": []interface{}{"cipher"}}
	// 对无密文对应字段的协议保留调用和非空 JSON 参数，无法解密时不伪造明文。
	raw, _ := json.Marshal(map[string]interface{}{"input": []interface{}{item}})
	var request map[string]interface{}
	_ = json.Unmarshal(raw, &request)
	items, err := types.ParseResponsesInput(request["input"])
	if err != nil {
		t.Fatal(err)
	}
	_, _, args, err := resolveFunctionCallItem(items[0])
	if err != nil || args != "{}" {
		t.Fatalf("args=%q err=%v", args, err)
	}
}

func TestAdditionalToolsExtendExistingNamespace(t *testing.T) {
	var request map[string]interface{}
	_ = json.Unmarshal([]byte(`{"tools":[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}],"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]}]}]}`), &request)
	_, ctx, ok := HoistCodexAdditionalTools(request)
	if !ok || len(request["tools"].([]interface{})) != 2 || !ctx.IsCustomToolProxy("functions__exec") {
		t.Fatalf("namespace extension lost: %#v", request)
	}
}

func TestHoistCodexAdditionalToolsRewritesFunctionItemIDPrefixes(t *testing.T) {
	var request map[string]interface{}
	_ = json.Unmarshal([]byte(`{"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]}]},{"type":"custom_tool_call","id":"ctc_call_suffix","call_id":"call_1","name":"exec","input":"print(1)"},{"type":"custom_tool_call_output","id":"ctco_output_suffix","call_id":"call_1","output":"1"}]}`), &request)
	_, _, ok := HoistCodexAdditionalTools(request)
	if !ok {
		t.Fatal("expected additional tools to be hoisted")
	}
	items := request["input"].([]interface{})
	call := items[0].(map[string]interface{})
	output := items[1].(map[string]interface{})
	if call["type"] != "function_call" || call["id"] != "fc_call_suffix" {
		t.Errorf("function call identity = type %v, id %v", call["type"], call["id"])
	}
	if output["type"] != "function_call_output" || output["id"] != "fco_output_suffix" {
		t.Errorf("function output identity = type %v, id %v", output["type"], output["id"])
	}
	if call["call_id"] != "call_1" || output["call_id"] != "call_1" {
		t.Error("call_id must remain unchanged")
	}
}
