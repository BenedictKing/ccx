package handlers

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// fast 探活专用参数：多模型候选 × 4 协议并行探测，相较全量 ChannelDiscovery
// 不遍历全部模型、不做能力/兼容诊断，只为”快速定 kind 并建渠道”服务。
// 采用”一模型一协议成功即返回”策略，降低因单模型上游限流导致整体验活失败的风险。
const (
	fastDiscoveryProbeTimeout = 8 * time.Second
	fastDiscoveryRPM          = 60
	// 单次 fast 请求最多尝试的 (baseURL, apiKey) 组合数，避免几十个 key 串行探测。
	fastDiscoveryMaxCombos = 3
	// fast 探活允许的 manifest 兜底候选 serviceType。
	// 仅当上游命中已知内置 manifest 时才允许用静态模型清单，禁止对任意自定义上游
	// 盲探固定 GPT/Claude/Gemini 名称。
	fastDiscoveryManifestCandidates = "messages\x00openai\x00gemini\x00responses\x00copilot"
)

// ChannelDiscoveryFastRequest 快速探活请求体。
// 与 ChannelDiscoveryRequest 区别：不支持单次全量探测，仅探一个真实模型以定 primaryKind。
type ChannelDiscoveryFastRequest struct {
	ChannelKind        string            `json:"channelKind"` // 可选提示，不能限制自动探测结果
	BaseURL            string            `json:"baseUrl"`     // 兼容单 URL
	BaseURLs           []string          `json:"baseUrls"`
	APIKey             string            `json:"apiKey"` // 兼容单 key
	APIKeys            []string          `json:"apiKeys"`
	AuthHeader         string            `json:"authHeader"`
	CustomHeaders      map[string]string `json:"customHeaders"`
	ProxyURL           string            `json:"proxyUrl"`
	InsecureSkipVerify bool              `json:"insecureSkipVerify"`
}

// ChannelDiscoveryFastResponse 快速探活响应体。
// primaryKind 必须来自成功协议；testedModel/streamingSupported 仅作证据展示，
// 不得写入渠道级 SupportedModels。
type ChannelDiscoveryFastResponse struct {
	PrimaryKind        string                   `json:"primaryKind"`
	TestedModel        string                   `json:"testedModel"`
	StreamingSupported bool                     `json:"streamingSupported"`
	TestedKeyHash      string                   `json:"testedKeyHash"`
	RateLimit          DiscoveryRateLimitResult `json:"rateLimit"`
}

// ChannelDiscoveryFast 快速探活 handler：获取真实模型列表并逐模型探测协议，返回推荐 primaryKind。
// 采用多模型候选策略：Strong/Primary/Fast + 全列表前几个模型，逐个并行探测 4 协议，
// 只要有一个模型在一个协议上成功就返回成功，其余模型留给后台 discovery 补全。
// 全部候选模型均失败时返回 4xx，不建渠道；错误信息不泄露 API Key / baseURL 明文。
func ChannelDiscoveryFast(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ChannelDiscoveryFastRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		baseURLs := normalizeDiscoveryBaseURLs(req.BaseURL, req.BaseURLs)
		if len(baseURLs) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "baseUrl is required"})
			return
		}
		for _, baseURL := range baseURLs {
			if err := utils.ValidateBaseURL(baseURL); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		apiKeys := normalizeFastDiscoveryAPIKeys(req.APIKey, req.APIKeys)
		if len(apiKeys) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "apiKey is required"})
			return
		}

		// 遍历 (baseURL, apiKey) 组合，选择第一组能成功获取真实模型并完成探测的凭证。
		// 不能只固定使用 apiKeys[0] 导致第二个有效 key 被忽略。
		type comboResult struct {
			models    []string
			testedKey string
		}
		var selected *comboResult
		var modelsSource string
		var warnings []string
		maxCombos := len(baseURLs) * len(apiKeys)
		if maxCombos > fastDiscoveryMaxCombos {
			maxCombos = fastDiscoveryMaxCombos
		}
		tried := 0
		for _, baseURL := range baseURLs {
			if selected != nil {
				break
			}
			for _, apiKey := range apiKeys {
				if tried >= maxCombos {
					break
				}
				tried++
				channel := buildFastTransientChannel(baseURL, apiKey, req)
				models := discoverTransientModelsWithFetchers(c.Request.Context(), channel, "", apiKey, nil)
				if len(models.Items) == 0 {
					// models 端点失败：仅当命中已知内置 manifest 才用其静态清单兜底。
					manifestModels := fastDiscoveryManifestFallback(baseURL)
					if len(manifestModels) > 0 {
						selected = &comboResult{models: manifestModels, testedKey: apiKey}
						modelsSource = "builtin_manifest"
						break
					}
					warnings = append(warnings, models.Warnings...)
					continue
				}
				selected = &comboResult{models: models.Items, testedKey: apiKey}
				modelsSource = models.Source
				break
			}
		}
		if selected == nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "无法获取上游真实模型清单，且无已知 provider manifest 兜底"})
			return
		}

		// 构建候选模型列表（Strong/Primary/Fast + 全列表前几个），逐模型探测。
		// 只要有一个模型在一个协议上成功就通过，其余模型留给后台补全。
		globalCapabilities := map[string]config.UpstreamModelCapability(nil)
		if cfgManager != nil {
			globalCapabilities = cfgManager.GetConfig().UpstreamModelCapabilities
		}
		selectedModels := selectDiscoveryModels(selected.models, globalCapabilities)
		probeModels := discoveryProbeModels(selectedModels, selected.models)
		if len(probeModels) == 0 {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "未找到可探测的真实模型"})
			return
		}

		probeChannel := buildFastTransientChannel(baseURLs[0], selected.testedKey, req)
		protocols := []string{"messages", "responses", "chat", "gemini"}

		var bestModel string
		var bestKind string
		var bestResults []DiscoveryProtocolResult
		bestStreaming := false
		var bestRateLimit DiscoveryRateLimitResult

		for _, model := range probeModels {
			// 对当前模型并行探测 4 协议。
			results := make([]DiscoveryProtocolResult, len(protocols))
			streamingSupported := false
			var rateLimitMu sync.Mutex
			rateLimitedCount := 0
			anyRateLimited := false
			minEffectiveRPM := fastDiscoveryRPM

			var wg sync.WaitGroup
			for i, protocol := range protocols {
				wg.Add(1)
				go func(idx int, proto string) {
					defer wg.Done()
					pacer := newDiscoveryProbePacer(fastDiscoveryRPM)
					success, streaming := fastProbeProtocol(c.Request.Context(), probeChannel, proto, model, fastDiscoveryProbeTimeout, cfgManager, pacer)
					results[idx] = DiscoveryProtocolResult{Protocol: proto, Success: success}
					r := pacer.result()
					rateLimitMu.Lock()
					if success && streaming {
						streamingSupported = true
					}
					if r.RateLimited {
						anyRateLimited = true
					}
					rateLimitedCount += r.RateLimitedCount
					if r.EffectiveRPM > 0 && r.EffectiveRPM < minEffectiveRPM {
						minEffectiveRPM = r.EffectiveRPM
					}
					rateLimitMu.Unlock()
				}(i, protocol)
			}
			wg.Wait()

			// 只要当前模型有任意一个协议成功，就采用该结果并停止后续模型探测。
			primaryKind := recommendDiscoveryChannelKind(req.ChannelKind, nil, results)
			if primaryKind != "" {
				bestModel = model
				bestKind = primaryKind
				bestResults = results
				bestStreaming = streamingSupported
				bestRateLimit = DiscoveryRateLimitResult{
					InitialRPM:       fastDiscoveryRPM,
					EffectiveRPM:     minEffectiveRPM,
					RateLimited:      anyRateLimited,
					RateLimitedCount: rateLimitedCount,
				}
				break
			}
			// 当前模型全部协议失败，记录速率限制信息用于最终兜底返回。
			if anyRateLimited {
				bestRateLimit.RateLimited = true
			}
			bestRateLimit.RateLimitedCount += rateLimitedCount
		}

		if bestModel == "" {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "所有候选模型的协议探测均失败，无法确定渠道类型"})
			return
		}

		c.JSON(http.StatusOK, ChannelDiscoveryFastResponse{
			PrimaryKind:        bestKind,
			TestedModel:        bestModel,
			StreamingSupported: bestStreaming,
			TestedKeyHash:      autopilot.KeyHashFromAPIKey(selected.testedKey),
			RateLimit:          bestRateLimit,
		})
		_ = modelsSource // 保留用于后续诊断日志，当前不返回
		_ = warnings
		_ = bestResults // 最终探测结果，用于未来扩展诊断
	}
}

