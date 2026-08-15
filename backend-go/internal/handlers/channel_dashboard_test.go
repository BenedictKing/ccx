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
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestGetChannelDashboard_IncludesBreakerFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:    "msg-test",
			BaseURL: "https://example.com",
			APIKeys: []string{"sk-test"},
		}},
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
	defer errutil.IgnoreDeferred(cfgManager.Close)

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	defer messagesMetrics.Stop()
	defer responsesMetrics.Stop()
	defer geminiMetrics.Stop()
	defer chatMetrics.Stop()
	defer imagesMetrics.Stop()

	for i := 0; i < 5; i++ {
		messagesMetrics.RecordFailure("https://example.com", "sk-test", "claude")
	}

	traceAffinity := session.NewTraceAffinityManager()
	defer traceAffinity.Stop()
	urlManager := warmup.NewURLManager(30*time.Second, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)

	r := gin.New()
	r.GET("/messages/channels/dashboard", GetChannelDashboard(cfgManager, sch))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels/dashboard?type=messages", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Current int              `json:"current"`
		Metrics []map[string]any `json:"metrics"`
		Stats   map[string]any   `json:"stats"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Current != 0 {
		t.Fatalf("current=%d, want=0", resp.Current)
	}
	if len(resp.Metrics) != 1 {
		t.Fatalf("metrics len=%d, want=1", len(resp.Metrics))
	}
	if got := resp.Metrics[0]["circuitState"]; got != "open" {
		t.Fatalf("circuitState=%v, want=open", got)
	}
	if got := resp.Metrics[0]["runtimeState"]; got != "open" {
		t.Fatalf("runtimeState=%v, want=open", got)
	}
	if _, ok := resp.Metrics[0]["nextRetryAt"]; !ok {
		t.Fatalf("缺少 nextRetryAt 字段: %v", resp.Metrics[0])
	}
	if got := resp.Stats["halfOpenSuccessTarget"]; got != float64(1) {
		t.Fatalf("halfOpenSuccessTarget=%v, want=1", got)
	}
}

func TestGetChannelDashboard_IncludesConsumptionPolicyDistribution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:       "msg-test",
			ChannelUID: "ch_test_policy",
			BaseURL:    "https://example.com",
			APIKeys:    []string{"sk-public", "sk-private"},
		}},
	}
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
	defer errutil.IgnoreDeferred(cfgManager.Close)

	store, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{
		DBPath:        filepath.Join(tmpDir, "metrics.db"),
		RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("创建 SQLite store 失败: %v", err)
	}

	now := time.Now()
	for i := 0; i < 3; i++ {
		store.AddRecord(metrics.PersistentRecord{
			ChannelUID:        "ch_test_policy",
			MetricsKey:        "k1",
			BaseURL:           "https://example.com",
			KeyMask:           "sk-***pub",
			Timestamp:         now,
			Success:           true,
			APIType:           "messages",
			ConsumptionPolicy: "opportunistic",
		})
	}
	for i := 0; i < 2; i++ {
		store.AddRecord(metrics.PersistentRecord{
			ChannelUID:        "ch_test_policy",
			MetricsKey:        "k2",
			BaseURL:           "https://example.com",
			KeyMask:           "sk-***priv",
			Timestamp:         now,
			Success:           true,
			APIType:           "messages",
			ConsumptionPolicy: "normal",
		})
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭 SQLite store 失败: %v", err)
	}
	store, err = metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{
		DBPath:        filepath.Join(tmpDir, "metrics.db"),
		RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("重新打开 SQLite store 失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store.Close)

	messagesMetrics := metrics.NewMetricsManagerWithPersistence(10, 0.5, store, "messages")
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	defer messagesMetrics.Stop()
	defer responsesMetrics.Stop()
	defer geminiMetrics.Stop()
	defer chatMetrics.Stop()
	defer imagesMetrics.Stop()

	traceAffinity := session.NewTraceAffinityManager()
	defer traceAffinity.Stop()
	urlManager := warmup.NewURLManager(30*time.Second, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)

	r := gin.New()
	r.GET("/messages/channels/dashboard", GetChannelDashboard(cfgManager, sch))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels/dashboard?type=messages", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Channels []map[string]any `json:"channels"`
		Metrics  []map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Channels) != 1 || len(resp.Metrics) != 1 {
		t.Fatalf("channels=%d metrics=%d, want 1/1", len(resp.Channels), len(resp.Metrics))
	}

	assertDist := func(m map[string]any) {
		raw, ok := m["consumptionPolicyDistribution"]
		if !ok {
			t.Fatalf("缺少 consumptionPolicyDistribution: %v", m)
		}
		dist, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("consumptionPolicyDistribution 类型错误: %T", raw)
		}
		if dist["opportunistic"] != float64(3) {
			t.Errorf("opportunistic=%v, want 3", dist["opportunistic"])
		}
		if dist["normal"] != float64(2) {
			t.Errorf("normal=%v, want 2", dist["normal"])
		}
	}
	assertDist(resp.Channels[0])
	assertDist(resp.Metrics[0])
}

func TestGetChannelDashboard_LLMReturnsAllProtocolDashboards(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := func(name, serviceType string) []config.UpstreamConfig {
		return []config.UpstreamConfig{{
			Name:        name,
			ServiceType: serviceType,
			BaseURL:     "https://example.com",
			APIKeys:     []string{"sk-test"},
		}}
	}
	cfg := config.Config{
		Upstream:          upstream("messages-test", "claude"),
		ChatUpstream:      upstream("chat-test", "openai"),
		ResponsesUpstream: upstream("responses-test", "responses"),
		GeminiUpstream:    upstream("gemini-test", "gemini"),
	}

	configFile := filepath.Join(t.TempDir(), "config.json")
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
	defer errutil.IgnoreDeferred(cfgManager.Close)

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	defer messagesMetrics.Stop()
	defer responsesMetrics.Stop()
	defer geminiMetrics.Stop()
	defer chatMetrics.Stop()
	defer imagesMetrics.Stop()

	traceAffinity := session.NewTraceAffinityManager()
	defer traceAffinity.Stop()
	sch := scheduler.NewChannelScheduler(
		cfgManager,
		messagesMetrics,
		responsesMetrics,
		geminiMetrics,
		chatMetrics,
		imagesMetrics,
		traceAffinity,
		warmup.NewURLManager(30*time.Second, 3),
	)

	r := gin.New()
	r.GET("/messages/channels/dashboard", GetChannelDashboard(cfgManager, sch))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/messages/channels/dashboard?type=llm", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Dashboards map[string]struct {
			Channels []map[string]any `json:"channels"`
		} `json:"dashboards"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	for _, kind := range []string{"messages", "chat", "responses", "gemini"} {
		dashboard, ok := resp.Dashboards[kind]
		if !ok {
			t.Fatalf("缺少 %s dashboard: %v", kind, resp.Dashboards)
		}
		if len(dashboard.Channels) != 1 {
			t.Fatalf("%s channels len=%d, want=1", kind, len(dashboard.Channels))
		}
	}
}

