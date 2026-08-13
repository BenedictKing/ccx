package config

// channel_view.go 从六个 Upstream* 数组合成只读的 ChannelView 列表，并产出一份
// 跨账号共享的 EndpointCapability 注册表（按 CapabilityUID 索引）。
//
// 合成规则见 docs/specs/channel-data-model-v2.md：
//   - 归组：LogicalChannelUID > AccountUID > SiteIdentity > 物理 ChannelUID。
//   - 凭证：同一 KeyHash 在组内跨协议合并为一个 ChannelKeyView。
//   - 能力：每个 (SiteIdentity, GroupIdentity, IdentityBaseURL, Protocol) 一份 EndpointCapability，
//     同站点同分组的多账号 key 共享；模型清单 Phase1 取 SupportedModels，后续接入探测画像。

import (
	"log"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// channelViewMember 是参与合成的一个物理渠道引用。
type channelViewMember struct {
	kind    string
	index   int
	channel *UpstreamConfig
}

// collectPhysicalChannelsForView 汇总六个数组为可迭代引用（保持数组内顺序）。
func collectPhysicalChannelsForView(cfg *Config) []channelViewMember {
	out := make([]channelViewMember, 0)
	add := func(kind string, arr []UpstreamConfig) {
		for i := range arr {
			out = append(out, channelViewMember{kind: kind, index: i, channel: &arr[i]})
		}
	}
	add("messages", cfg.Upstream)
	add("chat", cfg.ChatUpstream)
	add("responses", cfg.ResponsesUpstream)
	add("gemini", cfg.GeminiUpstream)
	add("images", cfg.ImagesUpstream)
	add("vectors", cfg.VectorsUpstream)
	return out
}

// primaryBaseURLForView 返回物理渠道的首选 baseURL（用于站点身份计算）。
func primaryBaseURLForView(u *UpstreamConfig) string {
	if all := u.GetAllBaseURLs(); len(all) > 0 {
		return all[0]
	}
	return strings.TrimSpace(u.BaseURL)
}

// channelViewAggregationKey 决定物理渠道归到哪个 ChannelView。
func channelViewAggregationKey(u *UpstreamConfig) string {
	if uid := strings.TrimSpace(u.LogicalChannelUID); uid != "" {
		return "lc:" + uid
	}
	if acc := strings.TrimSpace(u.AccountUID); acc != "" {
		return "acct:" + acc
	}
	if site := SiteIdentityForBaseURL(primaryBaseURLForView(u)); site != "" {
		return "site:" + site
	}
	return "chan:" + u.ChannelUID
}

// channelViewBuilder 累积单个 ChannelView 的合成中间态。
type channelViewBuilder struct {
	view      *ChannelView
	keyByHash map[string]*ChannelKeyView // KeyHash -> 合并后的 key 视图
	keyOrder  []string                   // KeyHash 首次出现顺序
	baseSeen  map[string]struct{}
}

// BuildChannelViews 合成 ChannelView 列表与共享能力注册表。
// 返回的 capabilities 以 CapabilityUID 为键，供健康检查/画像跨账号复用同一份协议+模型认知。
func BuildChannelViews(cfg *Config) ([]ChannelView, map[string]EndpointCapability) {
	if cfg == nil {
		return nil, map[string]EndpointCapability{}
	}
	now := time.Now()
	builders := make(map[string]*channelViewBuilder)
	order := make([]string, 0)
	capabilities := make(map[string]EndpointCapability)

	for _, m := range collectPhysicalChannelsForView(cfg) {
		aggKey := channelViewAggregationKey(m.channel)
		b := builders[aggKey]
		if b == nil {
			b = &channelViewBuilder{
				view:      &ChannelView{},
				keyByHash: make(map[string]*ChannelKeyView),
				baseSeen:  make(map[string]struct{}),
			}
			builders[aggKey] = b
			order = append(order, aggKey)
		}
		mergeMemberIntoBuilder(b, m, now, capabilities)
	}

	out := make([]ChannelView, 0, len(order))
	for _, aggKey := range order {
		out = append(out, finalizeChannelView(builders[aggKey]))
	}
	return out, capabilities
}

// RebuildChannels 把六个 Upstream 数组合成的 ChannelView 与共享能力写入 config 镜像字段。
//
// 与 RebuildLogicalChannels 同为落盘前的非权威投影：六个数组仍是运行时权威，
// Channels/ChannelCapabilities 只是把新粒度（渠道→key→endpoint→模型 + 跨账号共享能力）
// 持久化到 config.json，供前端与后续 Phase 3 消费。不含明文 key（仅 KeyMask）。
func RebuildChannels(cfg *Config) {
	if cfg == nil {
		return
	}
	views, caps := BuildChannelViews(cfg)
	cfg.Channels = views
	cfg.ChannelCapabilities = make([]EndpointCapability, 0, len(caps))
	for _, uid := range sortedCapabilityUIDs(caps) {
		cfg.ChannelCapabilities = append(cfg.ChannelCapabilities, caps[uid])
	}
	cfg.ChannelSchemaVersion = ChannelSchemaVersion
	// 清理由旧派生规则写入的存量名称：把 v.Name 重新派生为 DeriveChannelNameFromBaseURL
	// (primary BaseURL)，旧名写入 Remark。saveConfigLocked 之后会同步到 ChannelsV3。
	migrateStaleChannelViewNames(cfg)
}

// GetChannelViews 按当前配置实时合成 ChannelView 列表与排序后的共享能力。
// 与持久化镜像不同，本方法总是基于运行时权威（六数组）现算，不依赖 save 是否已执行。
func (cm *ConfigManager) GetChannelViews() ([]ChannelView, []EndpointCapability) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	views, caps := BuildChannelViews(&cm.config)
	sortedCaps := make([]EndpointCapability, 0, len(caps))
	for _, uid := range sortedCapabilityUIDs(caps) {
		sortedCaps = append(sortedCaps, caps[uid])
	}
	return views, sortedCaps
}

