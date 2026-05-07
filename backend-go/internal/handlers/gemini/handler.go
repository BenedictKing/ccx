// Package gemini 提供 Gemini API 的处理器
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/forwarding"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/handlers/wire"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/pipeline"
	pipelinemw "github.com/BenedictKing/ccx/internal/pipeline/middleware"
	"github.com/BenedictKing/ccx/internal/pricing"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/usage"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// Handler Gemini API 代理处理器
// 支持多渠道调度：当配置多个渠道时自动启用
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		// Gemini 代理端点统一使用代理访问密钥鉴权（x-api-key / Authorization: Bearer）
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		startTime := time.Now()

		// 读取原始请求体
		maxBodySize := envCfg.MaxRequestBodySize
		bodyBytes, err := common.ReadRequestBody(c, maxBodySize)
		if err != nil {
			return
		}
		c.Set("requestBodyBytes", bodyBytes)

		// 解析 Gemini 请求
		var geminiReq types.GeminiRequest
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &geminiReq); err != nil {
				c.JSON(400, types.GeminiError{
					Error: types.GeminiErrorDetail{
						Code:    400,
						Message: fmt.Sprintf("Invalid request body: %v", err),
						Status:  "INVALID_ARGUMENT",
					},
				})
				return
			}
		}

		// 从 URL 路径提取模型名称
		// 格式: /v1/models/{model}:generateContent 或 /v1/models/{model}:streamGenerateContent
		// 使用 *modelAction 通配符捕获整个后缀，如 /gemini-pro:generateContent
		modelAction := c.Param("modelAction")
		// 移除前导斜杠（Gin 的 * 通配符会保留前导斜杠）
		modelAction = strings.TrimPrefix(modelAction, "/")
		model := extractModelName(modelAction)
		if model == "" {
			c.JSON(400, types.GeminiError{
				Error: types.GeminiErrorDetail{
					Code:    400,
					Message: "Model name is required in URL path",
					Status:  "INVALID_ARGUMENT",
				},
			})
			return
		}

		// 判断是否流式
		isStream := strings.Contains(c.Request.URL.Path, "streamGenerateContent")

		// 提取统一会话标识用于 Trace 亲和性
		userID := utils.ExtractUnifiedSessionID(c, bodyBytes)

		// 记录原始请求信息
		common.LogOriginalRequest(c, bodyBytes, envCfg, "Gemini")

		// 检查是否为多渠道模式
		isMultiChannel := channelScheduler.IsMultiChannelMode(scheduler.ChannelKindGemini)

		if isMultiChannel {
			handleMultiChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, &geminiReq, model, isStream, userID, startTime)
		} else {
			handleSingleChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, &geminiReq, model, isStream, startTime)
		}
	})
}

// extractModelName 从 URL 参数提取模型名称
// 输入: "gemini-2.0-flash:generateContent" 或 "gemini-2.0-flash"
// 输出: "gemini-2.0-flash"
func extractModelName(param string) string {
	if param == "" {
		return ""
	}
	// 移除 :generateContent 或 :streamGenerateContent 后缀
	if idx := strings.Index(param, ":"); idx > 0 {
		return param[:idx]
	}
	return param
}

// handleMultiChannel 处理多渠道 Gemini 请求
//
// PR3 T8d：路由到 pipeline.Process（wire.LBOutboundAdapter 内部驱动渠道选择 +
// failover），不再走 TryUpstreamWithAllKeys。
func handleMultiChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	geminiReq *types.GeminiRequest,
	model string,
	isStream bool,
	userID string,
	startTime time.Time,
) {
	processViaPipeline(c, envCfg, cfgManager, channelScheduler, geminiReq, bodyBytes, userID, model, isStream, "", startTime)
}

// handleSingleChannel 处理单渠道 Gemini 请求
//
// PR3 T8d：路由到 pipeline.Process。空渠道 / 空 key 的早退保留在此处，避免
// pipeline 主循环再做一次同等校验。
func handleSingleChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	geminiReq *types.GeminiRequest,
	model string,
	isStream bool,
	startTime time.Time,
) {
	upstream, _, err := cfgManager.GetCurrentGeminiUpstreamWithIndex()
	if err != nil {
		c.JSON(503, types.GeminiError{
			Error: types.GeminiErrorDetail{
				Code:    503,
				Message: "No Gemini upstream configured",
				Status:  "UNAVAILABLE",
			},
		})
		return
	}

	if len(upstream.APIKeys) == 0 {
		c.JSON(503, types.GeminiError{
			Error: types.GeminiErrorDetail{
				Code:    503,
				Message: fmt.Sprintf("No API keys configured for upstream \"%s\"", upstream.Name),
				Status:  "UNAVAILABLE",
			},
		})
		return
	}

	processViaPipeline(c, envCfg, cfgManager, channelScheduler, geminiReq, bodyBytes, "", model, isStream, "", startTime)
}

