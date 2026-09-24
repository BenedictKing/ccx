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
			if _, err := applyUpstreamUpdateFields(sibling.route, shared); err == nil {
				changed = true
			}
		}
	}
	return changed
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