// mergeMemberIntoBuilder 把一个物理渠道并入 ChannelView 中间态。
func mergeMemberIntoBuilder(b *channelViewBuilder, m channelViewMember, now time.Time, caps map[string]EndpointCapability) {
	u := m.channel
	v := b.view

	// 身份字段：首个非空者胜出，保持稳定。
	if v.ChannelUID == "" {
		if uid := strings.TrimSpace(u.LogicalChannelUID); uid != "" {
			v.ChannelUID = uid
		} else {
			v.ChannelUID = u.ChannelUID
		}
	}
	if v.AccountUID == "" {
		v.AccountUID = strings.TrimSpace(u.AccountUID)
	}
	if v.ProviderID == "" {
		v.ProviderID = strings.TrimSpace(u.ProviderID)
	}
	if v.Name == "" {
		if ln := strings.TrimSpace(u.LogicalName); ln != "" {
			v.Name = ln
		} else {
			v.Name = strings.TrimSpace(u.Name)
		}
	}
	if v.SiteIdentity == "" {
		v.SiteIdentity = SiteIdentityForBaseURL(primaryBaseURLForView(u))
	}
	if v.Account == nil && strings.EqualFold(strings.TrimSpace(u.AutoManagedKind), "new_api") {
		v.Account = &NewApiAccountView{
			AccountUID: strings.TrimSpace(u.AccountUID),
			Status:     effectiveChannelStatus(u),
		}
	}
	for _, bu := range u.GetAllBaseURLs() {
		if _, ok := b.baseSeen[bu]; !ok {
			b.baseSeen[bu] = struct{}{}
			v.BaseURLs = append(v.BaseURLs, bu)
		}
	}

	v.Protocols = append(v.Protocols, ProtocolFacadeView{
		Kind:        m.kind,
		ServiceType: serviceTypeOrFallback(u.ServiceType, m.kind),
		ChannelUID:  u.ChannelUID,
		Index:       m.index,
		Enabled:     effectiveChannelStatus(u) == "active",
		Status:      effectiveChannelStatus(u),
		Priority:    u.Priority,
		RoutePrefix: u.RoutePrefix,
	})
	v.MemberRoutes = append(v.MemberRoutes, ChannelRouteRefView{Kind: m.kind, Index: m.index, ChannelUID: u.ChannelUID})

	mergeKeysIntoBuilder(b, m, now, caps)
}

