package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// ============== 缓存 ==============

const (
	compatDiagnoseCacheTTL          = 10 * time.Minute
	compatDiagnoseResponseBodyLimit = 64 * 1024
)

type compatDiagnoseCacheEntry struct {
	result    CompatDiagnoseResult
	expiresAt time.Time
}

var compatDiagnoseCache = struct {
	sync.RWMutex
	entries map[string]*compatDiagnoseCacheEntry
}{entries: make(map[string]*compatDiagnoseCacheEntry)}

func getCompatDiagnoseCache(key string) (*CompatDiagnoseResult, bool) {
	compatDiagnoseCache.RLock()
	defer compatDiagnoseCache.RUnlock()
	e, ok := compatDiagnoseCache.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	r := e.result
	return &r, true
}

func setCompatDiagnoseCache(key string, r CompatDiagnoseResult) {
	compatDiagnoseCache.Lock()
	compatDiagnoseCache.entries[key] = &compatDiagnoseCacheEntry{result: r, expiresAt: time.Now().Add(compatDiagnoseCacheTTL)}
	compatDiagnoseCache.Unlock()
}

// ============== 类型 ==============

// CompatDiagnoseResult 诊断结果
type CompatDiagnoseResult struct {
	Recommendations    map[string]bool    `json:"recommendations"`
	URLRecommendations *URLRecommendation `json:"urlRecommendations,omitempty"`
	Evidence           map[string]string  `json:"evidence"`
	Duration           int64              `json:"duration"` // ms
	Cached             bool               `json:"cached"`
}

// URLRecommendation BaseURL 修正建议（如误带 # 导致版本前缀拼接错误）
type URLRecommendation struct {
	Current     string `json:"current"`     // 当前 BaseURL（首个）
	Recommended string `json:"recommended"` // 推荐 BaseURL
	Reason      string `json:"reason"`      // 修正原因
}

// ============== 主 Handler ==============

// DiagnoseChannelCompat 兼容性诊断处理器
func DiagnoseChannelCompat(cfgManager *config.ConfigManager, channelKind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
			return
		}
		channel, err := getCapabilityTestChannel(cfgManager, channelKind, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}

		apiKey := ""
		if len(channel.APIKeys) > 0 {
			apiKey = channel.APIKeys[0]
		} else if len(channel.DisabledAPIKeys) > 0 {
			apiKey = channel.DisabledAPIKeys[0].Key
		}
		if apiKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no_api_key"})
			return
		}

		baseURL := capabilityTestBaseURL(channel)
		if baseURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no base url"})
			return
		}

		cacheKey := fmt.Sprintf("compat:%s:%s:%s:%s", channelKind, baseURL, apiKey, channel.ServiceType)
		if cached, ok := getCompatDiagnoseCache(cacheKey); ok {
			cached.Cached = true
			c.JSON(http.StatusOK, cached)
			return
		}

		start := time.Now()
		result := runCompatDiagnose(channel, channelKind, apiKey, baseURL)
		result.Duration = time.Since(start).Milliseconds()
		result.Cached = false

		setCompatDiagnoseCache(cacheKey, result)
		c.JSON(http.StatusOK, result)
	}
}

// ============== 诊断逻辑 ==============

func runCompatDiagnose(channel *config.UpstreamConfig, channelKind, apiKey, baseURL string) CompatDiagnoseResult {
	return runCompatDiagnoseWithProbeModel(channel, channelKind, apiKey, baseURL, "")
}

func runCompatDiagnoseWithProbeModel(channel *config.UpstreamConfig, channelKind, apiKey, baseURL, probeModel string) CompatDiagnoseResult {
	recs := make(map[string]bool)
	evid := make(map[string]string)
	urlRec := diagnoseBaseURLHashWithModel(channel, channelKind, apiKey, baseURL, probeModel)
	if urlRec != nil {
		evid["baseUrl"] = urlRec.Reason
	}

	switch channel.ServiceType {
	case "claude", "messages":
		diagnoseClaudeChannelWithModel(channel, apiKey, baseURL, probeModel, recs, evid)
	case "gemini":
		diagnoseGeminiChannelWithModel(channel, apiKey, baseURL, probeModel, recs, evid)
	default:
		log.Printf("[CompatDiagnose] serviceType %q: no diagnose rules", channel.ServiceType)
	}
	diagnoseImageGenerationToolWithModel(channel, channelKind, apiKey, baseURL, probeModel, recs, evid)

	return CompatDiagnoseResult{Recommendations: recs, URLRecommendations: urlRec, Evidence: evid}
}

