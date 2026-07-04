// Package messages 提供 Claude Messages API 的处理器
package messages

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/copilot"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/keypool"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

const (
	modelsRequestTimeout      = 5 * time.Second
	modelsCollectTimeout      = 1 * time.Second
	modelsBatchSize           = 2
	modelsMaxChannels         = 2
	modelsMaxAttempts         = 3
	modelsFallbackMaxAttempts = 1
	modelsMaxKeysPerChannel   = 5
	modelsDiscoveryCacheTTL   = 10 * time.Second
)

var errNoChannelWithDisabledKeys = errors.New("no channel with disabled keys")

// copilotTokenResolver 解析 Copilot runtime token；测试可替换以注入 mock，避免真实 GitHub 调用。
var copilotTokenResolver = copilot.ResolveTokenWithProxy

// ModelsResponse OpenAI 兼容的 models 响应格式
type ModelsResponse struct {
	Object  string       `json:"object"`
	Data    []ModelEntry `json:"data"`
	HasMore bool         `json:"has_more"`
	FirstID string       `json:"first_id,omitempty"`
	LastID  string       `json:"last_id,omitempty"`
}

// ModelEntry 单个模型条目
type ModelEntry struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name,omitempty"`
	Object              string   `json:"object"`
	Type                string   `json:"type,omitempty"`
	Created             int64    `json:"created"`
	CreatedAt           string   `json:"created_at,omitempty"`
	OwnedBy             string   `json:"owned_by"`
	DisplayName         string   `json:"display_name,omitempty"`
	LabelOverride       string   `json:"labelOverride,omitempty"`
	Supports1M          bool     `json:"supports1m,omitempty"`
	AnthropicFamilyTier string   `json:"anthropicFamilyTier,omitempty"`
	IsFamilyDefault     bool     `json:"isFamilyDefault,omitempty"`
	InputModalities     []string `json:"input_modalities,omitempty"`
}

// ModelsHandler 处理 /v1/models 请求，从 Messages、Responses、Chat、Gemini、Images 和 Vectors 渠道获取并合并模型列表
func ModelsHandler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	cache := newModelsDiscoveryCache()

	return func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		req := modelsCollectionRequest{
			ctx:              c.Request.Context(),
			cfgManager:       cfgManager,
			channelScheduler: channelScheduler,
			routePrefix:      c.Param("routePrefix"),
			channelName:      c.GetHeader("X-Channel"),
		}
		cacheKey := modelsDiscoveryCacheKey{
			routePrefix: req.routePrefix,
			channelName: req.channelName,
		}

		results := collectModelsFromAllKinds(req)
		messagesModels := results[scheduler.ChannelKindMessages]
		responsesModels := results[scheduler.ChannelKindResponses]
		chatModels := results[scheduler.ChannelKindChat]
		geminiModels := results[scheduler.ChannelKindGemini]
		imagesModels := results[scheduler.ChannelKindImages]
		vectorsModels := results[scheduler.ChannelKindVectors]

		mergedModels := mergeModels(messagesModels, responsesModels, chatModels, geminiModels, imagesModels, vectorsModels)

		if len(mergedModels) == 0 {
			if cached, ok := cache.Get(cacheKey); ok {
				log.Printf("[Models] 实时发现失败，返回缓存: routePrefix=%q, channel=%q, merged=%d",
					req.routePrefix, req.channelName, len(cached.Data))
				c.JSON(http.StatusOK, cached)
				return
			}
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"message": "models endpoint not available from any upstream",
					"type":    "not_found_error",
				},
			})
			return
		}

		response := buildModelsResponse(mergedModels)
		cache.Set(cacheKey, response)

		log.Printf("[Models] 合并完成: messages=%d, responses=%d, chat=%d, gemini=%d, images=%d, vectors=%d, merged=%d",
			len(messagesModels), len(responsesModels), len(chatModels), len(geminiModels), len(imagesModels), len(vectorsModels), len(mergedModels))

		c.JSON(http.StatusOK, response)
	}
}

type modelsDiscoveryCacheKey struct {
	routePrefix string
	channelName string
}

type modelsDiscoveryCacheEntry struct {
	response  ModelsResponse
	expiresAt time.Time
}

type modelsDiscoveryCache struct {
	mu      sync.RWMutex
	entries map[modelsDiscoveryCacheKey]modelsDiscoveryCacheEntry
}

func newModelsDiscoveryCache() *modelsDiscoveryCache {
	return &modelsDiscoveryCache{
		entries: make(map[modelsDiscoveryCacheKey]modelsDiscoveryCacheEntry),
	}
}

func (cache *modelsDiscoveryCache) Get(key modelsDiscoveryCacheKey) (ModelsResponse, bool) {
	now := time.Now()

	cache.mu.RLock()
	entry, ok := cache.entries[key]
	if !ok || now.After(entry.expiresAt) {
		cache.mu.RUnlock()
		return ModelsResponse{}, false
	}
	response := cloneModelsResponse(entry.response)
	cache.mu.RUnlock()

	return response, true
}

