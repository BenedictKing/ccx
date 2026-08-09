package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// 逻辑渠道 schema 版本：首次引入为 1。变更字段时 bump，并在加载逻辑里处理兼容。
const LogicalChannelSchemaVersion = 1

// LogicalChannelKind 逻辑渠道的协议家族分类。
type LogicalChannelKind string

const (
	LogicalChannelKindLLM        LogicalChannelKind = "llm"
	LogicalChannelKindEmbeddings LogicalChannelKind = "embeddings"
	LogicalChannelKindImages     LogicalChannelKind = "images"
)

// LogicalChannelProtocol 逻辑渠道下单个协议物理路由的引用。
type LogicalChannelProtocol struct {
	Kind        string `json:"kind"`        // messages / chat / responses / gemini / images / vectors
	ChannelUID  string `json:"channelUid"`  // 物理 UpstreamConfig.ChannelUID
	ServiceType string `json:"serviceType"` // claude / openai / responses / gemini
	Enabled     bool   `json:"enabled"`     // 用户可见启停；与 UpstreamConfig.Status 同步
	Status      string `json:"status"`      // 运行时状态（active / suspended / disabled）
	Priority    int    `json:"priority"`    // 与物理 route 同步
	RoutePrefix string `json:"routePrefix"` // 物理 route 的 RoutePrefix（仅展示）
}

// LogicalChannel 是管理面聚合的逻辑渠道视图。
// 用户的产品语义是“同一个站点的多协议能力只看作一张渠道卡片”；运行时仍以六类 Upstream*
// 数组为权威存储，本结构体是稳定身份与跨协议视图。
type LogicalChannel struct {
	LogicalChannelUID string                   `json:"logicalChannelUid"`    // 稳定 ULID
	AccountUID        string                   `json:"accountUid,omitempty"` // 可选：自动托管账号身份
	ProviderID        string                   `json:"providerId,omitempty"` // 可选：来源 provider 模板 ID
	Name              string                   `json:"name"`                 // 用户可见名称
	Description       string                   `json:"description,omitempty"`
	Website           string                   `json:"website,omitempty"`
	Kind              LogicalChannelKind       `json:"kind"`         // llm / embeddings / images
	BaseURLs          []string                 `json:"baseUrls"`     // 站点地址池（归一化后）
	SiteIdentity      string                   `json:"siteIdentity"` // 主 URL 的归一化站点身份
	Protocols         []LogicalChannelProtocol `json:"protocols"`    // 多协议物理路由
	Tags              []string                 `json:"tags,omitempty"`
	CreatedAt         time.Time                `json:"createdAt"`
	UpdatedAt         time.Time                `json:"updatedAt"`
}

// logicalChannelGroupKey 归组键。
type logicalChannelGroupKey struct {
	accountUID  string
	providerID  string
	siteIdent   string
	hasAccount  bool
	hasProvider bool
}

func logicalChannelGroupKeyFrom(ch UpstreamConfig) logicalChannelGroupKey {
	k := logicalChannelGroupKey{
		accountUID:  strings.TrimSpace(ch.AccountUID),
		providerID:  strings.TrimSpace(ch.ProviderID),
		hasAccount:  strings.TrimSpace(ch.AccountUID) != "",
		hasProvider: strings.TrimSpace(ch.ProviderID) != "",
	}
	if base := primaryBaseURLForSiteIdentity(ch); base != "" {
		if idents := utils.BaseURLSiteIdentities(base); len(idents) > 0 {
			k.siteIdent = idents[0]
		}
	}
	return k
}

func primaryBaseURLForSiteIdentity(ch UpstreamConfig) string {
	if len(ch.BaseURLs) > 0 {
		for _, u := range ch.BaseURLs {
			if strings.TrimSpace(u) != "" {
				return u
			}
		}
	}
	return ch.BaseURL
}

