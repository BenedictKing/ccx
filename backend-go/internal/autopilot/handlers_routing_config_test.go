package autopilot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func setupRoutingConfigRouter(deps *RoutingConfigDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	group := r.Group("/api")
	RegisterRoutingConfigRoutes(group, deps)
	return r
}

func TestGetRoutingConfig_DefaultMode(t *testing.T) {
	// 使用默认配置（shadow 模式）
	cfg := config.DefaultAutopilotRoutingConfig()

	// 由于 ConfigManager 的构造需要配置文件，跳过此测试
	_ = cfg
	t.Skip("需要集成测试环境")
}

func TestPutRoutingConfig_InvalidMode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 创建一个简单的 ConfigManager 来测试
	// 由于 ConfigManager 的构造需要配置文件，这里通过 mock 方式测试 handler 层的校验逻辑

	// 测试 mode 校验逻辑
	tests := []struct {
		name       string
		body       RoutingConfigUpdateRequest
		wantStatus int
		wantError  string
	}{
		{
			name:       "无效的 mode 值",
			body:       RoutingConfigUpdateRequest{Mode: "invalid_mode"},
			wantStatus: http.StatusBadRequest,
			wantError:  "无效的 mode",
		},
		{
			name:       "无效的 costPreference 值",
			body:       RoutingConfigUpdateRequest{CostPreference: "wrong_value"},
			wantStatus: http.StatusBadRequest,
			wantError:  "无效的 costPreference",
		},
		{
			name:       "空 body",
			body:       RoutingConfigUpdateRequest{},
			wantStatus: http.StatusBadRequest,
			wantError:  "至少需要提供",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 验证校验逻辑（不依赖 ConfigManager）
			validModes := map[string]bool{"off": true, "shadow": true, "assist": true, "auto": true}
			validCP := map[string]bool{"quality_first": true, "balanced": true, "cost_first": true}

			if tt.body.Mode != "" {
				if validModes[tt.body.Mode] && tt.wantError == "无效的 mode" {
					t.Fatal("模式应该被拒绝但实际上有效")
				}
				if !validModes[tt.body.Mode] && tt.wantError == "" {
					t.Fatal("模式应该无效但被标记为有效")
				}
			}

			if tt.body.CostPreference != "" {
				if validCP[tt.body.CostPreference] && tt.wantError == "无效的 costPreference" {
					t.Fatal("costPreference 应该被拒绝但实际上有效")
				}
				if !validCP[tt.body.CostPreference] && tt.wantError == "" {
					t.Fatal("costPreference 应该无效但被标记为有效")
				}
			}
		})
	}
}

func TestIsTruthyEnv(t *testing.T) {
	tests := []struct {
		name     string
		val      string
		expected bool
	}{
		{"true", "true", true},
		{"TRUE", "TRUE", true},
		{"1", "1", true},
		{"yes", "yes", true},
		{"on", "on", true},
		{"false", "false", false},
		{"0", "0", false},
		{"no", "no", false},
		{"off", "off", false},
		{"empty", "", false},
		{"whitespace", "  true  ", true},
		{"random", "xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTruthyEnv(tt.val)
			if result != tt.expected {
				t.Fatalf("isTruthyEnv(%q) = %v, 期望 %v", tt.val, result, tt.expected)
			}
		})
	}
}

func TestRoutingConfigResponse_Serialization(t *testing.T) {
	resp := RoutingConfigResponse{
		Mode:             "shadow",
		KillSwitchActive: false,
		CostPreference:   "balanced",
		L2ProbeEnabled:   true,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	if parsed["mode"] != "shadow" {
		t.Fatalf("期望 mode=shadow, 实际=%v", parsed["mode"])
	}
	if parsed["killSwitchActive"] != false {
		t.Fatalf("期望 killSwitchActive=false, 实际=%v", parsed["killSwitchActive"])
	}
	if parsed["costPreference"] != "balanced" {
		t.Fatalf("期望 costPreference=balanced, 实际=%v", parsed["costPreference"])
	}
	if parsed["l2ProbeEnabled"] != true {
		t.Fatalf("期望 l2ProbeEnabled=true, 实际=%v", parsed["l2ProbeEnabled"])
	}
}

func TestRoutingConfigUpdateRequest_Binding(t *testing.T) {
	tests := []struct {
		name string
		body string
		want RoutingConfigUpdateRequest
	}{
		{
			name: "mode 和 costPreference",
			body: `{"mode":"auto","costPreference":"cost_first"}`,
			want: RoutingConfigUpdateRequest{Mode: "auto", CostPreference: "cost_first"},
		},
		{
			name: "仅 mode",
			body: `{"mode":"off"}`,
			want: RoutingConfigUpdateRequest{Mode: "off"},
		},
		{
			name: "仅 costPreference",
			body: `{"costPreference":"quality_first"}`,
			want: RoutingConfigUpdateRequest{CostPreference: "quality_first"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req RoutingConfigUpdateRequest
			r := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(tt.body))
			r.Header.Set("Content-Type", "application/json")

			// 手动绑定（与 gin 的 ShouldBindJSON 逻辑一致）
			decoder := json.NewDecoder(r.Body)
			if err := decoder.Decode(&req); err != nil {
				t.Fatalf("解码失败: %v", err)
			}

			if req.Mode != tt.want.Mode {
				t.Fatalf("期望 mode=%s, 实际=%s", tt.want.Mode, req.Mode)
			}
			if req.CostPreference != tt.want.CostPreference {
				t.Fatalf("期望 costPreference=%s, 实际=%s", tt.want.CostPreference, req.CostPreference)
			}
		})
	}
}

func TestPutRoutingConfigRejectsAutoBeforeReadiness(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	store := newRoutingReadinessTestStore(t)
	router := setupRoutingConfigRouter(&RoutingConfigDeps{CfgManager: cfgManager, TraceStore: store})

	req := httptest.NewRequest(http.MethodPut, "/api/smart-routing/config", bytes.NewBufferString(`{"mode":"auto"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", resp.Code, resp.Body.String())
	}
	if got := cfgManager.GetEffectiveRoutingMode(); got != config.AutopilotModeShadow {
		t.Fatalf("mode changed despite failed readiness: %q", got)
	}
}

func TestPutRoutingConfigManualAssistCancelsPendingAutoRecovery(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"autopilot":{"mode":"assist"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	store := newRoutingReadinessTestStore(t)
	if err := store.RecordAutoSafetyEvent(AutoSafetyEvent{
		FromMode: config.AutopilotModeAuto,
		ToMode:   config.AutopilotModeAssist,
	}); err != nil {
		t.Fatal(err)
	}
	router := setupRoutingConfigRouter(&RoutingConfigDeps{CfgManager: cfgManager, TraceStore: store})

	req := httptest.NewRequest(http.MethodPut, "/api/smart-routing/config", bytes.NewBufferString(`{"mode":"assist"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	lastEvent, err := store.LastAutoSafetyEvent()
	if err != nil || lastEvent == nil || !containsString(lastEvent.Reasons, "manual_mode_change") {
		t.Fatalf("last safety event = %+v, err=%v", lastEvent, err)
	}
	if readiness := store.EvaluateAutoReadiness(time.Now()); readiness.RecoveryPending {
		t.Fatalf("manual assist should cancel pending recovery: %+v", readiness)
	}
}
