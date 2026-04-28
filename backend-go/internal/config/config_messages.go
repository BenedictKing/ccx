package config

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

func firstActiveUpstreamIndex(upstreams []UpstreamConfig) int {
	for i := range upstreams {
		status := upstreams[i].Status
		if status == "" || status == "active" {
			return i
		}
	}

	return -1
}

func validateUniqueUpstreamName(upstreams []UpstreamConfig, excludeIndex int, name string) error {
	for i := range upstreams {
		if i == excludeIndex {
			continue
		}
		if upstreams[i].Name == name {
			return fmt.Errorf("渠道名称 '%s' 已存在", name)
		}
	}

	return nil
}

// ============== Messages 渠道方法 ==============

// GetCurrentUpstream 获取当前上游配置
// 优先选择第一个 active 状态的渠道，若无则返回显式错误
func (cm *ConfigManager) GetCurrentUpstream() (*UpstreamConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.config.Upstream) == 0 {
		return nil, fmt.Errorf("未配置任何上游渠道")
	}

	activeIndex := firstActiveUpstreamIndex(cm.config.Upstream)
	if activeIndex < 0 {
		return nil, fmt.Errorf("没有可用的 active 上游渠道")
	}

	return &cm.config.Upstream[activeIndex], nil
}

// GetCurrentUpstreamWithIndex 获取当前上游配置及其索引
func (cm *ConfigManager) GetCurrentUpstreamWithIndex() (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.config.Upstream) == 0 {
		return nil, 0, fmt.Errorf("未配置任何上游渠道")
	}

	activeIndex := firstActiveUpstreamIndex(cm.config.Upstream)
	if activeIndex < 0 {
		return nil, -1, fmt.Errorf("没有可用的 active 上游渠道")
	}

	return &cm.config.Upstream[activeIndex], activeIndex, nil
}

// AddUpstream 添加上游
func (cm *ConfigManager) AddUpstream(upstream UpstreamConfig) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 检查 Name 是否已存在
	for _, existing := range cm.config.Upstream {
		if existing.Name == upstream.Name {
			return fmt.Errorf("渠道名称 '%s' 已存在", upstream.Name)
		}
	}

	// 新建渠道默认设为 active
	if upstream.Status == "" {
		upstream.Status = "active"
	}

	// 去重 API Keys 和 Base URLs
	upstream.APIKeys = deduplicateStrings(upstream.APIKeys)
	upstream.BaseURL = utils.CanonicalBaseURL(upstream.BaseURL, upstream.ServiceType)
	upstream.BaseURLs = deduplicateBaseURLs(upstream.BaseURLs, upstream.ServiceType)
	upstream.NormalizeClaudePassthroughMode()
	upstream.NormalizeModelsHealthCheckOptions()
	upstream.NormalizeModelsResponseMode()

	cm.config.Upstream = append(cm.config.Upstream, upstream)

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Upstream] 已添加上游: %s", upstream.Name)
	return nil
}