var strictClaudeThinkingKeywords = []string{
	"deepseek", "glm", "zhipu", "bigmodel",
	"volc", "volces", "ark.cn-beijing",
	"compshare", "modelscope",
	"opencode",
}

var deepSeekClaudeThinkingCacheKeywords = []string{
	"deepseek",
}

var domesticClaudeProviderKeywords = []string{
	"deepseek", "mimo", "xiaomimimo",
	"compshare",
	"kimi", "moonshot",
	"glm", "zhipu", "bigmodel",
	"minimax",
	"dashscope", "aliyun", "aliyuncs",
	"modelscope",
	"volc", "volces", "ark.cn-beijing",
	"qianfan", "baidu", "baidubce",
	"xfyun", "xf-yun", "iflytek",
	"tencent", "lkeap", "hunyuan",
	"opencode",
}

var compatDiagnoseAggregateProviderKeywords = []string{
	"anthropic.com",
	"openrouter",
	"runapi",
	"unity2",
	"originrouter",
}

func shouldPassbackThinkingBlocksByDefault(channel *config.UpstreamConfig, baseURL string) bool {
	return channelMatchesCompatKeywords(channel, baseURL, strictClaudeThinkingKeywords)
}

func shouldUseThinkingCacheForClaudePassback(channel *config.UpstreamConfig, baseURL string) bool {
	return channelMatchesCompatKeywords(channel, baseURL, deepSeekClaudeThinkingCacheKeywords)
}

func shouldStripEmptyTextBlocksByDefault(channel *config.UpstreamConfig, baseURL string) bool {
	return channelMatchesCompatKeywords(channel, baseURL, strictClaudeThinkingKeywords)
}

func shouldNormalizeSystemRoleToTopLevelByDefault(channel *config.UpstreamConfig, baseURL string) bool {
	return channelMatchesCompatKeywords(channel, baseURL, domesticClaudeProviderKeywords)
}

func channelMatchesCompatKeywords(channel *config.UpstreamConfig, baseURL string, keywords []string) bool {
	signal := buildCompatDiagnoseChannelSignal(channel, baseURL)
	if containsAnyCompatKeyword(signal, compatDiagnoseAggregateProviderKeywords) {
		return false
	}
	return containsAnyCompatKeyword(signal, keywords)
}

func buildCompatDiagnoseChannelSignal(channel *config.UpstreamConfig, baseURL string) string {
	parts := []string{
		baseURL,
		channel.BaseURL,
		channel.Name,
		channel.Description,
		channel.Website,
		channel.ServiceType,
		channel.RoutePrefix,
	}
	for key, value := range channel.CustomHeaders {
		parts = append(parts, key, value)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func containsAnyCompatKeyword(signal string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(signal, keyword) {
			return true
		}
	}
	return false
}

// diagnoseBaseURLHash 检测 BaseURL 末尾 # 是否导致版本前缀拼接错误。
// # 是 CCX 的高级语义：显式禁止自动追加 /v1、/v1beta 等。
// 当前 URL 探测失败而反向 # 形态探测成功时，才给出覆盖建议。
func diagnoseBaseURLHash(channel *config.UpstreamConfig, channelKind, apiKey, baseURL string) *URLRecommendation {
	return diagnoseBaseURLHashWithModel(channel, channelKind, apiKey, baseURL, "")
}

func diagnoseBaseURLHashWithModel(channel *config.UpstreamConfig, channelKind, apiKey, baseURL, probeModel string) *URLRecommendation {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return nil
	}

	candidate := ""
	if strings.HasSuffix(trimmed, "#") {
		candidate = strings.TrimRight(strings.TrimSuffix(trimmed, "#"), "/")
	} else {
		candidate = strings.TrimRight(trimmed, "/") + "#"
	}
	if candidate == strings.TrimRight(trimmed, "/") {
		return nil
	}

	if probeBaseURLCandidateWithModel(channel, channelKind, apiKey, trimmed, probeModel) != compatBaseURLProbeFailed {
		return nil
	}
	if probeBaseURLCandidateWithModel(channel, channelKind, apiKey, candidate, probeModel) != compatBaseURLProbeSucceeded {
		return nil
	}

	var reason string
	if strings.HasSuffix(trimmed, "#") {
		reason = "当前 BaseURL 末尾 # 会禁止自动追加版本前缀，探测失败；移除 # 后探测成功"
	} else {
		reason = "当前 BaseURL 会自动追加版本前缀，探测失败；追加 # 禁止自动追加后探测成功"
	}
	return &URLRecommendation{Current: trimmed, Recommended: candidate, Reason: reason}
}

