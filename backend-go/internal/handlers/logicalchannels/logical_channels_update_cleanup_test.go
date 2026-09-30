package logicalchannels

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/quota"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

// PUT /api/logical-channels/:uid 带 removals 移除协议时，必须与 DELETE 一样清理被移除
// 物理渠道的指标/日志/亲和/配额。removals 是协议 kind（"messages"）而不是 ChannelUID，
// 且必须在 Update 之前解析——更新后上游已不在配置里。
func TestUpdateLogicalChannelRemovalsTriggerCleanup(t *testing.T) {
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
		Name:     "带清理验证的渠道",
		Kind:     config.LogicalChannelKindLLM,
		BaseURLs: []string{"https://api.test.com/v1"},
		Protocols: []config.CreateLogicalChannelProtocol{
			{Kind: "messages", ServiceType: "claude", APIKeys: []string{"k1"}},
			{Kind: "chat", ServiceType: "openai", APIKeys: []string{"k1"}},
		},
	})
	if err != nil {
		t.Fatalf("CreateLogicalChannel: %v", err)
	}

	cfgNow := cm.GetConfig()
	var removedUID, keptUID string
	for i := range cfgNow.Upstream {
		if cfgNow.Upstream[i].LogicalChannelUID == logical.LogicalChannelUID {
			removedUID = cfgNow.Upstream[i].ChannelUID
		}
	}
	for i := range cfgNow.ChatUpstream {
		if cfgNow.ChatUpstream[i].LogicalChannelUID == logical.LogicalChannelUID {
			keptUID = cfgNow.ChatUpstream[i].ChannelUID
		}
	}
	if removedUID == "" || keptUID == "" {
		t.Fatalf("物理渠道未创建: removed=%q kept=%q", removedUID, keptUID)
	}

	qm := quota.NewManager()
	sch := scheduler.NewChannelScheduler(cm, nil, nil, nil, nil, nil, session.NewTraceAffinityManager(), nil)
	sch.SetQuotaManager(qm)

	headers := http.Header{
		"anthropic-ratelimit-input-tokens-limit":     {"10000"},
		"anthropic-ratelimit-input-tokens-remaining": {"500"},
	}
	qm.UpdateChannelResponseHeaders(removedUID, "ep_removed", "anthropic", headers)
	qm.UpdateChannelResponseHeaders(keptUID, "ep_kept", "anthropic", headers)
	if len(qm.SnapshotAll()) != 2 {
		t.Fatalf("前置配额快照数量 = %d, want 2", len(qm.SnapshotAll()))
	}

	router := gin.New()
	h := &Handler{cm: cm, scheduler: sch}
	router.PUT("/api/logical-channels/:uid", h.Update)

	body := `{"removals":["messages"],"protocols":[{"kind":"chat","serviceType":"openai","apiKeys":["k1"],"baseUrls":["https://api.test.com/v1"]}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/logical-channels/"+logical.LogicalChannelUID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT 失败: %d %s", rec.Code, rec.Body.String())
	}

	if truth := qm.GetChannelTruth(removedUID); truth != quota.TruthUnknown {
		t.Fatalf("被移除协议的配额状态应被清理, truth = %v", truth)
	}
	states := qm.SnapshotAll()
	if len(states) != 1 || states[0].ChannelUID != keptUID {
		t.Fatalf("只应保留未被移除协议的配额状态, got %+v", states)
	}
}
