package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

// TestGetChannelLogs_AfterChannelDeletion 验证删除渠道后，logs API 对剩余渠道仍返回正确数据
func TestGetChannelLogs_AfterChannelDeletion(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// ch-a 和 ch-b 共享 baseURL，但使用不同的 key（不同 metricsKey 桶）
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "ch-a", AccountUID: "shared-account", BaseURL: "https://shared.example.com", APIKeys: []string{"sk-a"}},
			{Name: "ch-b", AccountUID: "shared-account", BaseURL: "https://shared.example.com", APIKeys: []string{"sk-b"}},
		},
	}

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(30*0, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)
	t.Cleanup(func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		traceAffinity.Stop()
	})

	logStore := sch.GetChannelLogStore(scheduler.ChannelKindMessages)

	// 用不同的 metricsKey 为两个渠道记录日志（与 stats 同源的 hash）
	keyA := metrics.GenerateMetricsIdentityKey("https://shared.example.com", "sk-a", "claude")
	keyB := metrics.GenerateMetricsIdentityKey("https://shared.example.com", "sk-b", "claude")

	logStore.Record(keyA, &metrics.ChannelLog{RequestID: "r1", Model: "model-a", BaseURL: "https://shared.example.com", KeyMask: "***a"})
	logStore.Record(keyB, &metrics.ChannelLog{RequestID: "r2", Model: "model-b", BaseURL: "https://shared.example.com", KeyMask: "***b"})

	// 验证删除前 ch-b 日志存在
	logsBefore := logStore.Get(keyB)
	if len(logsBefore) != 1 {
		t.Fatalf("删除前 ch-b 日志数 = %d, want 1", len(logsBefore))
	}

	// 模拟删除 ch-a：先从配置移除，再调 DeleteChannelLogs
	removed, err := cfgManager.RemoveUpstream(0)
	if err != nil {
		t.Fatalf("删除渠道失败: %v", err)
	}
	sch.DeleteChannelLogs(removed, scheduler.ChannelKindMessages)

	// ch-a 的日志应被清理（它是独占 key）
	if got := logStore.Get(keyA); got != nil {
		t.Fatalf("删除后 ch-a 日志应为 nil, got %v", got)
	}

	// ch-b 的日志应保留
	if got := logStore.Get(keyB); len(got) != 1 || got[0].RequestID != "r2" {
		t.Fatalf("删除后 ch-b 日志 = %v, want [r2]", got)
	}

	// 通过 API 查询 ch-b（index 现在是 0，因为 ch-a 被移除）
	r := gin.New()
	r.GET("/messages/channels/:id/logs", GetChannelLogs(logStore, cfgManager, scheduler.ChannelKindMessages, nil))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels/0/logs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		ChannelIndex int                   `json:"channelIndex"`
		Logs         []*metrics.ChannelLog `json:"logs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ChannelIndex != 0 {
		t.Fatalf("channelIndex = %d, want 0", resp.ChannelIndex)
	}
	if len(resp.Logs) != 1 || resp.Logs[0].RequestID != "r2" {
		t.Fatalf("logs = %#v, want [r2]", resp.Logs)
	}
}

func TestGetChannelLogs_FiltersSharedMetricsKeyByChannelIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "LocalHostClaude",
				AccountUID:  "shared-account",
				BaseURL:     "http://127.0.0.1:3699",
				APIKeys:     []string{"sk-local"},
				ServiceType: "responses",
			},
			{
				Name:        "LocalHostOpenAIChat",
				AccountUID:  "shared-account",
				BaseURL:     "http://127.0.0.1:3699",
				APIKeys:     []string{"sk-local"},
				ServiceType: "responses",
			},
		},
	}

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	logStore := metrics.NewChannelLogStore()
	metricsKey := metrics.GenerateMetricsIdentityKey("http://127.0.0.1:3699", "sk-local", "responses")
	now := time.Now()
	logStore.Record(metricsKey, &metrics.ChannelLog{RequestID: "claude-log", ChannelIndex: 0, Model: "gpt-5.4", Timestamp: now.Add(-time.Second)})
	logStore.Record(metricsKey, &metrics.ChannelLog{RequestID: "chat-log", ChannelIndex: 1, Model: "gpt-5.5", Timestamp: now})

	r := gin.New()
	r.GET("/responses/channels/:id/logs", GetChannelLogs(logStore, cfgManager, scheduler.ChannelKindResponses, nil))

	req := httptest.NewRequest(http.MethodGet, "/responses/channels/0/logs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		ChannelIndex int                   `json:"channelIndex"`
		Logs         []*metrics.ChannelLog `json:"logs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ChannelIndex != 0 {
		t.Fatalf("channelIndex = %d, want 0", resp.ChannelIndex)
	}
	if len(resp.Logs) != 1 {
		t.Fatalf("logs count = %d, want 1: %#v", len(resp.Logs), resp.Logs)
	}
	if resp.Logs[0].RequestID != "claude-log" {
		t.Fatalf("logs[0].RequestID = %q, want claude-log", resp.Logs[0].RequestID)
	}

	req = httptest.NewRequest(http.MethodGet, "/responses/channels/1/logs", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ChannelIndex != 1 {
		t.Fatalf("channelIndex = %d, want 1", resp.ChannelIndex)
	}
	if len(resp.Logs) != 1 {
		t.Fatalf("logs count = %d, want 1: %#v", len(resp.Logs), resp.Logs)
	}
	if resp.Logs[0].RequestID != "chat-log" {
		t.Fatalf("logs[0].RequestID = %q, want chat-log", resp.Logs[0].RequestID)
	}
}