type compatBaseURLProbeStatus int

const (
	compatBaseURLProbeFailed compatBaseURLProbeStatus = iota
	compatBaseURLProbeSucceeded
	compatBaseURLProbeInconclusive
)

func probeBaseURLCandidateWithModel(channel *config.UpstreamConfig, channelKind, apiKey, baseURL, probeModel string) compatBaseURLProbeStatus {
	candidate := *channel
	candidate.BaseURL = baseURL
	candidate.BaseURLs = nil

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var (
		req *http.Request
		err error
	)
	protocol := compatProbeProtocol(channel, channelKind)
	switch protocol {
	case "gemini":
		model := compatProbeModel("gemini-3.5-flash", probeModel)
		req, err = buildGeminiCompatRequest(baseURL, "/v1beta/models/"+model+":streamGenerateContent?alt=sse", buildGeminiCompatProbeBody(), &candidate, apiKey)
	case "chat":
		req, err = buildOpenAIChatCompatRequest(baseURL, buildOpenAIChatCompatProbeBody(probeModel), &candidate, apiKey)
	case "responses":
		req, err = buildResponsesCompatRequest(baseURL, buildResponsesCompatProbeBody(probeModel), &candidate, apiKey)
	default:
		req, err = buildClaudeCompatRequest(baseURL, buildSystemRoleInMessagesProbeBody(compatProbeModel(capabilityProbeModelClaudeFable5, probeModel)), &candidate, apiKey)
	}
	if err != nil {
		return compatBaseURLProbeFailed
	}
	events, statusCode, sendErr := sendAndReadSSE(ctx, req, &candidate)
	if isCompatProbeTimeout(sendErr, ctx) {
		return compatBaseURLProbeInconclusive
	}
	if sendErr != nil || statusCode < 200 || statusCode >= 300 {
		return compatBaseURLProbeFailed
	}
	if !hasMeaningfulCompatSSE(events, protocol) {
		return compatBaseURLProbeFailed
	}
	return compatBaseURLProbeSucceeded
}

func compatProbeProtocol(channel *config.UpstreamConfig, channelKind string) string {
	switch channelKind {
	case "responses":
		switch channel.ServiceType {
		case "claude", "gemini", "responses":
			return channel.ServiceType
		case "copilot":
			return "responses"
		case "openai", "chat":
			return "chat"
		}
	case "messages":
		// Messages 渠道若 serviceType 指向非 Claude 上游，用上游实际协议探测能力，
		// 避免用 Claude 格式探测一个只接受 responses/chat 格式的上游导致假阴性。
		switch channel.ServiceType {
		case "responses", "copilot":
			return "responses"
		case "openai", "chat":
			return "chat"
		case "gemini":
			return "gemini"
		}
	}
	return channelKind
}

func shouldProbeImageGenerationTool(channel *config.UpstreamConfig, channelKind string) string {
	if channelKind == "responses" {
		switch channel.ServiceType {
		case "responses", "copilot":
			return "responses"
		}
		return ""
	}
	if channelKind == "chat" {
		switch channel.ServiceType {
		case "", "openai", "chat", "gemini":
			return "chat"
		}
		return ""
	}
	return ""
}

