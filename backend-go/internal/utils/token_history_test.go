package utils

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestResponsesBudgetCountsToolPayloads(t *testing.T) {
	for _, kind := range []string{"function_call", "function_call_output", "custom_tool_call", "additional_tools", "future_item"} {
		field := map[string]string{"function_call": "arguments", "function_call_output": "output", "custom_tool_call": "input", "additional_tools": "tools", "future_item": "future_payload"}[kind]
		item := map[string]interface{}{"type": kind, field: strings.Repeat("x", 350000)}
		body, _ := json.Marshal(map[string]interface{}{"input": []interface{}{item}})
		if got := EstimateResponsesRequestTokens(body); got < 87000 || got > 88000 {
			t.Errorf("%s payload estimate = %d, want approximately 87500", kind, got)
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
	if diff := count(large) - count(small); diff < 8700 || diff > 8800 {
		t.Fatalf("tool schema estimate difference = %d, want approximately 8750", diff)
	}
}

func TestResponsesJSONDoesNotCountHTMLEscaping(t *testing.T) {
	count := func(description string) int {
		tools := []interface{}{map[string]interface{}{"type": "function", "description": description}}
		body, _ := json.Marshal(map[string]interface{}{"input": []interface{}{map[string]interface{}{"type": "additional_tools", "tools": tools}}})
		return EstimateResponsesRequestTokens(body)
	}
	if plain, symbols := count("AAAA"), count("<>&"); plain != symbols {
		t.Fatalf("same-length JSON text differs after escaping: plain=%d symbols=%d", plain, symbols)
	}
}

func TestResponsesReasoningMatchesCodexEstimate(t *testing.T) {
	for _, test := range []struct {
		encodedLength int
		want          int
	}{
		{encodedLength: 4000, want: 588},
		{encodedLength: 100, want: 0},
	} {
		item := map[string]interface{}{
			"type":              "reasoning",
			"id":                strings.Repeat("metadata", 1000),
			"encrypted_content": strings.Repeat("A", test.encodedLength),
			"summary":           strings.Repeat("not replayed", 1000),
		}
		body, _ := json.Marshal(map[string]interface{}{"input": []interface{}{item}})
		if got := EstimateResponsesRequestTokens(body); got != test.want {
			t.Errorf("encoded length %d: estimate = %d, want %d", test.encodedLength, got, test.want)
		}
	}
}

func TestResponsesContextExcludesTransportMetadata(t *testing.T) {
	item := map[string]interface{}{
		"type":    "message",
		"id":      strings.Repeat("metadata", 1000),
		"role":    "user",
		"content": "hello world",
	}
	body, _ := json.Marshal(map[string]interface{}{"input": []interface{}{item}})
	if got := EstimateResponsesRequestTokens(body); got != 3 {
		t.Fatalf("message estimate = %d, want 3 model-visible text tokens", got)
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