// ensureThoughtSignatures 确保所有 functionCall 都有 thought_signature 字段
// 用于兼容 x666.me 等要求必须有该字段的第三方 API
// 参考: https://ai.google.dev/gemini-api/docs/thought-signatures
//
// 行为：
//   - 如果 functionCall 已有 thought_signature（非空），保留原始值
//   - 如果 functionCall 没有 thought_signature（空字符串），填充 DummyThoughtSignature
//
// 使用场景：
//   - x666.me 等第三方 API 会验证 thought_signature 字段必须存在
//   - Gemini CLI 等客户端可能不会为所有 functionCall 提供 thought_signature
func ensureThoughtSignatures(geminiReq *types.GeminiRequest) {
	for i := range geminiReq.Contents {
		for j := range geminiReq.Contents[i].Parts {
			part := &geminiReq.Contents[i].Parts[j]
			if part.FunctionCall != nil && part.FunctionCall.ThoughtSignature == "" {
				part.FunctionCall.ThoughtSignature = types.DummyThoughtSignature
			}
		}
	}
}

// stripThoughtSignature 移除所有 functionCall 的 thought_signature 字段
// 用于兼容旧版 Gemini API（不支持该字段）
func stripThoughtSignature(geminiReq *types.GeminiRequest) {
	for i := range geminiReq.Contents {
		for j := range geminiReq.Contents[i].Parts {
			part := &geminiReq.Contents[i].Parts[j]
			if part.FunctionCall != nil {
				// 使用特殊标记表示需要完全移除字段
				part.FunctionCall.ThoughtSignature = types.StripThoughtSignatureMarker
			}
		}
	}
}

// cloneGeminiRequest 深拷贝 GeminiRequest（通过 JSON 序列化/反序列化）
func cloneGeminiRequest(req *types.GeminiRequest) *types.GeminiRequest {
	clone := &types.GeminiRequest{}
	data, _ := json.Marshal(req)
	_ = json.Unmarshal(data, clone)
	return clone
}

