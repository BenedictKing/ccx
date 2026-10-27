package channels

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// UpdateRequest 是 v2 PUT /api/channels/:uid 的入口结构，该端点是渠道编辑保存的主通道
// （前端 updateChannelByType 只要渠道有 channelUid 就走它）。它手动逐字段映射到
// config.UpstreamUpdate，入口或映射漏字段 = 前端保存被静默丢弃，且后端因
// hasConfigChanged 判定「无实质变化」跳过落盘，表现为「保存后再打开就丢了」
// （2026-09 计费四字段事故根因）。本测试保证 UpstreamUpdate 的面向前端字段
// 在 UpdateRequest 中全部有对应 JSON 入口；服务端内部字段显式豁免。
func TestUpdateRequestCoversUpstreamUpdateJSONFields(t *testing.T) {
	// UpstreamUpdate 中由服务端专管、不允许客户端经 v2 更新端点写入的字段
	internalFields := map[string]bool{
		"promotionUntil":           true, // 促销期走专门的 promotion 端点
		"learnedClientFingerprint": true, // 由发现流程服务端回写
		"autoManaged":              true, // 托管生命周期由服务端管理
		"autoManagedAt":            true,
		"autoManagedKind":          true,
	}

	jsonTags := func(v interface{}) map[string]bool {
		tags := make(map[string]bool)
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			tag := rt.Field(i).Tag.Get("json")
			if tag == "" || tag == "-" {
				continue
			}
			name := strings.Split(tag, ",")[0]
			if name != "" {
				tags[name] = true
			}
		}
		return tags
	}

	upstreamTags := jsonTags(config.UpstreamUpdate{})
	requestTags := jsonTags(UpdateRequest{})

	for tag := range upstreamTags {
		if internalFields[tag] {
			if requestTags[tag] {
				t.Errorf("内部字段 %q 不应出现在 UpdateRequest（客户端可伪造服务端专管字段）", tag)
			}
			continue
		}
		if !requestTags[tag] {
			t.Errorf("UpdateRequest 缺少字段 %q：v2 更新端点会静默丢弃该字段的保存请求", tag)
		}
	}

	for tag := range requestTags {
		if !upstreamTags[tag] && tag != "kind" {
			t.Errorf("UpdateRequest 字段 %q 在 UpstreamUpdate 中不存在，映射悬空", tag)
		}
	}
}

// 端到端回归：编辑保存带计费四字段与倍率字段，必须落盘生效而非「跳过保存」。
func TestUpdatePersistsBillingFields(t *testing.T) {
	cfg := config.Config{Upstream: []config.UpstreamConfig{{Name: "ch-edited", BaseURL: "https://example.com"}}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	tmpFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}
	cm, err := config.NewConfigManager(tmpFile, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	uid := cm.GetConfig().Upstream[0].ChannelUID
	if uid == "" {
		t.Fatal("渠道加载后应自动补齐 ChannelUID")
	}

	gin.SetMode(gin.TestMode)
	h := &Handler{cm: cm}
	r := gin.New()
	RegisterRoutesForTest(r, h)

	body := `{
		"channelPaymentCurrency": "CNY",
		"channelPaymentAmount": 20,
		"channelCreditCurrency": "USD",
		"channelCreditAmount": 1,
		"costMultiplier": 0.5,
		"maxGroupMultiplier": 2,
		"reasoningParamStyle": "reasoning_effort",
		"rateLimitWindowMinutes": 3
	}`
	req := httptest.NewRequest(http.MethodPut, "/channels/"+uid, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d: %s", w.Code, w.Body.String())
	}

	// 内存态：字段已应用
	up := cm.GetConfig().Upstream[0]
	if up.ChannelPaymentCurrency != "CNY" || up.ChannelCreditCurrency != "USD" {
		t.Fatalf("币种未应用: payment=%q credit=%q", up.ChannelPaymentCurrency, up.ChannelCreditCurrency)
	}
	if up.ChannelPaymentAmount == nil || *up.ChannelPaymentAmount != 20 {
		t.Fatalf("ChannelPaymentAmount 未应用: %v", up.ChannelPaymentAmount)
	}
	if up.ChannelCreditAmount == nil || *up.ChannelCreditAmount != 1 {
		t.Fatalf("ChannelCreditAmount 未应用: %v", up.ChannelCreditAmount)
	}
	if up.CostMultiplier == nil || *up.CostMultiplier != 0.5 {
		t.Fatalf("CostMultiplier 未应用: %v", up.CostMultiplier)
	}
	if up.MaxGroupMultiplier == nil || *up.MaxGroupMultiplier != 2 {
		t.Fatalf("MaxGroupMultiplier 未应用: %v", up.MaxGroupMultiplier)
	}
	if up.ReasoningParamStyle != "reasoning_effort" {
		t.Fatalf("ReasoningParamStyle 未应用: %q", up.ReasoningParamStyle)
	}
	if up.RateLimitWindowMinutes != 3 {
		t.Fatalf("RateLimitWindowMinutes 未应用: %d", up.RateLimitWindowMinutes)
	}

	// 落盘态：「跳过保存」路径下内存有值但不持久化，重启即丢，故必须验证文件
	persisted, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("读取落盘配置失败: %v", err)
	}
	for _, fragment := range []string{`"channelPaymentCurrency": "CNY"`, `"costMultiplier": 0.5`} {
		if !strings.Contains(string(persisted), fragment) {
			t.Errorf("落盘配置缺少 %s，更新被跳过保存", fragment)
		}
	}
}

func TestUpdatePersistsResponsesReasoningFold(t *testing.T) {
	cfg := config.Config{ResponsesUpstream: []config.UpstreamConfig{{
		BaseURL: "https://example.com", APIKeys: []string{"test-key"}, ServiceType: "responses", Status: "active", Priority: 1,
	}}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := config.NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	uid := cm.GetConfig().ResponsesUpstream[0].ChannelUID
	r := gin.New()
	RegisterRoutesForTest(r, &Handler{cm: cm})
	for _, enabled := range []bool{true, false} {
		body, _ := json.Marshal(map[string]bool{"responsesReasoningFold": enabled})
		req := httptest.NewRequest(http.MethodPut, "/channels/"+uid, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		if got := cm.GetConfig().ResponsesUpstream[0].ResponsesReasoningFold; got != enabled {
			t.Fatalf("folding = %v, want %v", got, enabled)
		}
	}
}

// RegisterRoutesForTest 供测试注入 handler 实例（scheduler 为 nil 的最小路由）。
func RegisterRoutesForTest(r *gin.Engine, h *Handler) {
	r.PUT("/channels/:uid", h.Update)
}
