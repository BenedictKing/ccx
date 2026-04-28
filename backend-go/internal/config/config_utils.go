package config

import (
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

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

func normalizeUpstreamServiceType(serviceType, fallback string) string {
	trimmed := strings.TrimSpace(serviceType)
	if trimmed != "" {
		return trimmed
	}
	return fallback
}

func deduplicateBaseURLs(urls []string, serviceType string) []string {
	if len(urls) == 0 {
		return urls
	}
	seen := make(map[string]struct{}, len(urls))
	result := make([]string, 0, len(urls))
	for _, rawURL := range urls {
		canonical := utils.CanonicalBaseURL(rawURL, serviceType)
		if canonical == "" {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result
}

type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	return e.Message
}

func RedirectModel(model string, upstream *UpstreamConfig) string {
	if upstream.ModelMapping == nil || len(upstream.ModelMapping) == 0 {
		return model
	}
	if mapped, ok := upstream.ModelMapping[model]; ok {
		return mapped
	}

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

func ResolveReasoningEffort(model string, upstream *UpstreamConfig) string {
	if upstream == nil || len(upstream.ReasoningMapping) == 0 {
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

func GetChannelStatus(upstream *UpstreamConfig) string {
	if upstream.Status == "" {
		return "active"
	}
	return upstream.Status
}

func GetChannelAdminState(upstream *UpstreamConfig) string {
	return GetChannelStatus(upstream)
}

func GetChannelRuntimeState(upstream *UpstreamConfig) string {
	if upstream == nil {
		return "unknown"
	}
	if len(upstream.DisabledAPIKeys) > 0 {
		return "disabled_keys_present"
	}
	if len(upstream.APIKeys) == 0 {
		return "no_active_keys"
	}
	return "ready"
}

func GetChannelEffectiveState(upstream *UpstreamConfig) string {
	if upstream == nil {
		return "unknown"
	}
	adminState := GetChannelAdminState(upstream)
	if adminState != "active" {
		return adminState
	}
	if len(upstream.APIKeys) == 0 {
		return "degraded"
	}
	return "active"
}

func applySingleKeyReplacementTransition(upstream *UpstreamConfig, newKeys []string) bool {
	if upstream == nil {
		return false
	}
	if len(upstream.APIKeys) == 1 && len(newKeys) == 1 && upstream.APIKeys[0] != newKeys[0] {
		if upstream.Status == "suspended" {
			upstream.Status = "active"
		}
		return true
	}
	return false
}

func GetChannelPriority(upstream *UpstreamConfig, index int) int {
	if upstream.Priority == 0 {
		return index
	}
	return upstream.Priority
}

func IsChannelInPromotion(upstream *UpstreamConfig) bool {
	return upstream.PromotionUntil != nil && time.Now().Before(*upstream.PromotionUntil)
}

func (u *UpstreamConfig) Clone() *UpstreamConfig {
	cloned := *u
	if u.BaseURLs != nil {
		cloned.BaseURLs = append([]string(nil), u.BaseURLs...)
	}
	if u.APIKeys != nil {
		cloned.APIKeys = append([]string(nil), u.APIKeys...)
	}
	if u.HistoricalAPIKeys != nil {
		cloned.HistoricalAPIKeys = append([]string(nil), u.HistoricalAPIKeys...)
	}
	if u.ModelMapping != nil {
		cloned.ModelMapping = make(map[string]string, len(u.ModelMapping))
		for k, v := range u.ModelMapping {
			cloned.ModelMapping[k] = v
		}
	}
	if u.ReasoningMapping != nil {
		cloned.ReasoningMapping = make(map[string]string, len(u.ReasoningMapping))
		for k, v := range u.ReasoningMapping {
			cloned.ReasoningMapping[k] = v
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
		cloned.SupportedModels = append([]string(nil), u.SupportedModels...)
	}
	if u.ManualModels != nil {
		cloned.ManualModels = append([]string(nil), u.ManualModels...)
	}
	if u.DisabledAPIKeys != nil {
		cloned.DisabledAPIKeys = append([]DisabledKeyInfo(nil), u.DisabledAPIKeys...)
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

func (u *UpstreamConfig) SupportsModel(model string) bool {
	supported, _ := u.ExplainModelSupport(model)
	return supported
}

func (u *UpstreamConfig) ExplainModelSupport(model string) (bool, string) {
	if len(u.SupportedModels) == 0 {
		return true, ""
	}
	includes, excludes := splitSupportedModelRules(u.SupportedModels)
	for _, pattern := range excludes {
		if matchSupportedModelPattern(pattern, model) {
			return false, "matched exclude rule !" + pattern
		}
	}
	if len(includes) == 0 {
		return true, ""
	}
	for _, pattern := range includes {
		if matchSupportedModelPattern(pattern, model) {
			return true, ""
		}
	}
	return false, "no include rule matched"
}

func splitSupportedModelRules(rules []string) (includes []string, excludes []string) {
	includes = make([]string, 0, len(rules))
	excludes = make([]string, 0, len(rules))
	for _, rawRule := range rules {
		rule := strings.TrimSpace(rawRule)
		if rule == "" {
			continue
		}
		if strings.HasPrefix(rule, "!") {
			pattern := strings.TrimSpace(strings.TrimPrefix(rule, "!"))
			if strings.HasPrefix(pattern, "!") {
				continue
			}
			if isValidSupportedModelPattern(pattern) {
				excludes = append(excludes, pattern)
			}
			continue
		}
		if isValidSupportedModelPattern(rule) {
			includes = append(includes, rule)
		}
	}
	return includes, excludes
}

func isValidSupportedModelPattern(pattern string) bool {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return false
	}
	if strings.Count(trimmed, "!") > 1 {
		return false
	}
	normalized := trimmed
	if strings.HasPrefix(normalized, "!") {
		normalized = strings.TrimSpace(strings.TrimPrefix(normalized, "!"))
	}
	if normalized == "" || strings.HasPrefix(normalized, "!") {
		return false
	}
	starCount := strings.Count(normalized, "*")
	if starCount == 0 {
		return true
	}
	if normalized == "*" {
		return true
	}
	if starCount == 1 {
		return strings.HasPrefix(normalized, "*") || strings.HasSuffix(normalized, "*")
	}
	if starCount == 2 {
		return strings.HasPrefix(normalized, "*") && strings.HasSuffix(normalized, "*") && strings.Trim(normalized, "*") != ""
	}
	return false
}

func matchSupportedModelPattern(pattern, model string) bool {
	if !isValidSupportedModelPattern(pattern) {
		return false
	}
	if strings.HasPrefix(pattern, "!") {
		pattern = strings.TrimSpace(strings.TrimPrefix(pattern, "!"))
	}
	if pattern == "*" {
		return true
	}
	starCount := strings.Count(pattern, "*")
	if starCount == 0 {
		return pattern == model
	}
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		return strings.Contains(model, strings.Trim(pattern, "*"))
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(model, strings.TrimPrefix(pattern, "*"))
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(model, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

func (u *UpstreamConfig) GetEffectiveBaseURL() string {
	if u.BaseURL != "" {
		return utils.CanonicalBaseURL(u.BaseURL, u.ServiceType)
	}
	if len(u.BaseURLs) > 0 {
		return utils.CanonicalBaseURL(u.BaseURLs[0], u.ServiceType)
	}
	return ""
}

func (u *UpstreamConfig) GetAllBaseURLs() []string {
	if len(u.BaseURLs) > 0 {
		return deduplicateBaseURLs(u.BaseURLs, u.ServiceType)
	}
	if u.BaseURL != "" {
		canonical := utils.CanonicalBaseURL(u.BaseURL, u.ServiceType)
		if canonical == "" {
			return nil
		}
		return []string{canonical}
	}
	return nil
}
