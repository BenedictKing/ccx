package config

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// ============== Images 渠道方法 ==============

// normalizeImagesServiceType 规范化并校验 Images 渠道 serviceType。
func normalizeImagesServiceType(serviceType string) (string, error) {
	normalized := normalizeUpstreamServiceType(serviceType, "openai")
	if normalized != "openai" {
		return "", &ConfigError{Message: fmt.Sprintf("Images 渠道仅支持 openai serviceType，当前为 %s", normalized)}
	}
	return normalized, nil
}

// NormalizeImagesServiceTypeForProxy 供代理层复用 Images 渠道 serviceType 校验。
func NormalizeImagesServiceTypeForProxy(serviceType string) (string, error) {
	return normalizeImagesServiceType(serviceType)
}

// GetCurrentImagesUpstream 获取当前 Images 上游配置
// 优先选择第一个 active 状态的渠道，若无则回退到第一个渠道
func (cm *ConfigManager) GetCurrentImagesUpstream() (*UpstreamConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.config.ImagesUpstream) == 0 {
		return nil, fmt.Errorf("未配置任何 Images 渠道")
	}

	// 优先选择第一个 active 状态的渠道
	for i := range cm.config.ImagesUpstream {
		status := cm.config.ImagesUpstream[i].Status
		if status == "" || status == "active" {
			return &cm.config.ImagesUpstream[i], nil
		}
	}

	// 没有 active 渠道，回退到第一个渠道
	return &cm.config.ImagesUpstream[0], nil
}

// GetCurrentImagesUpstreamWithIndex 获取当前 Images 上游配置及其索引
func (cm *ConfigManager) GetCurrentImagesUpstreamWithIndex() (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.config.ImagesUpstream) == 0 {
		return nil, 0, fmt.Errorf("未配置任何 Images 渠道")
	}

	for i := range cm.config.ImagesUpstream {
		status := cm.config.ImagesUpstream[i].Status
		if status == "" || status == "active" {
			return &cm.config.ImagesUpstream[i], i, nil
		}
	}

	return &cm.config.ImagesUpstream[0], 0, nil
}

// AddImagesUpstream 添加 Images 上游
// placements 可选传 "front"（故障转移序列首位），缺省为追加到序列末尾（见 assignChannelPriority）
func (cm *ConfigManager) AddImagesUpstream(upstream UpstreamConfig, placements ...string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 检查 Name 是否已存在
	for _, existing := range cm.config.ImagesUpstream {
		if existing.Name == upstream.Name {
			return fmt.Errorf("渠道名称 '%s' 已存在", upstream.Name)
		}
	}

	// 新建渠道默认设为 active
	if upstream.Status == "" {
		upstream.Status = "active"
	}

	upstream.ServiceType = normalizeUpstreamServiceType(upstream.ServiceType, "openai")

	serviceType, err := normalizeImagesServiceType(upstream.ServiceType)
	if err != nil {
		return err
	}
	upstream.ServiceType = serviceType
	authHeader, err := applyAuthHeader(upstream.AuthHeader)
	if err != nil {
		return err
	}
	upstream.AuthHeader = authHeader
	if err := validateRequestTimeoutMs(upstream.RequestTimeoutMs); err != nil {
		return err
	}
	if err := validateResponseHeaderTimeoutMs(upstream.ResponseHeaderTimeoutMs); err != nil {
		return err
	}
	if upstream.RateLimitRPM < 0 || upstream.RateLimitBurst < 0 || upstream.RateLimitMaxConcurrent < 0 {
		return fmt.Errorf("限速参数不能为负数")
	}
	if err := validateStreamTimeouts(upstream.StreamFirstContentTimeoutMs, upstream.StreamInactivityTimeoutMs, upstream.StreamToolCallIdleTimeoutMs); err != nil {
		return err
	}

	// 去重 API Keys 和 Base URLs
	upstream.APIKeys = deduplicateStrings(upstream.APIKeys)
	upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)
	upstream.BaseURL = utils.CanonicalBaseURL(upstream.BaseURL, upstream.ServiceType)
	upstream.BaseURLs = deduplicateBaseURLs(upstream.BaseURLs, upstream.ServiceType)

	upstream.ModelMapping, _ = sanitizeDeprecatedGrokModelMapping(upstream.ModelMapping)
	assignChannelPriority(cm.config.ImagesUpstream, &upstream, resolvePlacement(placements))
	cm.config.ImagesUpstream = append([]UpstreamConfig{upstream}, cm.config.ImagesUpstream...)

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Upstream] 已添加 Images 上游: %s", upstream.Name)
	return nil
}

