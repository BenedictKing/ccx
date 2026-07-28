package chat

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestChatHandler_DeepSeekChatAndMessagesThinkingMatrix(t *testing.T) {
	tests := []struct {
		name           string
		serviceType    string
		responseBody   string
		wantUpstream   func(t *testing.T, body []byte)
		wantDownstream func(t *testing.T, body []byte)
	}{
		{
			name:         "chat_to_deepseek_chat",
			serviceType:  "openai",
			responseBody: `{"id":"chatcmpl_ds","object":"chat.completion","choices":[{"message":{"role":"assistant","reasoning_content":"chat reasoning","content":"chat text"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
			wantUpstream: func(t *testing.T, body []byte) {
				var req map[string]interface{}
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("unmarshal upstream request: %v", err)
				}
				messages, ok := req["messages"].([]interface{})
				if !ok || len(messages) < 2 {
					t.Fatalf("messages shape invalid: %s", string(body))
				}
				assistant, ok := messages[1].(map[string]interface{})
				if !ok {
					t.Fatalf("assistant message shape invalid: %s", string(body))
				}
				if got := assistant["reasoning_content"]; got != "previous reasoning" {
					t.Fatalf("reasoning_content = %v, want previous reasoning; body=%s", got, string(body))
				}
			},
			wantDownstream: func(t *testing.T, body []byte) {
				if !bytes.Contains(body, []byte(`"reasoning_content":"chat reasoning"`)) {
					t.Fatalf("expected OpenAI reasoning_content passthrough, got %s", string(body))
				}
			},
		},
		{
			name:         "chat_to_deepseek_messages",
			serviceType:  "claude",
			responseBody: `{"id":"msg_ds","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"messages thinking","signature":"sig_ds"},{"type":"text","text":"messages text"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`,
			wantUpstream: func(t *testing.T, body []byte) {
				if !bytes.Contains(body, []byte(`"type":"thinking"`)) || !bytes.Contains(body, []byte(`"thinking":"previous reasoning"`)) {
					t.Fatalf("expected converted Claude thinking block, got %s", string(body))
				}
			},
			wantDownstream: func(t *testing.T, body []byte) {
				if !bytes.Contains(body, []byte(`"reasoning_content":"messages thinking"`)) {
					t.Fatalf("expected Claude thinking converted to OpenAI reasoning_content, got %s", string(body))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read upstream request: %v", err)
				}
				captured = body
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer upstream.Close()

			channel := config.UpstreamConfig{
				Name:        tt.name,
				BaseURL:     upstream.URL,
				APIKeys:     []string{"sk-test"},
				ServiceType: tt.serviceType,
				Status:      "active",
			}
			// 该兼容开关没有手工字段：走配置落盘的链路只能用种子表达"生效值为 true"。
			channel.SetCompatSeed(config.TraitPassbackReasoningContent, true)
			router := newChatTestRouter(t, channel)

			reqBody := `{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hello"},{"role":"assistant","reasoning_content":"previous reasoning","content":"previous text"}]}`
			w := performChatHandlerRequest(t, router, reqBody)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
			}
			tt.wantUpstream(t, captured)
			tt.wantDownstream(t, w.Body.Bytes())
		})
	}
}
