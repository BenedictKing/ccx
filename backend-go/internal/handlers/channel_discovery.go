package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

const (
	discoveryProbeTimeout      = 10 * time.Second
	defaultChannelDiscoveryRPM = 30
	maxDiscovery429Retries     = 2
)

type discoveryProbeWaitFunc func(context.Context, time.Duration) error

type discoveryProbePacer struct {
	initialRPM       int
	effectiveRPM     int
	rateLimitedCount int
	nextProbeAt      time.Time
	now              func() time.Time
	wait             discoveryProbeWaitFunc
}

func newDiscoveryProbePacer(initialRPM int) *discoveryProbePacer {
	if initialRPM <= 0 {
		initialRPM = defaultChannelDiscoveryRPM
	}
	return &discoveryProbePacer{
		initialRPM:   initialRPM,
		effectiveRPM: initialRPM,
		now:          time.Now,
		wait: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

func (p *discoveryProbePacer) waitForNext(ctx context.Context) error {
	if p == nil {
		return nil
	}
	now := p.now()
	if p.nextProbeAt.After(now) {
		if err := p.wait(ctx, p.nextProbeAt.Sub(now)); err != nil {
			return err
		}
		now = p.now()
	}
	interval := time.Minute / time.Duration(p.effectiveRPM)
	p.nextProbeAt = now.Add(interval)
	return nil
}

func (p *discoveryProbePacer) observeRateLimited() {
	if p == nil {
		return
	}
	p.rateLimitedCount++
	nextRPM := p.effectiveRPM / 2
	if nextRPM < 1 {
		nextRPM = 1
	}
	p.effectiveRPM = nextRPM
	candidate := p.now().Add(time.Minute / time.Duration(p.effectiveRPM))
	if candidate.After(p.nextProbeAt) {
		p.nextProbeAt = candidate
	}
}

func (p *discoveryProbePacer) result() DiscoveryRateLimitResult {
	if p == nil {
		return DiscoveryRateLimitResult{}
	}
	return DiscoveryRateLimitResult{
		InitialRPM:       p.initialRPM,
		EffectiveRPM:     p.effectiveRPM,
		RateLimited:      p.rateLimitedCount > 0,
		RateLimitedCount: p.rateLimitedCount,
	}
}

type ChannelDiscoveryRequest struct {
	ChannelKind        string            `json:"channelKind"`
	ServiceType        string            `json:"serviceType"`
	BaseURL            string            `json:"baseUrl"`
	BaseURLs           []string          `json:"baseUrls"`
	APIKey             string            `json:"apiKey"`
	AuthHeader         string            `json:"authHeader"`
	CustomHeaders      map[string]string `json:"customHeaders"`
	ProxyURL           string            `json:"proxyUrl"`
	ProxyPreferDirect  bool              `json:"proxyPreferDirect"`
	InsecureSkipVerify bool              `json:"insecureSkipVerify"`
	ModelMapping       map[string]string `json:"modelMapping"`
	ReasoningMapping   map[string]string `json:"reasoningMapping"`
	TargetClients      []string          `json:"targetClients"`
	// ProbeAllModels 仅用于兼容旧版调用方。serviceType 为空时服务端会自动全量探测。
	ProbeAllModels bool `json:"probeAllModels"`
}

type DiscoveryModelsFetchRequest struct {
	ChannelKind        string
	ServiceType        string
	BaseURL            string
	BaseURLs           []string
	APIKey             string
	AuthHeader         string
	CustomHeaders      map[string]string
	ProxyURL           string
	ProxyPreferDirect  bool
	InsecureSkipVerify bool
	// LearnedClientFingerprint 渠道学习到的客户端伪装标记（保活 L1 等调用方传入），
	// 命中时 GetChannelModels 请求带 Claude Code 客户端伪装头。
	LearnedClientFingerprint bool
}

type DiscoveryModelsFetchResponse struct {
	StatusCode int
	URL        string
	Body       []byte
}

type ChannelDiscoveryModelFetcher func(context.Context, DiscoveryModelsFetchRequest) (DiscoveryModelsFetchResponse, error)

type ChannelDiscoveryModelFetchers map[string]ChannelDiscoveryModelFetcher

type DiscoverySelectedModels struct {
	Strong  string `json:"strong,omitempty"`
	Primary string `json:"primary,omitempty"`
	Fast    string `json:"fast,omitempty"`
}

type DiscoveryModelsResult struct {
	Source     string                  `json:"source"`
	URL        string                  `json:"url,omitempty"`
	StatusCode int                     `json:"statusCode,omitempty"`
	Items      []string                `json:"items"`
	Selected   DiscoverySelectedModels `json:"selected"`
	Warnings   []string                `json:"warnings,omitempty"`
}

type DiscoveryProtocolResult struct {
	Protocol      string   `json:"protocol"`
	Success       bool     `json:"success"`
	SuccessModels []string `json:"successModels,omitempty"`
	FailedModels  []string `json:"failedModels,omitempty"`
	LatencyMs     int64    `json:"latencyMs,omitempty"`
	Error         string   `json:"error,omitempty"`
}

type DiscoveryCapabilityProbeResult struct {
	Tested         bool            `json:"tested"`
	Supported      bool            `json:"supported"`
	Required       bool            `json:"required,omitempty"`
	StatusCode     int             `json:"statusCode,omitempty"`
	Evidence       string          `json:"evidence,omitempty"`
	Error          string          `json:"error,omitempty"`
	Recommendation map[string]bool `json:"recommendation,omitempty"`
}

type DiscoveryCapabilitiesResult struct {
	ToolCalls        DiscoveryCapabilityProbeResult `json:"toolCalls"`
	Vision           DiscoveryCapabilityProbeResult `json:"vision"`
	ImageGeneration  DiscoveryCapabilityProbeResult `json:"imageGeneration"`
	ThinkingPassback DiscoveryCapabilityProbeResult `json:"thinkingPassback"`
}

type DiscoveryRateLimitResult struct {
	InitialRPM       int  `json:"initialRpm"`
	EffectiveRPM     int  `json:"effectiveRpm"`
	RateLimited      bool `json:"rateLimited"`
	RateLimitedCount int  `json:"rateLimitedCount,omitempty"`
}

type DiscoveryEvidence struct {
	Type    string `json:"type"`
	Key     string `json:"key,omitempty"`
	Message string `json:"message"`
}

type DiscoveryRecommendation struct {
	ChannelKind         string                 `json:"channelKind"`
	ServiceType         string                 `json:"serviceType"`
	BaseURLs            []string               `json:"baseUrls,omitempty"`
	ModelMapping        map[string]string      `json:"modelMapping"`
	ReasoningMapping    map[string]string      `json:"reasoningMapping,omitempty"`
	SupportedModels     []string               `json:"supportedModels,omitempty"`
	NoVisionModels      []string               `json:"noVisionModels,omitempty"`
	VisionFallbackModel string                 `json:"visionFallbackModel,omitempty"`
	Compat              map[string]bool        `json:"compat,omitempty"`
	URLRecommendation   *URLRecommendation     `json:"urlRecommendation,omitempty"`
	Evidence            []DiscoveryEvidence    `json:"evidence,omitempty"`
	Alternatives        []DiscoveryAlternative `json:"alternatives,omitempty"`
}

type DiscoveryAlternative struct {
	ChannelKind string `json:"channelKind"`
	Reason      string `json:"reason"`
}

type ChannelDiscoveryResponse struct {
	Models         DiscoveryModelsResult       `json:"models"`
	Protocols      []DiscoveryProtocolResult   `json:"protocols"`
	Capabilities   DiscoveryCapabilitiesResult `json:"capabilities"`
	Recommendation DiscoveryRecommendation     `json:"recommendation"`
	RateLimit      DiscoveryRateLimitResult    `json:"rateLimit"`
	Evidence       []DiscoveryEvidence         `json:"evidence,omitempty"`
}

func ChannelDiscovery(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return channelDiscoveryWithPacerFactory(cfgManager, nil, func() *discoveryProbePacer {
		return newDiscoveryProbePacer(defaultChannelDiscoveryRPM)
	})
}

func ChannelDiscoveryWithModelFetchers(cfgManager *config.ConfigManager, modelFetchers ChannelDiscoveryModelFetchers) gin.HandlerFunc {
	return channelDiscoveryWithPacerFactory(cfgManager, modelFetchers, func() *discoveryProbePacer {
		return newDiscoveryProbePacer(defaultChannelDiscoveryRPM)
	})
}

func channelDiscoveryWithPacerFactory(
	cfgManager *config.ConfigManager,
	modelFetchers ChannelDiscoveryModelFetchers,
	pacerFactory func() *discoveryProbePacer,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ChannelDiscoveryRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		channel, err := buildTransientDiscoveryChannel(req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		globalCapabilities := map[string]config.UpstreamModelCapability(nil)
		if cfgManager != nil {
			globalCapabilities = cfgManager.GetConfig().UpstreamModelCapabilities
		}

		autoDetectServiceType := strings.TrimSpace(req.ServiceType) == ""
		models := discoverTransientModelsWithFetchers(c.Request.Context(), channel, normalizeDiscoveryChannelKind(req.ChannelKind), channel.APIKeys[0], modelFetchers)
		if len(models.Items) == 0 {
			models.Items = fallbackDiscoveryProbeModels(req.ChannelKind, channel.ServiceType)
			models.Source = "fallback"
			models.Warnings = append(models.Warnings, "models endpoint unavailable; used built-in probe candidates")
		}
		models.Selected = selectDiscoveryModels(models.Items, globalCapabilities)

		probeModels := discoveryProtocolProbeModels(models, req.ProbeAllModels || autoDetectServiceType)
		pacer := pacerFactory()
		protocols := runDiscoveryProtocolProbes(c.Request.Context(), channel, probeModels, cfgManager, pacer)
		successByProtocol := discoverySuccessModelsByProtocol(protocols)
		recommendedKind := recommendDiscoveryChannelKind(req.ChannelKind, req.TargetClients, protocols)
		channel.ServiceType = resolveDiscoveryServiceType(req.ServiceType, recommendedKind)

		recommendation := buildDiscoveryMappingRecommendation(recommendedKind, compatProbeProtocol(channel, recommendedKind), models.Selected, successByProtocol, req.TargetClients)
		recommendation.ServiceType = channel.ServiceType
		recommendation.BaseURLs = append([]string(nil), channel.BaseURLs...)
		// 实际生效的成功模型列表：当 channelKind 协议无成功模型时（已做过 fallback
		// 建映射）此处同步用 fallback 结果，避免后续探测从空 successByProtocol 取数。
		effectiveSuccessModels := discoveryEffectiveSuccessModels(recommendedKind, compatProbeProtocol(channel, recommendedKind), successByProtocol)
		applyDiscoveryModelCapabilityRecommendations(&recommendation, models.Items, effectiveSuccessModels, globalCapabilities)
		capabilities := DiscoveryCapabilitiesResult{}
		if recommendation.ChannelKind != "" {
			compatModel := discoveryCompatProbeModel(recommendation.ChannelKind, models.Selected, effectiveSuccessModels)
			visionModel := discoveryVisionProbeModel(recommendation, models.Items, effectiveSuccessModels, globalCapabilities, compatModel)
			compat := runCompatDiagnoseWithProbeModel(channel, recommendation.ChannelKind, channel.APIKeys[0], capabilityTestBaseURL(channel), compatModel)
			recommendation.Compat = compat.Recommendations
			recommendation.URLRecommendation = compat.URLRecommendations
			// 按 key 排序后遍历，保证证据列表顺序确定（map 迭代顺序不稳定）。
			compatEvidenceKeys := make([]string, 0, len(compat.Evidence))
			for key := range compat.Evidence {
				compatEvidenceKeys = append(compatEvidenceKeys, key)
			}
			sort.Strings(compatEvidenceKeys)
			for _, key := range compatEvidenceKeys {
				recommendation.Evidence = append(recommendation.Evidence, DiscoveryEvidence{Type: "compat", Key: key, Message: compat.Evidence[key]})
			}
			capabilities = runDiscoveryCapabilityProbes(channel, recommendation.ChannelKind, channel.APIKeys[0], capabilityTestBaseURL(channel), compatModel, visionModel, req.TargetClients, compat)
			mergeDiscoveryCapabilityRecommendations(&recommendation, capabilities)
		}

		evidence := buildDiscoveryEvidence(models, protocols, recommendation)
		c.JSON(http.StatusOK, ChannelDiscoveryResponse{
			Models:         models,
			Protocols:      protocols,
			Capabilities:   capabilities,
			Recommendation: recommendation,
			RateLimit:      pacer.result(),
			Evidence:       evidence,
		})
	}
}

func buildTransientDiscoveryChannel(req ChannelDiscoveryRequest) (*config.UpstreamConfig, error) {
	baseURLs := normalizeDiscoveryBaseURLs(req.BaseURL, req.BaseURLs)
	if len(baseURLs) == 0 {
		return nil, errors.New("baseUrl is required")
	}
	for _, baseURL := range baseURLs {
		if err := utils.ValidateBaseURL(baseURL); err != nil {
			return nil, err
		}
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return nil, errors.New("apiKey is required")
	}
	return &config.UpstreamConfig{
		Name:               "临时发现渠道",
		ServiceType:        strings.TrimSpace(req.ServiceType),
		BaseURL:            baseURLs[0],
		BaseURLs:           baseURLs,
		APIKeys:            []string{apiKey},
		AuthHeader:         strings.TrimSpace(req.AuthHeader),
		CustomHeaders:      cloneStringMap(req.CustomHeaders),
		ProxyURL:           strings.TrimSpace(req.ProxyURL),
		ProxyPreferDirect:  req.ProxyPreferDirect,
		InsecureSkipVerify: req.InsecureSkipVerify,
		ModelMapping:       cloneStringMap(req.ModelMapping),
		ReasoningMapping:   cloneStringMap(req.ReasoningMapping),
	}, nil
}

func normalizeDiscoveryBaseURLs(baseURL string, baseURLs []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(baseURLs)+1)
	add := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	add(baseURL)
	for _, value := range baseURLs {
		add(value)
	}
	return result
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		dst[trimmedKey] = trimmedValue
	}
	if len(dst) == 0 {
		return nil
	}
	return dst
}

