package logicalchannels

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

// GET /api/logical-channels/dashboard 的 protocolRoutes 必须按协议成员下发各自的
// modelMapping：同名源模型在兄弟协议成员上可指向不同目标，聚合视图（统一渠道卡）
// 依赖该摘要展示"映射按执行成员生效"，入口协议的映射不广播到兄弟成员。
func TestDashboardProtocolRoutesCarryPerMemberModelMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Config{
		Upstream:          []config.UpstreamConfig{},
		ChatUpstream:      []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{},
		GeminiUpstream:    []config.UpstreamConfig{},
		ImagesUpstream:    []config.UpstreamConfig{},
		VectorsUpstream:   []config.UpstreamConfig{},
	}
	data, err := json.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cm, err := config.NewConfigManager(cfgPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	logical, err := cm.CreateLogicalChannel(config.CreateLogicalChannelInput{
		Name:     "协议级映射渠道",
		Kind:     config.LogicalChannelKindLLM,
		BaseURLs: []string{"https://mapping.test.com/v1"},
		Protocols: []config.CreateLogicalChannelProtocol{
			{
				Kind:         "chat",
				ServiceType:  "openai",
				APIKeys:      []string{"k1"},
				ModelMapping: map[string]string{"gpt-5": "chat-target"},
			},
			{
				Kind:         "responses",
				ServiceType:  "responses",
				APIKeys:      []string{"k1"},
				ModelMapping: map[string]string{"gpt-5": "responses-target"},
			},
			{
				Kind:        "messages",
				ServiceType: "claude",
				APIKeys:     []string{"k1"},
				// messages 成员无映射：聚合视图不应伪造空摘要之外的任何内容
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateLogicalChannel: %v", err)
	}

	router := gin.New()
	h := &Handler{cm: cm}
	router.GET("/api/logical-channels/dashboard", h.Dashboard)

	req := httptest.NewRequest(http.MethodGet, "/api/logical-channels/dashboard?kind=llm", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard 失败: %d %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Channels []struct {
			LogicalChannelUID string `json:"logicalChannelUid"`
			ProtocolRoutes    []struct {
				Kind         string            `json:"kind"`
				ChannelUID   string            `json:"channelUid"`
				ModelMapping map[string]string `json:"modelMapping"`
			} `json:"protocolRoutes"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	var target *struct {
		LogicalChannelUID string `json:"logicalChannelUid"`
		ProtocolRoutes    []struct {
			Kind         string            `json:"kind"`
			ChannelUID   string            `json:"channelUid"`
			ModelMapping map[string]string `json:"modelMapping"`
		} `json:"protocolRoutes"`
	}
	for i := range resp.Channels {
		if resp.Channels[i].LogicalChannelUID == logical.LogicalChannelUID {
			target = &resp.Channels[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("dashboard 未返回逻辑渠道 %s", logical.LogicalChannelUID)
	}

	mappingByKind := make(map[string]map[string]string, len(target.ProtocolRoutes))
	for _, route := range target.ProtocolRoutes {
		mappingByKind[route.Kind] = route.ModelMapping
	}
	if got := mappingByKind["chat"]["gpt-5"]; got != "chat-target" {
		t.Fatalf("chat 成员映射 = %q, want chat-target", got)
	}
	if got := mappingByKind["responses"]["gpt-5"]; got != "responses-target" {
		t.Fatalf("responses 成员映射 = %q, want responses-target（同名源模型按成员隔离）", got)
	}
	if mm := mappingByKind["messages"]; len(mm) != 0 {
		t.Fatalf("messages 成员未配置映射, got %v", mm)
	}
}
