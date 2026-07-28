// Package presetstore 提供可远程增量更新的"预置数据"运行时存储。
//
// 预置数据（订阅来源分类、模型注册表、渠道预置、内置模型清单）原先散落在
// 编译期常量里，每次微调都要发版。presetstore 把它们抽成统一的 PresetBundle，
// 支持按数据版本选择：编译期内置（embedded）、磁盘缓存（cache）和远程覆盖（remote）
// 中仅允许较新版本替换当前快照，避免旧缓存使新二进制能力回退。
//
// 本文件只定义数据契约与订阅来源预置；远程拉取/校验/缓存在 Phase 2 补充。
package presetstore

// CurrentSchemaVersion 是本二进制支持的 subscription-preset schema 主版本。
// 远程 schemaVersion 高于此值时整体弃用远程（老客户端不误解新结构）。
import "encoding/json"

const CurrentSchemaVersion = 1

// PresetBundle 是所有预置数据类的聚合，运行时由 PresetStore 原子持有。
type PresetBundle struct {
	// SchemaVersion 结构版本；仅在字段不兼容变更时递增。
	SchemaVersion int `json:"schemaVersion"`
	// DataVersion 数据版本，按数字段进行单调递增比较（如 "v2.9.37+20260721"）。
	DataVersion string `json:"dataVersion,omitempty"`

	// Subscription 订阅来源预置。
	Subscription SubscriptionPreset `json:"subscription"`
	// ModelRegistry 上游模型能力注册表预置。
	ModelRegistry *ModelRegistryPreset `json:"modelRegistry,omitempty"`
	// ChannelPresets 各协议渠道预置原始 JSON，保持结构保真。
	ChannelPresets *ChannelPresetsPreset `json:"channelPresets,omitempty"`
	// BuiltinModelsManifests 内置模型清单预置。
	BuiltinModelsManifests *BuiltinModelsManifestPreset `json:"builtinModelsManifests,omitempty"`
}

type ModelRegistryPreset struct {
	SchemaVersion        int                             `json:"schemaVersion"`
	PricingUnit          string                          `json:"pricingUnit,omitempty"`
	UpstreamCapabilities []ModelRegistryCapabilityPreset `json:"upstreamCapabilities"`
	BenchmarkProfiles    []ModelBenchmarkProfilePreset   `json:"benchmarkProfiles,omitempty"`
}

type ModelRegistryCapabilityPreset struct {
	Patterns                []string            `json:"patterns"`
	ContextWindowTokens     int                 `json:"contextWindowTokens,omitempty"`
	MaxOutputTokens         int                 `json:"maxOutputTokens,omitempty"`
	DefaultOutputTokens     int                 `json:"defaultOutputTokens,omitempty"`
	RecommendedOutputTokens int                 `json:"recommendedOutputTokens,omitempty"`
	ThinkingMode            string              `json:"thinkingMode,omitempty"`
	ReasoningEfforts        []string            `json:"reasoningEfforts,omitempty"`
	Provider                string              `json:"provider,omitempty"`
	DisplayName             string              `json:"displayName,omitempty"`
	Description             string              `json:"description,omitempty"`
	Capabilities            map[string]bool     `json:"capabilities,omitempty"`
	Pricing                 *ModelPricingPreset `json:"pricing,omitempty"`
	Sources                 []string            `json:"sources,omitempty"`
	// ParamConstraints 厂商文档已知的请求参数硬约束（如 Kimi 固定值采样参数）。
	// 与 config.ModelParamConstraints 字段一一对应；随本 preset 走定期刷新链路。
	ParamConstraints *ModelParamConstraintsPreset `json:"paramConstraints,omitempty"`
}

// ModelParamConstraintsPreset 对应 config.ModelParamConstraints 的可刷新载体。
type ModelParamConstraintsPreset struct {
	FixedParams                   []string               `json:"fixedParams,omitempty"`
	ToolChoiceRequiredUnsupported bool                   `json:"toolChoiceRequiredUnsupported,omitempty"`
	ThinkingFixedValue            map[string]interface{} `json:"thinkingFixedValue,omitempty"`
}

type ModelBenchmarkProfilePreset struct {
	Patterns             []string                       `json:"patterns"`
	CanonicalModel       string                         `json:"canonicalModel"`
	OverallScore         float64                        `json:"overallScore,omitempty"`
	CategoryScores       map[string]float64             `json:"categoryScores,omitempty"`
	BenchmarkEvidence    []ModelBenchmarkEvidencePreset `json:"benchmarkEvidence,omitempty"`
	Sources              []string                       `json:"sources,omitempty"`
	VerifiedAt           string                         `json:"verifiedAt,omitempty"`
	Lane                 string                         `json:"lane,omitempty"`
	SharedResults        int                            `json:"sharedResults,omitempty"`
	ComparableCategories int                            `json:"comparableCategories,omitempty"`
	TotalCategories      int                            `json:"totalCategories,omitempty"`
}