// UpdateImagesUpstream 更新 Images 上游
// 返回值：shouldResetMetrics 表示是否需要重置渠道指标（熔断状态）
func (cm *ConfigManager) UpdateImagesUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return false, fmt.Errorf("无效的 Images 上游索引: %d", index)
	}

	// 保存修改前的配置快照用于变更检测
	originalConfig := cm.config.deepCopy()

	upstream := &cm.config.ImagesUpstream[index]
	serviceType, err := normalizeImagesServiceType(upstream.ServiceType)
	if err != nil {
		return false, err
	}
	upstream.ServiceType = serviceType
	if updates.ServiceType != nil {
		serviceType, err = normalizeImagesServiceType(*updates.ServiceType)
		if err != nil {
			return false, err
		}
	}

	if updates.Name != nil {
		upstream.Name = *updates.Name
	}
	if updates.BaseURL != nil {
		upstream.BaseURL = utils.CanonicalBaseURL(*updates.BaseURL, serviceType)
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
	if updates.AuthHeader != nil {
		authHeader, err := applyAuthHeader(*updates.AuthHeader)
		if err != nil {
			return false, err
		}
		upstream.AuthHeader = authHeader
	}
	if updates.Description != nil {
		upstream.Description = *updates.Description
	}
	if updates.Website != nil {
		upstream.Website = *updates.Website
	}
	if updates.APIKeys != nil {
		newKeys := make(map[string]bool)
		for _, key := range updates.APIKeys {
			newKeys[key] = true
		}

		for _, key := range upstream.APIKeys {
			if !newKeys[key] {
				alreadyInHistory := false
				for _, hk := range upstream.HistoricalAPIKeys {
					if hk == key {
						alreadyInHistory = true
						break
					}
				}
				if !alreadyInHistory {
					upstream.HistoricalAPIKeys = append(upstream.HistoricalAPIKeys, key)
					log.Printf("[Config-Upstream] Images 渠道 [%d] %s: Key %s 已移入历史列表", index, upstream.Name, utils.MaskAPIKey(key))
				}
			}
		}

		var newHistoricalKeys []string
		for _, hk := range upstream.HistoricalAPIKeys {
			if !newKeys[hk] {
				newHistoricalKeys = append(newHistoricalKeys, hk)
			} else {
				log.Printf("[Config-Upstream] Images 渠道 [%d] %s: Key %s 已从历史列表恢复", index, upstream.Name, utils.MaskAPIKey(hk))
			}
		}
		upstream.HistoricalAPIKeys = newHistoricalKeys

		wasSuspended := upstream.Status == "suspended"
		if applySingleKeyReplacementTransition(upstream, updates.APIKeys) {
			shouldResetMetrics = true
			if wasSuspended {
				log.Printf("[Config-Upstream] Images 渠道 [%d] %s 已从暂停状态自动激活（单 key 更换）", index, upstream.Name)
			}
		}
		upstream.APIKeys = deduplicateStrings(updates.APIKeys)
	}
	applyAPIKeyConfigUpdate(upstream, updates)
	if updates.ModelMapping != nil {
		upstream.ModelMapping = updates.ModelMapping
	}
	upstream.ModelMapping, _ = sanitizeDeprecatedGrokModelMapping(upstream.ModelMapping)
	applyModelCapabilityUpdates(upstream, updates)
	if updates.ReasoningMapping != nil {
		upstream.ReasoningMapping = updates.ReasoningMapping
	}
	if updates.ReasoningParamStyle != nil {
		upstream.ReasoningParamStyle = *updates.ReasoningParamStyle
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
	if updates.StripBillingHeader != nil {
		v := *updates.StripBillingHeader
		upstream.StripBillingHeader = &v
	}
	if updates.CodexToolCompat != nil {
		v := *updates.CodexToolCompat
		upstream.CodexToolCompat = &v
	}
	if updates.ConvertImageURLToB64JSON != nil {
		upstream.ConvertImageURLToB64JSON = *updates.ConvertImageURLToB64JSON
	}
	if updates.CustomHeaders != nil {
		upstream.CustomHeaders = updates.CustomHeaders
	}
	if updates.ProxyURL != nil {
		upstream.ProxyURL = *updates.ProxyURL
	}
	if updates.RequestTimeoutMs != nil {
		if err := validateRequestTimeoutMs(*updates.RequestTimeoutMs); err != nil {
			return false, err
		}
		upstream.RequestTimeoutMs = *updates.RequestTimeoutMs
	}
	if updates.ResponseHeaderTimeoutMs != nil {
		if err := validateResponseHeaderTimeoutMs(*updates.ResponseHeaderTimeoutMs); err != nil {
			return false, err
		}
		upstream.ResponseHeaderTimeoutMs = *updates.ResponseHeaderTimeoutMs
	}
	if updates.StreamFirstContentTimeoutMs != nil {
		if err := validateStreamFirstContentTimeoutMs(*updates.StreamFirstContentTimeoutMs); err != nil {
			return false, err
		}
		upstream.StreamFirstContentTimeoutMs = *updates.StreamFirstContentTimeoutMs
	}
	if updates.StreamInactivityTimeoutMs != nil {
		if err := validateStreamInactivityTimeoutMs(*updates.StreamInactivityTimeoutMs); err != nil {
			return false, err
		}
		upstream.StreamInactivityTimeoutMs = *updates.StreamInactivityTimeoutMs
	}
	if updates.StreamToolCallIdleTimeoutMs != nil {
		if err := validateStreamToolCallIdleTimeoutMs(*updates.StreamToolCallIdleTimeoutMs); err != nil {
			return false, err
		}
		upstream.StreamToolCallIdleTimeoutMs = *updates.StreamToolCallIdleTimeoutMs
	}
	if updates.SupportedModels != nil {
		upstream.SupportedModels = updates.SupportedModels
	}
	if updates.RoutePrefix != nil {
		upstream.RoutePrefix = *updates.RoutePrefix
	}
	if updates.NoVision != nil {
		upstream.NoVision = *updates.NoVision
	}
	if updates.NoVisionModels != nil {
		upstream.NoVisionModels = updates.NoVisionModels
	}
	if updates.VisionFallbackModel != nil {
		upstream.VisionFallbackModel = *updates.VisionFallbackModel
	}
	if updates.RateLimitRPM != nil {
		if *updates.RateLimitRPM < 0 {
			return false, fmt.Errorf("rateLimitRpm 不能为负数")
		}
		upstream.RateLimitRPM = *updates.RateLimitRPM
	}
	if updates.RateLimitWindowMinutes != nil {
		if *updates.RateLimitWindowMinutes < 0 {
			return false, fmt.Errorf("rateLimitWindowMinutes 不能为负数")
		}
		upstream.RateLimitWindowMinutes = *updates.RateLimitWindowMinutes
	}
	if updates.RateLimitBurst != nil {
		if *updates.RateLimitBurst < 0 {
			return false, fmt.Errorf("rateLimitBurst 不能为负数")
		}
		upstream.RateLimitBurst = *updates.RateLimitBurst
	}
	if updates.RateLimitMaxConcurrent != nil {
		if *updates.RateLimitMaxConcurrent < 0 {
			return false, fmt.Errorf("rateLimitMaxConcurrent 不能为负数")
		}
		upstream.RateLimitMaxConcurrent = *updates.RateLimitMaxConcurrent
	}
	if updates.RateLimitAutoFromHeaders != nil {
		v := *updates.RateLimitAutoFromHeaders
		upstream.RateLimitAutoFromHeaders = &v
	}
	if updates.HistoricalImageTurnLimit != nil {
		upstream.HistoricalImageTurnLimit = NormalizeChannelHistoricalImageTurnLimit(*updates.HistoricalImageTurnLimit)

	}
	if updates.AutoManaged != nil {
		upstream.AutoManaged = *updates.AutoManaged
	}
	if updates.AutoManagedAt != nil {
		upstream.AutoManagedAt = updates.AutoManagedAt
	}
	if updates.Tags != nil {
		// 去重并裁剪空白，空切片表示清空标签
		seen := make(map[string]struct{}, len(updates.Tags))
		cleaned := make([]string, 0, len(updates.Tags))
		for _, t := range updates.Tags {
			trimmed := strings.TrimSpace(t)
			if trimmed == "" {
				continue
			}
			if _, dup := seen[trimmed]; dup {
				continue
			}
			seen[trimmed] = struct{}{}
			cleaned = append(cleaned, trimmed)
		}
		upstream.Tags = cleaned
	}

	// 检测配置是否真的发生了变化
	if !cm.hasConfigChanged(originalConfig, cm.config) {
		log.Printf("[Config-Upstream] Images 渠道 [%d] %s 配置未发生实质性变化，跳过保存", index, upstream.Name)
		return shouldResetMetrics, nil
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return false, err
	}

	log.Printf("[Config-Upstream] 已更新 Images 上游: [%d] %s", index, cm.config.ImagesUpstream[index].Name)
	return shouldResetMetrics, nil
}

// RemoveImagesUpstream 删除 Images 上游
func (cm *ConfigManager) RemoveImagesUpstream(index int) (*UpstreamConfig, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return nil, fmt.Errorf("无效的 Images 上游索引: %d", index)
	}

	removed := cm.config.ImagesUpstream[index]
	cm.config.ImagesUpstream = append(cm.config.ImagesUpstream[:index], cm.config.ImagesUpstream[index+1:]...)

	cm.clearFailedKeysForUpstream(&removed, "Images")

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}

	log.Printf("[Config-Upstream] 已删除 Images 上游: %s", removed.Name)
	return &removed, nil
}