func (cache *modelsDiscoveryCache) Set(key modelsDiscoveryCacheKey, response ModelsResponse) {
	now := time.Now()

	cache.mu.Lock()
	for cachedKey, entry := range cache.entries {
		if now.After(entry.expiresAt) {
			delete(cache.entries, cachedKey)
		}
	}
	cache.entries[key] = modelsDiscoveryCacheEntry{
		response:  cloneModelsResponse(response),
		expiresAt: now.Add(modelsDiscoveryCacheTTL),
	}
	cache.mu.Unlock()
}

func cloneModelsResponse(response ModelsResponse) ModelsResponse {
	clone := response
	if response.Data == nil {
		return clone
	}

	clone.Data = make([]ModelEntry, len(response.Data))
	for i, model := range response.Data {
		if model.InputModalities != nil {
			model.InputModalities = append([]string(nil), model.InputModalities...)
		}
		clone.Data[i] = model
	}
	return clone
}

// ModelsDetailHandler 处理 /v1/models/:model 请求，转发到上游
func ModelsDetailHandler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		modelID := c.Param("model")
		if modelID == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"message": "model id is required",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		for _, kind := range []scheduler.ChannelKind{
			scheduler.ChannelKindMessages,
			scheduler.ChannelKindResponses,
			scheduler.ChannelKindChat,
			scheduler.ChannelKindGemini,
			scheduler.ChannelKindImages,
			scheduler.ChannelKindVectors,
		} {
			if body, _, ok := tryModelsRequest(c, cfgManager, channelScheduler, "GET", "/"+modelID, kind); ok {
				c.Data(http.StatusOK, "application/json", body)
				return
			}
		}

		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"message": "model not found",
				"type":    "not_found_error",
			},
		})
	}
}

type modelsCollectionRequest struct {
	ctx              context.Context
	cfgManager       *config.ConfigManager
	channelScheduler *scheduler.ChannelScheduler
	routePrefix      string
	channelName      string
}

type modelsChannelCandidate struct {
	selection *scheduler.SelectionResult
}

type modelsAPIKeyCandidate struct {
	apiKey           string
	disabledFallback bool
}

func collectModelsFromAllKinds(req modelsCollectionRequest) map[scheduler.ChannelKind][]ModelEntry {
	kinds := []scheduler.ChannelKind{
		scheduler.ChannelKindMessages,
		scheduler.ChannelKindResponses,
		scheduler.ChannelKindChat,
		scheduler.ChannelKindGemini,
		scheduler.ChannelKindImages,
		scheduler.ChannelKindVectors,
	}

	results := make(map[scheduler.ChannelKind][]ModelEntry, len(kinds))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, kind := range kinds {
		kind := kind
		wg.Add(1)
		go func() {
			defer wg.Done()
			models := collectModelsFromChannels(req, kind, modelsMaxChannels)
			mu.Lock()
			results[kind] = models
			mu.Unlock()
		}()
	}

	wg.Wait()
	return results
}

func collectModelsFromChannels(req modelsCollectionRequest, kind scheduler.ChannelKind, maxSuccess int) []ModelEntry {
	if maxSuccess <= 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(req.ctx, modelsCollectTimeout)
	defer cancel()

	candidates, failedChannels := selectModelsChannelCandidates(req, kind)
	if len(candidates) == 0 {
		if req.channelName == "" {
			return fetchModelsFromDisabledKeyFallback(ctx, req, kind, failedChannels, modelsFallbackMaxAttempts)
		}
		return nil
	}

	resultsByIndex := make(map[int][]ModelEntry, len(candidates))
	successCount := 0

	// 分批启动候选：每批 modelsBatchSize 个，等全批返回后决定是否继续
	for batchStart := 0; batchStart < len(candidates) && successCount < maxSuccess; batchStart += modelsBatchSize {
		batchEnd := batchStart + modelsBatchSize
		if batchEnd > len(candidates) {
			batchEnd = len(candidates)
		}
		batch := candidates[batchStart:batchEnd]

		type batchResult struct {
			index  int
			models []ModelEntry
		}
		resultCh := make(chan batchResult, len(batch))
		var wg sync.WaitGroup
		for i, candidate := range batch {
			i := i
			candidate := candidate
			wg.Add(1)
			go func() {
				defer wg.Done()
				models := fetchModelsFromCandidate(ctx, req.cfgManager, candidate, kind)
				if len(models) > 0 {
					resultCh <- batchResult{index: batchStart + i, models: models}
				}
			}()
		}

		wg.Wait()
		close(resultCh)

		for result := range resultCh {
			resultsByIndex[result.index] = result.models
			successCount++
		}

		if ctx.Err() != nil {
			break
		}
	}

	if len(resultsByIndex) == 0 {
		if req.channelName == "" && ctx.Err() == nil {
			if fallback := fetchModelsFromDisabledKeyFallback(ctx, req, kind, failedChannels, modelsFallbackMaxAttempts); len(fallback) > 0 {
				return mergeModels(fallback)
			}
		}
		return nil
	}

	modelLists := make([][]ModelEntry, 0, min(maxSuccess, len(resultsByIndex)))
	for idx := 0; idx < len(candidates) && len(modelLists) < maxSuccess; idx++ {
		if models := resultsByIndex[idx]; len(models) > 0 {
			modelLists = append(modelLists, models)
		}
	}

	merged := mergeModels(modelLists...)
	log.Printf("[%s-Models] 协议采集完成: successChannels=%d, merged=%d", channelKindLabel(kind), len(modelLists), len(merged))
	return merged
}