func diagnoseImageGenerationToolWithModel(channel *config.UpstreamConfig, channelKind, apiKey, baseURL, probeModel string, recs map[string]bool, evid map[string]string) {
	protocol := shouldProbeImageGenerationTool(channel, channelKind)
	if protocol == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
	defer cancel()

	results := probeImageGenerationToolModes(ctx, channel, protocol, apiKey, baseURL, probeModel)
	state := aggregateImageGenerationProbeState(results)
	switch state {
	case ImageGenerationProbeSupported:
		recs["stripImageGenerationTool"] = false
		if protocol == "responses" {
			evid["stripImageGenerationTool"] = "upstream accepted image_generation tool and Codex image_gen namespace"
		} else {
			evid["stripImageGenerationTool"] = "upstream accepted image_generation tool"
		}
	case ImageGenerationProbeUnsupported:
		recs["stripImageGenerationTool"] = true
		evid["stripImageGenerationTool"] = codexProbeDiagnostic(results)
	default:
		evid["imageGenerationToolProbe"] = "image generation tool probe was inconclusive"
		log.Printf("[CompatDiagnose] image generation tool probes inconclusive: %s", codexProbeDiagnostic(results))
	}
}

// diagnoseClaudeChannel 探测 Claude 兼容渠道
// 检测：passbackReasoningContent、passbackThinkingBlocks、stripEmptyTextBlocks、normalizeSystemRoleToTopLevel

func diagnoseClaudeChannelWithModel(channel *config.UpstreamConfig, apiKey, baseURL, probeModel string, recs map[string]bool, evid map[string]string) {
	probeModel = compatProbeModel(capabilityProbeModelClaudeFable5, probeModel)
	shouldPassbackThinkingBlocks := shouldPassbackThinkingBlocksByDefault(channel, baseURL)
	shouldUseThinkingCache := shouldUseThinkingCacheForClaudePassback(channel, baseURL)
	shouldStripEmptyTextBlocks := shouldStripEmptyTextBlocksByDefault(channel, baseURL)
	shouldNormalizeSystemRole := shouldNormalizeSystemRoleToTopLevelByDefault(channel, baseURL)
	hasThinkingProbe := false

	if shouldUseThinkingCache {
		applyDeepSeekClaudeCacheRecommendations(recs, evid)
	}

	// 探测 1：带 thinking 的流式请求
	ctx1, cancel1 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel1()

	thinkingBody := buildClaudeThinkingProbeBody(probeModel)
	req, err := buildClaudeCompatRequest(baseURL, thinkingBody, channel, apiKey)
	if err != nil {
		log.Printf("[CompatDiagnose] build thinking probe: %v", err)
	} else {
		events, statusCode, reqErr := sendAndReadSSE(ctx1, req, channel)
		if reqErr != nil || statusCode < 200 || statusCode >= 300 {
			log.Printf("[CompatDiagnose] thinking probe failed (status=%d): %v", statusCode, reqErr)
		} else {
			hasThinking, hasEmptyText := analyzeClaudeSSE(events)
			hasThinkingProbe = hasThinking
			if hasThinking {
				if shouldUseThinkingCache {
					evid["passbackReasoningContent"] = "DeepSeek Claude thinking uses CCX SQLite cache; keep legacy reasoning_content passback disabled"
					evid["passbackThinkingBlocks"] = "DeepSeek Claude thinking uses CCX SQLite cache; cached content[].thinking is injected only on exact history hits"
				} else {
					recs["passbackReasoningContent"] = true
					evid["passbackReasoningContent"] = "upstream returned thinking block in stream"
				}
			} else {
				if !shouldUseThinkingCache {
					recs["passbackReasoningContent"] = false
					evid["passbackReasoningContent"] = "no thinking block detected"
					recs["passbackThinkingBlocks"] = false
					evid["passbackThinkingBlocks"] = "no thinking block detected"
				}
			}
			if hasEmptyText || shouldStripEmptyTextBlocks {
				recs["stripEmptyTextBlocks"] = true
				if hasEmptyText {
					evid["stripEmptyTextBlocks"] = "upstream returned empty text content blocks"
				} else {
					evid["stripEmptyTextBlocks"] = "strict Claude-compatible upstream defaults to stripping empty text blocks before tool_use"
				}
			} else {
				recs["stripEmptyTextBlocks"] = false
				evid["stripEmptyTextBlocks"] = "no empty text blocks detected"
			}
		}
	}

	if hasThinkingProbe && !shouldUseThinkingCache {
		diagnoseClaudeThinkingBlockPassback(channel, apiKey, baseURL, probeModel, shouldPassbackThinkingBlocks, recs, evid)
	}

	// 探测 2：system role 放在 messages 数组中，检测是否需要 normalizeSystemRoleToTopLevel
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()

	normBody := buildSystemRoleInMessagesProbeBody(probeModel)
	req2, err := buildClaudeCompatRequest(baseURL, normBody, channel, apiKey)
	if err == nil {
		_, status2, _ := sendAndReadSSE(ctx2, req2, channel)
		if status2 == 400 || status2 == 422 {
			recs["normalizeSystemRoleToTopLevel"] = true
			evid["normalizeSystemRoleToTopLevel"] = fmt.Sprintf("upstream rejected system role in messages array (HTTP %d)", status2)
		} else if shouldNormalizeSystemRole {
			recs["normalizeSystemRoleToTopLevel"] = true
			evid["normalizeSystemRoleToTopLevel"] = "domestic Claude-compatible upstreams default to top-level system normalization"
		} else {
			recs["normalizeSystemRoleToTopLevel"] = false
			evid["normalizeSystemRoleToTopLevel"] = "upstream accepted system role in messages array"
		}
	}
}