func TestGetChannelDashboard_GeminiFallbackServiceTypeReadsMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{{
			Name:    "gemini-empty-service-type",
			BaseURL: "https://example.com",
			APIKeys: []string{"sk-test"},
		}},
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
	defer errutil.IgnoreDeferred(cfgManager.Close)

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	defer messagesMetrics.Stop()
	defer responsesMetrics.Stop()
	defer geminiMetrics.Stop()
	defer chatMetrics.Stop()
	defer imagesMetrics.Stop()

	for i := 0; i < 5; i++ {
		geminiMetrics.RecordFailure("https://example.com", "sk-test", "gemini")
	}

	traceAffinity := session.NewTraceAffinityManager()
	defer traceAffinity.Stop()
	urlManager := warmup.NewURLManager(30*time.Second, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)

	r := gin.New()
	r.GET("/messages/channels/dashboard", GetChannelDashboard(cfgManager, sch))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels/dashboard?type=gemini", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Metrics []map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Metrics) != 1 {
		t.Fatalf("metrics len=%d, want=1", len(resp.Metrics))
	}
	if got := resp.Metrics[0]["circuitState"]; got != "open" {
		t.Fatalf("circuitState=%v, want=open", got)
	}
}

func TestGetChannelDashboard_Gemini_IncludesAdvancedOptionFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	enabled := true
	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:                    "gemini-test",
				ServiceType:             "gemini",
				BaseURL:                 "https://example.com",
				APIKeys:                 []string{"test-key"},
				ReasoningMapping:        map[string]string{"gemini-2.5-pro": "high"},
				TextVerbosity:           "medium",
				FastMode:                true,
				StripThoughtSignature:   true,
				NormalizeMetadataUserID: &enabled,
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

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	t.Cleanup(func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		chatMetrics.Stop()
		geminiMetrics.Stop()
	})

	traceAffinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(30*time.Second, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)

	r := gin.New()
	r.GET("/messages/channels/dashboard", GetChannelDashboard(cfgManager, sch))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels/dashboard?type=gemini", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Channels []map[string]any `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Channels) != 1 {
		t.Fatalf("channels len=%d, want=1", len(resp.Channels))
	}

	value, ok := resp.Channels[0]["stripThoughtSignature"]
	if !ok {
		t.Fatalf("响应缺少 stripThoughtSignature 字段: %v", resp.Channels[0])
	}
	strip, ok := value.(bool)
	if !ok {
		t.Fatalf("stripThoughtSignature 类型=%T, want=bool", value)
	}
	if strip != true {
		t.Fatalf("stripThoughtSignature=%v, want=true", strip)
	}

	if got := resp.Channels[0]["textVerbosity"]; got != "medium" {
		t.Fatalf("textVerbosity=%v, want=medium", got)
	}

	if got := resp.Channels[0]["fastMode"]; got != true {
		t.Fatalf("fastMode=%v, want=true", got)
	}

	reasoning, ok := resp.Channels[0]["reasoningMapping"].(map[string]any)
	if !ok {
		t.Fatalf("reasoningMapping 类型=%T, want=map[string]any", resp.Channels[0]["reasoningMapping"])
	}
	if got := reasoning["gemini-2.5-pro"]; got != "high" {
		t.Fatalf("reasoningMapping[gemini-2.5-pro]=%v, want=high", got)
	}
	if got := resp.Channels[0]["normalizeMetadataUserId"]; got != true {
		t.Fatalf("normalizeMetadataUserId=%v, want=true", got)
	}
	if got := resp.Channels[0]["stripEmptyTextBlocks"]; got != false {
		t.Fatalf("gemini channel stripEmptyTextBlocks=%v, want=false (not configured)", got)
	}
}