func selectModelsChannelCandidates(req modelsCollectionRequest, kind scheduler.ChannelKind) ([]modelsChannelCandidate, map[int]bool) {
	maxAttempts := modelsMaxAttempts
	if req.channelName != "" {
		maxAttempts = 1
	}

	failedChannels := make(map[int]bool)
	candidates := make([]modelsChannelCandidate, 0, maxAttempts)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selection, err := req.channelScheduler.SelectChannel(req.ctx, "", failedChannels, kind, "", req.routePrefix, req.channelName)
		if err != nil {
			if len(candidates) == 0 {
				log.Printf("[%s-Models] 渠道无可用: %v", channelKindLabel(kind), err)
			}
			break
		}
		candidates = append(candidates, modelsChannelCandidate{selection: selection})
		failedChannels[selection.ChannelIndex] = true
	}
	return candidates, failedChannels
}

func fetchModelsFromCandidate(ctx context.Context, cfgManager *config.ConfigManager, candidate modelsChannelCandidate, kind scheduler.ChannelKind) []ModelEntry {
	body, upstream, ok := requestModelsFromSelection(ctx, cfgManager, candidate.selection, "GET", "", kind)
	if !ok {
		return nil
	}
	return parseModelsResponseForKind(body, upstream, cfgManager.GetConfig().UpstreamModelCapabilities, kind)
}

func fetchModelsFromDisabledKeyFallback(ctx context.Context, req modelsCollectionRequest, kind scheduler.ChannelKind, failedChannels map[int]bool, maxAttempts int) []ModelEntry {
	if maxAttempts <= 0 || ctx.Err() != nil {
		return nil
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil
		}
		selection, err := selectChannelWithDisabledKeys(req.cfgManager, failedChannels, kind, req.routePrefix)
		if err != nil {
			break
		}
		log.Printf("[%s-Models] 活跃渠道不可用，回退到挂起渠道查询模型: channel=%s, reason=%s", channelKindLabel(kind), selection.Upstream.Name, selection.Reason)
		body, upstream, ok := requestModelsFromSelection(ctx, req.cfgManager, selection, "GET", "", kind)
		if ok {
			return parseModelsResponseForKind(body, upstream, req.cfgManager.GetConfig().UpstreamModelCapabilities, kind)
		}
		if ctx.Err() != nil {
			return nil
		}
		failedChannels[selection.ChannelIndex] = true
	}
	return nil
}

// fetchModelsFromChannels 从指定类型的渠道获取模型列表
func fetchModelsFromChannels(c *gin.Context, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind) []ModelEntry {
	body, upstream, ok := tryModelsRequest(c, cfgManager, channelScheduler, "GET", "", kind)
	if !ok {
		return nil
	}
	return parseModelsResponseForKind(body, upstream, cfgManager.GetConfig().UpstreamModelCapabilities, kind)
}

func parseModelsResponseForKind(body []byte, upstream *config.UpstreamConfig, globalCapabilities map[string]config.UpstreamModelCapability, kind scheduler.ChannelKind) []ModelEntry {
	// Gemini 渠道或 serviceType=gemini 的渠道返回 {"models": [...]} 格式
	if kind == scheduler.ChannelKindGemini {
		return enrichModelsForUpstream(parseGeminiModelsResponse(body), upstream, globalCapabilities)
	}

	// 尝试 OpenAI 格式解析
	var resp ModelsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("[%s-Models] 解析渠道响应失败: %v", channelKindLabel(kind), err)
		return nil
	}

	// 如果 data 为空，尝试 Gemini 格式（Responses 渠道中 serviceType=gemini 的情况）
	if len(resp.Data) == 0 {
		if geminiModels := parseGeminiModelsResponse(body); len(geminiModels) > 0 {
			return enrichModelsForUpstream(geminiModels, upstream, globalCapabilities)
		}
	}

	return enrichModelsForUpstream(resp.Data, upstream, globalCapabilities)
}

func enrichModelModalitiesForUpstream(models []ModelEntry, upstream *config.UpstreamConfig) []ModelEntry {
	return enrichModelsForUpstream(models, upstream, nil)
}

