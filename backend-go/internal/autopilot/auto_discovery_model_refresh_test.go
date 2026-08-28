package autopilot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/utils"
)

// setupRefreshConfigManager 构造指向指定 baseURL 的自动托管渠道配置管理器。
func setupRefreshConfigManager(t *testing.T, channelUID, accountUID, baseURL string) *config.ConfigManager {
	t.Helper()
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			ChannelUID:  channelUID,
			Name:        "refresh-channel",
			ServiceType: "openai",
			BaseURL:     baseURL,
			BaseURLs:    []string{baseURL},
			APIKeys:     []string{"sk-refresh-key"},
			AccountUID:  accountUID,
			AutoManaged: true,
			Status:      "active",
		}},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("创建 ConfigManager 失败: %v", err)
	}
	return cfgManager
}

func TestChannelModelsStale(t *testing.T) {
	now := time.Now()
	stale := now.Add(-8 * 24 * time.Hour)
	fresh := now.Add(-1 * time.Hour)

	tests := []struct {
		name    string
		profile *KeyEndpointProfile
		second  *KeyEndpointProfile
		want    bool
	}{
		{
			name: "时间戳缺失视为过期",
			profile: &KeyEndpointProfile{EndpointUID: "ep-nil", ChannelUID: "ch-stale", ServiceType: "claude",
				AvailableModels: []string{"m1"}},
			want: true,
		},
		{
			name: "TTL 内不过期",
			profile: &KeyEndpointProfile{EndpointUID: "ep-fresh", ChannelUID: "ch-stale", ServiceType: "claude",
				AvailableModels: []string{"m1"}, ModelsDiscoveredAt: &fresh},
			want: false,
		},
		{
			name: "全部端点过期",
			profile: &KeyEndpointProfile{EndpointUID: "ep-old-1", ChannelUID: "ch-stale", ServiceType: "claude",
				AvailableModels: []string{"m1"}, ModelsDiscoveredAt: &stale},
			second: &KeyEndpointProfile{EndpointUID: "ep-old-2", ChannelUID: "ch-stale", ServiceType: "claude",
				AvailableModels: []string{"m1"}, ModelsDiscoveredAt: &stale},
			want: true,
		},
		{
			name: "任一端点在 TTL 内即不过期",
			profile: &KeyEndpointProfile{EndpointUID: "ep-mix-old", ChannelUID: "ch-stale", ServiceType: "claude",
				AvailableModels: []string{"m1"}, ModelsDiscoveredAt: &stale},
			second: &KeyEndpointProfile{EndpointUID: "ep-mix-fresh", ChannelUID: "ch-stale", ServiceType: "claude",
				AvailableModels: []string{"m1"}, ModelsDiscoveredAt: &fresh},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner, store, _ := newRunnerWithTaskStore(t)
			for _, p := range []*KeyEndpointProfile{tt.profile, tt.second} {
				if p == nil {
					continue
				}
				if err := store.Upsert(p); err != nil {
					t.Fatalf("Upsert 画像失败: %v", err)
				}
			}
			// 无画像渠道走 "无画像不过期" 分支，单独覆盖。
			uid := tt.profile.ChannelUID
			if tt.profile.EndpointUID == "" {
				uid = "ch-empty"
			}
			if got := runner.channelModelsStale(uid, now); got != tt.want {
				t.Fatalf("channelModelsStale(%s) = %v, want %v", uid, got, tt.want)
			}
		})
	}
}