// apiKeyConfigForKey 返回某明文 key 的 APIKeyConfig（找不到返回零值副本）。
func apiKeyConfigForKey(u *UpstreamConfig, apiKey string) APIKeyConfig {
	for _, kc := range u.APIKeyConfigs {
		if kc.Key == apiKey {
			return kc
		}
	}
	return APIKeyConfig{}
}

// mergeKeysIntoBuilder 把物理渠道的每个 key 并入 view，并注册其 endpoint 能力。
func mergeKeysIntoBuilder(b *channelViewBuilder, m channelViewMember, now time.Time, caps map[string]EndpointCapability) {
	u := m.channel
	protocol := ProtocolForChannelKind(m.kind)
	serviceType := serviceTypeOrFallback(u.ServiceType, m.kind)

	for _, apiKey := range u.APIKeys {
		apiKey = strings.TrimSpace(apiKey)
		if apiKey == "" {
			continue
		}
		kc := apiKeyConfigForKey(u, apiKey)
		hash := ChannelKeyHash(apiKey)

		kv := b.keyByHash[hash]
		if kv == nil {
			kv = &ChannelKeyView{
				KeyUID:                 kc.KeyUID,
				CredentialUID:          firstNonEmpty(kc.CredentialUID, u.CredentialUIDForKey(apiKey)),
				KeyMask:                utils.MaskAPIKey(apiKey),
				KeyHash:                hash,
				AccountUID:             strings.TrimSpace(u.AccountUID),
				QuotaGroup:             kc.QuotaGroup,
				GroupIdentity:          NormalizeGroupIdentity(kc.QuotaGroup),
				Enabled:                keyEnabledForView(u, apiKey, kc, now),
				Weight:                 kc.Weight,
				RateLimitRPM:           kc.RateLimitRPM,
				RateLimitMaxConcurrent: kc.RateLimitMaxConcurrent,
			}
			b.keyByHash[hash] = kv
			b.keyOrder = append(b.keyOrder, hash)
		}

		for _, baseURL := range u.BaseURLsForKey(apiKey) {
			identity := utils.MetricsIdentityBaseURL(baseURL, serviceType)
			site := b.view.SiteIdentity
			capUID := GenerateCapabilityUID(site, kv.GroupIdentity, identity, protocol)
			registerCapability(caps, capUID, site, kv.GroupIdentity, kc.QuotaGroup, baseURL, identity, protocol, serviceType, u.SupportedModels)
			if !hasBinding(kv, capUID) {
				kv.Endpoints = append(kv.Endpoints, KeyEndpointBindingView{
					CapabilityUID:   capUID,
					Protocol:        protocol,
					IdentityBaseURL: identity,
					Enabled:         kv.Enabled,
				})
			}
		}
	}
}

// finalizeChannelView 按 key 首现顺序重建 Keys，并推导聚合状态。
func finalizeChannelView(b *channelViewBuilder) ChannelView {
	v := *b.view
	v.Keys = make([]ChannelKeyView, 0, len(b.keyOrder))
	for _, hash := range b.keyOrder {
		v.Keys = append(v.Keys, *b.keyByHash[hash])
	}
	v.Status = aggregateStatus(v.Protocols)
	if v.Account != nil {
		for _, k := range v.Keys {
			if k.KeyUID != "" {
				v.Account.KeyUIDs = append(v.Account.KeyUIDs, k.KeyUID)
			}
		}
	}
	return v
}

// registerCapability 幂等写入共享能力；已存在时并入模型清单（去重保序）。
func registerCapability(caps map[string]EndpointCapability, capUID, site, groupIdentity, groupName, baseURL, identity, protocol, serviceType string, models []string) {
	existing, ok := caps[capUID]
	if !ok {
		existing = EndpointCapability{
			CapabilityUID:   capUID,
			SiteIdentity:    site,
			GroupIdentity:   groupIdentity,
			GroupName:       strings.TrimSpace(groupName),
			BaseURL:         baseURL,
			IdentityBaseURL: identity,
			Protocol:        protocol,
			ServiceType:     serviceType,
			Source:          "config_supported_models",
		}
	}
	existing.Models = mergeModelList(existing.Models, models)
	caps[capUID] = existing
}

// hasBinding 判断 key 是否已绑定某能力。
func hasBinding(kv *ChannelKeyView, capUID string) bool {
	for _, e := range kv.Endpoints {
		if e.CapabilityUID == capUID {
			return true
		}
	}
	return false
}

