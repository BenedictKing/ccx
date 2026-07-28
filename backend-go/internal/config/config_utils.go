package config

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// ============== 工具函数 ==============

const defaultCopilotBaseURL = "https://api.githubcopilot.com"

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

// deprecatedGrokModelMappings 记录已下线/不再需要的 grok 模型映射精确对照，
// 用于从渠道 modelMapping 中清除历史遗留项。
var deprecatedGrokModelMappings = map[string]string{
	"grok-4.1": "grok-4.1-thinking",
	"grok-4.2": "grok-4.20-beta",
}

// sanitizeDeprecatedGrokModelMapping 从 mapping 中精确剔除已废弃的 grok 映射对。
// 仅当 key 与 value 同时匹配才删除，避免影响用户自定义的其他 target。
// 返回 changed=true 表示发生了删除；未命中或 mapping 为 nil/空时原样返回，不分配新 map。
func sanitizeDeprecatedGrokModelMapping(mapping map[string]string) (map[string]string, bool) {
	if len(mapping) == 0 {
		return mapping, false
	}
	changed := false
	for k, v := range deprecatedGrokModelMappings {
		if mapping[k] == v {
			changed = true
			break
		}
	}
	if !changed {
		return mapping, false
	}
	cleaned := make(map[string]string, len(mapping))
	for k, v := range mapping {
		cleaned[k] = v
	}
	for k, v := range deprecatedGrokModelMappings {
		if cleaned[k] == v {
			delete(cleaned, k)
		}
	}
	return cleaned, true
}

func normalizeUpstreamServiceType(serviceType, fallback string) string {
	trimmed := strings.TrimSpace(serviceType)
	if trimmed != "" {
		return trimmed
	}
	return fallback
}

func normalizeAuthHeader(authHeader string) string {
	return strings.ToLower(strings.TrimSpace(authHeader))
}

func validateAuthHeader(authHeader string) error {
	switch normalizeAuthHeader(authHeader) {
	case "", "auto", "bearer", "x-api-key":
		return nil
	default:
		return fmt.Errorf("authHeader 仅支持 auto、bearer 或 x-api-key，当前为 %s", authHeader)
	}
}

func applyAuthHeader(authHeader string) (string, error) {
	normalized := normalizeAuthHeader(authHeader)
	if err := validateAuthHeader(normalized); err != nil {
		return "", err
	}
	if normalized == "auto" {
		return "", nil
	}
	return normalized, nil
}

// deduplicateBaseURLs 去重 BaseURLs，忽略尾部 / 和默认版本前缀差异，保留 # 语义。
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
		if _, exists := seen[canonical]; !exists {
			seen[canonical] = struct{}{}
			result = append(result, canonical)
		}
	}
	return result
}

func applyDefaultBaseURL(upstream *UpstreamConfig) {
	if upstream == nil || upstream.ServiceType != "copilot" || strings.TrimSpace(upstream.BaseURL) != "" || len(upstream.BaseURLs) > 0 {
		return
	}
	upstream.BaseURL = defaultCopilotBaseURL
}

// ConfigError 配置错误
type ConfigError struct {
	Message string
	Cause   error
}

func (e *ConfigError) Error() string {
	return e.Message
}

func (e *ConfigError) Unwrap() error {
	return e.Cause
}

var (
	ErrUnsupportedServiceType     = errors.New("unsupported service type")
	ErrDuplicateChannelName       = errors.New("duplicate channel name")
	ErrInvalidEmbeddingCapability = errors.New("invalid embedding capability")
)

// ============== 模型重定向 ==============

// RedirectModel 模型重定向
func RedirectModel(model string, upstream *UpstreamConfig) string {
	redirected, _ := RedirectModelWithMatch(model, upstream)
	return redirected
}

// RedirectModelWithMatch 返回模型重定向结果，并标记是否命中 ModelMapping。
func RedirectModelWithMatch(model string, upstream *UpstreamConfig) (string, bool) {
	if upstream == nil || upstream.ModelMapping == nil || len(upstream.ModelMapping) == 0 {
		return model, false
	}

	// 直接匹配（精确匹配优先）
	if mapped, ok := upstream.ModelMapping[model]; ok {
		return mapped, true
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
		if strings.Contains(model, m.source) {
			return m.target, true
		}
	}

	return model, false
}