// AddImagesAPIKey 添加 Images 上游的 API 密钥
func (cm *ConfigManager) AddImagesAPIKey(index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	for _, key := range cm.config.ImagesUpstream[index].APIKeys {
		if key == apiKey {
			return fmt.Errorf("API密钥已存在")
		}
	}

	cm.config.ImagesUpstream[index].APIKeys = append(cm.config.ImagesUpstream[index].APIKeys, apiKey)

	var newDisabledKeys []DisabledKeyInfo
	for _, dk := range cm.config.ImagesUpstream[index].DisabledAPIKeys {
		if dk.Key != apiKey {
			newDisabledKeys = append(newDisabledKeys, dk)
		}
	}
	cm.config.ImagesUpstream[index].DisabledAPIKeys = newDisabledKeys
	removeDisabledKeyModelsForKey(&cm.config.ImagesUpstream[index], apiKey)

	var newHistoricalKeys []string
	for _, hk := range cm.config.ImagesUpstream[index].HistoricalAPIKeys {
		if hk != apiKey {
			newHistoricalKeys = append(newHistoricalKeys, hk)
		} else {
			log.Printf("[Images-Key] 上游 [%d] %s: Key %s 已从历史列表恢复", index, cm.config.ImagesUpstream[index].Name, utils.MaskAPIKey(hk))
		}
	}
	cm.config.ImagesUpstream[index].HistoricalAPIKeys = newHistoricalKeys

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Images-Key] 已添加API密钥到 Images 上游 [%d] %s", index, cm.config.ImagesUpstream[index].Name)
	return nil
}