// keyEnabledForView 综合 DisabledAPIKeys 与 APIKeyConfig.Enabled 判断 key 是否可参与调度。
func keyEnabledForView(u *UpstreamConfig, apiKey string, kc APIKeyConfig, now time.Time) bool {
	if kc.Enabled != nil && !*kc.Enabled {
		return false
	}
	return !u.IsKeyDisabledNow(apiKey, now)
}

// serviceTypeOrFallback 优先用配置 serviceType，缺失时按 kind 兜底。
func serviceTypeOrFallback(serviceType, kind string) string {
	if s := strings.TrimSpace(serviceType); s != "" {
		return s
	}
	return ServiceTypeForChannelKind(kind)
}

// effectiveChannelStatus 空状态视为 active（与调度器口径一致）。
func effectiveChannelStatus(u *UpstreamConfig) string {
	if strings.TrimSpace(u.Status) == "" {
		return "active"
	}
	return u.Status
}

// aggregateStatus 聚合多协议状态：全 active→active；全非 active 且一致→取该值；混合→partial。
func aggregateStatus(protocols []ProtocolFacadeView) string {
	if len(protocols) == 0 {
		return "disabled"
	}
	first := protocols[0].Status
	allSame := true
	anyActive := false
	for _, p := range protocols {
		if p.Status == "active" {
			anyActive = true
		}
		if p.Status != first {
			allSame = false
		}
	}
	if allSame {
		return first
	}
	if anyActive {
		return "partial"
	}
	return first
}

