package config

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// ============== Chat 渠道方法 ==============

// GetCurrentChatUpstream 获取当前 Chat 上游配置
// 优先选择第一个 active 状态的渠道，若无则回退到第一个渠道
func (cm *ConfigManager) GetCurrentChatUpstream() (*UpstreamConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.config.ChatUpstream) == 0 {
		return nil, fmt.Errorf("未配置任何 Chat 渠道")
	}

	// 优先选择第一个 active 状态的渠道
	for i := range cm.config.ChatUpstream {
		status := cm.config.ChatUpstream[i].Status
		if status == "" || status == "active" {
			return &cm.config.ChatUpstream[i], nil
		}
	}

	// 没有 active 渠道，回退到第一个渠道
	return &cm.config.ChatUpstream[0], nil
}

// GetCurrentChatUpstreamWithIndex 获取当前 Chat 上游配置及其索引
func (cm *ConfigManager) GetCurrentChatUpstreamWithIndex() (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.config.ChatUpstream) == 0 {
		return nil, 0, fmt.Errorf("未配置任何 Chat 渠道")
	}

	for i := range cm.config.ChatUpstream {
		status := cm.config.ChatUpstream[i].Status
		if status == "" || status == "active" {
			return &cm.config.ChatUpstream[i], i, nil
		}
	}

	return &cm.config.ChatUpstream[0], 0, nil
}

// AddChatUpstream 添加 Chat 上游
// placements 可选传 "front"（故障转移序列首位），缺省为追加到序列末尾（见 assignChannelPriority）
func (cm *ConfigManager) AddChatUpstream(upstream UpstreamConfig, placements ...string) error {
	return cm.addUpstreamCommon(ChannelKindRegistry[ChannelKindChat], upstream, placements...)
}

// UpdateChatUpstream 更新 Chat 上游
// 返回值：shouldResetMetrics 表示是否需要重置渠道指标（熔断状态）
func (cm *ConfigManager) UpdateChatUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.updateUpstreamCommon(ChannelKindRegistry[ChannelKindChat], index, updates)
}

// RemoveChatUpstream 删除 Chat 上游
func (cm *ConfigManager) RemoveChatUpstream(index int) (*UpstreamConfig, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ChatUpstream) {
		return nil, fmt.Errorf("无效的 Chat 上游索引: %d", index)
	}

	removed := cm.config.ChatUpstream[index]
	cm.config.ChatUpstream = append(cm.config.ChatUpstream[:index], cm.config.ChatUpstream[index+1:]...)

	cm.clearFailedKeysForUpstream(&removed, "chat")

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}

	log.Printf("[Config-Upstream] 已删除 Chat 上游: %s", removed.Name)
	return &removed, nil
}

// AddChatAPIKey 添加 Chat 上游的 API 密钥
func (cm *ConfigManager) AddChatAPIKey(index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ChatUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	for _, key := range cm.config.ChatUpstream[index].APIKeys {
		if key == apiKey {
			return fmt.Errorf("API密钥已存在")
		}
	}

	cm.config.ChatUpstream[index].APIKeys = append(cm.config.ChatUpstream[index].APIKeys, apiKey)

	var newDisabledKeys []DisabledKeyInfo
	for _, dk := range cm.config.ChatUpstream[index].DisabledAPIKeys {
		if dk.Key != apiKey {
			newDisabledKeys = append(newDisabledKeys, dk)
		}
	}
	cm.config.ChatUpstream[index].DisabledAPIKeys = newDisabledKeys
	removeDisabledKeyModelsForKey(&cm.config.ChatUpstream[index], apiKey)

	var newHistoricalKeys []string
	for _, hk := range cm.config.ChatUpstream[index].HistoricalAPIKeys {
		if hk != apiKey {
			newHistoricalKeys = append(newHistoricalKeys, hk)
		} else {
			log.Printf("[Chat-Key] 上游 [%d] %s: Key %s 已从历史列表恢复", index, cm.config.ChatUpstream[index].Name, utils.MaskAPIKey(hk))
		}
	}
	cm.config.ChatUpstream[index].HistoricalAPIKeys = newHistoricalKeys

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Chat-Key] 已添加API密钥到 Chat 上游 [%d] %s", index, cm.config.ChatUpstream[index].Name)
	return nil
}