// buildProviderRequest 构建上游请求
func buildProviderRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	geminiReq *types.GeminiRequest,
	model string,
	isStream bool,
) (*http.Request, error) {
	// 应用模型映射
	mappedModel := config.RedirectModel(model, upstream)

	var requestBody []byte
	var url string
	var err error

	switch upstream.ServiceType {
	case "gemini":
		requestBody, err = buildGeminiNativeRequestBody(c, geminiReq, upstream)
		if err != nil {
			return nil, err
		}

		url = forwarding.BuildGeminiNativeURL(baseURL, mappedModel, isStream)
		prepared, err := forwarding.Build(c, forwarding.ForwardingRequest{
			Method:        http.MethodPost,
			URL:           url,
			Body:          requestBody,
			ServiceType:   upstream.ServiceType,
			CustomHeaders: upstream.CustomHeaders,
			AuthKind:      forwarding.AuthKindGemini,
			APIKey:        apiKey,
			RawResponse:   true,
			RawStream:     isStream,
		})
		if err != nil {
			return nil, err
		}
		return prepared.Request, nil

	case "claude":
		// Claude 上游：需要转换
		claudeReq, err := converters.GeminiToClaudeRequest(geminiReq, mappedModel)
		if err != nil {
			return nil, err
		}
		claudeReq["stream"] = isStream
		requestBody, err = json.Marshal(claudeReq)
		if err != nil {
			return nil, err
		}
		url = forwarding.BuildEndpointURL(baseURL, "/v1", "/messages")

	case "openai":
		// OpenAI 上游：需要转换
		openaiReq, err := converters.GeminiToOpenAIRequest(geminiReq, mappedModel)
		if err != nil {
			return nil, err
		}
		openaiReq["stream"] = isStream
		requestBody, err = json.Marshal(openaiReq)
		if err != nil {
			return nil, err
		}
		url = forwarding.BuildEndpointURL(baseURL, "/v1", "/chat/completions")

	case "responses":
		// Responses 上游：需要转换
		responsesReq, err := converters.GeminiToResponsesRequest(geminiReq, mappedModel)
		if err != nil {
			return nil, err
		}
		responsesReq["stream"] = isStream
		requestBody, err = json.Marshal(responsesReq)
		if err != nil {
			return nil, err
		}
		url = forwarding.BuildEndpointURL(baseURL, "/v1", "/responses")

	default:
		// 默认当作 Gemini 处理，根据配置处理 thought_signature 字段
		reqToUse := geminiReq

		// 优先处理 StripThoughtSignature（移除字段）
		if upstream.StripThoughtSignature {
			reqCopy := cloneGeminiRequest(geminiReq)
			stripThoughtSignature(reqCopy)
			reqToUse = reqCopy
		} else if upstream.InjectDummyThoughtSignature {
			// 给空签名注入 dummy 值（兼容 x666.me 等要求必须有该字段的 API）
			reqCopy := cloneGeminiRequest(geminiReq)
			ensureThoughtSignatures(reqCopy)
			reqToUse = reqCopy
		}
		// else: 默认直接透传，不做任何修改

		requestBody, err = json.Marshal(reqToUse)
		if err != nil {
			return nil, err
		}
		url = forwarding.BuildGeminiNativeURL(baseURL, mappedModel, isStream)
	}

	authKind := forwarding.AuthKindStandard
	platformHeaders := map[string]string(nil)
	switch upstream.ServiceType {
	case "gemini":
		authKind = forwarding.AuthKindGemini
	case "claude":
		platformHeaders = map[string]string{"anthropic-version": "2023-06-01"}
	default:
		authKind = forwarding.AuthKindGemini
	}

	prepared, err := forwarding.Build(c, forwarding.ForwardingRequest{
		Method:          http.MethodPost,
		URL:             url,
		Body:            requestBody,
		ContentType:     "application/json",
		ServiceType:     upstream.ServiceType,
		PlatformHeaders: platformHeaders,
		CustomHeaders:   upstream.CustomHeaders,
		AuthKind:        authKind,
		APIKey:          apiKey,
	})
	if err != nil {
		return nil, err
	}
	return prepared.Request, nil
}

func buildGeminiNativeRequestBody(c *gin.Context, geminiReq *types.GeminiRequest, upstream *config.UpstreamConfig) ([]byte, error) {
	bodyBytes, ok := geminiRequestBodyFromContext(c)
	if !ok {
		var err error
		bodyBytes, err = json.Marshal(geminiReq)
		if err != nil {
			return nil, err
		}
	}

	if !upstream.StripThoughtSignature && !upstream.InjectDummyThoughtSignature {
		return append([]byte(nil), bodyBytes...), nil
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return nil, err
	}
	patchGeminiThoughtSignatures(raw, upstream)
	return json.Marshal(raw)
}

func geminiRequestBodyFromContext(c *gin.Context) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	raw, exists := c.Get("requestBodyBytes")
	if !exists {
		return nil, false
	}
	bodyBytes, ok := raw.([]byte)
	if !ok || len(bodyBytes) == 0 {
		return nil, false
	}
	return bodyBytes, true
}

func patchGeminiThoughtSignatures(body map[string]interface{}, upstream *config.UpstreamConfig) {
	contents, ok := body["contents"].([]interface{})
	if !ok {
		return
	}
	for _, rawContent := range contents {
		content, ok := rawContent.(map[string]interface{})
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]interface{})
		if !ok {
			continue
		}
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]interface{})
			if !ok {
				continue
			}
			if _, ok := part["functionCall"].(map[string]interface{}); !ok {
				continue
			}
			if upstream.StripThoughtSignature {
				delete(part, "thoughtSignature")
				delete(part, "thought_signature")
				if functionCall, ok := part["functionCall"].(map[string]interface{}); ok {
					delete(functionCall, "thoughtSignature")
					delete(functionCall, "thought_signature")
				}
				continue
			}
			if upstream.InjectDummyThoughtSignature && !geminiPartHasThoughtSignature(part) {
				part["thoughtSignature"] = types.DummyThoughtSignature
			}
		}
	}
}

func geminiPartHasThoughtSignature(part map[string]interface{}) bool {
	for _, key := range []string{"thoughtSignature", "thought_signature"} {
		if value, ok := part[key].(string); ok && value != "" {
			return true
		}
	}
	functionCall, ok := part["functionCall"].(map[string]interface{})
	if !ok {
		return false
	}
	for _, key := range []string{"thoughtSignature", "thought_signature"} {
		if value, ok := functionCall[key].(string); ok && value != "" {
			return true
		}
	}
	return false
}

