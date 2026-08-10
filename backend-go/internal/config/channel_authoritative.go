package config

// channel_authoritative.go 提供渠道 v2 的**无损权威形态** ChannelV3 与六数组之间的双向投影。
//
// 与 ChannelView（有损读模型，仅 KeyMask）不同，ChannelV3 的每个协议成员携带完整
// UpstreamConfig，因此可无损地在「六个 Upstream 数组」与「按账号/站点聚合的渠道」之间往返。
// 这是 Phase 3「Channels 权威」的核心机制：先保证无损投影，再逐步把运行时消费者从
// 六数组切到 ChannelV3。数组删除是最后的机械步骤。
//
// 设计要点：
//   - 归组口径与 ChannelView 一致（channelViewAggregationKey），保证两套视图归组稳定一致。
//   - 每个成员记录原数组下标 Index，回投影时据此恢复各数组内的原始顺序（failover 优先级）。
//   - 不做任何字段裁剪，UpstreamConfig 整体复制，保证 round-trip 逐字节一致。

import (
	"log"
	"sort"
	"strings"
)

// ChannelV3SchemaVersion 是 ChannelsV3 权威形态的 schema 版本。
const ChannelV3SchemaVersion = 1

// ChannelV3 是同账号/同站点多协议物理渠道的无损聚合权威形态。
type ChannelV3 struct {
	ChannelUID   string                  `json:"channelUid"`
	AccountUID   string                  `json:"accountUid,omitempty"`
	ProviderID   string                  `json:"providerId,omitempty"`
	Name         string                  `json:"name,omitempty"`
	SiteIdentity string                  `json:"siteIdentity,omitempty"`
	Protocols    []ChannelProtocolMember `json:"protocols"`
}

// ChannelProtocolMember 是渠道下一个协议成员，携带完整 UpstreamConfig。
type ChannelProtocolMember struct {
	Kind     string         `json:"kind"`  // messages | chat | responses | gemini | images | vectors
	Index    int            `json:"index"` // 原 Upstream* 数组内下标，回投影时恢复顺序
	Upstream UpstreamConfig `json:"upstream"`
}

// BuildAuthoritativeChannels 把六个 Upstream 数组无损聚合为 ChannelV3 列表。
// 归组顺序：按各渠道首次出现顺序稳定输出（与 channelViewAggregationKey 一致）。
func BuildAuthoritativeChannels(cfg *Config) []ChannelV3 {
	if cfg == nil {
		return nil
	}
	byKey := make(map[string]*ChannelV3)
	order := make([]string, 0)
	for _, m := range collectPhysicalChannelsForView(cfg) {
		aggKey := channelViewAggregationKey(m.channel)
		ch := byKey[aggKey]
		if ch == nil {
			ch = &ChannelV3{
				ChannelUID:   authoritativeChannelUID(m.channel),
				AccountUID:   strings.TrimSpace(m.channel.AccountUID),
				ProviderID:   strings.TrimSpace(m.channel.ProviderID),
				Name:         authoritativeChannelName(m.channel),
				SiteIdentity: SiteIdentityForBaseURL(primaryBaseURLForView(m.channel)),
			}
			byKey[aggKey] = ch
			order = append(order, aggKey)
		}
		ch.Protocols = append(ch.Protocols, ChannelProtocolMember{
			Kind:     m.kind,
			Index:    m.index,
			Upstream: *m.channel,
		})
	}
	out := make([]ChannelV3, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

// ApplyAuthoritativeChannels 把 ChannelV3 列表无损回投影为六个 Upstream 数组，
// 并按成员 Index 恢复各数组内的原始顺序。
func ApplyAuthoritativeChannels(channels []ChannelV3) (upstream, chat, responses, gemini, images, vectors []UpstreamConfig) {
	type indexed struct {
		idx int
		u   UpstreamConfig
	}
	buckets := map[string]*[]indexed{
		"messages":  {},
		"chat":      {},
		"responses": {},
		"gemini":    {},
		"images":    {},
		"vectors":   {},
	}
	for _, ch := range channels {
		for _, m := range ch.Protocols {
			b, ok := buckets[m.Kind]
			if !ok {
				continue
			}
			*b = append(*b, indexed{idx: m.Index, u: m.Upstream})
		}
	}
	drain := func(kind string) []UpstreamConfig {
		b := buckets[kind]
		sort.SliceStable(*b, func(i, j int) bool { return (*b)[i].idx < (*b)[j].idx })
		out := make([]UpstreamConfig, 0, len(*b))
		for _, e := range *b {
			out = append(out, e.u)
		}
		return out
	}
	return drain("messages"), drain("chat"), drain("responses"), drain("gemini"), drain("images"), drain("vectors")
}

// authoritativeChannelUID 选聚合主键：优先 LogicalChannelUID，否则首个物理 ChannelUID。
func authoritativeChannelUID(u *UpstreamConfig) string {
	if uid := strings.TrimSpace(u.LogicalChannelUID); uid != "" {
		return uid
	}
	return u.ChannelUID
}

// authoritativeChannelName 取逻辑名或物理名。
func authoritativeChannelName(u *UpstreamConfig) string {
	if ln := strings.TrimSpace(u.LogicalName); ln != "" {
		return ln
	}
	return strings.TrimSpace(u.Name)
}

// reconcileAuthoritativeChannels 对账 ChannelsV3 与六数组是否一致（Phase 3b，非破坏）。
//
// 正常流程下 ChannelsV3 由 save 从同一份（已脱敏）六数组合成，二者按 ChannelUID 集合恒等。
// 若外部手改 config.json 只改了数组未改 ChannelsV3（或反之）导致集合不一致，仅打印告警并
// 信任六数组，绝不覆盖运行时数据。返回是否检测到分歧。
func reconcileAuthoritativeChannels(cfg *Config) bool {
	if cfg == nil || cfg.ChannelAuthoritativeVersion != ChannelV3SchemaVersion || len(cfg.ChannelsV3) == 0 {
		return false
	}
	fromV3 := channelUIDSetFromV3(cfg.ChannelsV3)
	fromArrays := channelUIDSetFromArrays(cfg)
	if len(fromV3) != len(fromArrays) {
		logChannelV3Divergence(len(fromArrays), len(fromV3))
		return true
	}
	for uid := range fromArrays {
		if _, ok := fromV3[uid]; !ok {
			logChannelV3Divergence(len(fromArrays), len(fromV3))
			return true
		}
	}
	return false
}

func channelUIDSetFromV3(channels []ChannelV3) map[string]struct{} {
	out := make(map[string]struct{})
	for _, ch := range channels {
		for _, m := range ch.Protocols {
			out[m.Kind+":"+m.Upstream.ChannelUID] = struct{}{}
		}
	}
	return out
}

func channelUIDSetFromArrays(cfg *Config) map[string]struct{} {
	out := make(map[string]struct{})
	for _, m := range collectPhysicalChannelsForView(cfg) {
		out[m.kind+":"+m.channel.ChannelUID] = struct{}{}
	}
	return out
}

func logChannelV3Divergence(arrays, v3 int) {
	log.Printf("[Channel-V3] 警告: ChannelsV3 权威形态与六数组不一致 (数组成员=%d, V3成员=%d)，本次信任六数组，将在下次保存时重建 ChannelsV3", arrays, v3)
}
