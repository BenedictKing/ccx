package autopilot

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// maybeEnableDiscoveredProtocolRoutes 将自定义自动托管账号已探测成功的协议转为真实渠道。
// 新渠道直接复用本轮 endpoint 证据写入画像，使发现完成后即可参与模型路由。
func (r *AutoDiscoveryRunner) maybeEnableDiscoveredProtocolRoutes(
	source *config.UpstreamConfig,
	endpoints []EndpointDiscoveryResult,
	cfgManager *config.ConfigManager,
) error {
	if r == nil || source == nil || cfgManager == nil || !source.AutoManaged || source.AccountUID == "" || source.ProviderID != "" {
		return nil
	}

	discovered := discoveredProtocolKinds(endpoints)
	if len(discovered) == 0 {
		return nil
	}
	channels := cfgManager.GetAccountChannels(source.AccountUID)
	if len(channels) == 0 {
		return fmt.Errorf("账号 %s 不存在", source.AccountUID)
	}
	present := make(map[string]bool, len(channels))
	for _, channel := range channels {
		if !channel.Upstream.AutoManaged || channel.Upstream.ProviderID != "" {
			return fmt.Errorf("账号 %s 包含非自定义自动托管渠道", source.AccountUID)
		}
		present[channel.Kind] = true
	}

	missing := make([]string, 0, len(discoverableProtocols))
	for _, protocol := range discoverableProtocols {
		if discovered[protocol] && !present[protocol] {
			missing = append(missing, protocol)
		}
	}
	baseName := managedCustomAccountName(channels[0])
	totalRoutes := len(present) + len(missing)
	routeSeed := source.Clone()
	if len(missing) > 0 {
		desiredKeys := make([]string, 0)
		for _, channel := range channels {
			desiredKeys = append(desiredKeys, channel.Upstream.APIKeys...)
		}
		updates, _, err := planCustomManagedAccountUpdates(source.AccountUID, updateAccountRequest{
			Name: baseName, APIKeys: uniqueNonEmptyStrings(desiredKeys),
		}, channels, totalRoutes)
		if err != nil {
			return fmt.Errorf("规划协议渠道更新失败: %w", err)
		}
		seedUpdated := false
		for _, update := range updates {
			if update.ChannelUID != source.ChannelUID {
				continue
			}
			routeSeed.APIKeys = append([]string(nil), update.APIKeys...)
			routeSeed.APIKeyConfigs = append([]config.APIKeyConfig(nil), update.APIKeyConfig...)
			routeSeed.BaseURLs = append([]string(nil), update.BaseURLs...)
			if len(routeSeed.BaseURLs) > 0 {
				routeSeed.BaseURL = routeSeed.BaseURLs[0]
			}
			seedUpdated = true
			break
		}
		if !seedUpdated {
			return fmt.Errorf("规划结果缺少源渠道 %s", source.ChannelUID)
		}

		additions := make([]config.AccountChannelAddition, 0, len(missing))
		for _, protocol := range missing {
			upstream := discoveredProtocolUpstream(routeSeed, protocol, baseName, totalRoutes > 1)
			additions = append(additions, config.AccountChannelAddition{Kind: protocol, Upstream: upstream})
		}
		if err := cfgManager.ApplyAccountChannelChanges(source.AccountUID, updates, additions); err != nil {
			return fmt.Errorf("补建协议渠道失败: %w", err)
		}

	}
	// Manager 的 active inventory 可能已经初始化；每次发现都按最新配置刷新，
	// 同时覆盖“配置已补建但上次画像写入中断”的重试场景。
	inventory := buildEndpointInventory(cfgManager.GetConfig())
	if r.store != nil {
		r.store.ReplaceActiveEndpointUIDs(inventory.EndpointUIDs)
	}
	if r.ModelProfileStore != nil {
		r.ModelProfileStore.ReplaceActiveBindings(inventory.ModelProfileBindings)
	}

	configured := cfgManager.GetAccountChannels(source.AccountUID)
	for _, channel := range configured {
		if !discovered[channel.Kind] {
			continue
		}
		protocolEndpoints := endpointResultsForProtocol(endpoints, channel.Kind)
		if len(protocolEndpoints) == 0 {
			return fmt.Errorf("%s 渠道缺少可用 endpoint 画像", channel.Kind)
		}
		r.writeProfiles(channel.Upstream.ChannelUID, &channel.Upstream, protocolEndpoints, cfgManager)
		if !present[channel.Kind] {
			log.Printf("[AutoDiscovery-RouteEnable] 已自动启用协议: account=%s kind=%s uid=%s endpoints=%d",
				source.AccountUID, channel.Kind, channel.Upstream.ChannelUID, len(protocolEndpoints))
		}
	}
	return nil
}