func enrichModelsForUpstream(models []ModelEntry, upstream *config.UpstreamConfig, globalCapabilities map[string]config.UpstreamModelCapability) []ModelEntry {
	if upstream == nil {
		return normalizeModelEntries(models, nil, globalCapabilities)
	}

	enriched := make([]ModelEntry, 0, len(models)+1)
	seen := make(map[string]int, len(models)+1)
	addOrUpdate := func(model ModelEntry) {
		model = normalizeModelEntry(model, upstream, globalCapabilities)
		if model.ID == "" {
			return
		}
		if idx, exists := seen[model.ID]; exists {
			enriched[idx] = mergeModelEntryMetadata(enriched[idx], model)
			return
		}
		seen[model.ID] = len(enriched)
		enriched = append(enriched, model)
	}

	for _, model := range models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			modelID = strings.TrimSpace(model.Name)
		}
		if _, isRequestModel := upstream.ModelMapping[modelID]; isRequestModel {
			model.InputModalities = inputModalitiesForRequestModel(upstream, modelID)
		} else {
			model.InputModalities = inputModalitiesForActualModel(upstream, modelID)
		}
		addOrUpdate(model)
	}

	for requestModel := range upstream.ModelMapping {
		requestModel = strings.TrimSpace(requestModel)
		if requestModel == "" {
			continue
		}
		addOrUpdate(ModelEntry{
			ID:              requestModel,
			Object:          "model",
			InputModalities: inputModalitiesForRequestModel(upstream, requestModel),
		})
	}

	if fallback := strings.TrimSpace(upstream.VisionFallbackModel); fallback != "" && !upstream.NoVision {
		addOrUpdate(ModelEntry{
			ID:              fallback,
			Object:          "model",
			InputModalities: inputModalitiesForActualModel(upstream, fallback),
		})
	}

	return enriched
}

func normalizeModelEntries(models []ModelEntry, upstream *config.UpstreamConfig, globalCapabilities map[string]config.UpstreamModelCapability) []ModelEntry {
	normalized := make([]ModelEntry, 0, len(models))
	for _, model := range models {
		model = normalizeModelEntry(model, upstream, globalCapabilities)
		if model.ID != "" {
			normalized = append(normalized, model)
		}
	}
	return normalized
}

func normalizeModelEntry(model ModelEntry, upstream *config.UpstreamConfig, globalCapabilities map[string]config.UpstreamModelCapability) ModelEntry {
	model.ID = strings.TrimSpace(model.ID)
	model.Name = strings.TrimSpace(model.Name)
	if model.ID == "" {
		model.ID = model.Name
	}
	if model.Name == "" {
		model.Name = model.ID
	}
	if model.Object == "" {
		model.Object = "model"
	}
	if model.Type == "" {
		model.Type = "model"
	}
	if model.CreatedAt == "" && model.Created > 0 {
		model.CreatedAt = time.Unix(model.Created, 0).UTC().Format(time.RFC3339)
	}

	resolved := config.ResolveUpstreamCapability(model.ID, upstream, globalCapabilities)
	if model.DisplayName == "" {
		model.DisplayName = resolved.Capability.DisplayName
	}
	if model.LabelOverride == "" {
		model.LabelOverride = model.DisplayName
	}
	if !model.Supports1M && resolved.Capability.ContextWindowTokens >= 1000000 {
		model.Supports1M = true
	}
	if model.AnthropicFamilyTier == "" {
		model.AnthropicFamilyTier = anthropicFamilyTierForModel(model.ID, resolved.ActualModel, resolved.Capability.DisplayName)
	}

	return model
}

func mergeModelEntryMetadata(existing, incoming ModelEntry) ModelEntry {
	existing.InputModalities = mergeInputModalities(existing.InputModalities, incoming.InputModalities)
	if existing.Name == "" {
		existing.Name = incoming.Name
	}
	if existing.Object == "" {
		existing.Object = incoming.Object
	}
	if existing.Type == "" {
		existing.Type = incoming.Type
	}
	if existing.Created == 0 {
		existing.Created = incoming.Created
	}
	if existing.CreatedAt == "" {
		existing.CreatedAt = incoming.CreatedAt
	}
	if existing.OwnedBy == "" {
		existing.OwnedBy = incoming.OwnedBy
	}
	if existing.DisplayName == "" {
		existing.DisplayName = incoming.DisplayName
	}
	if existing.LabelOverride == "" {
		existing.LabelOverride = incoming.LabelOverride
	}
	existing.Supports1M = existing.Supports1M || incoming.Supports1M
	if existing.AnthropicFamilyTier == "" {
		existing.AnthropicFamilyTier = incoming.AnthropicFamilyTier
	}
	existing.IsFamilyDefault = existing.IsFamilyDefault || incoming.IsFamilyDefault
	return existing
}

func buildModelsResponse(models []ModelEntry) ModelsResponse {
	markAnthropicFamilyDefaults(models)
	resp := ModelsResponse{
		Object:  "list",
		Data:    models,
		HasMore: false,
	}
	if len(models) > 0 {
		resp.FirstID = models[0].ID
		resp.LastID = models[len(models)-1].ID
	}
	return resp
}

func markAnthropicFamilyDefaults(models []ModelEntry) {
	seen := make(map[string]bool)
	for i := range models {
		tier := models[i].AnthropicFamilyTier
		if tier == "" {
			continue
		}
		if seen[tier] {
			models[i].IsFamilyDefault = false
			continue
		}
		models[i].IsFamilyDefault = true
		seen[tier] = true
	}
}

func anthropicFamilyTierForModel(requestModel, actualModel, displayName string) string {
	for _, value := range []string{requestModel, actualModel, displayName} {
		lower := strings.ToLower(value)
		switch {
		case strings.Contains(lower, "fable"):
			return "fable"
		case strings.Contains(lower, "mythos"):
			return "mythos"
		case strings.Contains(lower, "opus"):
			return "opus"
		case strings.Contains(lower, "sonnet"):
			return "sonnet"
		case strings.Contains(lower, "haiku"):
			return "haiku"
		}
	}
	return ""
}

