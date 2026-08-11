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
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

// ChannelV3SchemaVersion 是 ChannelsV3 权威形态的 schema 版本。
const ChannelV3SchemaVersion = 1

// channelAuthoritativeStrictEnv 是 Phase 3c 严格模式开关：
//   - 加载时若 ChannelsV3 重建出的六数组与磁盘六数组不一致（对账失败），
//     严格模式拒绝启动；非严格模式以 ChannelsV3 为权威覆盖（默认非严格）。
//
// 注意：CCX_CHANNEL_AUTHORITATIVE_LOAD 门控已于波 1（运行时权威反转）移除，
// ChannelsV3 存在时始终应用，LOAD 变量不再有任何效果。
const channelAuthoritativeStrictEnv = "CCX_CHANNEL_AUTHORITATIVE_STRICT"

// channelAuthoritativeStrictEnabled 读取严格模式开关。
func channelAuthoritativeStrictEnabled() bool {
	return isTruthyEnv(os.Getenv(channelAuthoritativeStrictEnv))
}

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

// applyAuthoritativeChannelsAsLoadSource 在加载阶段执行 Phase 3c 翻转：
//   - 若开关关闭，或配置不含有效 ChannelsV3，直接返回 false，保持六数组不变。
//   - 若开关开启，从 ChannelsV3 重建六数组，并与磁盘六数组逐字段比对。
//     完全一致则把重建结果写回 cfg 的六个数组，返回 true。
//     不一致时：严格模式返回 error 拒绝启动；非严格模式打印醒目告警并返回 false，
//     保持磁盘六数组不变。
func applyAuthoritativeChannelsAsLoadSource(cfg *Config) (applied bool, err error) {
	if cfg == nil {
		return false, nil
	}
	if cfg.ChannelAuthoritativeVersion != ChannelV3SchemaVersion || len(cfg.ChannelsV3) == 0 {
		return false, nil
	}

	rebuilt := ApplyAuthoritativeChannelsAsStruct(cfg.ChannelsV3)
	// 纯 V3 落盘格式（波 3 起六数组不再持久化）：磁盘六数组全空是格式特征而非偏差，
	// 跳过对账直接应用重建结果。仅当磁盘六数组非空（旧双写格式或被外部手改）才对账。
	diskArraysEmpty := len(cfg.Upstream) == 0 && len(cfg.ChatUpstream) == 0 && len(cfg.ResponsesUpstream) == 0 &&
		len(cfg.GeminiUpstream) == 0 && len(cfg.ImagesUpstream) == 0 && len(cfg.VectorsUpstream) == 0
	if !diskArraysEmpty {
		// 严格模式：对账失败拒绝启动（配置可能被外部手改坏）。
		if err := compareAuthoritativeRoundTrip(cfg, rebuilt); err != nil {
			if channelAuthoritativeStrictEnabled() {
				return false, fmt.Errorf("[Config-Load] ChannelsV3 与六数组不一致且严格模式开启，拒绝启动: %w", err)
			}
			// 非严格模式：ChannelsV3 已是权威来源，记录诊断后仍以 ChannelsV3 覆盖六数组。
			log.Printf("[Config-Load] 注意: ChannelsV3 与磁盘六数组有偏差，以 ChannelsV3 为权威覆盖: %v", err)
		}
	}

	cfg.Upstream, cfg.ChatUpstream, cfg.ResponsesUpstream, cfg.GeminiUpstream, cfg.ImagesUpstream, cfg.VectorsUpstream =
		rebuilt.Upstream, rebuilt.Chat, rebuilt.Responses, rebuilt.Gemini, rebuilt.Images, rebuilt.Vectors
	log.Printf("[Config-Load] 已从 ChannelsV3 重建六数组（渠道数: messages=%d chat=%d responses=%d gemini=%d images=%d vectors=%d）",
		len(cfg.Upstream), len(cfg.ChatUpstream), len(cfg.ResponsesUpstream),
		len(cfg.GeminiUpstream), len(cfg.ImagesUpstream), len(cfg.VectorsUpstream))
	return true, nil
}

// authoritativeArrays 是 ApplyAuthoritativeChannels 返回的六数组命名元组。
type authoritativeArrays struct {
	Upstream        []UpstreamConfig
	Chat            []UpstreamConfig
	Responses       []UpstreamConfig
	Gemini          []UpstreamConfig
	Images          []UpstreamConfig
	Vectors         []UpstreamConfig
}

// ApplyAuthoritativeChannelsAsStruct 与 ApplyAuthoritativeChannels 等价，但返回命名元组，便于比对。
func ApplyAuthoritativeChannelsAsStruct(channels []ChannelV3) authoritativeArrays {
	u, c, r, g, i, v := ApplyAuthoritativeChannels(channels)
	return authoritativeArrays{Upstream: u, Chat: c, Responses: r, Gemini: g, Images: i, Vectors: v}
}

