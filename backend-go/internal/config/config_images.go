package config

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// ============== Images ???? ==============

// normalizeImagesServiceType ?????? Images ?? serviceType?
func normalizeImagesServiceType(serviceType string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(serviceType))
	if normalized == "" {
		normalized = "openai"
	}
	if normalized != "openai" {
		return "", &ConfigError{Message: fmt.Sprintf("Images ????? openai serviceType???? %s", normalized)}
	}
	return normalized, nil
}

// NormalizeImagesServiceTypeForProxy ?????? Images ?? serviceType ???
func NormalizeImagesServiceTypeForProxy(serviceType string) (string, error) {
	return normalizeImagesServiceType(serviceType)
}

// GetCurrentImagesUpstream ???? Images ????
// ??????? active ?????????????????
func (cm *ConfigManager) GetCurrentImagesUpstream() (*UpstreamConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.config.ImagesUpstream) == 0 {
		return nil, fmt.Errorf("????? Images ??")
	}

	// ??????? active ?????
	for i := range cm.config.ImagesUpstream {
		status := cm.config.ImagesUpstream[i].Status
		if status == "" || status == "active" {
			return &cm.config.ImagesUpstream[i], nil
		}
	}

	// ?? active ???????????
	return &cm.config.ImagesUpstream[0], nil
}

// GetCurrentImagesUpstreamWithIndex ???? Images ????????
func (cm *ConfigManager) GetCurrentImagesUpstreamWithIndex() (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.config.ImagesUpstream) == 0 {
		return nil, 0, fmt.Errorf("????? Images ??")
	}

	for i := range cm.config.ImagesUpstream {
		status := cm.config.ImagesUpstream[i].Status
		if status == "" || status == "active" {
			return &cm.config.ImagesUpstream[i], i, nil
		}
	}

	return &cm.config.ImagesUpstream[0], 0, nil
}

// AddImagesUpstream ?? Images ??
func (cm *ConfigManager) AddImagesUpstream(upstream UpstreamConfig) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// ?? Name ?????
	for _, existing := range cm.config.ImagesUpstream {
		if existing.Name == upstream.Name {
			return fmt.Errorf("???? '%s' ???", upstream.Name)
		}
	}

	// ???????? active
	if upstream.Status == "" {
		upstream.Status = "active"
	}

	serviceType, err := normalizeImagesServiceType(upstream.ServiceType)
	if err != nil {
		return err
	}
	upstream.ServiceType = serviceType

	// ?? API Keys ? Base URLs
	upstream.APIKeys = deduplicateStrings(upstream.APIKeys)
	upstream.BaseURLs = deduplicateBaseURLs(upstream.BaseURLs)

	cm.config.ImagesUpstream = append(cm.config.ImagesUpstream, upstream)

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Upstream] ??? Images ??: %s", upstream.Name)
	return nil
}

// UpdateImagesUpstream ?? Images ??
// ????shouldResetMetrics ??????????????????
func (cm *ConfigManager) UpdateImagesUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return false, fmt.Errorf("??? Images ????: %d", index)
	}

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
		upstream.BaseURL = *updates.BaseURL
		if updates.BaseURLs == nil {
			upstream.BaseURLs = nil
		}
	}
	if updates.BaseURLs != nil {
		upstream.BaseURLs = deduplicateBaseURLs(updates.BaseURLs)
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
					log.Printf("[Config-Upstream] Images ?? [%d] %s: Key %s ???????", index, upstream.Name, utils.MaskAPIKey(key))
				}
			}
		}

		var newHistoricalKeys []string
		for _, hk := range upstream.HistoricalAPIKeys {
			if !newKeys[hk] {
				newHistoricalKeys = append(newHistoricalKeys, hk)
			} else {
				log.Printf("[Config-Upstream] Images ?? [%d] %s: Key %s ????????", index, upstream.Name, utils.MaskAPIKey(hk))
			}
		}
		upstream.HistoricalAPIKeys = newHistoricalKeys

		if len(upstream.APIKeys) == 1 && len(updates.APIKeys) == 1 &&
			upstream.APIKeys[0] != updates.APIKeys[0] {
			shouldResetMetrics = true
			if upstream.Status == "suspended" {
				upstream.Status = "active"
				log.Printf("[Config-Upstream] Images ?? [%d] %s ???????????? key ???", index, upstream.Name)
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
	if updates.CustomHeaders != nil {
		upstream.CustomHeaders = updates.CustomHeaders
	}
	if updates.ProxyURL != nil {
		upstream.ProxyURL = *updates.ProxyURL
	}
	if updates.SupportedModels != nil {
		upstream.SupportedModels = updates.SupportedModels
	}
	if updates.RoutePrefix != nil {
		upstream.RoutePrefix = *updates.RoutePrefix
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return false, err
	}

	log.Printf("[Config-Upstream] ??? Images ??: [%d] %s", index, cm.config.ImagesUpstream[index].Name)
	return shouldResetMetrics, nil
}

// RemoveImagesUpstream ?? Images ??
func (cm *ConfigManager) RemoveImagesUpstream(index int) (*UpstreamConfig, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return nil, fmt.Errorf("??? Images ????: %d", index)
	}

	removed := cm.config.ImagesUpstream[index]
	cm.config.ImagesUpstream = append(cm.config.ImagesUpstream[:index], cm.config.ImagesUpstream[index+1:]...)

	cm.clearFailedKeysForUpstream(&removed, "Images")

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}

	log.Printf("[Config-Upstream] ??? Images ??: %s", removed.Name)
	return &removed, nil
}

