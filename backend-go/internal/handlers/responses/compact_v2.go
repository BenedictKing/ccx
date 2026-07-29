package responses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

func hasCompactionTrigger(input interface{}) bool {
	items, err := types.ParseResponsesInput(input)
	if err != nil {
		return false
	}
	for _, item := range items {
		if item.Type == "compaction_trigger" {
			return true
		}
	}
	return false
}

func tryLocalCompactV2WithAllKeys(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	channelIndex int,
	requestModel string,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	channelLogStore *metrics.ChannelLogStore,
	bodyBytes []byte,
	envCfg *config.EnvConfig,
	sessionManager *session.SessionManager,
) (bool, string, *compactError) {
	if upstream == nil || len(upstream.APIKeys) == 0 {
		return false, "", &compactError{status: http.StatusServiceUnavailable, body: []byte(`{"error":"当前渠道未配置 API 密钥"}`), shouldFailover: true}
	}

	metricsServiceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindResponses, upstream.ServiceType)
	failedKeys := make(map[string]bool)
	var lastErr *compactError

	for attempt := 0; attempt < len(upstream.APIKeys); attempt++ {
		apiKey, err := cfgManager.GetNextResponsesAPIKey(upstream, failedKeys)
		if err != nil {
			lastErr = &compactError{status: http.StatusServiceUnavailable, body: []byte(`{"error":"所有 API 密钥都不可用"}`), shouldFailover: true, err: err}
			break
		}

		attemptStart := time.Now()
		success, compactErr := tryLocalCompactV2WithKey(c, upstream, apiKey, bodyBytes, envCfg, cfgManager, sessionManager)
		metricsKey := metrics.GenerateMetricsIdentityKey(upstream.BaseURL, apiKey, metricsServiceType)
		if success {
			common.RecordChannelLog(channelLogStore, metricsKey, channelIndex, requestModel, "", http.StatusOK, time.Since(attemptStart).Milliseconds(), true, apiKey, upstream.BaseURL, "", "Responses", attempt > 0, upstream.Name)
			channelScheduler.RecordSuccessWithUsage(upstream.BaseURL, apiKey, metricsServiceType, nil, scheduler.ChannelKindResponses)
			return true, apiKey, nil
		}

		if compactErr != nil {
			lastErr = compactErr
			common.RecordChannelLog(channelLogStore, metricsKey, channelIndex, requestModel, "", compactErr.status, time.Since(attemptStart).Milliseconds(), false, apiKey, upstream.BaseURL, compactErr.errorInfo(), "Responses", attempt > 0, upstream.Name)
			if compactErr.shouldFailover {
				failedKeys[apiKey] = true
				cfgManager.MarkKeyAsFailed(apiKey, "Responses")
				channelScheduler.RecordFailure(upstream.BaseURL, apiKey, metricsServiceType, scheduler.ChannelKindResponses)
				continue
			}
			c.Data(compactErr.status, "application/json", compactErr.body)
			return true, "", nil
		}
	}

	return false, "", lastErr
}

func tryLocalCompactV2WithKey(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	apiKey string,
	bodyBytes []byte,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
) (bool, *compactError) {
	var originalReq types.ResponsesRequest
	if err := json.Unmarshal(bodyBytes, &originalReq); err != nil {
		return false, &compactError{status: 400, body: []byte(`{"error":"解析 compact v2 请求失败"}`), shouldFailover: false, err: err}
	}

	common.RequestLogf(c, "[Compact-V2-Local] 标准 Responses compaction_trigger 使用本地 compact: serviceType=%s model=%s stream=%v", upstream.ServiceType, originalReq.Model, originalReq.Stream)

	templateStore := getTaskTemplateStoreFromContext(c)
	localBody, err := buildLocalCompactRequestBodyWithLogTag(bodyBytes, originalReq.Stream, sessionManager, common.RequestLogTag(c), upstream, templateStore)
	if err != nil {
		return false, &compactError{status: 400, body: []byte(fmt.Sprintf(`{"error":"%s"}`, err.Error())), shouldFailover: false, err: err}
	}

	upstreamForCompact := upstream
	if upstream.CompactModel != "" {
		upstreamCopy := *upstream
		upstreamCopy.ModelMapping = nil
		upstreamForCompact = &upstreamCopy
	}

	provider := &providers.ResponsesProvider{SessionManager: sessionManager}
	req, _, err := provider.ConvertBodyToProviderRequest(c, upstreamForCompact, apiKey, localBody, "/v1/responses")
	if err != nil {
		return false, &compactError{status: 500, body: []byte(`{"error":"构建本地 compact v2 上游请求失败"}`), shouldFailover: true, err: err}
	}
	req = common.WithRequestLogContext(req, c)

	resp, err := common.SendRequest(req, upstream, envCfg, originalReq.Stream, "Responses")
	if err != nil {
		return false, &compactError{status: 502, body: []byte(`{"error":"本地 compact v2 上游请求失败"}`), shouldFailover: true, err: err}
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		respBody = utils.DecompressGzipIfNeeded(resp, respBody)
		shouldFailover, _ := common.ShouldRetryWithNextKeyWithLogTag(resp.StatusCode, respBody, "Responses", common.RequestLogTag(c))
		return false, &compactError{status: resp.StatusCode, body: respBody, shouldFailover: shouldFailover}
	}

	if originalReq.Stream {
		return handleLocalCompactV2Stream(c, resp, upstream.ServiceType, originalReq, sessionManager)
	}
	return handleLocalCompactV2NonStream(c, resp, upstream.ServiceType, originalReq, sessionManager)
}

func buildCompactionV2SSEEvents(resp *types.ResponsesResponse) []string {
	itemPayload := map[string]interface{}{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item":         resp.Output[0],
	}
	completedPayload := map[string]interface{}{
		"type": "response.completed",
		"response": map[string]interface{}{
			"id":         resp.ID,
			"object":     "response",
			"created_at": time.Now().Unix(),
			"status":     resp.Status,
			"model":      resp.Model,
			"output":     resp.Output,
			"usage":      resp.Usage,
		},
	}
	if resp.PreviousID != "" {
		completedPayload["response"].(map[string]interface{})["previous_id"] = resp.PreviousID
	}

	return []string{
		formatResponseSSE("response.output_item.done", itemPayload),
		formatResponseSSE("response.completed", completedPayload),
	}
}

func formatResponseSSE(event string, payload map[string]interface{}) string {
	data, _ := utils.MarshalJSONNoEscape(payload)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
}

func responsesUsageFromStreamUsage(usage responsesStreamUsage) types.ResponsesUsage {
	result := types.ResponsesUsage{
		InputTokens:                usage.InputTokens,
		OutputTokens:               usage.OutputTokens,
		TotalTokens:                usage.TotalTokens,
		CacheCreationInputTokens:   usage.CacheCreationInputTokens,
		CacheReadInputTokens:       usage.CacheReadInputTokens,
		CacheCreation5mInputTokens: usage.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: usage.CacheCreation1hInputTokens,
		CacheTTL:                   usage.CacheTTL,
	}
	if usage.CacheReadInputTokens > 0 && !usage.HasClaudeCache {
		result.InputTokensDetails = &types.InputTokensDetails{CachedTokens: usage.CacheReadInputTokens}
	}
	return result
}

func mustMarshalResponsesRequest(req types.ResponsesRequest) []byte {
	body, err := json.Marshal(req)
	if err != nil {
		return nil
	}
	return bytes.TrimSpace(body)
}