// compareAuthoritativeRoundTrip 比对"从 ChannelsV3 重建的六数组"与 cfg 中直接读到的六数组
// 是否逐字段一致。返回不一致时的诊断信息，完全一致返回 nil。
func compareAuthoritativeRoundTrip(cfg *Config, rebuilt authoritativeArrays) error {
	// 规范化比对：加载时六数组经过完整迁移集（含 ensureAutoManagedKind 等会回填
	// AutoManagedKind/AutoManagedAt 的迁移），而落盘构建 ChannelsV3 的 persisted 副本只跑了
	// 部分迁移。直接逐字段比对会把这些"加载期才回填/含时间戳"的字段误判为不一致而误报回退。
	// 因此比对前对两边统一置空这些易变/加载期字段，只比对持久化权威字段。
	// 深拷贝磁盘与重建数组，避免污染运行时数据。
	want := Config{
		Upstream:          cloneUpstreamSlice(cfg.Upstream),
		ChatUpstream:      cloneUpstreamSlice(cfg.ChatUpstream),
		ResponsesUpstream: cloneUpstreamSlice(cfg.ResponsesUpstream),
		GeminiUpstream:    cloneUpstreamSlice(cfg.GeminiUpstream),
		ImagesUpstream:    cloneUpstreamSlice(cfg.ImagesUpstream),
		VectorsUpstream:   cloneUpstreamSlice(cfg.VectorsUpstream),
	}
	got := Config{
		Upstream:          cloneUpstreamSlice(rebuilt.Upstream),
		ChatUpstream:      cloneUpstreamSlice(rebuilt.Chat),
		ResponsesUpstream: cloneUpstreamSlice(rebuilt.Responses),
		GeminiUpstream:    cloneUpstreamSlice(rebuilt.Gemini),
		ImagesUpstream:    cloneUpstreamSlice(rebuilt.Images),
		VectorsUpstream:   cloneUpstreamSlice(rebuilt.Vectors),
	}
	stripeVolatileForAuthoritativeCompare(&want)
	stripeVolatileForAuthoritativeCompare(&got)

	pairs := []struct {
		kind string
		want []UpstreamConfig
		got  []UpstreamConfig
	}{
		{"messages", want.Upstream, got.Upstream},
		{"chat", want.ChatUpstream, got.ChatUpstream},
		{"responses", want.ResponsesUpstream, got.ResponsesUpstream},
		{"gemini", want.GeminiUpstream, got.GeminiUpstream},
		{"images", want.ImagesUpstream, got.ImagesUpstream},
		{"vectors", want.VectorsUpstream, got.VectorsUpstream},
	}
	for _, p := range pairs {
		if len(p.want) != len(p.got) {
			return fmt.Errorf("%s 数组长度不一致: 磁盘=%d 重建=%d", p.kind, len(p.want), len(p.got))
		}
		for i := range p.want {
			wb, _ := json.Marshal(p.want[i])
			gb, _ := json.Marshal(p.got[i])
			if !bytes.Equal(wb, gb) {
				return fmt.Errorf("%s[%d] 字段不一致\n磁盘:   %s\n重建: %s", p.kind, i, wb, gb)
			}
		}
	}
	return nil
}

// stripeVolatileForAuthoritativeCompare 置空比对时易变或加载期/重建期生成的字段。
// 这些字段由迁移或归组逻辑在运行时/重建时产出（多为随机 ID 或时间戳），persisted 与
// 重建结果天然不一致，不能作为权威一致性判据：
// - AutoManagedAt：ensureAutoManagedKind 每次回填用 time.Now()；
// - AutoManagedKind：persisted 未跑该迁移，加载期才回填；
// - AccountUID / CredentialUID：加载期 ensureAccountUIDs/ensureCredentialUIDs 随机生成；
// - LogicalChannelUID / LogicalName：RebuildLogicalChannels 归组时随机生成；
// - APIKeys / APIKeyConfigs[].Key：ChannelsV3 是脱敏权威形态（Key 只存 ManagedAccounts），
//   加载期 hydrate 会先给磁盘六数组补 Key 而重建侧恒为空，Key 差异本就无法对账。
// 只修改传入副本，不影响运行时数据。
func stripeVolatileForAuthoritativeCompare(cfg *Config) {
	strip := func(channels []UpstreamConfig) {
		for i := range channels {
			ch := &channels[i]
			ch.AutoManagedAt = nil
			ch.AutoManagedKind = ""
			ch.AccountUID = ""
			ch.LogicalChannelUID = ""
			ch.LogicalName = ""
			ch.APIKeys = nil
			for j := range ch.APIKeyConfigs {
				ch.APIKeyConfigs[j].CredentialUID = ""
				ch.APIKeyConfigs[j].Key = ""
			}
		}
	}
	strip(cfg.Upstream)
	strip(cfg.ResponsesUpstream)
	strip(cfg.GeminiUpstream)
	strip(cfg.ChatUpstream)
	strip(cfg.ImagesUpstream)
	strip(cfg.VectorsUpstream)
}

// cloneUpstreamSlice 深拷贝渠道切片用于比对规范化，避免污染原数组。
func cloneUpstreamSlice(in []UpstreamConfig) []UpstreamConfig {
	if in == nil {
		return nil
	}
	out := make([]UpstreamConfig, len(in))
	for i := range in {
		if c := in[i].Clone(); c != nil {
			out[i] = *c
		}
	}
	return out
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