// handleSuccess 处理成功的响应
func handleSuccess(
	c *gin.Context,
	resp *http.Response,
	upstream *config.UpstreamConfig,
	apiKey string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	geminiReq *types.GeminiRequest,
	model string,
	isStream bool,
) (*types.Usage, error) {
	defer func() { _ = resp.Body.Close() }()
	upstreamType := upstream.ServiceType

	if isStream {
		return handleStreamSuccess(c, resp, upstream, apiKey, envCfg, startTime, model)
	}

	// 非流式响应处理
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(500, types.GeminiError{
			Error: types.GeminiErrorDetail{
				Code:    500,
				Message: "Failed to read response",
				Status:  "INTERNAL",
			},
		})
		return nil, err
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Gemini-Timing] 响应完成: %dms, 状态: %d", responseTime, resp.StatusCode)
	}

	// 根据上游类型转换响应
	var geminiResp *types.GeminiResponse

	switch upstreamType {
	case "gemini":
		// 直接解析 Gemini 响应
		if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
			preview := bodyBytes
			if len(preview) > 100 {
				preview = preview[:100]
			}
			log.Printf("[Gemini-InvalidBody] 响应体解析失败: %v, body前100字节: %s", err, preview)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}
		utils.ForwardResponseHeaders(resp.Header, c.Writer)
		c.Data(resp.StatusCode, "application/json", bodyBytes)
		return common.GeminiUsageFromMetadata(geminiResp.UsageMetadata), nil

	case "claude":
		// 转换 Claude 响应为 Gemini 格式
		var claudeResp map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &claudeResp); err != nil {
			preview := bodyBytes
			if len(preview) > 100 {
				preview = preview[:100]
			}
			log.Printf("[Gemini-InvalidBody] Claude响应体解析失败: %v, body前100字节: %s", err, preview)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}
		geminiResp, err = converters.ClaudeResponseToGemini(claudeResp)
		if err != nil {
			log.Printf("[Gemini-InvalidBody] Claude响应转换失败: %v", err)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}

	case "openai":
		// 转换 OpenAI 响应为 Gemini 格式
		var openaiResp map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &openaiResp); err != nil {
			preview := bodyBytes
			if len(preview) > 100 {
				preview = preview[:100]
			}
			log.Printf("[Gemini-InvalidBody] OpenAI响应体解析失败: %v, body前100字节: %s", err, preview)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}
		geminiResp, err = converters.OpenAIResponseToGemini(openaiResp)
		if err != nil {
			log.Printf("[Gemini-InvalidBody] OpenAI响应转换失败: %v", err)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}

	case "responses":
		// 转换 Responses 响应为 Gemini 格式
		var responsesResp map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &responsesResp); err != nil {
			preview := bodyBytes
			if len(preview) > 100 {
				preview = preview[:100]
			}
			log.Printf("[Gemini-InvalidBody] Responses响应体解析失败: %v, body前100字节: %s", err, preview)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}
		geminiResp, err = converters.ResponsesResponseToGemini(responsesResp)
		if err != nil {
			log.Printf("[Gemini-InvalidBody] Responses响应转换失败: %v", err)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}

	default:
		// 默认直接透传，避免非必要整包读入内存
		return nil, common.PassthroughResponse(c, resp)
	}

	// 返回 Gemini 格式响应
	respBytes, err := json.Marshal(geminiResp)
	if err != nil {
		c.Data(resp.StatusCode, "application/json", bodyBytes)
		return nil, nil
	}

	c.Data(resp.StatusCode, "application/json", respBytes)

	// 提取 usage 统计
	var usage *types.Usage
	if geminiResp.UsageMetadata != nil {
		usage = common.GeminiUsageFromMetadata(geminiResp.UsageMetadata)
	}

	return usage, nil
}

// handleAllChannelsFailed 处理所有渠道失败的情况
func handleAllChannelsFailed(c *gin.Context, failoverErr *common.FailoverError, lastError error) {
	if failoverErr != nil {
		c.Data(failoverErr.Status, "application/json", failoverErr.Body)
		return
	}

	errMsg := "All channels failed"
	if lastError != nil {
		errMsg = lastError.Error()
	}

	c.JSON(503, types.GeminiError{
		Error: types.GeminiErrorDetail{
			Code:    503,
			Message: errMsg,
			Status:  "UNAVAILABLE",
		},
	})
}

