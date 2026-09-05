package presetstore

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// knownTiers 是允许的信任等级白名单。
// 远程数据引入未知 tier 时该来源类型条目视为非法，整份数据弃用。
var knownTiers = map[string]bool{
	"first":   true,
	"second":  true,
	"third":   true,
	"local":   true,
	"unknown": true,
}

// Validate 校验一份候选 bundle 是否可安全采用。
//
// 预置数据会影响路由信任等级（tier），故按不可信输入严格校验：
// schema 兼容 + 枚举非空 + tier 白名单 + new-api 默认值自洽。
// 任一不满足返回 error，调用方应弃用该候选、保留当前生效数据。
func Validate(b *PresetBundle) error {
	if b == nil {
		return fmt.Errorf("[presetstore] bundle 为 nil")
	}
	if b.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("[presetstore] schemaVersion %d 高于本二进制支持的 %d，需升级版本",
			b.SchemaVersion, CurrentSchemaVersion)
	}

	sub := b.Subscription
	if len(sub.OriginTypes) == 0 {
		return fmt.Errorf("[presetstore] originTypes 不能为空")
	}

	seen := make(map[string]bool, len(sub.OriginTypes))
	for _, e := range sub.OriginTypes {
		if e.Value == "" {
			return fmt.Errorf("[presetstore] originType.value 不能为空")
		}
		if seen[e.Value] {
			return fmt.Errorf("[presetstore] originType %q 重复", e.Value)
		}
		seen[e.Value] = true
		if !knownTiers[e.Tier] {
			return fmt.Errorf("[presetstore] originType %q 的 tier %q 不在白名单内", e.Value, e.Tier)
		}
	}

	if len(sub.BillingModes) == 0 {
		return fmt.Errorf("[presetstore] billingModes 不能为空")
	}
	if len(sub.Sources) == 0 {
		return fmt.Errorf("[presetstore] sources 不能为空")
	}

	if d := sub.NewApiDefaults; d.OriginType != "" {
		if !seen[sub.Canonicalize(d.OriginType)] {
			return fmt.Errorf("[presetstore] newApiDefaults.originType %q 不是已知来源类型", d.OriginType)
		}
	}

	for alias, canonical := range sub.OriginTypeAliases {
		if !seen[canonical] {
			return fmt.Errorf("[presetstore] originTypeAlias %q -> %q 的目标不是已知来源类型", alias, canonical)
		}
	}

	if b.ModelRegistry != nil {
		if err := validateModelRegistryPreset(b.ModelRegistry); err != nil {
			return err
		}
	}
	if b.ChannelPresets != nil {
		if err := validateChannelPresets(b.ChannelPresets); err != nil {
			return err
		}
	}
	if b.BuiltinModelsManifests != nil {
		if err := validateBuiltinModelsManifestPreset(b.BuiltinModelsManifests); err != nil {
			return err
		}
	}

	return nil
}

