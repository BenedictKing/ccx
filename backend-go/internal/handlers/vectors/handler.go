package vectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const embeddingsOperation = "embeddings"

func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}
		if channelScheduler == nil {
			vectorsErrorResponse(c, http.StatusServiceUnavailable, "Vectors scheduler unavailable", "service_unavailable", "service_unavailable")
			return
		}

		startTime := time.Now()
		bodyBytes, err := common.ReadRequestBody(c, envCfg.MaxRequestBodySize)
		if err != nil {
			return
		}
		c.Set("requestBodyBytes", bodyBytes)

		_, model, dimensions, ok := parseEmbeddingsRequest(c, bodyBytes)
		if !ok {
			return
		}

		userID := utils.ExtractUnifiedSessionID(c, bodyBytes)
		agentCtx := utils.ExtractAgentContext(c, bodyBytes)
		c.Set("agentContext", agentCtx)
		common.SetRequestLogContextWithAgent(c, userID, 0, agentCtx)
		common.LogOriginalRequest(c, bodyBytes, envCfg, "Vectors")

		handleVectorsFailover(c, envCfg, cfgManager, channelScheduler, bodyBytes, model, dimensions, userID, startTime)
	})
}

func parseEmbeddingsRequest(c *gin.Context, bodyBytes []byte) (map[string]interface{}, string, int, bool) {
	var reqMap map[string]interface{}
	if len(bodyBytes) == 0 {
		vectorsErrorResponse(c, http.StatusBadRequest, "request body is required", "invalid_request_error", "missing_body")
		return nil, "", 0, false
	}
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&reqMap); err != nil {
		vectorsErrorResponse(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err), "invalid_request_error", "invalid_json")
		return nil, "", 0, false
	}
	if reqMap == nil {
		vectorsErrorResponse(c, http.StatusBadRequest, "request body must be a JSON object", "invalid_request_error", "invalid_json")
		return nil, "", 0, false
	}

	model, _ := reqMap["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		vectorsErrorResponse(c, http.StatusBadRequest, "model is required", "invalid_request_error", "missing_parameter")
		return nil, "", 0, false
	}
	input, exists := reqMap["input"]
	if !exists || !validEmbeddingsInput(input) {
		vectorsErrorResponse(c, http.StatusBadRequest, "input is required", "invalid_request_error", "missing_parameter")
		return nil, "", 0, false
	}
	dimensions, ok := parseEmbeddingsDimensions(c, reqMap)
	if !ok {
		return nil, "", 0, false
	}
	return reqMap, model, dimensions, true
}

func parseEmbeddingsDimensions(c *gin.Context, reqMap map[string]interface{}) (int, bool) {
	raw, exists := reqMap["dimensions"]
	if !exists || raw == nil {
		return 0, true
	}
	number, ok := raw.(json.Number)
	if !ok {
		vectorsErrorResponse(c, http.StatusBadRequest, "dimensions must be a positive integer", "invalid_request_error", "invalid_parameter")
		return 0, false
	}
	value, err := number.Int64()
	maxInt := int64(^uint(0) >> 1)
	if err != nil || value <= 0 || value > maxInt || strconv.FormatInt(value, 10) != number.String() {
		vectorsErrorResponse(c, http.StatusBadRequest, "dimensions must be a positive integer", "invalid_request_error", "invalid_parameter")
		return 0, false
	}
	return int(value), true
}

func validEmbeddingsInput(input interface{}) bool {
	switch v := input.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case []interface{}:
		return len(v) > 0
	default:
		return true
	}
}