func selectDiscoveryModels(models []string, global map[string]config.UpstreamModelCapability) DiscoverySelectedModels {
	unique := uniqueDiscoveryModels(models)
	if len(unique) == 0 {
		return DiscoverySelectedModels{}
	}

	selected := DiscoverySelectedModels{
		Strong:  bestDiscoveryModel(unique, global, []string{"opus", "pro", "max", "ultra", "codex"}),
		Primary: bestDiscoveryModel(unique, global, []string{"sonnet", "gpt", "chat", "main"}),
		Fast:    bestDiscoveryModel(unique, global, []string{"haiku", "mini", "flash", "lite", "fast"}),
	}
	fallback := bestDiscoveryFallbackModel(unique, global)
	if selected.Strong == "" {
		selected.Strong = firstNonEmptyDiscoveryModel(selected.Primary, selected.Fast, fallback)
	}
	if selected.Primary == "" {
		selected.Primary = firstNonEmptyDiscoveryModel(selected.Strong, selected.Fast, fallback)
	}
	if selected.Fast == "" {
		selected.Fast = firstNonEmptyDiscoveryModel(selected.Primary, selected.Strong, fallback)
	}
	return selected
}

func uniqueDiscoveryModels(models []string) []string {
	result := uniqueDiscoveryModelsPreservingOrder(models)
	sort.Strings(result)
	return result
}