func validateModelRegistryPreset(preset *ModelRegistryPreset) error {
	for idx, capability := range preset.UpstreamCapabilities {
		if len(capability.Patterns) == 0 {
			return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].patterns 不能为空", idx)
		}
		for patternIdx, pattern := range capability.Patterns {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].patterns[%d] 不能为空", idx, patternIdx)
			}
			if err := validateModelPattern(pattern); err != nil {
				return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].patterns[%d] 非法: %w", idx, patternIdx, err)
			}
		}
		if err := validateNonNegativeInt("contextWindowTokens", capability.ContextWindowTokens); err != nil {
			return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].%w", idx, err)
		}
		for tierIdx, tier := range capability.ContextWindowTiers {
			if tier < 0 {
				return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].contextWindowTiers[%d] 不能为负数", idx, tierIdx)
			}
		}
		if err := validateNonNegativeInt("maxOutputTokens", capability.MaxOutputTokens); err != nil {
			return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].%w", idx, err)
		}
		if err := validateNonNegativeInt("defaultOutputTokens", capability.DefaultOutputTokens); err != nil {
			return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].%w", idx, err)
		}
		if err := validateNonNegativeInt("recommendedOutputTokens", capability.RecommendedOutputTokens); err != nil {
			return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].%w", idx, err)
		}
		if capability.Pricing != nil {
			if err := validatePricePointer("inputCacheHitPrice", capability.Pricing.InputCacheHitPrice); err != nil {
				return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].pricing.%w", idx, err)
			}
			if err := validatePricePointer("inputCacheMissPrice", capability.Pricing.InputCacheMissPrice); err != nil {
				return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].pricing.%w", idx, err)
			}
			if err := validatePricePointer("outputPrice", capability.Pricing.OutputPrice); err != nil {
				return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].pricing.%w", idx, err)
			}
			for tierIdx, tier := range capability.Pricing.Tiers {
				if err := validateNonNegativeInt("inputTokensAbove", tier.InputTokensAbove); err != nil {
					return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].pricing.tiers[%d].%w", idx, tierIdx, err)
				}
				if err := validateNonNegativeInt("inputTokensUpTo", tier.InputTokensUpTo); err != nil {
					return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].pricing.tiers[%d].%w", idx, tierIdx, err)
				}
				if err := validatePricePointer("inputCacheHitPrice", tier.InputCacheHitPrice); err != nil {
					return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].pricing.tiers[%d].%w", idx, tierIdx, err)
				}
				if err := validatePricePointer("inputCacheMissPrice", tier.InputCacheMissPrice); err != nil {
					return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].pricing.tiers[%d].%w", idx, tierIdx, err)
				}
				if err := validatePricePointer("outputPrice", tier.OutputPrice); err != nil {
					return fmt.Errorf("[presetstore] modelRegistry.upstreamCapabilities[%d].pricing.tiers[%d].%w", idx, tierIdx, err)
				}
			}
		}
	}
	for idx, benchmark := range preset.BenchmarkProfiles {
		prefix := fmt.Sprintf("[presetstore] modelRegistry.benchmarkProfiles[%d]", idx)
		if len(benchmark.Patterns) == 0 {
			return fmt.Errorf("%s.patterns 不能为空", prefix)
		}
		for patternIdx, pattern := range benchmark.Patterns {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("%s.patterns[%d] 不能为空", prefix, patternIdx)
			}
			if err := validateModelPattern(pattern); err != nil {
				return fmt.Errorf("%s.patterns[%d] 非法: %w", prefix, patternIdx, err)
			}
		}
		if strings.TrimSpace(benchmark.CanonicalModel) == "" {
			return fmt.Errorf("%s.canonicalModel 不能为空", prefix)
		}
		if err := validateBenchmarkScore("overallScore", benchmark.OverallScore); err != nil {
			return fmt.Errorf("%s.%w", prefix, err)
		}
		for category, score := range benchmark.CategoryScores {
			if strings.TrimSpace(category) == "" {
				return fmt.Errorf("%s.categoryScores 包含空类别", prefix)
			}
			if err := validateBenchmarkScore("categoryScores."+category, score); err != nil {
				return fmt.Errorf("%s.%w", prefix, err)
			}
		}
		if len(benchmark.CategoryScores) == 0 && len(benchmark.BenchmarkEvidence) == 0 {
			return fmt.Errorf("%s 至少需要 categoryScores 或 benchmarkEvidence", prefix)
		}
		for evidenceIdx, evidence := range benchmark.BenchmarkEvidence {
			if err := validateModelBenchmarkEvidence(evidence); err != nil {
				return fmt.Errorf("%s.benchmarkEvidence[%d].%w", prefix, evidenceIdx, err)
			}
		}
		if len(benchmark.Sources) == 0 {
			return fmt.Errorf("%s.sources 不能为空", prefix)
		}
		if _, err := time.Parse("2006-01-02", benchmark.VerifiedAt); err != nil {
			return fmt.Errorf("%s.verifiedAt 必须是 YYYY-MM-DD: %w", prefix, err)
		}
		if benchmark.Lane != "provisional" && benchmark.Lane != "verified" {
			return fmt.Errorf("%s.lane=%q，仅支持 provisional 或 verified", prefix, benchmark.Lane)
		}
		if benchmark.SharedResults <= 0 || benchmark.ComparableCategories <= 0 || benchmark.TotalCategories <= 0 {
			return fmt.Errorf("%s 的证据计数字段必须为正数", prefix)
		}
		if benchmark.TotalCategories > 0 && benchmark.ComparableCategories > benchmark.TotalCategories {
			return fmt.Errorf("%s.comparableCategories 不能大于 totalCategories", prefix)
		}
	}
	return nil
}

func validateModelPattern(pattern string) error {
	rePattern := "(?i)" + pattern
	if idx := strings.LastIndex(rePattern, "(?="); idx >= 0 && strings.HasSuffix(rePattern[idx:], ")") {
		rePattern = rePattern[:idx]
	}
	_, err := regexp.Compile(rePattern)
	return err
}

func validateBenchmarkScore(field string, value float64) error {
	if value < 0 || value > 100 {
		return fmt.Errorf("%s 必须在 0-100 之间", field)
	}
	return nil
}

