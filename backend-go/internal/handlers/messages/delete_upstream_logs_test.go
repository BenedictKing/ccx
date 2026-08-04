package messages

import (
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

func setupDeleteLogsConfigManager(t *testing.T, upstream []config.UpstreamConfig) *config.ConfigManager {
	t.Helper()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	cfg := config.Config{Upstream: upstream}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	cm, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	return cm
}

func TestDeleteUpstream_PreservesRemainingChannelLogs(t *testing.T) {
	cm := setupDeleteLogsConfigManager(t, []config.UpstreamConfig{
		{
			Name:       "channel-a",
			AccountUID: "shared-account",
			BaseURL:    "https://shared.example.com",
			APIKeys:    []string{"sk-a"},
		},
		{
			Name:       "channel-b",
			AccountUID: "shared-account",
			BaseURL:    "https://shared.example.com",
			APIKeys:    []string{"sk-b"},
		},
	})

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	t.Cleanup(func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		imagesMetrics.Stop()
		traceAffinity.Stop()
	})

	sch := scheduler.NewChannelScheduler(
		cm,
		messagesMetrics,
		responsesMetrics,
		geminiMetrics,
		chatMetrics,
		imagesMetrics,
		traceAffinity,
		nil,
	)
	logStore := sch.GetChannelLogStore(scheduler.ChannelKindMessages)
	keyA := metrics.GenerateMetricsIdentityKey("https://shared.example.com", "sk-a", "claude")
	keyB := metrics.GenerateMetricsIdentityKey("https://shared.example.com", "sk-b", "claude")
	logStore.Record(keyA, &metrics.ChannelLog{RequestID: "r1", Model: "deleted-channel", BaseURL: "https://shared.example.com", KeyMask: "***a"})
	logStore.Record(keyB, &metrics.ChannelLog{RequestID: "r2", Model: "remaining-channel", BaseURL: "https://shared.example.com", KeyMask: "***b"})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/messages/channels/:id", DeleteUpstream(cm, sch))

	req := httptest.NewRequest(http.MethodDelete, "/messages/channels/0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		Removed config.UpstreamConfig `json:"removed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Removed.Name != "channel-a" {
		t.Fatalf("removed name = %s, want channel-a", resp.Removed.Name)
	}

	if got := logStore.Get(keyA); got != nil {
		t.Fatalf("deleted channel logs should be nil, got %v", got)
	}

	remainingLogs := logStore.Get(keyB)
	if len(remainingLogs) != 1 || remainingLogs[0].Model != "remaining-channel" {
		t.Fatalf("remaining logs = %#v, want remaining-channel", remainingLogs)
	}

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 || cfg.Upstream[0].Name != "channel-b" {
		t.Fatalf("remaining upstreams = %#v, want only channel-b", cfg.Upstream)
	}
}
