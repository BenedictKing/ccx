package utils

import (
	"encoding/json"
	"github.com/BenedictKing/ccx/internal/types"
	"strings"
	"testing"
)

func TestResponsesBudgetCountsToolPayloads(t *testing.T) {
	for _, kind := range []string{"function_call", "function_call_output", "custom_tool_call", "additional_tools", "future_item"} {
		field := map[string]string{"function_call": "arguments", "function_call_output": "output", "custom_tool_call": "input", "additional_tools": "tools", "future_item": "future_payload"}[kind]
		item := map[string]interface{}{"type": kind, field: strings.Repeat("x", 350000)}
		body, _ := json.Marshal(map[string]interface{}{"input": []interface{}{item}})
		if got := EstimateResponsesRequestTokens(body); got < 100000 {
			t.Errorf("%s payload undercounted: %d", kind, got)
		}
	}
}

func TestResponsesBudgetCountsActualToolSchema(t *testing.T) {
	small := map[string]interface{}{"type": "function", "name": "read", "parameters": map[string]interface{}{"type": "object"}}
	large := map[string]interface{}{"type": "function", "name": "read", "description": strings.Repeat("x", 35000)}
	count := func(tool interface{}) int {
		b, _ := json.Marshal(map[string]interface{}{"input": "hello", "tools": []interface{}{tool}})
		return EstimateResponsesRequestTokens(b)
	}
	if count(large)-count(small) < 9900 {
		t.Fatal("tool schema size did not affect estimate")
	}
}

func TestEncryptedArgumentsCountAndRoundTrip(t *testing.T) {
	item := map[string]interface{}{"type": "function_call", "name": "notes", "encrypted_function_args": []interface{}{strings.Repeat("A", 1500), strings.Repeat("B", 1500)}}
	items, err := types.ParseResponsesInput([]interface{}{item})
	if err != nil {
		t.Fatal(err)
	}
	if len(items[0].EncryptedFunctionArgs) != 2 {
		t.Fatal("encrypted argument segments lost")
	}
	encoded, _ := json.Marshal(items)
	if !strings.Contains(string(encoded), "encrypted_function_args") {
		t.Fatal("segments lost during serialization")
	}
	rawCount := EstimateResponsesOutputTokens([]interface{}{item})
	typedCount := EstimateResponsesOutputTokens(items)
	if rawCount < 2000 || rawCount != typedCount {
		t.Fatalf("encrypted estimates raw=%d typed=%d", rawCount, typedCount)
	}
}