// RemoveImagesAPIKey 删除 Images 上游的 API 密钥
func (cm *ConfigManager) RemoveImagesAPIKey(index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	keys := cm.config.ImagesUpstream[index].APIKeys
	found := false
	for i, key := range keys {
		if key == apiKey {
			cm.config.ImagesUpstream[index].APIKeys = append(keys[:i], keys[i+1:]...)
			found = true
			break
		}
	}

	// 已拉黑的 Key 不在活跃列表中，允许连同禁用记录一起删除
	if cm.removeDisabledKeyEntryLocked(&cm.config.ImagesUpstream[index], "Images", apiKey) {
		found = true
	}

	if !found {
		return fmt.Errorf("API密钥不存在")
	}

	alreadyInHistory := false
	for _, hk := range cm.config.ImagesUpstream[index].HistoricalAPIKeys {
		if hk == apiKey {
			alreadyInHistory = true
			break
		}
	}
	if !alreadyInHistory {
		cm.config.ImagesUpstream[index].HistoricalAPIKeys = append(cm.config.ImagesUpstream[index].HistoricalAPIKeys, apiKey)
		log.Printf("[Images-Key] 上游 [%d] %s: Key %s 已移入历史列表", index, cm.config.ImagesUpstream[index].Name, utils.MaskAPIKey(apiKey))
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Images-Key] 已从 Images 上游 [%d] %s 删除API密钥", index, cm.config.ImagesUpstream[index].Name)
	return nil
}

// GetNextImagesAPIKey 获取下一个 Images API 密钥（纯 failover 模式）
func (cm *ConfigManager) GetNextImagesAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Images")
}

// MoveImagesAPIKeyToTop 将指定 Images 渠道的 API 密钥移到最前面
func (cm *ConfigManager) MoveImagesAPIKeyToTop(upstreamIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upstreamIndex < 0 || upstreamIndex >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("无效的上游索引: %d", upstreamIndex)
	}

	upstream := &cm.config.ImagesUpstream[upstreamIndex]
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

