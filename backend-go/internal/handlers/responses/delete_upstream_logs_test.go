package responses

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

func TestDeleteUpstream_PreservesRemainingChannelLogs(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	cfg := config.Config{ResponsesUpstream: []config.UpstreamConfig{
		{Name: "channel-a", AccountUID: "shared-account", BaseURL: "https://shared.example.com", APIKeys: []string{"sk-a"}},
		{Name: "channel-b", AccountUID: "shared-account", BaseURL: "https://shared.example.com", APIKeys: []string{"sk-b"}},
	}}
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
		traceAffinity.Stop()
	})

	sch := scheduler.NewChannelScheduler(cm, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, nil)
	logStore := sch.GetChannelLogStore(scheduler.ChannelKindResponses)
	keyA := metrics.GenerateMetricsIdentityKey("https://shared.example.com", "sk-a", "responses")
	keyB := metrics.GenerateMetricsIdentityKey("https://shared.example.com", "sk-b", "responses")
	logStore.Record(keyA, &metrics.ChannelLog{RequestID: "r1", Model: "deleted-channel"})
	logStore.Record(keyB, &metrics.ChannelLog{RequestID: "r2", Model: "remaining-channel"})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/responses/channels/:id", DeleteUpstream(cm, sch))

	req := httptest.NewRequest(http.MethodDelete, "/responses/channels/0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	if got := logStore.Get(keyA); got != nil {
		t.Fatalf("deleted channel logs should be nil, got %v", got)
	}
	remainingLogs := logStore.Get(keyB)
	if len(remainingLogs) != 1 || remainingLogs[0].Model != "remaining-channel" {
		t.Fatalf("remaining logs = %#v, want remaining-channel", remainingLogs)
	}
}
