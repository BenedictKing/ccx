package autopilot

import (
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// channelKindToAPIType 把渠道 kind 字符串映射为 limiter key 前缀。
// 覆盖六类正式渠道（messages/responses/gemini/chat/images/vectors）；
// 未知 kind 返回空字符串，调用方应跳过，不能默认映射到 Messages。
func channelKindToAPIType(kind string) string {
	switch kind {
	case "messages":
		return "Messages"
	case "responses":
		return "Responses"
	case "gemini":
		return "Gemini"
	case "chat":
		return "Chat"
	case "images":
		return "Images"
	case "vectors":
		return "Vectors"
	default:
		return ""
	}
}

// channelEntry 是 L1 画像遍历使用的真实 endpoint 绑定。
// 同一渠道的不同 BaseURL 会拆成独立 entry；绑定了 BaseURL 的 Key 只出现在对应 entry。
type channelEntry struct {
	ChannelUID  string
	ChannelID   int
	ChannelKind string
	ServiceType string
	BaseURL     string
	APIKeys     []string
	OriginType  string
	OriginTier  string
}

type endpointInventory struct {
	Entries              []channelEntry
	EndpointUIDs         map[string]struct{}
	ModelProfileBindings map[string]struct{}
}

// buildEndpointInventory 从当前配置构建与真实 failover 一致的 endpoint 清单。
// 未绑定 Key 与所有 BaseURL 组合；绑定 Key 只与其指定 BaseURL 组合。
func buildEndpointInventory(cfg config.Config) endpointInventory {
	inventory := endpointInventory{
		EndpointUIDs:         make(map[string]struct{}),
		ModelProfileBindings: make(map[string]struct{}),
	}

	type upstreamList struct {
		channels    []config.UpstreamConfig
		channelKind string
	}
	lists := []upstreamList{
		{cfg.Upstream, "messages"},
		{cfg.ResponsesUpstream, "responses"},
		{cfg.GeminiUpstream, "gemini"},
		{cfg.ChatUpstream, "chat"},
		{cfg.ImagesUpstream, "images"},
		{cfg.VectorsUpstream, "vectors"},
	}

	for _, list := range lists {
		for channelID := range list.channels {
			channel := list.channels[channelID]
			runtimeChannel := config.RuntimeUpstreamForAutoManagedProvider(&channel)
			if runtimeChannel == nil || strings.TrimSpace(runtimeChannel.ChannelUID) == "" {
				continue
			}

			baseURLs := runtimeChannel.GetAllBaseURLs()
			if runtimeChannel.AutoManaged {
				for _, keyCfg := range runtimeChannel.APIKeyConfigs {
					if boundBaseURL := runtimeChannel.BoundBaseURLForKey(keyCfg.Key); boundBaseURL != "" {
						baseURLs = append(baseURLs, boundBaseURL)
					}
				}
				baseURLs = deduplicateInventoryBaseURLs(baseURLs)
			}
			if len(baseURLs) == 0 || len(runtimeChannel.APIKeys) == 0 {
				continue
			}
			serviceType := scheduler.NormalizedMetricsServiceType(
				scheduler.ChannelKind(list.channelKind),
				runtimeChannel.ServiceType,
			)

			for _, baseURL := range baseURLs {
				baseURL = strings.TrimSpace(baseURL)
				if baseURL == "" {
					continue
				}

				keys := make([]string, 0, len(runtimeChannel.APIKeys))
				seenKeys := make(map[string]struct{}, len(runtimeChannel.APIKeys))
				for _, rawKey := range runtimeChannel.APIKeys {
					apiKey := strings.TrimSpace(rawKey)
					if apiKey == "" {
						continue
					}
					if _, seen := seenKeys[apiKey]; seen {
						continue
					}
					boundBaseURL := runtimeChannel.BoundBaseURLForKey(apiKey)
					if boundBaseURL != "" && boundBaseURL != baseURL {
						continue
					}

					seenKeys[apiKey] = struct{}{}
					keys = append(keys, apiKey)
					endpointUID := GenerateEndpointUID(runtimeChannel.ChannelUID, baseURL, KeyHashFromAPIKey(apiKey))
					inventory.EndpointUIDs[endpointUID] = struct{}{}
					metricsKey := computeMetricsIdentityKey(baseURL, apiKey, serviceType)
					bindingKey := modelProfileBindingKey(runtimeChannel.ChannelUID, list.channelKind, metricsKey)
					inventory.ModelProfileBindings[bindingKey] = struct{}{}
				}
				if len(keys) == 0 {
					continue
				}

				inventory.Entries = append(inventory.Entries, channelEntry{
					ChannelUID:  runtimeChannel.ChannelUID,
					ChannelID:   channelID,
					ChannelKind: list.channelKind,
					ServiceType: serviceType,
					BaseURL:     baseURL,
					APIKeys:     keys,
					OriginType:  runtimeChannel.OriginType,
					OriginTier:  runtimeChannel.OriginTier,
				})
			}
		}
	}

	return inventory
}

func deduplicateInventoryBaseURLs(baseURLs []string) []string {
	result := make([]string, 0, len(baseURLs))
	seen := make(map[string]struct{}, len(baseURLs))
	for _, baseURL := range baseURLs {
		baseURL = strings.TrimSpace(baseURL)
		if baseURL == "" {
			continue
		}
		if _, ok := seen[baseURL]; ok {
			continue
		}
		seen[baseURL] = struct{}{}
		result = append(result, baseURL)
	}
	return result
}
