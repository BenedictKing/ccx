package config

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// ============== Vectors 渠道方法 ==============

// normalizeVectorsServiceType 规范化并校验 Vectors 渠道 serviceType。
func normalizeVectorsServiceType(serviceType string) (string, error) {
	normalized := normalizeUpstreamServiceType(serviceType, "openai")
	if normalized != "openai" {
		return "", &ConfigError{
			Message: fmt.Sprintf("Vectors 渠道仅支持 openai serviceType，当前为 %s", normalized),
			Cause:   ErrUnsupportedServiceType,
		}
	}
	return normalized, nil
}

// NormalizeVectorsServiceTypeForProxy 供代理层复用 Vectors 渠道 serviceType 校验。
func NormalizeVectorsServiceTypeForProxy(serviceType string) (string, error) {
	return normalizeVectorsServiceType(serviceType)
}

// GetCurrentVectorsUpstream 获取当前 Vectors 上游配置
// 优先选择第一个 active 状态的渠道，若无则回退到第一个渠道
func (cm *ConfigManager) GetCurrentVectorsUpstream() (*UpstreamConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.config.VectorsUpstream) == 0 {
		return nil, fmt.Errorf("未配置任何 Vectors 渠道")
	}

	// 优先选择第一个 active 状态的渠道
	for i := range cm.config.VectorsUpstream {
		status := cm.config.VectorsUpstream[i].Status
		if status == "" || status == "active" {
			return &cm.config.VectorsUpstream[i], nil
		}
	}

	// 没有 active 渠道，回退到第一个渠道
	return &cm.config.VectorsUpstream[0], nil
}

// GetCurrentVectorsUpstreamWithIndex 获取当前 Vectors 上游配置及其索引
func (cm *ConfigManager) GetCurrentVectorsUpstreamWithIndex() (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.config.VectorsUpstream) == 0 {
		return nil, 0, fmt.Errorf("未配置任何 Vectors 渠道")
	}

	for i := range cm.config.VectorsUpstream {
		status := cm.config.VectorsUpstream[i].Status
		if status == "" || status == "active" {
			return &cm.config.VectorsUpstream[i], i, nil
		}
	}

	return &cm.config.VectorsUpstream[0], 0, nil
}

// AddVectorsUpstream 添加 Vectors 上游
// placements 可选传 "front"（故障转移序列首位），缺省为追加到序列末尾（见 assignChannelPriority）
func (cm *ConfigManager) AddVectorsUpstream(upstream UpstreamConfig, placements ...string) error {
	return cm.addUpstreamCommon(ChannelKindRegistry[ChannelKindVectors], upstream, placements...)
}

// UpdateVectorsUpstream 更新 Vectors 上游
// 返回值：shouldResetMetrics 表示是否需要重置渠道指标（熔断状态）
func (cm *ConfigManager) UpdateVectorsUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.updateUpstreamCommon(ChannelKindRegistry[ChannelKindVectors], index, updates)
}

// RemoveVectorsUpstream 删除 Vectors 上游
func (cm *ConfigManager) RemoveVectorsUpstream(index int) (*UpstreamConfig, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.VectorsUpstream) {
		return nil, fmt.Errorf("无效的 Vectors 上游索引: %d", index)
	}

	removed := cm.config.VectorsUpstream[index]
	cm.config.VectorsUpstream = append(cm.config.VectorsUpstream[:index], cm.config.VectorsUpstream[index+1:]...)

	cm.clearFailedKeysForUpstream(&removed, "Vectors")

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}

	log.Printf("[Config-Upstream] 已删除 Vectors 上游: %s", removed.Name)
	return &removed, nil
}