// TestRefreshStaleModelLists_TriggersDiscoveryForStaleChannel 端到端验证：
// 画像模型清单超过 TTL 的自动托管渠道被重新发现，画像刷新为上游最新清单。
func TestRefreshStaleModelLists_TriggersDiscoveryForStaleChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-new"}]}`))
	}))
	defer server.Close()

	runner, store, _ := newRunnerWithTaskStore(t)
	channelUID := "ch-refresh"
	channel := &config.UpstreamConfig{
		AccountUID:  "acct-refresh",
		ChannelUID:  channelUID,
		ServiceType: "openai",
		BaseURL:     server.URL,
		BaseURLs:    []string{server.URL},
		APIKeys:     []string{"sk-refresh-key"},
		AutoManaged: true,
	}

	// 预写一份 8 天前的过期画像（模型清单为旧值）。
	stale := time.Now().Add(-8 * 24 * time.Hour)
	endpoint := EndpointDiscoveryResult{
		KeyMask:            utils.MaskAPIKey("sk-refresh-key"),
		BaseURL:            server.URL,
		Models:             []string{"model-old"},
		ModelsCount:        1,
		ProtocolOk:         true,
		ModelsDiscoveredAt: &stale,
	}
	uid, err := runner.writeProfileForEndpoint(channelUID, channel, endpoint, 0, "messages", nil)
	if err != nil {
		t.Fatalf("预写画像失败: %v", err)
	}
	if err := runner.flushStores(); err != nil {
		t.Fatalf("flushStores 失败: %v", err)
	}

	cfgManager := setupRefreshConfigManager(t, channelUID, "acct-refresh", server.URL)
	defer func() { _ = cfgManager.Close() }()

	if !runner.channelModelsStale(channelUID, time.Now()) {
		t.Fatal("预写的过期画像应判定为 stale")
	}

	runner.refreshStaleModelLists(cfgManager)

	// 后台发现是异步的，轮询等待画像刷新（上限 5 秒）。
	deadline := time.Now().Add(5 * time.Second)
	for {
		profile := store.Get(uid)
		if profile != nil && len(profile.AvailableModels) == 1 && profile.AvailableModels[0] == "model-new" {
			if profile.ModelsDiscoveredAt == nil || time.Since(*profile.ModelsDiscoveredAt) > time.Minute {
				t.Fatalf("画像刷新后 ModelsDiscoveredAt 应为当前时间, 实际: %v", profile.ModelsDiscoveredAt)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待画像刷新超时, 当前画像: %+v", store.Get(uid))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestRefreshStaleModelLists_SkipsFreshChannel 验证 TTL 内的渠道不会被重新探测。
func TestRefreshStaleModelLists_SkipsFreshChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-new"}]}`))
	}))
	defer server.Close()

	runner, store, _ := newRunnerWithTaskStore(t)
	channelUID := "ch-fresh"
	channel := &config.UpstreamConfig{
		AccountUID:  "acct-fresh",
		ChannelUID:  channelUID,
		ServiceType: "openai",
		BaseURL:     server.URL,
		BaseURLs:    []string{server.URL},
		APIKeys:     []string{"sk-fresh-key"},
		AutoManaged: true,
	}
	fresh := time.Now().Add(-1 * time.Hour)
	endpoint := EndpointDiscoveryResult{
		KeyMask:            utils.MaskAPIKey("sk-fresh-key"),
		BaseURL:            server.URL,
		Models:             []string{"model-old"},
		ModelsCount:        1,
		ProtocolOk:         true,
		ModelsDiscoveredAt: &fresh,
	}
	uid, err := runner.writeProfileForEndpoint(channelUID, channel, endpoint, 0, "messages", nil)
	if err != nil {
		t.Fatalf("预写画像失败: %v", err)
	}
	if err := runner.flushStores(); err != nil {
		t.Fatalf("flushStores 失败: %v", err)
	}

	cfgManager := setupRefreshConfigManager(t, channelUID, "acct-fresh", server.URL)
	defer func() { _ = cfgManager.Close() }()

	runner.refreshStaleModelLists(cfgManager)
	time.Sleep(500 * time.Millisecond)

	profile := store.Get(uid)
	if profile == nil {
		t.Fatal("画像不应丢失")
	}
	if len(profile.AvailableModels) != 1 || profile.AvailableModels[0] != "model-old" {
		t.Fatalf("TTL 内渠道不应被重新发现, 画像模型 = %v", profile.AvailableModels)
	}
}
