package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestSuspendAndResumeAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configFile := filepath.Join(t.TempDir(), "config.json")
	initial := config.Config{Upstream: []config.UpstreamConfig{{
		Name:    "messages",
		BaseURL: "https://example.com",
		APIKeys: []string{"sk-test"},
	}}}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatalf("序列化初始配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cfgManager.Close()

	r := gin.New()
	r.POST("/messages/channels/:id/keys/suspend", SuspendAPIKey(cfgManager, "Messages"))
	r.POST("/messages/channels/:id/keys/resume", ResumeAPIKey(cfgManager, "Messages"))

	for _, step := range []struct {
		path        string
		wantEnabled *bool
	}{
		{path: "/messages/channels/0/keys/suspend", wantEnabled: boolPtr(false)},
		{path: "/messages/channels/0/keys/resume", wantEnabled: nil},
	} {
		req := httptest.NewRequest(http.MethodPost, step.path, strings.NewReader(`{"apiKey":"sk-test"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST %s status = %d, body = %s", step.path, w.Code, w.Body.String())
		}

		configs := cfgManager.GetConfig().Upstream[0].APIKeyConfigs
		if len(configs) != 1 {
			t.Fatalf("POST %s 后 apiKeyConfigs = %+v", step.path, configs)
		}
		if step.wantEnabled == nil {
			if configs[0].Enabled != nil {
				t.Fatalf("POST %s 后 Enabled = %v, want nil", step.path, *configs[0].Enabled)
			}
		} else if configs[0].Enabled == nil || *configs[0].Enabled != *step.wantEnabled {
			t.Fatalf("POST %s 后 Enabled = %v, want false", step.path, configs[0].Enabled)
		}
	}
}

func boolPtr(value bool) *bool {
	return &value
}