// AddVectorsAPIKey 添加 Vectors 上游的 API 密钥
func (cm *ConfigManager) AddVectorsAPIKey(index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.VectorsUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	for _, key := range cm.config.VectorsUpstream[index].APIKeys {
		if key == apiKey {
			return fmt.Errorf("API密钥已存在")
		}
	}

	cm.config.VectorsUpstream[index].APIKeys = append(cm.config.VectorsUpstream[index].APIKeys, apiKey)

	var newDisabledKeys []DisabledKeyInfo
	for _, dk := range cm.config.VectorsUpstream[index].DisabledAPIKeys {
		if dk.Key != apiKey {
			newDisabledKeys = append(newDisabledKeys, dk)
		}
	}
	cm.config.VectorsUpstream[index].DisabledAPIKeys = newDisabledKeys
	removeDisabledKeyModelsForKey(&cm.config.VectorsUpstream[index], apiKey)

	var newHistoricalKeys []string
	for _, hk := range cm.config.VectorsUpstream[index].HistoricalAPIKeys {
		if hk != apiKey {
			newHistoricalKeys = append(newHistoricalKeys, hk)
		} else {
			log.Printf("[Vectors-Key] 上游 [%d] %s: Key %s 已从历史列表恢复", index, cm.config.VectorsUpstream[index].Name, utils.MaskAPIKey(hk))
		}
	}
	cm.config.VectorsUpstream[index].HistoricalAPIKeys = newHistoricalKeys

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Vectors-Key] 已添加API密钥到 Vectors 上游 [%d] %s", index, cm.config.VectorsUpstream[index].Name)
	return nil
}

// RemoveVectorsAPIKey 删除 Vectors 上游的 API 密钥
func (cm *ConfigManager) RemoveVectorsAPIKey(index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.VectorsUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	keys := cm.config.VectorsUpstream[index].APIKeys
	found := false
	for i, key := range keys {
		if key == apiKey {
			cm.config.VectorsUpstream[index].APIKeys = append(keys[:i], keys[i+1:]...)
			found = true
			break
		}
	}

	// 已拉黑的 Key 不在活跃列表中，允许连同禁用记录一起删除
	if cm.removeDisabledKeyEntryLocked(&cm.config.VectorsUpstream[index], "Vectors", apiKey) {
		found = true
	}

	if !found {
		return fmt.Errorf("API密钥不存在")
	}

	alreadyInHistory := false
	for _, hk := range cm.config.VectorsUpstream[index].HistoricalAPIKeys {
		if hk == apiKey {
			alreadyInHistory = true
			break
		}
	}
	if !alreadyInHistory {
		cm.config.VectorsUpstream[index].HistoricalAPIKeys = append(cm.config.VectorsUpstream[index].HistoricalAPIKeys, apiKey)
		log.Printf("[Vectors-Key] 上游 [%d] %s: Key %s 已移入历史列表", index, cm.config.VectorsUpstream[index].Name, utils.MaskAPIKey(apiKey))
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Vectors-Key] 已从 Vectors 上游 [%d] %s 删除API密钥", index, cm.config.VectorsUpstream[index].Name)
	return nil
}

// GetNextVectorsAPIKey 获取下一个 Vectors API 密钥（纯 failover 模式）
func (cm *ConfigManager) GetNextVectorsAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Vectors")
}

