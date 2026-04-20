package config

import (
	"sort"
	"strings"
	"time"
)

// ============== 工具函数 ==============

// deduplicateStrings 去重字符串切片，保持原始顺序
func deduplicateStrings(items []string) []string {
	if len(items) <= 1 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if _, exists := seen[item]; !exists {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}

// deduplicateBaseURLs 去重 BaseURLs，忽略末尾 / 和 # 差异
func deduplicateBaseURLs(urls []string) []string {
	if len(urls) <= 1 {
		return urls
	}
	seen := make(map[string]struct{}, len(urls))
	result := make([]string, 0, len(urls))
	for _, url := range urls {
		normalized := strings.TrimRight(url, "/#")
		if _, exists := seen[normalized]; !exists {
			seen[normalized] = struct{}{}
			result = append(result, url)
		}
	}
	return result
}

// ConfigError 配置错误
type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	return e.Message
}

// ============== 模型重定向 ==============

// RedirectModel 模型重定向
func RedirectModel(model string, upstream *UpstreamConfig) string {
	if upstream.ModelMapping == nil || len(upstream.ModelMapping) == 0 {
		return model
	}

	// 直接匹配（精确匹配优先）
	if mapped, ok := upstream.ModelMapping[model]; ok {
		return mapped
	}

	// 模糊匹配：按源模型长度从长到短排序，确保最长匹配优先
	type mapping struct {
		source string
		target string
	}
	mappings := make([]mapping, 0, len(upstream.ModelMapping))
	for source, target := range upstream.ModelMapping {
		mappings = append(mappings, mapping{source, target})
	}
	sort.Slice(mappings, func(i, j int) bool {
		return len(mappings[i].source) > len(mappings[j].source)
	})

	for _, m := range mappings {
		if strings.Contains(model, m.source) || strings.Contains(m.source, model) {
			return m.target
		}
	}

	return model
}

// ResolveReasoningEffort 根据原始模型名解析 reasoning effort
func ResolveReasoningEffort(model string, upstream *UpstreamConfig) string {
	if upstream == nil || upstream.ReasoningMapping == nil || len(upstream.ReasoningMapping) == 0 {
		return ""
	}
	if effort, ok := upstream.ReasoningMapping[model]; ok {
		return effort
	}
	type mapping struct {
		source string
		effort string
	}
	mappings := make([]mapping, 0, len(upstream.ReasoningMapping))
	for source, effort := range upstream.ReasoningMapping {
		mappings = append(mappings, mapping{source, effort})
	}
	sort.Slice(mappings, func(i, j int) bool {
		return len(mappings[i].source) > len(mappings[j].source)
	})
	for _, m := range mappings {
		if strings.Contains(model, m.source) || strings.Contains(m.source, model) {
			return m.effort
		}
	}
	return ""
}

// ============== 渠道状态与优先级辅助函数 ==============

// GetChannelStatus 获取渠道状态（带默认值处理）
func GetChannelStatus(upstream *UpstreamConfig) string {
	if upstream.Status == "" {
		return "active"
	}
	return upstream.Status
}

// GetChannelPriority 获取渠道优先级（带默认值处理）
func GetChannelPriority(upstream *UpstreamConfig, index int) int {
	if upstream.Priority == 0 {
		return index
	}
	return upstream.Priority
}

// IsChannelInPromotion 检查渠道是否处于促销期
func IsChannelInPromotion(upstream *UpstreamConfig) bool {
	if upstream.PromotionUntil == nil {
		return false
	}
	return time.Now().Before(*upstream.PromotionUntil)
}

// ============== UpstreamConfig 方法 ==============

