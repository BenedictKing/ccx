package config

import (
	"reflect"
	"strings"
)

// sharedChannelUpdate 返回本次更新中属于逻辑渠道的共享字段。
// 协议级字段（serviceType/baseURL/model 能力/routePrefix/status/priority 等）不进入这里，
// 避免把不同协议的适配配置强行覆盖到兄弟路由。新增渠道字段时必须先在这里归类。
func sharedChannelUpdate(update UpstreamUpdate) *UpstreamUpdate {
	shared := UpstreamUpdate{
		AuthHeader:                  update.AuthHeader,
		Remark:                      update.Remark,
		Description:                 update.Description,
		Website:                     update.Website,
		InsecureSkipVerify:          update.InsecureSkipVerify,
		LowQuality:                  update.LowQuality,
		AutoBlacklistBalance:        update.AutoBlacklistBalance,
		NormalizeMetadataUserID:     update.NormalizeMetadataUserID,
		CustomHeaders:               update.CustomHeaders,
		ProxyURL:                    update.ProxyURL,
		ProxyPreferDirect:           update.ProxyPreferDirect,
		RequestTimeoutMs:            update.RequestTimeoutMs,
		ResponseHeaderTimeoutMs:     update.ResponseHeaderTimeoutMs,
		StreamFirstContentTimeoutMs: update.StreamFirstContentTimeoutMs,
		StreamInactivityTimeoutMs:   update.StreamInactivityTimeoutMs,
		StreamToolCallIdleTimeoutMs: update.StreamToolCallIdleTimeoutMs,
		CostMultiplier:              update.CostMultiplier,
		ChannelPaymentCurrency:      update.ChannelPaymentCurrency,
		ChannelPaymentAmount:        update.ChannelPaymentAmount,
		ChannelCreditCurrency:       update.ChannelCreditCurrency,
		ChannelCreditAmount:         update.ChannelCreditAmount,
		MaxGroupMultiplier:          update.MaxGroupMultiplier,
		Racing:                      update.Racing,
	}
	if reflect.DeepEqual(shared, UpstreamUpdate{}) {
		return nil
	}
	return &shared
}

// sharedChannelUpdateFromRoute 读取主路由的共享字段，用于加载期分叉收敛。
func sharedChannelUpdateFromRoute(route UpstreamConfig) UpstreamUpdate {
	update := UpstreamUpdate{
		AuthHeader:                  sharedStringPtr(route.AuthHeader),
		Remark:                      sharedStringPtr(route.Remark),
		Description:                 sharedStringPtr(route.Description),
		Website:                     sharedStringPtr(route.Website),
		InsecureSkipVerify:          sharedBoolPtr(route.InsecureSkipVerify),
		LowQuality:                  sharedBoolPtr(route.LowQuality),
		AutoBlacklistBalance:        sharedBoolPtr(route.IsAutoBlacklistBalanceEnabled()),
		NormalizeMetadataUserID:     sharedBoolPtr(route.IsNormalizeMetadataUserIDEnabled()),
		CustomHeaders:               cloneStringMap(route.CustomHeaders),
		ProxyURL:                    sharedStringPtr(route.ProxyURL),
		ProxyPreferDirect:           sharedBoolPtr(route.ProxyPreferDirect),
		RequestTimeoutMs:            sharedIntPtr(route.RequestTimeoutMs),
		ResponseHeaderTimeoutMs:     sharedIntPtr(route.ResponseHeaderTimeoutMs),
		StreamFirstContentTimeoutMs: sharedIntPtr(route.StreamFirstContentTimeoutMs),
		StreamInactivityTimeoutMs:   sharedIntPtr(route.StreamInactivityTimeoutMs),
		StreamToolCallIdleTimeoutMs: sharedIntPtr(route.StreamToolCallIdleTimeoutMs),
		Racing:                      cloneRacing(route.Racing),
	}
	if route.CostMultiplier != nil {
		v := *route.CostMultiplier
		update.CostMultiplier = &v
	}
	if route.MaxGroupMultiplier != nil {
		v := *route.MaxGroupMultiplier
		update.MaxGroupMultiplier = &v
	}
	update.ChannelPaymentCurrency = sharedStringPtr(route.ChannelPaymentCurrency)
	if route.ChannelPaymentAmount != nil {
		v := *route.ChannelPaymentAmount
		update.ChannelPaymentAmount = &v
	}
	update.ChannelCreditCurrency = sharedStringPtr(route.ChannelCreditCurrency)
	if route.ChannelCreditAmount != nil {
		v := *route.ChannelCreditAmount
		update.ChannelCreditAmount = &v
	}
	if len(route.Tags) > 0 {
		update.Tags = append([]string(nil), route.Tags...)
	} else {
		update.Tags = []string{}
	}
	return update
}