func inputModalitiesForActualModel(upstream *config.UpstreamConfig, modelID string) []string {
	if actualModelSupportsImageInput(upstream, modelID) {
		return []string{"text", "image"}
	}
	return []string{"text"}
}

func inputModalitiesForRequestModel(upstream *config.UpstreamConfig, modelID string) []string {
	if requestModelSupportsImageInput(upstream, modelID) {
		return []string{"text", "image"}
	}
	return []string{"text"}
}

func requestModelSupportsImageInput(upstream *config.UpstreamConfig, modelID string) bool {
	if upstream == nil || upstream.NoVision {
		return false
	}

	actualModel := config.RedirectModel(modelID, upstream)
	if actualModelSupportsImageInput(upstream, actualModel) {
		return true
	}

	fallback := strings.TrimSpace(upstream.VisionFallbackModel)
	return fallback != "" && actualModelSupportsImageInput(upstream, fallback)
}

func actualModelSupportsImageInput(upstream *config.UpstreamConfig, modelID string) bool {
	if upstream == nil || upstream.NoVision {
		return false
	}

	for _, noVisionModel := range upstream.NoVisionModels {
		if noVisionModel == modelID {
			return false
		}
	}
	return true
}

func mergeInputModalities(a, b []string) []string {
	if hasInputModality(a, "image") || hasInputModality(b, "image") {
		return []string{"text", "image"}
	}
	if len(a) > 0 || len(b) > 0 {
		return []string{"text"}
	}
	return nil
}

func hasInputModality(modalities []string, modality string) bool {
	for _, item := range modalities {
		if item == modality {
			return true
		}
	}
	return false
}

// parseGeminiModelsResponse 解析 Gemini 格式的模型列表响应
func parseGeminiModelsResponse(body []byte) []ModelEntry {
	var geminiResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		log.Printf("[Gemini-Models] 解析响应失败: %v", err)
		return nil
	}

	entries := make([]ModelEntry, 0, len(geminiResp.Models))
	for _, m := range geminiResp.Models {
		id := m.Name
		if idx := strings.LastIndex(m.Name, "/"); idx >= 0 {
			id = m.Name[idx+1:]
		}
		entries = append(entries, ModelEntry{ID: id, Object: "model"})
	}
	return entries
}

// modelSortKey 返回模型的排序键，用于智能排序
// Claude 系列模型按能力排序，其他模型按字母序
func modelSortKey(id string) string {
	lowerID := strings.ToLower(id)

	// Claude 原生模型排序（按能力从高到低）
	claudeModels := map[string]string{
		"claude-fable-5":             "001-fable",
		"claude-mythos-5":            "002-mythos",
		"claude-opus-4-8":            "003-opus-4-8",
		"claude-opus-4-7":            "004-opus-4-7",
		"claude-opus-4-6":            "005-opus-4-6",
		"claude-sonnet-4-6":          "006-sonnet-4-6",
		"claude-haiku-4-5-20251001":  "007-haiku-4-5",
		"claude-3-5-sonnet-20241022": "008-sonnet-3-5",
		"claude-3-5-haiku-20241022":  "009-haiku-3-5",
		"claude-3-opus-20240229":     "010-opus-3",
		"claude-3-sonnet-20240229":   "011-sonnet-3",
		"claude-3-haiku-20240307":    "012-haiku-3",
	}
	if key, ok := claudeModels[lowerID]; ok {
		return key
	}

	// 通用 Claude tier 匹配（用于自定义名称）
	if strings.Contains(lowerID, "fable") {
		return "001-fable-" + lowerID
	}
	if strings.Contains(lowerID, "mythos") {
		return "002-mythos-" + lowerID
	}
	if strings.Contains(lowerID, "opus") {
		return "003-opus-" + lowerID
	}
	if strings.Contains(lowerID, "sonnet") {
		return "006-sonnet-" + lowerID
	}
	if strings.Contains(lowerID, "haiku") {
		return "007-haiku-" + lowerID
	}

	// Kimi 系列排序（按能力从高到低）
	kimiModels := map[string]string{
		"kimi-for-coding": "100-kimi-for-coding",
		"kimi-k2.7":       "101-kimi-k2.7",
		"kimi-k2.6":       "102-kimi-k2.6",
		"kimi-k2.5":       "103-kimi-k2.5",
		"kimi-k2":         "104-kimi-k2",
	}
	if key, ok := kimiModels[lowerID]; ok {
		return key
	}

	// DeepSeek 系列排序
	deepseekModels := map[string]string{
		"deepseek-v4-pro":   "200-deepseek-v4-pro",
		"deepseek-v4-flash": "201-deepseek-v4-flash",
		"deepseek-v3":       "202-deepseek-v3",
	}
	if key, ok := deepseekModels[lowerID]; ok {
		return key
	}

	// GLM 系列排序
	if strings.HasPrefix(lowerID, "glm-5") {
		return "300-glm-5-" + lowerID
	}
	if strings.HasPrefix(lowerID, "glm-4") {
		return "301-glm-4-" + lowerID
	}

	// MiMo 系列排序
	if strings.HasPrefix(lowerID, "mimo-v2.5-pro") {
		return "400-mimo-v2.5-pro"
	}
	if strings.HasPrefix(lowerID, "mimo-v2.5") {
		return "401-mimo-v2.5"
	}

	// GPT 系列排序
	gptModels := map[string]string{
		"gpt-4o":      "500-gpt-4o",
		"gpt-4-turbo": "501-gpt-4-turbo",
		"gpt-4":       "502-gpt-4",
		"gpt-3.5":     "503-gpt-3.5",
	}
	if key, ok := gptModels[lowerID]; ok {
		return key
	}

	// 其他模型按原始 ID 字母序
	return "999-" + lowerID
}

