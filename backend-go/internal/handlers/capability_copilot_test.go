package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/copilot"
	"github.com/tidwall/gjson"
)

func TestCopilotDiscoveryAndCapabilityUseModelEndpoint(t *testing.T) {
	for _, tc := range []struct{ model, endpoint, stream string }{
		{"native", "/responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"output\":[]}}\n\n"},
		{"chat", "/chat/completions", "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer runtime" {
					t.Errorf("探测使用了错误的认证: %v", r.Header)
				}
				if r.URL.Path == "/models" {
					_, _ = fmt.Fprintf(w, `{"data":[{"id":%q,"supported_endpoints":[%q]}]}`, tc.model, tc.endpoint)
					return
				}
				body, _ := io.ReadAll(r.Body)
				if r.URL.Path != tc.endpoint || gjson.GetBytes(body, "model").String() != tc.model {
					t.Errorf("探测与运行时路径不一致: %s %s", r.URL, body)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.stream)
			}))
			defer server.Close()
			resolver := copilot.NewModelResolver(server.Client(), func(context.Context, string, string) (string, string, error) { return "runtime", server.URL, nil })
			ctx := copilot.WithModelResolver(context.Background(), resolver)
			channel := &config.UpstreamConfig{ServiceType: "copilot", BaseURL: server.URL, APIKeys: []string{"oauth"}, ModelMapping: map[string]string{"alias": tc.model}}
			discovery := discoverTransientModelsWithFetchers(ctx, channel, "responses", "oauth", nil)
			if len(discovery.Details) != 1 || discovery.Details[0].SupportedEndpoints[0] != tc.endpoint {
				t.Fatalf("发现结果丢失 endpoint: %+v", discovery)
			}
			for _, protocol := range []string{"chat", "responses->responses", "responses->chat"} {
				req, err := buildTestRequestWithContext(ctx, protocol, channel, "alias", "")
				if err != nil {
					t.Fatal(err)
				}
				success, streaming, _, _, err := sendAndCheckStream(ctx, channel, req, protocol)
				if err != nil || !success || !streaming {
					t.Fatalf("%s 探测误判: success=%t streaming=%t err=%v", protocol, success, streaming, err)
				}
			}
		})
	}
}

func TestCopilotCapabilityCancellationIncludesDiscovery(t *testing.T) {
	resolver := copilot.NewModelResolver(nil, func(ctx context.Context, _, _ string) (string, string, error) { return "", "", ctx.Err() })
	ctx, cancel := context.WithCancel(copilot.WithModelResolver(context.Background(), resolver))
	cancel()
	_, err := buildTestRequestWithContext(ctx, "responses", &config.UpstreamConfig{ServiceType: "copilot", BaseURL: "https://unused.invalid", APIKeys: []string{"oauth"}}, "model", "")
	if err == nil {
		t.Fatal("取消的探测仍尝试发现")
	}
}
