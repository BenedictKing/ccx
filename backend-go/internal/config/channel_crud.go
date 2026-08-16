package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/BenedictKing/ccx/internal/utils"
)

// ChannelKind 是六类协议入口的标识，用于在通用 CRUD 方法中区分目标数组。
type ChannelKind string

const (
	ChannelKindMessages  ChannelKind = "messages"
	ChannelKindChat      ChannelKind = "chat"
	ChannelKindResponses ChannelKind = "responses"
	ChannelKindGemini    ChannelKind = "gemini"
	ChannelKindImages    ChannelKind = "images"
	ChannelKindVectors   ChannelKind = "vectors"
)

// ChannelKindConfig 描述每个协议 kind 在六数组权威模型下的配置差异。
// 本结构用于把 ConfigManager 中六套重复的 Add*/Update*/Remove* 方法收敛为通用实现。
type ChannelKindConfig struct {
	Kind               ChannelKind
	DefaultServiceType string
	// ValidateServiceType 对最终 serviceType 做额外校验；nil 表示无额外限制。
	ValidateServiceType func(string) error
	// SliceRef 返回当前配置中对应 kind 的数组引用，便于通用方法直接修改。
	SliceRef func(*Config) *[]UpstreamConfig
	// Slice 返回当前配置中对应 kind 的数组只读副本（用于日志/查找）。
	Slice func(Config) []UpstreamConfig
	// AddCapabilityValidation 对新建/更新的 UpstreamConfig 做 kind 专属校验。
	AddCapabilityValidation func(UpstreamConfig) error
	// UpdateCapabilityValidation 对 Update 请求中的 UpstreamUpdate 做 kind 专属校验。
	UpdateCapabilityValidation func(UpstreamUpdate) error
}

// ChannelKindRegistry 六类协议 kind 的配置注册表。
// 注意：顺序影响日志输出，不影响业务语义。
var ChannelKindRegistry = map[ChannelKind]ChannelKindConfig{
	ChannelKindMessages: {
		Kind:               ChannelKindMessages,
		DefaultServiceType: "claude",
		SliceRef:           func(cfg *Config) *[]UpstreamConfig { return &cfg.Upstream },
		Slice:              func(cfg Config) []UpstreamConfig { return cfg.Upstream },
	},
	ChannelKindChat: {
		Kind:               ChannelKindChat,
		DefaultServiceType: "openai",
		SliceRef:           func(cfg *Config) *[]UpstreamConfig { return &cfg.ChatUpstream },
		Slice:              func(cfg Config) []UpstreamConfig { return cfg.ChatUpstream },
	},
	ChannelKindResponses: {
		Kind:               ChannelKindResponses,
		DefaultServiceType: "responses",
		SliceRef:           func(cfg *Config) *[]UpstreamConfig { return &cfg.ResponsesUpstream },
		Slice:              func(cfg Config) []UpstreamConfig { return cfg.ResponsesUpstream },
	},
	ChannelKindGemini: {
		Kind:               ChannelKindGemini,
		DefaultServiceType: "gemini",
		SliceRef:           func(cfg *Config) *[]UpstreamConfig { return &cfg.GeminiUpstream },
		Slice:              func(cfg Config) []UpstreamConfig { return cfg.GeminiUpstream },
	},
	ChannelKindImages: {
		Kind:               ChannelKindImages,
		DefaultServiceType: "openai",
		ValidateServiceType: func(st string) error {
			if st != "openai" {
				return &ConfigError{Message: fmt.Sprintf("Images 渠道仅支持 openai serviceType，当前为 %s", st)}
			}
			return nil
		},
		SliceRef: func(cfg *Config) *[]UpstreamConfig { return &cfg.ImagesUpstream },
		Slice:    func(cfg Config) []UpstreamConfig { return cfg.ImagesUpstream },
	},
	ChannelKindVectors: {
		Kind:               ChannelKindVectors,
		DefaultServiceType: "openai",
		ValidateServiceType: func(st string) error {
			if st != "openai" {
				return &ConfigError{
					Message: fmt.Sprintf("Vectors 渠道仅支持 openai serviceType，当前为 %s", st),
					Cause:   ErrUnsupportedServiceType,
				}
			}
			return nil
		},
		AddCapabilityValidation: func(up UpstreamConfig) error {
			return ValidateEmbeddingCapabilities(up.EmbeddingCapabilities)
		},
		UpdateCapabilityValidation: func(up UpstreamUpdate) error {
			return ValidateEmbeddingCapabilities(up.EmbeddingCapabilities)
		},
		SliceRef: func(cfg *Config) *[]UpstreamConfig { return &cfg.VectorsUpstream },
		Slice:    func(cfg Config) []UpstreamConfig { return cfg.VectorsUpstream },
	},
}