// uniqueDiscoveryModelsPreservingOrder 去重并保留首次出现顺序（平台目录的原生排序
// 通常把主推模型排在前面，是探测候选补位的关键信号）。
func uniqueDiscoveryModelsPreservingOrder(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	result := make([]string, 0, len(models))
	for _, model := range models {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func bestDiscoveryModel(models []string, global map[string]config.UpstreamModelCapability, keywords []string) string {
	best := ""
	bestScore := -1
	for _, model := range models {
		keywordScore := discoveryModelKeywordScore(model, keywords)
		if keywordScore == 0 {
			continue
		}
		score := keywordScore + discoveryModelCapabilityScore(model, global)
		if score > bestScore {
			best = model
			bestScore = score
		}
	}
	if bestScore <= 0 {
		return ""
	}
	return best
}

func bestDiscoveryFallbackModel(models []string, global map[string]config.UpstreamModelCapability) string {
	best := ""
	bestScore := -1
	for _, model := range models {
		score := discoveryModelCapabilityScore(model, global)
		if score > bestScore {
			best = model
			bestScore = score
		}
	}
	if best != "" {
		return best
	}
	return models[0]
}

func firstNonEmptyDiscoveryModel(models ...string) string {
	for _, model := range models {
		if strings.TrimSpace(model) != "" {
			return model
		}
	}
	return ""
}

func discoveryModelCapabilityScore(model string, global map[string]config.UpstreamModelCapability) int {
	resolved := config.ResolveUpstreamCapability(model, nil, global)
	score := 0
	if resolved.Capability.ContextWindowTokens > 0 {
		score += resolved.Capability.ContextWindowTokens / 100000
	}
	if known, supported := discoveryModelKnownCapability(model, "toolCalls", global); known {
		if supported {
			score += 20
		} else {
			score -= 20
		}
	}
	if len(resolved.Capability.ReasoningEfforts) > 0 || strings.TrimSpace(resolved.Capability.ThinkingMode) != "" {
		score += 4
	}
	return score
}

func discoveryModelKeywordScore(model string, keywords []string) int {
	lower := strings.ToLower(model)
	score := 0
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			score += 10
		}
	}
	return score
}

func buildDiscoveryMappingRecommendation(
	channelKind string,
	preferredProtocol string,
	selected DiscoverySelectedModels,
	successByProtocol map[string][]string,
	targetClients []string,
) DiscoveryRecommendation {
	// 当 channelKind 协议无成功模型时降级到其他成功协议，确保别名映射仍能生成。
	// preferredProtocol 来自 compatProbeProtocol，反映 serviceType 对应的实际上游协议。
	successful := make(map[string]struct{})
	for _, model := range discoveryEffectiveSuccessModels(channelKind, preferredProtocol, successByProtocol) {
		successful[model] = struct{}{}
	}

	modelMapping := make(map[string]string)
	add := func(alias, model string) {
		if model == "" {
			return
		}
		if _, ok := successful[model]; !ok {
			return
		}
		modelMapping[alias] = model
	}

	switch channelKind {
	case "messages":
		add("opus", selected.Strong)
		add("sonnet", selected.Primary)
		add("haiku", selected.Fast)
		add("fable", selected.Strong)
	case "responses", "chat":
		add("gpt", selected.Primary)
		add("mini", selected.Fast)
		add("codex", firstSuccessfulDiscoveryModel(successful, selected.Strong, selected.Primary))
	case "gemini":
		add("gemini", selected.Primary)
		add("pro", selected.Strong)
		add("flash", selected.Fast)
	}

	reasoningMapping := discoveryReasoningMapping(channelKind, modelMapping)
	evidence := []DiscoveryEvidence(nil)
	if len(reasoningMapping) > 0 {
		evidence = append(evidence, DiscoveryEvidence{Type: "reasoning", Message: "思考强度为按源模型角色给出的默认建议；发现流程会继续验证工具调用与思考回传要求"})
	}
	return DiscoveryRecommendation{
		ChannelKind:      channelKind,
		ModelMapping:     modelMapping,
		ReasoningMapping: reasoningMapping,
		Evidence:         evidence,
	}
}

func firstSuccessfulDiscoveryModel(successful map[string]struct{}, models ...string) string {
	for _, model := range models {
		if _, ok := successful[model]; ok {
			return model
		}
	}
	return ""
}

func discoveryReasoningMapping(channelKind string, modelMapping map[string]string) map[string]string {
	reasoning := make(map[string]string)
	add := func(alias, effort string) {
		if _, ok := modelMapping[alias]; ok {
			reasoning[alias] = effort
		}
	}
	switch channelKind {
	case "messages":
		add("fable", "max")
		add("opus", "max")
		add("sonnet", "max")
		add("haiku", "high")
	case "responses", "chat":
		add("gpt", "max")
		add("mini", "high")
		add("codex", "high")
		// gemini: reasoningMapping 暂不推荐，Gemini handler 目前不消费该字段，
		// 写入配置只会产生无效噪声。待 Gemini thinking 路径完整支持后再启用。
	}
	if len(reasoning) == 0 {
		return nil
	}
	return reasoning
}

func applyDiscoveryModelCapabilityRecommendations(
	recommendation *DiscoveryRecommendation,
	allModels []string,
	successfulModels []string,
	global map[string]config.UpstreamModelCapability,
) {
	if recommendation == nil || len(recommendation.ModelMapping) == 0 {
		return
	}

	mappedModels := uniqueDiscoveryModels(mapValuesDiscoveryModels(recommendation.ModelMapping))
	noVisionModels := make([]string, 0, len(mappedModels))
	noToolModels := make([]string, 0)
	for _, model := range mappedModels {
		if known, supported := discoveryModelKnownCapability(model, "vision", global); known && !supported {
			noVisionModels = append(noVisionModels, model)
		}
		if known, supported := discoveryModelKnownCapability(model, "toolCalls", global); known && !supported {
			noToolModels = append(noToolModels, model)
		}
	}
	if len(noVisionModels) > 0 {
		recommendation.NoVisionModels = noVisionModels
		if fallback := bestDiscoveryVisionModel(successfulModels, allModels, global, noVisionModels); fallback != "" {
			recommendation.VisionFallbackModel = fallback
			recommendation.Evidence = append(recommendation.Evidence, DiscoveryEvidence{
				Type:    "vision",
				Key:     "visionFallbackModel",
				Message: fmt.Sprintf("内置能力表显示部分映射模型不支持图片输入，推荐使用 %s 作为图片回退模型", fallback),
			})
		} else {
			recommendation.Evidence = append(recommendation.Evidence, DiscoveryEvidence{
				Type:    "vision",
				Key:     "noVisionModels",
				Message: "内置能力表显示部分映射模型不支持图片输入，未找到可确认的图片回退模型",
			})
		}
	}
	if len(noToolModels) > 0 {
		recommendation.Evidence = append(recommendation.Evidence, DiscoveryEvidence{
			Type:    "capability",
			Key:     "toolCalls",
			Message: fmt.Sprintf("内置能力表显示部分映射模型可能不支持工具调用：%s", strings.Join(noToolModels, ", ")),
		})
	}
}

func mapValuesDiscoveryModels(mapping map[string]string) []string {
	values := make([]string, 0, len(mapping))
	for _, value := range mapping {
		values = append(values, value)
	}
	return values
}

func bestDiscoveryVisionModel(successfulModels []string, allModels []string, global map[string]config.UpstreamModelCapability, exclude []string) string {
	excluded := make(map[string]struct{}, len(exclude))
	for _, model := range exclude {
		excluded[model] = struct{}{}
	}
	candidates := append([]string(nil), successfulModels...)
	candidates = append(candidates, allModels...)
	candidates = uniqueDiscoveryModels(candidates)
	best := ""
	bestScore := -1
	for _, model := range candidates {
		if _, ok := excluded[model]; ok {
			continue
		}
		known, supported := discoveryModelKnownCapability(model, "vision", global)
		if !known || !supported {
			continue
		}
		score := discoveryModelCapabilityScore(model, global)
		if score > bestScore {
			best = model
			bestScore = score
		}
	}
	return best
}