// ResolveReasoningEffort 根据原始模型名解析 reasoning effort
func ResolveReasoningEffort(model string, upstream *UpstreamConfig) string {
	if upstream == nil || upstream.ReasoningMapping == nil || len(upstream.ReasoningMapping) == 0 {
		return ""
	}
	if effort, ok := upstream.ReasoningMapping[model]; ok {
		return NormalizeReasoningEffortForUpstream(upstream, effort)
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
		if strings.Contains(model, m.source) {
			return NormalizeReasoningEffortForUpstream(upstream, m.effort)
		}
	}
	return ""
}

// NormalizeReasoningEffortForUpstream 将通用 effort 收敛到特定上游实际支持的枚举。
func NormalizeReasoningEffortForUpstream(upstream *UpstreamConfig, effort string) string {
	effort = strings.TrimSpace(effort)
	if !isMiMoResponsesUpstream(upstream) {
		return effort
	}
	switch effort {
	case "max", "xhigh":
		return "high"
	case "off":
		return "none"
	default:
		return effort
	}
}

// NormalizeReasoningObjectForUpstream 修正透传请求中上游不支持的 reasoning.effort。
func NormalizeReasoningObjectForUpstream(req map[string]interface{}, upstream *UpstreamConfig) {
	if req == nil || !isMiMoResponsesUpstream(upstream) {
		return
	}
	reasoning, ok := req["reasoning"].(map[string]interface{})
	if !ok || reasoning == nil {
		return
	}
	effort, _ := reasoning["effort"].(string)
	if normalized := NormalizeReasoningEffortForUpstream(upstream, effort); normalized != effort {
		reasoning["effort"] = normalized
	}
}

func isMiMoResponsesUpstream(upstream *UpstreamConfig) bool {
	if upstream == nil || !strings.EqualFold(strings.TrimSpace(upstream.ServiceType), "responses") {
		return false
	}
	if strings.Contains(strings.ToLower(upstream.BaseURL), "xiaomimimo.com") {
		return true
	}
	for _, baseURL := range upstream.BaseURLs {
		if strings.Contains(strings.ToLower(baseURL), "xiaomimimo.com") {
			return true
		}
	}
	return false
}

// ApplyReasoningParamStyle 将统一的 reasoning effort 写成上游要求的参数形态。
func ApplyReasoningParamStyle(req map[string]interface{}, style string, effort string) {
	if req == nil {
		return
	}

	switch style {
	case "thinking":
		delete(req, "reasoning")
		delete(req, "reasoning_effort")
		if effort == "" {
			return
		}
		if effort == "off" || effort == "none" {
			req["thinking"] = map[string]interface{}{"type": "disabled"}
			return
		}
		thinking, _ := req["thinking"].(map[string]interface{})
		if thinking == nil {
			thinking = make(map[string]interface{})
		}
		thinking["type"] = "enabled"
		thinking["effort"] = effort
		delete(thinking, "budget_tokens")
		req["thinking"] = thinking
	case "reasoning_effort":
		delete(req, "reasoning")
		if effort != "" {
			req["reasoning_effort"] = effort
		}
	case ReasoningParamStyleGemini:
		applyGeminiThinkingConfig(req, effort)
	default:
		if effort != "" {
			req["reasoning"] = map[string]interface{}{"effort": effort}
		}
	}
}

// ReasoningParamStyleGemini 是 Gemini 原生协议的思考参数形态标识。
// Gemini 不使用 thinking / reasoning_effort / reasoning 任一形态，
// 而是写 generationConfig.thinkingConfig.thinkingLevel。
const ReasoningParamStyleGemini = "gemini"

