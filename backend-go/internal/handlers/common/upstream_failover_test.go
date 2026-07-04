package common

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestShouldNormalizeMetadataUserIDOnlyMessages(t *testing.T) {
	enabled := true
	disabled := false

	tests := []struct {
		name     string
		kind     scheduler.ChannelKind
		upstream *config.UpstreamConfig
		want     bool
	}{
		{
			name:     "messages inherits default enabled",
			kind:     scheduler.ChannelKindMessages,
			upstream: &config.UpstreamConfig{},
			want:     true,
		},
		{
			name:     "messages honors disabled switch",
			kind:     scheduler.ChannelKindMessages,
			upstream: &config.UpstreamConfig{NormalizeMetadataUserID: &disabled},
			want:     false,
		},
		{
			name:     "responses ignores enabled switch",
			kind:     scheduler.ChannelKindResponses,
			upstream: &config.UpstreamConfig{NormalizeMetadataUserID: &enabled},
			want:     false,
		},
		{
			name:     "nil upstream",
			kind:     scheduler.ChannelKindMessages,
			upstream: nil,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldNormalizeMetadataUserID(tt.kind, tt.upstream); got != tt.want {
				t.Fatalf("shouldNormalizeMetadataUserID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTryUpstreamWithAllKeysRejectsOversizedVisionFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:                "desktop-compshare-messages",
				BaseURL:             "https://upstream.example.com",
				APIKeys:             []string{"sk-test"},
				Status:              "active",
				ServiceType:         "openai",
				ModelMapping:        map[string]string{"haiku": "deepseek-v4-flash"},
				NoVisionModels:      []string{"deepseek-v4-flash"},
				VisionFallbackModel: "MiniMax-M2.7",
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"deepseek-v4-flash": {ContextWindowTokens: 1000000},
					"MiniMax-M2.7":      {ContextWindowTokens: 200000},
				},
			},
		},
	}

	tmpDir, err := os.MkdirTemp("", "vision-fallback-context-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.json")
	cfgData, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configPath, cfgData, 0644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer cfgManager.Close()

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

	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		messagesMetrics,
		responsesMetrics,
		geminiMetrics,
		chatMetrics,
		imagesMetrics,
		session.NewTraceAffinityManager(),
		warmup.NewURLManager(30*time.Second, 3),
	)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", http.NoBody)

	requestBody := []byte(`{"model":"haiku","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}]}]}`)
	requirement := &scheduler.ContextRequirement{InputTokens: 250000, OutputTokens: 4096, RequiredTokens: 254096}
	upstream := &cfg.Upstream[0]
	buildCalled := false

	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: upstream.BaseURL}},
		requestBody,
		requirement,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return upstream.APIKeys[0], nil
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			buildCalled = true
			return httptest.NewRequest(http.MethodPost, upstreamCopy.BaseURL, http.NoBody), nil
		},
		func(apiKey string) {},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			return nil, nil
		},
		"haiku",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if handled {
		t.Fatal("fallback 上下文不足时不应处理请求")
	}
	if successKey != "" {
		t.Fatalf("successKey = %q, want empty", successKey)
	}
	if failoverErr != nil {
		t.Fatalf("failoverErr = %#v, want nil", failoverErr)
	}
	if lastErr == nil {
		t.Fatal("期望返回上下文校验错误")
	}
	if !strings.Contains(lastErr.Error(), "MiniMax-M2.7") || !strings.Contains(lastErr.Error(), "上下文窗口") {
		t.Fatalf("错误信息未包含 fallback 模型上下文根因: %v", lastErr)
	}
	if buildCalled {
		t.Fatal("fallback 模型上下文不足时不应构建上游请求")
	}
}