func discoveryModelKnownCapability(model, capability string, global map[string]config.UpstreamModelCapability) (bool, bool) {
	resolved := config.ResolveUpstreamCapability(model, nil, global)
	if !resolved.Known || resolved.Capability.Capabilities == nil {
		return false, false
	}
	supported, exists := resolved.Capability.Capabilities[capability]
	if !exists {
		return true, false
	}
	return true, supported
}

const discoveryToolProbeName = "ccx_probe"

func runDiscoveryCapabilityProbes(
	channel *config.UpstreamConfig,
	channelKind string,
	apiKey string,
	baseURL string,
	probeModel string,
	visionProbeModel string,
	targetClients []string,
	compat CompatDiagnoseResult,
) DiscoveryCapabilitiesResult {
	return DiscoveryCapabilitiesResult{
		ToolCalls:        runDiscoveryToolCallProbe(channel, channelKind, apiKey, baseURL, probeModel, targetClients),
		Vision:           runDiscoveryVisionProbe(channel, channelKind, apiKey, baseURL, visionProbeModel),
		ImageGeneration:  discoveryImageGenerationProbeResult(compat),
		ThinkingPassback: discoveryThinkingPassbackProbeResult(compat),
	}
}

func mergeDiscoveryCapabilityRecommendations(recommendation *DiscoveryRecommendation, capabilities DiscoveryCapabilitiesResult) {
	merge := func(probe DiscoveryCapabilityProbeResult, key string) {
		if probe.Tested && probe.Evidence != "" {
			recommendation.Evidence = append(recommendation.Evidence, DiscoveryEvidence{Type: "capability", Key: key, Message: probe.Evidence})
		}
		if len(probe.Recommendation) == 0 {
			return
		}
		if recommendation.Compat == nil {
			recommendation.Compat = make(map[string]bool)
		}
		for name, value := range probe.Recommendation {
			recommendation.Compat[name] = value
		}
	}

	merge(capabilities.ToolCalls, "toolCalls")
	merge(capabilities.Vision, "vision")
	merge(capabilities.ImageGeneration, "imageGeneration")
	merge(capabilities.ThinkingPassback, "thinkingPassback")
}

func discoveryImageGenerationProbeResult(compat CompatDiagnoseResult) DiscoveryCapabilityProbeResult {
	evidence := strings.TrimSpace(compat.Evidence["stripImageGenerationTool"])
	strip, tested := compat.Recommendations["stripImageGenerationTool"]
	if !tested {
		return DiscoveryCapabilityProbeResult{
			Tested:    false,
			Supported: false,
			Evidence:  strings.TrimSpace(compat.Evidence["imageGenerationToolProbe"]),
		}
	}
	return DiscoveryCapabilityProbeResult{
		Tested:    true,
		Supported: !strip,
		Evidence:  evidence,
		Recommendation: map[string]bool{
			"stripImageGenerationTool": strip,
		},
	}
}

func discoveryVisionProbeModel(
	recommendation DiscoveryRecommendation,
	allModels []string,
	successfulModels []string,
	global map[string]config.UpstreamModelCapability,
	fallbackModel string,
) string {
	if model := strings.TrimSpace(recommendation.VisionFallbackModel); model != "" {
		return model
	}
	if model := bestDiscoveryVisionModel(successfulModels, allModels, global, recommendation.NoVisionModels); model != "" {
		return model
	}
	return fallbackModel
}

func discoveryThinkingPassbackProbeResult(compat CompatDiagnoseResult) DiscoveryCapabilityProbeResult {
	keys := []string{"passbackReasoningContent", "passbackThinkingBlocks"}
	evidence := make([]string, 0, len(keys))
	recommendation := make(map[string]bool)
	required := false
	tested := false

	for _, key := range keys {
		if message := strings.TrimSpace(compat.Evidence[key]); message != "" {
			tested = true
			evidence = append(evidence, fmt.Sprintf("%s: %s", key, message))
		}
		if value, ok := compat.Recommendations[key]; ok {
			recommendation[key] = value
			if value {
				required = true
			}
		}
	}
	if !tested {
		return DiscoveryCapabilityProbeResult{
			Tested:    false,
			Supported: false,
			Evidence:  "当前上游类型无思考回传探测项或探测未得出结论",
		}
	}

	return DiscoveryCapabilityProbeResult{
		Tested:         true,
		Supported:      true,
		Required:       required,
		Evidence:       strings.Join(evidence, " / "),
		Recommendation: recommendation,
	}
}