func applyDeepSeekClaudeCacheRecommendations(recs map[string]bool, evid map[string]string) {
	recs["passbackReasoningContent"] = false
	recs["passbackThinkingBlocks"] = false
	recs["stripEmptyTextBlocks"] = true
	recs["normalizeMetadataUserId"] = true
	recs["stripBillingHeader"] = true

	evid["passbackReasoningContent"] = "DeepSeek Claude thinking uses CCX SQLite cache; legacy reasoning_content passback must stay disabled"
	evid["passbackThinkingBlocks"] = "DeepSeek Claude thinking uses CCX SQLite cache; do not project reasoning_content globally"
	evid["stripEmptyTextBlocks"] = "DeepSeek Claude endpoint strictly validates empty text blocks before tool_use"
	evid["normalizeMetadataUserId"] = "DeepSeek Claude endpoint expects flattened metadata.user_id"
	evid["stripBillingHeader"] = "DeepSeek Claude endpoint should not receive Claude Code billing suffixes"
}

func diagnoseClaudeThinkingBlockPassback(channel *config.UpstreamConfig, apiKey, baseURL, probeModel string, defaultEnabled bool, recs map[string]bool, evid map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := buildClaudeCompatRequest(baseURL, buildClaudeHistoricalThinkingBlockProbeBody(probeModel), channel, apiKey)
	if err != nil {
		log.Printf("[CompatDiagnose] build historical thinking probe: %v", err)
		recs["passbackThinkingBlocks"] = defaultEnabled
		evid["passbackThinkingBlocks"] = "historical content thinking block probe failed to build; used provider default"
		return
	}

	_, statusCode, reqErr := sendAndReadSSE(ctx, req, channel)
	if reqErr == nil && statusCode >= 200 && statusCode < 300 {
		recs["passbackThinkingBlocks"] = true
		evid["passbackThinkingBlocks"] = "upstream accepted historical content thinking blocks"
		return
	}

	if statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity {
		// 历史 thinking block 探测仅检测 thinking 格式支持，不影响 reasoning content 透传；
		// passbackReasoningContent 由独立探测决定，不在此覆写。
		recs["passbackThinkingBlocks"] = false
		evid["passbackThinkingBlocks"] = fmt.Sprintf("upstream rejected historical content thinking blocks (HTTP %d)", statusCode)
		return
	}

	log.Printf("[CompatDiagnose] historical thinking probe inconclusive (status=%d): %v", statusCode, reqErr)
	recs["passbackThinkingBlocks"] = defaultEnabled
	evid["passbackThinkingBlocks"] = "historical content thinking block probe inconclusive; used provider default"
}

// diagnoseGeminiChannel 探测 Gemini 兼容渠道
// 检测：stripThoughtSignature

