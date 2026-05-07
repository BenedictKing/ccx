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
	"github.com/shopspring/decimal"
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

	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer func() { _ = cfgManager.Close() }()

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

	for i := 0; i < 3; i++ {
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
		Metrics []map[string]any `json:"metrics"`
		Stats   map[string]any   `json:"stats"`
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

	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer func() { _ = cfgManager.Close() }()

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

	for i := 0; i < 3; i++ {
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
	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:                  "gemini-test",
				ServiceType:           "gemini",
				BaseURL:               "https://example.com",
				APIKeys:               []string{"test-key"},
				ReasoningMapping:      map[string]string{"gemini-2.5-pro": "high"},
				TextVerbosity:         "medium",
				FastMode:              true,
				StripThoughtSignature: true,
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

	cfgManager, err := config.NewConfigManager(configFile)
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

	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer func() { _ = cfgManager.Close() }()

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

	for i := 0; i < 3; i++ {
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

// PR3 T9: dashboard 应返回 cost / cache token 字段（来自 LB 聚合面）。
func TestGetChannelDashboard_ExposesCostAndCacheTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:    "msg-cost",
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
	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer func() { _ = cfgManager.Close() }()

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

	// 注入 cost / token 累计：模拟 wire.Finalize → RecordCost。
	channelKey := metrics.BuildLBChannelKey(string(scheduler.ChannelKindMessages), "msg-cost")
	cost := decimal.RequireFromString("1.2345")
	messagesMetrics.RecordCost(channelKey, &cost, 1000, 500, 200, 100)

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
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
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
	m0 := resp.Metrics[0]

	// totalCost 必须是 string（与 T6/T7 一致），值等于 RecordCost 注入的 decimal 字符串。
	costStr, ok := m0["totalCost"].(string)
	if !ok {
		t.Fatalf("totalCost 类型=%T, want=string; full=%v", m0["totalCost"], m0)
	}
	if costStr != "1.2345" {
		t.Fatalf("totalCost=%q, want=1.2345", costStr)
	}

	// 4 个 token 字段：JSON 数字解码到 float64。
	if got := m0["inputTokens"]; got != float64(1000) {
		t.Fatalf("inputTokens=%v, want=1000", got)
	}
	if got := m0["outputTokens"]; got != float64(500) {
		t.Fatalf("outputTokens=%v, want=500", got)
	}
	if got := m0["cacheReadInputTokens"]; got != float64(200) {
		t.Fatalf("cacheReadInputTokens=%v, want=200", got)
	}
	if got := m0["cacheCreationInputTokens"]; got != float64(100) {
		t.Fatalf("cacheCreationInputTokens=%v, want=100", got)
	}
	// totalTokens = input + output + cacheRead + cacheCreate = 1800
	if got := m0["totalTokens"]; got != float64(1800) {
		t.Fatalf("totalTokens=%v, want=1800", got)
	}
}

// PR3 T9: 未注入 cost 数据时，cost 应为 "0"，token 字段为 0。
func TestGetChannelDashboard_DefaultsCostAndCacheToZero(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:    "msg-zero",
			BaseURL: "https://example.com",
			APIKeys: []string{"sk-test"},
		}},
	}
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer func() { _ = cfgManager.Close() }()

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
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Metrics []map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	m0 := resp.Metrics[0]
	if got := m0["totalCost"]; got != "0" {
		t.Fatalf("totalCost=%v, want=\"0\"", got)
	}
	if got := m0["totalTokens"]; got != float64(0) {
		t.Fatalf("totalTokens=%v, want=0", got)
	}
}