func runDiscoveryToolCallProbe(channel *config.UpstreamConfig, channelKind, apiKey, baseURL, probeModel string, targetClients []string) DiscoveryCapabilityProbeResult {
	protocol := compatProbeProtocol(channel, channelKind)
	if protocol == "" {
		return DiscoveryCapabilityProbeResult{
			Tested:    false,
			Supported: false,
			Evidence:  "当前渠道类型无工具调用探测项",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	req, err := buildDiscoveryToolCallProbeRequest(protocol, baseURL, probeModel, channel, apiKey)
	if err != nil {
		return DiscoveryCapabilityProbeResult{
			Tested:    false,
			Supported: false,
			Error:     err.Error(),
			Evidence:  "工具调用探测请求构建失败",
		}
	}

	events, statusCode, body, sendErr := sendCompatProbe(ctx, req, channel)
	result := DiscoveryCapabilityProbeResult{Tested: true, StatusCode: statusCode}
	if isCompatProbeTimeout(sendErr, ctx) {
		result.Error = "timeout"
		result.Evidence = "工具调用探测超时，无法确认上游是否支持工具调用"
		return result
	}
	if sendErr != nil || statusCode < 200 || statusCode >= 300 {
		result.Error = discoveryProbeDiagnostic(statusCode, body, sendErr)
		result.Evidence = fmt.Sprintf("上游拒绝工具调用探测请求（HTTP %d）", statusCode)
		return result
	}
	if discoverySSEHasToolCall(events, protocol, discoveryToolProbeName) {
		result.Supported = true
		result.Evidence = "上游返回了 ccx_probe 工具调用"
		result.Recommendation = discoveryToolCallRecommendations(channelKind, channel.ServiceType, targetClients)
		return result
	}
	if hasMeaningfulCompatSSE(events, protocol) {
		result.Evidence = "上游返回了有效内容，但未按强制 tool_choice 产生工具调用"
		return result
	}
	result.Evidence = "工具调用探测响应为空或无法识别"
	return result
}

const discoveryVisionProbeImageBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+ip1sAAAAASUVORK5CYII="

func runDiscoveryVisionProbe(channel *config.UpstreamConfig, channelKind, apiKey, baseURL, probeModel string) DiscoveryCapabilityProbeResult {
	protocol := compatProbeProtocol(channel, channelKind)
	if protocol == "" {
		return DiscoveryCapabilityProbeResult{
			Tested:    false,
			Supported: false,
			Evidence:  "当前渠道类型无图片输入探测项",
		}
	}
	if strings.TrimSpace(probeModel) == "" {
		return DiscoveryCapabilityProbeResult{
			Tested:    false,
			Supported: false,
			Evidence:  "未找到可用于图片输入探测的模型",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	req, err := buildDiscoveryVisionProbeRequest(protocol, baseURL, probeModel, channel, apiKey)
	if err != nil {
		return DiscoveryCapabilityProbeResult{
			Tested:    false,
			Supported: false,
			Error:     err.Error(),
			Evidence:  "图片输入探测请求构建失败",
		}
	}

	events, statusCode, body, sendErr := sendCompatProbe(ctx, req, channel)
	result := DiscoveryCapabilityProbeResult{Tested: true, StatusCode: statusCode}
	if isCompatProbeTimeout(sendErr, ctx) {
		result.Error = "timeout"
		result.Evidence = "图片输入探测超时，无法确认上游是否支持 vision"
		return result
	}
	if sendErr != nil || statusCode < 200 || statusCode >= 300 {
		result.Error = discoveryProbeDiagnostic(statusCode, body, sendErr)
		result.Evidence = fmt.Sprintf("上游拒绝图片输入探测请求（HTTP %d，model=%s）", statusCode, probeModel)
		return result
	}
	if hasMeaningfulCompatSSE(events, protocol) {
		result.Supported = true
		result.Evidence = fmt.Sprintf("上游接受图片输入探测请求（model=%s）", probeModel)
		return result
	}
	result.Evidence = "图片输入探测响应为空或无法识别"
	return result
}

func discoveryProbeDiagnostic(statusCode int, body string, err error) string {
	diagnostic := strings.TrimSpace(body)
	if diagnostic == "" && err != nil {
		diagnostic = err.Error()
	}
	if diagnostic == "" && statusCode > 0 {
		diagnostic = fmt.Sprintf("HTTP %d", statusCode)
	}
	return truncateCapabilityError(diagnostic)
}

func discoveryToolCallRecommendations(channelKind, serviceType string, targetClients []string) map[string]bool {
	if channelKind != "responses" && !hasDiscoveryTargetClient(targetClients, "codex") {
		return nil
	}
	switch strings.TrimSpace(serviceType) {
	case "responses", "copilot":
		return map[string]bool{"codexNativeToolPassthrough": true}
	case "openai", "chat", "claude", "gemini":
		return map[string]bool{"codexToolCompat": true}
	default:
		return nil
	}
}

func hasDiscoveryTargetClient(targetClients []string, target string) bool {
	for _, client := range targetClients {
		if strings.EqualFold(strings.TrimSpace(client), target) {
			return true
		}
	}
	return false
}

func buildDiscoveryToolCallProbeRequest(protocol, baseURL, probeModel string, channel *config.UpstreamConfig, apiKey string) (*http.Request, error) {
	switch protocol {
	case "messages", "claude":
		return buildClaudeCompatRequest(baseURL, buildClaudeToolCallProbeBody(compatProbeModel(capabilityProbeModelClaudeFable5, probeModel)), channel, apiKey)
	case "chat":
		return buildOpenAIChatCompatRequest(baseURL, buildOpenAIChatToolCallProbeBody(probeModel), channel, apiKey)
	case "responses":
		return buildResponsesCompatRequest(baseURL, buildResponsesToolCallProbeBody(probeModel), channel, apiKey)
	case "gemini":
		model := compatProbeModel("gemini-3.5-flash", probeModel)
		return buildGeminiCompatRequest(baseURL, "/v1beta/models/"+model+":streamGenerateContent?alt=sse", buildGeminiToolCallProbeBody(), channel, apiKey)
	default:
		return nil, fmt.Errorf("unsupported tool call probe protocol: %s", protocol)
	}
}

func buildDiscoveryVisionProbeRequest(protocol, baseURL, probeModel string, channel *config.UpstreamConfig, apiKey string) (*http.Request, error) {
	switch protocol {
	case "messages", "claude":
		return buildClaudeCompatRequest(baseURL, buildClaudeVisionProbeBody(compatProbeModel(capabilityProbeModelClaudeFable5, probeModel)), channel, apiKey)
	case "chat":
		return buildOpenAIChatCompatRequest(baseURL, buildOpenAIChatVisionProbeBody(probeModel), channel, apiKey)
	case "responses":
		return buildResponsesCompatRequest(baseURL, buildResponsesVisionProbeBody(probeModel), channel, apiKey)
	case "gemini":
		model := compatProbeModel("gemini-3.5-flash", probeModel)
		return buildGeminiCompatRequest(baseURL, "/v1beta/models/"+model+":streamGenerateContent?alt=sse", buildGeminiVisionProbeBody(), channel, apiKey)
	default:
		return nil, fmt.Errorf("unsupported vision probe protocol: %s", protocol)
	}
}

func buildClaudeToolCallProbeBody(model string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"system": []map[string]interface{}{
			{"type": "text", "text": "You are validating tool-call support. Nonce: " + nonce},
		},
		"messages":   []map[string]string{{"role": "user", "content": "Call ccx_probe with value ok."}},
		"max_tokens": 128,
		"stream":     true,
		"tools": []map[string]interface{}{
			discoveryClaudeToolDefinition(),
		},
		"tool_choice": map[string]string{"type": "tool", "name": discoveryToolProbeName},
	})
	return body
}

func buildClaudeVisionProbeBody(model string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"system": []map[string]interface{}{
			{"type": "text", "text": "You are validating image-input support. Nonce: " + nonce},
		},
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": "Reply with ok if you can inspect this image."},
					{"type": "image", "source": map[string]string{"type": "base64", "media_type": "image/png", "data": discoveryVisionProbeImageBase64}},
				},
			},
		},
		"max_tokens": 32,
		"stream":     true,
	})
	return body
}

func buildOpenAIChatToolCallProbeBody(models ...string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model": compatProbeModel("gpt-5.4-mini", models...),
		"messages": []map[string]string{
			{"role": "system", "content": "You are validating tool-call support. Nonce: " + nonce},
			{"role": "user", "content": "Call ccx_probe with value ok."},
		},
		"max_tokens": 128,
		"stream":     true,
		"tools": []map[string]interface{}{
			discoveryOpenAIFunctionToolDefinition(),
		},
		"tool_choice": map[string]interface{}{"type": "function", "function": map[string]string{"name": discoveryToolProbeName}},
	})
	return body
}

func buildOpenAIChatVisionProbeBody(models ...string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model": compatProbeModel("gpt-5.4-mini", models...),
		"messages": []map[string]interface{}{
			{"role": "system", "content": "You are validating image-input support. Nonce: " + nonce},
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": "Reply with ok if you can inspect this image."},
					{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64," + discoveryVisionProbeImageBase64}},
				},
			},
		},
		"max_tokens": 32,
		"stream":     true,
	})
	return body
}

func buildResponsesToolCallProbeBody(models ...string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model":             compatProbeModel("gpt-5.4-mini", models...),
		"instructions":      "You are validating tool-call support. Nonce: " + nonce,
		"input":             "Call ccx_probe with value ok.",
		"max_output_tokens": 128,
		"stream":            true,
		"tools": []map[string]interface{}{
			discoveryResponsesFunctionToolDefinition(),
		},
		"tool_choice": map[string]string{"type": "function", "name": discoveryToolProbeName},
	})
	return body
}

func buildResponsesVisionProbeBody(models ...string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model":        compatProbeModel("gpt-5.4-mini", models...),
		"instructions": "You are validating image-input support. Nonce: " + nonce,
		"input": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]string{
					{"type": "input_text", "text": "Reply with ok if you can inspect this image."},
					{"type": "input_image", "image_url": "data:image/png;base64," + discoveryVisionProbeImageBase64},
				},
			},
		},
		"max_output_tokens": 32,
		"stream":            true,
	})
	return body
}

func buildGeminiToolCallProbeBody() []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{"role": "user", "parts": []map[string]string{{"text": "Call ccx_probe with value ok. Nonce: " + nonce}}},
		},
		"tools": []map[string]interface{}{
			{
				"functionDeclarations": []map[string]interface{}{
					{
						"name":        discoveryToolProbeName,
						"description": "Returns a probe value.",
						"parameters": map[string]interface{}{
							"type": "OBJECT",
							"properties": map[string]interface{}{
								"value": map[string]string{"type": "STRING"},
							},
							"required": []string{"value"},
						},
					},
				},
			},
		},
		"toolConfig": map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{
				"mode":                 "ANY",
				"allowedFunctionNames": []string{discoveryToolProbeName},
			},
		},
		"generationConfig": map[string]int{"maxOutputTokens": 128},
	})
	return body
}

func buildGeminiVisionProbeBody() []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]interface{}{
					{"text": "Reply with ok if you can inspect this image. Nonce: " + nonce},
					{"inlineData": map[string]string{"mimeType": "image/png", "data": discoveryVisionProbeImageBase64}},
				},
			},
		},
		"generationConfig": map[string]int{"maxOutputTokens": 32},
	})
	return body
}