// GeminiThinkingLevelForEffort 把统一 EffortLevel 字符串映射为 Gemini 的 thinkingLevel 取值。
//
// Gemini 官方 ThinkingLevel 枚举只有 MINIMAL / LOW / MEDIUM / HIGH（REST JSON 用小写），
// 没有对应 max/xhigh 的档位，因此最高档统一收敛到 "high"。
// 关闭思考不使用 thinkingLevel，而是走 thinkingBudget=0（仓库内既有的"已关闭"表示），
// 因此本函数对 off/none 返回 ok=false，由调用方按关闭语义处理。
// 无法识别的档位返回 ok=false，调用方必须保持请求体原样（fail-open，不注入伪字段）。
func GeminiThinkingLevelForEffort(effort string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "minimal", "min":
		return "minimal", true
	case "low":
		return "low", true
	case "medium", "med", "default":
		return "medium", true
	case "high", "xhigh", "max", "ultra":
		return "high", true
	default:
		return "", false
	}
}

// isGeminiThinkingDisabledEffort 判断 effort 是否表示"关闭思考"。
func isGeminiThinkingDisabledEffort(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "off", "none", "disabled":
		return true
	default:
		return false
	}
}

// applyGeminiThinkingConfig 把 effort 写入 generationConfig.thinkingConfig。
//
// 约束：
//   - thinkingLevel 与 thinkingBudget 互斥，写一个必须清掉另一个（上游会因两者同时出现报错）。
//   - off/none 用 thinkingBudget=0 表示关闭，与 extractGeminiEffortExplicit 的读路径一致。
//   - effort 为空或无法映射时整体不落任何字段，请求体保持原样。
func applyGeminiThinkingConfig(req map[string]interface{}, effort string) {
	if strings.TrimSpace(effort) == "" {
		return
	}

	disabled := isGeminiThinkingDisabledEffort(effort)
	level, ok := GeminiThinkingLevelForEffort(effort)
	if !disabled && !ok {
		// 无法映射到 Gemini 档位：不注入伪字段，交由上游沿用自身默认。
		return
	}

	generationConfig, _ := req["generationConfig"].(map[string]interface{})
	if generationConfig == nil {
		generationConfig = make(map[string]interface{})
	}
	thinkingConfig, _ := generationConfig["thinkingConfig"].(map[string]interface{})
	if thinkingConfig == nil {
		thinkingConfig = make(map[string]interface{})
	}

	if disabled {
		thinkingConfig["thinkingBudget"] = 0
		delete(thinkingConfig, "thinkingLevel")
	} else {
		thinkingConfig["thinkingLevel"] = level
		delete(thinkingConfig, "thinkingBudget")
	}

	generationConfig["thinkingConfig"] = thinkingConfig
	req["generationConfig"] = generationConfig
	// 清理裸 thinkingConfig 兼容形态，避免同一请求出现两处冲突配置。
	delete(req, "thinkingConfig")
}