// Clone 深拷贝 UpstreamConfig（用于避免并发修改问题）
// 在多 BaseURL failover 场景下，需要临时修改 BaseURL 字段，
// 使用深拷贝可避免并发请求之间的竞态条件
func (u *UpstreamConfig) Clone() *UpstreamConfig {
	cloned := *u // 浅拷贝

	// 深拷贝切片字段
	if u.BaseURLs != nil {
		cloned.BaseURLs = make([]string, len(u.BaseURLs))
		copy(cloned.BaseURLs, u.BaseURLs)
	}
	if u.APIKeys != nil {
		cloned.APIKeys = make([]string, len(u.APIKeys))
		copy(cloned.APIKeys, u.APIKeys)
	}
	if u.HistoricalAPIKeys != nil {
		cloned.HistoricalAPIKeys = make([]string, len(u.HistoricalAPIKeys))
		copy(cloned.HistoricalAPIKeys, u.HistoricalAPIKeys)
	}
	if u.ModelMapping != nil {
		cloned.ModelMapping = make(map[string]string, len(u.ModelMapping))
		for k, v := range u.ModelMapping {
			cloned.ModelMapping[k] = v
		}
	}
	if u.CustomHeaders != nil {
		cloned.CustomHeaders = make(map[string]string, len(u.CustomHeaders))
		for k, v := range u.CustomHeaders {
			cloned.CustomHeaders[k] = v
		}
	}
	if u.PromotionUntil != nil {
		t := *u.PromotionUntil
		cloned.PromotionUntil = &t
	}
	if u.SupportedModels != nil {
		cloned.SupportedModels = make([]string, len(u.SupportedModels))
		copy(cloned.SupportedModels, u.SupportedModels)
	}
	if u.DisabledAPIKeys != nil {
		cloned.DisabledAPIKeys = make([]DisabledKeyInfo, len(u.DisabledAPIKeys))
		copy(cloned.DisabledAPIKeys, u.DisabledAPIKeys)
	}
	if u.AutoBlacklistBalance != nil {
		v := *u.AutoBlacklistBalance
		cloned.AutoBlacklistBalance = &v
	}
	if u.NormalizeMetadataUserID != nil {
		v := *u.NormalizeMetadataUserID
		cloned.NormalizeMetadataUserID = &v
	}
	if u.StreamPassthroughEnabled != nil {
		v := *u.StreamPassthroughEnabled
		cloned.StreamPassthroughEnabled = &v
	}
	if u.Sub2APIPassthroughEnabled != nil {
		v := *u.Sub2APIPassthroughEnabled
		cloned.Sub2APIPassthroughEnabled = &v
	}
	if u.KeyAffinityEnabled != nil {
		v := *u.KeyAffinityEnabled
		cloned.KeyAffinityEnabled = &v
	}
	if u.StrictRequestPassthroughEnabled != nil {
		v := *u.StrictRequestPassthroughEnabled
		cloned.StrictRequestPassthroughEnabled = &v
	}
	if u.ModelsHealthCheckEnabled != nil {
		v := *u.ModelsHealthCheckEnabled
		cloned.ModelsHealthCheckEnabled = &v
	}
	if u.ModelsHealthCheckIntervalMinutes != nil {
		v := *u.ModelsHealthCheckIntervalMinutes
		cloned.ModelsHealthCheckIntervalMinutes = &v
	}
	if len(u.FailoverRules) > 0 {
		cloned.FailoverRules = CloneFailoverRules(u.FailoverRules)
	}

	return &cloned
}

// SupportsModel 检查渠道是否支持指定模型
// 空列表表示支持所有模型，支持通配符前缀匹配（如 gpt-4* 匹配 gpt-4o）
func (u *UpstreamConfig) SupportsModel(model string) bool {
	if len(u.SupportedModels) == 0 {
		return true
	}
	for _, pattern := range u.SupportedModels {
		if strings.HasSuffix(pattern, "*") {
			if strings.HasPrefix(model, strings.TrimSuffix(pattern, "*")) {
				return true
			}
		} else if pattern == model {
			return true
		}
	}
	return false
}

// GetEffectiveBaseURL 获取当前应使用的 BaseURL（纯 failover 模式）
// 优先使用 BaseURL 字段（支持调用方临时覆盖），否则从 BaseURLs 数组获取
func (u *UpstreamConfig) GetEffectiveBaseURL() string {
	// 优先使用 BaseURL（可能被调用方临时设置用于指定本次请求的 URL）
	if u.BaseURL != "" {
		return u.BaseURL
	}

	// 回退到 BaseURLs 数组
	if len(u.BaseURLs) > 0 {
		return u.BaseURLs[0]
	}

	return ""
}

// GetAllBaseURLs 获取所有 BaseURL（用于延迟测试）
func (u *UpstreamConfig) GetAllBaseURLs() []string {
	if len(u.BaseURLs) > 0 {
		return u.BaseURLs
	}
	if u.BaseURL != "" {
		return []string{u.BaseURL}
	}
	return nil
}
