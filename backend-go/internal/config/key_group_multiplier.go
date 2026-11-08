package config

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
)

// OptionalMultiplier 区分未提交、不设上限(null)、免费分组上限(0)。
type OptionalMultiplier struct {
	Present bool
	Value   *float64
}

func (v *OptionalMultiplier) UnmarshalJSON(data []byte) error {
	v.Present = true
	return json.Unmarshal(data, &v.Value)
}

func validateKeySettings(configs []APIKeyConfig, limit *float64) error {
	valid := func(v *float64) bool { return v == nil || (!math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= 0) }
	if !valid(limit) {
		return fmt.Errorf("分组倍率上限必须是有限非负数或 null")
	}
	for _, c := range configs {
		if !valid(c.GroupMultiplier) {
			return fmt.Errorf("Key 分组倍率必须是有限非负数或 null")
		}
	}
	return nil
}

func validateSiteChannelIdentity(upstream UpstreamConfig, updates UpstreamUpdate) error {
	if updates.ExpectedSiteSnapshot != nil && !reflect.DeepEqual(upstream, *updates.ExpectedSiteSnapshot) {
		return fmt.Errorf("渠道已被编辑、重排或删除，保留最新设置，请重试同步")
	}
	return nil
}

// IsAPIKeyGroupMultiplierAllowed 只在显式设定上限时要求可信的倍率值。
func (u *UpstreamConfig) IsAPIKeyGroupMultiplierAllowed(key string) bool {
	if u == nil {
		return false
	}
	for _, c := range u.APIKeyConfigs {
		if c.Key != key {
			continue
		}
		if c.RemoteStatus != "" {
			return false
		}
		if validateKeySettings([]APIKeyConfig{c}, u.MaxGroupMultiplier) != nil {
			return false
		}
		return u.MaxGroupMultiplier == nil || (c.GroupMultiplier != nil && *c.GroupMultiplier <= *u.MaxGroupMultiplier)
	}
	return u.MaxGroupMultiplier == nil
}