// shouldGroupLogical 判断两个 key 是否可归为同一逻辑渠道。
func shouldGroupLogical(a, b logicalChannelGroupKey) bool {
	// 1) 同一托管账号
	if a.hasAccount && b.hasAccount && a.accountUID == b.accountUID {
		return true
	}
	// 站点身份必须一致
	if a.siteIdent == "" || b.siteIdent == "" || a.siteIdent != b.siteIdent {
		return false
	}
	// 2) 同一 provider
	if a.hasProvider && b.hasProvider && a.providerID == b.providerID {
		return true
	}
	// 3) 手工渠道（无 provider / 无 account）+ 同站点
	if !a.hasProvider && !b.hasProvider && !a.hasAccount && !b.hasAccount {
		return true
	}
	return false
}

// generateLogicalChannelUID 生成稳定唯一 ID（22 字符 ULID-like hex）。
func generateLogicalChannelUID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "lc_" + fmt.Sprintf("%012x", time.Now().UnixNano()&0xffffffffffff)
	}
	return "lc_" + hex.EncodeToString(b[:])
}

// GenerateLogicalChannelUID 暴露给外部包使用的稳定 ID 生成器。
func GenerateLogicalChannelUID() string { return generateLogicalChannelUID() }

// physicalChannelEntry 在归组时记录物理渠道所在的物理数组名（"messages"/"chat"/...）
// 与原 channel 本身的元组，区别于 serviceType → kind 的推断。
type physicalChannelEntry struct {
	slice   string
	channel UpstreamConfig
}

