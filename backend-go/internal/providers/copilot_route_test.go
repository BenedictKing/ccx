package providers

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
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/tidwall/gjson"
)

func TestCopilotMappedResponsesEndpointAndProtectedHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[{"id":"actual","supported_endpoints":["/v1/messages","/chat/completions"]}]}`)
	}))
	defer server.Close()
	resolver := copilot.NewModelResolver(server.Client(), func(context.Context, string, string) (string, string, error) { return "runtime", server.URL, nil })
	ctx := copilot.WithModelResolver(context.Background(), resolver)
	c := newGinContext(http.MethodPost, "/v1/responses", []byte(`{"model":"alias","input":"hello"}`), ctx)
	c.Set("requestBodyBytes", []byte(`{"model":"alias","input":"hello"}`))
	original := &config.UpstreamConfig{ServiceType: "copilot", BaseURL: server.URL, ModelMapping: map[string]string{"alias": "actual"}, CustomHeaders: map[string]string{"Authorization": "Bearer wrong", "Copilot-Integration-Id": "wrong", "X-Custom": "kept"}}
	copy := original.Clone()
	route, token, err := PrepareCopilotRoute(c, copy, "oauth", "alias", "responses", false)
	if err != nil {
		t.Fatal(err)
	}
	req, _, err := (&ResponsesProvider{}).ConvertToProviderRequest(c, copy, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyCopilotRoute(req, route, original.CustomHeaders); err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(req.Body)
	if req.URL.Path != "/v1/messages" || gjson.GetBytes(body, "model").String() != "actual" || !gjson.GetBytes(body, "messages").Exists() || gjson.GetBytes(body, "input").Exists() {
		t.Fatalf("endpoint 和正文不一致: %s %s", req.URL, body)
	}
	if req.Header.Get("Authorization") != "Bearer runtime" || req.Header.Get("Copilot-Integration-Id") == "wrong" || req.Header.Get("x-api-key") != "" || req.Header.Get("X-Custom") != "kept" {
		t.Fatalf("认证头错误: %v", req.Header)
	}
	if original.ServiceType != "copilot" || original.BaseURL != server.URL {
		t.Fatalf("持久配置被修改: %+v", original)
	}
}

func TestCopilotOpaqueHistoryCannotCrossProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[{"id":"actual","supported_endpoints":["/chat/completions"]}]}`)
	}))
	defer server.Close()
	resolver := copilot.NewModelResolver(server.Client(), func(context.Context, string, string) (string, string, error) { return "runtime", server.URL, nil })
	body := []byte(`{"model":"actual","input":[{"type":"reasoning","encrypted_content":"opaque"}]}`)
	c := newGinContext(http.MethodPost, "/v1/responses", body, copilot.WithModelResolver(context.Background(), resolver))
	c.Set("requestBodyBytes", body)
	_, _, err := PrepareCopilotRoute(c, &config.UpstreamConfig{ServiceType: "copilot", BaseURL: server.URL}, "oauth", "actual", "responses", false)
	if err == nil || !strings.Contains(err.Error(), "加密历史") {
		t.Fatalf("丢失加密状态的转换未被阻止: %v", err)
	}
}

func TestCopilotCrossProtocolContinuationRequiresConvertibleSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[{"id":"actual","supported_endpoints":["/chat/completions"]}]}`)
	}))
	defer server.Close()
	resolver := copilot.NewModelResolver(server.Client(), func(context.Context, string, string) (string, string, error) { return "runtime", server.URL, nil })
	for _, mode := range []string{"missing", "encrypted", "plain"} {
		t.Run(mode, func(t *testing.T) {
			sm := session.NewSessionManager(time.Hour, 100, 100000)
			if mode != "missing" {
				saved, err := sm.GetOrCreateSession("")
				if err != nil {
					t.Fatal(err)
				}
				item := types.ResponsesItem{Type: "message", Role: "user", Content: "previous question"}
				if mode == "encrypted" {
					item = types.ResponsesItem{Type: "reasoning", EncryptedContent: "opaque"}
				}
				if err := sm.AppendMessage(saved.ID, item, 1); err != nil {
					t.Fatal(err)
				}
				sm.RecordResponseMapping("saved", saved.ID)
			}
			body := []byte(`{"model":"actual","previous_response_id":"saved","input":[{"type":"message","role":"user","content":"next question"}]}`)
			c := newGinContext(http.MethodPost, "/v1/responses", body, copilot.WithModelResolver(context.Background(), resolver))
			c.Set("requestBodyBytes", body)
			channel := &config.UpstreamConfig{ServiceType: "copilot", BaseURL: server.URL}
			_, token, err := PrepareCopilotRoute(c, channel, "oauth", "actual", "responses", false)
			if err != nil {
				t.Fatal(err)
			}
			req, _, err := (&ResponsesProvider{SessionManager: sm}).ConvertToProviderRequest(c, channel, token)
			if mode != "plain" {
				if err == nil {
					t.Fatal("不可恢复的续轮未失败")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			converted, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(converted), "previous question") || !strings.Contains(string(converted), "next question") {
				t.Fatalf("历史未恢复: %s", converted)
			}
			saved, err := sm.GetSessionByResponseID("saved")
			if err != nil || len(saved.Messages) != 1 {
				t.Fatalf("快照被修改: %+v err=%v", saved, err)
			}
		})
	}
}