type ModelBenchmarkEvidencePreset struct {
	Benchmark        string  `json:"benchmark"`
	BenchmarkVersion string  `json:"benchmarkVersion"`
	SourceModel      string  `json:"sourceModel"`
	Domain           string  `json:"domain"`
	Metric           string  `json:"metric"`
	RawValue         float64 `json:"rawValue"`
	Uncertainty      float64 `json:"uncertainty,omitempty"`
	CohortPercentile float64 `json:"cohortPercentile"`
	TaskCount        int     `json:"taskCount"`
	CohortSize       int     `json:"cohortSize"`
	Effort           string  `json:"effort"`
	SelectionBasis   string  `json:"selectionBasis"`
	SourceURL        string  `json:"sourceUrl"`
	CapturedAt       string  `json:"capturedAt"`
}

type ModelPricingPreset struct {
	Unit                string                   `json:"unit,omitempty"`
	Currency            string                   `json:"currency,omitempty"`
	InputCacheHitPrice  *float64                 `json:"inputCacheHitPrice,omitempty"`
	InputCacheMissPrice *float64                 `json:"inputCacheMissPrice,omitempty"`
	OutputPrice         *float64                 `json:"outputPrice,omitempty"`
	Tiers               []ModelPricingTierPreset `json:"tiers,omitempty"`
}

type ModelPricingTierPreset struct {
	Label               string   `json:"label,omitempty"`
	InputTokensAbove    int      `json:"inputTokensAbove,omitempty"`
	InputTokensUpTo     int      `json:"inputTokensUpTo,omitempty"`
	InputCacheHitPrice  *float64 `json:"inputCacheHitPrice,omitempty"`
	InputCacheMissPrice *float64 `json:"inputCacheMissPrice,omitempty"`
	OutputPrice         *float64 `json:"outputPrice,omitempty"`
}

type ChannelPresetsPreset struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Collections   map[string]json.RawMessage `json:"-"`
}

type BuiltinModelsManifestPreset struct {
	SchemaVersion int                                `json:"schemaVersion"`
	Manifests     []BuiltinModelsManifestEntryPreset `json:"manifests"`
}

type BuiltinModelsManifestEntryPreset struct {
	BaseURLPattern       string   `json:"baseUrlPattern"`
	ServiceType          string   `json:"serviceType"`
	PlanHint             string   `json:"planHint,omitempty"`
	ModelsURL            string   `json:"modelsUrl,omitempty"`
	ModelIDs             []string `json:"modelIds"`
	ExcludeModelPatterns []string `json:"excludeModelPatterns,omitempty"`
	DisableProbe         bool     `json:"disableProbe"`
}

func (p *ChannelPresetsPreset) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Collections = make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		if key == "schemaVersion" {
			if err := json.Unmarshal(value, &p.SchemaVersion); err != nil {
				return err
			}
			continue
		}
		copied := append(json.RawMessage(nil), value...)
		p.Collections[key] = copied
	}
	return nil
}

func (p ChannelPresetsPreset) MarshalJSON() ([]byte, error) {
	body := make(map[string]json.RawMessage, len(p.Collections)+1)
	schemaBytes, err := json.Marshal(p.SchemaVersion)
	if err != nil {
		return nil, err
	}
	body["schemaVersion"] = schemaBytes
	for key, value := range p.Collections {
		body[key] = append(json.RawMessage(nil), value...)
	}
	return json.Marshal(body)
}

// SubscriptionPreset 是订阅中心的来源分类预置。
type SubscriptionPreset struct {
	// OriginTypes 来源类型及其推导出的信任等级。
	OriginTypes []OriginTypeEntry `json:"originTypes"`
	// BillingModes 计费模式枚举。
	BillingModes []string `json:"billingModes"`
	// Sources 订阅来源枚举（手动/自动发现）。
	Sources []string `json:"sources"`
	// AutoRefreshProviders 支持自动余额刷新的 provider 白名单。
	AutoRefreshProviders []string `json:"autoRefreshProviders"`
	// NewApiDefaults new-api 家族站点接入时的建议预填值。
	NewApiDefaults NewApiDefaults `json:"newApiDefaults"`
	// OriginTypeAliases 历史枚举归一化映射（如 public_benefit -> community）。
	// 读取存量数据时用于兼容，键为旧值、值为规范值。
	OriginTypeAliases map[string]string `json:"originTypeAliases,omitempty"`
}

// OriginTypeEntry 是单个来源类型到信任等级的映射。
type OriginTypeEntry struct {
	Value string `json:"value"`
	Tier  string `json:"tier"`
}

// NewApiDefaults 是 new-api 接入的建议预填值。
type NewApiDefaults struct {
	OriginType  string `json:"originType"`
	OriginTier  string `json:"originTier"`
	BillingMode string `json:"billingMode"`
}

// TierFor 返回给定来源类型（先经别名归一化）推导出的信任等级；
// 未命中返回 "unknown"。
func (s SubscriptionPreset) TierFor(originType string) string {
	canonical := s.Canonicalize(originType)
	for _, e := range s.OriginTypes {
		if e.Value == canonical {
			return e.Tier
		}
	}
	return "unknown"
}

// Canonicalize 把历史/别名来源类型归一化为规范值；无别名时原样返回。
func (s SubscriptionPreset) Canonicalize(originType string) string {
	if canonical, ok := s.OriginTypeAliases[originType]; ok {
		return canonical
	}
	return originType
}

// SupportsAutoRefresh 判断 provider 是否在自动余额刷新白名单内。
func (s SubscriptionPreset) SupportsAutoRefresh(provider string) bool {
	for _, p := range s.AutoRefreshProviders {
		if p == provider {
			return true
		}
	}
	return false
}