// RebuildLogicalChannels 从当前 Config 重新构建 LogicalChannels 列表与每个 UpstreamConfig 的
// LogicalChannelUID / LogicalName 字段。锁内调用，纯函数转换，不写盘。
// 归组规则：
//  1. 同 accountUID 合并（保留现有托管账号语义）。
//  2. 同 providerID + 同 siteIdentity 合并。
//  3. 手工渠道（无 provider / 无 account）+ 同 siteIdentity 合并。
//  4. 不同 account / 不同 provider / 不同 siteIdentity 不合并。
//  5. 已有 LogicalChannelUID 优先（用户已通过 API 创建的），不会被打散。
func RebuildLogicalChannels(cfg *Config) {
	if cfg == nil {
		return
	}
	// 1) 收集全部物理渠道（带 slice 名）
	all := collectAllPhysicalChannelsWithSlice(cfg)
	if len(all) == 0 && len(cfg.LogicalChannels) == 0 {
		return
	}
	// 2) 已有 logical 按 UID 索引（先复制为副本，existingByUID 与 logicals 指向同一批副本，
	// 保证第 4 步的 append 与第 7 步的物化作用于同一对象）。
	logicals := make([]*LogicalChannel, 0, len(cfg.LogicalChannels))
	existingByUID := make(map[string]*LogicalChannel, len(cfg.LogicalChannels))
	usedUIDs := make(map[string]struct{}, len(cfg.LogicalChannels))
	for i := range cfg.LogicalChannels {
		if cfg.LogicalChannels[i].LogicalChannelUID == "" {
			continue
		}
		copy := cfg.LogicalChannels[i]
		// 重置 protocols：第 4 步与 4.5 步按当前物理渠道重建，避免陈旧引用残留。
		copy.Protocols = nil
		logicals = append(logicals, &copy)
		existingByUID[copy.LogicalChannelUID] = &copy
		usedUIDs[copy.LogicalChannelUID] = struct{}{}
	}
	// 4) 按已回填的 LogicalChannelUID 把物理渠道归入对应 logical（protocols 重建）。
	// 但物理渠道的 LogicalChannelUID 只能作为候选：若其字段值对应的 logical 仍在
	// existingByUID 中，则追加 protocol；否则视为未分组，进入第 5 步按最新归组键重建。
	// 这保证物理渠道字段变更（providerID、site 等）后，旧的 UID 不会被固执沿用。
	for _, e := range all {
		uid := strings.TrimSpace(e.channel.LogicalChannelUID)
		if uid == "" {
			continue
		}
		if l, ok := existingByUID[uid]; ok {
			appendProtocolToLogical(l, e.slice, e.channel)
		}
	}
	// 4.5) 同一托管账号强制收敛到单一逻辑卡。
	// 历史一次性缺陷曾给同账号的不同协议渠道分别回填了不同的 LogicalChannelUID，
	// 而 attachLogicalToSlicesByIndex 仅在 UID 为空时回填，导致分歧被永久写死、
	// 后续 Rebuild 在第 4 步按旧 UID 各归各，第 5 步的账号合并永远轮不到它们。
	// 这里以 accountUid 为身份真相：把同账号的物理渠道重新归并到一张 canonical 卡，
	// 并强制重指物理渠道的 LogicalChannelUID，孤儿卡在物化时随之消失。
	convergeLogicalByAccount(cfg, all, logicals)
	// 5) 剩余按归组键合并
	groups := make(map[logicalChannelGroupKey][]physicalChannelEntry)
	groupOrder := make([]logicalChannelGroupKey, 0)
	for _, e := range all {
		uid := strings.TrimSpace(e.channel.LogicalChannelUID)
		if uid != "" {
			// 已按旧 UID 归入 existing logical 的，检查其归组键是否仍匹配。
			// 若该 physical 的 (provider/site/account) 已不再属于该 logical，
			// 则把它从原 logical 移出，重新归组。
			if l, ok := existingByUID[uid]; ok {
				lk := logicalChannelGroupKeyFromPhysical(l)
				ek := logicalChannelGroupKeyFrom(e.channel)
				if shouldGroupLogical(lk, ek) {
					continue
				}
				removeProtocolFromLogical(l, e.slice)
			}
		}
		k := logicalChannelGroupKeyFrom(e.channel)
		if _, ok := groups[k]; !ok {
			groupOrder = append(groupOrder, k)
		}
		groups[k] = append(groups[k], e)
	}
	for _, k := range groupOrder {
		members := groups[k]
		if len(members) == 0 {
			continue
		}
		lc := &LogicalChannel{
			LogicalChannelUID: pickFreshUID(usedUIDs),
			AccountUID:        k.accountUID,
			ProviderID:        k.providerID,
			BaseURLs:          collectAllBaseURLsFromEntries(members),
			SiteIdentity:      k.siteIdent,
			Kind:              inferLogicalKindFromEntries(members),
			Protocols:         collectProtocolsFromEntries(members),
			CreatedAt:         inferEarliestCreated(members),
			UpdatedAt:         time.Now().UTC(),
		}
		if name := inferLogicalDisplayName(members); name != "" {
			lc.Name = name
		} else {
			lc.Name = deriveLogicalName(k, members[0].channel.BaseURL)
		}
		// 写回物理渠道字段：强制刷新，确保物理字段与最新 logical 视图一致
		for _, m := range members {
			if up := findChannelInSlices(cfg, m.slice, m.channel.ChannelUID); up != nil {
				up.LogicalChannelUID = lc.LogicalChannelUID
				up.LogicalName = lc.Name
			}
		}
		logicals = append(logicals, lc)
	}
	// 6) protocols 列表去重
	for _, l := range logicals {
		l.Protocols = dedupProtocols(l.Protocols)
	}
	// 7) 物化；跳过被 4.5 步清空 protocols 的孤儿卡（其渠道已并入 canonical 卡）
	out := make([]LogicalChannel, 0, len(logicals))
	for _, l := range logicals {
		if len(l.Protocols) == 0 {
			continue
		}
		out = append(out, *l)
	}
	cfg.LogicalChannels = out
	cfg.LogicalChannelSchemaVersion = LogicalChannelSchemaVersion
}

