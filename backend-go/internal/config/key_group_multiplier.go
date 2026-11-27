package config

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"reflect"
)

// OptionalMultiplier 区分未提交、恢复默认倍率(null)和免费(0)。
type OptionalMultiplier struct {
	Present bool
	Value   *float64
}

func (v *OptionalMultiplier) UnmarshalJSON(data []byte) error {
	v.Present = true
	return json.Unmarshal(data, &v.Value)
}

func validateGroupMultiplier(value *float64) error {
	if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
		return fmt.Errorf("渠道分组倍率必须是有限非负数或 null")
	}
	return nil
}

func cloneGroupMultiplier(value *float64) *float64 {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}

func validateSiteChannelIdentity(upstream UpstreamConfig, updates UpstreamUpdate) error {
	if updates.ExpectedSiteSnapshot != nil && !reflect.DeepEqual(upstream, *updates.ExpectedSiteSnapshot) {
		return fmt.Errorf("渠道已被编辑、重排或删除，保留最新设置，请重试同步")
	}
	return nil
}

func (u *UpstreamConfig) BillingMultiplier() float64 {
	if u == nil || u.GroupMultiplier == nil {
		return 1
	}
	return *u.GroupMultiplier
}

func (u *UpstreamConfig) IsAPIKeyRemoteAvailable(key string) bool {
	for _, c := range u.APIKeyConfigs {
		if c.Key == key {
			return c.RemoteStatus == ""
		}
	}
	return true
}

// migrateChannelGroupMultipliers 只提升一致的旧倍率，避免把一个 Key 的价格套用到其他 Key。
func (cm *ConfigManager) migrateChannelGroupMultipliers(rawConfig map[string]json.RawMessage) bool {
	type legacyKey struct {
		Key             string
		GroupMultiplier json.RawMessage
	}
	type legacyChannel struct {
		MaxGroupMultiplier json.RawMessage
		GroupMultiplier    json.RawMessage
		APIKeyConfigs      []legacyKey
		DisabledAPIKeys    []struct {
			Key    string
			Config *legacyKey
		}
	}
	migrated := false
	for field, channels := range map[string][]UpstreamConfig{
		"upstream": cm.config.Upstream, "chatUpstream": cm.config.ChatUpstream,
		"responsesUpstream": cm.config.ResponsesUpstream, "geminiUpstream": cm.config.GeminiUpstream,
		"imagesUpstream": cm.config.ImagesUpstream, "vectorsUpstream": cm.config.VectorsUpstream,
	} {
		var legacy []legacyChannel
		if json.Unmarshal(rawConfig[field], &legacy) != nil {
			continue
		}
		for i := range legacy {
			old := legacy[i]
			active, disabled := map[string]json.RawMessage{}, map[string]json.RawMessage{}
			hasLegacyFactor := false
			for _, cfg := range old.APIKeyConfigs {
				active[cfg.Key] = cfg.GroupMultiplier
				hasLegacyFactor = hasLegacyFactor || len(cfg.GroupMultiplier) != 0
			}
			for _, key := range old.DisabledAPIKeys {
				if key.Config != nil {
					disabled[key.Key] = key.Config.GroupMultiplier
					hasLegacyFactor = hasLegacyFactor || len(key.Config.GroupMultiplier) != 0
				}
			}
			migrated = migrated || len(old.MaxGroupMultiplier) != 0 || hasLegacyFactor
			channel := &channels[i]
			if !hasLegacyFactor || len(old.GroupMultiplier) != 0 {
				continue
			}

			var common *float64
			consistent := true
			include := func(raw json.RawMessage) {
				value := 1.0
				if (len(raw) != 0 && json.Unmarshal(raw, &value) != nil) || validateGroupMultiplier(&value) != nil {
					consistent = false
					return
				}
				if common == nil {
					common = &value
				} else if *common != value {
					consistent = false
				}
			}
			for _, key := range channel.APIKeys {
				include(active[key])
			}
			for _, key := range channel.DisabledAPIKeys {
				if raw, exists := active[key.Key]; exists {
					include(raw)
				} else {
					include(disabled[key.Key])
				}
			}
			if consistent {
				channel.GroupMultiplier = common
			} else {
				log.Printf("[Config-Migration] %s 渠道 %q 的旧 Key 倍率不一致或无效，按默认倍率 1，请按需拆分渠道", field, channel.Name)
			}
		}
	}
	return migrated
}