// convergeSharedChannelFields 在加载期以主路由为共享设置真源，修复历史分叉。
// 返回值表示是否发生了实际收敛，供 loader 触发一次落盘。
func convergeSharedChannelFields(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	type routeRef struct {
		route *UpstreamConfig
	}
	groups := make(map[string][]routeRef)
	for _, kind := range orderedChannelKinds() {
		slice := ChannelKindRegistry[kind].SliceRef(cfg)
		for i := range *slice {
			uid := strings.TrimSpace((*slice)[i].LogicalChannelUID)
			if uid != "" {
				groups[uid] = append(groups[uid], routeRef{route: &(*slice)[i]})
			}
		}
	}
	changed := false
	for _, routes := range groups {
		if len(routes) < 2 {
			continue
		}
		shared := sharedChannelUpdateFromRoute(*routes[0].route)
		for _, sibling := range routes[1:] {
			before := sharedChannelUpdateFromRoute(*sibling.route)
			if reflect.DeepEqual(before, shared) {
				continue
			}
			if _, err := applyUpstreamUpdateFields(sibling.route, shared); err != nil {
				continue
			}
			clearNilSharedChannelFields(sibling.route, shared)
			if !reflect.DeepEqual(before, sharedChannelUpdateFromRoute(*sibling.route)) {
				changed = true
			}
		}
	}
	return changed
}

// clearNilSharedChannelFields 把加载期共享设置中的 nil 投影为物理路由的清除操作。
// UpstreamUpdate 的 nil 通常表示“保持不变”，因此不能直接依赖 applyUpstreamUpdateFields
// 清除可选字段；这里仅由加载期收敛/Settings 投影调用，不改变普通部分更新的语义。
func clearNilSharedChannelFields(route *UpstreamConfig, update UpstreamUpdate) {
	if route == nil {
		return
	}
	if update.AutoBlacklistBalance == nil {
		route.AutoBlacklistBalance = nil
	}
	if update.NormalizeMetadataUserID == nil {
		route.NormalizeMetadataUserID = nil
	}
	if update.CustomHeaders == nil {
		route.CustomHeaders = nil
	}
	if update.CostMultiplier == nil {
		route.CostMultiplier = nil
	}
	if update.MaxGroupMultiplier == nil {
		route.MaxGroupMultiplier = nil
	}
	if update.ChannelPaymentAmount == nil {
		route.ChannelPaymentAmount = nil
	}
	if update.ChannelCreditAmount == nil {
		route.ChannelCreditAmount = nil
	}
	if update.Racing == nil {
		route.Racing = nil
	}
}

func sharedStringPtr(value string) *string { return &value }
func sharedBoolPtr(value bool) *bool       { return &value }
func sharedIntPtr(value int) *int          { return &value }

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneRacing(input *ChannelRacingConfig) *ChannelRacingConfig {
	if input == nil {
		return nil
	}
	output := *input
	return &output
}

func logicalSettingsFromRoute(route UpstreamConfig) LogicalChannelSettings {
	settings := LogicalChannelSettings{
		InsecureSkipVerify:          route.InsecureSkipVerify,
		LowQuality:                  route.LowQuality,
		CustomHeaders:               cloneStringMap(route.CustomHeaders),
		ProxyURL:                    route.ProxyURL,
		ProxyPreferDirect:           route.ProxyPreferDirect,
		RequestTimeoutMs:            route.RequestTimeoutMs,
		ResponseHeaderTimeoutMs:     route.ResponseHeaderTimeoutMs,
		StreamFirstContentTimeoutMs: route.StreamFirstContentTimeoutMs,
		StreamInactivityTimeoutMs:   route.StreamInactivityTimeoutMs,
		StreamToolCallIdleTimeoutMs: route.StreamToolCallIdleTimeoutMs,
		ChannelPaymentCurrency:      route.ChannelPaymentCurrency,
		ChannelCreditCurrency:       route.ChannelCreditCurrency,
		Racing:                      cloneRacing(route.Racing),
	}
	if route.AutoBlacklistBalance != nil {
		v := *route.AutoBlacklistBalance
		settings.AutoBlacklistBalance = &v
	}
	if route.NormalizeMetadataUserID != nil {
		v := *route.NormalizeMetadataUserID
		settings.NormalizeMetadataUserID = &v
	}
	if route.CostMultiplier != nil {
		v := *route.CostMultiplier
		settings.CostMultiplier = &v
	}
	if route.MaxGroupMultiplier != nil {
		v := *route.MaxGroupMultiplier
		settings.MaxGroupMultiplier = &v
	}
	if route.ChannelPaymentAmount != nil {
		v := *route.ChannelPaymentAmount
		settings.ChannelPaymentAmount = &v
	}
	if route.ChannelCreditAmount != nil {
		v := *route.ChannelCreditAmount
		settings.ChannelCreditAmount = &v
	}
	return settings
}