// MoveImagesAPIKeyToBottom 将指定 Images 渠道的 API 密钥移到最后面
func (cm *ConfigManager) MoveImagesAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upstreamIndex < 0 || upstreamIndex >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("无效的上游索引: %d", upstreamIndex)
	}

	upstream := &cm.config.ImagesUpstream[upstreamIndex]
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

// ReorderImagesUpstreams 重新排序 Images 渠道优先级
// order 是渠道索引数组，按新的优先级顺序排列（只更新传入的渠道，支持部分排序）
func (cm *ConfigManager) ReorderImagesUpstreams(order []int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if len(order) == 0 {
		return fmt.Errorf("排序数组不能为空")
	}

	seen := make(map[int]bool)
	for _, idx := range order {
		if idx < 0 || idx >= len(cm.config.ImagesUpstream) {
			return fmt.Errorf("无效的渠道索引: %d", idx)
		}
		if seen[idx] {
			return fmt.Errorf("重复的渠道索引: %d", idx)
		}
		seen[idx] = true
	}

	for i, idx := range order {
		cm.config.ImagesUpstream[idx].Priority = i + 1
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Reorder] 已更新 Images 渠道优先级顺序 (%d 个渠道)", len(order))
	return nil
}

// SetImagesChannelStatus 设置 Images 渠道状态
func (cm *ConfigManager) SetImagesChannelStatus(index int, status string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	status = strings.ToLower(status)
	if status != "active" && status != "suspended" && status != "disabled" {
		return fmt.Errorf("无效的状态: %s (允许值: active, suspended, disabled)", status)
	}

	cm.config.ImagesUpstream[index].Status = status

	if status == "suspended" && cm.config.ImagesUpstream[index].PromotionUntil != nil {
		cm.config.ImagesUpstream[index].PromotionUntil = nil
		log.Printf("[Config-Status] 已清除 Images 渠道 [%d] %s 的促销期", index, cm.config.ImagesUpstream[index].Name)
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Status] 已设置 Images 渠道 [%d] %s 状态为: %s", index, cm.config.ImagesUpstream[index].Name, status)
	return nil
}

// SetImagesChannelPromotion 设置 Images 渠道促销期
func (cm *ConfigManager) SetImagesChannelPromotion(index int, duration time.Duration) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("无效的 Images 上游索引: %d", index)
	}

	if duration <= 0 {
		cm.config.ImagesUpstream[index].PromotionUntil = nil
		log.Printf("[Config-Promotion] 已清除 Images 渠道 [%d] %s 的促销期", index, cm.config.ImagesUpstream[index].Name)
	} else {
		for i := range cm.config.ImagesUpstream {
			if i != index && cm.config.ImagesUpstream[i].PromotionUntil != nil {
				cm.config.ImagesUpstream[i].PromotionUntil = nil
			}
		}
		promotionEnd := time.Now().Add(duration)
		cm.config.ImagesUpstream[index].PromotionUntil = &promotionEnd
		log.Printf("[Config-Promotion] 已设置 Images 渠道 [%d] %s 进入促销期，截止: %s", index, cm.config.ImagesUpstream[index].Name, promotionEnd.Format(time.RFC3339))
	}

	return cm.saveConfigLocked(cm.config)
}

// GetPromotedImagesChannel 获取当前处于促销期的 Images 渠道索引
func (cm *ConfigManager) GetPromotedImagesChannel() (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	for i, upstream := range cm.config.ImagesUpstream {
		if IsChannelInPromotion(&upstream) && GetChannelStatus(&upstream) == "active" {
			return i, true
		}
	}
	return -1, false
}

// UpdateImagesModelMapping 更新指定 Images 上游的单个模型映射
func (cm *ConfigManager) UpdateImagesModelMapping(index int, sourcePattern, targetModel, reasoning string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}

	upstream := &cm.config.ImagesUpstream[index]

	// 检查 sourcePattern 是否存在
	if upstream.ModelMapping == nil {
		return fmt.Errorf("源模型匹配模式 '%s' 不存在", sourcePattern)
	}
	if _, exists := upstream.ModelMapping[sourcePattern]; !exists {
		return fmt.Errorf("源模型匹配模式 '%s' 不存在", sourcePattern)
	}

	// 验证 reasoning 值（Images 一般不使用 reasoning，但为了接口一致性保留）
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

	log.Printf("[Config-Images] 已更新上游 [%d] %s 的模型映射: %s -> %s (reasoning: %s)",
		index, upstream.Name, sourcePattern, targetModel, reasoning)
	return nil
}