func discoveredProtocolKinds(endpoints []EndpointDiscoveryResult) map[string]bool {
	discovered := make(map[string]bool, len(discoverableProtocols))
	for _, endpoint := range endpoints {
		if !endpoint.ProtocolOk {
			continue
		}
		for protocol := range endpoint.ProtocolModels {
			if len(endpoint.ProtocolModels[protocol]) > 0 {
				discovered[protocol] = true
			}
		}
	}
	return discovered
}

func managedCustomAccountName(channel config.AccountChannel) string {
	return strings.TrimSuffix(channel.Upstream.Name, accountRouteSuffix(channel.Kind))
}

func discoveredProtocolUpstream(source *config.UpstreamConfig, protocol, baseName string, multiRoute bool) config.UpstreamConfig {
	cloned := source.Clone()
	now := time.Now()
	if cloned.AutoManagedAt != nil {
		now = *cloned.AutoManagedAt
	}
	seed := config.UpstreamConfig{
		BaseURL:                     cloned.BaseURL,
		BaseURLs:                    cloned.BaseURLs,
		APIKeys:                     cloned.APIKeys,
		APIKeyConfigs:               cloned.APIKeyConfigs,
		AuthHeader:                  cloned.AuthHeader,
		Description:                 cloned.Description,
		Website:                     cloned.Website,
		InsecureSkipVerify:          cloned.InsecureSkipVerify,
		Priority:                    cloned.Priority,
		PromotionUntil:              cloned.PromotionUntil,
		AutoBlacklistBalance:        cloned.AutoBlacklistBalance,
		NormalizeMetadataUserID:     cloned.NormalizeMetadataUserID,
		CustomHeaders:               cloned.CustomHeaders,
		ProxyURL:                    cloned.ProxyURL,
		RequestTimeoutMs:            cloned.RequestTimeoutMs,
		ResponseHeaderTimeoutMs:     cloned.ResponseHeaderTimeoutMs,
		StreamFirstContentTimeoutMs: cloned.StreamFirstContentTimeoutMs,
		StreamInactivityTimeoutMs:   cloned.StreamInactivityTimeoutMs,
		StreamToolCallIdleTimeoutMs: cloned.StreamToolCallIdleTimeoutMs,
		RoutePrefix:                 cloned.RoutePrefix,
		RateLimitRPM:                cloned.RateLimitRPM,
		RateLimitWindowMinutes:      cloned.RateLimitWindowMinutes,
		RateLimitBurst:              cloned.RateLimitBurst,
		RateLimitMaxConcurrent:      cloned.RateLimitMaxConcurrent,
		RateLimitAutoFromHeaders:    cloned.RateLimitAutoFromHeaders,
		OriginType:                  cloned.OriginType,
		OriginTier:                  cloned.OriginTier,
		Tags:                        cloned.Tags,
		HealthCheck:                 cloned.HealthCheck,
	}
	return buildCustomManagedProtocolRoute(seed, cloned.AccountUID, baseName, protocol, multiRoute, now)
}

func endpointResultsForProtocol(endpoints []EndpointDiscoveryResult, protocol string) []EndpointDiscoveryResult {
	results := make([]EndpointDiscoveryResult, 0, len(endpoints))
	for _, endpoint := range endpoints {
		models, ok := endpoint.ProtocolModels[protocol]
		if !endpoint.ProtocolOk || !ok || len(models) == 0 {
			continue
		}
		result := endpoint
		result.Models = append([]string(nil), models...)
		result.ModelsCount = len(models)
		result.ErrorMessage = ""
		result.ModelDiscoverySource = endpoint.ProtocolDiscoverySource[protocol]
		result.ModelDiscoveryMessage = endpoint.ProtocolDiscoveryMessage[protocol]
		if discoveredAt, exists := endpoint.ProtocolDiscoveredAt[protocol]; exists {
			value := discoveredAt.UTC()
			result.ModelsDiscoveredAt = &value
		}
		results = append(results, result)
	}
	return results
}