// logicalPrimaryRoute 按协议优先级返回逻辑渠道可解析到的主路由，找不到返回 nil。
func logicalPrimaryRoute(cfg *Config, lc *LogicalChannel) *UpstreamConfig {
	if cfg == nil || lc == nil {
		return nil
	}
	for _, kind := range orderedChannelKinds() {
		for _, protocol := range lc.Protocols {
			if protocol.Kind != string(kind) {
				continue
			}
			if route := getChannelByUID(cfg, protocol.Kind, protocol.ChannelUID); route != nil {
				return route
			}
		}
	}
	return nil
}

func logicalSettingsUpdate(settings LogicalChannelSettings) UpstreamUpdate {
	return UpstreamUpdate{
		InsecureSkipVerify:          sharedBoolPtr(settings.InsecureSkipVerify),
		LowQuality:                  sharedBoolPtr(settings.LowQuality),
		AutoBlacklistBalance:        cloneBoolPtr(settings.AutoBlacklistBalance),
		NormalizeMetadataUserID:     cloneBoolPtr(settings.NormalizeMetadataUserID),
		CustomHeaders:               cloneStringMap(settings.CustomHeaders),
		ProxyURL:                    sharedStringPtr(settings.ProxyURL),
		ProxyPreferDirect:           sharedBoolPtr(settings.ProxyPreferDirect),
		RequestTimeoutMs:            sharedIntPtr(settings.RequestTimeoutMs),
		ResponseHeaderTimeoutMs:     sharedIntPtr(settings.ResponseHeaderTimeoutMs),
		StreamFirstContentTimeoutMs: sharedIntPtr(settings.StreamFirstContentTimeoutMs),
		StreamInactivityTimeoutMs:   sharedIntPtr(settings.StreamInactivityTimeoutMs),
		StreamToolCallIdleTimeoutMs: sharedIntPtr(settings.StreamToolCallIdleTimeoutMs),
		CostMultiplier:              cloneFloatPtr(settings.CostMultiplier),
		MaxGroupMultiplier:          cloneFloatPtr(settings.MaxGroupMultiplier),
		ChannelPaymentCurrency:      sharedStringPtr(settings.ChannelPaymentCurrency),
		ChannelPaymentAmount:        cloneFloatPtr(settings.ChannelPaymentAmount),
		ChannelCreditCurrency:       sharedStringPtr(settings.ChannelCreditCurrency),
		ChannelCreditAmount:         cloneFloatPtr(settings.ChannelCreditAmount),
		Racing:                      cloneRacing(settings.Racing),
	}
}

func cloneBoolPtr(input *bool) *bool {
	if input == nil {
		return nil
	}
	v := *input
	return &v
}

func cloneFloatPtr(input *float64) *float64 {
	if input == nil {
		return nil
	}
	v := *input
	return &v
}

// syncLogicalChannelSettings 加载期以 LogicalChannel.Settings 为共享设置真源：
// Settings 缺失时从主路由一次性迁移（旧配置升级）；存在但与物理路由分叉时投影回路由。
// 返回值表示是否发生实际变更，供 loader 决定是否落盘。
func syncLogicalChannelSettings(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	changed := false
	for i := range cfg.LogicalChannels {
		lc := &cfg.LogicalChannels[i]
		primary := logicalPrimaryRoute(cfg, lc)
		if primary == nil {
			continue
		}
		if lc.Settings == nil {
			settings := logicalSettingsFromRoute(*primary)
			lc.Settings = &settings
			changed = true
			continue
		}
		update := logicalSettingsUpdate(*lc.Settings)
		for _, protocol := range lc.Protocols {
			route := getChannelByUID(cfg, protocol.Kind, protocol.ChannelUID)
			if route == nil {
				continue
			}
			before := logicalSettingsFromRoute(*route)
			if reflect.DeepEqual(before, *lc.Settings) {
				continue
			}
			if _, err := applyUpstreamUpdateFields(route, update); err != nil {
				continue
			}
			clearNilSharedChannelFields(route, update)
			if !reflect.DeepEqual(logicalSettingsFromRoute(*route), before) {
				changed = true
			}
		}
	}
	return changed
}

// refreshLogicalChannelSettingsFromRoutes 落盘前从主路由刷新逻辑渠道 Settings。
// 运行时更新路径（方案 A 投影）以物理路由为落点，这里把结果镜像回 Settings，
// 保证持久化形态自洽：磁盘上的 Settings 永远与主路由同代，加载期投影才幂等。
func refreshLogicalChannelSettingsFromRoutes(cfg *Config) {
	if cfg == nil {
		return
	}
	for i := range cfg.LogicalChannels {
		lc := &cfg.LogicalChannels[i]
		primary := logicalPrimaryRoute(cfg, lc)
		if primary == nil {
			continue
		}
		settings := logicalSettingsFromRoute(*primary)
		lc.Settings = &settings
	}
}
