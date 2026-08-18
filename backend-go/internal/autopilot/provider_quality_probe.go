package autopilot

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

const (
	defaultProviderQualityDailyBudget = 12
	defaultProviderQualityMaxRepeats  = 3
	defaultProviderQualityTimeout     = 90 * time.Second
	defaultProviderQualityMaxBody     = int64(64 * 1024)
	providerQualityMaxOutputTokens    = 2048
	providerQualitySourceProbe        = "probe"
	providerQualityCanaryVersion      = "pq-v1-20260715"
)

const providerQualityCanaryPrompt = `Return exactly one JSON object and no markdown or explanation. Solve all fields independently. Required keys and types: answer (integer), sequence (integer), checksum (string). Tasks: answer=17*19; sequence=the next Fibonacci number after [2,3,5,8]; checksum=concatenate the first letters of Alpha, Bravo, Charlie.`

// ProviderQualityProbeConfig 控制手动 L3 探测的硬预算和资源边界。
// 该探测器不含后台 worker，只有管理 API 显式调用时才会请求上游。
type ProviderQualityProbeConfig struct {
	DailyBudget     int
	MaxRepetitions  int
	RequestTimeout  time.Duration
	MaxResponseBody int64
	TimeFunc        func() time.Time
}

func (c ProviderQualityProbeConfig) withDefaults() ProviderQualityProbeConfig {
	if c.DailyBudget <= 0 {
		c.DailyBudget = defaultProviderQualityDailyBudget
	}
	if c.MaxRepetitions <= 0 {
		c.MaxRepetitions = defaultProviderQualityMaxRepeats
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultProviderQualityTimeout
	}
	if c.MaxResponseBody <= 0 {
		c.MaxResponseBody = defaultProviderQualityMaxBody
	}
	if c.TimeFunc == nil {
		c.TimeFunc = time.Now
	}
	return c
}

// ProviderQualityProbeRequest 是固定 canary 的唯一可调输入。
// prompt 不对外开放，避免该管理端点变成任意额度消耗入口。
type ProviderQualityProbeRequest struct {
	EndpointUID string `json:"endpointUid"`
	ModelID     string `json:"modelId"`
	Repetitions int    `json:"repetitions,omitempty"`
}

// ProviderQualityDimensions 是单次固定 canary 的四维评分。
type ProviderQualityDimensions struct {
	Completeness float64 `json:"completeness"`
	Semantic     float64 `json:"semantic"`
	Format       float64 `json:"format"`
	Latency      float64 `json:"latency"`
}

// ProviderQualityEvidence 只保存结构化判定，不保存模型原始输出。
type ProviderQualityEvidence struct {
	ContentPresent bool `json:"contentPresent"`
	StrictJSON     bool `json:"strictJson"`
	RequiredFields int  `json:"requiredFields"`
	CorrectFields  int  `json:"correctFields"`
}

// ProviderQualitySampleResult 描述一次真实上游调用的脱敏结果。
type ProviderQualitySampleResult struct {
	Index      int                       `json:"index"`
	StatusCode int                       `json:"statusCode,omitempty"`
	LatencyMs  int64                     `json:"latencyMs"`
	Score      float64                   `json:"score"`
	Dimensions ProviderQualityDimensions `json:"dimensions"`
	Evidence   ProviderQualityEvidence   `json:"evidence"`
	ErrorCode  string                    `json:"errorCode,omitempty"`
}

// ProviderQualityBudgetState 返回当前进程内的 L3 每日预算状态。
type ProviderQualityBudgetState struct {
	DailyLimit int `json:"dailyLimit"`
	Used       int `json:"used"`
	Remaining  int `json:"remaining"`
}