func handleVectorsFailover(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	model string,
	dimensions int,
	userID string,
	startTime time.Time,
) {
	metricsManager := channelScheduler.GetVectorsMetricsManager()
	agentRole := ""
	if ac := common.AgentContextFromGin(c); ac != nil {
		agentRole = ac.AgentRole
	}
	common.HandleMultiChannelFailoverWithSelectionFilter(
		c,
		envCfg,
		channelScheduler,
		scheduler.ChannelKindVectors,
		"Vectors",
		userID,
		model,
		nil,
		agentRole,
		newEmbeddingCompatibilityFilter(c, model, dimensions),
		func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			upstream := selection.Upstream
			channelIndex := selection.ChannelIndex
			if upstream == nil {
				return common.MultiChannelAttemptResult{}
			}

			baseURLs := upstream.GetAllBaseURLs()
			sortedURLResults := channelScheduler.GetSortedURLsForChannel(scheduler.ChannelKindVectors, channelIndex, baseURLs)
			handled, successKey, successBaseURLIdx, failoverErr, usage, lastErr := common.TryUpstreamWithAllKeys(
				c,
				envCfg,
				cfgManager,
				channelScheduler,
				scheduler.ChannelKindVectors,
				"Vectors",
				metricsManager,
				upstream,
				sortedURLResults,
				bodyBytes,
				nil,
				false,
				func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
					return cfgManager.GetNextVectorsAPIKey(upstream, failedKeys)
				},
				func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
					return buildProviderRequest(c, upstreamCopy, upstreamCopy.BaseURL, apiKey, common.GetEffectiveRequestBody(c, bodyBytes), model)
				},
				func(apiKey string) {
					_ = cfgManager.DeprioritizeAPIKey(apiKey)
				},
				func(url string) {
					channelScheduler.MarkURLFailure(scheduler.ChannelKindVectors, channelIndex, url)
				},
				func(url string) {
					channelScheduler.MarkURLSuccess(scheduler.ChannelKindVectors, channelIndex, url)
				},
				func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
					return handleSuccess(c, resp, envCfg, startTime)
				},
				model,
				embeddingsOperation,
				selection.ChannelIndex,
				channelScheduler.GetChannelLogStore(scheduler.ChannelKindVectors),
			)

			return common.MultiChannelAttemptResult{
				Handled:           handled,
				Attempted:         true,
				SuccessKey:        successKey,
				SuccessBaseURLIdx: successBaseURLIdx,
				FailoverError:     failoverErr,
				Usage:             usage,
				LastError:         lastErr,
			}
		},
		nil,
		func(ctx *gin.Context, failoverErr *common.FailoverError, lastError error) {
			handleAllChannelsFailed(ctx, failoverErr, lastError)
		},
	)
}

func buildProviderRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	bodyBytes []byte,
	model string,
) (*http.Request, error) {
	serviceType, err := config.NormalizeVectorsServiceTypeForProxy(upstream.ServiceType)
	if err != nil {
		common.RequestLogf(c, "[Vectors-BuildRequest] base_url=%q key_mask=%s model=%q stage=normalize_service_type reason=invalid_service_type error=%q",
			baseURL, utils.MaskAPIKey(apiKey), model, sanitizeDiagnosticError(err))
		return nil, err
	}
	upstream.ServiceType = serviceType

	redirectedModel, mappingMatched := config.RedirectModelWithMatch(model, upstream)
	requestBody, err := buildEmbeddingsRequestBody(bodyBytes, model, redirectedModel)
	if err != nil {
		common.RequestLogf(c, "[Vectors-BuildRequest] base_url=%q key_mask=%s model=%q stage=build_json reason=invalid_json error=%q",
			baseURL, utils.MaskAPIKey(apiKey), model, sanitizeDiagnosticError(err))
		return nil, err
	}

	endpointURL := buildEmbeddingsURL(baseURL)
	common.RequestLogf(c, "[Vectors-Mapping] channel=%q original_model=%q mapped_model=%q upstream_body_model=%q base_host=%q mapping_hit=%t",
		upstream.Name, model, redirectedModel, extractEmbeddingsBodyModel(requestBody), safeURLHost(endpointURL), mappingMatched)

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, endpointURL, bytes.NewReader(requestBody))
	if err != nil {
		common.RequestLogf(c, "[Vectors-BuildRequest] base_url=%q key_mask=%s model=%q stage=new_request reason=request_init_failed error=%q",
			baseURL, utils.MaskAPIKey(apiKey), model, sanitizeDiagnosticError(err))
		return nil, err
	}
	if c.Request.URL != nil {
		req.URL.RawQuery = c.Request.URL.RawQuery
	}
	req.Header = prepareVectorsUpstreamHeaders(c, req.URL.Host)
	utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
	utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)
	return req, nil
}

func buildEmbeddingsRequestBody(bodyBytes []byte, model string, redirectedModel string) ([]byte, error) {
	var reqMap map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&reqMap); err != nil {
		return nil, err
	}
	if reqMap == nil {
		reqMap = make(map[string]interface{})
	}
	if strings.TrimSpace(model) != "" && strings.TrimSpace(redirectedModel) != "" {
		reqMap["model"] = redirectedModel
	}
	return utils.MarshalJSONNoEscape(reqMap)
}