// RemoveChatAPIKey 删除 Chat 上游的 API 密钥
func (cm *ConfigManager) RemoveChatAPIKey(index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ChatUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	keys := cm.config.ChatUpstream[index].APIKeys
	found := false
	for i, key := range keys {
		if key == apiKey {
			cm.config.ChatUpstream[index].APIKeys = append(keys[:i], keys[i+1:]...)
			found = true
			break
		}
	}

	// 已拉黑的 Key 不在活跃列表中，允许连同禁用记录一起删除
	if cm.removeDisabledKeyEntryLocked(&cm.config.ChatUpstream[index], "Chat", apiKey) {
		found = true
	}

	if !found {
		return fmt.Errorf("API密钥不存在")
	}

	alreadyInHistory := false
	for _, hk := range cm.config.ChatUpstream[index].HistoricalAPIKeys {
		if hk == apiKey {
			alreadyInHistory = true
			break
		}
	}
	if !alreadyInHistory {
		cm.config.ChatUpstream[index].HistoricalAPIKeys = append(cm.config.ChatUpstream[index].HistoricalAPIKeys, apiKey)
		log.Printf("[Chat-Key] 上游 [%d] %s: Key %s 已移入历史列表", index, cm.config.ChatUpstream[index].Name, utils.MaskAPIKey(apiKey))
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Chat-Key] 已从 Chat 上游 [%d] %s 删除API密钥", index, cm.config.ChatUpstream[index].Name)
	return nil
}

// GetNextChatAPIKey 获取下一个 Chat API 密钥（纯 failover 模式）
func (cm *ConfigManager) GetNextChatAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Chat")
}

// MoveChatAPIKeyToTop 将指定 Chat 渠道的 API 密钥移到最前面
func (cm *ConfigManager) MoveChatAPIKeyToTop(upstreamIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upstreamIndex < 0 || upstreamIndex >= len(cm.config.ChatUpstream) {
		return fmt.Errorf("无效的上游索引: %d", upstreamIndex)
	}

	upstream := &cm.config.ChatUpstream[upstreamIndex]
	index := -1
	for i, key := range upstream.APIKeys {
		if key == apiKey {
			index = i
			break
		}
	}

	if index <= 0 {
		return nil
	}

	upstream.APIKeys = append([]string{apiKey}, append(upstream.APIKeys[:index], upstream.APIKeys[index+1:]...)...)
	return cm.saveConfigLocked(cm.config)
}

// MoveChatAPIKeyToBottom 将指定 Chat 渠道的 API 密钥移到最后面
func (cm *ConfigManager) MoveChatAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upstreamIndex < 0 || upstreamIndex >= len(cm.config.ChatUpstream) {
		return fmt.Errorf("无效的上游索引: %d", upstreamIndex)
	}

	upstream := &cm.config.ChatUpstream[upstreamIndex]
	index := -1
	for i, key := range upstream.APIKeys {
		if key == apiKey {
			index = i
			break
		}
	}

	if index == -1 || index == len(upstream.APIKeys)-1 {
		return nil
	}

	upstream.APIKeys = append(upstream.APIKeys[:index], upstream.APIKeys[index+1:]...)
	upstream.APIKeys = append(upstream.APIKeys, apiKey)
	return cm.saveConfigLocked(cm.config)
}

// ReorderChatUpstreams 重新排序 Chat 渠道优先级
// order 是渠道索引数组，按新的优先级顺序排列（只更新传入的渠道，支持部分排序）
// priorities 可选：与 order 平行的显式优先级值（统一 LLM 视图按全局位次提交），缺省按位次 1..N
func (cm *ConfigManager) ReorderChatUpstreams(order []int, priorities ...[]int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if len(order) == 0 {
		return fmt.Errorf("排序数组不能为空")
	}

	seen := make(map[int]bool)
	for _, idx := range order {
		if idx < 0 || idx >= len(cm.config.ChatUpstream) {
			return fmt.Errorf("无效的渠道索引: %d", idx)
		}
		if seen[idx] {
			return fmt.Errorf("重复的渠道索引: %d", idx)
		}
		seen[idx] = true
	}

	values, err := resolveReorderPriorities(order, priorities...)
	if err != nil {
		return err
	}
	for i, idx := range order {
		cm.config.ChatUpstream[idx].Priority = values[i]
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Reorder] 已更新 Chat 渠道优先级顺序 (%d 个渠道)", len(order))
	return nil
}