// ProviderQualityProbeResult 是管理 API 的脱敏响应与画像写入摘要。
type ProviderQualityProbeResult struct {
	EndpointUID   string                        `json:"endpointUid"`
	ChannelUID    string                        `json:"channelUid"`
	ChannelKind   string                        `json:"channelKind"`
	ModelID       string                        `json:"modelId"`
	CanaryVersion string                        `json:"canaryVersion"`
	SampleCount   int                           `json:"sampleCount"`
	SuccessCount  int                           `json:"successCount"`
	Score         float64                       `json:"score"`
	Confidence    float64                       `json:"confidence"`
	Source        string                        `json:"source"`
	Persisted     bool                          `json:"persisted"`
	PersistNote   string                        `json:"persistNote,omitempty"`
	Samples       []ProviderQualitySampleResult `json:"samples"`
	Budget        ProviderQualityBudgetState    `json:"budget"`
}

// ProviderQualityProbeError 表示无需请求上游即可确定的输入或状态错误。
type ProviderQualityProbeError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *ProviderQualityProbeError) Error() string { return e.Message }

// ProviderQualityProbe 执行 endpoint×model 维度的手动 L3 固定 canary。
type ProviderQualityProbe struct {
	profiles      *ProfileStore
	modelProfiles *ModelProfileStore
	cfgManager    *config.ConfigManager
	resolveAPIKey APIKeyResolver
	config        ProviderQualityProbeConfig
	budget        *ProbeBudget

	inflightMu sync.Mutex
	inflight   map[string]struct{}
}

// NewProviderQualityProbe 创建手动 L3 探测器；不会启动后台任务。
func NewProviderQualityProbe(
	profiles *ProfileStore,
	modelProfiles *ModelProfileStore,
	cfgManager *config.ConfigManager,
	resolveAPIKey APIKeyResolver,
	cfg ProviderQualityProbeConfig,
) *ProviderQualityProbe {
	cfg = cfg.withDefaults()
	return &ProviderQualityProbe{
		profiles:      profiles,
		modelProfiles: modelProfiles,
		cfgManager:    cfgManager,
		resolveAPIKey: resolveAPIKey,
		config:        cfg,
		budget:        NewProbeBudgetWithTime(cfg.DailyBudget, cfg.TimeFunc),
		inflight:      make(map[string]struct{}),
	}
}

// BudgetState 返回当前进程内预算快照。
func (p *ProviderQualityProbe) BudgetState() ProviderQualityBudgetState {
	if p == nil || p.budget == nil {
		return ProviderQualityBudgetState{}
	}
	return ProviderQualityBudgetState{
		DailyLimit: p.budget.Limit(),
		Used:       p.budget.Used(),
		Remaining:  p.budget.Remaining(),
	}
}