// collectAllPhysicalChannelsWithSlice 汇总六类数组。
func collectAllPhysicalChannelsWithSlice(cfg *Config) []physicalChannelEntry {
	out := make([]physicalChannelEntry, 0,
		len(cfg.Upstream)+len(cfg.ChatUpstream)+len(cfg.ResponsesUpstream)+
			len(cfg.GeminiUpstream)+len(cfg.ImagesUpstream)+len(cfg.VectorsUpstream))
	for i := range cfg.Upstream {
		out = append(out, physicalChannelEntry{slice: "messages", channel: cfg.Upstream[i]})
	}
	for i := range cfg.ChatUpstream {
		out = append(out, physicalChannelEntry{slice: "chat", channel: cfg.ChatUpstream[i]})
	}
	for i := range cfg.ResponsesUpstream {
		out = append(out, physicalChannelEntry{slice: "responses", channel: cfg.ResponsesUpstream[i]})
	}
	for i := range cfg.GeminiUpstream {
		out = append(out, physicalChannelEntry{slice: "gemini", channel: cfg.GeminiUpstream[i]})
	}
	for i := range cfg.ImagesUpstream {
		out = append(out, physicalChannelEntry{slice: "images", channel: cfg.ImagesUpstream[i]})
	}
	for i := range cfg.VectorsUpstream {
		out = append(out, physicalChannelEntry{slice: "vectors", channel: cfg.VectorsUpstream[i]})
	}
	return out
}

// collectProtocolsFromEntries 将一组物理渠道投影为 LogicalChannelProtocol 列表。
func collectProtocolsFromEntries(members []physicalChannelEntry) []LogicalChannelProtocol {
	byKind := make(map[string]physicalChannelEntry, len(members))
	for _, e := range members {
		byKind[e.slice] = e
	}
	order := []string{"messages", "chat", "responses", "gemini", "images", "vectors"}
	out := make([]LogicalChannelProtocol, 0, len(byKind))
	seen := make(map[string]struct{}, len(byKind))
	for _, kind := range order {
		e, ok := byKind[kind]
		if !ok {
			continue
		}
		enabled := strings.EqualFold(strings.TrimSpace(e.channel.Status), "active")
		out = append(out, LogicalChannelProtocol{
			Kind:        kind,
			ChannelUID:  e.channel.ChannelUID,
			ServiceType: e.channel.ServiceType,
			Enabled:     enabled,
			Status:      strings.TrimSpace(e.channel.Status),
			Priority:    e.channel.Priority,
			RoutePrefix: e.channel.RoutePrefix,
		})
		seen[kind] = struct{}{}
	}
	// 兼容历史未知 kind（按 serviceType 推断的）
	keys := make([]string, 0, len(byKind))
	for k := range byKind {
		if _, ok := seen[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, kind := range keys {
		e := byKind[kind]
		enabled := strings.EqualFold(strings.TrimSpace(e.channel.Status), "active")
		out = append(out, LogicalChannelProtocol{
			Kind:        kind,
			ChannelUID:  e.channel.ChannelUID,
			ServiceType: e.channel.ServiceType,
			Enabled:     enabled,
			Status:      strings.TrimSpace(e.channel.Status),
			Priority:    e.channel.Priority,
			RoutePrefix: e.channel.RoutePrefix,
		})
	}
	return out
}

// collectAllBaseURLsFromEntries 收集 baseURL（去重，保留顺序）。
func collectAllBaseURLsFromEntries(members []physicalChannelEntry) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(members))
	for _, e := range members {
		for _, u := range e.channel.GetAllBaseURLs() {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			if _, ok := seen[u]; ok {
				continue
			}
			seen[u] = struct{}{}
			out = append(out, u)
		}
	}
	return out
}

// inferLogicalKindFromEntries 根据组内渠道所在物理数组名推断逻辑渠道类型。
// images/vectors 单数组专属；其余一律 llm（与文本生成 API 同源）。
func inferLogicalKindFromEntries(members []physicalChannelEntry) LogicalChannelKind {
	hasImages, hasVectors := false, false
	for _, e := range members {
		switch e.slice {
		case "images":
			hasImages = true
		case "vectors":
			hasVectors = true
		}
	}
	switch {
	case hasImages && !hasVectors:
		return LogicalChannelKindImages
	case hasVectors && !hasImages:
		return LogicalChannelKindEmbeddings
	default:
		// 混合（如 images+llm，或仅 llm）归为 llm；如未来需更细粒度可加判断
		return LogicalChannelKindLLM
	}
}