func discoveryClaudeToolDefinition() map[string]interface{} {
	return map[string]interface{}{
		"name":        discoveryToolProbeName,
		"description": "Returns a probe value.",
		"input_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"value": map[string]string{"type": "string"},
			},
			"required": []string{"value"},
		},
	}
}

func discoveryOpenAIFunctionToolDefinition() map[string]interface{} {
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        discoveryToolProbeName,
			"description": "Returns a probe value.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"value": map[string]string{"type": "string"},
				},
				"required": []string{"value"},
			},
		},
	}
}

func discoveryResponsesFunctionToolDefinition() map[string]interface{} {
	return map[string]interface{}{
		"type":        "function",
		"name":        discoveryToolProbeName,
		"description": "Returns a probe value.",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"value": map[string]string{"type": "string"},
			},
			"required": []string{"value"},
		},
	}
}

func discoverySSEHasToolCall(lines []string, protocol, toolName string) bool {
	for _, line := range lines {
		if line == "" || line == "[DONE]" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch protocol {
		case "messages", "claude":
			if discoveryClaudeEventHasToolCall(ev, toolName) {
				return true
			}
		case "chat":
			if discoveryOpenAIChatEventHasToolCall(ev, toolName) {
				return true
			}
		case "responses":
			if discoveryResponsesEventHasToolCall(ev, toolName) {
				return true
			}
		case "gemini":
			if discoveryGeminiEventHasToolCall(ev, toolName) {
				return true
			}
		}
	}
	return false
}

func discoveryClaudeEventHasToolCall(ev map[string]interface{}, toolName string) bool {
	if stringField(ev, "type") != "content_block_start" {
		return false
	}
	block, ok := ev["content_block"].(map[string]interface{})
	if !ok {
		return false
	}
	blockType := stringField(block, "type")
	return (blockType == "tool_use" || blockType == "server_tool_use") && stringField(block, "name") == toolName
}

func discoveryOpenAIChatEventHasToolCall(ev map[string]interface{}, toolName string) bool {
	choices, ok := ev["choices"].([]interface{})
	if !ok {
		return false
	}
	for _, choiceValue := range choices {
		choice, ok := choiceValue.(map[string]interface{})
		if !ok {
			continue
		}
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		if discoveryToolCallsContainName(delta["tool_calls"], toolName) {
			return true
		}
		if functionCall, ok := delta["function_call"].(map[string]interface{}); ok && stringField(functionCall, "name") == toolName {
			return true
		}
	}
	return false
}

func discoveryResponsesEventHasToolCall(ev map[string]interface{}, toolName string) bool {
	if item, ok := ev["item"].(map[string]interface{}); ok && discoveryResponsesItemIsToolCall(item, toolName) {
		return true
	}
	response, ok := ev["response"].(map[string]interface{})
	if !ok {
		return false
	}
	output, ok := response["output"].([]interface{})
	if !ok {
		return false
	}
	for _, itemValue := range output {
		item, ok := itemValue.(map[string]interface{})
		if ok && discoveryResponsesItemIsToolCall(item, toolName) {
			return true
		}
	}
	return false
}

func discoveryResponsesItemIsToolCall(item map[string]interface{}, toolName string) bool {
	itemType := stringField(item, "type")
	if itemType != "function_call" && itemType != "custom_tool_call" && !strings.HasSuffix(itemType, "_call") {
		return false
	}
	return stringField(item, "name") == toolName
}

func discoveryGeminiEventHasToolCall(ev map[string]interface{}, toolName string) bool {
	candidates, ok := ev["candidates"].([]interface{})
	if !ok {
		return false
	}
	for _, candidateValue := range candidates {
		candidate, ok := candidateValue.(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := candidate["content"].(map[string]interface{})
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]interface{})
		if !ok {
			continue
		}
		for _, partValue := range parts {
			part, ok := partValue.(map[string]interface{})
			if !ok {
				continue
			}
			functionCall, ok := part["functionCall"].(map[string]interface{})
			if ok && stringField(functionCall, "name") == toolName {
				return true
			}
		}
	}
	return false
}

func discoveryToolCallsContainName(raw interface{}, toolName string) bool {
	toolCalls, ok := raw.([]interface{})
	if !ok {
		return false
	}
	for _, callValue := range toolCalls {
		call, ok := callValue.(map[string]interface{})
		if !ok {
			continue
		}
		if function, ok := call["function"].(map[string]interface{}); ok && stringField(function, "name") == toolName {
			return true
		}
		if stringField(call, "name") == toolName {
			return true
		}
	}
	return false
}

func discoverTransientModelsWithFetchers(ctx context.Context, channel *config.UpstreamConfig, channelKind string, apiKey string, fetchers ChannelDiscoveryModelFetchers) DiscoveryModelsResult {
	if channelKind == "" && strings.TrimSpace(channel.ServiceType) == "" {
		return discoverAutoTransientModelsWithFetchers(ctx, channel, apiKey, fetchers)
	}
	return discoverTransientModelsForCandidate(ctx, channel, channelKind, apiKey, fetchers)
}

func discoverAutoTransientModelsWithFetchers(ctx context.Context, channel *config.UpstreamConfig, apiKey string, fetchers ChannelDiscoveryModelFetchers) DiscoveryModelsResult {
	type candidate struct {
		channelKind string
		serviceType string
	}
	candidates := []candidate{
		{channelKind: "messages", serviceType: "claude"},
		{channelKind: "gemini", serviceType: "gemini"},
	}

	items := make([]string, 0)
	sources := make([]string, 0, len(candidates))
	sourceSeen := make(map[string]bool, len(candidates))
	failureWarnings := make([]string, 0)
	result := DiscoveryModelsResult{Source: "auto_models"}
	for _, candidate := range candidates {
		probeChannel := *channel
		probeChannel.ServiceType = candidate.serviceType
		candidateResult := discoverTransientModelsForCandidate(ctx, &probeChannel, candidate.channelKind, apiKey, fetchers)
		if len(candidateResult.Items) == 0 {
			for _, warning := range candidateResult.Warnings {
				failureWarnings = append(failureWarnings, candidate.channelKind+": "+warning)
			}
			continue
		}

		items = append(items, candidateResult.Items...)
		if candidateResult.Source != "" && !sourceSeen[candidateResult.Source] {
			sourceSeen[candidateResult.Source] = true
			sources = append(sources, candidateResult.Source)
		}
		if result.URL == "" {
			result.URL = candidateResult.URL
			result.StatusCode = candidateResult.StatusCode
		}
		result.Warnings = append(result.Warnings, candidateResult.Warnings...)
	}

	// 保留 API 原始顺序：平台通常把主推/免费模型排在目录前面，探测候选的
	// 补位窗口依赖该顺序；ASCII 排序会让大写 org 的 embedding 模型霸占头部。
	result.Items = uniqueDiscoveryModelsPreservingOrder(items)
	if len(result.Items) == 0 {
		result.Warnings = failureWarnings
		return result
	}
	result.Source = strings.Join(sources, "+")
	if result.Source == "" {
		result.Source = "auto_models"
	}
	return result
}

func discoverTransientModelsForCandidate(ctx context.Context, channel *config.UpstreamConfig, channelKind string, apiKey string, fetchers ChannelDiscoveryModelFetchers) DiscoveryModelsResult {
	fetcherKey, fetcher := selectDiscoveryModelsFetcher(channelKind, channel.ServiceType, fetchers)
	if fetcher == nil {
		return discoverTransientModels(ctx, channel, channelKind, apiKey)
	}

	resp, err := fetcher(ctx, DiscoveryModelsFetchRequest{
		ChannelKind:              channelKind,
		ServiceType:              channel.ServiceType,
		BaseURL:                  channel.BaseURL,
		BaseURLs:                 append([]string(nil), channel.BaseURLs...),
		APIKey:                   apiKey,
		AuthHeader:               channel.AuthHeader,
		CustomHeaders:            cloneStringMap(channel.CustomHeaders),
		ProxyURL:                 channel.ProxyURL,
		ProxyPreferDirect:        channel.ProxyPreferDirect,
		InsecureSkipVerify:       channel.InsecureSkipVerify,
		LearnedClientFingerprint: channel.LearnedClientFingerprint,
	})

	result := DiscoveryModelsResult{
		Source:     fetcherKey + "_models_handler",
		URL:        resp.URL,
		StatusCode: resp.StatusCode,
	}
	if err != nil {
		result.Warnings = []string{err.Error()}
		return result
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Warnings = []string{fmt.Sprintf("%s models handler returned HTTP %d", fetcherKey, resp.StatusCode)}
		return result
	}
	result.Items = parseDiscoveryModels(resp.Body)
	if len(result.Items) == 0 {
		result.Warnings = []string{fetcherKey + " models handler returned no parseable models"}
	}
	return result
}