// buildFastTransientChannel 构造单 baseURL + 单 key 的临时发现渠道。
func buildFastTransientChannel(baseURL, apiKey string, req ChannelDiscoveryFastRequest) *config.UpstreamConfig {
	return &config.UpstreamConfig{
		Name:               "临时快速发现渠道",
		BaseURL:            baseURL,
		BaseURLs:           []string{baseURL},
		APIKeys:            []string{apiKey},
		AuthHeader:         strings.TrimSpace(req.AuthHeader),
		CustomHeaders:      cloneStringMap(req.CustomHeaders),
		ProxyURL:           strings.TrimSpace(req.ProxyURL),
		InsecureSkipVerify: req.InsecureSkipVerify,
	}
}

// normalizeFastDiscoveryAPIKeys 合并单个 apiKey 与 apiKeys 列表，去重去空。
func normalizeFastDiscoveryAPIKeys(single string, list []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(list)+1)
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
	add(single)
	for _, value := range list {
		add(value)
	}
	return result
}

// fastDiscoveryManifestFallback 在 models 端点失败时，仅命中已知内置 manifest 才返回静态模型清单。
func fastDiscoveryManifestFallback(baseURL string) []string {
	candidates := strings.Split(fastDiscoveryManifestCandidates, "\x00")
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, serviceType := range candidates {
		manifest, ok := config.LookupBuiltinManifest(baseURL, serviceType)
		if !ok || len(manifest.ModelIDs) == 0 {
			continue
		}
		for _, model := range manifest.ModelIDs {
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
	}
	return result
}

// fastProbeProtocol 对单个真实模型探测单个协议，返回成功标志与流式支持标志。
// 复用 executeModelTest 的 429 重试与 pacer 限速，但不把测试模型写成渠道白名单。
func fastProbeProtocol(ctx context.Context, channel *config.UpstreamConfig, protocol, model string, timeout time.Duration, cfgManager *config.ConfigManager, pacer *discoveryProbePacer) (bool, bool) {
	probeChannel := channel
	if strings.TrimSpace(channel.ServiceType) == "" {
		cloned := *channel
		cloned.ServiceType = resolveDiscoveryServiceType("", protocol)
		probeChannel = &cloned
	}
	for attempt := 0; ; attempt++ {
		if err := pacer.waitForNext(ctx); err != nil {
			return false, false
		}
		result := executeModelTest(ctx, probeChannel, protocol, model, timeout, "", cfgManager, -1, protocol, probeChannel.APIKeys[0], nil)
		if !isDiscoveryRateLimited(result) {
			return result.Success, result.StreamingSupported
		}
		pacer.observeRateLimited()
		if attempt >= maxDiscovery429Retries {
			return result.Success, result.StreamingSupported
		}
	}
}