// inferLogicalDisplayName 从组内渠道抽取用户可见名：优先非空 Name，否则返回空。
func inferLogicalDisplayName(members []physicalChannelEntry) string {
	for _, e := range members {
		name := strings.TrimSpace(e.channel.Name)
		if name == "" {
			continue
		}
		if !looksAutoDerived(name) {
			return name
		}
	}
	return ""
}

// looksAutoDerived 粗略判断渠道名是否是 "<url> - kind" 自动派生格式。
func looksAutoDerived(name string) bool {
	for _, suf := range []string{" - chat", " - codex", " - gemini", " - claude", " - responses"} {
		if strings.HasSuffix(name, suf) {
			return true
		}
	}
	return false
}

// inferEarliestCreated 选取最早创建时间（不可知时给当前时间）。
func inferEarliestCreated(members []physicalChannelEntry) time.Time {
	var earliest time.Time
	for _, e := range members {
		if e.channel.AutoManagedAt != nil {
			if earliest.IsZero() || e.channel.AutoManagedAt.Before(earliest) {
				earliest = *e.channel.AutoManagedAt
			}
		}
	}
	if earliest.IsZero() {
		earliest = time.Now().UTC()
	}
	return earliest
}

// appendProtocolToLogical 把单条物理渠道挂到 logical.Protocols（同 slice 覆盖）。
func appendProtocolToLogical(l *LogicalChannel, slice string, ch UpstreamConfig) {
	enabled := strings.EqualFold(strings.TrimSpace(ch.Status), "active")
	entry := LogicalChannelProtocol{
		Kind:        slice,
		ChannelUID:  ch.ChannelUID,
		ServiceType: ch.ServiceType,
		Enabled:     enabled,
		Status:      strings.TrimSpace(ch.Status),
		Priority:    ch.Priority,
		RoutePrefix: ch.RoutePrefix,
	}
	for i := range l.Protocols {
		if l.Protocols[i].Kind == slice {
			l.Protocols[i] = entry
			return
		}
	}
	l.Protocols = append(l.Protocols, entry)
}

// pickFreshUID 在 usedUIDs 中找一个未占用的新 UID。
func pickFreshUID(used map[string]struct{}) string {
	for i := 0; i < 16; i++ {
		uid := generateLogicalChannelUID()
		if _, ok := used[uid]; !ok {
			used[uid] = struct{}{}
			return uid
		}
	}
	uid := generateLogicalChannelUID()
	used[uid] = struct{}{}
	return uid
}

// dedupProtocols 去重并保持顺序（按 Kind）。
func dedupProtocols(in []LogicalChannelProtocol) []LogicalChannelProtocol {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]int, len(in))
	out := make([]LogicalChannelProtocol, 0, len(in))
	for _, p := range in {
		idx, ok := seen[p.Kind]
		if !ok {
			seen[p.Kind] = len(out)
			out = append(out, p)
			continue
		}
		out[idx] = p
	}
	return out
}

// attachLogicalToEntries 是 members 副本上的写入器，目前改用 attachLogicalToSlicesByIndex 直接写原 slice。
// 保留以备未来非切片入口（如测试）。
func attachLogicalToEntries(l *LogicalChannel, members []physicalChannelEntry) {
	for i := range members {
		ch := &members[i].channel
		if ch.LogicalChannelUID == "" {
			ch.LogicalChannelUID = l.LogicalChannelUID
		}
		if ch.LogicalName == "" {
			ch.LogicalName = l.Name
		}
	}
}