// mergeModels 合并多个模型列表并去重（按 ID），然后按智能规则排序
func mergeModels(modelLists ...[]ModelEntry) []ModelEntry {
	seen := make(map[string]int)
	var result []ModelEntry

	for _, models := range modelLists {
		for _, m := range models {
			if idx, exists := seen[m.ID]; exists {
				result[idx] = mergeModelEntryMetadata(result[idx], m)
			} else {
				seen[m.ID] = len(result)
				result = append(result, m)
			}
		}
	}

	// 按智能排序键排序
	sort.Slice(result, func(i, j int) bool {
		return modelSortKey(result[i].ID) < modelSortKey(result[j].ID)
	})

	return result
}

// tryModelsRequest 使用调度器选择渠道，按故障转移顺序尝试请求 models 端点
func tryModelsRequest(c *gin.Context, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, method, suffix string, kind scheduler.ChannelKind) ([]byte, *config.UpstreamConfig, bool) {
	failedChannels := make(map[int]bool)
	channelType := channelKindLabel(kind)

	for attempt := 0; attempt < modelsMaxAttempts; attempt++ {
		selection, err := channelScheduler.SelectChannel(c.Request.Context(), "", failedChannels, kind, "", c.Param("routePrefix"), c.GetHeader("X-Channel"))
		if err != nil {
			fallbackSelection, fallbackErr := selectChannelWithDisabledKeys(cfgManager, failedChannels, kind, c.Param("routePrefix"))
			if fallbackErr != nil {
				log.Printf("[%s-Models] 渠道无可用: %v", channelType, err)
				break
			}
			selection = fallbackSelection
			log.Printf("[%s-Models] 活跃渠道不可用，回退到挂起渠道查询模型: channel=%s, reason=%s", channelType, selection.Upstream.Name, selection.Reason)
		}

		body, upstream, ok := requestModelsFromSelection(c.Request.Context(), cfgManager, selection, method, suffix, kind)
		if ok {
			return body, upstream, true
		}
		failedChannels[selection.ChannelIndex] = true
	}

	log.Printf("[%s-Models] 所有渠道均失败: method=%s, suffix=%s", channelType, method, suffix)
	return nil, nil, false
}

func requestModelsFromSelection(ctx context.Context, cfgManager *config.ConfigManager, selection *scheduler.SelectionResult, method, suffix string, kind scheduler.ChannelKind) ([]byte, *config.UpstreamConfig, bool) {
	channelType := channelKindLabel(kind)
	upstream := selection.Upstream

	var candidateURLs []string
	var copilotRuntimeToken string
	if upstream.ServiceType == "copilot" {
		keyCandidates := selectModelsAPIKeyCandidates(cfgManager, upstream, channelType)
		if len(keyCandidates) == 0 {
			log.Printf("[Models-Copilot] 获取 API Key 失败: channel=%s, error=没有可用于模型探测的密钥", upstream.Name)
			return nil, upstream, false
		}
		proxyURL := strings.TrimSpace(upstream.ProxyURL)
		rt, baseURL, err := copilotTokenResolver(ctx, keyCandidates[0].apiKey, proxyURL)
		if err != nil {
			log.Printf("[Models-Copilot] Copilot token exchange 失败: channel=%s, error=%v", upstream.Name, err)
			return nil, upstream, false
		}
		copilotRuntimeToken = rt
		targetBaseURL := strings.TrimSuffix(strings.TrimSuffix(upstream.BaseURL, "#"), "/")
		if baseURL != "" {
			targetBaseURL = strings.TrimRight(baseURL, "/")
		}
		candidateURLs = []string{targetBaseURL + "/models" + suffix}
	} else if upstream.ServiceType == "gemini" || kind == scheduler.ChannelKindGemini {
		candidateURLs = []string{buildGeminiModelsURL(upstream.BaseURL) + suffix}
	} else if kind == scheduler.ChannelKindMessages {
		bases := buildClaudeCompatibleModelsURLs(upstream.BaseURL)
		candidateURLs = make([]string, len(bases))
		for i, b := range bases {
			candidateURLs[i] = b + suffix
		}
	} else {
		candidateURLs = []string{buildModelsURL(upstream.BaseURL) + suffix}
	}

	client := httpclient.GetManager().GetStandardClient(modelsRequestTimeout, upstream.InsecureSkipVerify, upstream.ProxyURL)

	keyCandidates := selectModelsAPIKeyCandidates(cfgManager, upstream, channelType)
	if len(keyCandidates) == 0 {
		log.Printf("[%s-Models] 获取 API Key 失败: channel=%s, error=没有可用于模型探测的密钥", channelType, upstream.Name)
		return nil, upstream, false
	}

	type requestResult struct {
		body []byte
		ok   bool
	}

	keyCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultCh := make(chan requestResult, len(keyCandidates))
	for _, candidate := range keyCandidates {
		candidate := candidate
		go func() {
			body, ok := requestModelsWithKey(keyCtx, client, candidateURLs, upstream, method, kind, selection.Reason, candidate, copilotRuntimeToken)
			resultCh <- requestResult{body: body, ok: ok}
		}()
	}

	for range keyCandidates {
		select {
		case result := <-resultCh:
			if result.ok {
				cancel()
				return result.body, upstream, true
			}
		case <-ctx.Done():
			return nil, upstream, false
		}
	}

	return nil, upstream, false
}