// ChannelKindByName 按字符串 kind 返回注册表项，未知 kind 返回错误。
func ChannelKindByName(kind string) (ChannelKindConfig, error) {
	k := ChannelKind(strings.ToLower(strings.TrimSpace(kind)))
	cfg, ok := ChannelKindRegistry[k]
	if !ok {
		return ChannelKindConfig{}, fmt.Errorf("不支持的协议 kind: %s", kind)
	}
	return cfg, nil
}

// ChannelKindLabel 返回 kind 的中文/日志标签。
func (k ChannelKind) Label() string {
	switch k {
	case ChannelKindMessages:
		return "Messages"
	case ChannelKindChat:
		return "Chat"
	case ChannelKindResponses:
		return "Responses"
	case ChannelKindGemini:
		return "Gemini"
	case ChannelKindImages:
		return "Images"
	case ChannelKindVectors:
		return "Vectors"
	default:
		return string(k)
	}
}

// addUpstreamCommonLocked 是六类 Add*Upstream 方法的通用实现。
// 调用方必须已持有 cm.mu 写锁。
func (cm *ConfigManager) addUpstreamCommonLocked(k ChannelKindConfig, upstream UpstreamConfig, placements ...string) error {
	if upstream.Status == "" {
		upstream.Status = "active"
	}

	upstream.ServiceType = normalizeUpstreamServiceType(upstream.ServiceType, k.DefaultServiceType)
	if k.ValidateServiceType != nil {
		if err := k.ValidateServiceType(upstream.ServiceType); err != nil {
			return err
		}
	}

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
	if k.AddCapabilityValidation != nil {
		if err := k.AddCapabilityValidation(upstream); err != nil {
			return err
		}
	}

	upstream.APIKeys = deduplicateStrings(upstream.APIKeys)
	upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)
	upstream.BaseURL = utils.CanonicalBaseURL(upstream.BaseURL, upstream.ServiceType)
	upstream.BaseURLs = deduplicateBaseURLs(upstream.BaseURLs, upstream.ServiceType)
	applyDefaultBaseURL(&upstream)

	upstream.ModelMapping, _ = sanitizeDeprecatedGrokModelMapping(upstream.ModelMapping)
	stripAutoManagedExplicitOverrides(&upstream)
	applyAutoDerivedChannelName(&upstream, "")
	if shouldAutoDeriveChannelName(&upstream) {
		existing := k.Slice(cm.config)
		upstream.Name = uniqueAutoDerivedChannelName(existing, nil, upstream.Name, channelPrimaryBaseURL(&upstream), upstream.ServiceType)
	}

	sliceRef := k.SliceRef(&cm.config)
	assignChannelPriority(*sliceRef, &upstream, resolvePlacement(placements))
	*sliceRef = append([]UpstreamConfig{upstream}, *sliceRef...)

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[Config-Upstream] 已添加 %s 上游: %s", k.Kind.Label(), upstream.Name)
	return nil
}

// addUpstreamCommon 是 Add*Upstream 的公开薄封装，先加锁再调用通用实现。
func (cm *ConfigManager) addUpstreamCommon(k ChannelKindConfig, upstream UpstreamConfig, placements ...string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.addUpstreamCommonLocked(k, upstream, placements...)
}