func isValidReasoningEffort(reasoning string) bool {
	switch reasoning {
	case "", "off", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

// ============== 渠道状态与优先级辅助函数 ==============

// GetChannelStatus 获取渠道状态（带默认值处理）
func GetChannelStatus(upstream *UpstreamConfig) string {
	if upstream.Status == "" {
		return "active"
	}
	return upstream.Status
}

// GetChannelAdminState 获取渠道管理员配置状态。
func GetChannelAdminState(upstream *UpstreamConfig) string {
	return GetChannelStatus(upstream)
}

// GetChannelRuntimeState 获取渠道运行时状态视图（不依赖 metrics，仅反映配置侧可观察状态）。
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

// GetChannelEffectiveState 获取渠道当前有效状态视图。
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

// applySingleKeyReplacementTransition 统一处理“单 key 更换”带来的自动激活与熔断重置判定。
func applySingleKeyReplacementTransition(upstream *UpstreamConfig, newKeys []string) (shouldResetMetrics bool) {
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
	if u.APIKeyConfigs != nil {
		cloned.APIKeyConfigs = make([]APIKeyConfig, len(u.APIKeyConfigs))
		for i, cfg := range u.APIKeyConfigs {
			cloned.APIKeyConfigs[i] = cloneAPIKeyConfig(cfg)
		}
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
	if u.ModelCapabilities != nil {
		cloned.ModelCapabilities = make(map[string]UpstreamModelCapability, len(u.ModelCapabilities))
		for k, v := range u.ModelCapabilities {
			cloned.ModelCapabilities[k] = cloneUpstreamModelCapability(v)
		}
	}
	if u.EmbeddingCapabilities != nil {
		cloned.EmbeddingCapabilities = make(map[string]EmbeddingCapability, len(u.EmbeddingCapabilities))
		for k, v := range u.EmbeddingCapabilities {
			cloned.EmbeddingCapabilities[k] = cloneEmbeddingCapability(v)
		}
	}
	cloned.DefaultCapability = cloneUpstreamModelCapability(u.DefaultCapability)
	if u.CustomHeaders != nil {
		cloned.CustomHeaders = make(map[string]string, len(u.CustomHeaders))
		for k, v := range u.CustomHeaders {
			cloned.CustomHeaders[k] = v
		}
	}
	if u.CompatSeeds != nil {
		cloned.CompatSeeds = make(map[string]bool, len(u.CompatSeeds))
		for k, v := range u.CompatSeeds {
			cloned.CompatSeeds[k] = v
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
		for i, dk := range u.DisabledAPIKeys {
			cloned.DisabledAPIKeys[i] = dk
			if dk.Config != nil {
				c := cloneAPIKeyConfig(*dk.Config)
				cloned.DisabledAPIKeys[i].Config = &c
			}
		}
	}
	if u.DisabledKeyModels != nil {
		cloned.DisabledKeyModels = make([]DisabledKeyModelInfo, len(u.DisabledKeyModels))
		copy(cloned.DisabledKeyModels, u.DisabledKeyModels)
	}
	if u.AutoBlacklistBalance != nil {
		v := *u.AutoBlacklistBalance
		cloned.AutoBlacklistBalance = &v
	}
	if u.NormalizeMetadataUserID != nil {
		v := *u.NormalizeMetadataUserID
		cloned.NormalizeMetadataUserID = &v
	}
	if u.StripBillingHeader != nil {
		v := *u.StripBillingHeader
		cloned.StripBillingHeader = &v
	}
	if u.CodexToolCompat != nil {
		v := *u.CodexToolCompat
		cloned.CodexToolCompat = &v
	}
	if u.RateLimitAutoFromHeaders != nil {
		v := *u.RateLimitAutoFromHeaders
		cloned.RateLimitAutoFromHeaders = &v
	}
	if u.NoVisionModels != nil {
		cloned.NoVisionModels = make([]string, len(u.NoVisionModels))
		copy(cloned.NoVisionModels, u.NoVisionModels)
	}

	return &cloned
}

// ApplyProviderUpstreamDefaults 应用已知 Provider 原生协议所需的运行时默认值。
func ApplyProviderUpstreamDefaults(providerID string, upstream *UpstreamConfig) {
	if upstream == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(providerID)) {
	case "glm":
		if upstream.ServiceType == "openai" {
			upstream.ReasoningParamStyle = "reasoning_effort"
			upstream.PassbackReasoningContent = BoolPtr(true)
		}
	case "compshare":
		// Compshare 的 Claude 兼容端点只接受 user/assistant 消息。最新 Claude Code
		// 会在 messages 中插入 system 角色，因此必须先抽取到顶层 system 字段。
		if strings.EqualFold(strings.TrimSpace(upstream.ServiceType), "claude") {
			upstream.NormalizeSystemRoleToTopLevel = true
		}
	}
}

// RuntimeUpstreamForAutoManagedProvider 返回自动托管 provider 渠道的运行时视图。
//
// 已知 provider 的自动托管渠道不再使用编辑渠道页里的手工兼容开关；
// 模型选择和能力差异由 Autopilot 的 ModelResolver/EndpointAttemptPolicy 做 request-scoped 决策。
// 因此这里屏蔽历史版本可能写入配置的旧兼容字段，再恢复 Provider 原生协议默认值。
func RuntimeUpstreamForAutoManagedProvider(upstream *UpstreamConfig) *UpstreamConfig {
	if upstream == nil || !upstream.AutoManaged || strings.TrimSpace(upstream.ProviderID) == "" {
		return upstream
	}

	runtime := upstream.Clone()
	runtime.ModelMapping = nil
	runtime.ReasoningMapping = nil
	runtime.ReasoningParamStyle = ""
	runtime.FastMode = false
	// 兼容性开关置 nil 而非 false：nil 表示"用户未设置"，运行时兼容性学习结论仍可生效；
	// 置 false 会被当作"用户显式关闭"，使自动托管渠道永远学不会兼容改写。
	// 种子同样清空：自动托管渠道的兼容性由 ApplyProviderUpstreamDefaults 重建已知厂商真相
	// 加运行时学习决定，不保留手工配置派生的历史证据。
	runtime.CompatSeeds = nil
	runtime.NormalizeNonstandardChatRoles = nil
	runtime.CodexNativeToolPassthrough = nil
	runtime.CodexToolCompat = nil
	runtime.StripCodexClientTools = false
	runtime.StripImageGenerationTool = nil
	runtime.ConvertImageURLToB64JSON = false
	runtime.NormalizeMetadataUserID = nil
	runtime.StripBillingHeader = nil
	runtime.StripEmptyTextBlocks = nil
	runtime.NormalizeSystemRoleToTopLevel = false
	runtime.InjectDummyThoughtSignature = false
	runtime.StripThoughtSignature = false
	runtime.PassbackReasoningContent = nil
	runtime.PassbackThinkingBlocks = nil
	runtime.NoVision = false
	runtime.NoVisionModels = nil
	runtime.VisionFallbackModel = ""
	runtime.HistoricalImageTurnLimit = 0
	runtime.CompactModel = ""
	ApplyProviderUpstreamDefaults(runtime.ProviderID, runtime)
	return runtime
}

func applyModelCapabilityUpdates(upstream *UpstreamConfig, updates UpstreamUpdate) {
	if upstream == nil {
		return
	}
	if updates.ModelCapabilities != nil {
		upstream.ModelCapabilities = updates.ModelCapabilities
	}
	if updates.EmbeddingCapabilities != nil {
		upstream.EmbeddingCapabilities = updates.EmbeddingCapabilities
	}
	if updates.DefaultCapability != nil {
		upstream.DefaultCapability = *updates.DefaultCapability
	}
	if updates.AllowUnknownContext != nil {
		upstream.AllowUnknownContext = *updates.AllowUnknownContext
	}
}

// applyAPIKeyConfigUpdate 根据 UpstreamUpdate 同步 upstream.APIKeyConfigs：
//   - updates.APIKeyConfigs != nil：以新值为准，按当前 APIKeys 归一化（保留 orphan）
//   - updates.APIKeyConfigs == nil 但 updates.APIKeys != nil：仅按新 APIKeys 重新归一化原有 configs
//   - 两者都为 nil：不动 APIKeyConfigs
//
// 六类渠道 Update 函数共用，避免新增字段时遗漏其中某一处。
func applyAPIKeyConfigUpdate(upstream *UpstreamConfig, updates UpstreamUpdate) {
	if updates.APIKeyConfigs != nil {
		upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, updates.APIKeyConfigs)
	} else if updates.APIKeys != nil {
		upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)
	}
}

func cloneEmbeddingCapability(capability EmbeddingCapability) EmbeddingCapability {
	if capability.SupportedDimensions != nil {
		capability.SupportedDimensions = append([]int(nil), capability.SupportedDimensions...)
	}
	if capability.Normalized != nil {
		normalized := *capability.Normalized
		capability.Normalized = &normalized
	}
	return capability
}

func cloneAPIKeyConfig(cfg APIKeyConfig) APIKeyConfig {
	if cfg.Enabled != nil {
		v := *cfg.Enabled
		cfg.Enabled = &v
	}
	if cfg.GroupMultiplier != nil {
		v := *cfg.GroupMultiplier
		cfg.GroupMultiplier = &v
	}
	if cfg.MaxGroupMultiplier != nil {
		v := *cfg.MaxGroupMultiplier
		cfg.MaxGroupMultiplier = &v
	}
	if cfg.RateLimitAutoFromHeaders != nil {
		v := *cfg.RateLimitAutoFromHeaders
		cfg.RateLimitAutoFromHeaders = &v
	}
	if cfg.Models != nil {
		cfg.Models = append([]string(nil), cfg.Models...)
	}
	return cfg
}

func cloneAgentModelProfile(profile AgentModelProfile) AgentModelProfile {
	if profile.ReasoningEfforts != nil {
		profile.ReasoningEfforts = append([]string(nil), profile.ReasoningEfforts...)
	}
	return profile
}

func cloneUpstreamModelCapability(capability UpstreamModelCapability) UpstreamModelCapability {
	if capability.ReasoningEfforts != nil {
		capability.ReasoningEfforts = append([]string(nil), capability.ReasoningEfforts...)
	}
	if capability.Capabilities != nil {
		capabilities := make(map[string]bool, len(capability.Capabilities))
		for key, value := range capability.Capabilities {
			capabilities[key] = value
		}
		capability.Capabilities = capabilities
	}
	if capability.Pricing != nil {
		pricing := *capability.Pricing
		if capability.Pricing.InputCacheHitPrice != nil {
			value := *capability.Pricing.InputCacheHitPrice
			pricing.InputCacheHitPrice = &value
		}
		if capability.Pricing.InputCacheMissPrice != nil {
			value := *capability.Pricing.InputCacheMissPrice
			pricing.InputCacheMissPrice = &value
		}
		if capability.Pricing.OutputPrice != nil {
			value := *capability.Pricing.OutputPrice
			pricing.OutputPrice = &value
		}
		if capability.Pricing.Tiers != nil {
			pricing.Tiers = append([]ModelPricingTier(nil), capability.Pricing.Tiers...)
			for i := range pricing.Tiers {
				if pricing.Tiers[i].InputCacheHitPrice != nil {
					value := *pricing.Tiers[i].InputCacheHitPrice
					pricing.Tiers[i].InputCacheHitPrice = &value
				}
				if pricing.Tiers[i].InputCacheMissPrice != nil {
					value := *pricing.Tiers[i].InputCacheMissPrice
					pricing.Tiers[i].InputCacheMissPrice = &value
				}
				if pricing.Tiers[i].OutputPrice != nil {
					value := *pricing.Tiers[i].OutputPrice
					pricing.Tiers[i].OutputPrice = &value
				}
			}
		}
		capability.Pricing = &pricing
	}
	if capability.Sources != nil {
		capability.Sources = append([]string(nil), capability.Sources...)
	}
	if capability.ParamConstraints != nil {
		constraints := *capability.ParamConstraints
		if constraints.FixedParams != nil {
			constraints.FixedParams = append([]string(nil), constraints.FixedParams...)
		}
		if constraints.ThinkingFixedValue != nil {
			thinking := make(map[string]interface{}, len(constraints.ThinkingFixedValue))
			for k, v := range constraints.ThinkingFixedValue {
				thinking[k] = v
			}
			constraints.ThinkingFixedValue = thinking
		}
		capability.ParamConstraints = &constraints
	}
	return capability
}

// SupportsModel 检查渠道是否支持指定模型
// 空列表表示支持所有模型；支持精确匹配，以及 prefix* / *suffix / *contains* 形式的包含与排除规则。
func (u *UpstreamConfig) SupportsModel(model string) bool {
	supported, _ := u.ExplainModelSupport(model)
	return supported
}

// ExplainModelSupport 返回渠道是否支持指定模型，以及不支持时的原因。
func (u *UpstreamConfig) ExplainModelSupport(model string) (bool, string) {
	if len(u.SupportedModels) == 0 {
		return true, ""
	}

	includes, excludes := splitSupportedModelRules(u.SupportedModels)
	for _, pattern := range excludes {
		if matchSupportedModelPattern(pattern, model) {
			return false, "命中排除规则 !" + pattern
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
	return false, "未命中包含规则"
}

func splitSupportedModelRules(rules []string) (includes []string, excludes []string) {
	includes = make([]string, 0, len(rules))
	excludes = make([]string, 0, len(rules))
	for _, rawRule := range rules {
		// 兼容用户把多条规则用顿号/逗号粘进同一项的情况，先按分隔符拆分
		for _, rule := range parseSupportedModelInput(rawRule) {
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
	}
	return includes, excludes
}

// supportedModelSeparatorPattern 模型规则分隔符：空白、中文顿号、逗号（中英文）、分号（中英文）、竖线
var supportedModelSeparatorPattern = regexp.MustCompile(`[\s、,，;；|]+`)

// supportedModelTokenPattern 模型名合法字符集：字母、数字、点、下划线、连字符、冒号、斜杠，外加通配符 * 与排除前缀 !
var supportedModelTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:/*!-]+$`)

// parseSupportedModelInput 将原始规则文本按合法分隔符拆分为独立规则，过滤空白项。
// 例如 "GPT-5*、ada*" -> ["GPT-5*", "ada*"]。
func parseSupportedModelInput(raw string) []string {
	parts := supportedModelSeparatorPattern.Split(raw, -1)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func isValidSupportedModelPattern(pattern string) bool {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return false
	}
	// 仅允许模型名合法字符集；含顿号等非法字符直接拒绝
	if !supportedModelTokenPattern.MatchString(trimmed) {
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

// GetEffectiveBaseURL 获取当前应使用的 BaseURL（纯 failover 模式）
// 优先使用 BaseURL 字段（支持调用方临时覆盖），否则从 BaseURLs 数组获取
func (u *UpstreamConfig) GetEffectiveBaseURL() string {
	// 优先使用 BaseURL（可能被调用方临时设置用于指定本次请求的 URL）
	if u.BaseURL != "" {
		return utils.CanonicalBaseURL(u.BaseURL, u.ServiceType)
	}

	// 回退到 BaseURLs 数组
	if len(u.BaseURLs) > 0 {
		return utils.CanonicalBaseURL(u.BaseURLs[0], u.ServiceType)
	}

	return ""
}

// GetAllBaseURLs 获取所有 BaseURL（用于延迟测试）
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

// BoundBaseURLForKey 返回某 API Key 通过 APIKeyConfigs 绑定的上游端点（已归一化）。
//
// provider 模板化添加时，不同 plan 的 Key（如 MiMo sk-/tp-）各自绑定成功探测的 baseURL，
// failover 遍历应仅在绑定端点上尝试该 Key，避免多 baseURL × 多 Key 的无效笛卡尔积。
//
// 返回空串表示该 Key 未绑定端点（历史手填渠道 / 自定义模式），调用方应保持原有笛卡尔积行为。
// 归一化后与 GetAllBaseURLs 的元素同源，便于直接字符串比较。
func (u *UpstreamConfig) BoundBaseURLForKey(apiKey string) string {
	for _, cfg := range u.APIKeyConfigs {
		if cfg.Key == apiKey {
			if cfg.BaseURL == "" {
				return ""
			}
			return utils.CanonicalBaseURL(cfg.BaseURL, u.ServiceType)
		}
	}
	return ""
}

// BaseURLsForKey 返回某 API Key 应当探测的 BaseURL 列表。
//
// 绑定优先：Key 通过 APIKeyConfigs 绑定端点时，只返回该端点（已归一化），
// 不参与渠道级 BaseURL 的笛卡尔积，避免混合套餐渠道把 Agent Plan Key 误打到 Coding Plan 入口。
// 未绑定（历史手填 / 自定义渠道）时回退到 GetAllBaseURLs，保持原有按顺序回退遍历语义。
//
// 纯只读：不修改原 UpstreamConfig，避免并发保活任务污染共享配置快照。
func (u *UpstreamConfig) BaseURLsForKey(apiKey string) []string {
	if bound := u.BoundBaseURLForKey(apiKey); bound != "" {
		return []string{bound}
	}
	return u.GetAllBaseURLs()
}