func selectModelsAPIKeyCandidates(cfgManager *config.ConfigManager, upstream *config.UpstreamConfig, apiType string) []modelsAPIKeyCandidate {
	if upstream == nil {
		return nil
	}

	seen := make(map[string]bool, modelsMaxKeysPerChannel)
	active := make([]modelsAPIKeyCandidate, 0, min(modelsMaxKeysPerChannel, len(upstream.APIKeys)))
	for _, candidate := range keypool.CandidatesForModel(upstream, nil, "") {
		key := strings.TrimSpace(candidate.APIKey)
		if key == "" || seen[key] || cfgManager.IsKeyFailed(key, apiType) {
			continue
		}
		seen[key] = true
		active = append(active, modelsAPIKeyCandidate{apiKey: key})
		if len(active) >= modelsMaxKeysPerChannel {
			return active
		}
	}
	if len(active) > 0 {
		return active
	}

	// 只要渠道仍配置了正常 APIKeys，就不借用已拉黑 key。
	for _, key := range upstream.APIKeys {
		if strings.TrimSpace(key) != "" {
			return nil
		}
	}

	disabled := make([]modelsAPIKeyCandidate, 0, min(modelsMaxKeysPerChannel, len(upstream.DisabledAPIKeys)))
	for _, disabledKey := range upstream.DisabledAPIKeys {
		key := strings.TrimSpace(disabledKey.Key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		disabled = append(disabled, modelsAPIKeyCandidate{apiKey: key, disabledFallback: true})
		if len(disabled) >= modelsMaxKeysPerChannel {
			break
		}
	}
	return disabled
}

func requestModelsWithKey(ctx context.Context, client *http.Client, candidateURLs []string, upstream *config.UpstreamConfig, method string, kind scheduler.ChannelKind, reason string, candidate modelsAPIKeyCandidate, copilotRuntimeToken string) ([]byte, bool) {
	channelType := channelKindLabel(kind)
	apiKey := candidate.apiKey
	if candidate.disabledFallback {
		log.Printf("[%s-Models] 使用已拉黑密钥查询模型列表: channel=%s, key=%s", channelType, upstream.Name, utils.MaskAPIKey(apiKey))
	}

	for _, candidateURL := range candidateURLs {
		req, err := http.NewRequestWithContext(ctx, method, candidateURL, nil)
		if err != nil {
			log.Printf("[%s-Models] 创建请求失败: channel=%s, url=%s, error=%v", channelType, upstream.Name, candidateURL, err)
			continue
		}
		if upstream.ServiceType == "copilot" {
			copilot.ApplyRuntimeHeaders(req.Header, copilotRuntimeToken)
		} else if (upstream.ServiceType == "gemini" || kind == scheduler.ChannelKindGemini) && !utils.HasAuthenticationHeaderOverride(upstream.AuthHeader) {
			utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
		} else {
			utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
		}
		req.Header.Set("Content-Type", "application/json")
		if upstream.ServiceType == "copilot" {
			utils.ApplyCustomHeadersProtected(req.Header, upstream.CustomHeaders, utils.CopilotProtectedHeaders)
		} else {
			utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[%s-Models] 请求失败: channel=%s, key=%s, url=%s, error=%v",
				channelType, upstream.Name, utils.MaskAPIKey(apiKey), candidateURL, err)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				log.Printf("[%s-Models] 读取响应失败: channel=%s, error=%v", channelType, upstream.Name, err)
				continue
			}
			log.Printf("[%s-Models] 请求成功: method=%s, channel=%s, key=%s, url=%s, reason=%s",
				channelType, method, upstream.Name, utils.MaskAPIKey(apiKey), candidateURL, reason)
			return body, true
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			log.Printf("[%s-Models] 上游认证失败: channel=%s, key=%s, status=%d, url=%s",
				channelType, upstream.Name, utils.MaskAPIKey(apiKey), resp.StatusCode, candidateURL)
			resp.Body.Close()
			break
		}

		log.Printf("[%s-Models] 上游返回非 200: channel=%s, key=%s, status=%d, url=%s",
			channelType, upstream.Name, utils.MaskAPIKey(apiKey), resp.StatusCode, candidateURL)
		resp.Body.Close()
	}

	return nil, false
}