func selectDiscoveryModelsFetcher(channelKind, serviceType string, fetchers ChannelDiscoveryModelFetchers) (string, ChannelDiscoveryModelFetcher) {
	if len(fetchers) == 0 {
		return "", nil
	}

	candidates := []string{normalizeDiscoveryChannelKind(channelKind)}
	if protocol, ok := normalizeServiceTypeToProtocol(serviceType); ok {
		candidates = append(candidates, string(protocol))
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if fetcher, ok := fetchers[candidate]; ok {
			return candidate, fetcher
		}
	}
	return "", nil
}

func discoverTransientModels(ctx context.Context, channel *config.UpstreamConfig, channelKind string, apiKey string) DiscoveryModelsResult {
	baseURL := capabilityTestBaseURL(channel)
	if baseURL == "" {
		return DiscoveryModelsResult{Source: "none", Warnings: []string{"base URL is empty"}}
	}

	modelsURL := discoveryModelsURL(baseURL, channelKind, channel.ServiceType)
	manifestServiceType := strings.ToLower(strings.TrimSpace(channel.ServiceType))
	if manifestServiceType == "claude" {
		manifestServiceType = "messages"
	}
	if manifestURL, ok := config.ResolveBuiltinModelsURL(baseURL, manifestServiceType); ok {
		modelsURL = manifestURL
	}
	client := httpclient.GetManager().GetClient(httpclient.ClientOptions{
		Timeout:           10 * time.Second,
		Insecure:          channel.InsecureSkipVerify,
		ProxyURL:          channel.ProxyURL,
		ProxyPreferDirect: channel.ProxyPreferDirect,
	})
	// Anthropic 风格端点（messages/claude）首发即带 Claude Code 探针头，与 capability test
	// 探测一致；其余风格裸发，命中客户端指纹风控时由 FetchUpstreamModels 带探针头重试。
	useProbeHeaders := manifestServiceType == "messages" || channelKind == "messages" || channel.LearnedClientFingerprint
	statusCode, body, _, err := utils.FetchUpstreamModels(ctx, client, modelsURL,
		func(h http.Header) { utils.SetAuthenticationHeaderWithOverride(h, apiKey, channel.AuthHeader) },
		channel.CustomHeaders, useProbeHeaders)
	if err != nil {
		return DiscoveryModelsResult{Source: "models_endpoint", URL: modelsURL, Warnings: []string{err.Error()}}
	}

	result := DiscoveryModelsResult{
		Source:     "models_endpoint",
		URL:        modelsURL,
		StatusCode: statusCode,
	}
	if statusCode < 200 || statusCode >= 300 {
		result.Warnings = []string{fmt.Sprintf("models endpoint returned HTTP %d", statusCode)}
		return result
	}
	result.Items = parseDiscoveryModels(body)
	if len(result.Items) == 0 {
		result.Warnings = []string{"models endpoint returned no parseable models"}
	}
	return result
}

func discoveryModelsURL(baseURL, channelKind, serviceType string) string {
	if channelKind == "gemini" || serviceType == "gemini" {
		return buildCapabilityTestURL(baseURL, "/v1beta", "/models")
	}
	if serviceType == "copilot" {
		return strings.TrimRight(strings.TrimSuffix(baseURL, "#"), "/") + "/models"
	}
	return buildCapabilityTestURL(baseURL, "/v1", "/models")
}

func parseDiscoveryModels(body []byte) []string {
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	models := make([]string, 0)
	appendModel := func(value interface{}) {
		switch typed := value.(type) {
		case string:
			model := strings.TrimPrefix(strings.TrimSpace(typed), "models/")
			if model != "" {
				models = append(models, model)
			}
		case map[string]interface{}:
			for _, key := range []string{"id", "name", "model"} {
				if model, ok := typed[key].(string); ok {
					model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
					if model != "" {
						models = append(models, model)
						return
					}
				}
			}
		}
	}
	if data, ok := raw["data"].([]interface{}); ok {
		for _, item := range data {
			appendModel(item)
		}
	}
	if data, ok := raw["models"].([]interface{}); ok {
		for _, item := range data {
			appendModel(item)
		}
	}
	return uniqueDiscoveryModelsPreservingOrder(models)
}

func fallbackDiscoveryProbeModels(channelKind, serviceType string) []string {
	if channelKind != "" {
		if models, err := getCapabilityProbeModels(channelKind); err == nil {
			return models
		}
	}
	if protocol, ok := normalizeServiceTypeToProtocol(serviceType); ok {
		if models, err := getCapabilityProbeModels(string(protocol)); err == nil {
			return models
		}
	}
	return []string{"gpt-5.4", "claude-sonnet-4-6", "gemini-3.5-flash"}
}

// nonChatDiscoveryModelMarkers 命名特征命中即视为非 chat 模型：它们挂在
// embeddings/images 等专用端点上，对 messages/chat/responses/gemini 四协议
// 的探测只可能失败，却会挤占候选补位窗口（bitdeer 事件：BAAI/bge-* 两个
// embedding 模型按 ASCII 序霸占头部，把免费 chat 模型挤出候选导致 422）。
const nonChatDiscoveryModelMarkers = "\x00bge\x00embed\x00reranker\x00flux\x00imagen\x00whisper\x00tts\x00"