// handleAllKeysFailed 处理所有 Key 失败的情况
func handleAllKeysFailed(c *gin.Context, failoverErr *common.FailoverError, lastError error) {
	if failoverErr != nil {
		c.Data(failoverErr.Status, "application/json", failoverErr.Body)
		return
	}

	errMsg := "All API keys failed"
	if lastError != nil {
		errMsg = lastError.Error()
	}

	c.JSON(503, types.GeminiError{
		Error: types.GeminiErrorDetail{
			Code:    503,
			Message: errMsg,
			Status:  "UNAVAILABLE",
		},
	})
}

// ----------------------------------------------------------------------------
// PR3 T8d: pipeline 路由 helper（与 messages/chat/responses handler 同模板）
// ----------------------------------------------------------------------------

var (
	globalUsageStore  usage.UsageStore
	globalPriceLoader *pricing.Loader
)

// SetGlobalDependencies 由 main.go 在启动阶段调用，注入 usage/pricing。
// 测试可通过同一函数注入 noop 实现，避免 nil 依赖。
func SetGlobalDependencies(store usage.UsageStore, loader *pricing.Loader) {
	globalUsageStore, globalPriceLoader = store, loader
}

// ginInjectingInbound 装饰 gemini.NewInbound：在 TransformRequest 阶段把
// gin.Context 写入 llm.Request.Metadata，并立即驱动 LBOutboundAdapter
// 选定首个 channel/key（pipeline 主循环只在错误后才会调 NextChannel）。
type ginInjectingInbound struct {
	Inner pipeline.Inbound
	Gin   *gin.Context
	LB    *wire.LBOutboundAdapter
}

func (g *ginInjectingInbound) Format() llm.Format { return g.Inner.Format() }

func (g *ginInjectingInbound) TransformRequest(ctx context.Context, req *http.Request, body []byte) (*llm.Request, error) {
	llmReq, err := g.Inner.TransformRequest(ctx, req, body)
	if err != nil {
		return nil, err
	}
	adapters.SetGinContext(llmReq, g.Gin)
	g.LB.Request = llmReq
	if e := g.LB.NextChannel(ctx); e != nil {
		return nil, e
	}
	return llmReq, nil
}

func (g *ginInjectingInbound) TransformResponse(ctx context.Context, resp *llm.Response) ([]byte, http.Header, error) {
	return g.Inner.TransformResponse(ctx, resp)
}

func (g *ginInjectingInbound) TransformStream(ctx context.Context, in llm.Stream[*llm.Response]) llm.Stream[*llm.StreamEvent] {
	return g.Inner.TransformStream(ctx, in)
}

// processViaPipeline 是 PR3 T8d 把 gemini handler 切流量到 pipeline.Process
// 的入口；single-channel / multi-channel 共享同一份代码。
//
// 与其他 handler 的差异：
//   - 用 GetGeminiMetricsManager / ChannelKindGemini；
//   - common.SetLBAPIType(c, "gemini")；
//   - attemptInfo.APIType = "Gemini"；
//   - parsedModel 由 URL path 提取（gemini 是 /v1beta/models/{model}:action 形式）；
//   - isStream 来自 URL path 的 :streamGenerateContent 后缀（已由 handler 入口
//     提取后传入）。
func processViaPipeline(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	geminiReq *types.GeminiRequest,
	bodyBytes []byte,
	userID, parsedModel string,
	isStream bool,
	routePrefix string,
	requestStart time.Time,
) {
	mm := channelScheduler.GetGeminiMetricsManager()
	common.SetLBAPIType(c, "gemini")
	common.SetLBMetricsManager(c, mm)

	if requestStart.IsZero() {
		requestStart = time.Now()
	}
	attemptInfo := &pipelinemw.AttemptInfo{APIType: "Gemini"}
	ctx := pipelinemw.WithAttemptInfo(c.Request.Context(), attemptInfo)

	lb := wire.NewLBOutboundAdapter(NewOutbound(), channelScheduler, scheduler.ChannelKindGemini, requestStart)
	lb.AttemptInfo = attemptInfo
	lb.LBMetricsMgr = mm
	lb.UsageStore = globalUsageStore
	lb.PriceLoader = globalPriceLoader
	lb.UserID = userID
	lb.Model = parsedModel
	lb.RoutePrefix = routePrefix
	outbound, _ := lb.Inner.(*outboundAdapter)

	inbound := &ginInjectingInbound{Inner: NewInbound(), Gin: c, LB: lb}

	exec := pipeline.ExecutorFunc(func(_ context.Context, req *http.Request) (*http.Response, error) {
		var upstream *config.UpstreamConfig
		if attemptInfo != nil {
			upstream = attemptInfo.Upstream
		}
		return common.SendRequest(req, upstream, envCfg, isStream, "Gemini")
	})

	p := pipeline.NewFactory(exec).Pipeline(inbound, lb, wire.BuildPipelineOpts(cfgManager, lb)...)
	result, err := p.Process(ctx, c.Request, bodyBytes)

	durationMs := time.Since(requestStart).Milliseconds()
	var errCode string
	if err != nil {
		errCode = pipelineErrCode(err)
	}
	// llmUsage 用 defer-closure 捕获指针：stream 路径在事件流消费完成后通过
	// outbound.TakeStreamUsage 把累计 SSE usage 注入；非 stream 路径走
	// result.Response.Usage（与其他 handler 同模板）。
	var llmUsage llm.Usage
	if result != nil && result.Response != nil && result.Response.Usage != nil {
		llmUsage = *result.Response.Usage
	}
	defer func() {
		if outbound != nil {
			if streamUsage := outbound.TakeStreamUsage(); streamUsage != nil && llmUsage.IsZero() {
				llmUsage = *streamUsage
			}
		}
		lb.Finalize(ctx, err == nil, errCode, parsedModel, llmUsage, durationMs)
	}()

	if err != nil {
		writePipelineError(c, err)
		return
	}
	if result == nil {
		return
	}

	if result.Stream && result.EventStream != nil {
		writePipelineStream(c, result.EventStream)
		return
	}
	if result.Response != nil {
		writePipelineResponse(c, result.Response)
	}
	// 静默引用，防止未使用 import 报错（geminiReq 仅作为 inbound 已解析载体的回退）。
	_ = geminiReq
}