// UpdateUpstream 更新上游
// 返回值：shouldResetMetrics 表示是否需要重置渠道指标（熔断状态）
func (cm *ConfigManager) UpdateUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.Upstream) {
		return false, fmt.Errorf("无效的上游索引: %d", index)
	}

	upstream := &cm.config.Upstream[index]
	serviceType := upstream.ServiceType
	if updates.ServiceType != nil {
		serviceType = *updates.ServiceType
	}

	if updates.Name != nil {
		if err := validateUniqueUpstreamName(cm.config.Upstream, index, *updates.Name); err != nil {
			return false, err
		}
		upstream.Name = *updates.Name
	}
	if updates.BaseURL != nil {
		upstream.BaseURL = utils.CanonicalBaseURL(*updates.BaseURL, serviceType)
		// 当 BaseURL 被更新且 BaseURLs 未被显式设置时，清空 BaseURLs 保持一致性
		// 避免出现 baseUrl 和 baseUrls[0] 不一致的情况
		if updates.BaseURLs == nil {
			upstream.BaseURLs = nil
		}
	}
	if updates.BaseURLs != nil {
		upstream.BaseURLs = deduplicateBaseURLs(updates.BaseURLs, serviceType)
	}
	if updates.ServiceType != nil {
		upstream.ServiceType = serviceType
	}
	if updates.Description != nil {
		upstream.Description = *updates.Description
	}
	if updates.Website != nil {
		upstream.Website = *updates.Website
	}
	if updates.APIKeys != nil {
		// 记录被移除的 Key 到历史列表（用于统计聚合）
		newKeys := make(map[string]bool)
		for _, key := range updates.APIKeys {
			newKeys[key] = true
		}

		// 找出被移除的 Key（在旧列表中但不在新列表中）
		for _, key := range upstream.APIKeys {
			if !newKeys[key] {
				// 检查是否已在历史列表中
				alreadyInHistory := false
				for _, hk := range upstream.HistoricalAPIKeys {
					if hk == key {
						alreadyInHistory = true
						break
					}
				}
				if !alreadyInHistory {
					upstream.HistoricalAPIKeys = append(upstream.HistoricalAPIKeys, key)
					log.Printf("[Config-Upstream] 渠道 [%d] %s: Key %s 已移入历史列表", index, upstream.Name, utils.MaskAPIKey(key))
				}
			}
		}

		// 如果新 Key 在历史列表中，从历史列表移除（换回来了）
		var newHistoricalKeys []string
		for _, hk := range upstream.HistoricalAPIKeys {
			if !newKeys[hk] {
				newHistoricalKeys = append(newHistoricalKeys, hk)
			} else {
				log.Printf("[Config-Upstream] 渠道 [%d] %s: Key %s 已从历史列表恢复", index, upstream.Name, utils.MaskAPIKey(hk))
			}
		}
		upstream.HistoricalAPIKeys = newHistoricalKeys

		// 只有单 key 场景且 key 被更换时，才自动激活并重置熔断
		if len(upstream.APIKeys) == 1 && len(updates.APIKeys) == 1 &&
			upstream.APIKeys[0] != updates.APIKeys[0] {
			shouldResetMetrics = true
			if upstream.Status == "suspended" {
				upstream.Status = "active"
				log.Printf("[Config-Upstream] 渠道 [%d] %s 已从暂停状态自动激活（单 key 更换）", index, upstream.Name)
			}
		}
		upstream.APIKeys = deduplicateStrings(updates.APIKeys)
	}
	if updates.ModelMapping != nil {
		upstream.ModelMapping = updates.ModelMapping
	}
	if updates.ReasoningMapping != nil {
		upstream.ReasoningMapping = updates.ReasoningMapping
	}
	if updates.TextVerbosity != nil {
		upstream.TextVerbosity = *updates.TextVerbosity
	}
	if updates.FastMode != nil {
		upstream.FastMode = *updates.FastMode
	}
	if updates.InsecureSkipVerify != nil {
		upstream.InsecureSkipVerify = *updates.InsecureSkipVerify
	}
	if updates.Priority != nil {
		upstream.Priority = *updates.Priority
	}
	if updates.Status != nil {
		upstream.Status = *updates.Status
	}
	if updates.PromotionUntil != nil {
		upstream.PromotionUntil = updates.PromotionUntil
	}
	if updates.LowQuality != nil {
		upstream.LowQuality = *updates.LowQuality
	}
	if updates.AutoBlacklistBalance != nil {
		v := *updates.AutoBlacklistBalance
		upstream.AutoBlacklistBalance = &v
	}
	if updates.NormalizeMetadataUserID != nil {
		v := *updates.NormalizeMetadataUserID
		upstream.NormalizeMetadataUserID = &v
	}
	if updates.StreamPassthroughEnabled != nil {
		v := *updates.StreamPassthroughEnabled
		upstream.StreamPassthroughEnabled = &v
	}
	if updates.Sub2APIPassthroughEnabled != nil {
		v := *updates.Sub2APIPassthroughEnabled
		upstream.Sub2APIPassthroughEnabled = &v
	}
	if updates.KeyAffinityEnabled != nil {
		v := *updates.KeyAffinityEnabled
		upstream.KeyAffinityEnabled = &v
	}
	if updates.StrictRequestPassthroughEnabled != nil {
		v := *updates.StrictRequestPassthroughEnabled
		upstream.StrictRequestPassthroughEnabled = &v
	}
	if updates.ModelsHealthCheckEnabled != nil {
		v := *updates.ModelsHealthCheckEnabled
		upstream.ModelsHealthCheckEnabled = &v
	}
	if updates.ModelsHealthCheckIntervalMinutes != nil {
		v := *updates.ModelsHealthCheckIntervalMinutes
		if v <= 0 {
			v = 60
		}
		upstream.ModelsHealthCheckIntervalMinutes = &v
	}
	if updates.FailoverRules != nil {
		upstream.FailoverRules = CloneFailoverRules(updates.FailoverRules)
	}
	if updates.InjectDummyThoughtSignature != nil {
		upstream.InjectDummyThoughtSignature = *updates.InjectDummyThoughtSignature
	}
	if updates.CustomHeaders != nil {
		upstream.CustomHeaders = updates.CustomHeaders
	}
	if updates.ProxyURL != nil {
		upstream.ProxyURL = *updates.ProxyURL
	}
	if updates.SupportedModels != nil {
		upstream.SupportedModels = updates.SupportedModels
	}
	if updates.ModelsResponseMode != nil {
		upstream.ModelsResponseMode = *updates.ModelsResponseMode
	}
	if updates.ManualModels != nil {
		upstream.ManualModels = updates.ManualModels
	}
	if updates.RoutePrefix != nil {
		upstream.RoutePrefix = *updates.RoutePrefix
	}
	upstream.NormalizeClaudePassthroughMode()
	upstream.NormalizeModelsHealthCheckOptions()
	upstream.NormalizeModelsResponseMode()

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return false, err
	}

	log.Printf("[Config-Upstream] 已更新上游: [%d] %s", index, cm.config.Upstream[index].Name)
	return shouldResetMetrics, nil
}