func TestGetChannelDashboard_MessagesIncludesStripEmptyTextBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	channel := config.UpstreamConfig{
		Name:        "msg-claude",
		ServiceType: "claude",
		BaseURL:     "https://example.com",
		APIKeys:     []string{"sk-test"},
	}
	// 该兼容开关已无手工字段：用可落盘的种子表达"生效值为 true"。
	channel.SetCompatSeed(config.TraitStripEmptyTextBlocks, true)
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{channel},
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
	defer errutil.IgnoreDeferred(cfgManager.Close)

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	defer messagesMetrics.Stop()
	defer responsesMetrics.Stop()
	defer geminiMetrics.Stop()
	defer chatMetrics.Stop()
	defer imagesMetrics.Stop()

	traceAffinity := session.NewTraceAffinityManager()
	defer traceAffinity.Stop()
	urlManager := warmup.NewURLManager(30*time.Second, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)

	r := gin.New()
	r.GET("/messages/channels/dashboard", GetChannelDashboard(cfgManager, sch))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels/dashboard?type=messages", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Channels []map[string]any `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Channels) != 1 {
		t.Fatalf("channels len=%d, want=1", len(resp.Channels))
	}
	if got := resp.Channels[0]["stripEmptyTextBlocks"]; got != true {
		t.Fatalf("stripEmptyTextBlocks=%v, want=true", got)
	}
}

func TestGetChannelDashboard_ChatFallbackServiceTypeReadsMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		ChatUpstream: []config.UpstreamConfig{{
			Name:    "chat-empty-service-type",
			BaseURL: "https://example.com",
			APIKeys: []string{"sk-test"},
		}},
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
	defer errutil.IgnoreDeferred(cfgManager.Close)

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	defer messagesMetrics.Stop()
	defer responsesMetrics.Stop()
	defer geminiMetrics.Stop()
	defer chatMetrics.Stop()
	defer imagesMetrics.Stop()

	for i := 0; i < 5; i++ {
		chatMetrics.RecordFailure("https://example.com", "sk-test", "openai")
	}

	traceAffinity := session.NewTraceAffinityManager()
	defer traceAffinity.Stop()
	urlManager := warmup.NewURLManager(30*time.Second, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)

	r := gin.New()
	r.GET("/messages/channels/dashboard", GetChannelDashboard(cfgManager, sch))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels/dashboard?type=chat", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Metrics []map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Metrics) != 1 {
		t.Fatalf("metrics len=%d, want=1", len(resp.Metrics))
	}
	if got := resp.Metrics[0]["circuitState"]; got != "open" {
		t.Fatalf("circuitState=%v, want=open", got)
	}
}