func diagnoseGeminiChannelWithModel(channel *config.UpstreamConfig, apiKey, baseURL, probeModel string, recs map[string]bool, evid map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	probeModel = compatProbeModel("gemini-3.5-flash", probeModel)
	body := buildGeminiCompatProbeBody()
	endpoint := "/v1beta/models/" + probeModel + ":streamGenerateContent?alt=sse"

	req, err := buildGeminiCompatRequest(baseURL, endpoint, body, channel, apiKey)
	if err != nil {
		log.Printf("[CompatDiagnose] build gemini probe: %v", err)
		return
	}

	events, statusCode, reqErr := sendAndReadSSE(ctx, req, channel)
	if reqErr != nil || statusCode < 200 || statusCode >= 300 {
		log.Printf("[CompatDiagnose] gemini probe failed (status=%d): %v", statusCode, reqErr)
		return
	}

	hasThoughtSignature, hasLeakage := analyzeGeminiSSE(events)
	if hasLeakage {
		recs["stripThoughtSignature"] = true
		evid["stripThoughtSignature"] = "thought_signature leaked into visible text"
	} else if hasThoughtSignature {
		recs["stripThoughtSignature"] = false
		evid["stripThoughtSignature"] = "thought_signature properly contained"
	}
}

// ============== 请求构建 ==============

func buildClaudeCompatRequest(baseURL string, body []byte, channel *config.UpstreamConfig, apiKey string) (*http.Request, error) {
	body, sessionID := ensureClaudeCodeProbeBody(body)
	url := buildCapabilityTestURL(baseURL, "/v1", "/messages")
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	applyClaudeCodeProbeHeaders(req.Header, sessionID)
	utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, channel.AuthHeader)
	for k, v := range channel.CustomHeaders {
		req.Header.Set(k, v)
	}
	return req, nil
}

func buildGeminiCompatRequest(baseURL, endpoint string, body []byte, channel *config.UpstreamConfig, apiKey string) (*http.Request, error) {
	url := buildCapabilityTestURL(baseURL, "/v1beta", endpoint)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if utils.HasAuthenticationHeaderOverride(channel.AuthHeader) {
		utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, channel.AuthHeader)
	} else {
		utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
	}
	for k, v := range channel.CustomHeaders {
		req.Header.Set(k, v)
	}
	return req, nil
}

func buildOpenAIChatCompatRequest(baseURL string, body []byte, channel *config.UpstreamConfig, apiKey string) (*http.Request, error) {
	url := buildCapabilityTestURL(baseURL, "/v1", "/chat/completions")
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, channel.AuthHeader)
	for k, v := range channel.CustomHeaders {
		req.Header.Set(k, v)
	}
	return req, nil
}

func buildResponsesCompatRequest(baseURL string, body []byte, channel *config.UpstreamConfig, apiKey string) (*http.Request, error) {
	url := buildCapabilityTestURL(baseURL, "/v1", "/responses")
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Originator", "codex_cli_rs")
	utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, channel.AuthHeader)
	for k, v := range channel.CustomHeaders {
		req.Header.Set(k, v)
	}
	return req, nil
}

// ============== 探测请求体 ==============

func compatProbeModel(defaultModel string, candidates ...string) string {
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return defaultModel
}

// buildClaudeThinkingProbeBody 带 thinking enabled 的最小探测，nonce 防止上游缓存命中
func buildClaudeThinkingProbeBody(model string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"system":     []map[string]interface{}{{"type": "text", "text": "You are a helpful assistant. Nonce: " + nonce}},
		"messages":   []map[string]string{{"role": "user", "content": "Reply with one word."}},
		"max_tokens": 300,
		"stream":     true,
		"thinking":   map[string]interface{}{"type": "enabled", "budget_tokens": 512},
	})
	return body
}

// buildClaudeHistoricalThinkingBlockProbeBody 检测上游是否接受历史 assistant content[].thinking。
func buildClaudeHistoricalThinkingBlockProbeBody(model string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"system": []map[string]interface{}{
			{"type": "text", "text": "You are a helpful assistant. Nonce: " + nonce},
		},
		"messages": []map[string]interface{}{
			{"role": "user", "content": "Reply with ok."},
			{
				"role": "assistant",
				"content": []map[string]string{
					{"type": "thinking", "thinking": "previous reasoning"},
					{"type": "text", "text": "ok"},
				},
			},
			{"role": "user", "content": "Reply with ok again."},
		},
		"max_tokens": 50,
		"stream":     true,
		"thinking":   map[string]interface{}{"type": "enabled", "budget_tokens": 512},
	})
	return body
}

