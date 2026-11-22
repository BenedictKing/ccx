package handlers

import (
	"sort"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
)

type routingHistoryKeyOption struct {
	KeyID   string `json:"keyId"`
	KeyMask string `json:"keyMask"`
}

func routingHistoryKeyOptions(apiType, channelUID string, requests []metrics.RoutingRequest, managers []*config.ConfigManager) []routingHistoryKeyOption {
	byID := map[string]string{}
	if len(managers) > 0 && managers[0] != nil {
		cfg := managers[0].GetConfig()
		channels := map[string][]config.UpstreamConfig{
			"messages": cfg.Upstream, "chat": cfg.ChatUpstream, "responses": cfg.ResponsesUpstream,
			"gemini": cfg.GeminiUpstream, "images": cfg.ImagesUpstream, "vectors": cfg.VectorsUpstream,
		}[apiType]
		for _, channel := range channels {
			if channelUID != "" && channel.ChannelUID != channelUID {
				continue
			}
			for _, key := range channelStatsAPIKeys(channel) {
				if key != "" {
					byID[metrics.RoutingKeyID(key)] = utils.MaskAPIKey(key)
				}
			}
		}
	}
	// 被删除渠道的历史 Key 仍可从返回的尝试选择，不依赖当前配置。
	for _, request := range requests {
		for _, attempt := range request.Attempts {
			if attempt.KeyID != "" && (channelUID == "" || channelUID == attempt.ChannelUID) {
				byID[attempt.KeyID] = attempt.KeyMask
			}
		}
	}
	options := make([]routingHistoryKeyOption, 0, len(byID))
	for id, mask := range byID {
		options = append(options, routingHistoryKeyOption{KeyID: id, KeyMask: mask})
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].KeyMask == options[j].KeyMask {
			return options[i].KeyID < options[j].KeyID
		}
		return options[i].KeyMask < options[j].KeyMask
	})
	return options
}