// mergeModelList 合并两个模型清单，去重保序。
func mergeModelList(base, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, list := range [][]string{base, extra} {
		for _, m := range list {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// firstNonEmpty 返回首个非空串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// channelNameRemarkMaxRunes 镜像 Name 旧名搬迁到 Remark 的最大字符数（rune）。
// 与 migrateAllChannelNamesConfig 物理渠道侧 10 字符保持一致，避免前端/日志对账歧义。
const channelNameRemarkMaxRunes = 10

// deriveChannelNameForView 优先用 view 上的 baseUrls[0]，否则用 siteIdentity 推一个
// 候选 baseURL。SiteIdentity 是 BaseURL 的归一化值（去 path/尾部斜杠），再用
// SiteIdentityForBaseURL 的反推路径上不可逆时退化为原始 site 字符串。
func deriveChannelNameForView(v *ChannelView) (string, string) {
	if v == nil {
		return "", ""
	}
	primary := ""
	if len(v.BaseURLs) > 0 {
		primary = strings.TrimSpace(v.BaseURLs[0])
	}
	if primary == "" {
		primary = strings.TrimSpace(v.SiteIdentity)
	}
	if primary == "" {
		return "", ""
	}
	return primary, utils.DeriveChannelNameFromBaseURL(primary)
}

// migrateStaleChannelViewNames 清理 Channels 镜像里被旧派生规则写下的历史名。
// 旧名 vip-lyclaude-site-claude 形式源自旧 DeriveChannelNameFromBaseURL 把 .site 当
// 公共后缀处理 + 追加 -claude 协议后缀的旧实现；新规则下应派生为 vip-lyclaude。
// 规则：
//   - 派生值与当前 Name 相同：不动；
//   - 派生值非空、与当前 Name 不同：v.Name = 派生值，v.Remark = 旧名（仅当 Remark 为空，
//     截断到 channelNameRemarkMaxRunes）；
//   - 派生值为空（缺 baseURL/siteIdentity）：保持现状，仅打印一次诊断；
//   - ChannelsV3 镜像按 ChannelUID 同步：仅在 channels 改名时同步改 ChannelsV3[].Name。
//
// 返回 true 表示发生了写回。
func migrateStaleChannelViewNames(cfg *Config) bool {
	if cfg == nil || len(cfg.Channels) == 0 {
		return false
	}
	changed := false
	byUID := make(map[string]*ChannelView, len(cfg.Channels))
	for i := range cfg.Channels {
		v := &cfg.Channels[i]
		primary, derived := deriveChannelNameForView(v)
		if derived == "" {
			log.Printf("[Config-ChannelName] 跳过无 baseURL 渠道: uid=%s", v.ChannelUID)
			continue
		}
		current := strings.TrimSpace(v.Name)
		if current == derived {
			byUID[v.ChannelUID] = v
			continue
		}
		old := current
		if old != "" && strings.TrimSpace(v.Remark) == "" {
			if remarkRuneCount(old) > channelNameRemarkMaxRunes {
				old = string([]rune(old)[:channelNameRemarkMaxRunes])
			}
			v.Remark = old
		}
		v.Name = derived
		byUID[v.ChannelUID] = v
		changed = true
		log.Printf("[Config-ChannelName] 镜像 Name 派生: uid=%s site=%s old=%q -> new=%q",
			v.ChannelUID, primary, current, derived)
	}
	// 同步到 ChannelsV3 镜像（仅在 channels 发生改名时）。
	if changed && len(cfg.ChannelsV3) > 0 {
		for i := range cfg.ChannelsV3 {
			ch := &cfg.ChannelsV3[i]
			v, ok := byUID[ch.ChannelUID]
			if !ok || v == nil {
				continue
			}
			if strings.TrimSpace(ch.Name) == v.Name {
				continue
			}
			ch.Name = v.Name
		}
	}
	return changed
}

// migrateStaleChannelViewNamesFromDisk 启动期专用版本：直接基于磁盘已加载的 cfg.Channels 与
// cfg.ChannelsV3 计算派生值，不要求 v.BaseURLs 非空（兼容历史镜像里 BaseURLs 缺失的场景）。
//
// 与 migrateStaleChannelViewNames 的差异：
//   - baseURL 来源：view.BaseURLs[0] → view.SiteIdentity → 由 v.Name 反推 “host 段”
//     （v.Name 在旧派生规则下形如 host-part-a-host-part-b，去除第一个 host 段后回填一次
//     解析；该路径只用于启动期历史残留，最坏情况是派生失败跳过，逻辑通道在落盘阶段再走
//     一次 RebuildChannels 重新合成）。
//   - 不会在 cfg.Channels 为空时强行写出新条目：不破坏磁盘形态未知（fixture 无 ChannelsV3）
//     的测试场景。
func migrateStaleChannelViewNamesFromDisk(cfg *Config) bool {
	if cfg == nil || len(cfg.Channels) == 0 {
		return false
	}
	changed := false
	byUID := make(map[string]*ChannelView, len(cfg.Channels))
	for i := range cfg.Channels {
		v := &cfg.Channels[i]
		primary, derived := deriveChannelNameForViewFromDisk(v)
		if derived == "" {
			continue
		}
		current := strings.TrimSpace(v.Name)
		if current == derived {
			byUID[v.ChannelUID] = v
			continue
		}
		old := current
		if old != "" && strings.TrimSpace(v.Remark) == "" {
			if remarkRuneCount(old) > channelNameRemarkMaxRunes {
				old = string([]rune(old)[:channelNameRemarkMaxRunes])
			}
			v.Remark = old
		}
		v.Name = derived
		byUID[v.ChannelUID] = v
		changed = true
		log.Printf("[Config-ChannelName] 启动期镜像 Name 派生: uid=%s site=%s old=%q -> new=%q",
			v.ChannelUID, primary, current, derived)
	}
	if changed && len(cfg.ChannelsV3) > 0 {
		for i := range cfg.ChannelsV3 {
			ch := &cfg.ChannelsV3[i]
			v, ok := byUID[ch.ChannelUID]
			if !ok || v == nil {
				continue
			}
			if strings.TrimSpace(ch.Name) == v.Name {
				continue
			}
			ch.Name = v.Name
		}
	}
	return changed
}

// deriveChannelNameForViewFromDisk 启动期镜像派生：view 上 BaseURLs 缺失时退回到 SiteIdentity。
func deriveChannelNameForViewFromDisk(v *ChannelView) (string, string) {
	primary := ""
	if len(v.BaseURLs) > 0 {
		primary = strings.TrimSpace(v.BaseURLs[0])
	}
	if primary == "" {
		primary = strings.TrimSpace(v.SiteIdentity)
	}
	if primary == "" {
		return "", ""
	}
	return primary, utils.DeriveChannelNameFromBaseURL(primary)
}