func extractEmbeddingsBodyModel(bodyBytes []byte) string {
	var reqMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
		return ""
	}
	model, _ := reqMap["model"].(string)
	return strings.TrimSpace(model)
}

func safeURLHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func buildEmbeddingsURL(baseURL string) string {
	return buildEndpointURL(baseURL, "/v1", "/embeddings")
}

func prepareVectorsUpstreamHeaders(c *gin.Context, targetHost string) http.Header {
	headers := utils.PrepareUpstreamHeaders(c, targetHost)
	headers.Set("Content-Type", "application/json")
	return headers
}

func handleSuccess(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, startTime time.Time) (*types.Usage, error) {
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		vectorsErrorResponse(c, http.StatusInternalServerError, "Failed to read response", "server_error", "server_error")
		return nil, err
	}
	if len(bodyBytes) == 0 {
		return nil, common.ErrEmptyNonStreamResponse
	}
	if err := validateEmbeddingsResponse(bodyBytes); err != nil {
		return nil, err
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		common.RequestLogf(c, "[Vectors-Timing] response completed: %dms, status: %d", responseTime, resp.StatusCode)
		common.LogUpstreamResponse(c, resp, bodyBytes, envCfg, "Vectors")
	}

	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	c.Status(resp.StatusCode)
	if _, err := c.Writer.Write(bodyBytes); err != nil {
		return nil, err
	}
	return extractEmbeddingsUsage(bodyBytes), nil
}

func validateEmbeddingsResponse(bodyBytes []byte) error {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return fmt.Errorf("%w: invalid embeddings JSON: %v", common.ErrInvalidResponseBody, err)
	}
	if payload == nil {
		return fmt.Errorf("%w: embeddings response must be a JSON object", common.ErrInvalidResponseBody)
	}

	dataRaw, ok := payload["data"]
	if !ok {
		return fmt.Errorf("%w: embeddings response missing data", common.ErrInvalidResponseBody)
	}
	dataRaw = bytes.TrimSpace(dataRaw)
	if len(dataRaw) == 0 || dataRaw[0] != '[' {
		return fmt.Errorf("%w: embeddings response data must be an array", common.ErrInvalidResponseBody)
	}

	var data []map[string]json.RawMessage
	if err := json.Unmarshal(dataRaw, &data); err != nil {
		return fmt.Errorf("%w: invalid embeddings data: %v", common.ErrInvalidResponseBody, err)
	}
	for i, item := range data {
		embeddingRaw, ok := item["embedding"]
		if !ok {
			return fmt.Errorf("%w: embeddings data[%d] missing embedding", common.ErrInvalidResponseBody, i)
		}
		embeddingRaw = bytes.TrimSpace(embeddingRaw)
		if len(embeddingRaw) == 0 || embeddingRaw[0] != '[' {
			return fmt.Errorf("%w: embeddings data[%d].embedding must be an array", common.ErrInvalidResponseBody, i)
		}
	}
	return nil
}

func extractEmbeddingsUsage(bodyBytes []byte) *types.Usage {
	usageMap := gjson.GetBytes(bodyBytes, "usage")
	if !usageMap.Exists() || !usageMap.IsObject() {
		return nil
	}

	inputTokens := int(usageMap.Get("prompt_tokens").Int())
	if inputTokens == 0 {
		inputTokens = int(usageMap.Get("total_tokens").Int())
	}
	if inputTokens == 0 {
		return nil
	}
	return &types.Usage{
		InputTokens:       inputTokens,
		OutputTokens:      0,
		PromptTokens:      inputTokens,
		PromptTokensTotal: inputTokens,
		CompletionTokens:  0,
	}
}

func sanitizeDiagnosticError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

func vectorsErrorResponse(c *gin.Context, statusCode int, message, errorType, code string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errorType,
			"code":    code,
		},
	})
}

func handleAllChannelsFailed(c *gin.Context, failoverErr *common.FailoverError, lastError error) {
	if failoverErr != nil {
		c.Data(failoverErr.Status, "application/json", failoverErr.Body)
		return
	}
	errMsg := "All Vectors channels failed"
	if lastError != nil {
		errMsg = lastError.Error()
	}
	vectorsErrorResponse(c, http.StatusServiceUnavailable, errMsg, "service_unavailable", "service_unavailable")
}
