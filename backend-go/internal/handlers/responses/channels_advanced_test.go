package responses

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func setupResponsesConfigManager(t *testing.T, upstream []config.UpstreamConfig) *config.ConfigManager {
	t.Helper()
	cfg := config.Config{ResponsesUpstream: upstream}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	tmpFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}
	cm, err := config.NewConfigManager(tmpFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { cm.Close() })
	return cm
}

func TestBuildHealthCheckURLs_UseExistingVersionSuffix(t *testing.T) {
	if got := buildMessagesURL("https://api.example.com/codex/v1"); got != "https://api.example.com/codex/v1/messages" {
		t.Fatalf("buildMessagesURL() = %s", got)
	}
	if got := buildModelsURL("https://api.example.com/codex/v1"); got != "https://api.example.com/codex/v1/models" {
		t.Fatalf("buildModelsURL() = %s", got)
	}
	if got := buildGeminiModelsURL("https://api.example.com/codex/v1beta"); got != "https://api.example.com/codex/v1beta/models" {
		t.Fatalf("buildGeminiModelsURL() = %s", got)
	}
	if got := buildModelsURL("https://api.example.com/codex/v1#"); got != "https://api.example.com/codex/v1/models" {
		t.Fatalf("buildModelsURL() with marker = %s", got)
	}
}

func TestGetUpstreams_IncludesAdvancedOptionFields(t *testing.T) {
	cm := setupResponsesConfigManager(t, []config.UpstreamConfig{{
		Name:             "resp-ch",
		ServiceType:      "responses",
		BaseURL:          "https://api.example.com",
		APIKeys:          []string{"sk-1"},
		ModelMapping:     map[string]string{"gpt-5": "gpt-5.2"},
		ReasoningMapping: map[string]string{"gpt-5": "high"},
		TextVerbosity:    "high",
		FastMode:         true,
	}})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/responses/channels", GetUpstreams(cm))

	req := httptest.NewRequest(http.MethodGet, "/responses/channels", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Channels []map[string]interface{} `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	ch := resp.Channels[0]
	if ch["textVerbosity"] != "high" {
		t.Fatalf("textVerbosity = %v, want high", ch["textVerbosity"])
	}
	if ch["fastMode"] != true {
		t.Fatalf("fastMode = %v, want true", ch["fastMode"])
	}
	rm, ok := ch["reasoningMapping"].(map[string]interface{})
	if !ok || rm["gpt-5"] != "high" {
		t.Fatalf("reasoningMapping = %#v, want gpt-5=high", ch["reasoningMapping"])
	}
}

func TestMoveApiKeyRoutes_RejectInvalidID(t *testing.T) {
	cm := setupResponsesConfigManager(t, []config.UpstreamConfig{{
		Name:    "responses-ch",
		BaseURL: "https://api.example.com",
		APIKeys: []string{"sk-1", "sk-2"},
	}})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/responses/channels/:id/api-keys/:apiKey/move-top", MoveApiKeyToTop(cm))
	r.POST("/responses/channels/:id/api-keys/:apiKey/move-bottom", MoveApiKeyToBottom(cm))

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "move_top", path: "/responses/channels/not-a-number/api-keys/sk-2/move-top"},
		{name: "move_bottom", path: "/responses/channels/not-a-number/api-keys/sk-1/move-bottom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}
