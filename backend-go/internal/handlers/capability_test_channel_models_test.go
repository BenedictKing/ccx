package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestParseChannelModelsBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		channelKind string
		want        []string
	}{
		{
			name:        "openai data 形态",
			body:        `{"data":[{"id":"doubao-seed-2.1-turbo"},{"id":"kimi-k3"},{"id":""}]}`,
			channelKind: "chat",
			want:        []string{"doubao-seed-2.1-turbo", "kimi-k3"},
		},
		{
			name:        "gemini models 形态（剥 models/ 前缀）",
			body:        `{"models":[{"name":"models/gemini-2.5-pro"},{"name":"models/gemini-2.5-flash"}]}`,
			channelKind: "gemini",
			want:        []string{"gemini-2.5-pro", "gemini-2.5-flash"},
		},
		{
			name:        "空 data",
			body:        `{"data":[]}`,
			channelKind: "messages",
			want:        []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseChannelModelsBody([]byte(tt.body), tt.channelKind)
			if err != nil {
				t.Fatalf("parseChannelModelsBody error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("models = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("models[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}

	if _, err := parseChannelModelsBody([]byte("not-json"), "chat"); err == nil {
		t.Fatal("畸形 JSON 应返回错误")
	}
}

func TestExactSupportedModels(t *testing.T) {
	got := exactSupportedModels([]string{"claude-opus-4-8", "claude-*", "!claude-instant", "*-latest", " kimi-k3 ", ""})
	want := []string{"claude-opus-4-8", "kimi-k3"}
	if len(got) != len(want) {
		t.Fatalf("exactSupportedModels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("exact[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// newChannelModelsTestServer 构建模拟上游：/v1/models 返回给定模型清单
func newChannelModelsTestServer(t *testing.T, status int, models []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("非预期路径: %s", r.URL.Path)
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		var sb strings.Builder
		sb.WriteString(`{"data":[`)
		for i, m := range models {
			if i > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"id":%q}`, m)
		}
		sb.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sb.String()))
	}))
}

func TestResolveChannelProbeModels_FetchAndSupportedModelsFilter(t *testing.T) {
	srv := newChannelModelsTestServer(t, http.StatusOK, []string{"kimi-k3", "glm-5.3", "doubao-seed-2.1-turbo"})
	defer srv.Close()

	channel := &config.UpstreamConfig{
		Name:            "ch",
		BaseURL:         srv.URL,
		APIKeys:         []string{"sk-test"},
		ServiceType:     "openai",
		SupportedModels: []string{"kimi-*", "glm-*"},
	}
	got, err := resolveChannelProbeModels(context.Background(), channel, "chat", nil)
	if err != nil {
		t.Fatalf("resolveChannelProbeModels error: %v", err)
	}
	want := []string{"glm-5.3", "kimi-k3"}
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveChannelProbeModels_NoOverlapFallsBackToFullList(t *testing.T) {
	srv := newChannelModelsTestServer(t, http.StatusOK, []string{"kimi-k3", "glm-5.3"})
	defer srv.Close()

	// SupportedModels（对外口径 claude-*）与上游清单（真实模型）无交集时退回清单全量
	channel := &config.UpstreamConfig{
		Name:            "ch",
		BaseURL:         srv.URL,
		APIKeys:         []string{"sk-test"},
		ServiceType:     "openai",
		SupportedModels: []string{"claude-*"},
	}
	got, err := resolveChannelProbeModels(context.Background(), channel, "chat", nil)
	if err != nil {
		t.Fatalf("resolveChannelProbeModels error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("models = %v, want 2 items", got)
	}
}

func TestResolveChannelProbeModels_FetchFailFallsBackToExactSupported(t *testing.T) {
	srv := newChannelModelsTestServer(t, http.StatusInternalServerError, nil)
	defer srv.Close()

	channel := &config.UpstreamConfig{
		Name:            "ch",
		BaseURL:         srv.URL,
		APIKeys:         []string{"sk-test"},
		ServiceType:     "openai",
		SupportedModels: []string{"claude-opus-4-8", "claude-*"},
	}
	got, err := resolveChannelProbeModels(context.Background(), channel, "chat", nil)
	if err != nil {
		t.Fatalf("resolveChannelProbeModels error: %v", err)
	}
	if len(got) != 1 || got[0] != "claude-opus-4-8" {
		t.Fatalf("models = %v, want [claude-opus-4-8]", got)
	}
}

func TestResolveChannelProbeModels_FetchFailNoSupportedErrors(t *testing.T) {
	srv := newChannelModelsTestServer(t, http.StatusInternalServerError, nil)
	defer srv.Close()

	channel := &config.UpstreamConfig{
		Name:        "ch",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "openai",
	}
	if _, err := resolveChannelProbeModels(context.Background(), channel, "chat", nil); err == nil {
		t.Fatal("拉取失败且无 SupportedModels 精确项时应返回错误")
	}
}

func TestResolveChannelProbeModels_TruncateToMax(t *testing.T) {
	models := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		models = append(models, fmt.Sprintf("model-%02d", i))
	}
	srv := newChannelModelsTestServer(t, http.StatusOK, models)
	defer srv.Close()

	channel := &config.UpstreamConfig{
		Name:        "ch",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "openai",
	}
	got, err := resolveChannelProbeModels(context.Background(), channel, "chat", nil)
	if err != nil {
		t.Fatalf("resolveChannelProbeModels error: %v", err)
	}
	if len(got) != maxChannelProbeModels {
		t.Fatalf("models len = %d, want %d", len(got), maxChannelProbeModels)
	}
}
