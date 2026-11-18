package responses

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/copilot"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func TestCopilotLocalCompactUsesMappedEndpoint(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_stream%t", version, stream), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer runtime" || r.Header.Get("Copilot-Integration-Id") != "vscode-chat" {
						t.Errorf("compact 认证错误: %v", r.Header)
					}
					if r.URL.Path == "/models" {
						_, _ = fmt.Fprint(w, `{"data":[{"id":"actual","supported_endpoints":["/chat/completions"]}]}`)
						return
					}
					body, _ := io.ReadAll(r.Body)
					if r.URL.Path != "/chat/completions" || gjson.GetBytes(body, "model").String() != "actual" || !gjson.GetBytes(body, "messages").Exists() {
						t.Errorf("compact 协议/模型错误: %s %s", r.URL, body)
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"summary\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprint(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"summary"},"finish_reason":"stop"}]}`)
					}
				}))
				defer upstream.Close()
				resolver := copilot.NewModelResolver(upstream.Client(), func(context.Context, string, string) (string, string, error) { return "runtime", upstream.URL, nil })
				channel := &config.UpstreamConfig{ServiceType: "copilot", BaseURL: upstream.URL, APIKeys: []string{"oauth"}, ModelMapping: map[string]string{"alias": "actual"}}
				cm := setupResponsesTestConfigManager(t, []config.UpstreamConfig{*channel})
				body := fmt.Sprintf(`{"model":"alias","stream":%t,"input":[{"type":"message","role":"user","content":"please summarize"}]}`, stream)
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(body)).WithContext(copilot.WithModelResolver(context.Background(), resolver))
				sm := session.NewSessionManager(time.Hour, 100, 100000)
				compact := tryLocalCompactWithKey
				if version == "v2" {
					compact = tryLocalCompactV2WithKey
				}
				success, compactErr := compact(c, channel, "oauth", []byte(body), config.NewEnvConfig(), cm, sm)
				if !success || compactErr != nil || !strings.Contains(w.Body.String(), "summary") {
					t.Fatalf("compact 失败: success=%t err=%+v body=%s", success, compactErr, w.Body.String())
				}
			})
		}
	}
}
