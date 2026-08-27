package autopilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func newProxyTestConfigManager(t *testing.T, channels ...config.UpstreamConfig) *config.ConfigManager {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{"managedAccounts": [], "upstream": [], "chatUpstream": [], "responsesUpstream": [], "geminiUpstream": [], "imagesUpstream": [], "vectorsUpstream": []}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatalf("写测试配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })
	for _, ch := range channels {
		if err := cfgManager.AddUpstream(ch); err != nil {
			t.Fatalf("AddUpstream 失败: %v", err)
		}
	}
	return cfgManager
}

// 渠道"代理通道"是唯一事实源：生效代理解析渠道级优先，回退订阅级存量。
func TestEffectiveProxyForChannelFirst(t *testing.T) {
	channelWithProxy := config.UpstreamConfig{
		ChannelUID: "ch-with-proxy", Name: "with-proxy", ServiceType: "messages",
		ProxyURL: "socks5://127.0.0.1:6785", ProxyPreferDirect: true,
	}

	tests := []struct {
		name           string
		channels       []config.UpstreamConfig
		linkedUIDs     []string
		withCfgManager bool
		subProfile     SubscriptionProfile
		wantURL        string
		wantDirect     bool
	}{
		{
			name:           "关联渠道代理优先生效",
			channels:       []config.UpstreamConfig{channelWithProxy},
			linkedUIDs:     []string{"ch-with-proxy"},
			withCfgManager: true,
			subProfile:     SubscriptionProfile{SubscriptionUID: "sub-1", ProxyURL: "socks5://sub-legacy:1080", ProxyPreferDirect: false},
			wantURL:        "socks5://127.0.0.1:6785",
			wantDirect:     true,
		},
		{
			name:           "关联渠道未配代理回退订阅级",
			channels:       []config.UpstreamConfig{{ChannelUID: "ch-no-proxy", Name: "no-proxy", ServiceType: "messages"}},
			linkedUIDs:     []string{"ch-no-proxy"},
			withCfgManager: true,
			subProfile:     SubscriptionProfile{SubscriptionUID: "sub-1", ProxyURL: "socks5://sub-legacy:1080", ProxyPreferDirect: true},
			wantURL:        "socks5://sub-legacy:1080",
			wantDirect:     true,
		},
		{
			name:           "多个关联渠道取第一个配置了代理的",
			channels:       []config.UpstreamConfig{{ChannelUID: "ch-a", Name: "a", ServiceType: "messages"}, channelWithProxy},
			linkedUIDs:     []string{"ch-a", "ch-with-proxy"},
			withCfgManager: true,
			subProfile:     SubscriptionProfile{SubscriptionUID: "sub-1", ProxyURL: "socks5://sub-legacy:1080"},
			wantURL:        "socks5://127.0.0.1:6785",
			wantDirect:     true,
		},
		{
			name:           "无关联渠道回退订阅级",
			withCfgManager: true,
			subProfile:     SubscriptionProfile{SubscriptionUID: "sub-1", ProxyURL: "socks5://sub-legacy:1080"},
			wantURL:        "socks5://sub-legacy:1080",
		},
		{
			name:       "无 cfgManager 回退订阅级（兼容单测/降级场景）",
			subProfile: SubscriptionProfile{SubscriptionUID: "sub-1", ProxyURL: "socks5://sub-legacy:1080", ProxyPreferDirect: true},
			wantURL:    "socks5://sub-legacy:1080",
			wantDirect: true,
		},
		{
			name:           "渠道与订阅级均未配置返回空",
			channels:       []config.UpstreamConfig{{ChannelUID: "ch-empty", Name: "empty", ServiceType: "messages"}},
			linkedUIDs:     []string{"ch-empty"},
			withCfgManager: true,
			subProfile:     SubscriptionProfile{SubscriptionUID: "sub-1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewSubscriptionStoreWithDB(newTestDB(t))
			if err != nil {
				t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
			}
			svc := NewNewApiSubscriptionSyncService(NewApiSubscriptionSyncServiceDeps{Store: store})
			if tc.withCfgManager {
				svc.cfgManager = newProxyTestConfigManager(t, tc.channels...)
			}
			profile := tc.subProfile
			profile.LinkedChannelUIDs = tc.linkedUIDs

			gotURL, gotDirect := svc.effectiveProxyFor(&profile)
			if gotURL != tc.wantURL || gotDirect != tc.wantDirect {
				t.Fatalf("effectiveProxyFor() = (%q, %v), want (%q, %v)", gotURL, gotDirect, tc.wantURL, tc.wantDirect)
			}
		})
	}
}

// 账号级代理覆盖：显式配置优先，空值继承传入的生效值。
func TestResolveNewApiAccountProxy(t *testing.T) {
	tests := []struct {
		name         string
		account      NewApiAccount
		fallbackURL  string
		fallbackPass bool
		wantURL      string
		wantDirect   bool
	}{
		{
			name:         "账号级覆盖优先",
			account:      NewApiAccount{ProxyURL: "socks5://account:1080", ProxyPreferDirect: false},
			fallbackURL:  "socks5://channel:6785",
			fallbackPass: true,
			wantURL:      "socks5://account:1080",
		},
		{
			name:         "账号级为空继承生效值",
			account:      NewApiAccount{},
			fallbackURL:  "socks5://channel:6785",
			fallbackPass: true,
			wantURL:      "socks5://channel:6785",
			wantDirect:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotDirect := resolveNewApiAccountProxy(tc.account, tc.fallbackURL, tc.fallbackPass)
			if gotURL != tc.wantURL || gotDirect != tc.wantDirect {
				t.Fatalf("resolveNewApiAccountProxy() = (%q, %v), want (%q, %v)", gotURL, gotDirect, tc.wantURL, tc.wantDirect)
			}
		})
	}
}