// convergeLogicalByAccount 把同一托管账号的物理渠道收敛到单一 canonical 逻辑卡。
// 遍历顺序即六类数组的固定顺序（messages → chat → …），因此同账号首个遇到的
// logical 即 canonical，幂等稳定；该账号其余 logical 被清空 protocols（物化时剔除）。
// 物理渠道的 LogicalChannelUID / LogicalName 被强制重指到 canonical，解除历史写死的分歧。
func convergeLogicalByAccount(cfg *Config, all []physicalChannelEntry, logicals []*LogicalChannel) {
	byUID := make(map[string]*LogicalChannel, len(logicals))
	for _, l := range logicals {
		byUID[l.LogicalChannelUID] = l
	}
	canonicalByAccount := make(map[string]*LogicalChannel)
	for _, e := range all {
		acc := strings.TrimSpace(e.channel.AccountUID)
		if acc == "" {
			continue
		}
		uid := strings.TrimSpace(e.channel.LogicalChannelUID)
		l := byUID[uid]
		if l == nil {
			continue
		}
		canonical, ok := canonicalByAccount[acc]
		if !ok {
			canonicalByAccount[acc] = l
			continue
		}
		if l == canonical {
			continue
		}
		// 把 e 从 l 移到 canonical，并强制重指物理渠道身份
		appendProtocolToLogical(canonical, e.slice, e.channel)
		removeProtocolFromLogical(l, e.slice)
		if up := findChannelInSlices(cfg, e.slice, e.channel.ChannelUID); up != nil {
			up.LogicalChannelUID = canonical.LogicalChannelUID
			up.LogicalName = canonical.Name
		}
	}
}

// removeProtocolFromLogical 移除 logical 中指定 kind 的协议引用。
func removeProtocolFromLogical(l *LogicalChannel, slice string) {
	for i := range l.Protocols {
		if l.Protocols[i].Kind == slice {
			l.Protocols = append(l.Protocols[:i], l.Protocols[i+1:]...)
			return
		}
	}
}

// attachLogicalToSlicesByIndex 通过原始 slice + index 把 logical UID/Name 写回原 UpstreamConfig 元素。
// 这是 RebuildLogicalChannels 写回物理渠道字段的唯一入口，避免副本写入丢失。
func attachLogicalToSlicesByIndex(cfg *Config, l *LogicalChannel, members []physicalChannelEntry) {
	for _, m := range members {
		up := findChannelInSlices(cfg, m.slice, m.channel.ChannelUID)
		if up == nil {
			continue
		}
		if up.LogicalChannelUID == "" {
			up.LogicalChannelUID = l.LogicalChannelUID
		}
		if up.LogicalName == "" {
			up.LogicalName = l.Name
		}
	}
}

// findChannelInSlices 在 cfg 六个数组中按 kind + ChannelUID 找到原始元素指针。
func findChannelInSlices(cfg *Config, kind, channelUID string) *UpstreamConfig {
	if channelUID == "" {
		return nil
	}
	switch kind {
	case "messages":
		for i := range cfg.Upstream {
			if cfg.Upstream[i].ChannelUID == channelUID {
				return &cfg.Upstream[i]
			}
		}
	case "chat":
		for i := range cfg.ChatUpstream {
			if cfg.ChatUpstream[i].ChannelUID == channelUID {
				return &cfg.ChatUpstream[i]
			}
		}
	case "responses":
		for i := range cfg.ResponsesUpstream {
			if cfg.ResponsesUpstream[i].ChannelUID == channelUID {
				return &cfg.ResponsesUpstream[i]
			}
		}
	case "gemini":
		for i := range cfg.GeminiUpstream {
			if cfg.GeminiUpstream[i].ChannelUID == channelUID {
				return &cfg.GeminiUpstream[i]
			}
		}
	case "images":
		for i := range cfg.ImagesUpstream {
			if cfg.ImagesUpstream[i].ChannelUID == channelUID {
				return &cfg.ImagesUpstream[i]
			}
		}
	case "vectors":
		for i := range cfg.VectorsUpstream {
			if cfg.VectorsUpstream[i].ChannelUID == channelUID {
				return &cfg.VectorsUpstream[i]
			}
		}
	}
	return nil
}