// isNonChatDiscoveryModel 判断模型是否明显不属于 chat/completions 类推理模型。
// 保守起见只匹配词边界内的高置信命名特征，误杀的代价只是少一个候选。
func isNonChatDiscoveryModel(model string) bool {
	lower := strings.ToLower(model)
	for _, marker := range strings.Split(nonChatDiscoveryModelMarkers, "\x00") {
		if marker == "" {
			continue
		}
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func discoveryProbeModels(selected DiscoverySelectedModels, all []string) []string {
	candidates := []string{selected.Strong, selected.Primary, selected.Fast}
	for _, model := range all {
		if isNonChatDiscoveryModel(model) {
			continue
		}
		candidates = append(candidates, model)
		if len(candidates) >= 6 {
			break
		}
	}
	probes := uniqueDiscoveryModels(candidates)
	chatOnly := make([]string, 0, len(probes))
	for _, model := range probes {
		if !isNonChatDiscoveryModel(model) {
			chatOnly = append(chatOnly, model)
		}
	}
	if len(chatOnly) > 0 {
		return chatOnly
	}
	return probes
}

func discoveryProtocolProbeModels(models DiscoveryModelsResult, probeAll bool) []string {
	if probeAll && len(models.Items) > 0 {
		return uniqueDiscoveryModels(models.Items)
	}
	return discoveryProbeModels(models.Selected, models.Items)
}

func runDiscoveryProtocolProbes(ctx context.Context, channel *config.UpstreamConfig, models []string, cfgManager *config.ConfigManager, pacer *discoveryProbePacer) []DiscoveryProtocolResult {
	protocols := []string{"messages", "responses", "chat", "gemini"}
	results := make([]DiscoveryProtocolResult, 0, len(protocols))
	for _, protocol := range protocols {
		results = append(results, runDiscoveryProtocolProbe(ctx, channel, protocol, models, discoveryProbeTimeout, cfgManager, pacer))
	}
	return results
}

func runDiscoveryProtocolProbe(ctx context.Context, channel *config.UpstreamConfig, protocol string, models []string, timeout time.Duration, cfgManager *config.ConfigManager, pacer *discoveryProbePacer) DiscoveryProtocolResult {
	result := DiscoveryProtocolResult{Protocol: protocol}
	var successLatency int64
	var successCount int
	availableModels := make(map[string]struct{}, len(models))
	for _, model := range models {
		availableModels[strings.TrimSpace(model)] = struct{}{}
	}
	probeResults := make(map[string]ModelTestResult, len(models))
	probeChannel := channel
	if strings.TrimSpace(channel.ServiceType) == "" {
		cloned := *channel
		cloned.ServiceType = resolveDiscoveryServiceType("", protocol)
		probeChannel = &cloned
	}
	for _, model := range models {
		probeModel := discoveryEquivalentProbeModel(model, availableModels)
		modelResult, cached := probeResults[probeModel]
		if !cached {
			for attempt := 0; ; attempt++ {
				if err := pacer.waitForNext(ctx); err != nil {
					result.Error = err.Error()
					return result
				}
				modelResult = executeModelTest(ctx, probeChannel, protocol, probeModel, timeout, "", cfgManager, -1, protocol, probeChannel.APIKeys[0], nil, false)
				if !isDiscoveryRateLimited(modelResult) {
					break
				}
				pacer.observeRateLimited()
				if attempt >= maxDiscovery429Retries {
					break
				}
			}
			probeResults[probeModel] = modelResult
		}
		if modelResult.Success {
			result.Success = true
			result.SuccessModels = append(result.SuccessModels, model)
			successLatency += modelResult.Latency
			successCount++
		} else {
			result.FailedModels = append(result.FailedModels, model)
			if modelResult.Error != nil && result.Error == "" {
				result.Error = *modelResult.Error
			}
		}
	}
	// LatencyMs 仅统计成功模型的均值，避免超时探测拉低有效延迟读数。
	if successCount > 0 {
		result.LatencyMs = successLatency / int64(successCount)
	}
	return result
}

func discoveryEquivalentProbeModel(model string, availableModels map[string]struct{}) string {
	trimmed := strings.TrimSpace(model)
	const thinkingSuffix = "-thinking"
	if !strings.HasSuffix(strings.ToLower(trimmed), thinkingSuffix) {
		return trimmed
	}
	baseModel := trimmed[:len(trimmed)-len(thinkingSuffix)]
	if _, exists := availableModels[baseModel]; exists {
		return baseModel
	}
	return trimmed
}

func isDiscoveryRateLimited(result ModelTestResult) bool {
	if result.statusCode != http.StatusTooManyRequests {
		return false
	}
	if result.Error != nil {
		blacklist := common.ShouldBlacklistKey(http.StatusTooManyRequests, []byte(*result.Error))
		if blacklist.ShouldBlacklist && common.IsBalanceOrQuotaBlacklistReason(blacklist.Reason) {
			return false
		}
	}
	return true
}

func discoverySuccessModelsByProtocol(protocols []DiscoveryProtocolResult) map[string][]string {
	result := make(map[string][]string, len(protocols))
	for _, protocol := range protocols {
		result[protocol.Protocol] = append([]string(nil), protocol.SuccessModels...)
	}
	return result
}

// discoveryEffectiveSuccessModels 返回 channelKind 协议实际探测成功的模型列表。
// 当该协议无成功模型时（用户显式选择 channelKind 但协议失败），降级策略：
//  1. 优先尝试 preferredProtocol（通常来自 compatProbeProtocol，反映 serviceType 实际目标协议）
//  2. 再按 responses > messages > chat > gemini 固定顺序兜底
//
// 保持与 buildDiscoveryMappingRecommendation 的 fallback 行为一致。
func discoveryEffectiveSuccessModels(channelKind, preferredProtocol string, successByProtocol map[string][]string) []string {
	if models := successByProtocol[channelKind]; len(models) > 0 {
		return models
	}
	// 优先试 serviceType 对应的实际上游协议，避免把模型映射到不可用的端点。
	if preferredProtocol != "" && preferredProtocol != channelKind {
		if models := successByProtocol[preferredProtocol]; len(models) > 0 {
			return models
		}
	}
	for _, protocol := range []string{"responses", "messages", "chat", "gemini"} {
		if protocol == channelKind || protocol == preferredProtocol {
			continue // 已试过，跳过
		}
		if models := successByProtocol[protocol]; len(models) > 0 {
			return models
		}
	}
	return nil
}

// discoveryCompatProbeModel 从有效成功模型列表中选出最适合兼容性探测的模型。
// 接受已经过 fallback 处理的 effectiveModels 列表（由 discoveryEffectiveSuccessModels 计算）。
func discoveryCompatProbeModel(channelKind string, selected DiscoverySelectedModels, effectiveModels []string) string {
	successful := make(map[string]struct{})
	for _, model := range effectiveModels {
		if strings.TrimSpace(model) != "" {
			successful[model] = struct{}{}
		}
	}
	candidates := []string{selected.Primary, selected.Fast, selected.Strong}
	for _, model := range candidates {
		if _, ok := successful[model]; ok {
			return model
		}
	}
	if len(effectiveModels) > 0 {
		return effectiveModels[0]
	}
	for _, model := range candidates {
		if strings.TrimSpace(model) != "" {
			return model
		}
	}
	return ""
}

func recommendDiscoveryChannelKind(requested string, targetClients []string, protocols []DiscoveryProtocolResult) string {
	if normalized := normalizeDiscoveryChannelKind(requested); normalized != "" {
		return normalized
	}

	success := make(map[string]bool, len(protocols))
	for _, protocol := range protocols {
		success[protocol.Protocol] = protocol.Success
	}
	targetSet := make(map[string]bool, len(targetClients))
	for _, target := range targetClients {
		targetSet[strings.ToLower(strings.TrimSpace(target))] = true
	}
	if targetSet["codex"] {
		if success["responses"] {
			return "responses"
		}
		if success["chat"] {
			return "chat"
		}
	}
	if targetSet["claude-code"] || targetSet["claude"] {
		if success["messages"] {
			return "messages"
		}
	}
	for _, protocol := range []string{"responses", "messages", "chat", "gemini"} {
		if success[protocol] {
			return protocol
		}
	}
	return ""
}

func normalizeDiscoveryChannelKind(channelKind string) string {
	switch strings.TrimSpace(channelKind) {
	case "messages", "responses", "chat", "gemini":
		return strings.TrimSpace(channelKind)
	default:
		return ""
	}
}

func resolveDiscoveryServiceType(requested, detectedProtocol string) string {
	if serviceType := strings.TrimSpace(requested); serviceType != "" {
		return serviceType
	}
	switch normalizeDiscoveryChannelKind(detectedProtocol) {
	case "messages":
		return "claude"
	case "responses":
		return "responses"
	case "chat":
		return "openai"
	case "gemini":
		return "gemini"
	default:
		return ""
	}
}

func buildDiscoveryEvidence(models DiscoveryModelsResult, protocols []DiscoveryProtocolResult, recommendation DiscoveryRecommendation) []DiscoveryEvidence {
	evidence := make([]DiscoveryEvidence, 0, len(protocols)+len(recommendation.Evidence)+1)
	if len(models.Items) > 0 {
		evidence = append(evidence, DiscoveryEvidence{Type: "models", Message: fmt.Sprintf("%s returned %d models", models.Source, len(models.Items))})
	}
	for _, warning := range models.Warnings {
		evidence = append(evidence, DiscoveryEvidence{Type: "models", Message: warning})
	}
	for _, protocol := range protocols {
		if protocol.Success {
			evidence = append(evidence, DiscoveryEvidence{Type: "protocol", Key: protocol.Protocol, Message: fmt.Sprintf("%d models passed", len(protocol.SuccessModels))})
		}
	}
	evidence = append(evidence, recommendation.Evidence...)
	return evidence
}