// buildSystemRoleInMessagesProbeBody system role 在 messages 数组中，用于检测 normalizeSystemRoleToTopLevel
func buildSystemRoleInMessagesProbeBody(model string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helpful assistant. Nonce: " + nonce},
			{"role": "user", "content": "Reply with one word."},
		},
		"max_tokens": 50,
		"stream":     true,
	})
	return body
}

// buildGeminiCompatProbeBody Gemini 探测请求体
func buildGeminiCompatProbeBody() []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{"role": "user", "parts": []map[string]string{{"text": "Reply with one word. Nonce: " + nonce}}},
		},
		"generationConfig": map[string]interface{}{
			"maxOutputTokens": 50,
			"thinkingConfig":  map[string]interface{}{"thinkingLevel": "low"},
		},
	})
	return body
}

func buildOpenAIChatCompatProbeBody(models ...string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model": compatProbeModel("gpt-5.4-mini", models...),
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with one word. Nonce: " + nonce},
		},
		"max_tokens": 16,
		"stream":     true,
	})
	return body
}

func buildOpenAIChatImageGenerationToolProbeBody(models ...string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model": compatProbeModel("gpt-5.4-mini", models...),
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with ok. Nonce: " + nonce},
		},
		"max_tokens": 16,
		"stream":     true,
		"tools": []map[string]string{
			{"type": "image_generation"},
		},
	})
	return body
}

func buildResponsesCompatProbeBody(models ...string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model":             compatProbeModel("gpt-5.4-mini", models...),
		"input":             "Reply with one word. Nonce: " + nonce,
		"max_output_tokens": 16,
		"stream":            true,
	})
	return body
}

func buildResponsesImageGenerationToolProbeBody(models ...string) []byte {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"model":             compatProbeModel("gpt-5.4-mini", models...),
		"input":             "Reply with ok. Nonce: " + nonce,
		"max_output_tokens": 16,
		"stream":            true,
		"tools": []map[string]string{
			{"type": "image_generation"},
		},
	})
	return body
}

// ============== SSE 读取与分析 ==============

// sendAndReadSSE 发送请求并读取完整 SSE 流，返回所有 data: 行内容
func sendAndReadSSE(ctx context.Context, req *http.Request, channel *config.UpstreamConfig) ([]string, int, error) {
	lines, statusCode, _, err := sendCompatProbe(ctx, req, channel)
	return lines, statusCode, err
}

func sendCompatProbe(ctx context.Context, req *http.Request, channel *config.UpstreamConfig) ([]string, int, string, error) {
	envCfg := config.NewEnvConfig()
	req = req.WithContext(ctx)
	resp, err := common.SendRequest(req, channel, envCfg, true, "Messages")
	if err != nil {
		return nil, 0, "", err
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, compatDiagnoseResponseBodyLimit))
		return nil, resp.StatusCode, string(bodyBytes), fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if l := scanner.Text(); strings.HasPrefix(l, "data:") {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(l, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return lines, resp.StatusCode, "", ctxErr
		}
		return lines, resp.StatusCode, "", err
	}
	return lines, resp.StatusCode, "", nil
}

func isCompatProbeTimeout(sendErr error, ctx context.Context) bool {
	if sendErr == nil {
		return false
	}
	if ctx.Err() != nil || errors.Is(sendErr, context.DeadlineExceeded) {
		return true
	}
	var timeoutErr interface{ Timeout() bool }
	return errors.As(sendErr, &timeoutErr) && timeoutErr.Timeout()
}

func isImageGenerationToolUnsupported(statusCode int, diagnostic string) bool {
	text := strings.ToLower(strings.TrimSpace(diagnostic))
	if text == "" {
		return false
	}
	if !strings.Contains(text, "image_generation") && !strings.Contains(text, "image generation") &&
		!strings.Contains(text, "image_gen") && !strings.Contains(text, "imagegen") {
		return false
	}

	if containsAnyCompatKeyword(text, []string{
		"not enabled",
		"not allowed",
		"permission",
		"requires",
		"unsupported",
		"not supported",
		"unknown tool",
		"invalid tool",
		"unrecognized tool",
		"tool is not",
	}) {
		return true
	}

	return statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity
}

