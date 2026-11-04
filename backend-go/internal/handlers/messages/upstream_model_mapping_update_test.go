package messages

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

// 旧六类管理 API 的 modelMapping 差量语义回归（docs/design/protocol-model-mapping-plan.md 阶段 3）：
// PUT /api/messages/channels/:id 绑定 config.UpstreamUpdate——请求体未带 modelMapping
// 时保留现有映射，空对象 {} 显式清空，带映射时整体替换。逻辑渠道 API 已有同等语义
// 的 config 层用例，此处补 handler 线上契约，保证两套入口行为一致。
func TestUpdateUpstream_ModelMappingDifferentialSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newRouter := func(t *testing.T, cm *config.ConfigManager) *gin.Engine {
		sch := scheduler.NewChannelScheduler(
			cm,
			metrics.NewMetricsManager(),
			metrics.NewMetricsManager(),
			metrics.NewMetricsManager(),
			metrics.NewMetricsManager(),
			metrics.NewMetricsManager(),
			session.NewTraceAffinityManager(),
			nil,
		)
		r := gin.New()
		r.PUT("/messages/channels/:id", UpdateUpstream(cm, sch))
		return r
	}

	put := func(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/messages/channels/0", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	steps := []struct {
		name string
		body string
		want map[string]string
	}{
		{name: "absent modelMapping preserves", body: `{"priority":5}`, want: map[string]string{"gpt-5": "initial-target"}},
		{name: "empty object clears", body: `{"modelMapping":{}}`, want: map[string]string{}},
		{name: "new mapping replaces", body: `{"modelMapping":{"claude-sonnet-4-6":"replaced-target"}}`, want: map[string]string{"claude-sonnet-4-6": "replaced-target"}},
	}

	// 自建配置文件以便后续做落盘重载断言（共享 helper 不暴露路径）
	cfg := config.Config{Upstream: []config.UpstreamConfig{{
		Name:         "legacy-mapping",
		BaseURL:      "https://legacy.test.com/v1",
		APIKeys:      []string{"sk-test"},
		ServiceType:  "claude",
		Status:       "active",
		ModelMapping: map[string]string{"gpt-5": "initial-target"},
	}}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("serialize config: %v", err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	cm, err := config.NewConfigManager(cfgPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() err = %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	router := newRouter(t, cm)

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			w := put(t, router, step.body)
			if w.Code != http.StatusOK {
				t.Fatalf("PUT status = %d, body=%s", w.Code, w.Body.String())
			}
			cfg := cm.GetConfig()
			if len(cfg.Upstream) != 1 {
				t.Fatalf("upstream count = %d, want 1", len(cfg.Upstream))
			}
			got := cfg.Upstream[0].ModelMapping
			if len(got) != len(step.want) {
				t.Fatalf("modelMapping = %v, want %v", got, step.want)
			}
			for k, v := range step.want {
				if got[k] != v {
					t.Fatalf("modelMapping[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}

	// 保存重载后仍按该成员落盘
	reloaded, err := config.NewConfigManager(cfgPath, "")
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	if got := reloaded.GetConfig().Upstream[0].ModelMapping["claude-sonnet-4-6"]; got != "replaced-target" {
		t.Fatalf("重载后映射 = %v, want replaced-target", got)
	}
}
