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

func TestDisableAndRestoreGroupModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configFile := filepath.Join(t.TempDir(), "config.json")
	initial := config.Config{Upstream: []config.UpstreamConfig{{
		Name:    "messages",
		BaseURL: "https://example.com",
		APIKeys: []string{"sk-a", "sk-b"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-a", QuotaGroup: "vip"},
			{Key: "sk-b", QuotaGroup: "vip"},
		},
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
	r.POST("/messages/channels/:id/keys/group-model/disable", DisableGroupModel(cfgManager, "Messages"))
	r.POST("/messages/channels/:id/keys/group-model/restore", RestoreGroupModel(cfgManager, "Messages"))

	for _, step := range []struct {
		path        string
		body        string
		wantRecords int
	}{
		{path: "/messages/channels/0/keys/group-model/disable", body: `{"apiKey":"sk-a","model":"model-a"}`, wantRecords: 1},
		{path: "/messages/channels/0/keys/group-model/restore", body: `{"quotaGroup":"vip","model":"model-a"}`, wantRecords: 0},
	} {
		req := httptest.NewRequest(http.MethodPost, step.path, strings.NewReader(step.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST %s status = %d, body = %s", step.path, w.Code, w.Body.String())
		}
		if got := len(cfgManager.GetConfig().Upstream[0].DisabledGroupModels); got != step.wantRecords {
			t.Fatalf("POST %s 后限制记录数 = %d, want %d", step.path, got, step.wantRecords)
		}
	}
}