// deriveLogicalName 在缺失名字时根据主 URL 与 provider 生成一个稳定展示名。
func deriveLogicalName(groupKey logicalChannelGroupKey, baseURL string) string {
	if baseURL == "" {
		if groupKey.hasProvider {
			return groupKey.providerID
		}
		if groupKey.hasAccount {
			return groupKey.accountUID
		}
		return "未命名渠道"
	}
	host := baseURL
	if idx := strings.Index(baseURL, "://"); idx >= 0 {
		host = baseURL[idx+3:]
	}
	if idx := strings.IndexAny(host, "/?#"); idx >= 0 {
		host = host[:idx]
	}
	if host == "" {
		host = baseURL
	}
	if groupKey.hasProvider {
		return groupKey.providerID + " · " + host
	}
	return host
}

// logicalChannelGroupKeyFromPhysical 从 LogicalChannel 反推其归组键，
// 用于判断某条物理渠道是否仍应属于该 logical。
func logicalChannelGroupKeyFromPhysical(l *LogicalChannel) logicalChannelGroupKey {
	return logicalChannelGroupKey{
		accountUID:  strings.TrimSpace(l.AccountUID),
		providerID:  strings.TrimSpace(l.ProviderID),
		siteIdent:   strings.TrimSpace(l.SiteIdentity),
		hasAccount:  strings.TrimSpace(l.AccountUID) != "",
		hasProvider: strings.TrimSpace(l.ProviderID) != "",
	}
}

// FindLogicalChannelByUID 在 Config 中按 UID 查找（线性，O(N)）。
func FindLogicalChannelByUID(cfg *Config, uid string) *LogicalChannel {
	if cfg == nil || uid == "" {
		return nil
	}
	for i := range cfg.LogicalChannels {
		if cfg.LogicalChannels[i].LogicalChannelUID == uid {
			return &cfg.LogicalChannels[i]
		}
	}
	return nil
}

// ListLogicalChannelsByKind 按 kind 过滤。
func ListLogicalChannelsByKind(cfg *Config, kind LogicalChannelKind) []LogicalChannel {
	if cfg == nil {
		return nil
	}
	out := make([]LogicalChannel, 0, len(cfg.LogicalChannels))
	for i := range cfg.LogicalChannels {
		if cfg.LogicalChannels[i].Kind == kind {
			out = append(out, cfg.LogicalChannels[i])
		}
	}
	return out
}

// ReloadFromMemory 替换内存中 Config 并触发逻辑渠道回填。
// 仅供测试使用，模拟从盘上加载后首次访问。
func (cm *ConfigManager) ReloadFromMemory(cfg *Config) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.config = *cfg
	RebuildLogicalChannels(&cm.config)
}

// ensureLogicalBackfill 加载时调用：若 schema 版本非 1、任意物理渠道缺少
// LogicalChannelUID，或存在同账号却被拆成多张逻辑卡的 UID 分歧，则重建
// LogicalChannels 并写回物理字段。返回 true 表示发生了写回（需要 saveConfigLocked）。
func ensureLogicalBackfill(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.LogicalChannelSchemaVersion == LogicalChannelSchemaVersion {
		all := collectAllPhysicalChannelsWithSlice(cfg)
		need := false
		for _, e := range all {
			if strings.TrimSpace(e.channel.LogicalChannelUID) == "" {
				need = true
				break
			}
		}
		if !need && !hasAccountUIDDivergence(all) {
			return false
		}
	}
	RebuildLogicalChannels(cfg)
	log.Printf("[Config-LogicalChannel] 已重建逻辑渠道视图，共 %d 条", len(cfg.LogicalChannels))
	return true
}

// hasAccountUIDDivergence 检测同一托管账号的物理渠道是否被回填了多个不同的
// LogicalChannelUID（历史缺陷产物）。若存在，需要 Rebuild 触发 4.5 步收敛。
func hasAccountUIDDivergence(all []physicalChannelEntry) bool {
	uidByAccount := make(map[string]string)
	for _, e := range all {
		acc := strings.TrimSpace(e.channel.AccountUID)
		if acc == "" {
			continue
		}
		uid := strings.TrimSpace(e.channel.LogicalChannelUID)
		if uid == "" {
			continue
		}
		if prev, ok := uidByAccount[acc]; ok {
			if prev != uid {
				return true
			}
			continue
		}
		uidByAccount[acc] = uid
	}
	return false
}

var _ = log.Printf
