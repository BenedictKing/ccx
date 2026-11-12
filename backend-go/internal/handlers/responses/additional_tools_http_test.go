package responses

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestAdditionalToolsHTTPRoundTrip(t *testing.T) {
	for _, protocol := range []string{"responses", "openai", "claude", "gemini"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !stream {
						w.Header().Set("Content-Type", "application/json")
						bodies := map[string]string{
							"responses": `{"id":"resp_test","status":"completed","output":[{"id":"fc1","type":"function_call","call_id":"call1","name":"functions__exec","arguments":"{\"input\":\"print(1)\"}"}],"usage":{"input_tokens":10,"output_tokens":10}}`,
							"openai":    `{"id":"chat1","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call1","type":"function","function":{"name":"functions__exec","arguments":"{\"input\":\"print(1)\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":10}}`,
							"claude":    `{"id":"msg1","type":"message","role":"assistant","content":[{"type":"tool_use","id":"call1","name":"functions__exec","input":{"input":"print(1)"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":10}}`,
							"gemini":    `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"functions__exec","args":{"input":"print(1)"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":10}}`,
						}
						_, _ = w.Write([]byte(bodies[protocol]))
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					send := func(data string) { fmt.Fprintf(w, "data: %s\n\n", data) }
					switch protocol {
					case "responses":
						send(`{"type":"response.created","response":{"id":"resp1","status":"in_progress"}}`)
						send(`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc1","type":"function_call","call_id":"call1","name":"functions__exec","arguments":""}}`)
						send(`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"input\":\"print(1)\"}"}`)
						send(`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc1","type":"function_call","call_id":"call1","name":"functions__exec","arguments":"{\"input\":\"print(1)\"}"}}`)
						send(`{"type":"response.completed","response":{"id":"resp1","status":"completed","usage":{"input_tokens":10,"output_tokens":10}}}`)
					case "openai":
						send(`{"id":"chat1","choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"functions__exec","arguments":"{\"input\":\"print(1)\"}"}}]},"finish_reason":null}]}`)
						send(`{"id":"chat1","choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
						send(`[DONE]`)
					case "claude":
						send(`{"type":"message_start","message":{"id":"msg1","usage":{"input_tokens":10}}}`)
						send(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call1","name":"functions__exec","input":{}}}`)
						send(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"input\":\"print(1)\"}"}}`)
						send(`{"type":"content_block_stop","index":0}`)
						send(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":10}}`)
						send(`{"type":"message_stop"}`)
					case "gemini":
						send(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"functions__exec","args":{"input":"print(1)"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":10}}`)
					}
				}))
				defer server.Close()
				router := newResponsesTestRouter(t, config.UpstreamConfig{Name: "tools", BaseURL: server.URL, APIKeys: []string{"key"}, ServiceType: protocol, CodexNativeToolPassthrough: protocol == "responses"}, nil)
				body := fmt.Sprintf(`{"model":"test","store":false,"stream":%t,"input":[{"type":"message","role":"user","content":"hello"},{"type":"additional_tools","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]}]}]}`, stream)
				w := performResponsesHandlerRequest(t, router, body)
				if w.Code != http.StatusOK {
					t.Fatalf("status=%d,body=%s", w.Code, w.Body.String())
				}
				output := w.Body.String()
				if !strings.Contains(output, `"type":"custom_tool_call"`) || !strings.Contains(output, `"name":"exec"`) || !strings.Contains(output, "print(1)") {
					t.Fatalf("custom call not restored: %s", output)
				}
				if stream && !strings.Contains(output, "response.custom_tool_call_input.delta") {
					t.Fatalf("custom delta missing: %s", output)
				}
				if strings.Contains(output, `"name":"functions__exec"`) {
					t.Fatalf("flat upstream name leaked: %s", output)
				}
				if !strings.Contains(output, `"namespace":"functions"`) {
					t.Fatalf("custom namespace missing: %s", output)
				}
			})
		}
	}
}
