package config

import "github.com/google/uuid"

func newChannelUID() string { return uuid.NewString() }

// normalizeChannelUIDs 只补齐观测身份，不改变渠道排序或调度策略。
func normalizeChannelUIDs(cfg *Config) bool {
	seen := map[string]bool{}
	changed := false
	for _, channels := range [][]UpstreamConfig{cfg.Upstream, cfg.ChatUpstream, cfg.ResponsesUpstream, cfg.GeminiUpstream, cfg.ImagesUpstream, cfg.VectorsUpstream} {
		for i := range channels {
			if channels[i].ChannelUID == "" || seen[channels[i].ChannelUID] {
				channels[i].ChannelUID = newChannelUID()
				changed = true
			}
			seen[channels[i].ChannelUID] = true
		}
	}
	return changed
}