// Probe 对一个 endpoint×model 执行 1~3 次固定 canary，并在形成有效证据后回写 ModelProfile。
func (p *ProviderQualityProbe) Probe(ctx context.Context, input ProviderQualityProbeRequest) (*ProviderQualityProbeResult, error) {
	if p == nil || p.profiles == nil || p.modelProfiles == nil || p.cfgManager == nil || p.resolveAPIKey == nil {
		return nil, newProviderQualityProbeError(http.StatusServiceUnavailable, "probe_unavailable", "ProviderQuality 探测器未完成初始化")
	}

	input.EndpointUID = strings.TrimSpace(input.EndpointUID)
	input.ModelID = strings.TrimSpace(input.ModelID)
	if input.EndpointUID == "" || input.ModelID == "" {
		return nil, newProviderQualityProbeError(http.StatusBadRequest, "invalid_request", "endpointUid 和 modelId 不能为空")
	}
	if len(input.EndpointUID) > 128 || len(input.ModelID) > 256 || strings.ContainsAny(input.EndpointUID+input.ModelID, "\r\n") {
		return nil, newProviderQualityProbeError(http.StatusBadRequest, "invalid_request", "endpointUid 或 modelId 格式无效")
	}
	if input.Repetitions == 0 {
		input.Repetitions = 1
	}
	if input.Repetitions < 1 || input.Repetitions > p.config.MaxRepetitions {
		return nil, newProviderQualityProbeError(http.StatusBadRequest, "invalid_repetitions", fmt.Sprintf("repetitions 必须在 1-%d 之间", p.config.MaxRepetitions))
	}

	profile := p.profiles.Get(input.EndpointUID)
	if profile == nil {
		return nil, newProviderQualityProbeError(http.StatusNotFound, "endpoint_not_found", "endpoint 画像不存在")
	}
	keyHash := profile.KeyHash
	if keyHash == "" {
		keyHash = profile.MetricsKey
	}
	apiKey, ok := p.resolveAPIKey(profile.ChannelUID, keyHash)
	if !ok {
		return nil, newProviderQualityProbeError(http.StatusConflict, "api_key_unavailable", "endpoint 对应的 API Key 已轮换或不可用")
	}
	upstream := findProviderQualityUpstream(p.cfgManager, profile.ChannelUID, profile.ChannelKind)
	if upstream == nil {
		return nil, newProviderQualityProbeError(http.StatusNotFound, "channel_not_found", "endpoint 对应渠道不存在")
	}
	if profile.ServiceType == "" {
		profile.ServiceType = upstream.ServiceType
	}
	if profile.BaseURL == "" {
		profile.BaseURL = upstream.GetEffectiveBaseURL()
	}
	if !supportsProviderQualityProbe(profile.ServiceType) {
		return nil, newProviderQualityProbeError(http.StatusBadRequest, "unsupported_service_type", "该 endpoint 协议不支持文本质量探测")
	}

	inflightKey := profile.EndpointUID + "|" + strings.ToLower(input.ModelID)
	if !p.tryStart(inflightKey) {
		return nil, newProviderQualityProbeError(http.StatusConflict, "probe_in_progress", "同一 endpoint 和模型正在探测")
	}
	defer p.finish(inflightKey)

	if !p.budget.TryConsumeN(input.Repetitions) {
		return nil, newProviderQualityProbeError(http.StatusTooManyRequests, "probe_budget_exhausted", "今日 ProviderQuality 探测预算不足")
	}

	result := &ProviderQualityProbeResult{
		EndpointUID:   profile.EndpointUID,
		ChannelUID:    profile.ChannelUID,
		ChannelKind:   profile.ChannelKind,
		ModelID:       input.ModelID,
		CanaryVersion: providerQualityCanaryVersion,
		SampleCount:   input.Repetitions,
		Source:        providerQualitySourceProbe,
		Samples:       make([]ProviderQualitySampleResult, 0, input.Repetitions),
	}

	for i := 1; i <= input.Repetitions; i++ {
		sample := p.runSample(ctx, profile, upstream, apiKey, input.ModelID, i)
		result.Samples = append(result.Samples, sample)
		if sample.ErrorCode == "" && sample.Evidence.ContentPresent {
			result.SuccessCount++
		}
	}
	result.Score, result.Confidence = aggregateProviderQualitySamples(result.Samples, result.SuccessCount)
	result.Budget = p.BudgetState()

	if result.SuccessCount == 0 {
		result.PersistNote = "no_valid_sample"
		return result, nil
	}
	if err := p.persistResult(profile, apiKey, result); err != nil {
		return nil, newProviderQualityProbeError(http.StatusInternalServerError, "persist_failed", "ProviderQuality 画像写入失败")
	}
	return result, nil
}

func newProviderQualityProbeError(status int, code, message string) *ProviderQualityProbeError {
	return &ProviderQualityProbeError{HTTPStatus: status, Code: code, Message: message}
}

func (p *ProviderQualityProbe) tryStart(key string) bool {
	p.inflightMu.Lock()
	defer p.inflightMu.Unlock()
	if _, exists := p.inflight[key]; exists {
		return false
	}
	p.inflight[key] = struct{}{}
	return true
}

func (p *ProviderQualityProbe) finish(key string) {
	p.inflightMu.Lock()
	delete(p.inflight, key)
	p.inflightMu.Unlock()
}

func supportsProviderQualityProbe(serviceType string) bool {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "messages", "openai", "openai-chat", "chat", "responses", "codex", "gemini":
		return true
	default:
		return false
	}
}