// writePipelineResponse 把非流式 *llm.Response 写回客户端。
func writePipelineResponse(c *gin.Context, resp *llm.Response) {
	if resp == nil {
		return
	}
	if c.Writer.Written() {
		return
	}
	for k, v := range resp.Header {
		if len(v) == 0 {
			continue
		}
		c.Writer.Header().Set(k, v[0])
	}
	if resp.Header.Get("Content-Type") == "" {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	c.Writer.WriteHeader(status)
	_, _ = c.Writer.Write(resp.Body)
}

// writePipelineStream 把 SSE 事件流逐帧写回客户端。
func writePipelineStream(c *gin.Context, stream llm.Stream[*llm.StreamEvent]) {
	defer func() { _ = stream.Close() }()
	if !c.Writer.Written() {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.WriteHeader(http.StatusOK)
	}
	flusher, _ := c.Writer.(http.Flusher)
	for stream.Next() {
		ev := stream.Current()
		if ev == nil || len(ev.Data) == 0 {
			continue
		}
		if _, err := c.Writer.Write(ev.Data); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// writePipelineError 把 pipeline.Process 返回的错误回写客户端。
func writePipelineError(c *gin.Context, err error) {
	if c.Writer.Written() {
		return
	}
	status := http.StatusBadGateway
	gErr := types.GeminiError{
		Error: types.GeminiErrorDetail{
			Code:    status,
			Message: err.Error(),
			Status:  "UNAVAILABLE",
		},
	}
	if errors.Is(err, pipeline.ErrChannelExhausted) {
		gErr.Error.Message = "all channels failed: " + err.Error()
	}
	c.JSON(status, gErr)
}

// pipelineErrCode 将 pipeline 错误归类为简短 error code，写入 UsageRecord。
func pipelineErrCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, pipeline.ErrChannelExhausted):
		return "channel_exhausted"
	case errors.Is(err, pipeline.ErrEmptyResponse):
		return "empty_response"
	case errors.Is(err, common.ErrInvalidResponseBody),
		errors.Is(err, ErrInvalidResponseBody):
		return "invalid_response_body"
	default:
		return "upstream_error"
	}
}

// 编译期保活：旧路径下的 helper 仍保留以便后续被独立单元测试或回滚使用；
// Go 允许导出/未导出函数未被调用，但若 vet / linter 报 unused，会通过这些 _
// 引用通过编译。
var (
	_ = handleSuccess
	_ = handleAllChannelsFailed
	_ = handleAllKeysFailed
	_ = forwarding.AuthKindStandard
	_ = io.EOF
	_ = converters.GeminiToClaudeRequest
	_ = utils.ExtractUnifiedSessionID
)
