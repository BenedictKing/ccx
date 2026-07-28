package autopilot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	cfg := config.DefaultAutopilotRoutingConfig()
	if cfg.EffectiveRoutingMode() != config.AutopilotModeAuto {
		t.Fatalf("默认应为 Autopilot 自动运行态，实际 %q", cfg.EffectiveRoutingMode())
	}
}

func TestPutRoutingConfig_InvalidMode(t *testing.T) {
	var req RoutingConfigUpdateRequest
	if err := json.Unmarshal([]byte(`{"mode":"shadow","costPreference":"wrong_value"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.CostPreference != "wrong_value" {
		t.Fatalf("costPreference = %q", req.CostPreference)
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
	if _, exists := parsed["mode"]; exists {
		t.Fatal("配置响应不应再暴露 mode")
	}
	if parsed["killSwitchActive"] != false || parsed["costPreference"] != "balanced" || parsed["l2ProbeEnabled"] != true {
		t.Fatalf("序列化结果异常: %+v", parsed)
	}
}

func TestRoutingConfigUpdateRequest_Binding(t *testing.T) {
	var req RoutingConfigUpdateRequest
	r := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{"mode":"off","costPreference":"quality_first"}`))
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if req.CostPreference != "quality_first" {
		t.Fatalf("期望 costPreference=quality_first, 实际=%s", req.CostPreference)
	}
}

func TestPutRoutingConfigRejectsAutoBeforeReadiness(t *testing.T) {
	t.Skip("Autopilot 唯一自动运行态不再存在 readiness 准入门槛")
}

func TestPutRoutingConfigManualAssistCancelsPendingAutoRecovery(t *testing.T) {
	t.Skip("Autopilot 唯一自动运行态不再支持 assist 降级或自动恢复")
}