// SetChatChannelStatus 设置 Chat 渠道状态
func (cm *ConfigManager) SetChatChannelStatus(index int, status string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ChatUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	status = strings.ToLower(status)
	if status != "active" && status != "suspended" && status != "disabled" {
		return fmt.Errorf("无效的状态: %s (允许值: active, suspended, disabled)", status)
	}

	promotionCleared := applyAdministrativeChannelStatus(&cm.config.ChatUpstream[index], status)

	if promotionCleared {
		log.Printf("[Config-Status] 已清除 Chat 渠道 [%d] %s 的促销期", index, cm.config.ChatUpstream[index].Name)
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Status] 已设置 Chat 渠道 [%d] %s 状态为: %s", index, cm.config.ChatUpstream[index].Name, status)
	return nil
}

// SetChatChannelPromotion 设置 Chat 渠道促销期
func (cm *ConfigManager) SetChatChannelPromotion(index int, duration time.Duration) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ChatUpstream) {
		return fmt.Errorf("无效的 Chat 上游索引: %d", index)
	}

	if duration <= 0 {
		cm.config.ChatUpstream[index].PromotionUntil = nil
		log.Printf("[Config-Promotion] 已清除 Chat 渠道 [%d] %s 的促销期", index, cm.config.ChatUpstream[index].Name)
	} else {
		for i := range cm.config.ChatUpstream {
			if i != index && cm.config.ChatUpstream[i].PromotionUntil != nil {
				cm.config.ChatUpstream[i].PromotionUntil = nil
			}
		}
		promotionEnd := time.Now().Add(duration)
		cm.config.ChatUpstream[index].PromotionUntil = &promotionEnd
		log.Printf("[Config-Promotion] 已设置 Chat 渠道 [%d] %s 进入促销期，截止: %s", index, cm.config.ChatUpstream[index].Name, promotionEnd.Format(time.RFC3339))
	}

	return cm.saveConfigLocked(cm.config)
}

// GetPromotedChatChannel 获取当前处于促销期的 Chat 渠道索引
func (cm *ConfigManager) GetPromotedChatChannel() (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	for i, upstream := range cm.config.ChatUpstream {
		if IsChannelInPromotion(&upstream) && GetChannelStatus(&upstream) == "active" {
			return i, true
		}
	}
	return -1, false
}

// UpdateChatModelMapping 更新指定 Chat 上游的单个模型映射
func (cm *ConfigManager) UpdateChatModelMapping(index int, sourcePattern, targetModel, reasoning string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ChatUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	upstream := &cm.config.ChatUpstream[index]
	if upstream.AutoManaged {
		return fmt.Errorf("渠道 [%d] %s 为自动托管渠道，模型映射由 Autopilot 自动解析，不支持手工编辑", index, upstream.Name)
	}

	// 检查 sourcePattern 是否存在
	if upstream.ModelMapping == nil {
		return fmt.Errorf("源模型匹配模式 '%s' 不存在", sourcePattern)
	}
	if _, exists := upstream.ModelMapping[sourcePattern]; !exists {
		return fmt.Errorf("源模型匹配模式 '%s' 不存在", sourcePattern)
	}

	// 验证 reasoning 值
	if !isValidReasoningEffort(reasoning) {
		return fmt.Errorf("无效的 reasoning 级别: %s", reasoning)
	}

	// 更新 ModelMapping
	upstream.ModelMapping[sourcePattern] = targetModel
	upstream.ModelMapping, _ = sanitizeDeprecatedGrokModelMapping(upstream.ModelMapping)

	// 更新 ReasoningMapping
	if reasoning != "" {
		if upstream.ReasoningMapping == nil {
			upstream.ReasoningMapping = make(map[string]string)
		}
		upstream.ReasoningMapping[sourcePattern] = reasoning
	} else if upstream.ReasoningMapping != nil {
		delete(upstream.ReasoningMapping, sourcePattern)
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Chat] 已更新上游 [%d] %s 的模型映射: %s -> %s (reasoning: %s)",
		index, upstream.Name, sourcePattern, targetModel, reasoning)
	return nil
}