func channelKindLabel(kind scheduler.ChannelKind) string {
	switch kind {
	case scheduler.ChannelKindResponses:
		return "Responses"
	case scheduler.ChannelKindChat:
		return "Chat"
	case scheduler.ChannelKindGemini:
		return "Gemini"
	case scheduler.ChannelKindImages:
		return "Images"
	case scheduler.ChannelKindVectors:
		return "Vectors"
	default:
		return "Messages"
	}
}

func selectChannelWithDisabledKeys(cfgManager *config.ConfigManager, failedChannels map[int]bool, kind scheduler.ChannelKind, routePrefix string) (*scheduler.SelectionResult, error) {
	cfg := cfgManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case scheduler.ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case scheduler.ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case scheduler.ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case scheduler.ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	case scheduler.ChannelKindVectors:
		upstreams = cfg.VectorsUpstream
	default:
		upstreams = cfg.Upstream
	}

	type candidate struct {
		index    int
		upstream config.UpstreamConfig
		priority int
	}

	candidates := make([]candidate, 0)
	for i, upstream := range upstreams {
		if failedChannels[i] {
			continue
		}
		if config.GetChannelStatus(&upstream) == "disabled" {
			continue
		}
		if len(upstream.APIKeys) > 0 || len(upstream.DisabledAPIKeys) == 0 {
			continue
		}
		if routePrefix != "" {
			if upstream.RoutePrefix != routePrefix {
				continue
			}
		} else if upstream.RoutePrefix != "" {
			continue
		}
		candidates = append(candidates, candidate{
			index:    i,
			upstream: upstream,
			priority: config.GetChannelPriority(&upstream, i),
		})
	}

	if len(candidates) == 0 {
		return nil, errNoChannelWithDisabledKeys
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].priority < candidates[j].priority
	})

	selected := candidates[0]
	upstreamCopy := selected.upstream
	return &scheduler.SelectionResult{
		Upstream:     &upstreamCopy,
		ChannelIndex: selected.index,
		Reason:       "disabled_key_fallback",
	}, nil
}

// buildModelsURL 构建 models 端点的 URL
func buildModelsURL(baseURL string) string {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	versionPattern := regexp.MustCompile(`/v\d+[a-z]*$`)
	hasVersionSuffix := versionPattern.MatchString(baseURL)

	endpoint := "/models"
	if !hasVersionSuffix && !skipVersionPrefix {
		endpoint = "/v1" + endpoint
	}

	return baseURL + endpoint
}

// buildGeminiModelsURL 构建 Gemini models 端点的 URL（使用 v1beta 前缀）
func buildGeminiModelsURL(baseURL string) string {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	versionPattern := regexp.MustCompile(`/v\d+[a-z]*$`)
	hasVersionSuffix := versionPattern.MatchString(baseURL)

	endpoint := "/models"
	if !hasVersionSuffix && !skipVersionPrefix {
		endpoint = "/v1beta" + endpoint
	}

	return baseURL + endpoint
}

// claudeCompatProtocolSuffixes 是 Claude/Messages 兼容协议常见的路径尾段
var claudeCompatProtocolSuffixes = []string{"anthropic", "claude", "messages"}

// buildClaudeCompatibleModelsURLs 为 messages/claude 渠道构建候选模型列表 URL（去重）
// 顺序：1) 当前逻辑 2) 剔除协议尾段后 3) 纯域名根路径
func buildClaudeCompatibleModelsURLs(baseURL string) []string {
	candidates := make([]string, 0, 3)
	seen := make(map[string]bool, 3)

	add := func(u string) {
		if u != "" && !seen[u] {
			seen[u] = true
			candidates = append(candidates, u)
		}
	}

	// 第一次：当前逻辑
	add(buildModelsURL(baseURL))

	// 规范化：去掉 # 和尾部 /
	normalized := strings.TrimSuffix(baseURL, "#")
	normalized = strings.TrimSuffix(normalized, "/")

	// 剥离尾部版本段
	versionPattern := regexp.MustCompile(`/v\d+[a-z]*$`)
	stripped := versionPattern.ReplaceAllString(normalized, "")

	// 第二次：如果最后一段是已知协议前缀，剔除后构建
	lastSlash := strings.LastIndex(stripped, "/")
	if lastSlash > 0 {
		lastSeg := strings.ToLower(stripped[lastSlash+1:])
		for _, suffix := range claudeCompatProtocolSuffixes {
			if lastSeg == suffix {
				strippedBase := stripped[:lastSlash]
				add(buildModelsURL(strippedBase))

				// 第三次：如果剔除后仍不是纯域名，用纯域名
				parsed, err := url.Parse(strippedBase)
				if err == nil && parsed.Path != "" && parsed.Path != "/" {
					origin := parsed.Scheme + "://" + parsed.Host
					add(buildModelsURL(origin))
				}
				break
			}
		}
	}

	return candidates
}