// AddImagesAPIKey ?? Images ??? API ??
func (cm *ConfigManager) AddImagesAPIKey(index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("???????: %d", index)
	}

	for _, key := range cm.config.ImagesUpstream[index].APIKeys {
		if key == apiKey {
			return fmt.Errorf("API?????")
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

	var newHistoricalKeys []string
	for _, hk := range cm.config.ImagesUpstream[index].HistoricalAPIKeys {
		if hk != apiKey {
			newHistoricalKeys = append(newHistoricalKeys, hk)
		} else {
			log.Printf("[Images-Key] ?? [%d] %s: Key %s ????????", index, cm.config.ImagesUpstream[index].Name, utils.MaskAPIKey(hk))
		}
	}
	cm.config.ImagesUpstream[index].HistoricalAPIKeys = newHistoricalKeys

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Images-Key] ???API??? Images ?? [%d] %s", index, cm.config.ImagesUpstream[index].Name)
	return nil
}

// RemoveImagesAPIKey ?? Images ??? API ??
func (cm *ConfigManager) RemoveImagesAPIKey(index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("???????: %d", index)
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

	if !found {
		return fmt.Errorf("API?????")
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
		log.Printf("[Images-Key] ?? [%d] %s: Key %s ???????", index, cm.config.ImagesUpstream[index].Name, utils.MaskAPIKey(apiKey))
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Images-Key] ?? Images ?? [%d] %s ??API??", index, cm.config.ImagesUpstream[index].Name)
	return nil
}

// GetNextImagesAPIKey ????? Images API ???? failover ???
func (cm *ConfigManager) GetNextImagesAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Images")
}

// MoveImagesAPIKeyToTop ??? Images ??? API ???????
func (cm *ConfigManager) MoveImagesAPIKeyToTop(upstreamIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upstreamIndex < 0 || upstreamIndex >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("???????: %d", upstreamIndex)
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

// MoveImagesAPIKeyToBottom ??? Images ??? API ???????
func (cm *ConfigManager) MoveImagesAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upstreamIndex < 0 || upstreamIndex >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("???????: %d", upstreamIndex)
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

// ReorderImagesUpstreams ???? Images ?????
// order ???????????????????????????????????
func (cm *ConfigManager) ReorderImagesUpstreams(order []int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if len(order) == 0 {
		return fmt.Errorf("????????")
	}

	seen := make(map[int]bool)
	for _, idx := range order {
		if idx < 0 || idx >= len(cm.config.ImagesUpstream) {
			return fmt.Errorf("???????: %d", idx)
		}
		if seen[idx] {
			return fmt.Errorf("???????: %d", idx)
		}
		seen[idx] = true
	}

	for i, idx := range order {
		cm.config.ImagesUpstream[idx].Priority = i + 1
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Reorder] ??? Images ??????? (%d ???)", len(order))
	return nil
}

// SetImagesChannelStatus ?? Images ????
func (cm *ConfigManager) SetImagesChannelStatus(index int, status string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("???????: %d", index)
	}

	status = strings.ToLower(status)
	if status != "active" && status != "suspended" && status != "disabled" {
		return fmt.Errorf("?????: %s (???: active, suspended, disabled)", status)
	}

	cm.config.ImagesUpstream[index].Status = status

	if status == "suspended" && cm.config.ImagesUpstream[index].PromotionUntil != nil {
		cm.config.ImagesUpstream[index].PromotionUntil = nil
		log.Printf("[Config-Status] ??? Images ?? [%d] %s ????", index, cm.config.ImagesUpstream[index].Name)
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Status] ??? Images ?? [%d] %s ???: %s", index, cm.config.ImagesUpstream[index].Name, status)
	return nil
}

// SetImagesChannelPromotion ?? Images ?????
func (cm *ConfigManager) SetImagesChannelPromotion(index int, duration time.Duration) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if index < 0 || index >= len(cm.config.ImagesUpstream) {
		return fmt.Errorf("??? Images ????: %d", index)
	}

	if duration <= 0 {
		cm.config.ImagesUpstream[index].PromotionUntil = nil
		log.Printf("[Config-Promotion] ??? Images ?? [%d] %s ????", index, cm.config.ImagesUpstream[index].Name)
	} else {
		for i := range cm.config.ImagesUpstream {
			if i != index && cm.config.ImagesUpstream[i].PromotionUntil != nil {
				cm.config.ImagesUpstream[i].PromotionUntil = nil
			}
		}
		promotionEnd := time.Now().Add(duration)
		cm.config.ImagesUpstream[index].PromotionUntil = &promotionEnd
		log.Printf("[Config-Promotion] ??? Images ?? [%d] %s ????????: %s", index, cm.config.ImagesUpstream[index].Name, promotionEnd.Format(time.RFC3339))
	}

	return cm.saveConfigLocked(cm.config)
}

// GetPromotedImagesChannel ?????????? Images ????
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
