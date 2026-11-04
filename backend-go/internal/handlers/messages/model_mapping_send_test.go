package messages

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// 协议级模型映射发送路径回归（docs/design/protocol-model-mapping-plan.md 阶段 3）：
// /v1/messages 入口按选中的执行协议成员应用该成员自己的 ModelMapping；
// 入口协议的映射不存在"混用"，未命中的模型按原名透传。
func TestMessagesHandler_AppliesExecutingMemberModelMapping(t *testing.T) {
	claudeResp := `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	chatResp := `{"id":"chatcmpl_1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

	tests := []struct {
		name         string
		serviceType  string // 执行成员的协议（claude=messages 原生 / openai=chat 转换，跨协议）
		modelMapping map[string]string
		requestModel string
		wantModel    string
		response     string
	}{
		{
			name:         "cross_protocol_openai_member_uses_own_mapping",
			serviceType:  "openai",
			modelMapping: map[string]string{"deepseek-v4-pro": "chat-exec-target"},
			requestModel: "deepseek-v4-pro",
			wantModel:    "chat-exec-target",
			response:     chatResp,
		},
		{
			name:         "native_claude_member_uses_own_mapping",
			serviceType:  "claude",
			modelMapping: map[string]string{"claude-sonnet-4-6": "claude-exec-target"},
			requestModel: "claude-sonnet-4-6",
			wantModel:    "claude-exec-target",
			response:     claudeResp,
		},
		{
			name:         "unmapped_model_passes_through",
			serviceType:  "openai",
			modelMapping: map[string]string{"other-model": "other-target"},
			requestModel: "deepseek-v4-pro",
			wantModel:    "deepseek-v4-pro",
			response:     chatResp,
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
				_, _ = w.Write([]byte(tt.response))
			}))
			defer upstream.Close()

			router := newMessagesTestRouter(t, config.UpstreamConfig{
				Name:         tt.name,
				BaseURL:      upstream.URL,
				APIKeys:      []string{"sk-test"},
				ServiceType:  tt.serviceType,
				Status:       "active",
				ModelMapping: tt.modelMapping,
			})

			reqBody := `{"model":"` + tt.requestModel + `","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
			w := performMessagesHandlerRequest(t, router, reqBody)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
			}
			if len(captured) == 0 {
				t.Fatal("upstream request body is empty")
			}
			var upstreamReq map[string]interface{}
			if err := json.Unmarshal(captured, &upstreamReq); err != nil {
				t.Fatalf("unmarshal upstream request: %v, body=%s", err, string(captured))
			}
			if got := upstreamReq["model"]; got != tt.wantModel {
				t.Fatalf("上游收到的 model = %v, want %q（应取执行成员 %s 的映射）", got, tt.wantModel, tt.serviceType)
			}
		})
	}
}