// RemoveUpstream 删除上游
func (cm *ConfigManager) RemoveUpstream(index int) (*UpstreamConfig, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.Upstream) {
		return nil, fmt.Errorf("无效的上游索引: %d", index)
	}

	removed := cm.config.Upstream[index]
	cm.config.Upstream = append(cm.config.Upstream[:index], cm.config.Upstream[index+1:]...)

	// 清理被删除渠道的失败 key 冷却记录
	cm.clearFailedKeysForUpstream(&removed, "Messages")

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}

	log.Printf("[Config-Upstream] 已删除上游: %s", removed.Name)
	return &removed, nil
}

// AddAPIKey 添加API密钥
func (cm *ConfigManager) AddAPIKey(index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.Upstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	// 检查密钥是否已存在
	for _, key := range cm.config.Upstream[index].APIKeys {
		if key == apiKey {
			return fmt.Errorf("API密钥已存在")
		}
	}

	cm.config.Upstream[index].APIKeys = append(cm.config.Upstream[index].APIKeys, apiKey)

	var newDisabledKeys []DisabledKeyInfo
	for _, dk := range cm.config.Upstream[index].DisabledAPIKeys {
		if dk.Key != apiKey {
			newDisabledKeys = append(newDisabledKeys, dk)
		}
	}
	cm.config.Upstream[index].DisabledAPIKeys = newDisabledKeys

	// 如果该 Key 在历史列表中，从历史列表移除（换回来了）
	var newHistoricalKeys []string
	for _, hk := range cm.config.Upstream[index].HistoricalAPIKeys {
		if hk != apiKey {
			newHistoricalKeys = append(newHistoricalKeys, hk)
		} else {
			log.Printf("[Messages-Key] 上游 [%d] %s: Key %s 已从历史列表恢复", index, cm.config.Upstream[index].Name, utils.MaskAPIKey(hk))
		}
	}
	cm.config.Upstream[index].HistoricalAPIKeys = newHistoricalKeys

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Messages-Key] 已添加API密钥到上游 [%d] %s", index, cm.config.Upstream[index].Name)
	return nil
}