// updateUpstreamCommonLocked 是六类 Update*Upstream 方法的通用实现。
// 返回值 shouldResetMetrics 与旧方法语义一致。
func (cm *ConfigManager) updateUpstreamCommonLocked(k ChannelKindConfig, index int, updates UpstreamUpdate) (bool, error) {
	slice := k.SliceRef(&cm.config)
	if index < 0 || index >= len(*slice) {
		return false, fmt.Errorf("无效的 %s 上游索引: %d", k.Kind.Label(), index)
	}

	originalConfig := cm.config.deepCopy()
	upstream := &(*slice)[index]

	upstream.ServiceType = normalizeUpstreamServiceType(upstream.ServiceType, k.DefaultServiceType)
	serviceType := upstream.ServiceType
	if updates.ServiceType != nil {
		serviceType = normalizeUpstreamServiceType(*updates.ServiceType, k.DefaultServiceType)
		if k.ValidateServiceType != nil {
			if err := k.ValidateServiceType(serviceType); err != nil {
				return false, err
			}
		}
	}

	if k.UpdateCapabilityValidation != nil {
		if err := k.UpdateCapabilityValidation(updates); err != nil {
			return false, err
		}
	}

	oldFirst := channelPrimaryBaseURL(upstream)

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
	applyDefaultBaseURL(upstream)

	shouldResetMetrics, err := applyUpstreamUpdateFields(upstream, updates)
	if err != nil {
		return false, err
	}

	stripAutoManagedExplicitOverrides(upstream)
	applyAutoDerivedChannelName(upstream, oldFirst)

	if !cm.hasConfigChanged(originalConfig, cm.config) {
		log.Printf("[Config-Upstream] 渠道 [%d] %s 配置未发生实质性变化，跳过保存", index, upstream.Name)
		return shouldResetMetrics, nil
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return false, err
	}

	log.Printf("[Config-Upstream] 已更新 %s 上游: [%d] %s", k.Kind.Label(), index, upstream.Name)
	return shouldResetMetrics, nil
}

// updateUpstreamCommon 是 Update*Upstream 的公开薄封装。
func (cm *ConfigManager) updateUpstreamCommon(k ChannelKindConfig, index int, updates UpstreamUpdate) (bool, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.updateUpstreamCommonLocked(k, index, updates)
}