func validateModelBenchmarkEvidence(evidence ModelBenchmarkEvidencePreset) error {
	for field, value := range map[string]string{
		"benchmark":        evidence.Benchmark,
		"benchmarkVersion": evidence.BenchmarkVersion,
		"sourceModel":      evidence.SourceModel,
		"domain":           evidence.Domain,
		"metric":           evidence.Metric,
		"effort":           evidence.Effort,
		"selectionBasis":   evidence.SelectionBasis,
		"sourceUrl":        evidence.SourceURL,
		"capturedAt":       evidence.CapturedAt,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s 不能为空", field)
		}
	}
	if err := validateBenchmarkRawValue(evidence.RawValue); err != nil {
		return err
	}
	if err := validateBenchmarkFraction("uncertainty", evidence.Uncertainty); err != nil {
		return err
	}
	if err := validateBenchmarkFraction("cohortPercentile", evidence.CohortPercentile); err != nil {
		return err
	}
	if evidence.TaskCount <= 0 || evidence.CohortSize <= 0 {
		return fmt.Errorf("taskCount 和 cohortSize 必须为正数")
	}
	parsedURL, err := url.ParseRequestURI(evidence.SourceURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return fmt.Errorf("sourceUrl 必须是 HTTPS URL")
	}
	if _, err := time.Parse("2006-01-02", evidence.CapturedAt); err != nil {
		return fmt.Errorf("capturedAt 必须是 YYYY-MM-DD: %w", err)
	}
	if evidence.CostUSD != nil && math.IsNaN(*evidence.CostUSD) {
		return fmt.Errorf("costUsd 不能为 NaN")
	}
	return nil
}

// rawValue 保留来源基准的原始量纲，不能假设都是 0-1 比例。
func validateBenchmarkRawValue(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return fmt.Errorf("rawValue 必须是有限非负数")
	}
	return nil
}

func validateBenchmarkFraction(field string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return fmt.Errorf("%s 必须在 0-1 之间", field)
	}
	return nil
}

func validateChannelPresets(preset *ChannelPresetsPreset) error {
	if preset.SchemaVersion != 1 {
		return fmt.Errorf("[presetstore] channelPresets.schemaVersion=%d，当前仅支持 1", preset.SchemaVersion)
	}
	required := []string{"claudeMessages", "openAIChat", "codexResponses", "openAIMessages"}
	for _, key := range required {
		raw, ok := preset.Collections[key]
		if !ok || len(raw) == 0 {
			return fmt.Errorf("[presetstore] channelPresets.%s 缺失", key)
		}
		var collection struct {
			SchemaVersion int `json:"schemaVersion"`
		}
		if err := json.Unmarshal(raw, &collection); err != nil {
			return fmt.Errorf("[presetstore] channelPresets.%s 解析失败: %w", key, err)
		}
		if collection.SchemaVersion != 1 {
			return fmt.Errorf("[presetstore] channelPresets.%s.schemaVersion=%d，当前仅支持 1", key, collection.SchemaVersion)
		}
	}
	return nil
}

func validateBuiltinModelsManifestPreset(preset *BuiltinModelsManifestPreset) error {
	seenServiceTypes := map[string]bool{
		"messages":  true,
		"openai":    true,
		"responses": true,
		"chat":      true,
		"gemini":    true,
		"images":    true,
		"vectors":   true,
	}
	for idx, manifest := range preset.Manifests {
		if strings.TrimSpace(manifest.BaseURLPattern) == "" {
			return fmt.Errorf("[presetstore] builtinModelsManifests.manifests[%d].baseUrlPattern 不能为空", idx)
		}
		if strings.Contains(manifest.BaseURLPattern, "://") {
			return fmt.Errorf("[presetstore] builtinModelsManifests.manifests[%d].baseUrlPattern 不能包含 scheme", idx)
		}
		if _, err := url.Parse("https://" + manifest.BaseURLPattern); err != nil {
			return fmt.Errorf("[presetstore] builtinModelsManifests.manifests[%d].baseUrlPattern 非法: %w", idx, err)
		}
		if !seenServiceTypes[manifest.ServiceType] {
			return fmt.Errorf("[presetstore] builtinModelsManifests.manifests[%d].serviceType %q 不在白名单内", idx, manifest.ServiceType)
		}
		if len(manifest.ModelIDs) == 0 {
			return fmt.Errorf("[presetstore] builtinModelsManifests.manifests[%d].modelIds 不能为空", idx)
		}
		seenModels := make(map[string]bool, len(manifest.ModelIDs))
		for modelIdx, modelID := range manifest.ModelIDs {
			if strings.TrimSpace(modelID) == "" {
				return fmt.Errorf("[presetstore] builtinModelsManifests.manifests[%d].modelIds[%d] 不能为空", idx, modelIdx)
			}
			if seenModels[modelID] {
				return fmt.Errorf("[presetstore] builtinModelsManifests.manifests[%d].modelIds[%d] 重复", idx, modelIdx)
			}
			seenModels[modelID] = true
		}
		for patternIdx, pattern := range manifest.ExcludeModelPatterns {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("[presetstore] builtinModelsManifests.manifests[%d].excludeModelPatterns[%d] 不能为空", idx, patternIdx)
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("[presetstore] builtinModelsManifests.manifests[%d].excludeModelPatterns[%d] 非法: %w", idx, patternIdx, err)
			}
		}
	}
	return nil
}

func validateNonNegativeInt(field string, value int) error {
	if value < 0 {
		return fmt.Errorf("%s 不能为负数", field)
	}
	return nil
}

func validatePricePointer(field string, value *float64) error {
	if value != nil && *value < 0 {
		return fmt.Errorf("%s 不能为负数", field)
	}
	return nil
}