// RemoveAPIKey 删除API密钥
func (cm *ConfigManager) RemoveAPIKey(index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.Upstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	// 查找并删除密钥
	keys := cm.config.Upstream[index].APIKeys
	found := false
	for i, key := range keys {
		if key == apiKey {
			cm.config.Upstream[index].APIKeys = append(keys[:i], keys[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("API密钥不存在")
	}

	// 将被移除的 Key 添加到历史列表（用于统计聚合）
	alreadyInHistory := false
	for _, hk := range cm.config.Upstream[index].HistoricalAPIKeys {
		if hk == apiKey {
			alreadyInHistory = true
			break
		}
	}
	if !alreadyInHistory {
		cm.config.Upstream[index].HistoricalAPIKeys = append(cm.config.Upstream[index].HistoricalAPIKeys, apiKey)
		log.Printf("[Messages-Key] 上游 [%d] %s: Key %s 已移入历史列表", index, cm.config.Upstream[index].Name, utils.MaskAPIKey(apiKey))
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Messages-Key] 已从上游 [%d] %s 删除API密钥", index, cm.config.Upstream[index].Name)
	return nil
}

// MoveAPIKeyToTop 将指定渠道的 API 密钥移到最前面
func (cm *ConfigManager) MoveAPIKeyToTop(upstreamIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upstreamIndex < 0 || upstreamIndex >= len(cm.config.Upstream) {
		return fmt.Errorf("无效的上游索引: %d", upstreamIndex)
	}

	upstream := &cm.config.Upstream[upstreamIndex]
	index := -1
	for i, key := range upstream.APIKeys {
		if key == apiKey {
			index = i
			break
		}
	}

	if index <= 0 {
		return nil // 已经在最前面或未找到
	}

	// 移动到开头
	upstream.APIKeys = append([]string{apiKey}, append(upstream.APIKeys[:index], upstream.APIKeys[index+1:]...)...)
	return cm.saveConfigLocked(cm.config)
}

// MoveAPIKeyToBottom 将指定渠道的 API 密钥移到最后面
func (cm *ConfigManager) MoveAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upstreamIndex < 0 || upstreamIndex >= len(cm.config.Upstream) {
		return fmt.Errorf("无效的上游索引: %d", upstreamIndex)
	}

	upstream := &cm.config.Upstream[upstreamIndex]
	index := -1
	for i, key := range upstream.APIKeys {
		if key == apiKey {
			index = i
			break
		}
	}

	if index == -1 || index == len(upstream.APIKeys)-1 {
		return nil // 已经在最后面或未找到
	}

	// 移动到末尾
	upstream.APIKeys = append(upstream.APIKeys[:index], upstream.APIKeys[index+1:]...)
	upstream.APIKeys = append(upstream.APIKeys, apiKey)
	return cm.saveConfigLocked(cm.config)
}

// ReorderUpstreams 重新排序 Messages 渠道优先级
// order 是渠道索引数组，按新的优先级顺序排列（只更新传入的渠道，支持部分排序）
func (cm *ConfigManager) ReorderUpstreams(order []int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if len(order) == 0 {
		return fmt.Errorf("排序数组不能为空")
	}

	// 验证所有索引都有效且不重复
	seen := make(map[int]bool)
	for _, idx := range order {
		if idx < 0 || idx >= len(cm.config.Upstream) {
			return fmt.Errorf("无效的渠道索引: %d", idx)
		}
		if seen[idx] {
			return fmt.Errorf("重复的渠道索引: %d", idx)
		}
		seen[idx] = true
	}

	// 更新传入渠道的优先级（未传入的渠道保持原优先级不变）
	// 注意：priority 从 1 开始，避免 omitempty 吞掉 0 值
	for i, idx := range order {
		cm.config.Upstream[idx].Priority = i + 1
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Reorder] 已更新 Messages 渠道优先级顺序 (%d 个渠道)", len(order))
	return nil
}

// SetChannelStatus 设置 Messages 渠道状态
func (cm *ConfigManager) SetChannelStatus(index int, status string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.Upstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	// 状态值转为小写，支持大小写不敏感
	status = strings.ToLower(status)
	if status != "active" && status != "suspended" && status != "disabled" {
		return fmt.Errorf("无效的状态: %s (允许值: active, suspended, disabled)", status)
	}

	cm.config.Upstream[index].Status = status

	// 暂停时清除促销期
	if status == "suspended" && cm.config.Upstream[index].PromotionUntil != nil {
		cm.config.Upstream[index].PromotionUntil = nil
		log.Printf("[Config-Status] 已清除渠道 [%d] %s 的促销期", index, cm.config.Upstream[index].Name)
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Status] 已设置渠道 [%d] %s 状态为: %s", index, cm.config.Upstream[index].Name, status)
	return nil
}

// SetChannelPromotion 设置渠道促销期
// duration 为促销持续时间，传入 0 表示清除促销期
func (cm *ConfigManager) SetChannelPromotion(index int, duration time.Duration) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.Upstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	if duration <= 0 {
		cm.config.Upstream[index].PromotionUntil = nil
		log.Printf("[Config-Promotion] 已清除渠道 [%d] %s 的促销期", index, cm.config.Upstream[index].Name)
	} else {
		// 清除其他渠道的促销期（同一时间只允许一个促销渠道）
		for i := range cm.config.Upstream {
			if i != index && cm.config.Upstream[i].PromotionUntil != nil {
				cm.config.Upstream[i].PromotionUntil = nil
			}
		}
		promotionEnd := time.Now().Add(duration)
		cm.config.Upstream[index].PromotionUntil = &promotionEnd
		log.Printf("[Config-Promotion] 已设置渠道 [%d] %s 进入促销期，截止: %s", index, cm.config.Upstream[index].Name, promotionEnd.Format(time.RFC3339))
	}

	return cm.saveConfigLocked(cm.config)
}

// GetPromotedChannel 获取当前处于促销期的渠道索引（返回优先级最高的）
func (cm *ConfigManager) GetPromotedChannel() (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	for i, upstream := range cm.config.Upstream {
		if IsChannelInPromotion(&upstream) && GetChannelStatus(&upstream) == "active" {
			return i, true
		}
	}
	return -1, false
}

// DeprioritizeAPIKey 降低API密钥优先级（在所有渠道中查找）
func (cm *ConfigManager) DeprioritizeAPIKey(apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 遍历所有渠道查找该 API 密钥
	for upstreamIdx := range cm.config.Upstream {
		upstream := &cm.config.Upstream[upstreamIdx]
		index := -1
		for i, key := range upstream.APIKeys {
			if key == apiKey {
				index = i
				break
			}
		}

		if index != -1 && index != len(upstream.APIKeys)-1 {
			// 移动到末尾
			upstream.APIKeys = append(upstream.APIKeys[:index], upstream.APIKeys[index+1:]...)
			upstream.APIKeys = append(upstream.APIKeys, apiKey)
			log.Printf("[Messages-Key] 已将API密钥移动到末尾以降低优先级: %s (渠道: %s)", utils.MaskAPIKey(apiKey), upstream.Name)
			return cm.saveConfigLocked(cm.config)
		}
	}

	// 同样遍历 Responses 渠道
	for upstreamIdx := range cm.config.ResponsesUpstream {
		upstream := &cm.config.ResponsesUpstream[upstreamIdx]
		index := -1
		for i, key := range upstream.APIKeys {
			if key == apiKey {
				index = i
				break
			}
		}

		if index != -1 && index != len(upstream.APIKeys)-1 {
			// 移动到末尾
			upstream.APIKeys = append(upstream.APIKeys[:index], upstream.APIKeys[index+1:]...)
			upstream.APIKeys = append(upstream.APIKeys, apiKey)
			log.Printf("[Responses-Key] 已将API密钥移动到末尾以降低优先级: %s (Responses渠道: %s)", utils.MaskAPIKey(apiKey), upstream.Name)
			return cm.saveConfigLocked(cm.config)
		}
	}

	// 同样遍历 Chat 渠道
	for upstreamIdx := range cm.config.ChatUpstream {
		upstream := &cm.config.ChatUpstream[upstreamIdx]
		index := -1
		for i, key := range upstream.APIKeys {
			if key == apiKey {
				index = i
				break
			}
		}

		if index != -1 && index != len(upstream.APIKeys)-1 {
			// 移动到末尾
			upstream.APIKeys = append(upstream.APIKeys[:index], upstream.APIKeys[index+1:]...)
			upstream.APIKeys = append(upstream.APIKeys, apiKey)
			log.Printf("[Chat-Key] 已将API密钥移动到末尾以降低优先级: %s (Chat渠道: %s)", utils.MaskAPIKey(apiKey), upstream.Name)
			return cm.saveConfigLocked(cm.config)
		}
	}

	for upstreamIdx := range cm.config.ImagesUpstream {
		upstream := &cm.config.ImagesUpstream[upstreamIdx]
		index := -1
		for i, key := range upstream.APIKeys {
			if key == apiKey {
				index = i
				break
			}
		}

		if index != -1 && index != len(upstream.APIKeys)-1 {
			upstream.APIKeys = append(upstream.APIKeys[:index], upstream.APIKeys[index+1:]...)
			upstream.APIKeys = append(upstream.APIKeys, apiKey)
			log.Printf("[Images-Key] 已将API密钥移动到末尾以降低优先级: %s (Images渠道: %s)", utils.MaskAPIKey(apiKey), upstream.Name)
			return cm.saveConfigLocked(cm.config)
		}
	}

	return nil
}