func TestGetChannelLogs_FiltersSharedMetricsKeyByChannelNameAfterDeletion(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "RemainingA",
				AccountUID:  "shared-account",
				BaseURL:     "http://127.0.0.1:3699",
				APIKeys:     []string{"sk-local"},
				ServiceType: "responses",
			},
			{
				Name:        "RemainingB",
				AccountUID:  "shared-account",
				BaseURL:     "http://127.0.0.1:3699",
				APIKeys:     []string{"sk-local"},
				ServiceType: "responses",
			},
		},
	}

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	logStore := metrics.NewChannelLogStore()
	metricsKey := metrics.GenerateMetricsIdentityKey("http://127.0.0.1:3699", "sk-local", "responses")
	now := time.Now()
	logStore.Record(metricsKey, &metrics.ChannelLog{RequestID: "deleted-old-index", ChannelIndex: 0, ChannelName: "Deleted", Model: "gpt-5.4", Timestamp: now.Add(-2 * time.Second)})
	logStore.Record(metricsKey, &metrics.ChannelLog{RequestID: "remaining-a-old-index", ChannelIndex: 1, ChannelName: "RemainingA", Model: "gpt-5.5", Timestamp: now.Add(-time.Second)})
	logStore.Record(metricsKey, &metrics.ChannelLog{RequestID: "remaining-b-old-index", ChannelIndex: 2, ChannelName: "RemainingB", Model: "gpt-5.6", Timestamp: now})

	r := gin.New()
	r.GET("/responses/channels/:id/logs", GetChannelLogs(logStore, cfgManager, scheduler.ChannelKindResponses, nil))

	req := httptest.NewRequest(http.MethodGet, "/responses/channels/0/logs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Logs []*metrics.ChannelLog `json:"logs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Logs) != 1 {
		t.Fatalf("logs count = %d, want 1: %#v", len(resp.Logs), resp.Logs)
	}
	if resp.Logs[0].RequestID != "remaining-a-old-index" {
		t.Fatalf("logs[0].RequestID = %q, want remaining-a-old-index", resp.Logs[0].RequestID)
	}
}

func TestGetChannelLogs_ExcludesDeletedChannelResidualAfterBecomingExclusive(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 删除前有两个渠道共享同一 metricsKey；删除其一后仅 Remaining 独占该桶，
	// 但桶里仍残留已删除渠道的历史日志（带删除渠道名），必须按渠道名排除。
	cfg := config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "Remaining",
				BaseURL:     "http://127.0.0.1:3699",
				APIKeys:     []string{"sk-local"},
				ServiceType: "responses",
			},
		},
	}

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	logStore := metrics.NewChannelLogStore()
	metricsKey := metrics.GenerateMetricsIdentityKey("http://127.0.0.1:3699", "sk-local", "responses")
	now := time.Now()
	logStore.Record(metricsKey, &metrics.ChannelLog{RequestID: "deleted-residual", ChannelIndex: 1, ChannelName: "Deleted", Model: "gpt-5.4", Timestamp: now.Add(-time.Second)})
	logStore.Record(metricsKey, &metrics.ChannelLog{RequestID: "remaining", ChannelIndex: 0, ChannelName: "Remaining", Model: "gpt-5.5", Timestamp: now})

	r := gin.New()
	r.GET("/responses/channels/:id/logs", GetChannelLogs(logStore, cfgManager, scheduler.ChannelKindResponses, nil))

	req := httptest.NewRequest(http.MethodGet, "/responses/channels/0/logs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Logs []*metrics.ChannelLog `json:"logs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Logs) != 1 {
		t.Fatalf("logs count = %d, want 1: %#v", len(resp.Logs), resp.Logs)
	}
	if resp.Logs[0].RequestID != "remaining" {
		t.Fatalf("logs[0].RequestID = %q, want remaining", resp.Logs[0].RequestID)
	}
}

// TestGetChannelDashboard_AfterChannelDeletion 验证 dashboard 在渠道删除后 metrics 索引一致性
func TestGetChannelDashboard_AfterChannelDeletion(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "ch-a", BaseURL: "https://api-a.example.com", APIKeys: []string{"sk-a"}},
			{Name: "ch-b", BaseURL: "https://api-b.example.com", APIKeys: []string{"sk-b"}},
		},
	}

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(0, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)
	t.Cleanup(func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		traceAffinity.Stop()
	})

	// 为 channel 1 (ch-b) 记录 metrics
	messagesMetrics.RecordSuccess("https://api-b.example.com", "sk-b", "claude")

	// 模拟删除 channel 0，此时配置中只剩 channel-b
	removed, err := cfgManager.RemoveUpstream(0)
	if err != nil {
		t.Fatalf("删除渠道失败: %v", err)
	}
	sch.DeleteChannelLogs(removed, scheduler.ChannelKindMessages)
	sch.DeleteChannelMetrics(removed, scheduler.ChannelKindMessages)

	// 请求 dashboard
	r := gin.New()
	r.GET("/messages/channels/dashboard", GetChannelDashboard(cfgManager, sch))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels/dashboard?type=messages", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		Channels []map[string]any `json:"channels"`
		Metrics  []map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 删除后只剩 1 个渠道
	if len(resp.Channels) != 1 {
		t.Fatalf("channels count = %d, want 1", len(resp.Channels))
	}
	if resp.Channels[0]["name"] != "ch-b" {
		t.Fatalf("channel name = %v, want ch-b", resp.Channels[0]["name"])
	}

	// metrics 索引应该和 channels 索引对齐
	if len(resp.Metrics) != 1 {
		t.Fatalf("metrics count = %d, want 1", len(resp.Metrics))
	}
	metricsIdx, ok := resp.Metrics[0]["channelIndex"].(float64)
	if !ok || metricsIdx != 0 {
		t.Fatalf("metrics channelIndex = %v, want 0", resp.Metrics[0]["channelIndex"])
	}
}