func findProviderQualityUpstream(cfgManager *config.ConfigManager, channelUID, channelKind string) *config.UpstreamConfig {
	if cfgManager == nil || channelUID == "" {
		return nil
	}
	cfg := cfgManager.GetConfig()
	type upstreamList struct {
		kind     string
		channels []config.UpstreamConfig
	}
	lists := []upstreamList{
		{kind: "messages", channels: cfg.Upstream},
		{kind: "responses", channels: cfg.ResponsesUpstream},
		{kind: "gemini", channels: cfg.GeminiUpstream},
		{kind: "chat", channels: cfg.ChatUpstream},
	}
	for _, list := range lists {
		if channelKind != "" && list.kind != channelKind {
			continue
		}
		for i := range list.channels {
			if list.channels[i].ChannelUID == channelUID {
				upstream := list.channels[i]
				return &upstream
			}
		}
	}
	return nil
}

func (p *ProviderQualityProbe) persistResult(
	endpoint *KeyEndpointProfile,
	apiKey string,
	result *ProviderQualityProbeResult,
) error {
	metricsKey := computeMetricsIdentityKey(endpoint.BaseURL, apiKey, endpoint.ServiceType)
	modelProfile := p.modelProfiles.Get(endpoint.ChannelUID, endpoint.ChannelKind, metricsKey, result.ModelID)
	if modelProfile == nil {
		modelProfile = p.newProviderQualityModelProfile(endpoint, metricsKey, result.ModelID)
	}
	modelProfile.ServiceType = endpoint.ServiceType
	if strings.EqualFold(modelProfile.ProviderQualitySource, "user_feedback") {
		result.PersistNote = "user_feedback_override"
		return nil
	}

	now := p.config.TimeFunc()
	modelProfile.ProviderQualityScore = result.Score
	modelProfile.ProviderQualitySource = providerQualitySourceProbe
	modelProfile.ProviderQualityConfidence = result.Confidence
	modelProfile.ProviderQualityProbeVersion = providerQualityCanaryVersion
	modelProfile.LastProbeAt = now
	modelProfile.ProbeSuccess = true
	modelProfile.ProbeLatencyMs = averageSuccessfulProviderQualityLatency(result.Samples)
	modelProfile.ProbeConfidence = result.Confidence
	modelProfile.UpdatedAt = now

	if err := p.modelProfiles.Upsert(modelProfile); err != nil {
		return err
	}
	if err := p.modelProfiles.Flush(); err != nil {
		return err
	}
	result.Persisted = true
	log.Printf("[ProviderQuality-Probe] endpoint=%s model=%s score=%.3f confidence=%.3f samples=%d/%d",
		result.EndpointUID, result.ModelID, result.Score, result.Confidence, result.SuccessCount, result.SampleCount)
	return nil
}

func (p *ProviderQualityProbe) newProviderQualityModelProfile(
	endpoint *KeyEndpointProfile,
	metricsKey string,
	modelID string,
) *ModelProfile {
	now := p.config.TimeFunc()
	family := InferModelFamily(modelID, "")
	modelProfile := &ModelProfile{
		ChannelUID:  endpoint.ChannelUID,
		ChannelID:   endpoint.ChannelID,
		ChannelKind: endpoint.ChannelKind,
		ServiceType: endpoint.ServiceType,
		MetricsKey:  metricsKey,
		ModelID:     modelID,
		UpdatedAt:   now,
		ModelFamily: family,
		QualityTier: ModelProfileQualityTier(modelID, family),
		Source:      "l3_probe",
	}

	if p.cfgManager == nil {
		return modelProfile
	}
	cfg := p.cfgManager.GetConfig()
	upstream := findProviderQualityUpstream(p.cfgManager, endpoint.ChannelUID, endpoint.ChannelKind)
	resolved := config.ResolveUpstreamCapability(modelID, upstream, cfg.UpstreamModelCapabilities)
	if !resolved.Known {
		return modelProfile
	}
	applyUpstreamModelCapability(modelProfile, resolved.Capability)
	return modelProfile
}

func averageSuccessfulProviderQualityLatency(samples []ProviderQualitySampleResult) int64 {
	var total int64
	var count int64
	for _, sample := range samples {
		if sample.ErrorCode != "" || !sample.Evidence.ContentPresent {
			continue
		}
		total += sample.LatencyMs
		count++
	}
	if count == 0 {
		return 0
	}
	return total / count
}