func hasMeaningfulCompatSSE(lines []string, channelKind string) bool {
	for _, line := range lines {
		if line == "" || line == "[DONE]" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch channelKind {
		case "gemini":
			if hasMeaningfulGeminiCompatEvent(ev) {
				return true
			}
		case "chat":
			if hasMeaningfulOpenAIChatCompatEvent(ev) {
				return true
			}
		case "responses":
			if hasMeaningfulResponsesCompatEvent(ev) {
				return true
			}
		default:
			if hasMeaningfulClaudeCompatEvent(ev) {
				return true
			}
		}
	}
	return false
}

func hasMeaningfulClaudeCompatEvent(ev map[string]interface{}) bool {
	if stringField(ev, "type") != "content_block_delta" {
		return false
	}
	delta, ok := ev["delta"].(map[string]interface{})
	if !ok {
		return false
	}
	return hasAnyNonEmptyStringField(delta, "text", "thinking", "reasoning_content", "partial_json")
}

func hasMeaningfulOpenAIChatCompatEvent(ev map[string]interface{}) bool {
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
		if hasAnyNonEmptyStringField(delta, "content", "reasoning_content", "reasoning") {
			return true
		}
		if toolCalls, ok := delta["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
			return true
		}
	}
	return false
}

func hasMeaningfulResponsesCompatEvent(ev map[string]interface{}) bool {
	eventType := stringField(ev, "type")
	switch eventType {
	case "response.output_text.delta", "response.reasoning_summary_text.delta":
		return hasAnyNonEmptyStringField(ev, "delta", "text")
	case "response.completed":
		return responseCompletedHasOutputText(ev)
	default:
		return false
	}
}

func responseCompletedHasOutputText(ev map[string]interface{}) bool {
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
		if !ok {
			continue
		}
		content, ok := item["content"].([]interface{})
		if !ok {
			continue
		}
		for _, contentValue := range content {
			contentItem, ok := contentValue.(map[string]interface{})
			if ok && hasAnyNonEmptyStringField(contentItem, "text", "output_text") {
				return true
			}
		}
	}
	return false
}

func hasMeaningfulGeminiCompatEvent(ev map[string]interface{}) bool {
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
			if hasAnyNonEmptyStringField(part, "text") {
				return true
			}
			functionCall, ok := part["functionCall"].(map[string]interface{})
			if ok && hasAnyNonEmptyStringField(functionCall, "name") {
				return true
			}
		}
	}
	return false
}

func hasAnyNonEmptyStringField(m map[string]interface{}, keys ...string) bool {
	for _, key := range keys {
		if strings.TrimSpace(stringField(m, key)) != "" {
			return true
		}
	}
	return false
}

func stringField(m map[string]interface{}, key string) string {
	value, _ := m[key].(string)
	return value
}

// analyzeClaudeSSE 分析 Claude SSE 流，返回 (hasThinking, hasEmptyText)
func analyzeClaudeSSE(lines []string) (bool, bool) {
	hasThinking, hasEmptyText := false, false
	for _, line := range lines {
		if line == "[DONE]" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] != "content_block_start" {
			continue
		}
		cb, ok := ev["content_block"].(map[string]interface{})
		if !ok {
			continue
		}
		t, _ := cb["type"].(string)
		if t == "thinking" || t == "redacted_thinking" {
			hasThinking = true
		}
		if t == "text" {
			if txt, _ := cb["text"].(string); txt == "" {
				hasEmptyText = true
			}
		}
	}
	return hasThinking, hasEmptyText
}

// analyzeGeminiSSE 分析 Gemini SSE 流，返回 (hasThoughtSignature, hasLeakage)
func analyzeGeminiSSE(lines []string) (bool, bool) {
	hasThoughtSignature, hasLeakage := false, false
	for _, line := range lines {
		if strings.Contains(line, "thought_signature") {
			hasThoughtSignature = true
		}
		if strings.Contains(line, "<think>") {
			hasLeakage = true
		}
	}
	return hasThoughtSignature, hasLeakage
}
