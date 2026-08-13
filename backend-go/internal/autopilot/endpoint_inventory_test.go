package autopilot

import (
	"reflect"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestBuildEndpointInventoryRespectsBoundAndUnboundKeys(t *testing.T) {
	channel := config.UpstreamConfig{
		ChannelUID:  "ch-multi",
		ServiceType: "claude",
		BaseURLs: []string{
			"https://api.example.com/anthropic",
			"https://plan.example.com/anthropic",
		},
		APIKeys: []string{"sk-api", "sk-plan", "sk-unbound", "sk-unbound"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-api", BaseURL: "https://api.example.com/anthropic"},
			{Key: "sk-plan", BaseURL: "https://plan.example.com/anthropic"},
			{Key: "sk-unbound"},
		},
	}

	inventory := buildEndpointInventory(config.Config{Upstream: []config.UpstreamConfig{channel}})
	baseURLs := channel.GetAllBaseURLs()
	if len(inventory.Entries) != 2 || len(baseURLs) != 2 {
		t.Fatalf("entries=%d baseURLs=%d, want 2/2", len(inventory.Entries), len(baseURLs))
	}

	keysByURL := make(map[string][]string, len(inventory.Entries))
	for _, entry := range inventory.Entries {
		keysByURL[entry.BaseURL] = entry.APIKeys
		if entry.ChannelKind != "messages" || entry.ServiceType != "claude" {
			t.Fatalf("协议身份错误: kind=%q serviceType=%q", entry.ChannelKind, entry.ServiceType)
		}
	}
	if got, want := keysByURL[baseURLs[0]], []string{"sk-api", "sk-unbound"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("首个 URL keys=%v, want %v", got, want)
	}
	if got, want := keysByURL[baseURLs[1]], []string{"sk-plan", "sk-unbound"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("第二个 URL keys=%v, want %v", got, want)
	}
	if len(inventory.EndpointUIDs) != 4 {
		t.Fatalf("有效 endpoint UID 数=%d, want 4", len(inventory.EndpointUIDs))
	}
	if len(inventory.ModelProfileBindings) != 4 {
		t.Fatalf("有效模型 binding 数=%d, want 4", len(inventory.ModelProfileBindings))
	}
	for baseURL, keys := range keysByURL {
		for _, apiKey := range keys {
			uid := GenerateEndpointUID(channel.ChannelUID, baseURL, KeyHashFromAPIKey(apiKey))
			if _, ok := inventory.EndpointUIDs[uid]; !ok {
				t.Fatalf("缺少 endpoint UID: url=%s key=%s", baseURL, apiKey)
			}
			metricsKey := computeMetricsIdentityKey(baseURL, apiKey, "claude")
			bindingKey := modelProfileBindingKey(channel.ChannelUID, "messages", metricsKey)
			if _, ok := inventory.ModelProfileBindings[bindingKey]; !ok {
				t.Fatalf("缺少模型 binding: url=%s key=%s", baseURL, apiKey)
			}
		}
	}
}

func TestBuildEndpointInventoryMatchesRuntimeReachability(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		wantEntries int
		wantService string
	}{
		{
			name: "按入口补齐指标 service type",
			cfg: config.Config{ChatUpstream: []config.UpstreamConfig{{
				ChannelUID: "ch-chat",
				BaseURL:    "https://chat.example.com/v1",
				APIKeys:    []string{"sk-chat"},
			}}},
			wantEntries: 1,
			wantService: "openai",
		},
		{
			name: "绑定到未配置 URL 的 Key 不可达",
			cfg: config.Config{Upstream: []config.UpstreamConfig{{
				ChannelUID:  "ch-missing-bound-url",
				ServiceType: "claude",
				BaseURLs:    []string{"https://configured.example.com/anthropic"},
				APIKeys:     []string{"sk-bound-elsewhere"},
				APIKeyConfigs: []config.APIKeyConfig{{
					Key:     "sk-bound-elsewhere",
					BaseURL: "https://missing.example.com/anthropic",
				}},
			}}},
		},
		{
			name: "缺少稳定渠道身份时跳过",
			cfg: config.Config{ResponsesUpstream: []config.UpstreamConfig{{
				BaseURL: "https://responses.example.com/v1",
				APIKeys: []string{"sk-no-channel-uid"},
			}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inventory := buildEndpointInventory(tt.cfg)
			if len(inventory.Entries) != tt.wantEntries {
				t.Fatalf("entries=%d, want %d", len(inventory.Entries), tt.wantEntries)
			}
			if len(inventory.EndpointUIDs) != tt.wantEntries {
				t.Fatalf("endpointUIDs=%d, want %d", len(inventory.EndpointUIDs), tt.wantEntries)
			}
			if len(inventory.ModelProfileBindings) != tt.wantEntries {
				t.Fatalf("modelProfileBindings=%d, want %d", len(inventory.ModelProfileBindings), tt.wantEntries)
			}
			if tt.wantEntries > 0 && inventory.Entries[0].ServiceType != tt.wantService {
				t.Fatalf("serviceType=%q, want %q", inventory.Entries[0].ServiceType, tt.wantService)
			}
		})
	}
}

func TestBuildEndpointInventoryIncludesKeyBoundURLOutsideBaseURLs(t *testing.T) {
	channel := config.UpstreamConfig{
		ChannelUID:      "ch-provider-route",
		ServiceType:     "openai",
		AutoManaged:     true,
		AutoManagedKind: "generic",
		BaseURLs:        []string{"https://api.example.com/coding"},
		APIKeys:         []string{"sk-bound", "sk-unbound"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-bound", BaseURL: "https://plan.example.com/coding/v1"},
			{Key: "sk-unbound"},
		},
	}

	inventory := buildEndpointInventory(config.Config{ResponsesUpstream: []config.UpstreamConfig{channel}})
	if len(inventory.Entries) != 2 {
		t.Fatalf("entries=%d, want 2: %#v", len(inventory.Entries), inventory.Entries)
	}
	var entry channelEntry
	for _, candidate := range inventory.Entries {
		if got := candidate.APIKeys; reflect.DeepEqual(got, []string{"sk-bound", "sk-unbound"}) {
			entry = candidate
			break
		}
	}
	if entry.BaseURL == "" {
		t.Fatalf("missing key-bound URL inventory entry: %#v", inventory.Entries)
	}
	if len(inventory.EndpointUIDs) != 3 || len(inventory.ModelProfileBindings) != 3 {
		t.Fatalf("endpointUIDs=%d bindings=%d, want 3/3", len(inventory.EndpointUIDs), len(inventory.ModelProfileBindings))
	}
	metricsKey := computeMetricsIdentityKey(entry.BaseURL, "sk-bound", "openai")
	bindingKey := modelProfileBindingKey(channel.ChannelUID, "responses", metricsKey)
	if _, ok := inventory.ModelProfileBindings[bindingKey]; !ok {
		t.Fatalf("missing model binding for key-bound URL: %s", bindingKey)
	}
}
