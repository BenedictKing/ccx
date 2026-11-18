package copilot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModelEndpointRouting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer runtime" || r.Header.Get("Copilot-Integration-Id") == "" {
			t.Errorf("错误的模型发现请求: %s %v", r.URL.Path, r.Header)
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"native","supported_endpoints":["/responses","ws:/responses"]},{"id":"mixed","supported_endpoints":["/v1/messages","/chat/completions"]},{"id":"chat","supported_endpoints":["/chat/completions"]},{"id":"legacy"},{"id":"unknown","supported_endpoints":["/unsupported"]}]}`)
	}))
	defer server.Close()
	resolver := NewModelResolver(server.Client(), func(context.Context, string, string) (string, string, error) { return "runtime", server.URL, nil })
	for _, tc := range []struct {
		model, preferred, path, service, source string
		ws, wantError                           bool
	}{
		{"native", "responses", "/responses", "responses", "models-endpoint", false, false},
		{"native", "chat", "/responses", "responses", "models-endpoint", false, false},
		{"mixed", "responses", "/v1/messages", "claude", "models-endpoint", false, false},
		{"mixed", "chat", "/chat/completions", "openai", "models-endpoint", false, false},
		{"chat", "gemini", "/chat/completions", "openai", "models-endpoint", false, false},
		{"legacy", "chat", "/responses", "responses", "legacy-default", false, false},
		{"native", "responses", "/responses", "responses", "models-endpoint", true, false},
		{"mixed", "responses", "", "", "", true, true},
		{"unknown", "responses", "", "", "", false, true},
		{"missing", "responses", "", "", "", false, true},
	} {
		t.Run(fmt.Sprintf("%s_%s_ws%t", tc.model, tc.preferred, tc.ws), func(t *testing.T) {
			route, err := resolver.Resolve(context.Background(), "oauth", RouteOptions{Model: tc.model, PreferredProtocol: tc.preferred, WebSocket: tc.ws})
			if (err != nil) != tc.wantError {
				t.Fatalf("route=%+v err=%v", route, err)
			}
			if tc.wantError {
				return
			}
			if route.Path != tc.path || route.ServiceType != tc.service || route.Source != tc.source || route.BaseURL != server.URL || route.RuntimeToken != "runtime" {
				t.Fatalf("路由错误: %+v", route)
			}
		})
	}
}

func TestModelsCacheIsolationExpiryAndConcurrentDiscovery(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		endpoint := "/responses"
		if r.Header.Get("Authorization") == "Bearer other" {
			endpoint = "/chat/completions"
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"id":"model","supported_endpoints":[%q]}]}`, endpoint)
	}))
	defer server.Close()
	resolver := NewModelResolver(server.Client(), nil)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			models, err := resolver.Models(context.Background(), "runtime", server.URL, "", false)
			if err != nil || len(models) != 1 {
				t.Errorf("models=%v err=%v", models, err)
				return
			}
			models[0].SupportedEndpoints[0] = "/mutated"
		}()
	}
	wg.Wait()
	models, err := resolver.Models(context.Background(), "runtime", server.URL, "", false)
	if err != nil || models[0].SupportedEndpoints[0] != "/responses" || hits.Load() != 1 {
		t.Fatalf("缓存副本或并发合并错误: models=%v hits=%d err=%v", models, hits.Load(), err)
	}
	other, err := resolver.Models(context.Background(), "other", server.URL, "", false)
	if err != nil || other[0].SupportedEndpoints[0] != "/chat/completions" || hits.Load() != 2 {
		t.Fatalf("账号能力缓存未隔离: %v %v", other, err)
	}
	now := time.Now().Add(6 * time.Minute)
	resolver.now = func() time.Time { return now }
	if _, err := resolver.Models(context.Background(), "runtime", server.URL, "", false); err != nil || hits.Load() != 3 {
		t.Fatalf("过期能力未刷新: hits=%d err=%v", hits.Load(), err)
	}
}

func TestModelDiscoveryFailureAndExplicitBaseURL(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusNotFound} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		resolver := NewModelResolver(server.Client(), func(context.Context, string, string) (string, string, error) {
			return "runtime", "https://unused.invalid", nil
		})
		route, err := resolver.Resolve(context.Background(), "oauth", RouteOptions{BaseURL: server.URL + "#", Model: "model", PreferredProtocol: "chat"})
		if status != http.StatusNotFound {
			var modelsErr *ModelsError
			if !errors.As(err, &modelsErr) || modelsErr.Status != status {
				t.Fatalf("认证失败不能回退: route=%v err=%v", route, err)
			}
		} else if err != nil || route.Source != "legacy-default" || route.BaseURL != server.URL {
			t.Fatalf("旧接口默认值或显式 BaseURL 错误: route=%v err=%v", route, err)
		}
		server.Close()
	}
}