// MoveVectorsAPIKeyToTop 将指定 Vectors 渠道的 API 密钥移到最前面
func (cm *ConfigManager) MoveVectorsAPIKeyToTop(upstreamIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upstreamIndex < 0 || upstreamIndex >= len(cm.config.VectorsUpstream) {
		return fmt.Errorf("无效的上游索引: %d", upstreamIndex)
	}

	upstream := &cm.config.VectorsUpstream[upstreamIndex]
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

// MoveVectorsAPIKeyToBottom 将指定 Vectors 渠道的 API 密钥移到最后面
func (cm *ConfigManager) MoveVectorsAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upstreamIndex < 0 || upstreamIndex >= len(cm.config.VectorsUpstream) {
		return fmt.Errorf("无效的上游索引: %d", upstreamIndex)
	}

	upstream := &cm.config.VectorsUpstream[upstreamIndex]
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

// ReorderVectorsUpstreams 重新排序 Vectors 渠道优先级
// order 是渠道索引数组，按新的优先级顺序排列（只更新传入的渠道，支持部分排序）
// priorities 可选：与 order 平行的显式优先级值，缺省按位次 1..N
func (cm *ConfigManager) ReorderVectorsUpstreams(order []int, priorities ...[]int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if len(order) == 0 {
		return fmt.Errorf("排序数组不能为空")
	}

	seen := make(map[int]bool)
	for _, idx := range order {
		if idx < 0 || idx >= len(cm.config.VectorsUpstream) {
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
		cm.config.VectorsUpstream[idx].Priority = values[i]
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Reorder] 已更新 Vectors 渠道优先级顺序 (%d 个渠道)", len(order))
	return nil
}

// SetVectorsChannelStatus 设置 Vectors 渠道状态
func (cm *ConfigManager) SetVectorsChannelStatus(index int, status string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.VectorsUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	status = strings.ToLower(status)
	if status != "active" && status != "suspended" && status != "disabled" {
		return fmt.Errorf("无效的状态: %s (允许值: active, suspended, disabled)", status)
	}

	promotionCleared := applyAdministrativeChannelStatus(&cm.config.VectorsUpstream[index], status)

	if promotionCleared {
		log.Printf("[Config-Status] 已清除 Vectors 渠道 [%d] %s 的促销期", index, cm.config.VectorsUpstream[index].Name)
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Status] 已设置 Vectors 渠道 [%d] %s 状态为: %s", index, cm.config.VectorsUpstream[index].Name, status)
	return nil
}

// SetVectorsChannelPromotion 设置 Vectors 渠道促销期
func (cm *ConfigManager) SetVectorsChannelPromotion(index int, duration time.Duration) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.VectorsUpstream) {
		return fmt.Errorf("无效的 Vectors 上游索引: %d", index)
	}

	if duration <= 0 {
		cm.config.VectorsUpstream[index].PromotionUntil = nil
		log.Printf("[Config-Promotion] 已清除 Vectors 渠道 [%d] %s 的促销期", index, cm.config.VectorsUpstream[index].Name)
	} else {
		for i := range cm.config.VectorsUpstream {
			if i != index && cm.config.VectorsUpstream[i].PromotionUntil != nil {
				cm.config.VectorsUpstream[i].PromotionUntil = nil
			}
		}
		promotionEnd := time.Now().Add(duration)
		cm.config.VectorsUpstream[index].PromotionUntil = &promotionEnd
		log.Printf("[Config-Promotion] 已设置 Vectors 渠道 [%d] %s 进入促销期，截止: %s", index, cm.config.VectorsUpstream[index].Name, promotionEnd.Format(time.RFC3339))
	}

	return cm.saveConfigLocked(cm.config)
}

// GetPromotedVectorsChannel 获取当前处于促销期的 Vectors 渠道索引
func (cm *ConfigManager) GetPromotedVectorsChannel() (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	for i, upstream := range cm.config.VectorsUpstream {
		if IsChannelInPromotion(&upstream) && GetChannelStatus(&upstream) == "active" {
			return i, true
		}
	}
	return -1, false
}

// UpdateVectorsModelMapping 更新指定 Vectors 上游的单个模型映射
func (cm *ConfigManager) UpdateVectorsModelMapping(index int, sourcePattern, targetModel, reasoning string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.VectorsUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	upstream := &cm.config.VectorsUpstream[index]
	if upstream.AutoManaged {
		return fmt.Errorf("渠道 [%d] %s 为自动托管渠道，模型映射由 Autopilot 自动解析，不支持手工编辑", index, upstream.Name)
	}

	// sourcePattern 不存在时插入（upsert 语义），存在时更新；
	// 能力测试「创建映射」等场景需要向空映射中新增键。
	if upstream.ModelMapping == nil {
		upstream.ModelMapping = make(map[string]string)
	}

	// 验证 reasoning 值（Vectors 一般不使用 reasoning，但为了接口一致性保留）
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

	log.Printf("[Config-Vectors] 已更新上游 [%d] %s 的模型映射: %s -> %s (reasoning: %s)",
		index, upstream.Name, sourcePattern, targetModel, reasoning)
	return nil
}
