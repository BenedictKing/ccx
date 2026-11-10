package config

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// DisabledGroupModelInfo 记录人工禁用的配额组模型组合。
// QuotaGroup 非空时，当前渠道内所有同组 Key 动态共享该限制；为空时仅限制 Key。
type DisabledGroupModelInfo struct {
	QuotaGroup string `json:"quotaGroup,omitempty"`
	Key        string `json:"key,omitempty"`
	Model      string `json:"model"`
	DisabledAt string `json:"disabledAt"`
}

// IsGroupModelDisabled 判断人工分组模型限制是否作用于指定 Key。
func (u *UpstreamConfig) IsGroupModelDisabled(apiKey, quotaGroup, model string) bool {
	apiKey = strings.TrimSpace(apiKey)
	quotaGroup = strings.TrimSpace(quotaGroup)
	model = strings.ToLower(strings.TrimSpace(model))
	if apiKey == "" || model == "" || len(u.DisabledGroupModels) == 0 {
		return false
	}
	for _, dm := range u.DisabledGroupModels {
		if strings.ToLower(strings.TrimSpace(dm.Model)) != model {
			continue
		}
		disabledGroup := strings.TrimSpace(dm.QuotaGroup)
		if quotaGroup != "" && disabledGroup == quotaGroup {
			return true
		}
		if quotaGroup == "" && disabledGroup == "" && strings.TrimSpace(dm.Key) == apiKey {
			return true
		}
	}
	return false
}

// DisableGroupModel 永久禁用目标 Key 当前所属配额组的模型。
// 空配额组不会与其他空组 Key 合并，而是退化为单 Key 限制。
func (cm *ConfigManager) DisableGroupModel(apiType string, channelIndex int, apiKey, model string) (string, int, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return "", 0, fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}
	apiKey = strings.TrimSpace(apiKey)
	model = strings.TrimSpace(model)
	if apiKey == "" || model == "" {
		return "", 0, fmt.Errorf("apiKey 和 model 不能为空")
	}

	upstream := &(*upstreams)[channelIndex]
	quotaGroup, found := quotaGroupForAPIKey(upstream, apiKey)
	if !found {
		return "", 0, fmt.Errorf("API Key 不属于该渠道")
	}
	affectedKeyCount := countGroupModelAffectedKeys(upstream, apiKey, quotaGroup)
	for _, dm := range upstream.DisabledGroupModels {
		if sameDisabledGroupModel(dm, apiKey, quotaGroup, model) {
			return quotaGroup, affectedKeyCount, nil
		}
	}

	entry := DisabledGroupModelInfo{
		QuotaGroup: quotaGroup,
		Model:      model,
		DisabledAt: time.Now().Format(time.RFC3339),
	}
	if quotaGroup == "" {
		entry.Key = apiKey
	}
	upstream.DisabledGroupModels = append(upstream.DisabledGroupModels, entry)
	if err := cm.saveConfigLocked(cm.config); err != nil {
		upstream.DisabledGroupModels = upstream.DisabledGroupModels[:len(upstream.DisabledGroupModels)-1]
		return "", 0, err
	}
	log.Printf("[%s-GroupModel] 已禁用模型 %s (配额组: %s, 影响 Key: %d, 渠道: %s)",
		apiType, model, displayQuotaGroup(quotaGroup, apiKey), affectedKeyCount, upstream.Name)
	return quotaGroup, affectedKeyCount, nil
}

// RestoreGroupModel 移除人工分组模型限制。
func (cm *ConfigManager) RestoreGroupModel(apiType string, channelIndex int, apiKey, quotaGroup, model string) (string, int, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return "", 0, fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}
	apiKey = strings.TrimSpace(apiKey)
	quotaGroup = strings.TrimSpace(quotaGroup)
	model = strings.TrimSpace(model)
	if model == "" || (apiKey == "" && quotaGroup == "") {
		return "", 0, fmt.Errorf("model 以及 apiKey 或 quotaGroup 不能为空")
	}

	upstream := &(*upstreams)[channelIndex]
	if quotaGroup == "" {
		resolvedGroup, found := quotaGroupForAPIKey(upstream, apiKey)
		if found {
			quotaGroup = resolvedGroup
		}
	}
	affectedKeyCount := countGroupModelAffectedKeys(upstream, apiKey, quotaGroup)
	next := make([]DisabledGroupModelInfo, 0, len(upstream.DisabledGroupModels))
	removed := false
	for _, dm := range upstream.DisabledGroupModels {
		if sameDisabledGroupModel(dm, apiKey, quotaGroup, model) {
			removed = true
			continue
		}
		next = append(next, dm)
	}
	if !removed {
		return quotaGroup, affectedKeyCount, nil
	}
	previous := upstream.DisabledGroupModels
	upstream.DisabledGroupModels = next
	if err := cm.saveConfigLocked(cm.config); err != nil {
		upstream.DisabledGroupModels = previous
		return "", 0, err
	}
	log.Printf("[%s-GroupModel] 已恢复模型 %s (配额组: %s, 影响 Key: %d, 渠道: %s)",
		apiType, model, displayQuotaGroup(quotaGroup, apiKey), affectedKeyCount, upstream.Name)
	return quotaGroup, affectedKeyCount, nil
}

func quotaGroupForAPIKey(upstream *UpstreamConfig, apiKey string) (string, bool) {
	apiKey = strings.TrimSpace(apiKey)
	for _, key := range upstream.APIKeys {
		if strings.TrimSpace(key) != apiKey {
			continue
		}
		for _, cfg := range NormalizeAPIKeyConfigsForView(*upstream) {
			if strings.TrimSpace(cfg.Key) == apiKey {
				return strings.TrimSpace(cfg.QuotaGroup), true
			}
		}
		return "", true
	}
	return "", false
}

func countGroupModelAffectedKeys(upstream *UpstreamConfig, apiKey, quotaGroup string) int {
	if quotaGroup == "" {
		if apiKey == "" {
			return 0
		}
		return 1
	}
	count := 0
	for _, cfg := range NormalizeAPIKeyConfigsForView(*upstream) {
		if strings.TrimSpace(cfg.QuotaGroup) == quotaGroup && strings.TrimSpace(cfg.Key) != "" {
			count++
		}
	}
	return count
}

func sameDisabledGroupModel(dm DisabledGroupModelInfo, apiKey, quotaGroup, model string) bool {
	if !strings.EqualFold(strings.TrimSpace(dm.Model), strings.TrimSpace(model)) {
		return false
	}
	if quotaGroup != "" {
		return strings.TrimSpace(dm.QuotaGroup) == quotaGroup
	}
	return strings.TrimSpace(dm.QuotaGroup) == "" && strings.TrimSpace(dm.Key) == apiKey
}

func displayQuotaGroup(quotaGroup, apiKey string) string {
	if quotaGroup != "" {
		return quotaGroup
	}
	return "单 Key " + utils.MaskAPIKey(apiKey)
}