// applyUpstreamUpdateFields 把 UpstreamUpdate 的各字段应用到 upstream，返回 (shouldResetMetrics, error)。
// 本函数不处理 baseURL/serviceType 相关逻辑，由调用方提前处理。
func applyUpstreamUpdateFields(upstream *UpstreamConfig, updates UpstreamUpdate) (bool, error) {
	var shouldResetMetrics bool

	if updates.AuthHeader != nil {
		authHeader, err := applyAuthHeader(*updates.AuthHeader)
		if err != nil {
			return false, err
		}
		upstream.AuthHeader = authHeader
	}
	if updates.Remark != nil {
		r := strings.TrimSpace(*updates.Remark)
		if remarkRuneCount(r) > remarkMaxRunes {
			return false, fmt.Errorf("remark 不能超过 %d 个字符", remarkMaxRunes)
		}
		upstream.Remark = r
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
					log.Printf("[Config-Upstream] 渠道 %s: Key %s 已移入历史列表", upstream.Name, utils.MaskAPIKey(key))
				}
			}
		}
		var newHistoricalKeys []string
		for _, hk := range upstream.HistoricalAPIKeys {
			if !newKeys[hk] {
				newHistoricalKeys = append(newHistoricalKeys, hk)
			} else {
				log.Printf("[Config-Upstream] 渠道 %s: Key %s 已从历史列表恢复", upstream.Name, utils.MaskAPIKey(hk))
			}
		}
		upstream.HistoricalAPIKeys = newHistoricalKeys

		wasSuspended := upstream.Status == "suspended"
		if applySingleKeyReplacementTransition(upstream, updates.APIKeys) {
			shouldResetMetrics = true
			if wasSuspended {
				log.Printf("[Config-Upstream] 渠道 %s 已从暂停状态自动激活（单 key 更换）", upstream.Name)
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
		applyAdministrativeChannelStatus(upstream, strings.ToLower(*updates.Status))
	}
	if updates.PromotionUntil != nil && upstream.Status != "suspended" {
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
	if updates.NormalizeSystemRoleToTopLevel != nil {
		upstream.NormalizeSystemRoleToTopLevel = *updates.NormalizeSystemRoleToTopLevel
	}
	if updates.CodexToolCompat != nil {
		v := *updates.CodexToolCompat
		upstream.CodexToolCompat = &v
	}
	if updates.StripCodexClientTools != nil {
		upstream.StripCodexClientTools = *updates.StripCodexClientTools
	}
	if updates.ConvertImageURLToB64JSON != nil {
		upstream.ConvertImageURLToB64JSON = *updates.ConvertImageURLToB64JSON
	}
	if updates.InjectDummyThoughtSignature != nil {
		upstream.InjectDummyThoughtSignature = *updates.InjectDummyThoughtSignature
	}
	if updates.StripThoughtSignature != nil {
		upstream.StripThoughtSignature = *updates.StripThoughtSignature
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
	// 渠道级计费覆盖：倍率 <=0 视为清除（置 nil，不参与有效成本核算）
	if updates.CostMultiplier != nil {
		if *updates.CostMultiplier < 0 {
			return false, fmt.Errorf("costMultiplier 不能为负数")
		}
		if *updates.CostMultiplier == 0 {
			upstream.CostMultiplier = nil
		} else {
			v := *updates.CostMultiplier
			upstream.CostMultiplier = &v
		}
	}
	// 充值→渠道到账换算：金额 <=0 置 nil；币种 trim（空串表示清除该侧）
	if updates.ChannelPaymentCurrency != nil {
		upstream.ChannelPaymentCurrency = strings.TrimSpace(*updates.ChannelPaymentCurrency)
	}
	if updates.ChannelPaymentAmount != nil {
		if *updates.ChannelPaymentAmount < 0 {
			return false, fmt.Errorf("channelPaymentAmount 不能为负数")
		}
		if *updates.ChannelPaymentAmount == 0 {
			upstream.ChannelPaymentAmount = nil
		} else {
			v := *updates.ChannelPaymentAmount
			upstream.ChannelPaymentAmount = &v
		}
	}
	if updates.ChannelCreditCurrency != nil {
		upstream.ChannelCreditCurrency = strings.TrimSpace(*updates.ChannelCreditCurrency)
	}
	if updates.ChannelCreditAmount != nil {
		if *updates.ChannelCreditAmount < 0 {
			return false, fmt.Errorf("channelCreditAmount 不能为负数")
		}
		if *updates.ChannelCreditAmount == 0 {
			upstream.ChannelCreditAmount = nil
		} else {
			v := *updates.ChannelCreditAmount
			upstream.ChannelCreditAmount = &v
		}
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
	if updates.AutoManagedKind != nil {
		upstream.AutoManagedKind = *updates.AutoManagedKind
	}
	if updates.Tags != nil {
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

	return shouldResetMetrics, nil
}

// ChannelLocation 标识一个 UpstreamConfig 在六数组中的位置。
type ChannelLocation struct {
	Kind  ChannelKind
	Index int
}

// FindUpstreamByUID 通过 ChannelUID 在所有六数组中查找物理渠道。
// 返回位置、指针和是否找到。调用方必须持有至少读锁。
func (cm *ConfigManager) FindUpstreamByUID(uid string) (ChannelLocation, *UpstreamConfig, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.findUpstreamByUIDLocked(uid)
}

// findUpstreamByUIDLocked 锁内按 uid 查找，不限 kind。
func (cm *ConfigManager) findUpstreamByUIDLocked(uid string) (ChannelLocation, *UpstreamConfig, bool) {
	kinds := []ChannelKind{ChannelKindMessages, ChannelKindChat, ChannelKindResponses, ChannelKindGemini, ChannelKindImages, ChannelKindVectors}
	for _, kind := range kinds {
		k := ChannelKindRegistry[kind]
		slice := k.Slice(cm.config)
		for i := range slice {
			if slice[i].ChannelUID == uid {
				return ChannelLocation{Kind: kind, Index: i}, &slice[i], true
			}
		}
	}
	return ChannelLocation{}, nil, false
}

// removeUpstreamCommonLocked 是六类 Remove*Upstream 方法的通用实现。
// clearFailedKeysLabel 用于 clearFailedKeysForUpstream 的标签。
func (cm *ConfigManager) removeUpstreamCommonLocked(k ChannelKindConfig, index int, clearFailedKeysLabel string) (*UpstreamConfig, error) {
	slice := k.SliceRef(&cm.config)
	if index < 0 || index >= len(*slice) {
		return nil, fmt.Errorf("无效的 %s 上游索引: %d", k.Kind.Label(), index)
	}

	removed := (*slice)[index]
	*slice = append((*slice)[:index], (*slice)[index+1:]...)

	cm.clearFailedKeysForUpstream(&removed, clearFailedKeysLabel)

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}

	log.Printf("[Config-Upstream] 已删除 %s 上游: %s", k.Kind.Label(), removed.Name)
	return &removed, nil
}

// addChannelKeyCommonLocked 是六类 Add*APIKey 方法的通用实现。
func (cm *ConfigManager) addChannelKeyCommonLocked(k ChannelKindConfig, index int, apiKey string) error {
	slice := k.SliceRef(&cm.config)
	if index < 0 || index >= len(*slice) {
		return fmt.Errorf("无效的 %s 上游索引: %d", k.Kind.Label(), index)
	}

	upstream := &(*slice)[index]
	for _, key := range upstream.APIKeys {
		if key == apiKey {
			return fmt.Errorf("API密钥已存在")
		}
	}

	upstream.APIKeys = append(upstream.APIKeys, apiKey)

	var newDisabledKeys []DisabledKeyInfo
	for _, dk := range upstream.DisabledAPIKeys {
		if dk.Key != apiKey {
			newDisabledKeys = append(newDisabledKeys, dk)
		}
	}
	upstream.DisabledAPIKeys = newDisabledKeys
	removeDisabledKeyModelsForKey(upstream, apiKey)

	var newHistoricalKeys []string
	for _, hk := range upstream.HistoricalAPIKeys {
		if hk != apiKey {
			newHistoricalKeys = append(newHistoricalKeys, hk)
		} else {
			log.Printf("[%s-Key] 上游 [%d] %s: Key %s 已从历史列表恢复", k.Kind.Label(), index, upstream.Name, utils.MaskAPIKey(hk))
		}
	}
	upstream.HistoricalAPIKeys = newHistoricalKeys

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[%s-Key] 已添加API密钥到上游 [%d] %s", k.Kind.Label(), index, upstream.Name)
	return nil
}

// removeChannelKeyCommonLocked 是六类 Remove*APIKey 方法的通用实现。
func (cm *ConfigManager) removeChannelKeyCommonLocked(k ChannelKindConfig, index int, apiKey string) error {
	slice := k.SliceRef(&cm.config)
	if index < 0 || index >= len(*slice) {
		return fmt.Errorf("无效的 %s 上游索引: %d", k.Kind.Label(), index)
	}

	upstream := &(*slice)[index]
	keys := upstream.APIKeys
	found := false
	for i, key := range keys {
		if key == apiKey {
			upstream.APIKeys = append(keys[:i], keys[i+1:]...)
			found = true
			break
		}
	}

	if cm.removeDisabledKeyEntryLocked(upstream, string(k.Kind), apiKey) {
		found = true
	}

	if !found {
		return fmt.Errorf("API密钥不存在")
	}

	alreadyInHistory := false
	for _, hk := range upstream.HistoricalAPIKeys {
		if hk == apiKey {
			alreadyInHistory = true
			break
		}
	}
	if !alreadyInHistory {
		upstream.HistoricalAPIKeys = append(upstream.HistoricalAPIKeys, apiKey)
		log.Printf("[%s-Key] 上游 [%d] %s: Key %s 已移入历史列表", k.Kind.Label(), index, upstream.Name, utils.MaskAPIKey(apiKey))
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	log.Printf("[%s-Key] 已从 %s 上游 [%d] %s 删除API密钥", k.Kind.Label(), k.Kind.Label(), index, upstream.Name)
	return nil
}

// ============== 公共包装函数（供外部 handler 使用）==============

// AddUpstreamByKind 按 kind 字符串添加渠道，供统一 API 使用。
func AddUpstreamByKind(cm *ConfigManager, k ChannelKindConfig, up UpstreamConfig, placement string) error {
	return cm.addUpstreamCommon(k, up, placement)
}

// UpdateUpstreamByKind 按 ChannelLocation 更新渠道，供统一 API 使用。
func UpdateUpstreamByKind(cm *ConfigManager, loc ChannelLocation, updates UpstreamUpdate) (bool, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	k := ChannelKindRegistry[loc.Kind]
	return cm.updateUpstreamCommonLocked(k, loc.Index, updates)
}

// RemoveUpstreamByKind 按 ChannelLocation 删除渠道，供统一 API 使用。
func RemoveUpstreamByKind(cm *ConfigManager, loc ChannelLocation) (*UpstreamConfig, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	k := ChannelKindRegistry[loc.Kind]
	return cm.removeUpstreamCommonLocked(k, loc.Index, string(k.Kind))
}

// AddChannelKeyByKind 按 ChannelLocation 添加 key，供统一 API 使用。
func AddChannelKeyByKind(cm *ConfigManager, loc ChannelLocation, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	k := ChannelKindRegistry[loc.Kind]
	return cm.addChannelKeyCommonLocked(k, loc.Index, apiKey)
}

// RemoveChannelKeyByKind 按 ChannelLocation 删除 key，供统一 API 使用。
func RemoveChannelKeyByKind(cm *ConfigManager, loc ChannelLocation, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	k := ChannelKindRegistry[loc.Kind]
	return cm.removeChannelKeyCommonLocked(k, loc.Index, apiKey)
}
