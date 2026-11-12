package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/types"
)

func TestResponsesAdditionalToolsAcrossProtocols(t *testing.T) {
	const body = `{"model":"test","input":[{"type":"message","role":"user","content":"hello"},{"type":"additional_tools","role":"developer","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","description":"run code"},{"type":"function","name":"read","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}]}]}]}`
	for _, protocol := range []string{"responses", "openai", "claude", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			c := newGinContext(http.MethodPost, "/v1/responses", []byte(body), context.Background())
			upstream := &config.UpstreamConfig{BaseURL: "https://example.com", ServiceType: protocol, CodexNativeToolPassthrough: protocol == "responses"}
			req, _, err := (&ResponsesProvider{}).ConvertToProviderRequest(c, upstream, "test-key")
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(payload)
			for _, name := range []string{"functions__exec", "functions__read"} {
				if !strings.Contains(string(encoded), name) {
					t.Errorf("%s missing in %s", name, encoded)
				}
			}
			if strings.Contains(string(encoded), `"type":"additional_tools"`) {
				t.Fatalf("unsupported input item still present: %s", encoded)
			}
			value, _ := c.Get("codex_tool_context")
			ctx, ok := value.(converters.CodexToolContext)
			if !ok || !ctx.IsCustomToolProxy("functions__exec") {
				t.Fatalf("original custom context lost: %#v", value)
			}
			response := &types.ResponsesResponse{Output: []types.ResponsesItem{{Type: "function_call", Name: "functions__exec", CallID: "call_1", Arguments: `{"input":"print(1)"}`}, {Type: "function_call", Name: "functions__read", CallID: "call_2", Arguments: `{"path":"x"}`}}}
			ctx.RemapCustomToolCallsInResponse(response)
			ctx.RemapNamespaceFunctionCallsInResponse(response)
			if response.Output[0].Type != "custom_tool_call" || response.Output[0].Name != "exec" || response.Output[0].Input != "print(1)" {
				t.Fatalf("custom remap=%+v", response.Output[0])
			}
			if response.Output[1].Name != "read" || response.Output[1].Namespace != "functions" {
				t.Fatalf("namespace remap=%+v", response.Output[1])
			}
		})
	}
}

func TestAdditionalToolsHistoryReplay(t *testing.T) {
	const body = `{"model":"test","input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"},{"type":"function","name":"read","parameters":{"type":"object"}}]}]},{"type":"custom_tool_call","call_id":"call_1","name":"exec","input":"print(1)"},{"type":"custom_tool_call_output","call_id":"call_1","output":"1"},{"type":"function_call","call_id":"call_2","name":"read","namespace":"functions","arguments":"{}"},{"type":"function_call_output","call_id":"call_2","output":"file"}]}`
	for _, protocol := range []string{"responses", "openai", "claude", "gemini"} {
		c := newGinContext(http.MethodPost, "/v1/responses", []byte(body), context.Background())
		req, _, err := (&ResponsesProvider{}).ConvertToProviderRequest(c, &config.UpstreamConfig{BaseURL: "https://example.com", ServiceType: protocol, CodexNativeToolPassthrough: protocol == "responses"}, "key")
		if err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(payload)
		if !strings.Contains(string(encoded), "print(1)") || !strings.Contains(string(encoded), "functions__read") {
			t.Errorf("%s replay lost history: %s", protocol, encoded)
		}
	}
}

func TestEncryptedFunctionArgumentsPassthrough(t *testing.T) {
	const body = `{"model":"test","input":[{"type":"function_call","name":"notes","call_id":"c1","encrypted_function_args":["one","two"]}]}`
	c := newGinContext(http.MethodPost, "/v1/responses", []byte(body), context.Background())
	req, _, err := (&ResponsesProvider{}).ConvertToProviderRequest(c, &config.UpstreamConfig{BaseURL: "https://example.com", ServiceType: "responses"}, "key")
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]interface{}
	_ = json.NewDecoder(req.Body).Decode(&request)
	encoded, _ := json.Marshal(request)
	if !strings.Contains(string(encoded), `"encrypted_function_args":["one","two"]`) {
		t.Fatalf("request lost encrypted args: %s", encoded)
	}
	converter := &converters.ResponsesPassthroughConverter{}
	response, err := converter.FromProviderResponse(map[string]interface{}{"output": []interface{}{map[string]interface{}{"type": "function_call", "name": "notes", "encrypted_function_args": []interface{}{"one", "two"}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output[0].EncryptedFunctionArgs) != 2 {
		t.Fatal("response lost encrypted args")
	}
}
